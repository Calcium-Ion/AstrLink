package ingress

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier/cloud"
	"github.com/QuantumNous/astrlink/core/internal/classifieroutput"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/modelrouting"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// modelRoutingBindingTTL is how long a routing group's session binding lives,
// or false when session stickiness is off. The routing-settings switch and
// idle timeout that govern API provider bindings govern these too: one
// setting decides whether a session keeps what it was given.
func modelRoutingBindingTTL(settings *contract.RoutingSettings) (time.Duration, bool) {
	if settings == nil || settings.ChannelStickiness == nil || !settings.ChannelStickiness.Enabled || settings.ChannelStickiness.TTLSeconds <= 0 {
		return 0, false
	}
	return time.Duration(settings.ChannelStickiness.TTLSeconds) * time.Second, true
}

// modelRoutingBindingTime is the fixed-width spelling binding times are
// stored in, so string comparison in the store orders them correctly.
func modelRoutingBindingTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
}

type modelRoutingHTTPError struct {
	status  int
	code    string
	message string
}

func (err *modelRoutingHTTPError) Error() string { return err.message }

func (handler *Handler) applyModelRouting(ctx context.Context, session *recordSession, classified Request, settings contract.RoutingSettings) (Request, error) {
	started := time.Now()
	if classified.Model == "" || !strings.HasPrefix(classified.Model, contract.ModelRoutingModelPrefix) || classified.Protocol.IsModelDiscovery() {
		return classified, nil
	}
	if classified.Model == contract.AstrLinkAutoModelID {
		return classified, nil
	}
	groupID := strings.TrimPrefix(classified.Model, contract.ModelRoutingModelPrefix)
	var group *contract.ModelRoutingGroup
	for index := range settings.ModelRoutingGroups {
		if settings.ModelRoutingGroups[index].ID == groupID {
			group = &settings.ModelRoutingGroups[index]
			break
		}
	}
	if group == nil {
		return classified, &modelRoutingHTTPError{status: 422, code: "model_routing_group_not_found", message: fmt.Sprintf("model routing group %q was not found", groupID)}
	}
	// A disabled group serves nothing: not a session bound to it, not a
	// Responses continuation, not a WebSocket opened before it was switched
	// off. Disabling must not depend on how the client sends its next turn.
	if !group.Enabled {
		return classified, &modelRoutingHTTPError{status: 422, code: "model_routing_group_disabled", message: fmt.Sprintf("model routing group %q is disabled", groupID)}
	}
	// One classifier read at most per request, shared by the version check
	// below and the classification further down; a request that is pinned
	// before classification and names no custom dimension makes none.
	classifierSettings := handler.requestClassifierSettings(ctx, session)
	version, err := handler.modelRoutingSnapshotVersion(settings.ModelRoutingGroups, classifierSettings)
	if err != nil {
		return classified, &modelRoutingHTTPError{status: 422, code: "model_routing_group_invalid", message: err.Error()}
	}
	configVersion := handler.modelRoutingGroupVersion(ctx, session, *group, version)
	if turn := responsesWSTurnFromContext(ctx); turn != nil && turn.session.serviceID != "" && turn.session.routingModel != "" {
		for index := range group.Members {
			member := &group.Members[index]
			if member.ServiceID == turn.session.serviceID && member.Model == turn.session.routingModel {
				decision := modelRoutingDecisionFromBinding(contract.ModelRoutingBinding{GroupID: group.ID, MemberID: member.ID, ServiceID: member.ServiceID, Model: member.Model, ConfigSnapshotVersion: version}, classified.Model, version)
				decision.SelectionSource = contract.ModelRoutingSelectionWebSocket
				decision.ConfigVersion = configVersion
				decision.DurationMS = int(time.Since(started).Milliseconds())
				classified.ModelRoutingMember = member
				pinModelRoutingWebSocket(ctx, member.Model)
				if session != nil {
					session.noteModelRouting(ctx, decision)
				}
				return classified, nil
			}
		}
		return classified, &modelRoutingHTTPError{status: 409, code: "model_routing_binding_conflict", message: "Responses WebSocket is bound to a different model routing member"}
	}
	if classified.PreviousResponseID != "" && (classified.Protocol == contract.ProtocolOpenAIResponses || classified.Protocol == contract.ProtocolOpenAIResponsesCompact) {
		affinity, found, affinityErr := handler.loadResponseAffinity(ctx, classified.PreviousResponseID)
		if affinityErr != nil {
			return classified, &modelRoutingHTTPError{status: 409, code: "model_routing_binding_conflict", message: affinityErr.Error()}
		}
		if found {
			var member *contract.ModelRoutingMember
			for index := range group.Members {
				if group.Members[index].ServiceID == affinity.ServiceID && group.Members[index].Model == affinity.UpstreamModel {
					member = &group.Members[index]
					break
				}
			}
			if member == nil {
				return classified, &modelRoutingHTTPError{status: 409, code: "model_routing_binding_conflict", message: "previous response belongs to a different model routing member"}
			}
			decision := modelRoutingDecisionFromBinding(contract.ModelRoutingBinding{GroupID: group.ID, MemberID: member.ID, ServiceID: member.ServiceID, Model: member.Model, ConfigSnapshotVersion: version}, classified.Model, version)
			decision.SelectionSource = contract.ModelRoutingSelectionResponseAffinity
			decision.ConfigVersion = configVersion
			decision.DurationMS = int(time.Since(started).Milliseconds())
			classified.ModelRoutingMember = member
			pinModelRoutingWebSocket(ctx, member.Model)
			if session != nil {
				session.noteModelRouting(ctx, decision)
			}
			return classified, nil
		}
	}
	var bindingReset *contract.ModelRoutingBindingReset
	if binding, ok, bindingErr := handler.loadModelRoutingBinding(ctx, session, classified); bindingErr != nil {
		return classified, &modelRoutingHTTPError{status: 422, code: "model_routing_group_invalid", message: bindingErr.Error()}
	} else if ok && group.HasMember(binding.MemberID, binding.ServiceID, binding.Model) {
		decision := modelRoutingDecisionFromBinding(binding, classified.Model, version)
		decision.ConfigVersion = configVersion
		decision.DurationMS = int(time.Since(started).Milliseconds())
		decision.BindingRequestID = binding.RequestID
		classified.ModelRoutingMember = &contract.ModelRoutingMember{ID: binding.MemberID, ServiceID: binding.ServiceID, Model: binding.Model}
		pinModelRoutingWebSocket(ctx, binding.Model)
		if session != nil {
			session.noteModelRouting(ctx, decision)
		}
		return classified, nil
	} else if ok {
		// The member this session was bound to has left the group. The rules
		// pick again, and the record says why the session's model changed.
		bindingReset = &contract.ModelRoutingBindingReset{Reason: contract.ModelRoutingBindingMemberRemoved, MemberID: binding.MemberID, ServiceID: binding.ServiceID, Model: binding.Model}
		handler.dropModelRoutingBinding(ctx, session, binding)
	}
	classifier := classifierSettings()
	dimensions := classifier.ResolveCallPlan(group).Dimensions
	needsClassification := modelRoutingNeedsClassification(*group, dimensions)
	// Rules read the request's text only through the classifier and keyword
	// conditions; a group with neither never pays for the inspection.
	// Counts reveal none of the text, so they come from the request as sent;
	// only the text itself is read through the privacy policy.
	facts := modelRoutingFacts(classified, session, handler.now())
	view := classified
	if needsClassification || group.ReferencesField("literal_keyword") {
		view = handler.modelRoutingTextView(ctx, session, classified)
		facts.LiteralText = ""
		if view.routingUserTextKnown {
			facts.LiteralText = view.routingUserText
		}
	}
	var taskType modelrouting.TaskTypeOutcome
	if needsClassification {
		taskType = handler.modelRoutingTaskType(ctx, view, *group, classifier)
		facts.TaskType = taskType.Value
		facts.TaskTypeCandidates = modelrouting.TaskTypeCandidateLabels(taskType, *group)
	}
	// Custom dimensions ride on the same attempt as task_type. An empty
	// outcome leaves the map nil, which is the single-dimension configuration:
	// every rule that tests a built-in field evaluates exactly as before.
	facts.Dimensions = modelrouting.DimensionFacts(taskType)
	// Explain selects exactly as Evaluate does and also keeps every rule's own
	// verdict, so the record can show the walk the dry-run preview shows.
	selection, verdicts := modelrouting.Explain(*group, facts)
	// One conversion owns the provenance stamping, so the dry-run preview and
	// this record cannot describe the same attempt differently.
	modelrouting.StampClassifierProvenance(selection.Fields, taskType, *group, selection.TaskTypeCandidateIndex)
	if selection.Member.ID == "" {
		return classified, &modelRoutingHTTPError{status: 422, code: "model_routing_group_invalid", message: "model routing group default member is invalid"}
	}
	decision := contract.ModelRoutingDecision{
		SchemaVersion:         1,
		VirtualModelID:        classified.Model,
		GroupID:               group.ID,
		ConfigSnapshotVersion: version,
		SelectionSource:       selection.SelectionSource,
		RuleID:                selection.RuleID,
		RulePriority:          selection.RulePriority,
		UsedDefault:           selection.UsedDefault,
		Fields:                selection.Fields,
		MemberID:              selection.Member.ID,
		MemberServiceID:       selection.Member.ServiceID,
		MemberModel:           selection.Member.Model,
		FallbackReason:        taskType.Code,
		Execution:             contract.ModelRoutingExecution{Status: contract.ModelRoutingExecutionPending},
		PrivacyAttempts:       []contract.ModelRoutingPrivacyAttempt{},
		ClassifierDurationMS:  taskType.DurationMS,
		DurationMS:            int(time.Since(started).Milliseconds()),
		ConfigVersion:         configVersion,
		Rules:                 modelrouting.RuleVerdicts(verdicts, *group),
		BindingReset:          bindingReset,
	}
	if needsClassification {
		decision.Classifier = modelrouting.ClassifierAnswers(taskType, facts.Dimensions, dimensions)
	}
	if err := decision.Validate(); err != nil {
		return classified, &modelRoutingHTTPError{status: 422, code: "model_routing_group_invalid", message: err.Error()}
	}
	classified.ModelRoutingMember = &contract.ModelRoutingMember{ID: selection.Member.ID, ServiceID: selection.Member.ServiceID, Model: selection.Member.Model}
	pinModelRoutingWebSocket(ctx, selection.Member.Model)
	if session != nil {
		session.noteModelRouting(ctx, decision)
	}
	return classified, nil
}

