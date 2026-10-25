package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier/cloud"
	"github.com/QuantumNous/astrlink/core/internal/classifieroutput"
	"github.com/QuantumNous/astrlink/core/internal/modelrouting"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// ClassifierSettingsPath is the singleton external-classifier resource. It
// deliberately does not extend the routing-settings document or its
// whitelists.
const ClassifierSettingsPath = "/control/v1/routing/classifier"

// classifierCredentialRef is the single V1 credential slot.
const classifierCredentialRef = secretstore.Ref(contract.ClassifierCredentialRef)

// dryRunPreviewRunes bounds the sanitized preview returned to the UI.
const dryRunPreviewRunes = 400

// dryRunSlots allows one dry-run at a time: each one is a real cloud call.
var dryRunSlots = make(chan struct{}, 1)

// dryRunSlack is how much longer than the classifier timeout one dry-run may
// take in all, for the privacy check and the reads around the classifier call.
// A run that needs more ends in dry_run_timeout instead of keeping the caller
// waiting. It is a variable so tests need not wait seconds.
var dryRunSlack = 5 * time.Second

func (handler *Handler) classifierResource(writer http.ResponseWriter, request *http.Request) {
	if handler.classifierSettings == nil {
		writeError(writer, http.StatusServiceUnavailable, "classifier_settings_unavailable", "classifier settings are unavailable")
		return
	}
	switch request.Method {
	case http.MethodGet:
		settings, err := handler.classifierSettings.GetClassifierSettings(request.Context())
		if err != nil {
			handler.writeStoreError(writer, err)
			return
		}
		handler.writeClassifierSettings(writer, request, settings)
	case http.MethodPut:
		var settings contract.ClassifierSettings
		if !decodeControlJSON(writer, request, &settings) {
			return
		}
		if err := settings.Validate(); err != nil {
			if errors.Is(err, contract.ErrClassifierSourceNotSupported) {
				writeError(writer, http.StatusUnprocessableEntity, "classifier_source_not_supported", err.Error())
				return
			}
			writeError(writer, http.StatusUnprocessableEntity, "invalid_classifier_settings", err.Error())
			return
		}
		if err := handler.classifierSettings.UpdateClassifierSettings(request.Context(), settings); err != nil {
			var inUse *contract.ClassifierDimensionInUseError
			if errors.As(err, &inUse) {
				writeError(writer, http.StatusConflict, "classifier_dimension_in_use", inUse.Error())
				return
			}
			if errors.Is(err, storage.ErrClassifierTarget) {
				writeError(writer, http.StatusUnprocessableEntity, "classifier_model_unavailable", err.Error())
				return
			}
			handler.writeStoreError(writer, err)
			return
		}
		handler.writeClassifierSettings(writer, request, settings)
	default:
		writer.Header().Set("Allow", "GET, PUT")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and PUT are allowed")
	}
}

func (handler *Handler) writeClassifierSettings(writer http.ResponseWriter, request *http.Request, settings contract.ClassifierSettings) {
	configured, err := handler.classifierCredentialConfigured(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "credential_unavailable", "could not read classifier credential status")
		return
	}
	writeJSON(writer, http.StatusOK, classifierSettingsResponse{
		ClassifierSettings:   settings,
		CredentialConfigured: configured,
	})
}

type classifierSettingsResponse struct {
	contract.ClassifierSettings
	CredentialConfigured bool `json:"credential_configured"`
}

// classifierCredentialStatus reports whether the sealed classifier key exists
// without unsealing it. A key the keyring can no longer open, as after a move
// to another machine, still reads as configured, so the settings page loads
// and the key can be entered again.
type classifierCredentialStatus interface {
	ClassifierCredentialConfigured(context.Context) (bool, error)
}

func (handler *Handler) classifierCredentialConfigured(ctx context.Context) (bool, error) {
	status, ok := handler.serviceStore.(classifierCredentialStatus)
	if !ok {
		return false, nil
	}
	return status.ClassifierCredentialConfigured(ctx)
}

