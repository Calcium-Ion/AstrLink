package accountauth_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

// droidJWT builds an unsigned JWT with the given claims.
func droidJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	segment := func(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }
	return segment([]byte(`{"alg":"none"}`)) + "." + segment(payload) + ".sig"
}

type droidFactory struct {
	polls         atomic.Int32
	refreshes     atomic.Int32
	organizations atomic.Value // JSON of /api/cli/org
	whoami        atomic.Int32 // HTTP status of /api/cli/whoami
	token         atomic.Value // the poll's terminal error, or "" for a token
	orgless       string       // access token without an organization claim
	scoped        string       // access token scoped to the organization
}

func newDroidFactory(t *testing.T) (*droidFactory, *httptest.Server) {
	t.Helper()
	exp := float64(time.Now().Add(50 * time.Minute).Unix())
	factory := &droidFactory{
		orgless: "",
		scoped:  "",
	}
	factory.orgless = droidJWT(t, map[string]any{"sub": "user_01", "exp": exp})
	factory.scoped = droidJWT(t, map[string]any{"sub": "user_01", "exp": exp, "org_id": "org_01HWORKOS"})
	factory.organizations.Store(`{"workosOrgIds":["org_01HWORKOS"]}`)
	factory.whoami.Store(http.StatusOK)
	factory.token.Store("")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Header.Get("User-Agent") != "factory-cli/"+accountauth.DefaultDroidClientVersion {
			t.Errorf("%s User-Agent = %q", request.URL.Path, request.Header.Get("User-Agent"))
		}
		for name := range request.Header {
			if strings.Contains(strings.ToLower(name+request.Header.Get(name)), "astrlink") {
				t.Errorf("%s carries gateway branding: %s", request.URL.Path, name)
			}
		}
		var form url.Values
		if request.Method == http.MethodPost {
			if request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				t.Errorf("%s is not a form exchange: %v", request.URL.Path, request.Header)
			}
			raw, _ := io.ReadAll(request.Body)
			form, _ = url.ParseQuery(string(raw))
			if form.Get("client_id") != accountauth.DefaultDroidClientID {
				t.Errorf("%s client_id = %q", request.URL.Path, form.Get("client_id"))
			}
		}
		switch request.URL.Path {
		case "/authorize/device":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"device_code": "device-code-secret", "user_code": "DROID-1234",
				"verification_uri": "https://auth.factory.ai/device", "verification_uri_complete": "https://auth.factory.ai/device?user_code=DROID-1234",
				"expires_in": 300, "interval": 1,
			})
		case "/authenticate":
			switch form.Get("grant_type") {
			case "urn:ietf:params:oauth:grant-type:device_code":
				if form.Get("device_code") != "device-code-secret" {
					t.Errorf("device_code = %q", form.Get("device_code"))
				}
				switch factory.polls.Add(1) {
				case 1:
					writer.WriteHeader(http.StatusBadRequest)
					io.WriteString(writer, `{"error":"authorization_pending","error_description":"device-code-secret"}`)
				case 2:
					writer.WriteHeader(http.StatusBadRequest)
					io.WriteString(writer, `{"error":"slow_down"}`)
				default:
					if failure := factory.token.Load().(string); failure != "" {
						writer.WriteHeader(http.StatusBadRequest)
						io.WriteString(writer, `{"error":"`+failure+`"}`)
						return
					}
					_ = json.NewEncoder(writer).Encode(map[string]any{
						"access_token": factory.orgless, "refresh_token": "refresh-one", "user": map[string]string{"email": "dev@example.com"},
					})
				}
			case "refresh_token":
				factory.refreshes.Add(1)
				switch form.Get("refresh_token") {
				case "refresh-one":
					if form.Get("organization_id") != "org_01HWORKOS" {
						t.Errorf("organization_id = %q", form.Get("organization_id"))
					}
					_ = json.NewEncoder(writer).Encode(map[string]any{
						"access_token": factory.scoped, "refresh_token": "refresh-two", "organization_id": "org_01HWORKOS",
					})
				case "refresh-two":
					_ = json.NewEncoder(writer).Encode(map[string]any{"access_token": factory.scoped, "refresh_token": "refresh-three"})
				default:
					writer.WriteHeader(http.StatusBadRequest)
					io.WriteString(writer, `{"error":"invalid_grant","error_description":"refresh-dead"}`)
				}
			default:
				t.Errorf("grant_type = %q", form.Get("grant_type"))
				writer.WriteHeader(http.StatusBadRequest)
			}
		case "/api/cli/org":
			if request.Header.Get("Authorization") != "Bearer "+factory.orgless || request.Header.Get("X-Factory-Org-Id") != "" {
				t.Errorf("org lookup headers = %v", request.Header)
			}
			io.WriteString(writer, factory.organizations.Load().(string))
		case "/api/cli/whoami":
			if request.Header.Get("Authorization") != "Bearer "+factory.scoped || request.Header.Get("X-Factory-Org-Id") != "org_01HWORKOS" ||
				request.Header.Get("X-Factory-Whoami-Extended") != "true" || request.Header.Get("X-Factory-Client") != "cli" {
				t.Errorf("whoami headers = %v", request.Header)
			}
			if status := int(factory.whoami.Load()); status != http.StatusOK {
				writer.WriteHeader(status)
				return
			}
			io.WriteString(writer, `{"userId":"user_01","orgId":"factory-org-7","email":"dev@example.com","region":"eu"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	return factory, server
}

func droidTestConfig(server *httptest.Server) accountauth.OAuthConfig {
	return accountauth.OAuthConfig{
		Provider:              contract.SubscriptionProviderFactoryDroid,
		Issuer:                server.URL,
		APIBaseURL:            server.URL,
		HTTPClient:            server.Client(),
		DevicePollMinInterval: 5 * time.Millisecond,
		DevicePollMaxInterval: 20 * time.Millisecond,
	}
}

func TestDroidDeviceCodeLoginScopesTheTokenToAnOrganization(t *testing.T) {
	factory, server := newDroidFactory(t)
	store := accountauth.NewMemoryCredentialStore()
	config := droidTestConfig(server)
	manager := accountauth.NewSessionManager(config, store, func(ctx context.Context, session contract.AuthorizationSession, tokens accountauth.AccountTokens) error {
		return store.Put(ctx, session.ServiceID, tokens)
	})
	for _, flow := range []contract.AuthorizationFlow{contract.AuthorizationFlowBrowser, contract.AuthorizationFlowCode} {
		if _, err := manager.Begin(context.Background(), "service_droid", flow); err == nil {
			t.Fatalf("%s flow accepted for Droid", flow)
		}
	}
	session, err := manager.Begin(context.Background(), "service_droid", contract.AuthorizationFlowDeviceCode)
	if err != nil {
		t.Fatalf("Begin() = %v", err)
	}
	if session.Provider != contract.SubscriptionProviderFactoryDroid || session.DeviceCode == nil ||
		session.DeviceCode.UserCode != "DROID-1234" ||
		session.DeviceCode.VerificationURL != "https://auth.factory.ai/device?user_code=DROID-1234" {
		t.Fatalf("device session = %#v", session)
	}
	if err := session.Validate(); err != nil {
		t.Fatalf("session invalid: %v", err)
	}
	waitForSessionStatus(t, manager, "service_droid", contract.AuthorizationSessionStatusCompleted)
	if factory.polls.Load() != 3 || factory.refreshes.Load() != 1 {
		t.Fatalf("polls = %d refreshes = %d", factory.polls.Load(), factory.refreshes.Load())
	}
	tokens, err := store.Get(context.Background(), "service_droid")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != factory.scoped || tokens.RefreshToken != "refresh-two" ||
		tokens.AccountID != "factory-org-7" || tokens.ProjectID != "org_01HWORKOS" || tokens.Region != "eu" ||
		tokens.ExpiresAt.Before(time.Now().Add(40*time.Minute)) {
		t.Fatalf("stored tokens = %#v", tokens)
	}

	client := accountauth.NewTokenClient(config)
	renewed, err := client.RefreshAccount(context.Background(), tokens)
	if err != nil || renewed.AccessToken != factory.scoped || renewed.RefreshToken != "refresh-three" ||
		renewed.AccountID != "factory-org-7" || renewed.ProjectID != "org_01HWORKOS" || renewed.Region != "eu" {
		t.Fatalf("RefreshAccount() = %#v, %v", renewed, err)
	}
	if _, err := client.Refresh(context.Background(), "refresh-dead"); !errors.Is(err, accountauth.ErrInvalidGrant) {
		t.Fatalf("Refresh(dead) = %v, want ErrInvalidGrant", err)
	}
}

func TestDroidLoginFailsWithoutAnOrganizationOrApproval(t *testing.T) {
	for _, tt := range []struct {
		name, tokenError, organizations, code string
	}{
		{name: "no organization", organizations: `{"workosOrgIds":[]}`, code: accountauth.ErrCodeDroidNoOrganization},
		{name: "denied", tokenError: "access_denied", organizations: `{"workosOrgIds":["org_01HWORKOS"]}`, code: accountauth.ErrCodeDeviceCodePoll},
	} {
		t.Run(tt.name, func(t *testing.T) {
			factory, server := newDroidFactory(t)
			factory.organizations.Store(tt.organizations)
			factory.token.Store(tt.tokenError)
			store := accountauth.NewMemoryCredentialStore()
			manager := accountauth.NewSessionManager(droidTestConfig(server), store, func(ctx context.Context, session contract.AuthorizationSession, tokens accountauth.AccountTokens) error {
				return store.Put(ctx, session.ServiceID, tokens)
			})
			if _, err := manager.Begin(context.Background(), "service_droid", contract.AuthorizationFlowDeviceCode); err != nil {
				t.Fatalf("Begin() = %v", err)
			}
			waitForSessionStatus(t, manager, "service_droid", contract.AuthorizationSessionStatusFailed)
			failed, _ := manager.Get("service_droid")
			if failed.Error == nil || failed.Error.Code != tt.code || strings.Contains(failed.Error.Message, "secret") {
				t.Fatalf("failed session = %#v", failed.Error)
			}
			if _, err := store.Get(context.Background(), "service_droid"); !errors.Is(err, accountauth.ErrCredentialNotFound) {
				t.Fatalf("a failed login stored tokens: %v", err)
			}
		})
	}
}

func TestApplyDroidAPIHeadersUsesTheDroidCLIIdentity(t *testing.T) {
	headers := http.Header{"X-Factory-Org-Id": {"client-org"}}
	accountauth.ApplyDroidAPIHeaders(headers, accountauth.AccountTokens{AccessToken: "fk_tok", AccountID: "factory-org-7"})
	if headers.Get("Authorization") != "Bearer fk_tok" ||
		headers.Get("User-Agent") != "factory-cli/"+accountauth.DefaultDroidClientVersion ||
		headers.Get("X-Factory-Client") != "cli" || headers.Get("X-Client-Version") != accountauth.DefaultDroidClientVersion ||
		headers.Get("X-Factory-Org-Id") != "factory-org-7" || headers.Get("X-AstrLink-Factory-Endpoint") != "" {
		t.Fatalf("headers = %v", headers)
	}
	eu := make(http.Header)
	accountauth.ApplyDroidAPIHeaders(eu, accountauth.AccountTokens{AccessToken: "fk_tok", Region: "eu"})
	if eu.Get("X-AstrLink-Factory-Endpoint") != accountauth.DefaultDroidEUAPIBaseURL || len(eu["X-Factory-Org-Id"]) != 0 {
		t.Fatalf("eu headers = %v", eu)
	}
	if got := accountauth.DroidAPIBaseURL(accountauth.DefaultDroidAPIBaseURL, accountauth.AccountTokens{Region: "eu"}); got != accountauth.DefaultDroidEUAPIBaseURL {
		t.Fatalf("DroidAPIBaseURL(eu) = %q", got)
	}
	if got := accountauth.DroidAPIBaseURL("http://127.0.0.1:1/", accountauth.AccountTokens{Region: "eu"}); got != "http://127.0.0.1:1" {
		t.Fatalf("DroidAPIBaseURL(test server, eu) = %q", got)
	}
	normalized := accountauth.OAuthConfig{Provider: contract.SubscriptionProviderFactoryDroid}.Normalize()
	if normalized.ClientID != accountauth.DefaultDroidClientID || normalized.Issuer != "https://api.workos.com/user_management" ||
		normalized.TokenURL != "https://api.workos.com/user_management/authenticate" ||
		normalized.APIBaseURL != "https://api.factory.ai" {
		t.Fatalf("normalized = %#v", normalized)
	}
}