// modelRoutingSnapshotVersion validates and versions the whole routing
// configuration against the dimension set the classifier settings declare. A
// rule may only name a user-defined dimension when that dimension is actually
// declared, and the group documents never carry the dimensions, so the check
// needs the classifier settings as soon as any group, not only the requested
// one, has a dimension rule. The requested group is one of the groups, so its
// own validation is part of this.
//
// The cheap built-in-only pass runs first and is the whole check while no rule
// names a custom dimension. Only a configuration that fails it pays for the
// classifier read, and the read is skipped entirely when classifier settings
// are not wired (a build without the classifier sub-resource), where an
// undeclared dimension correctly stays unsupported. Resolving the dimensions
// unconditionally would add a store read to the hot path of every pinned
// request for the benefit of a document that was already validated on save.
func (handler *Handler) modelRoutingSnapshotVersion(groups []contract.ModelRoutingGroup, classifierSettings func() contract.ClassifierSettings) (string, error) {
	version, err := contract.ModelRoutingSnapshotVersion(groups)
	if err == nil || handler.classifierSettings == nil {
		return version, err
	}
	dimensions := classifierSettings().Dimensions
	if len(dimensions) == 0 {
		return "", err
	}
	return contract.ModelRoutingSnapshotVersionWithDimensions(groups, dimensions)
}

