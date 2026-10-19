package ingress

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// routingVersionStore answers the version lookup a routed request makes.
type routingVersionStore struct {
	*modelRoutingBindingTestStore
	version int
	looked  []string
}

func (store *routingVersionStore) EnsureModelRoutingGroupVersion(_ context.Context, group contract.ModelRoutingGroup) (int, error) {
	store.looked = append(store.looked, group.ID)
	return store.version, nil
}

func (store *routingVersionStore) GetModelRoutingGroupVersion(context.Context, string, int) (contract.ModelRoutingGroupVersion, error) {
	return contract.ModelRoutingGroupVersion{}, storage.ErrNotFound
}

func recordedRoutingGroup() contract.ModelRoutingGroup {
	return contract.ModelRoutingGroup{
		ID: "program", Enabled: true,
		Members: []contract.ModelRoutingMember{
			{ID: "strong", ServiceID: "service_01", Model: "strong-model"},
			{ID: "translator", ServiceID: "service_01", Model: "translator-model"},
			{ID: "fast", ServiceID: "service_01", Model: "fast-model"},
		},
		DefaultMemberID: "fast",
		Rules: []contract.ModelRoutingRule{
			{ID: "streamed", Priority: 1, When: contract.ModelRoutingCondition{Field: "streaming", EqualsBool: boolRef(true)}, TargetMemberID: "strong"},
			{ID: "translate", Priority: 2, When: contract.ModelRoutingCondition{Field: "literal_keyword", ContainsAny: []string{"translate"}}, TargetMemberID: "translator"},
			{ID: "chat", Priority: 3, When: contract.ModelRoutingCondition{Field: "protocol", In: []string{"openai.chat"}}, TargetMemberID: "strong"},
		},
	}
}

func recordedRoutingSettings(groups ...contract.ModelRoutingGroup) contract.RoutingSettings {
	settings := contract.DefaultRoutingSettings()
	settings.ModelRoutingGroups = groups
	return settings
}

