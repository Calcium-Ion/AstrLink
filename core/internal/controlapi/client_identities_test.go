package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

func TestClientIdentitiesReportLearnedAndBuiltinVersions(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	identities := accountauth.NewIdentityRegistry(store, store)
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		ServiceStore: store, ClientIdentities: identities, ControlToken: testControlToken, ObserverToken: testObserverToken,
	})
	if err != nil {
		t.Fatalf("NewWithDependencies: %v", err)
	}
	get := func(target string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("Authorization", "Bearer "+testObserverToken)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	response := get(ClientIdentitiesPath)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	var body contract.ClientIdentities
	decode(t, response, &body)
	builtin := contract.ClientIdentities{
		Codex:  contract.ClientIdentityStatus{BuiltinVersion: accountauth.DefaultCodexModelsClientVersion},
		Claude: contract.ClientIdentityStatus{BuiltinVersion: accountauth.DefaultClaudeIdentity().Version},
		Grok:   contract.ClientIdentityStatus{BuiltinVersion: accountauth.DefaultGrokCLIClientVersion},
	}
	if body != builtin {
		t.Fatalf("before learning = %+v", body)
	}

	// Learning stays reported while it is switched off.
	settings := contract.DefaultRoutingSettings()
	settings.CodexIdentityAutoLearn = false
	if err := store.UpdateRoutingSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if _, err := identities.LearnCodex(context.Background(), http.Header{
		"User-Agent": {"codex_cli_rs/0.160.0 (Mac OS 26.0.0; arm64) iTerm.app/3.6.1"},
		"Originator": {"codex_cli_rs"},
	}); err != nil {
		t.Fatal(err)
	}
	response = get(ClientIdentitiesPath)
	body = contract.ClientIdentities{}
	decode(t, response, &body)
	want := builtin
	want.Codex.LearnedVersion = "0.160.0"
	if body != want {
		t.Fatalf("after learning = %+v", body)
	}

	if response := get(ClientIdentitiesPath + "?verbose=1"); response.Code != http.StatusBadRequest {
		t.Fatalf("query status=%d", response.Code)
	}
	request := httptest.NewRequest(http.MethodPost, ClientIdentitiesPath, nil)
	request.Header.Set("Authorization", "Bearer "+testControlToken)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d", recorder.Code)
	}
}