// requestClassifierSettings returns a reader that loads the classifier
// settings on first use and hands every later caller in the same request the
// same document.
func (handler *Handler) requestClassifierSettings(ctx context.Context, session *recordSession) func() contract.ClassifierSettings {
	var loaded *contract.ClassifierSettings
	return func() contract.ClassifierSettings {
		if loaded == nil {
			settings := handler.loadClassifierSettings(ctx, session)
			loaded = &settings
		}
		return *loaded
	}
}

func (handler *Handler) resolveMemberCandidates(ctx context.Context, request endpoint.ResolveRequest, member contract.ModelRoutingMember) ([]endpoint.Resolved, error) {
	request.Model = member.Model
	request.AllCandidates = true
	if resolver, ok := handler.resolver.(endpoint.ExactTargetResolver); ok {
		return resolver.ResolveMemberCandidates(ctx, request, member.ServiceID, member.Model)
	}
	candidates, err := handler.resolveCandidates(ctx, request)
	if err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		if candidate.CanonicalService().ID == member.ServiceID && (candidate.UpstreamModel == "" || candidate.UpstreamModel == member.Model) {
			candidate.UpstreamModel = member.Model
			return []endpoint.Resolved{candidate}, nil
		}
	}
	return nil, endpoint.ErrNoEndpoint
}

func cloneModelRoutingFields(fields []contract.ModelRoutingFieldObservation) []contract.ModelRoutingFieldObservation {
	if fields == nil {
		return nil
	}
	return append([]contract.ModelRoutingFieldObservation{}, fields...)
}

func cloneModelRoutingPrivacyAttempts(attempts []contract.ModelRoutingPrivacyAttempt) []contract.ModelRoutingPrivacyAttempt {
	if attempts == nil {
		return nil
	}
	return append([]contract.ModelRoutingPrivacyAttempt{}, attempts...)
}

// noteModelRoutingPrivacyAttempt lists one provider attempt's privacy check
// in the routing decision. The check runs before the attempt opens, so it
// belongs to the attempt after the last one counted.
func (session *recordSession) noteModelRoutingPrivacyAttempt(serviceID contract.ServiceID, decision contract.ModelRoutingPrivacyDecision, findings, durationMS int, code string) {
	if session == nil || session.modelRouting == nil {
		return
	}
	if len(session.modelRouting.PrivacyAttempts) >= maxModelRoutingPrivacyAttempts {
		return
	}
	if findings < 0 {
		findings = 0
	}
	if durationMS < 0 {
		durationMS = 0
	}
	session.modelRouting.PrivacyAttempts = append(session.modelRouting.PrivacyAttempts, contract.ModelRoutingPrivacyAttempt{AttemptIndex: session.attemptIndex + 1, ServiceID: serviceID, Decision: decision, FindingCount: findings, DurationMS: durationMS, ErrorCode: code})
}

// maxModelRoutingPrivacyAttempts is the most attempts a decision may list;
// the contract rejects a longer list, which would drop the whole record.
const maxModelRoutingPrivacyAttempts = 64

// modelRoutingTextView is the request as the classifier and keyword
// conditions read it. Under an active privacy policy its text views are
// rebuilt from the redacted body, so a classifier outside this machine never
// receives what the policy keeps from an upstream. A body the policy blocks,
// or an inspection that fails, leaves routing no text: rules that need it
// fall back as if classification were unavailable, and the provider attempt
// still meets the policy on its own. Nothing here is recorded; each provider
// attempt inspects the body it sends and reports that.
func (handler *Handler) modelRoutingTextView(ctx context.Context, session *recordSession, classified Request) Request {
	if handler.privacyFilter == nil {
		return classified
	}
	accessTokenID, _ := AccessTokenIDFromContext(ctx)
	policy, err := handler.privacyFilter.ResolvePolicy(ctx, privacy.Scope{
		Protocol:      classified.Protocol,
		Model:         classified.Model,
		AccessTokenID: accessTokenID,
	})
	if err != nil {
		return classifiedWithSanitizedText(classified, nil)
	}
	if !policy.Enabled {
		return classified
	}
	if session != nil {
		// The same conversation names a value the same way the provider
		// attempt will, so a classifier sees the placeholders the upstream sees.
		ctx = privacy.WithConversation(ctx, string(session.sessionID))
	}
	result, err := handler.privacyFilter.Inspect(ctx, policy, classified.Protocol, classified.routingBody)
	if err != nil {
		return classifiedWithSanitizedText(classified, nil)
	}
	switch result.Decision {
	case privacy.DecisionAllow, privacy.DecisionWarn:
		return classified
	case privacy.DecisionRedact:
		return classifiedWithSanitizedText(classified, result.Body)
	default:
		return classifiedWithSanitizedText(classified, nil)
	}
}

