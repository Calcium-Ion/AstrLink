package ingress

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestRequestRecordsExplainWhyALowerPriorityProviderServed(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "routing-decision.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// Priority order: a provider without the model, a disabled one, one that
	// fails, then the provider that serves.
	for _, service := range []struct {
		id      string
		enabled bool
		model   string
	}{
		{"service_mly", true, "other"},
		{"service_codex", false, "public"},
		{"service_backup", true, "public"},
		{"service_newapi", true, "public"},
	} {
		ep := validEndpoint(contract.ProtocolOpenAIChat, false)
		ep.ID, ep.Name, ep.Enabled, ep.Models = contract.ServiceID(service.id), service.id, service.enabled, []string{service.model}
		ep.BaseURL = "https://" + strings.TrimPrefix(service.id, "service_") + ".example"
		ep.Capabilities = append(ep.Capabilities, contract.Capability{Protocol: contract.ProtocolOpenAIModels, Mode: contract.CapabilityModeNative})
		if _, err := store.CreateEndpoint(ctx, ep, storage.CredentialMutation{Present: true, Secret: []byte("test-secret")}); err != nil {
			t.Fatal(err)
		}
	}
	order, err := store.GetServiceOrder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateServiceOrder(ctx, contract.ServiceOrder{ServiceIDs: []contract.ServiceID{
		"service_mly", "service_codex", "service_backup", "service_newapi",
	}}, order.ETag); err != nil {
		t.Fatal(err)
	}
	resolver, err := endpoint.NewStoreResolver(store)
	if err != nil {
		t.Fatal(err)
	}
	failBackup := true
	answer := "The first answer is long enough to be recognized through the conversation fingerprint."
	handler := NewWithDependencies(Dependencies{Resolver: resolver, Authorizer: endpoint.NewSecretAuthorizer(store), RequestRecords: store, AuditBlobs: store, Forwarder: transport.New(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "backup.example" && failBackup {
			return jsonResponse(503, `{"error":{"message":"unavailable"}}`), nil
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": answer}}}})
		return jsonResponse(200, string(raw)), nil
	}))})
	serve := func(body string) contract.RequestRecord {
		t.Helper()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
		page, err := store.ListRequestRecords(ctx, storage.RequestRecordListOptions{Limit: 1})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("records %+v %v", page, err)
		}
		return page.Items[0]
	}
	follow := func(opening string) string {
		return `{"model":"public","messages":[{"role":"user","content":"` + opening + `"},{"role":"assistant","content":"` + answer + `"},{"role":"user","content":"continue"}]}`
	}
	check := func(name string, record contract.RequestRecord, service contract.ServiceID, selected contract.RoutingSelection, skipped []contract.RoutingSkip) {
		t.Helper()
		if record.ServiceID == nil || *record.ServiceID != service {
			t.Fatalf("%s served by %v, want %s", name, record.ServiceID, service)
		}
		if want := (&contract.RequestRoutingDecision{Selected: selected, Skipped: skipped}); !reflect.DeepEqual(record.RoutingDecision, want) {
			t.Fatalf("%s decision = %+v, want %+v", name, record.RoutingDecision, want)
		}
	}
	ahead := []contract.RoutingSkip{
		{ServiceID: "service_mly", Reason: contract.RoutingSkipModelNotListed},
		{ServiceID: "service_codex", Reason: contract.RoutingSkipDisabled},
	}

	root := serve(`{"model":"public","messages":[{"role":"user","content":"hello"}]}`)
	check("failover", root, "service_newapi", contract.RoutingSelectionFailover, ahead)
	children, err := store.ListRequestRecordChildren(ctx, root.ID)
	if err != nil || len(children) != 1 {
		t.Fatalf("children %+v %v", children, err)
	}
	check("failed attempt", children[0], "service_backup", contract.RoutingSelectionPriority, ahead)

	// The backup recovers, but the conversation stays where it was answered.
	failBackup = false
	check("session binding", serve(follow("hello")), "service_newapi", contract.RoutingSelectionSessionBinding, ahead)

	// A binding to the provider priority picks anyway overrides nothing.
	answer = "The second answer is long enough to be recognized through the conversation fingerprint."
	check("priority", serve(`{"model":"public","messages":[{"role":"user","content":"a new conversation"}]}`), "service_backup", contract.RoutingSelectionPriority, ahead)
	check("binding to the first eligible", serve(follow("a new conversation")), "service_backup", contract.RoutingSelectionPriority, ahead)

	// Without an eligible provider the decision explains every exclusion.
	root = serve(`{"model":"missing","messages":[{"role":"user","content":"hi"}]}`)
	want := &contract.RequestRoutingDecision{Skipped: []contract.RoutingSkip{
		{ServiceID: "service_mly", Reason: contract.RoutingSkipModelNotListed},
		{ServiceID: "service_codex", Reason: contract.RoutingSkipDisabled},
		{ServiceID: "service_backup", Reason: contract.RoutingSkipModelNotListed},
		{ServiceID: "service_newapi", Reason: contract.RoutingSkipModelNotListed},
	}}
	if root.Status != contract.RequestStatusFailed || root.ServiceID != nil || !reflect.DeepEqual(root.RoutingDecision, want) {
		t.Fatalf("no provider: %s %v %+v", root.Status, root.ServiceID, root.RoutingDecision)
	}

	// Discovery lists every provider's models instead of choosing one.
	discovery := newRecordSession(Request{Protocol: contract.ProtocolOpenAIModels}, "", contract.DefaultAuditSettings())
	listed, err := handler.resolveCandidates(withRecordSession(ctx, discovery), endpoint.ResolveRequest{Protocol: contract.ProtocolOpenAIModels})
	if err != nil || len(listed) != 3 || discovery.routing != nil {
		t.Fatalf("discovery listed %d, routing = %+v, err = %v", len(listed), discovery.routing, err)
	}
}

