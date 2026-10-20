package ingress

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/modelrouting"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestModelRoutingSelectsConcreteMemberBeforeRedirect(t *testing.T) {
	settings := contract.DefaultRoutingSettings()
	settings.ModelRoutingGroups = []contract.ModelRoutingGroup{{
		ID: "superclaude", Enabled: true,
		Members:         []contract.ModelRoutingMember{{ID: "fast", ServiceID: "service_01", Model: "fast-model"}, {ID: "strong", ServiceID: "endpoint_test", Model: "strong-model"}},
		DefaultMemberID: "fast",
		Rules:           []contract.ModelRoutingRule{{ID: "stream", Priority: 1, When: contract.ModelRoutingCondition{Field: "streaming", EqualsBool: boolRef(true)}, TargetMemberID: "strong"}},
	}}
	request := Request{Protocol: contract.ProtocolOpenAIResponses, Model: "astrlink/superclaude", Streaming: true}
	session := newRecordSession(request, "", contract.AuditSettings{})
	got, err := New().applyModelRouting(context.Background(), session, request, settings)
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelRoutingMember == nil || got.ModelRoutingMember.Model != "strong-model" {
		t.Fatalf("member = %+v", got.ModelRoutingMember)
	}
	if got.RedirectedModel != "" {
		t.Fatalf("virtual group set redirect model %q", got.RedirectedModel)
	}
	if session.modelRouting == nil || session.modelRouting.SelectionSource != contract.ModelRoutingSelectionRule {
		t.Fatalf("decision = %+v", session.modelRouting)
	}
}

func boolRef(value bool) *bool { return &value }

func TestModelRoutingFinalRoundTripUsesConcreteModelWithoutGatewayIdentity(t *testing.T) {
	store := newRedirectSettingsStore()
	store.settings.ModelRoutingGroups = []contract.ModelRoutingGroup{{ID: "group-x", Enabled: true, Members: []contract.ModelRoutingMember{{ID: "member-x", ServiceID: "endpoint_test", Model: "strong-model"}}, DefaultMemberID: "member-x", Rules: []contract.ModelRoutingRule{}}}
	var sent *http.Request
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
			if request.Model != "strong-model" {
				t.Fatalf("resolved model = %q", request.Model)
			}
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false), UpstreamModel: request.Model}, nil
		}),
		RequestRecords: store,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			sent = request.Clone(request.Context())
			body, _ := io.ReadAll(request.Body)
			if string(body) != `{"model":"strong-model","input":"hi"}` {
				t.Errorf("upstream body = %q", body)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"model":"strong-model"}`))}, nil
		})),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"astrlink/group-x","input":"hi"}`))
	req.Header.Set("X-AstrLink-Routing-Debug", "local-only")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if sent == nil {
		t.Fatal("no final RoundTrip request")
	}
	for name := range sent.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-astrlink-") {
			t.Fatalf("gateway header forwarded: %s", name)
		}
	}
	if sent.Header.Get("Via") != "" || sent.Header.Get("X-Powered-By") != "" || sent.Header.Get("Originator") != "" {
		t.Fatalf("gateway identity forwarded: %+v", sent.Header)
	}
	record := store.root(t)
	if record.ModelRouting == nil || record.ModelRouting.MemberModel != "strong-model" {
		t.Fatalf("model routing record = %+v", record.ModelRouting)
	}
}

type modelRoutingBindingTestStore struct {
	*redirectSettingsStore
	mu          sync.Mutex
	binding     *contract.ModelRoutingBinding
	getCalls    int
	upsertCalls int
	deleteCalls int
	started     time.Time
}

func (store *modelRoutingBindingTestStore) GetModelRoutingBinding(_ context.Context, principal string, sessionID contract.SessionID, protocol contract.ProtocolID, virtualModelID string) (contract.ModelRoutingBinding, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.getCalls++
	if store.binding == nil || store.binding.Principal != principal || store.binding.SessionID != sessionID || store.binding.Protocol != protocol || store.binding.VirtualModelID != virtualModelID {
		return contract.ModelRoutingBinding{}, storage.ErrNotFound
	}
	return *store.binding, nil
}

func (store *modelRoutingBindingTestStore) UpsertModelRoutingBinding(ctx context.Context, binding contract.ModelRoutingBinding, started time.Time) error {
	// The SQLite store refuses a cancelled context before it writes.
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.upsertCalls++
	store.started = started
	copy := binding
	store.binding = &copy
	return nil
}