func (session *recordSession) updateModelRoutingExecution(status contract.ModelRoutingExecutionStatus, code string) {
	if session == nil || session.modelRouting == nil {
		return
	}
	session.modelRouting.Execution.Status = status
	session.modelRouting.Execution.AttemptCount = max(session.attemptIndex, 0)
	session.modelRouting.Execution.ErrorCode = code
	session.modelRouting.ErrorCode = code
}

func (handler *Handler) persistModelRoutingBinding(ctx context.Context, session *recordSession) {
	if handler == nil || session == nil || session.modelRouting == nil || session.status != contract.RequestStatusSucceeded || session.sessionID == "" || handler.requestRecords == nil {
		return
	}
	ttl, enabled := modelRoutingBindingTTL(session.routingSettings)
	if !enabled {
		return
	}
	store, ok := handler.requestRecords.(storage.ModelRoutingBindingStore)
	if !ok {
		return
	}
	principal, ok := modelRoutingBindingPrincipal(session)
	if !ok {
		// Without a stable access-token principal, do not create a shared
		// anonymous namespace that could make same-session IDs cross-talk.
		return
	}
	decision := session.modelRouting
	// A reused binding keeps pointing at the request whose rules chose the
	// member; any other decision is its own origin.
	origin := string(session.id)
	if decision.SelectionSource == contract.ModelRoutingSelectionGroupBinding {
		origin = decision.BindingRequestID
	}
	now := time.Now().UTC()
	expires := modelRoutingBindingTime(now.Add(ttl))
	binding := contract.ModelRoutingBinding{
		Principal: principal, SessionID: session.sessionID, Protocol: session.classified.Protocol,
		VirtualModelID: decision.VirtualModelID, GroupID: decision.GroupID,
		MemberID: decision.MemberID, ServiceID: decision.MemberServiceID,
		Model: decision.MemberModel, ConfigSnapshotVersion: decision.ConfigSnapshotVersion, RequestID: origin,
		CreatedAt: modelRoutingBindingTime(now), UpdatedAt: modelRoutingBindingTime(now), ExpiresAt: expires,
	}
	// A client may hang up as soon as the response is delivered — Codex does
	// on the terminal event — so the write must not depend on its connection,
	// as the provider binding and response affinity do not.
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)
	defer cancel()
	if err := store.UpsertModelRoutingBinding(persist, binding, session.startedAt); err != nil {
		if session.persistLogf != nil {
			session.persistLogf("model routing binding write failed: %v", err)
		}
		return
	}
	decision.BindingExpiresAt = expires
}

func (handler *Handler) loadModelRoutingBinding(ctx context.Context, session *recordSession, classified Request) (contract.ModelRoutingBinding, bool, error) {
	if session == nil || session.sessionID == "" || handler.requestRecords == nil {
		return contract.ModelRoutingBinding{}, false, nil
	}
	if _, enabled := modelRoutingBindingTTL(session.routingSettings); !enabled {
		return contract.ModelRoutingBinding{}, false, nil
	}
	store, ok := handler.requestRecords.(storage.ModelRoutingBindingStore)
	if !ok {
		return contract.ModelRoutingBinding{}, false, nil
	}
	principal, ok := modelRoutingBindingPrincipal(session)
	if !ok {
		// Without a stable access-token principal, never read another caller's
		// binding from a shared anonymous namespace.
		return contract.ModelRoutingBinding{}, false, nil
	}
	binding, err := store.GetModelRoutingBinding(ctx, principal, session.sessionID, classified.Protocol, classified.Model)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return contract.ModelRoutingBinding{}, false, nil
		}
		return contract.ModelRoutingBinding{}, false, err
	}
	return binding, true, nil
}

// dropModelRoutingBinding forgets a binding the group can no longer honor.
// Failing to delete it is harmless: the next request finds the same missing
// member and judges the rules again.
func (handler *Handler) dropModelRoutingBinding(ctx context.Context, session *recordSession, binding contract.ModelRoutingBinding) {
	store, ok := handler.requestRecords.(storage.ModelRoutingBindingStore)
	if !ok {
		return
	}
	if err := store.DeleteModelRoutingBinding(ctx, binding.Principal, binding.SessionID, binding.Protocol, binding.VirtualModelID); err != nil && session != nil && session.persistLogf != nil {
		session.persistLogf("model routing binding delete failed: %v", err)
	}
}

// modelRoutingVersions remembers which saved version a group had under each
// settings snapshot. Snapshots change only when settings are saved, so after
// the first request under a configuration, labeling a record costs no hashing
// and no database read. The bound keeps a long-running Core from collecting
// one entry per save forever.
type modelRoutingVersions struct {
	mu      sync.Mutex
	entries map[string]int
}

const maxModelRoutingVersionEntries = 256

func (versions *modelRoutingVersions) get(key string) (int, bool) {
	versions.mu.Lock()
	defer versions.mu.Unlock()
	version, ok := versions.entries[key]
	return version, ok
}