func (handler *Handler) classifierSubResource(writer http.ResponseWriter, request *http.Request) {
	action, _, _ := strings.Cut(strings.TrimPrefix(request.URL.Path, ClassifierSettingsPath+"/"), "/")
	switch action {
	case "credential":
		handler.classifierCredentialResource(writer, request)
	case "dry-run":
		handler.classifierDryRun(writer, request)
	default:
		writeError(writer, http.StatusNotFound, "not_found", "classifier endpoint not found")
	}
}

func (handler *Handler) classifierCredentialResource(writer http.ResponseWriter, request *http.Request) {
	secrets, ok := handler.serviceStore.(secretstore.SecretStore)
	if !ok {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "credential store is unavailable")
		return
	}
	switch request.Method {
	case http.MethodGet:
		configured, err := handler.classifierCredentialConfigured(request.Context())
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "credential_unavailable", "could not read classifier credential status")
			return
		}
		writeJSON(writer, http.StatusOK, map[string]bool{"configured": configured})
	case http.MethodPut:
		var input struct {
			Secret string `json:"secret"`
		}
		if !decodeControlJSON(writer, request, &input) {
			return
		}
		if len(input.Secret) == 0 || len(input.Secret) > 16384 || strings.ContainsAny(input.Secret, "\r\n") {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_credential", "API key must contain 1 to 16384 bytes without newlines")
			return
		}
		value := []byte(input.Secret)
		defer clear(value)
		if err := secrets.Put(request.Context(), classifierCredentialRef, value); err != nil {
			writeError(writer, http.StatusInternalServerError, "credential_unavailable", "could not save classifier credential")
			return
		}
		writeJSON(writer, http.StatusOK, map[string]bool{"configured": true})
	case http.MethodDelete:
		if err := secrets.Delete(request.Context(), classifierCredentialRef); err != nil && !errors.Is(err, secretstore.ErrNotFound) {
			writeError(writer, http.StatusInternalServerError, "credential_unavailable", "could not delete classifier credential")
			return
		}
		writeJSON(writer, http.StatusOK, map[string]bool{"configured": false})
	default:
		writer.Header().Set("Allow", "GET, PUT, DELETE")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET, PUT or DELETE")
	}
}

// classifierDryRunRequest carries both the text and the request-side facts the
// rules are judged against.
//
// The dry-run evaluates the whole rule table, not just the classifier, so it
// needs the facts a live request would supply. Everything here is optional:
// an omitted fact stays unknown, which is a first-class outcome — an unknown
// fact never matches a rule, and the preview says so per field. Omitting is
// therefore honest rather than a silent default, which is why the booleans are
// pointers and not plain bools.
type classifierDryRunRequest struct {
	Text    string `json:"text"`
	GroupID string `json:"group_id,omitempty"`
	// Protocol must name a protocol AstrLink actually serves when set.
	Protocol string `json:"protocol,omitempty"`
	// MediaKind is one a live request can carry; "unknown" leaves it unset.
	MediaKind    string `json:"media_kind,omitempty"`
	Streaming    *bool  `json:"streaming,omitempty"`
	Continuation *bool  `json:"continuation,omitempty"`
	// SystemTime overrides the wall clock as "HH:MM" so a time-window rule can
	// be previewed without waiting for that window to come around.
	SystemTime string `json:"system_time,omitempty"`
}

type classifierDryRunDecision struct {
	MemberID    string `json:"member_id"`
	ServiceID   string `json:"service_id"`
	Model       string `json:"model"`
	RuleID      string `json:"rule_id,omitempty"`
	UsedDefault bool   `json:"used_default"`
}