func (store *modelRoutingBindingTestStore) DeleteModelRoutingBinding(_ context.Context, principal string, sessionID contract.SessionID, protocol contract.ProtocolID, virtualModelID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.deleteCalls++
	if store.binding != nil && store.binding.Principal == principal && store.binding.SessionID == sessionID && store.binding.Protocol == protocol && store.binding.VirtualModelID == virtualModelID {
		store.binding = nil
	}
	return nil
}

// A client may hang up as soon as its response is delivered; the session
// binding is still written.
func TestModelRoutingBindingOutlivesTheClientConnection(t *testing.T) {
	store := &modelRoutingBindingTestStore{redirectSettingsStore: newRedirectSettingsStore()}
	handler := &Handler{requestRecords: store}
	request := Request{Protocol: contract.ProtocolOpenAIResponses, Model: "astrlink/group-x"}
	session := newRecordSession(request, "", contract.AuditSettings{})
	session.sessionID = "session-x"
	session.status = contract.RequestStatusSucceeded
	settings := contract.DefaultRoutingSettings()
	session.routingSettings = &settings
	token := contract.AccessTokenID("access-token-x")
	session.accessTokenID = &token
	session.modelRouting = &contract.ModelRoutingDecision{
		VirtualModelID: request.Model, GroupID: "group-x", MemberID: "member-x",
		MemberServiceID: "service_01", MemberModel: "model-x",
	}
	gone, hangUp := context.WithCancel(context.Background())
	hangUp()

	handler.persistModelRoutingBinding(gone, session)
	if store.upsertCalls != 1 || store.binding == nil || store.binding.MemberID != "member-x" {
		t.Fatalf("binding after the client hung up = calls %d, binding %+v", store.upsertCalls, store.binding)
	}
	if session.modelRouting.BindingExpiresAt == "" {
		t.Fatal("decision does not say when the binding expires")
	}
}