func (versions *modelRoutingVersions) put(key string, version int) {
	versions.mu.Lock()
	defer versions.mu.Unlock()
	if versions.entries == nil || len(versions.entries) >= maxModelRoutingVersionEntries {
		versions.entries = make(map[string]int)
	}
	versions.entries[key] = version
}

// modelRoutingGroupVersion reads the saved configuration version the group is
// routed under. The number only labels the record, so a store that cannot
// answer leaves it at zero rather than failing the request. snapshot is the
// whole configuration's content version, already computed for the request.
func (handler *Handler) modelRoutingGroupVersion(ctx context.Context, session *recordSession, group contract.ModelRoutingGroup, snapshot string) int {
	store, ok := handler.requestRecords.(storage.ModelRoutingVersionStore)
	if !ok {
		return 0
	}
	key := group.ID + "\x00" + snapshot
	if version, ok := handler.routingVersions.get(key); ok {
		return version
	}
	version, err := store.EnsureModelRoutingGroupVersion(ctx, group)
	if err != nil {
		if session != nil && session.persistLogf != nil {
			session.persistLogf("model routing version lookup failed: %v", err)
		}
		return 0
	}
	handler.routingVersions.put(key, version)
	return version
}

func modelRoutingBindingPrincipal(session *recordSession) (string, bool) {
	if session == nil || session.accessTokenID == nil || *session.accessTokenID == "" {
		return "", false
	}
	return string(*session.accessTokenID), true
}

func modelRoutingDecisionFromBinding(binding contract.ModelRoutingBinding, virtualModelID, version string) contract.ModelRoutingDecision {
	if binding.ConfigSnapshotVersion != "" {
		version = binding.ConfigSnapshotVersion
	}
	return contract.ModelRoutingDecision{
		SchemaVersion: 1, VirtualModelID: virtualModelID, GroupID: binding.GroupID,
		ConfigSnapshotVersion: version, SelectionSource: contract.ModelRoutingSelectionGroupBinding,
		UsedDefault: false, Fields: []contract.ModelRoutingFieldObservation{}, MemberID: binding.MemberID,
		MemberServiceID: binding.ServiceID, MemberModel: binding.Model,
		Execution:       contract.ModelRoutingExecution{Status: contract.ModelRoutingExecutionPending},
		PrivacyAttempts: []contract.ModelRoutingPrivacyAttempt{},
	}
}

func modelRoutingFacts(classified Request, session *recordSession, now time.Time) contract.ModelRoutingFacts {
	facts := contract.ModelRoutingFacts{
		Protocol:     string(classified.Protocol),
		Streaming:    routingBoolPtr(classified.Streaming),
		Continuation: routingBoolPtr(classified.PreviousResponseID != ""),
	}
	// Keyword conditions and the character count read the user's latest
	// message, the text the rule editor and the dry-run speak of; the protocol
	// envelope around it would match field names and miss escaped text.
	if classified.routingUserTextKnown {
		facts.InputTextRunes = routingIntPtr(classified.routingUserTextRunes)
		facts.LiteralText = classified.routingUserText
	}
	// Facts read from the body's structure stay unknown when the body could
	// not be read, so a rule on them neither matches nor misfires on zeros.
	if shape := classified.shape; shape.known {
		facts.MediaKind = shape.mediaKind()
		facts.HasTools = routingBoolPtr(shape.toolDeclarations > 0)
		facts.HasMedia = routingBoolPtr(shape.image || shape.audio || shape.video)
		facts.HasImage = routingBoolPtr(shape.image)
		facts.HasAudio = routingBoolPtr(shape.audio)
		facts.HasVideo = routingBoolPtr(shape.video)
		facts.AssistantMessageCount = routingIntPtr(shape.assistantMessages)
		facts.ToolMessageCount = routingIntPtr(shape.toolMessages)
		facts.ToolDeclarationCount = routingIntPtr(shape.toolDeclarations)
		facts.ToolCallCount = routingIntPtr(shape.toolCalls)
		facts.InputBytes = routingIntPtr(shape.inputBytes)
		facts.EstimatedInputContextTokens = routingIntPtr(shape.estimatedTokens)
	}
	// One clock sample per request: system_time leaves read this instant, in
	// the OS local timezone, so all rows in one evaluation see the same time.
	facts.SystemTime = &now
	facts.UserMessageCount = routingIntPtr(classified.Conversation.UserTurnCount)
	if session != nil && session.turn != nil {
		facts.TurnCount = routingIntPtr(session.turn.Index)
	}
	return facts
}

// loadClassifierSettings reads the external classifier configuration. Any
// failure degrades to the empty (disabled) settings so classification stays
// fail-open: an unreadable classifier must never block routing.
func (handler *Handler) loadClassifierSettings(ctx context.Context, session *recordSession) contract.ClassifierSettings {
	if handler.classifierSettings == nil {
		return contract.ClassifierSettings{}
	}
	settings, err := handler.classifierSettings.GetClassifierSettings(ctx)
	if err != nil {
		if session != nil && session.persistLogf != nil {
			session.persistLogf("classifier settings lookup failed: %v", err)
		}
		return contract.ClassifierSettings{}
	}
	return settings
}