type classifierDryRunResponse struct {
	// Configured reports whether a classifier the run can call is set up: the
	// cloud service, or an existing gateway model.
	Configured     bool                            `json:"configured"`
	Category       string                          `json:"category,omitempty"`
	Confidence     float64                         `json:"confidence,omitempty"`
	Dimensions     map[string]dryRunDimensionScore `json:"dimensions,omitempty"`
	FallbackReason string                          `json:"fallback_reason,omitempty"`
	Privacy        string                          `json:"privacy,omitempty"`
	DurationMS     int                             `json:"duration_ms"`
	// TimeoutMS is the classifier timeout this run used, the one the
	// classifier settings give.
	TimeoutMS        int    `json:"timeout_ms,omitempty"`
	SanitizedPreview string `json:"sanitized_preview,omitempty"`
	// Instruction is what the classifier was told, placeholders rendered: an
	// existing model's whole system instruction, or the instructions of a
	// cloud service's task_type question. Empty when nothing was called.
	Instruction string                                  `json:"instruction,omitempty"`
	Decision    *classifierDryRunDecision               `json:"decision,omitempty"`
	Fields      []contract.ModelRoutingFieldObservation `json:"fields,omitempty"`
	// Rules is every rule in the group with its own verdict, in evaluation
	// order, in the same shape a request record keeps. The selection above
	// reports only the winner, which cannot answer "why did the rule I wrote
	// not fire?" — this is the rest of the table. State is one of "true",
	// "false", "unknown" or "not_evaluated": the last three all leave the
	// member unselected, but only the first is a fact the operator can act on
	// by writing a better rule.
	Rules []contract.ModelRoutingRuleVerdict `json:"rules,omitempty"`
}

type dryRunDimensionScore struct {
	Label string  `json:"label"`
	Score float64 `json:"score"`
	// Known and Produced separate the two ways a dimension can fail to decide
	// anything: the classifier never answered it, or it answered below the
	// threshold. Neither matches a rule, but they need different fixes —
	// reword the prompt, or lower the threshold.
	Known    bool `json:"known"`
	Produced bool `json:"produced"`
}

