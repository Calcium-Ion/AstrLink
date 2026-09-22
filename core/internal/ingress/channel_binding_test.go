package ingress

import (
	"context"
	"encoding/json"
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

func TestChannelStickinessReusesConvoFingerprintAndReleases(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "sticky.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings, err := store.GetRoutingSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		ep := validEndpoint(contract.ProtocolOpenAIChat, false)
		ep.ID = contract.ServiceID("service_" + id)
		ep.Name = id
		ep.BaseURL = "https://" + id + ".example"
		ep.Models = []string{"public"}
		if _, err := store.CreateEndpoint(ctx, ep, storage.CredentialMutation{Present: true, Secret: []byte("test-secret")}); err != nil {
			t.Fatal(err)
		}
	}
	resolver, err := endpoint.NewStoreResolver(store)
	if err != nil {
		t.Fatal(err)
	}
	var attempts []string
	failA := true
	failB := false
	answer := "This assistant response is long enough to be recognized through the existing conversation fingerprint."
	handler := NewWithDependencies(Dependencies{Resolver: resolver, Authorizer: endpoint.NewSecretAuthorizer(store), RequestRecords: store, AuditBlobs: store, Forwarder: transport.New(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts = append(attempts, r.URL.Host)
		if (r.URL.Host == "a.example" && failA) || (r.URL.Host == "b.example" && failB) {
			return jsonResponse(503, `{"error":{"message":"unavailable"}}`), nil
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": answer}}}})
		return jsonResponse(200, string(raw)), nil
	}))})
	serve := func(body string) int {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
		return response.Code
	}
	first := `{"model":"public","messages":[{"role":"user","content":"hello"}]}`
	if status := serve(first); status != 200 {
		t.Fatalf("first: %d", status)
	}
	if !reflect.DeepEqual(attempts, []string{"a.example", "b.example"}) {
		t.Fatalf("failover %v", attempts)
	}
	page, _ := store.ListRequestSessions(ctx, storage.RequestSessionListOptions{})
	if len(page.Items) != 1 {
		t.Fatalf("sessions %+v", page)
	}
	id := page.Items[0].ID
	audit, err := store.GetChannelBindingAudit(ctx, id, 0)
	if err != nil || len(audit.Bindings) != 1 || audit.Bindings[0].ServiceID != "service_b" {
		t.Fatalf("actual successful channel %+v %v", audit, err)
	}
	// A has recovered; without session preference the first candidate is A.
	failA = false
	attempts = nil
	follow := `{"model":"public","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"` + answer + `"},{"role":"user","content":"continue"}]}`
	if status := serve(follow); status != 200 {
		t.Fatalf("follow: %d", status)
	}
	if !reflect.DeepEqual(attempts, []string{"b.example"}) {
		t.Fatalf("fingerprint preference %v", attempts)
	}
	audit, _ = store.GetChannelBindingAudit(ctx, id, 0)
	if audit.Events[0].Action != "hit" || audit.Events[0].Source != "fingerprint" {
		t.Fatalf("convo evidence %+v", audit.Events)
	}
	// Even with failover disabled, selection can still prefer B; it cannot
	// switch to A if B's attempt then fails.
	settings.AllowUnmatchedFailover = false
	store.UpdateRoutingSettings(ctx, settings)
	failB = true
	attempts = nil
	if status := serve(follow); status != 503 {
		t.Fatalf("disabled failover %d", status)
	}
	if !reflect.DeepEqual(attempts, []string{"b.example"}) {
		t.Fatalf("bypassed failover policy %v", attempts)
	}
	audit, _ = store.GetChannelBindingAudit(ctx, id, 0)
	if audit.Bindings[0].ServiceID != "service_b" {
		t.Fatal("failure changed binding")
	}
	if err := store.ReleaseChannelBindings(ctx, id); err != nil {
		t.Fatal(err)
	}
	attempts = nil
	if status := serve(follow); status != 200 {
		t.Fatalf("after release %d", status)
	}
	if !reflect.DeepEqual(attempts, []string{"a.example"}) {
		t.Fatalf("release didn't reset selection %v", attempts)
	}
}

func TestChannelBindingStrictContinuationWins(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "strict.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	scope := contract.ChannelBindingScope{SessionID: "session_one", Protocol: contract.ProtocolOpenAIResponses, Model: "public"}
	store.RememberChannelBinding(ctx, contract.ChannelBinding{ChannelBindingScope: scope, ServiceID: "endpoint_b", UpdatedAt: now, ExpiresAt: now.Add(time.Hour)}, now)
	session := newRecordSession(Request{Protocol: contract.ProtocolOpenAIResponses, Model: "public", PreviousResponseID: "resp_previous"}, "", contract.DefaultAuditSettings())
	session.sessionID = scope.SessionID
	session.channelBinding = &channelBindingAttempt{store: store, scope: scope, source: "explicit"}
	policy := contract.DefaultFailurePolicy()
	candidates := recoveryCandidates(2, policy, contract.DefaultFailoverPolicy())
	first := candidates[0].CanonicalService().ID
	handler := NewWithDependencies(Dependencies{})
	got := handler.preferChannelBinding(httptest.NewRequest("POST", "/v1/responses", nil), session, candidates)
	if got[0].CanonicalService().ID != first {
		t.Fatal("ordinary affinity overrode protocol binding")
	}
	audit, _ := store.GetChannelBindingAudit(ctx, scope.SessionID, 0)
	if audit.Events[0].Action != "strict" {
		t.Fatalf("strict audit %+v", audit)
	}
}

func TestChannelBindingCancelledCompletionIsNotRemembered(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "cancelled.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope := contract.ChannelBindingScope{SessionID: "session_one", Protocol: contract.ProtocolOpenAIChat, Model: "public"}
	session := newRecordSession(Request{Protocol: scope.Protocol, Model: scope.Model}, "", contract.DefaultAuditSettings())
	session.channelBinding = &channelBindingAttempt{store: store, scope: scope, ttl: time.Hour, source: "explicit"}
	candidate := recoveryCandidates(1, contract.DefaultFailurePolicy(), contract.DefaultFailoverPolicy())[0]
	cancel()
	handler := NewWithDependencies(Dependencies{})
	handler.rememberChannelBinding(ctx, session, candidate)
	if _, found, err := store.GetChannelBinding(context.Background(), scope); err != nil || found {
		t.Fatalf("cancelled request created binding: %v %v", found, err)
	}
}
