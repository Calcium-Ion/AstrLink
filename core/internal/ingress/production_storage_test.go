package ingress

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
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
		Enabled: true, Models: []string{"gpt-5"},
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

func TestProductionGateRoutesToSelectedCodexSubscriptionService(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	credentials := accountauth.NewMemoryCredentialStore()
	manager, err := subscription.NewManager(
		subscription.StorageAccountStore{Store: store},
		credentials,
		accountauth.OAuthConfig{ClientID: "astrlink_registered_test_client"},
	)
	if err != nil {
		t.Fatalf("new subscription manager: %v", err)
	}
	for _, account := range []struct {
		id        contract.ServiceID
		name      string
		accountID string
		token     string
	}{
		{
			id: "service_codex_personal", name: "Codex personal",
			accountID: "acct_personal", token: "personal-access-token",
		},
		{
			id: "service_codex_work", name: "Codex work",
			accountID: "acct_work", token: "work-access-token",
		},
	} {
		expiresAt := now.Add(time.Hour)
		service := contract.Service{
			ID: account.id, Name: account.name, Kind: contract.ServiceKindCodexSubscription,
			Enabled: true, Models: []string{"gpt-5"}, Capabilities: contract.DefaultOpenAICodexCapabilities(),
			Subscription: &contract.SubscriptionConnection{
				Provider:          contract.SubscriptionProviderOpenAICodex,
				Status:            contract.SubscriptionStatusConnected,
				ProviderAccountID: account.accountID,
				CredentialRef:     accountauth.CredentialRefFor(account.id),
				TokenExpiresAt:    &expiresAt,
				LastRefreshAt:     &now,
			},
		}
		if _, err := store.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
			t.Fatalf("create subscription service %s: %v", account.id, err)
		}
		if err := credentials.Put(ctx, account.id, accountauth.AccountTokens{
			AccessToken: account.token, RefreshToken: "refresh-" + account.token,
			AccountID: account.accountID, ExpiresAt: expiresAt,
		}); err != nil {
			t.Fatalf("store subscription credential %s: %v", account.id, err)
		}
	}
	order, err := store.GetServiceOrder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateServiceOrder(ctx, contract.ServiceOrder{ServiceIDs: []contract.ServiceID{"service_codex_work", "service_codex_personal"}}, order.ETag); err != nil {
		t.Fatal(err)
	}

	resolver, err := endpoint.NewStoreResolver(store)
	if err != nil {
		t.Fatal(err)
	}
	resolver.WithSubscriptionBaseURL("https://subscription.example/backend-api/codex")
	handler, err := NewProduction(Dependencies{
		Resolver: resolver, Authorizer: endpoint.NewServiceAuthorizer(store, manager),
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.String() != "https://subscription.example/backend-api/codex/responses" {
				t.Errorf("upstream URL = %q", request.URL.String())
			}
			if request.Header.Get("Authorization") != "Bearer work-access-token" {
				t.Errorf("upstream Authorization = %q", request.Header.Get("Authorization"))
			}
			if request.Header.Get("ChatGPT-Account-ID") != "acct_work" {
				t.Errorf("upstream account = %q", request.Header.Get("ChatGPT-Account-ID"))
			}
			if request.Header.Get("OAI-Product-Sku") != "codex" {
				t.Errorf("upstream product = %q", request.Header.Get("OAI-Product-Sku"))
			}
			if request.Header.Get("X-Api-Key") != "" {
				t.Errorf("local client token leaked through X-Api-Key")
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{"id":"resp_from_codex_work"}`)),
			}, nil
		})),
		AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(_ context.Context, raw string) (contract.AccessTokenID, error) {
			if raw != "astr_local-client-token" {
				return "", storage.ErrNotFound
			}
			return "token_production", nil
		}),
		AllowedHost: "127.0.0.1:8317",
	})
	if err != nil {
		t.Fatalf("NewProduction: %v", err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"http://127.0.0.1:8317/v1/responses",
		strings.NewReader(`{"model":"gpt-5","stream":true}`),
	)
	request.Host = "127.0.0.1:8317"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Api-Key", "astr_local-client-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != `{"id":"resp_from_codex_work"}` {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}