// classifierDryRun previews one classification locally: the text passes the
// same privacy filter as production, then the configured classifier is asked
// once — the cloud service, or an existing gateway model through the path a
// live request takes. The text never goes on to a routed model and nothing is
// persisted.
//
// Without a configured classifier nothing is asked and the rules are still
// judged: every classifier field stays unknown, as it does for a live request
// whose classifier is unavailable, so the group's other rules and its default
// decide.
func (handler *Handler) classifierDryRun(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
		return
	}
	if handler.classifierSettings == nil {
		writeError(writer, http.StatusServiceUnavailable, "classifier_settings_unavailable", "classifier settings are unavailable")
		return
	}
	select {
	case dryRunSlots <- struct{}{}:
		defer func() { <-dryRunSlots }()
	default:
		writeError(writer, http.StatusTooManyRequests, "dry_run_busy", "another dry-run is in progress")
		return
	}
	begun := time.Now()
	var input classifierDryRunRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if strings.TrimSpace(input.Text) == "" || utf8.RuneCountInString(input.Text) > 64*1024 {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_dry_run", "text must contain 1 to 65536 runes")
		return
	}
	// An unset protocol is "unknown", which is a legitimate preview input. A set
	// one must be a protocol the gateway actually serves: previewing a rule
	// against a name no request can carry would report a selection that can
	// never happen, which is the same trap an open vocabulary creates.
	if input.Protocol != "" {
		protocol := contract.ProtocolID(input.Protocol)
		if !protocol.Valid() || !protocol.Known() {
			writeError(writer, http.StatusUnprocessableEntity, "unknown_dry_run_protocol", "protocol must name a known AstrLink protocol")
			return
		}
	}
	switch input.MediaKind {
	case "", "none", "image", "audio", "video", "mixed":
	case "unknown":
		// A live request is never of unknown media; an unknown fact is an
		// unset one, which matches no rule.
		input.MediaKind = ""
	default:
		writeError(writer, http.StatusUnprocessableEntity, "invalid_dry_run", "media_kind must be none, image, audio, video or mixed")
		return
	}
	systemTime, timeErr := dryRunSystemTime(input.SystemTime)
	if timeErr != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_dry_run", "system_time must be HH:MM")
		return
	}
	settings, err := handler.classifierSettings.GetClassifierSettings(request.Context())
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	response := classifierDryRunResponse{Configured: settings.CloudConfigured() || settings.ExistingModelConfigured()}
	// The classifier call keeps the timeout its settings give it; the whole
	// run gets that timeout plus dryRunSlack and then answers dry_run_timeout.
	timeoutMS := settings.EffectiveTimeoutMS()
	ctx, cancel := context.WithDeadline(request.Context(), begun.Add(time.Duration(timeoutMS)*time.Millisecond+dryRunSlack))
	defer cancel()
	sanitized, privacyState := handler.dryRunPrivacy(ctx, input.Text)
	if dryRunExpired(ctx) {
		writeDryRunTimeout(writer)
		return
	}
	response.Privacy = privacyState
	response.SanitizedPreview = boundedDryRunPreview(sanitized)
	switch privacyState {
	case "blocked", "inspection_failed":
		response.FallbackReason = "privacy_" + privacyState
	case "unavailable":
		// Without the policy the text cannot be checked, so it may not leave
		// either; a live request stops with the same code.
		response.FallbackReason = "privacy_policy_unavailable"
	}
	if response.FallbackReason != "" {
		writeJSON(writer, http.StatusOK, response)
		return
	}
	group := handler.dryRunGroup(ctx, input.GroupID)
	plan := settings.ResolveCallPlan(group)
	// The dry-run fills the same routing-side carrier as the inference
	// path's cloud classifier (modelRoutingTaskTypeCloud): success requires a
	// label the group accepts and enough confidence, low confidence stays a
	// code and never a fallback, and failures go through the shared mapping.
	// Facts and observations then derive from that carrier through the one
	// conversion in modelrouting, never from a synthesized single value. A
	// classifier that cannot be called leaves classifier_unavailable, the code
	// a live request records in its place.
	taskType := modelrouting.TaskTypeOutcome{
		Code:                   autoclassifier.FallbackUnavailable,
		LowConfidenceThreshold: plan.LowConfidenceThreshold,
	}
	if secret, ok := handler.dryRunClassifierKey(ctx, settings); ok {
		response.TimeoutMS = timeoutMS
		response.Instruction = plan.Prompt
		started := time.Now()
		outcome := cloud.Classify(ctx, nil, cloud.Input{
			Protocol:   cloud.Protocol(plan.Protocol),
			Endpoint:   plan.Endpoint,
			APIKey:     secret,
			Model:      plan.Model,
			Prompt:     plan.Prompt,
			Labels:     plan.Labels,
			Text:       sanitized,
			TimeoutMS:  plan.TimeoutMS,
			Dimensions: plan.Dimensions,
		})
		response.DurationMS = int(time.Since(started).Milliseconds())
		response.Confidence = outcome.Confidence
		taskType = modelrouting.TaskTypeOutcome{
			ClassifierID:           string(outcome.ClassifierID),
			Candidates:             outcome.Candidates,
			LowConfidenceThreshold: plan.LowConfidenceThreshold,
			Dimensions:             dryRunDimensionScores(outcome.Dimensions),
		}
		if outcome.OK() {
			if group != nil && !group.ValidTaskTypeLabel(outcome.Category) {
				taskType.Code = "classifier_invalid_output"
			} else if outcome.Confidence < plan.LowConfidenceThreshold {
				taskType.Code = modelrouting.CodeClassifierLowConfidence
			} else {
				taskType.Value = outcome.Category
				taskType.Confidence = outcome.Confidence
			}
		} else {
			// The dry-run preview reports failures through the same shared
			// mapping as production routing: a stable record code instead of
			// the raw provider reason, and no substituted label — the preview
			// shows the default member a live request would fall through to.
			failure := autoclassifier.FailureTaskType(outcome, response.DurationMS)
			taskType.Code = failure.Code
		}
	} else if settings.ExistingModelConfigured() && handler.modelClassifier != nil {
		// An existing gateway model classifies through the code a live request
		// runs, so instruction, parsing and failure codes cannot drift apart.
		response.TimeoutMS = timeoutMS
		// Built by the function the live call uses, from the same plan.
		response.Instruction, _ = classifieroutput.ExistingModelInstruction(plan)
		started := time.Now()
		taskType, response.Confidence = handler.modelClassifier.ExistingModelTaskType(ctx, sanitized, group, settings)
		response.DurationMS = int(time.Since(started).Milliseconds())
	}
	// The classifier's own timeout is a result (classifier_timeout); running
	// out of the whole run's time is not, because nothing ran to completion.
	if dryRunExpired(ctx) {
		writeDryRunTimeout(writer)
		return
	}
	response.Category = taskType.Value
	response.FallbackReason = taskType.Code
	// The dimensions the evaluator sees are the dimensions the preview reports,
	// one entry per configured dimension rather than per answered one: a
	// dimension the classifier skipped is the case an operator most needs to
	// see, and a report built from the answers alone would silently omit it.
	dimensionFacts := modelrouting.DimensionFacts(taskType)
	response.Dimensions = dryRunDimensions(plan.Dimensions, dimensionFacts)
	if group != nil {
		// The facts come from the request, not from constants. The preview used
		// to pin protocol/media_kind/streaming, so every request-derived rule
		// was judged against a request nobody made — the classifier was
		// previewed, but the rule table was not. What the operator omits is
		// left unknown, which the rule walk reports per field.
		facts := contract.ModelRoutingFacts{
			Protocol:     input.Protocol,
			MediaKind:    input.MediaKind,
			Streaming:    input.Streaming,
			Continuation: input.Continuation,
			LiteralText:  sanitized,
			TaskType:     taskType.Value,
			SystemTime:   &systemTime,
		}
		facts.TaskTypeCandidates = modelrouting.TaskTypeCandidateLabels(taskType, *group)
		// The dry-run preview must evaluate the same dimensions production
		// routing does, or a saved-then-previewed configuration would report a
		// different outcome than the live path.
		facts.Dimensions = dimensionFacts
		selection, verdicts := modelrouting.Explain(*group, facts)
		// Provenance is stamped by the same conversion the inference path uses,
		// so the preview cannot describe this attempt differently from the
		// record a live request would write.
		modelrouting.StampClassifierProvenance(selection.Fields, taskType, *group, selection.TaskTypeCandidateIndex)
		response.Fields = selection.Fields
		response.Rules = modelrouting.RuleVerdicts(verdicts, *group)
		response.Decision = &classifierDryRunDecision{
			MemberID:    selection.Member.ID,
			ServiceID:   string(selection.Member.ServiceID),
			Model:       selection.Member.Model,
			RuleID:      selection.RuleID,
			UsedDefault: selection.UsedDefault,
		}
	}
	writeJSON(writer, http.StatusOK, response)
}