// TaskTypeOutcome lives in the modelrouting package next to the one
// conversion (TaskTypeCandidateLabels / TaskTypeCandidateObservations) the
// inference path and the control-plane dry-run share; every classification
// source below fills that carrier and never derives facts on its own.
func (handler *Handler) modelRoutingTaskType(ctx context.Context, classified Request, group contract.ModelRoutingGroup, settings contract.ClassifierSettings) modelrouting.TaskTypeOutcome {
	// The classification input is the user's latest message as routing reads
	// it, never the protocol envelope. A request built without that view (the
	// retired auto path) falls back to its classifier text.
	text := classified.routingUserText
	if !classified.routingUserTextKnown {
		text = classified.lastUserText
	}
	text, ok := classificationText(text)
	if !ok {
		return modelrouting.TaskTypeOutcome{Code: autoclassifier.FallbackUnavailable}
	}
	if settings.CloudConfigured() {
		return handler.modelRoutingTaskTypeCloud(ctx, text, group, settings)
	}
	if settings.ExistingModelConfigured() {
		outcome, _ := handler.modelRoutingTaskTypeExistingModel(ctx, text, &group, settings)
		return outcome
	}
	if handler.classifier == nil {
		return modelrouting.TaskTypeOutcome{Code: autoclassifier.FallbackUnavailable}
	}
	budget := 300 * time.Millisecond
	if classified.Streaming {
		budget = 100 * time.Millisecond
	}
	start := time.Now()
	classifyCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var outcome autoclassifier.Outcome
	if tryClassifier, ok := handler.classifier.(interface {
		TryClassify(context.Context, string) autoclassifier.Outcome
	}); ok {
		outcome = tryClassifier.TryClassify(classifyCtx, text)
	} else {
		outcome = handler.classifier.Classify(classifyCtx, text)
	}
	result := modelrouting.TaskTypeOutcome{DurationMS: int(time.Since(start).Milliseconds()), ClassifierID: string(outcome.ClassifierID), Candidates: outcome.Candidates}
	if outcome.OK() {
		if group.ValidTaskTypeLabel(outcome.Category) {
			result.Value = outcome.Category
			result.Confidence = outcome.Confidence
			return result
		}
		result.Code = "classifier_invalid_output"
		return result
	}
	if outcome.FallbackReason == autoclassifier.FallbackBusy {
		result.Code = "classifier_busy"
		return result
	}
	if errors.Is(classifyCtx.Err(), context.DeadlineExceeded) || outcome.FallbackReason == autoclassifier.FallbackTimeout {
		result.Code = "classifier_timeout"
		return result
	}
	if outcome.FallbackReason == autoclassifier.FallbackInvalidText {
		result.Code = "classifier_invalid_output"
		return result
	}
	result.Code = "classifier_unavailable"
	return result
}

// classificationText bounds what every classifier source is given: a blank
// text has nothing to classify, and a longer one is cut to 64 KiB.
func classificationText(text string) (string, bool) {
	if strings.TrimSpace(text) == "" {
		return "", false
	}
	if len(text) > 64*1024 {
		text = text[:64*1024]
	}
	return text, true
}

// ExistingModelTaskType classifies text through the existing_model source the
// way a live request does: the same bounds on the text, instruction, call,
// strict parsing and failure codes. The classifier dry-run calls it, so a
// preview cannot classify differently from production, and its standalone
// call keeps no request record. group may be nil; the labels then come from
// the classifier settings alone. The second result is the score the model
// gave its label, reported even below the threshold.
func (handler *Handler) ExistingModelTaskType(ctx context.Context, text string, group *contract.ModelRoutingGroup, settings contract.ClassifierSettings) (modelrouting.TaskTypeOutcome, float64) {
	text, ok := classificationText(text)
	if !ok || !settings.ExistingModelConfigured() {
		return modelrouting.TaskTypeOutcome{Code: autoclassifier.FallbackUnavailable}, 0
	}
	return handler.modelRoutingTaskTypeExistingModel(ctx, text, group, settings)
}

