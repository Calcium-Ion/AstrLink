package subscription_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func TestBeginAuthorizationUsesOfficialPublicClientByDefault(t *testing.T) {
	accounts := subscription.NewMemoryAccountStore()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	account := contract.SubscriptionAccount{
		ID: "service_codex_01", Provider: contract.SubscriptionProviderOpenAICodex,
		Status: contract.SubscriptionStatusDisconnected, DisplayName: "Codex",
		Capabilities: contract.DefaultOpenAICodexCapabilities(), CreatedAt: now, UpdatedAt: now,
	}
	if err := accounts.PutAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	preferred, fallback := subscriptionTestPortPair(t)
	manager, err := subscription.NewManager(
		accounts,
		accountauth.NewMemoryCredentialStore(),
		accountauth.OAuthConfig{
			PreferredPort: preferred,
			FallbackPort:  fallback,
			Now:           func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatalf("NewManager() = %v", err)
	}
	session, err := manager.BeginAuthorization(
		context.Background(),
		account.ID,
		contract.AuthorizationFlowBrowser,
	)
	if err != nil {
		t.Fatalf("BeginAuthorization() = %v", err)
	}
	defer func() { _, _ = manager.CancelAuthorization(context.Background(), account.ID) }()
	if session.Flow != contract.AuthorizationFlowBrowser ||
		!strings.Contains(session.AuthorizationURL, accountauth.DefaultCodexOAuthClientID) {
		t.Fatalf("session = %#v", session)
	}
	account, err = manager.Get(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	if account.Status != contract.SubscriptionStatusAuthorizing {
		t.Fatalf("status = %s", account.Status)
	}
	if account.AuthorizationBoundary != "" {
		t.Fatalf("authorization boundary = %q", account.AuthorizationBoundary)
	}
}

func TestConnectedAccountModelsAndResponsesPath(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		auth := request.Header.Get("Authorization")
		if auth != "Bearer access-secret-token-value" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		if request.Header.Get("ChatGPT-Account-ID") != "acct_12345678" {
			http.Error(writer, "missing account", http.StatusUnauthorized)
			return
		}
		switch request.URL.Path {
		case "/models":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"object": "list",
				"data":   []map[string]any{{"id": "gpt-5", "object": "model"}},
			})
		case "/responses":
			body, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(body), `"model"`) {
				http.Error(writer, "bad body", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"id":     "resp_1",
				"object": "response",
				"status": "completed",
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.Close)

	accounts := subscription.NewMemoryAccountStore()
	credentials := accountauth.NewMemoryCredentialStore()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	manager, err := subscription.NewManager(accounts, credentials, accountauth.OAuthConfig{
		ClientID:   "astrlink_test_client",
		APIBaseURL: upstream.URL,
		HTTPClient: upstream.Client(),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewManager() = %v", err)
	}
	account := contract.SubscriptionAccount{
		ID: "service_codex_02", Provider: contract.SubscriptionProviderOpenAICodex,
		Status: contract.SubscriptionStatusDisconnected, DisplayName: "Codex",
		Capabilities: contract.DefaultOpenAICodexCapabilities(), CreatedAt: now, UpdatedAt: now,
	}
	if err := accounts.PutAccount(context.Background(), account); err != nil {
		t.Fatalf("PutAccount() = %v", err)
	}
	expires := now.Add(time.Hour)
	tokens := accountauth.AccountTokens{
		AccessToken:  "access-secret-token-value",
		RefreshToken: "refresh-secret-token-value",
		AccountID:    "acct_12345678",
		ExpiresAt:    expires,
	}
	if err := credentials.Put(context.Background(), account.ID, tokens); err != nil {
		t.Fatalf("Put() = %v", err)
	}
	account.Status = contract.SubscriptionStatusConnected
	account.CredentialRef = accountauth.CredentialRefFor(account.ID)
	account.TokenExpiresAt = &expires
	account.UpdatedAt = now
	if err := accounts.PutAccount(context.Background(), account); err != nil {
		t.Fatalf("PutAccount() = %v", err)
	}

	liveTokens, err := manager.AccessToken(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("AccessToken() = %v", err)
	}
	models, err := manager.Provider().ListModels(context.Background(), liveTokens)
	if err != nil {
		t.Fatalf("ListModels() = %v", err)
	}
	if len(models.Data) != 1 || models.Data[0].ID != "gpt-5" {
		t.Fatalf("models = %#v", models)
	}
	body, status, err := manager.Provider().ProbeNonStreamingResponse(context.Background(), liveTokens, "gpt-5")
	if err != nil || status != http.StatusOK {
		t.Fatalf("ProbeNonStreamingResponse() status=%d err=%v body=%s", status, err, body)
	}
	if !strings.Contains(string(body), "resp_1") {
		t.Fatalf("unexpected response body %s", body)
	}

	// Secret corpus: account JSON and errors must not contain token material.
	listed, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	raw, _ := json.Marshal(listed)
	for _, secret := range []string{"access-secret-token-value", "refresh-secret-token-value", "Bearer "} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("subscription list leaked %q: %s", secret, raw)
		}
	}
}

func TestUnavailableCredentialStoreFailsClosed(t *testing.T) {
	t.Parallel()
	accounts := subscription.NewMemoryAccountStore()
	now := time.Now().UTC()
	account := contract.SubscriptionAccount{
		ID: "service_codex_03", Provider: contract.SubscriptionProviderOpenAICodex,
		Status: contract.SubscriptionStatusDisconnected, DisplayName: "Codex",
		Capabilities: contract.DefaultOpenAICodexCapabilities(), CreatedAt: now, UpdatedAt: now,
	}
	if err := accounts.PutAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	manager, err := subscription.NewManager(
		accounts,
		accountauth.UnavailableCredentialStore{},
		accountauth.OAuthConfig{ClientID: "astrlink_test_client"},
	)
	if err != nil {
		t.Fatalf("NewManager() = %v", err)
	}
	_, err = manager.BeginAuthorization(
		context.Background(),
		account.ID,
		contract.AuthorizationFlowBrowser,
	)
	if err == nil {
		t.Fatal("BeginAuthorization() expected credential store failure")
	}
}

func TestMissingInMemorySessionReconcilesPersistedAuthorizingState(t *testing.T) {
	t.Parallel()
	accounts := subscription.NewMemoryAccountStore()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	account := contract.SubscriptionAccount{
		ID: "service_codex_interrupted", Provider: contract.SubscriptionProviderOpenAICodex,
		Status: contract.SubscriptionStatusAuthorizing, DisplayName: "Interrupted Codex",
		Capabilities: contract.DefaultOpenAICodexCapabilities(), CreatedAt: now, UpdatedAt: now,
	}
	if err := accounts.PutAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	manager, err := subscription.NewManager(
		accounts,
		accountauth.NewMemoryCredentialStore(),
		accountauth.OAuthConfig{Now: func() time.Time { return now.Add(time.Minute) }},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.GetAuthorization(context.Background(), account.ID); ok {
		t.Fatal("GetAuthorization() unexpectedly found an in-memory session")
	}
	reconciled, err := accounts.GetAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Status != contract.SubscriptionStatusDisconnected ||
		reconciled.LastError == nil ||
		reconciled.LastError.Code != accountauth.ErrCodeSessionInterrupted {
		t.Fatalf("reconciled account = %#v", reconciled)
	}
}

func subscriptionTestPortPair(t *testing.T) (int, int) {
	t.Helper()
	first := subscriptionTestPort(t)
	second := subscriptionTestPort(t)
	for first == second {
		second = subscriptionTestPort(t)
	}
	return first, second
}

func subscriptionTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}