func boolPtrDryRun(value bool) *bool { return &value }

// dryRunClassifierKey returns the cloud classifier's key when a dry-run can
// call it: the cloud source is configured and its key is stored. Otherwise the
// run calls nothing.
func (handler *Handler) dryRunClassifierKey(ctx context.Context, settings contract.ClassifierSettings) ([]byte, bool) {
	if !settings.CloudConfigured() {
		return nil, false
	}
	secrets, _ := handler.serviceStore.(secretstore.SecretStore)
	if secrets == nil {
		return nil, false
	}
	secret, err := secrets.Get(ctx, classifierCredentialRef)
	if err != nil {
		return nil, false
	}
	return secret, true
}

// dryRunExpired reports whether a dry-run outlived its own deadline, as
// opposed to the caller hanging up.
func dryRunExpired(ctx context.Context) bool {
	return errors.Is(ctx.Err(), context.DeadlineExceeded)
}

func writeDryRunTimeout(writer http.ResponseWriter) {
	writeError(writer, http.StatusGatewayTimeout, "dry_run_timeout", "dry-run did not finish within the classifier timeout plus "+dryRunSlack.String())
}

// dryRunSystemTime resolves the preview's clock. An empty value means "now",
// sampled once here so every rule in one preview reads the same instant — the
// production fact builder makes the same guarantee. A supplied "HH:MM" is
// pinned in UTC: `system_time` is a time-of-day window and the evaluator reads
// only the clock, which in a local zone would skip or repeat an hour on the
// day daylight saving time changes.
func dryRunSystemTime(value string) (time.Time, error) {
	if value == "" {
		return time.Now(), nil
	}
	hour, minute, ok := minutesOfClock(value)
	if !ok {
		return time.Time{}, errInvalidDryRunClock
	}
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, time.UTC), nil
}

var errInvalidDryRunClock = errors.New("system_time must be HH:MM")