// modelRoutingTaskTypeExistingModel classifies through an existing gateway
// model: one bounded in-process generation call, then the shared strict
// extraction core. Every failure maps to a fallback code; classification
// never blocks the request (fail-open is not negotiable). A nil group takes
// its labels from the classifier settings. The second result is the score the
// model gave its label, set on low confidence too, where the outcome leaves
// the label unknown.
func (handler *Handler) modelRoutingTaskTypeExistingModel(ctx context.Context, text string, group *contract.ModelRoutingGroup, settings contract.ClassifierSettings) (modelrouting.TaskTypeOutcome, float64) {
	plan := settings.ResolveCallPlan(group)
	result := modelrouting.TaskTypeOutcome{
		ClassifierID:           "existing_model",
		LowConfidenceThreshold: plan.LowConfidenceThreshold,
	}
	// The rendered classifier prompt opens the instruction and the answer
	// format closes it, so no stored prompt can drop the shape the strict
	// parser depends on.
	instruction, err := classifieroutput.ExistingModelInstruction(plan)
	if err != nil {
		// Unreachable with a validated group (the label snapshot is never
		// empty), but a broken catalog must still fail open, not block.
		result.Code = "classifier_unavailable"
		return result, 0
	}
	start := time.Now()
	assistantText, err := handler.ClassifyModel(ctx, settings.ExistingModel.ServiceID, settings.ExistingModel.Model, instruction, text, plan.TimeoutMS)
	result.DurationMS = int(time.Since(start).Milliseconds())
	if err != nil {
		result.Code = existingModelFailureCode(ctx, err)
		return result, 0
	}
	var parsed classifieroutput.TaskType
	if len(plan.Dimensions) == 0 {
		// Single dimension: the reply is the flat {label, score, candidates}
		// document it has always been, and this branch is the pre-dimensions
		// path verbatim.
		if parsed, err = classifieroutput.ParseTaskType(assistantText, plan.Labels); err != nil {
			if errors.Is(err, classifieroutput.ErrRefusal) {
				result.Code = "classifier_refusal"
				return result, 0
			}
			result.Code = "classifier_invalid_output"
			return result, 0
		}
	} else {
		// Multi dimension: one envelope keyed by dimension name, task_type
		// among them. A malformed dimension makes the reply unusable, exactly
		// as a malformed task_type would — the answer is one document, and
		// keeping the readable half would route on a partial reading.
		dimensions, dimensionErr := classifieroutput.ParseDimensions(assistantText, plan.Labels, plan.Dimensions)
		if dimensionErr != nil {
			if errors.Is(dimensionErr, classifieroutput.ErrRefusal) {
				result.Code = "classifier_refusal"
				return result, 0
			}
			result.Code = "classifier_invalid_output"
			return result, 0
		}
		taskType, ok := dimensions[contract.BuiltInTaskTypeDimension]
		if !ok {
			// Every custom dimension was answered but task_type was not. The
			// routing decision still needs a task_type, so this is the same
			// unusable reply as a malformed one.
			result.Code = "classifier_invalid_output"
			return result, 0
		}
		result.Dimensions = dimensionScores(dimensions)
		// Carry the ranked sequence through: the evaluator's scored-fallback
		// substitution reads it, so dropping it here would disable substitution
		// for every group as soon as one custom dimension was configured.
		parsed = classifieroutput.TaskType{Label: taskType.Label, Score: taskType.Score, Candidates: taskType.Candidates}
	}
	if group != nil && !group.ValidTaskTypeLabel(parsed.Label) {
		result.Code = "classifier_invalid_output"
		return result, 0
	}
	if parsed.Score < plan.LowConfidenceThreshold {
		// Low confidence is not a failure and never takes a fallback: this
		// task_type stays unknown and threshold filtering empties its
		// candidates. The custom dimensions parsed from the same reply are
		// not affected — each is judged against the threshold on its own
		// score (modelrouting.DimensionFacts), and the recorded code below
		// tells that apart from a real fallback.
		result.Code = modelrouting.CodeClassifierLowConfidence
		result.Candidates = autoclassifierCandidates(parsed.Candidates)
		return result, parsed.Score
	}
	result.Value = parsed.Label
	result.Confidence = parsed.Score
	result.Candidates = autoclassifierCandidates(parsed.Candidates)
	return result, parsed.Score
}

// dimensionScores moves the strict extraction core's parsed dimensions onto
// the shared Outcome carrier, dropping the built-in task_type: it already
// travels in Value/Candidates and must never appear in the dimensions map,
// whose entries the fact builder turns into Facts.Dimensions.
func dimensionScores(dimensions map[string]classifieroutput.DimensionLabel) map[string]modelrouting.DimensionScore {
	if len(dimensions) == 0 {
		return nil
	}
	scores := make(map[string]modelrouting.DimensionScore, len(dimensions))
	for name, dimension := range dimensions {
		if name == contract.BuiltInTaskTypeDimension {
			continue
		}
		scores[name] = modelrouting.DimensionScore{Label: dimension.Label, Score: dimension.Score}
	}
	if len(scores) == 0 {
		return nil
	}
	return scores
}

// autoclassifierDimensionScores moves the cloud source's dimension answers onto
// the shared Outcome carrier. Both classifier sources (the strict extraction
// core and the cloud adapters) speak this one type downstream, so the fact
// builder sees a single shape regardless of where the answer came from.
func autoclassifierDimensionScores(dimensions map[string]autoclassifier.DimensionAnswer) map[string]modelrouting.DimensionScore {
	if len(dimensions) == 0 {
		return nil
	}
	scores := make(map[string]modelrouting.DimensionScore, len(dimensions))
	for name, dimension := range dimensions {
		if name == contract.BuiltInTaskTypeDimension {
			continue
		}
		scores[name] = modelrouting.DimensionScore{Label: dimension.Label, Score: dimension.Score}
	}
	if len(scores) == 0 {
		return nil
	}
	return scores
}

// autoclassifierCandidates moves the strict extraction core's ranked output
// onto the shared Outcome carrier so every classifier source speaks one
// candidate type downstream.
func autoclassifierCandidates(candidates []classifieroutput.Candidate) []autoclassifier.Candidate {
	if len(candidates) == 0 {
		return nil
	}
	converted := make([]autoclassifier.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		converted = append(converted, autoclassifier.Candidate{Label: candidate.Label, Score: candidate.Score})
	}
	return converted
}

func existingModelFailureCode(ctx context.Context, err error) string {
	switch {
	case errors.Is(err, ErrClassificationVirtualModel):
		return "classifier_virtual_model_rejected"
	case errors.Is(err, ErrClassificationBusy):
		return "classifier_busy"
	case errors.Is(err, ErrClassificationTimeout), errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "classifier_timeout"
	case errors.Is(err, ErrClassificationInvalidText):
		return "classifier_invalid_output"
	default:
		return "classifier_unavailable"
	}
}

