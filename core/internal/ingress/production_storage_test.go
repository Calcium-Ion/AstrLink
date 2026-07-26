package ingress

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestProductionGateResolvesSQLiteEndpointAndLoadsDedicatedCredential(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	persisted := contract.Endpoint{
		ID: "endpoint_01", Name: "production", Kind: contract.EndpointKindOpenAI,
		BaseURL: "https://upstream.example/v1", Auth: contract.EndpointAuth{Scheme: contract.AuthSchemeBearer},
		Enabled: true,
		Capabilities: []contract.Capability{{
			Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true,
		}},
	}
	if _, err := store.CreateEndpoint(context.Background(), persisted, storage.CredentialMutation{
		Present: true, Secret: []byte("stored-upstream-secret"),
	}); err != nil {
		t.Fatalf("create endpoint: %v", err)
	}
	resolver, err := endpoint.NewStoreResolver(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewProduction(Dependencies{
		Resolver: resolver, Authorizer: endpoint.NewSecretAuthorizer(store),
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.String() != "https://upstream.example/v1/responses" {
				t.Errorf("upstream URL = %q", request.URL.String())
			}
			if request.Header.Get("Authorization") != "Bearer stored-upstream-secret" {
				t.Errorf("upstream Authorization = %q", request.Header.Get("Authorization"))
			}
			if request.Header.Get("X-Api-Key") != "" {
				t.Errorf("local client token leaked through X-Api-Key")
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{"id":"resp_from_upstream"}`)),
			}, nil
		})),
		AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(_ context.Context, raw string) (contract.AccessTokenID, error) {
			if raw != "astr_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG" {
				return "", storage.ErrNotFound
			}
			return "token_production", nil
		}),
		AllowedHost: "127.0.0.1:8317",
	})
	if err != nil {
		t.Fatalf("NewProduction: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8317/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":true}`))
	request.Host = "127.0.0.1:8317"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Api-Key", "astr_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != `{"id":"resp_from_upstream"}` {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}