func TestRoutingDecisionFollowsTheCurrentAttempt(t *testing.T) {
	session := newRecordSession(Request{Protocol: contract.ProtocolOpenAIResponses, Model: "public"}, "", contract.DefaultAuditSettings())
	if session.routingDecision() != nil {
		t.Fatal("a resolver without a ranking produced a decision")
	}
	ranking := []endpoint.RankedService{
		{ServiceID: "service_disabled", Skip: contract.RoutingSkipDisabled},
		{ServiceID: "service_first"},
		{ServiceID: "service_socket"},
		{ServiceID: "service_last"},
	}
	session.noteRoutingRanking(ranking)
	ranking[0].Skip = ""
	if session.routingDecision() != nil {
		t.Fatal("a pending request without a provider has a decision")
	}
	session.noteRoutingSkip("service_socket", contract.RoutingSkipWebSocketUnsupported)
	session.noteRoutingSkip("service_socket", contract.RoutingSkipCircuitOpen)
	attempt := func(id contract.ServiceID) {
		session.noteRoutingTried(id)
		session.endpointID = &id
	}
	expect := func(name string, selected contract.RoutingSelection, skipped ...contract.ServiceID) {
		t.Helper()
		record := session.recordSnapshot(nil, nil)
		if err := record.Validate(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if record.RoutingDecision == nil || record.RoutingDecision.Selected != selected || len(record.RoutingDecision.Skipped) != len(skipped) {
			t.Fatalf("%s decision = %+v", name, record.RoutingDecision)
		}
		for index, id := range skipped {
			if record.RoutingDecision.Skipped[index].ServiceID != id {
				t.Fatalf("%s skipped = %+v", name, record.RoutingDecision.Skipped)
			}
		}
	}
	attempt("service_first")
	expect("first eligible", contract.RoutingSelectionPriority, "service_disabled")
	session.noteRoutingPin(contract.RoutingSelectionResponseAffinity, "service_first")
	expect("pinned", contract.RoutingSelectionResponseAffinity, "service_disabled")
	if skip := session.recordSnapshot(nil, nil).RoutingDecision.Skipped[0]; skip.Reason != contract.RoutingSkipDisabled {
		t.Fatalf("the session shares the resolver's ranking: %+v", skip)
	}

	// Attempts share the trace; retrying the first provider is not failover.
	session.resetAttemptLocal()
	if session.routing == nil || session.routingDecision() != nil {
		t.Fatalf("reset trace = %+v", session.routing)
	}
	attempt("service_first")
	expect("retry", contract.RoutingSelectionResponseAffinity, "service_disabled")
	attempt("service_last")
	expect("failover", contract.RoutingSelectionFailover, "service_disabled", "service_socket")
	if got := session.routingDecision().Skipped[1].Reason; got != contract.RoutingSkipWebSocketUnsupported {
		t.Fatalf("later skip replaced the first reason: %s", got)
	}

	// A finished request without a provider explains every exclusion.
	session.endpointID = nil
	session.noteFailed(errorSummaryFromInference("responses_websocket_unavailable", "unavailable", false))
	expect("no provider", "", "service_disabled", "service_socket")

	many := make([]endpoint.RankedService, 0, contract.MaxRoutingSkips+2)
	for index := range contract.MaxRoutingSkips + 1 {
		many = append(many, endpoint.RankedService{ServiceID: contract.ServiceID(fmt.Sprintf("service_%02d", index)), Skip: contract.RoutingSkipDisabled})
	}
	session.noteRoutingRanking(append(many, endpoint.RankedService{ServiceID: "service_served"}))
	attempt("service_served")
	if record := session.recordSnapshot(nil, nil); len(record.RoutingDecision.Skipped) != contract.MaxRoutingSkips || record.Validate() != nil {
		t.Fatalf("capped decision kept %d skips", len(record.RoutingDecision.Skipped))
	}
}

func TestResponseAffinityPinsTheContinuedProvider(t *testing.T) {
	handler := NewWithDependencies(Dependencies{})
	candidates := recoveryCandidates(2, contract.DefaultFailurePolicy(), contract.DefaultFailoverPolicy())
	bound := candidates[1].CanonicalService().ID
	handler.affinities.entries = map[affinityKey]affinityEntry{{"", "resp_previous"}: {
		binding: storage.ResponseAffinity{ServiceID: bound, UpstreamModel: "public", UpstreamProtocol: contract.ProtocolOpenAIResponses, PlanType: contract.PlanTypeNative},
		at:      time.Now(),
	}}
	request := Request{Protocol: contract.ProtocolOpenAIResponses, Model: "public", PreviousResponseID: "resp_previous"}
	session := newRecordSession(request, "", contract.DefaultAuditSettings())
	session.noteRoutingRanking([]endpoint.RankedService{{ServiceID: candidates[0].CanonicalService().ID}, {ServiceID: bound}})
	got, err := handler.bindResponseAffinity(withRecordSession(context.Background(), session), request, candidates)
	if err != nil || len(got) != 1 || got[0].CanonicalService().ID != bound {
		t.Fatalf("affinity = %+v, %v", got, err)
	}
	session.noteRoutingTried(bound)
	session.endpointID = &bound
	if decision := session.routingDecision(); decision.Selected != contract.RoutingSelectionResponseAffinity || len(decision.Skipped) != 0 {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestWebSocketSkipNamesWhyAProviderCannotServeTheTurn(t *testing.T) {
	disabled := false
	for _, tt := range []struct {
		name   string
		change func(*endpoint.Resolved)
		want   contract.RoutingSkipReason
	}{
		{"native", func(*endpoint.Resolved) {}, ""},
		{"disabled provider", func(candidate *endpoint.Resolved) { candidate.Service.Enabled = false }, contract.RoutingSkipDisabled},
		{"websocket off", func(candidate *endpoint.Resolved) { candidate.Service.ResponsesWebSocketEnabled = &disabled }, contract.RoutingSkipWebSocketDisabled},
		{"converted", func(candidate *endpoint.Resolved) { candidate.PlanType = contract.PlanTypeRelayKit }, contract.RoutingSkipWebSocketUnsupported},
		{"other upstream protocol", func(candidate *endpoint.Resolved) { candidate.UpstreamProtocol = contract.ProtocolOpenAIChat }, contract.RoutingSkipWebSocketUnsupported},
		{"no streaming responses", func(candidate *endpoint.Resolved) { candidate.Service.Capabilities[0].Streaming = false }, contract.RoutingSkipWebSocketUnsupported},
	} {
		t.Run(tt.name, func(t *testing.T) {
			candidate := wsCandidate("https://upstream.example")
			tt.change(&candidate)
			if got := websocketSkip(candidate, "public"); got != tt.want {
				t.Fatalf("skip = %q, want %q", got, tt.want)
			}
			if kept := (&responsesWSSession{}).filterCandidates("public", "public", []endpoint.Resolved{candidate}); (len(kept) == 1) != (tt.want == "") {
				t.Fatalf("filter kept %d", len(kept))
			}
		})
	}
}