func TestModelRoutingBindingRequiresAccessToken(t *testing.T) {
	store := &modelRoutingBindingTestStore{redirectSettingsStore: newRedirectSettingsStore()}
	handler := &Handler{requestRecords: store}
	request := Request{Protocol: contract.ProtocolOpenAIResponses, Model: "astrlink/group-x"}
	session := newRecordSession(request, "", contract.AuditSettings{})
	session.sessionID = "session-x"
	session.status = contract.RequestStatusSucceeded
	settings := contract.DefaultRoutingSettings()
	session.routingSettings = &settings
	session.modelRouting = &contract.ModelRoutingDecision{
		VirtualModelID: request.Model, GroupID: "group-x", MemberID: "member-x",
		MemberServiceID: "service_01", MemberModel: "model-x",
	}

	handler.persistModelRoutingBinding(context.Background(), session)
	if store.upsertCalls != 0 {
		t.Fatalf("anonymous binding writes = %d, want 0", store.upsertCalls)
	}
	if _, found, err := handler.loadModelRoutingBinding(context.Background(), session, request); err != nil || found {
		t.Fatalf("anonymous binding load = found %v, err %v; want no lookup", found, err)
	}
	if store.getCalls != 0 {
		t.Fatalf("anonymous binding reads = %d, want 0", store.getCalls)
	}

	token := contract.AccessTokenID("access-token-x")
	session.accessTokenID = &token
	handler.persistModelRoutingBinding(context.Background(), session)
	if store.upsertCalls != 1 || store.binding == nil {
		t.Fatalf("token binding write = calls %d, binding %+v", store.upsertCalls, store.binding)
	}
	if store.binding.Principal != string(token) {
		t.Fatalf("binding principal = %q, want %q", store.binding.Principal, token)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, store.binding.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if remaining := time.Until(expiresAt); remaining < time.Hour-time.Second || remaining > time.Hour+time.Second {
		t.Fatalf("binding TTL = %s, want about the default hour", remaining)
	}
	if _, found, err := handler.loadModelRoutingBinding(context.Background(), session, request); err != nil || !found {
		t.Fatalf("token binding load = found %v, err %v; want hit", found, err)
	}
}

func TestModelRoutingRecordsClassifierIDOnTaskTypeObservation(t *testing.T) {
	classifierID := contract.AutoClassifierID("classifier_ab0123456789abcdef0123456789abcd")
	handler := New()
	handler.classifier = ClassifierFunc(func(context.Context, string) autoclassifier.Outcome {
		return autoclassifier.Outcome{Category: "coding", ClassifierID: classifierID}
	})
	settings := contract.DefaultRoutingSettings()
	settings.ModelRoutingGroups = []contract.ModelRoutingGroup{{
		ID: "superclaude", Enabled: true,
		Members:         []contract.ModelRoutingMember{{ID: "fast", ServiceID: "service_01", Model: "fast-model"}},
		DefaultMemberID: "fast",
		Rules: []contract.ModelRoutingRule{{
			ID: "coding", Priority: 1,
			When:           contract.ModelRoutingCondition{Field: "task_type", In: []string{"coding"}},
			TargetMemberID: "fast",
		}},
	}}
	request := Request{Protocol: contract.ProtocolOpenAIResponses, Model: "astrlink/superclaude", lastUserText: "write code"}
	session := newRecordSession(request, "", contract.AuditSettings{})
	if _, err := handler.applyModelRouting(context.Background(), session, request, settings); err != nil {
		t.Fatal(err)
	}
	if session.modelRouting == nil || len(session.modelRouting.Fields) != 1 {
		t.Fatalf("model routing decision = %+v", session.modelRouting)
	}
	if got := session.modelRouting.Fields[0].ClassifierID; got != string(classifierID) {
		t.Fatalf("classifier_id = %q, want %q", got, classifierID)
	}
}

// The classifier's ranked output must land on the task_type observation as
// candidates (W6): rank is the substitution position, chosen marks the entry
// the evaluator adopted, and the backfill only moves the one existing
// classification outcome — the classifier runs exactly once no matter how
// many candidates the row walks.
func TestModelRoutingRecordsAdoptedTaskTypeCandidates(t *testing.T) {
	classifierID := contract.AutoClassifierID("classifier_ab0123456789abcdef0123456789abcd")
	calls := 0
	handler := New()
	handler.classifier = ClassifierFunc(func(context.Context, string) autoclassifier.Outcome {
		calls++
		return autoclassifier.Outcome{
			Category: "coding", Confidence: 0.81, ClassifierID: classifierID,
			Candidates: []autoclassifier.Candidate{
				{Label: "coding", Score: 0.81},
				{Label: "general", Score: 0.64},
			},
		}
	})
	settings := contract.DefaultRoutingSettings()
	settings.ModelRoutingGroups = []contract.ModelRoutingGroup{{
		ID: "superclaude", Enabled: true,
		Members:         []contract.ModelRoutingMember{{ID: "fast", ServiceID: "service_01", Model: "fast-model"}},
		DefaultMemberID: "fast",
		Rules: []contract.ModelRoutingRule{{
			ID: "any", Priority: 1,
			When:           contract.ModelRoutingCondition{Field: "task_type", In: []string{"coding", "general"}},
			TargetMemberID: "fast",
		}},
	}}
	request := Request{Protocol: contract.ProtocolOpenAIResponses, Model: "astrlink/superclaude", lastUserText: "write code"}
	session := newRecordSession(request, "", contract.AuditSettings{})
	if _, err := handler.applyModelRouting(context.Background(), session, request, settings); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("classifier calls = %d, want 1: candidate substitution reuses the one outcome", calls)
	}
	if session.modelRouting == nil || len(session.modelRouting.Fields) != 1 {
		t.Fatalf("model routing decision = %+v", session.modelRouting)
	}
	want := []contract.ModelRoutingCandidateObservation{
		{Value: "coding", Score: 0.81, Rank: 0, Chosen: true},
		{Value: "general", Score: 0.64, Rank: 1, Chosen: false},
	}
	if got := session.modelRouting.Fields[0].Candidates; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %+v, want %+v", got, want)
	}
	encoded, err := json.Marshal(session.modelRouting)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"chosen":false`) {
		t.Fatalf("chosen:false must stay on the wire rather than being omitted: %s", encoded)
	}
	var roundTrip contract.ModelRoutingDecision
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTrip.Fields[0].Candidates, want) {
		t.Fatalf("candidates changed on round trip: %+v", roundTrip.Fields[0].Candidates)
	}
}

// When the row only matches on a lower-ranked candidate, chosen moves with
// the adopted rank: the top entry keeps its score and rank but is recorded
// as eliminated (chosen false), not silently reordered or dropped.
func TestModelRoutingMarksNonTopCandidateChosen(t *testing.T) {
	handler := New()
	handler.classifier = ClassifierFunc(func(context.Context, string) autoclassifier.Outcome {
		return autoclassifier.Outcome{
			Category: "coding", Confidence: 0.81,
			Candidates: []autoclassifier.Candidate{
				{Label: "coding", Score: 0.81},
				{Label: "general", Score: 0.64},
			},
		}
	})
	settings := contract.DefaultRoutingSettings()
	settings.ModelRoutingGroups = []contract.ModelRoutingGroup{{
		ID: "superclaude", Enabled: true,
		Members:         []contract.ModelRoutingMember{{ID: "fast", ServiceID: "service_01", Model: "fast-model"}},
		DefaultMemberID: "fast",
		Rules: []contract.ModelRoutingRule{{
			ID: "general", Priority: 1,
			When:           contract.ModelRoutingCondition{Field: "task_type", Equals: "general"},
			TargetMemberID: "fast",
		}},
	}}
	request := Request{Protocol: contract.ProtocolOpenAIResponses, Model: "astrlink/superclaude", lastUserText: "hello there"}
	session := newRecordSession(request, "", contract.AuditSettings{})
	if _, err := handler.applyModelRouting(context.Background(), session, request, settings); err != nil {
		t.Fatal(err)
	}
	if session.modelRouting == nil || session.modelRouting.RuleID != "general" {
		t.Fatalf("model routing decision = %+v, want the general rule selected", session.modelRouting)
	}
	want := []contract.ModelRoutingCandidateObservation{
		{Value: "coding", Score: 0.81, Rank: 0, Chosen: false},
		{Value: "general", Score: 0.64, Rank: 1, Chosen: true},
	}
	if got := session.modelRouting.Fields[0].Candidates; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %+v, want %+v", got, want)
	}
}

// A single-value classifier answer (the bundled worker shape: no ranked
// candidates, zero confidence) still records the one label it contributed,
// with the same score the threshold filter saw.
func TestModelRoutingRecordsSynthesizedSingleCandidate(t *testing.T) {
	handler := New()
	handler.classifier = ClassifierFunc(func(context.Context, string) autoclassifier.Outcome {
		return autoclassifier.Outcome{Category: "coding"}
	})
	settings := contract.DefaultRoutingSettings()
	settings.ModelRoutingGroups = []contract.ModelRoutingGroup{{
		ID: "superclaude", Enabled: true,
		Members:         []contract.ModelRoutingMember{{ID: "fast", ServiceID: "service_01", Model: "fast-model"}},
		DefaultMemberID: "fast",
		Rules: []contract.ModelRoutingRule{{
			ID: "coding", Priority: 1,
			When:           contract.ModelRoutingCondition{Field: "task_type", In: []string{"coding"}},
			TargetMemberID: "fast",
		}},
	}}
	request := Request{Protocol: contract.ProtocolOpenAIResponses, Model: "astrlink/superclaude", lastUserText: "write code"}
	session := newRecordSession(request, "", contract.AuditSettings{})
	if _, err := handler.applyModelRouting(context.Background(), session, request, settings); err != nil {
		t.Fatal(err)
	}
	want := []contract.ModelRoutingCandidateObservation{{Value: "coding", Score: 0, Rank: 0, Chosen: true}}
	if session.modelRouting == nil || !reflect.DeepEqual(session.modelRouting.Fields[0].Candidates, want) {
		t.Fatalf("candidates = %+v, want the synthesized single candidate %+v", session.modelRouting, want)
	}
}

// taskTypeCandidateObservations is the observation-side twin of the fact
// conversion: identical ordering and filters, plus rank/chosen bookkeeping.
// Fallback answers never contribute candidates, and a threshold that drops
// every ranked candidate leaves the field candidate-free.
func TestTaskTypeCandidateObservationsMatchFactFilters(t *testing.T) {
	group := contract.ModelRoutingGroup{TaskTypeLabels: []string{"coding", "general"}}
	ranked := []autoclassifier.Candidate{
		{Label: "coding", Score: 0.81},
		{Label: "general", Score: 0.64},
		{Label: "research", Score: 0.4},
	}
	outcome := modelrouting.TaskTypeOutcome{Value: "coding", Confidence: 0.81, Candidates: ranked}

	if got := modelrouting.TaskTypeCandidateLabels(outcome, group); !reflect.DeepEqual(got, []string{"coding", "general"}) {
		t.Fatalf("labels = %v, want threshold- and domain-filtered sequence", got)
	}
	adopted := 0
	want := []contract.ModelRoutingCandidateObservation{
		{Value: "coding", Score: 0.81, Rank: 0, Chosen: true},
		{Value: "general", Score: 0.64, Rank: 1, Chosen: false},
	}
	if got := modelrouting.TaskTypeCandidateObservations(outcome, group, &adopted); !reflect.DeepEqual(got, want) {
		t.Fatalf("observations = %+v, want %+v", got, want)
	}

	// No adoption (the row fell through every candidate): the sequence is
	// still recorded, with no chosen marker — never dropped, never all-true.
	if got := modelrouting.TaskTypeCandidateObservations(outcome, group, nil); got[0].Chosen || got[1].Chosen {
		t.Fatalf("observations without adoption must carry no chosen entry: %+v", got)
	}

	// A recorded fallback code suppresses candidates even when a fallback
	// value is present: a fallback is not a measurement.
	fallback := outcome
	fallback.Value = "general"
	fallback.Code = "classifier_unavailable"
	if got := modelrouting.TaskTypeCandidateObservations(fallback, group, &adopted); got != nil {
		t.Fatalf("fallback candidates = %+v, want none", got)
	}
	if got := modelrouting.TaskTypeCandidateLabels(fallback, group); got != nil {
		t.Fatalf("fallback labels = %v, want none", got)
	}

	// Threshold filtering runs per-candidate before ranking observations.
	strict := outcome
	strict.LowConfidenceThreshold = 0.7
	if got := modelrouting.TaskTypeCandidateObservations(strict, group, nil); len(got) != 1 || got[0].Value != "coding" {
		t.Fatalf("thresholded observations = %+v, want only coding", got)
	}

	// Duplicates collapse onto the first (highest-ranked) occurrence.
	duplicated := modelrouting.TaskTypeOutcome{Candidates: []autoclassifier.Candidate{
		{Label: "coding", Score: 0.81},
		{Label: "coding", Score: 0.7},
		{Label: "general", Score: 0.64},
	}}
	if got := modelrouting.TaskTypeCandidateObservations(duplicated, group, nil); len(got) != 2 || got[0].Score != 0.81 {
		t.Fatalf("deduped observations = %+v, want first coding occurrence kept", got)
	}
}

// A handler built without an explicit Classifier dependency must keep the
// classifier nil: construction of the worker is production wiring owned by
// the Core entry point, never an implicit side effect of the ingress seam.
// This is the behavioral counterexample for "production never wired the
// classifier" and compiles whether or not that wiring exists.
func TestNewWithDependenciesLeavesClassifierUnset(t *testing.T) {
	handler := NewWithDependencies(Dependencies{MaxRequestBodyMiB: 1})
	if handler.classifier != nil {
		t.Fatalf("classifier = %T, want nil without an explicit dependency", handler.classifier)
	}
}

// The Dependencies injection path must actually reach the handler field the
// routing evaluator reads; existing tests exercise classification behavior
// but never assert the injection result itself.
func TestProductionDependenciesClassifierReachesHandler(t *testing.T) {
	handler := NewWithDependencies(Dependencies{
		Classifier: ClassifierFunc(func(context.Context, string) autoclassifier.Outcome {
			return autoclassifier.Outcome{Category: "coding"}
		}),
	})
	if handler.classifier == nil {
		t.Fatal("classifier dependency did not reach the handler")
	}
}

// Without a classifier a task_type rule evaluates to unknown and is skipped:
// the default member wins and the decision records the deterministic
// classifier_unavailable fallback instead of failing the request.
func TestModelRoutingFallsBackDeterministicallyWithoutClassifier(t *testing.T) {
	handler := New()
	settings := contract.DefaultRoutingSettings()
	settings.ModelRoutingGroups = []contract.ModelRoutingGroup{{
		ID: "superclaude", Enabled: true,
		Members:         []contract.ModelRoutingMember{{ID: "fast", ServiceID: "service_01", Model: "fast-model"}, {ID: "strong", ServiceID: "endpoint_test", Model: "strong-model"}},
		DefaultMemberID: "fast",
		Rules: []contract.ModelRoutingRule{{
			ID: "coding", Priority: 1,
			When:           contract.ModelRoutingCondition{Field: "task_type", In: []string{"coding"}},
			TargetMemberID: "strong",
		}},
	}}
	request := Request{Protocol: contract.ProtocolOpenAIResponses, Model: "astrlink/superclaude", routingUserText: "write code", routingUserTextRunes: 10, routingUserTextKnown: true}
	session := newRecordSession(request, "", contract.AuditSettings{})
	got, err := handler.applyModelRouting(context.Background(), session, request, settings)
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelRoutingMember == nil || got.ModelRoutingMember.ID != "fast" {
		t.Fatalf("member = %+v, want the default member fast", got.ModelRoutingMember)
	}
	if session.modelRouting == nil || !session.modelRouting.UsedDefault || session.modelRouting.RuleID != "" {
		t.Fatalf("decision = %+v, want the task_type rule skipped for the default member", session.modelRouting)
	}
	if session.modelRouting.FallbackReason != autoclassifier.FallbackUnavailable {
		t.Fatalf("fallback reason = %q, want %q", session.modelRouting.FallbackReason, autoclassifier.FallbackUnavailable)
	}
}

// With a redacting privacy policy active, the classifier that feeds task_type
// facts must never see the raw sensitive text: routing classifies only the
// redacted view, and the provider attempt redacts the body it sends.
func TestModelRoutingClassifierReceivesSanitizedText(t *testing.T) {
	store := newRedirectSettingsStore()
	store.settings.ModelRoutingGroups = []contract.ModelRoutingGroup{{
		ID: "superclaude", Enabled: true,
		Members:         []contract.ModelRoutingMember{{ID: "fast", ServiceID: "endpoint_test", Model: "fast-model"}},
		DefaultMemberID: "fast",
		Rules: []contract.ModelRoutingRule{{
			ID: "coding", Priority: 1,
			When:           contract.ModelRoutingCondition{Field: "task_type", In: []string{"coding"}},
			TargetMemberID: "fast",
		}},
	}}
	var classifierTexts []string
	var classifierMu sync.Mutex
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false), UpstreamModel: "fast-model"}, nil
		}),
		RequestRecords: store,
		PrivacyFilter:  testPrivacyEngine(t, privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact}, nil),
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(request.Body)
			if strings.Contains(string(body), "alice@example.com") {
				t.Errorf("upstream received unredacted body: %q", body)
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{"model":"fast-model"}`)),
			}, nil
		})),
	})
	handler.classifier = ClassifierFunc(func(_ context.Context, text string) autoclassifier.Outcome {
		classifierMu.Lock()
		classifierTexts = append(classifierTexts, text)
		classifierMu.Unlock()
		return autoclassifier.Outcome{Category: "coding"}
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses",
		strings.NewReader(`{"model":"astrlink/superclaude","input":"write go code for alice@example.com now"}`))
	req.Header.Set("X-AstrLink-Routing-Debug", "local-only")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	classifierMu.Lock()
	defer classifierMu.Unlock()
	if len(classifierTexts) == 0 {
		t.Fatal("classifier was never called; the task_type rule needed a classification")
	}
	for _, text := range classifierTexts {
		if strings.Contains(text, "alice@example.com") {
			t.Fatalf("classifier received unredacted text: %q", text)
		}
	}
}

