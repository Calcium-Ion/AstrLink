package accountauth_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

func TestAntigravityBrowserAuthorizationAndRefresh(t *testing.T) {
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.UserAgent() != accountauth.AntigravityUserAgent() {
			t.Error("missing Antigravity identity")
		}
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
				return
			}
			if r.Form.Get("client_id") != "test-client" || r.Form.Get("client_secret") != "test-client-secret" {
				t.Error("missing configured OAuth client")
			}
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				if r.Form.Get("code") != "test-code" || r.Form.Get("code_verifier") == "" {
					t.Error("missing authorization code or PKCE")
				}
				_, _ = io.WriteString(w, `{"access_token":"test-access","refresh_token":"test-refresh","expires_in":1}`)
			case "refresh_token":
				if r.Form.Get("refresh_token") != "test-refresh" {
					t.Error("wrong refresh token")
				}
				_, _ = io.WriteString(w, `{"access_token":"test-refreshed","expires_in":3600}`)
			default:
				t.Error("unexpected OAuth grant")
			}
		case "/userinfo":
			_, _ = io.WriteString(w, `{"id":"test-google-account"}`)
		case "/v1internal:loadCodeAssist":
			_, _ = io.WriteString(w, `{"cloudaicompanionProject":{"id":"test-project"},"paidTier":{"id":"test-plan"}}`)
		default:
			t.Errorf("unexpected OAuth endpoint: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer issuer.Close()
	preferred, fallback := availablePortPair(t)
	config := accountauth.OAuthConfig{
		Provider: contract.SubscriptionProviderAntigravity,
		ClientID: "test-client", ClientSecret: "test-client-secret",
		TokenURL: issuer.URL + "/token", UserInfoURL: issuer.URL + "/userinfo",
		ProjectBaseURL: issuer.URL, HTTPClient: issuer.Client(),
		PreferredPort: preferred, FallbackPort: fallback,
	}
	store := accountauth.NewMemoryCredentialStore()
	manager := accountauth.NewSessionManager(config, store,
		func(ctx context.Context, session contract.AuthorizationSession, tokens accountauth.AccountTokens) error {
			return store.Put(ctx, session.ServiceID, tokens)
		})
	ctx := context.Background()
	const serviceID = "service_antigravity"
	if _, err := manager.Begin(ctx, serviceID, contract.AuthorizationFlowDeviceCode); err == nil {
		t.Fatal("accepted unsupported device authorization")
	}
	session, err := manager.Begin(ctx, serviceID, contract.AuthorizationFlowBrowser)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = manager.Cancel(ctx, serviceID) }()
	if session.Provider != contract.SubscriptionProviderAntigravity {
		t.Fatal("wrong session provider")
	}
	authURL, err := url.Parse(session.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := authURL.Query()
	if authURL.Host != "accounts.google.com" || query.Get("access_type") != "offline" ||
		query.Get("prompt") != "consent" || query.Get("code_challenge_method") != "S256" ||
		query.Get("state") == "" || query.Has("originator") || query.Has("codex_cli_simplified_flow") {
		t.Fatal("invalid Google authorization parameters")
	}
	callback, err := url.Parse(query.Get("redirect_uri"))
	if err != nil {
		t.Fatal(err)
	}
	if callback.Path != "/oauth-callback" {
		t.Fatal("wrong callback path")
	}
	callback.Host = "127.0.0.1:" + callback.Port()
	callback.RawQuery = url.Values{"code": {"test-code"}, "state": {query.Get("state")}}.Encode()
	response, err := http.Get(callback.String())
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d", response.StatusCode)
	}
	waitForSessionStatus(t, manager, serviceID, contract.AuthorizationSessionStatusCompleted)
	source := accountauth.NewTokenSource(store, accountauth.NewTokenClient(config), time.Minute, time.Now)
	tokens, err := source.AccessToken(ctx, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "test-refreshed" || tokens.RefreshToken != "test-refresh" ||
		tokens.AccountID != "test-google-account" || tokens.ProjectID != "test-project" || tokens.PlanType != "test-plan" {
		t.Fatal("refresh lost account, project, plan or refresh token")
	}
	public, _ := manager.Get(serviceID)
	if strings.Contains(public.AuthorizationURL, "test-access") {
		t.Fatal("session exposed credentials")
	}
}