// A live request keeps the dry-run's whole walk: the row that lost, the row
// that won and the row that never ran, each with the value it read. The
// keyword row says how many keywords matched and never what the text was.
func TestModelRoutingRecordsEveryRuleVerdict(t *testing.T) {
	settings := recordedRoutingSettings(recordedRoutingGroup())
	request := Request{Protocol: contract.ProtocolOpenAIResponses, Model: "astrlink/program", routingUserText: "please translate this", routingUserTextRunes: 21, routingUserTextKnown: true}
	session := newRecordSession(request, "", contract.AuditSettings{})
	if _, err := New().applyModelRouting(context.Background(), session, request, settings); err != nil {
		t.Fatal(err)
	}
	decision := session.modelRouting
	if decision == nil || decision.RuleID != "translate" || decision.MemberModel != "translator-model" {
		t.Fatalf("decision = %+v", decision)
	}
	if len(decision.Rules) != 3 {
		t.Fatalf("rules = %+v, want all three", decision.Rules)
	}
	streamed, translate, chat := decision.Rules[0], decision.Rules[1], decision.Rules[2]
	if streamed.State != contract.ModelRoutingRuleFalse || streamed.Selected || len(streamed.Fields) != 1 || streamed.Fields[0].Value != "false" {
		t.Fatalf("streamed verdict = %+v", streamed)
	}
	if translate.State != contract.ModelRoutingRuleTrue || !translate.Selected || translate.TargetModel != "translator-model" {
		t.Fatalf("translate verdict = %+v", translate)
	}
	if field := translate.Fields[0]; field.MatchCount != 1 || field.Value != "" {
		t.Fatalf("keyword observation = %+v, want a count and no text", field)
	}
	if chat.State != contract.ModelRoutingRuleNotEvaluated || len(chat.Fields) != 0 {
		t.Fatalf("chat verdict = %+v, want not evaluated", chat)
	}
	if decision.Classifier != nil {
		t.Fatalf("classifier answers = %+v for a group without classifier rules", decision.Classifier)
	}
	if err := decision.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestModelRoutingRecordsClassifierAnswers(t *testing.T) {
	handler := New()
	handler.classifier = ClassifierFunc(func(context.Context, string) autoclassifier.Outcome {
		return autoclassifier.Outcome{Category: "coding", Confidence: 0.93}
	})
	group := recordedRoutingGroup()
	group.Rules = []contract.ModelRoutingRule{{ID: "coding", Priority: 1, When: contract.ModelRoutingCondition{Field: "task_type", In: []string{"coding"}}, TargetMemberID: "strong"}}
	request := Request{Protocol: contract.ProtocolOpenAIResponses, Model: "astrlink/program", lastUserText: "write code"}
	session := newRecordSession(request, "", contract.AuditSettings{})
	if _, err := handler.applyModelRouting(context.Background(), session, request, recordedRoutingSettings(group)); err != nil {
		t.Fatal(err)
	}
	decision := session.modelRouting
	if decision.Classifier == nil || decision.Classifier.TaskType != "coding" || decision.Classifier.Confidence != 0.93 {
		t.Fatalf("classifier answers = %+v", decision.Classifier)
	}
	if len(decision.Rules) != 1 || decision.Rules[0].Fields[0].Value != "coding" {
		t.Fatalf("rules = %+v", decision.Rules)
	}
}

func TestModelRoutingRecordsConfigVersion(t *testing.T) {
	store := &routingVersionStore{modelRoutingBindingTestStore: &modelRoutingBindingTestStore{redirectSettingsStore: newRedirectSettingsStore()}, version: 12}
	handler := New()
	handler.requestRecords = store
	request := Request{Protocol: contract.ProtocolOpenAIResponses, Model: "astrlink/program"}
	session := newRecordSession(request, "", contract.AuditSettings{})
	if _, err := handler.applyModelRouting(context.Background(), session, request, recordedRoutingSettings(recordedRoutingGroup())); err != nil {
		t.Fatal(err)
	}
	if session.modelRouting.ConfigVersion != 12 || len(store.looked) != 1 || store.looked[0] != "program" {
		t.Fatalf("config version = %d, looked up %v", session.modelRouting.ConfigVersion, store.looked)
	}
	// The same configuration again costs no store read.
	again := newRecordSession(request, "", contract.AuditSettings{})
	if _, err := handler.applyModelRouting(context.Background(), again, request, recordedRoutingSettings(recordedRoutingGroup())); err != nil {
		t.Fatal(err)
	}
	if again.modelRouting.ConfigVersion != 12 || len(store.looked) != 1 {
		t.Fatalf("second request: version %d, store reads %d", again.modelRouting.ConfigVersion, len(store.looked))
	}
}

// boundRoutingSession is a request from a session that an earlier request
// bound to the group's "strong" member.
func boundRoutingSession(t *testing.T, settings contract.RoutingSettings, memberID, model string) (*Handler, *modelRoutingBindingTestStore, *recordSession, Request) {
	t.Helper()
	token := contract.AccessTokenID("access-token-x")
	store := &modelRoutingBindingTestStore{redirectSettingsStore: newRedirectSettingsStore()}
	store.binding = &contract.ModelRoutingBinding{
		Principal: string(token), SessionID: "session-x", Protocol: contract.ProtocolOpenAIResponses,
		VirtualModelID: "astrlink/program", GroupID: "program", MemberID: memberID, ServiceID: "service_01", Model: model,
		ConfigSnapshotVersion: "0000000000000000000000000000000000000000000000000000000000000000", RequestID: "req_origin",
		CreatedAt: modelRoutingBindingTime(time.Now()), UpdatedAt: modelRoutingBindingTime(time.Now()), ExpiresAt: modelRoutingBindingTime(time.Now().Add(time.Hour)),
	}
	handler := New()
	handler.requestRecords = store
	request := Request{Protocol: contract.ProtocolOpenAIResponses, Model: "astrlink/program"}
	session := newRecordSession(request, "", contract.AuditSettings{})
	session.sessionID = "session-x"
	session.accessTokenID = &token
	session.routingSettings = &settings
	return handler, store, session, request
}

func TestModelRoutingReusesSessionBinding(t *testing.T) {
	settings := recordedRoutingSettings(recordedRoutingGroup())
	handler, _, session, request := boundRoutingSession(t, settings, "strong", "strong-model")
	got, err := handler.applyModelRouting(context.Background(), session, request, settings)
	if err != nil {
		t.Fatal(err)
	}
	decision := session.modelRouting
	if got.ModelRoutingMember.ID != "strong" || decision.SelectionSource != contract.ModelRoutingSelectionGroupBinding {
		t.Fatalf("member = %+v, decision = %+v", got.ModelRoutingMember, decision)
	}
	if decision.BindingRequestID != "req_origin" || len(decision.Rules) != 0 {
		t.Fatalf("binding decision = %+v, want the origin request and no rule walk", decision)
	}
}

// The routing-settings stickiness switch governs routing bindings exactly as
// it governs API provider bindings: off means neither read nor written.
func TestModelRoutingSessionBindingFollowsStickinessSwitch(t *testing.T) {
	settings := recordedRoutingSettings(recordedRoutingGroup())
	settings.ChannelStickiness = &contract.ChannelStickiness{Enabled: false, TTLSeconds: 3600}
	handler, store, session, request := boundRoutingSession(t, settings, "strong", "strong-model")
	if _, err := handler.applyModelRouting(context.Background(), session, request, settings); err != nil {
		t.Fatal(err)
	}
	if session.modelRouting.SelectionSource == contract.ModelRoutingSelectionGroupBinding || store.getCalls != 0 {
		t.Fatalf("decision = %+v, binding reads = %d; want the rules and no read", session.modelRouting, store.getCalls)
	}
	session.status = contract.RequestStatusSucceeded
	handler.persistModelRoutingBinding(context.Background(), session)
	if store.upsertCalls != 0 {
		t.Fatalf("binding writes = %d with stickiness off", store.upsertCalls)
	}
}

func TestModelRoutingSessionBindingUsesStickinessTimeout(t *testing.T) {
	settings := recordedRoutingSettings(recordedRoutingGroup())
	settings.ChannelStickiness = &contract.ChannelStickiness{Enabled: true, TTLSeconds: 600}
	handler, store, session, request := boundRoutingSession(t, settings, "strong", "strong-model")
	store.binding = nil
	if _, err := handler.applyModelRouting(context.Background(), session, request, settings); err != nil {
		t.Fatal(err)
	}
	session.status = contract.RequestStatusSucceeded
	handler.persistModelRoutingBinding(context.Background(), session)
	if store.binding == nil || store.binding.RequestID != string(session.id) {
		t.Fatalf("binding = %+v, want this request as its origin", store.binding)
	}
	expires, err := time.Parse(time.RFC3339Nano, store.binding.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if remaining := time.Until(expires); remaining < 9*time.Minute || remaining > 11*time.Minute {
		t.Fatalf("binding lives %s, want the ten-minute idle timeout", remaining)
	}
	if session.modelRouting.BindingExpiresAt != store.binding.ExpiresAt {
		t.Fatalf("recorded expiry = %q, binding expiry = %q", session.modelRouting.BindingExpiresAt, store.binding.ExpiresAt)
	}
	if !store.started.Equal(session.startedAt) {
		t.Fatalf("binding write fenced at %s, want the request start %s", store.started, session.startedAt)
	}
}

// Reusing a binding keeps pointing at the request whose rules chose it.
func TestModelRoutingReusedBindingKeepsItsOrigin(t *testing.T) {
	settings := recordedRoutingSettings(recordedRoutingGroup())
	handler, store, session, request := boundRoutingSession(t, settings, "strong", "strong-model")
	if _, err := handler.applyModelRouting(context.Background(), session, request, settings); err != nil {
		t.Fatal(err)
	}
	session.status = contract.RequestStatusSucceeded
	handler.persistModelRoutingBinding(context.Background(), session)
	if store.binding.RequestID != "req_origin" {
		t.Fatalf("binding origin = %q, want req_origin", store.binding.RequestID)
	}
}

func TestModelRoutingDropsBindingWhoseMemberLeftTheGroup(t *testing.T) {
	settings := recordedRoutingSettings(recordedRoutingGroup())
	handler, store, session, request := boundRoutingSession(t, settings, "retired", "retired-model")
	got, err := handler.applyModelRouting(context.Background(), session, request, settings)
	if err != nil {
		t.Fatal(err)
	}
	decision := session.modelRouting
	if got.ModelRoutingMember.ID != "fast" || decision.SelectionSource != contract.ModelRoutingSelectionDefaultMember {
		t.Fatalf("member = %+v, decision = %+v; want the rules judged again", got.ModelRoutingMember, decision)
	}
	reset := decision.BindingReset
	if reset == nil || reset.Reason != contract.ModelRoutingBindingMemberRemoved || reset.Model != "retired-model" {
		t.Fatalf("binding reset = %+v", reset)
	}
	if store.deleteCalls != 1 || store.binding != nil {
		t.Fatalf("delete calls = %d, binding = %+v", store.deleteCalls, store.binding)
	}
	if err := decision.Validate(); err != nil {
		t.Fatal(err)
	}
}

// A member whose model was changed in place is not the member the session
// was bound to.
func TestModelRoutingDropsBindingWhenMemberModelChanged(t *testing.T) {
	settings := recordedRoutingSettings(recordedRoutingGroup())
	handler, _, session, request := boundRoutingSession(t, settings, "strong", "previous-strong-model")
	if _, err := handler.applyModelRouting(context.Background(), session, request, settings); err != nil {
		t.Fatal(err)
	}
	if reset := session.modelRouting.BindingReset; reset == nil || reset.Model != "previous-strong-model" {
		t.Fatalf("binding reset = %+v", reset)
	}
}

func TestModelRoutingDisabledGroupRefusesBoundSession(t *testing.T) {
	group := recordedRoutingGroup()
	group.Enabled = false
	settings := recordedRoutingSettings(group)
	handler, store, session, request := boundRoutingSession(t, settings, "strong", "strong-model")
	_, err := handler.applyModelRouting(context.Background(), session, request, settings)
	var routingErr *modelRoutingHTTPError
	if !errors.As(err, &routingErr) || routingErr.code != "model_routing_group_disabled" {
		t.Fatalf("err = %v, want model_routing_group_disabled", err)
	}
	if store.getCalls != 0 {
		t.Fatalf("binding reads = %d for a disabled group", store.getCalls)
	}
}

type namedServiceStore struct {
	*redirectSettingsStore
}

func (namedServiceStore) GetService(_ context.Context, id contract.ServiceID) (storage.ServiceRecord, error) {
	return storage.ServiceRecord{Service: contract.Service{ID: id, Name: "Claude 订阅"}}, nil
}

// A client such as a coding agent shows only this message, so it names the
// group, the model, the provider by its display name, and the format.
func TestModelRoutingUnsupportedMessageNamesModelProviderAndFormat(t *testing.T) {
	handler := New()
	handler.requestRecords = namedServiceStore{newRedirectSettingsStore()}
	request := Request{Protocol: contract.ProtocolOpenAIResponses, Model: "astrlink/program"}
	member := contract.ModelRoutingMember{ID: "strong", ServiceID: "service_01", Model: "claude-opus-5-5"}
	got := handler.modelRoutingUnsupportedMessage(context.Background(), request, member, contract.RoutingSkipProtocolUnsupported)
	want := `routing group "program" chose claude-opus-5-5 on "Claude 订阅", which does not accept openai.responses requests; enable a conversion in its entry protocols or route this rule to another model`
	if got != want {
		t.Fatalf("message = %q\nwant %q", got, want)
	}
	if got := handler.modelRoutingUnsupportedMessage(context.Background(), request, member, contract.RoutingSkipStreamingUnsupported); !strings.Contains(got, "does not accept streaming openai.responses requests") {
		t.Fatalf("streaming message = %q", got)
	}
	handler.requestRecords = newRedirectSettingsStore()
	if got := handler.modelRoutingUnsupportedMessage(context.Background(), request, member, contract.RoutingSkipProtocolUnsupported); !strings.Contains(got, `on "service_01"`) {
		t.Fatalf("message without a service reader = %q", got)
	}
}

// Disabling a group stops a Responses continuation too, not only new
// sessions: what disabling does must not depend on how the client sends its
// next turn.
func TestModelRoutingDisabledGroupRefusesAContinuation(t *testing.T) {
	group := recordedRoutingGroup()
	group.Enabled = false
	request := Request{Protocol: contract.ProtocolOpenAIResponses, Model: "astrlink/program", PreviousResponseID: "resp_previous"}
	session := newRecordSession(request, "", contract.AuditSettings{})
	_, err := New().applyModelRouting(context.Background(), session, request, recordedRoutingSettings(group))
	var routingErr *modelRoutingHTTPError
	if !errors.As(err, &routingErr) || routingErr.code != "model_routing_group_disabled" {
		t.Fatalf("err = %v, want model_routing_group_disabled", err)
	}
}