// minutesOfClock mirrors the evaluator's window parser: five characters, a
// colon in the middle, two digits either side, and a real time of day.
func minutesOfClock(value string) (int, int, bool) {
	if len(value) != 5 || value[2] != ':' {
		return 0, 0, false
	}
	for _, position := range []int{0, 1, 3, 4} {
		if digit := value[position]; digit < '0' || digit > '9' {
			return 0, 0, false
		}
	}
	hour := int(value[0]-'0')*10 + int(value[1]-'0')
	minute := int(value[3]-'0')*10 + int(value[4]-'0')
	if hour > 23 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}

// dryRunDimensionScores moves the cloud source's dimension answers onto the
// shared carrier the dry-run evaluates. task_type is dropped: it already
// travels in the carrier's Value and Candidates.
func dryRunDimensionScores(dimensions map[string]autoclassifier.DimensionAnswer) map[string]modelrouting.DimensionScore {
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

// dryRunDimensions renders the configured dimensions against the facts the
// evaluator used. It walks `configured` rather than the facts map so a
// dimension the classifier never answered still appears — with Produced
// false — instead of vanishing from the report, which would read as "this
// dimension is not configured" rather than "the classifier skipped it".
//
// Known is the fact builder's own threshold verdict, so a dimension the
// preview marks Known is exactly a dimension a rule could match on.
func dryRunDimensions(configured []contract.ClassifierDimension, facts map[string]contract.DimensionFact) map[string]dryRunDimensionScore {
	if len(configured) == 0 {
		return nil
	}
	converted := make(map[string]dryRunDimensionScore, len(configured))
	for _, dimension := range configured {
		if dimension.Name == contract.BuiltInTaskTypeDimension {
			// task_type already travels as category/confidence; repeating it
			// here would let the two disagree.
			continue
		}
		fact := facts[dimension.Name]
		converted[dimension.Name] = dryRunDimensionScore{
			Label:    fact.Value,
			Score:    fact.Score,
			Known:    fact.Known,
			Produced: fact.Produced,
		}
	}
	if len(converted) == 0 {
		return nil
	}
	return converted
}

// dryRunPrivacy applies the production privacy filter to the dry-run text by
// wrapping it in the chat envelope the detector understands. The sanitized
// user text returns for classification; the state names the outcome.
func (handler *Handler) dryRunPrivacy(ctx context.Context, text string) (string, string) {
	if handler.privacyFilter == nil {
		return text, "off"
	}
	policy, err := handler.privacyFilter.ResolvePolicy(ctx, privacy.Scope{})
	if err != nil {
		return "", "unavailable"
	}
	if !policy.Enabled {
		return text, "off"
	}
	envelope, err := json.Marshal(map[string]any{
		"model":    "astrlink-dry-run",
		"messages": []map[string]string{{"role": "user", "content": text}},
	})
	if err != nil {
		return "", "inspection_failed"
	}
	result, err := handler.privacyFilter.Inspect(ctx, policy, contract.ProtocolOpenAIChat, envelope)
	if err != nil {
		return "", "inspection_failed"
	}
	switch result.Decision {
	case privacy.DecisionBlock:
		return "", "blocked"
	case privacy.DecisionRedact:
		return extractDryRunText(result.Body), "redact"
	default:
		return text, string(result.Decision)
	}
}

func extractDryRunText(body []byte) string {
	var envelope struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return ""
	}
	for index := len(envelope.Messages) - 1; index >= 0; index-- {
		if envelope.Messages[index].Role == "user" {
			return envelope.Messages[index].Content
		}
	}
	return ""
}

func boundedDryRunPreview(text string) string {
	runes := []rune(text)
	if len(runes) > dryRunPreviewRunes {
		return string(runes[:dryRunPreviewRunes]) + "…"
	}
	return text
}

func (handler *Handler) dryRunGroup(ctx context.Context, groupID string) *contract.ModelRoutingGroup {
	if groupID == "" || handler.routingSettings == nil {
		return nil
	}
	settings, err := handler.routingSettings.GetRoutingSettings(ctx)
	if err != nil {
		return nil
	}
	for index := range settings.ModelRoutingGroups {
		if settings.ModelRoutingGroups[index].ID == groupID {
			group := settings.ModelRoutingGroups[index]
			return &group
		}
	}
	return nil
}