// taskTypeCandidateScores / taskTypeCandidateLabels /
// taskTypeCandidateObservations moved to the modelrouting package
// (TaskTypeCandidateScores and friends) so the control-plane dry-run consumes
// the same conversion instead of a copy. Behavior is unchanged.

// modelRoutingTaskTypeCloud classifies through the configured external
// provider. Every failure mode returns a deterministic fallback: the request
// is never blocked by the classifier seam.
func (handler *Handler) modelRoutingTaskTypeCloud(ctx context.Context, text string, group contract.ModelRoutingGroup, settings contract.ClassifierSettings) modelrouting.TaskTypeOutcome {
	plan := settings.ResolveCallPlan(&group)
	result := modelrouting.TaskTypeOutcome{LowConfidenceThreshold: plan.LowConfidenceThreshold}
	if handler.proxyCredentials == nil {
		result.Code = autoclassifier.FallbackUnavailable
		return result
	}
	start := time.Now()
	secret, err := handler.proxyCredentials.Get(ctx, secretstore.Ref(plan.CredentialRef))
	if err != nil {
		result.DurationMS = int(time.Since(start).Milliseconds())
		result.Code = autoclassifier.FallbackUnavailable
		return result
	}
	outcome := cloud.Classify(ctx, nil, cloud.Input{
		Protocol:   cloud.Protocol(plan.Protocol),
		Endpoint:   plan.Endpoint,
		APIKey:     secret,
		Model:      plan.Model,
		Prompt:     plan.Prompt,
		Labels:     plan.Labels,
		Text:       text,
		TimeoutMS:  plan.TimeoutMS,
		Dimensions: plan.Dimensions,
	})
	result.DurationMS = int(time.Since(start).Milliseconds())
	result.ClassifierID = string(outcome.ClassifierID)
	result.Candidates = outcome.Candidates
	result.Dimensions = autoclassifierDimensionScores(outcome.Dimensions)
	if outcome.OK() {
		if !group.ValidTaskTypeLabel(outcome.Category) {
			result.Code = "classifier_invalid_output"
			return result
		}
		// A confident answer inside the label set is the only success path.
		// Low confidence is never a failure and never takes a fallback: the
		// fact becomes unknown and triUnknown rules skip. As on the
		// existing-model path, the dimensions measured by the same request
		// are unaffected — only this task_type's score was weak.
		if outcome.Confidence < plan.LowConfidenceThreshold {
			result.Code = modelrouting.CodeClassifierLowConfidence
			return result
		}
		result.Value = outcome.Category
		result.Confidence = outcome.Confidence
		return result
	}
	// A failure records its reason and leaves task_type unknown: the shared
	// mapping owns the code domain, and nothing here substitutes a label. The
	// request falls through to the group's default member.
	failed := autoclassifier.FailureTaskType(outcome, result.DurationMS)
	return modelrouting.TaskTypeOutcome{
		Code:                   failed.Code,
		DurationMS:             failed.DurationMS,
		ClassifierID:           string(failed.ClassifierID),
		LowConfidenceThreshold: plan.LowConfidenceThreshold,
	}
}

// modelRoutingUnsupportedMessage names everything a client needs to fix a
// member that cannot take this request: the group, the model, the API
// provider by the name the user gave it, and the request format. Clients such
// as coding agents show only this message, so it has to stand on its own.
func (handler *Handler) modelRoutingUnsupportedMessage(ctx context.Context, classified Request, member contract.ModelRoutingMember, reason contract.RoutingSkipReason) string {
	group := strings.TrimPrefix(classified.Model, contract.ModelRoutingModelPrefix)
	provider := string(member.ServiceID)
	if services, ok := handler.requestRecords.(interface {
		GetService(context.Context, contract.ServiceID) (storage.ServiceRecord, error)
	}); ok {
		if record, err := services.GetService(ctx, member.ServiceID); err == nil && record.Service.Name != "" {
			provider = record.Service.Name
		}
	}
	format := string(classified.Protocol) + " requests"
	switch reason {
	case contract.RoutingSkipStreamingUnsupported:
		format = "streaming " + format
	case contract.RoutingSkipWebSocketUnsupported:
		format = string(classified.Protocol) + " over WebSocket"
	}
	return fmt.Sprintf("routing group %q chose %s on %q, which does not accept %s; enable a conversion in its entry protocols or route this rule to another model", group, member.Model, provider, format)
}

func pinModelRoutingWebSocket(ctx context.Context, model string) {
	if turn := responsesWSTurnFromContext(ctx); turn != nil {
		turn.routingModel = model
	}
}

// modelRoutingNeedsClassification reports whether any rule reads a field the
// classifier produces. task_type is always a classifier field; each declared
// custom dimension is one too, so a group whose rules reference only a custom
// dimension still classifies. Without this the dimension would never be asked
// for, every rule testing it would stay unknown forever, and the configuration
// would silently do nothing.
//
// With no custom dimensions declared this reduces to the task_type-only gate
// the routing path has always used.
func modelRoutingNeedsClassification(group contract.ModelRoutingGroup, dimensions []contract.ClassifierDimension) bool {
	if group.ReferencesField(contract.BuiltInTaskTypeDimension) {
		return true
	}
	for _, dimension := range dimensions {
		if group.ReferencesField(dimension.Name) {
			return true
		}
	}
	return false
}

func routingBoolPtr(value bool) *bool { return &value }
func routingIntPtr(value int) *int    { return &value }