func TestModelRoutingCustomTaskTypeLabelsRejectBundledClassifierOutput(t *testing.T) {
	handler := New()
	handler.classifier = ClassifierFunc(func(context.Context, string) autoclassifier.Outcome {
		return autoclassifier.Outcome{Category: "coding"}
	})
	settings := contract.DefaultRoutingSettings()
	settings.ModelRoutingGroups = []contract.ModelRoutingGroup{{
		ID: "custom", Enabled: true,
		Members:         []contract.ModelRoutingMember{{ID: "fast", ServiceID: "service_01", Model: "fast-model"}},
		DefaultMemberID: "fast",
		TaskTypeLabels:  []string{"storyboard"},
		Rules: []contract.ModelRoutingRule{{
			ID: "storyboard", Priority: 1,
			When: contract.ModelRoutingCondition{Field: "task_type", In: []string{"storyboard"}}, TargetMemberID: "fast",
		}},
	}}
	request := Request{Protocol: contract.ProtocolOpenAIResponses, Model: "astrlink/custom", lastUserText: "draw a storyboard"}
	session := newRecordSession(request, "", contract.AuditSettings{})
	if _, err := handler.applyModelRouting(context.Background(), session, request, settings); err != nil {
		t.Fatal(err)
	}
	if session.modelRouting == nil || !session.modelRouting.UsedDefault || session.modelRouting.RuleID != "" {
		t.Fatalf("custom classifier output selected a rule: %+v", session.modelRouting)
	}
	if session.modelRouting.FallbackReason != "classifier_invalid_output" {
		t.Fatalf("fallback reason = %q, want classifier_invalid_output", session.modelRouting.FallbackReason)
	}
}
