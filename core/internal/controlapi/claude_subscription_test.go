package controlapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/servicemodel"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func TestClaudeSubscriptionAuthorizationRefreshModelsUsageAndLogout(t *testing.T) {
	testClaudeLifecycle(t, false)
}
func TestClaudeSubscriptionLifecycleUsesInstanceProxy(t *testing.T) { testClaudeLifecycle(t, true) }
func testClaudeLifecycle(t *testing.T, useProxy bool) {
	ctx := context.Background()
	var challenge, state string
	var exchanges, refreshes atomic.Int32
	providerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth/token":
			if r.Header.Get("Content-Type") != "application/json" {
				t.Error("Claude token endpoint requires JSON")
			}
			var input map[string]string
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input["client_id"] != accountauth.DefaultClaudeClientID {
				t.Error("wrong OAuth client")
			}
			if input["grant_type"] == "authorization_code" {
				exchanges.Add(1)
				digest := sha256.Sum256([]byte(input["code_verifier"]))
				if base64.RawURLEncoding.EncodeToString(digest[:]) != challenge || input["state"] != state || input["code"] != "code-secret" {
					t.Error("PKCE or state not bound to session")
				}
				if input["redirect_uri"] != accountauth.DefaultClaudeRedirectURI {
					t.Error("wrong callback")
				}
				io.WriteString(w, `{"access_token":"claude-access-secret","refresh_token":"claude-refresh-secret","expires_in":1,"account":{"uuid":"account_claude"}}`)
			} else {
				refreshes.Add(1)
				if input["refresh_token"] != "claude-refresh-secret" {
					t.Error("wrong refresh token")
				}
				io.WriteString(w, `{"access_token":"claude-rotated-secret","refresh_token":"claude-refresh-rotated","expires_in":3600}`)
			}
		case "/v1/models", "/api/oauth/usage", "/api/oauth/profile":
			if r.Header.Get("Authorization") != "Bearer claude-rotated-secret" || !strings.Contains(r.Header.Get("Anthropic-Beta"), "oauth-2025-04-20") || r.Header.Get("ChatGPT-Account-ID") != "" {
				t.Error("wrong provider authentication")
			}
			if strings.HasPrefix(r.URL.Path, "/api/oauth/") && !strings.HasPrefix(r.Header.Get("User-Agent"), accountauth.ClaudeUserAgentPrefix) {
				t.Errorf("Claude %s sent non-CLI User-Agent %q", r.URL.Path, r.Header.Get("User-Agent"))
			}
			if r.URL.Path == "/v1/models" {
				if r.URL.Query().Get("client_version") != "" {
					t.Error("Codex query sent to Claude")
				}
				if r.URL.Query().Get("after_id") == "" {
					io.WriteString(w, `{"data":[{"id":"claude-sonnet-4-5"}],"has_more":true,"last_id":"claude-sonnet-4-5"}`)
				} else {
					io.WriteString(w, `{"data":[{"id":"claude-opus-4-5"}],"has_more":false}`)
				}
			} else if r.URL.Path == "/api/oauth/profile" {
				io.WriteString(w, `{"organization":{"organization_type":"claude_max","rate_limit_tier":"default_claude_max_20x"}}`)
			} else {
				io.WriteString(w, `{"five_hour":{"utilization":12,"resets_at":"2026-09-18T12:00:00Z"},"seven_day":{"utilization":34},"seven_day_sonnet":{"utilization":56},"seven_day_opus":null,"limits":[{"kind":"session","group":"session","percent":12},{"kind":"weekly_all","group":"weekly","percent":34},{"kind":"weekly_scoped","group":"weekly","percent":56,"scope":{"model":{"display_name":"Sonnet"}}}],"extra_usage":{"is_enabled":false}}`)
			}
		default:
			t.Errorf("unexpected upstream request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	upstream := httptest.NewServer(providerHandler)
	t.Cleanup(upstream.Close)
	store, err := sqlite.Open(ctx, t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	baseURL := upstream.URL
	proxyInput := ""
	if useProxy {
		baseURL = "http://claude.invalid"
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Host != "claude.invalid" || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("proxy-user:proxy-password")) {
				t.Error("wrong instance proxy request")
			}
			providerHandler.ServeHTTP(w, r)
		}))
		t.Cleanup(proxy.Close)
		raw, _ := json.Marshal(map[string]any{"mode": "custom", "url": proxy.URL, "credential": map[string]string{"username": "proxy-user", "password": "proxy-password"}})
		proxyInput = `,"proxy":` + string(raw)
	}
	credentials := accountauth.NewMemoryCredentialStore()
	manager, err := subscription.NewManager(subscription.StorageAccountStore{Store: store}, credentials,
		accountauth.OAuthConfig{HTTPClient: upstream.Client(), ResolveProxy: networkproxy.Resolver(store, store)}, accountauth.OAuthConfig{TokenURL: baseURL + "/oauth/token", APIBaseURL: baseURL, HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("test", "abc1234"), Dependencies{
		ServiceStore: store, Subscriptions: manager, ServiceModels: servicemodel.New(store, manager, upstream.Client()), ControlToken: testControlToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string, status int) []byte {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+testControlToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: status=%d body=%s", method, path, w.Code, w.Body.String())
		}
		for _, secret := range []string{"claude-access-secret", "claude-refresh-secret", "claude-rotated-secret", "claude-refresh-rotated", "code-secret", "proxy-user", "proxy-password"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("credential leaked in public response")
			}
		}
		return w.Body.Bytes()
	}
	var service contract.Service
	if err := json.Unmarshal(call("POST", ServicesPath, `{"name":"Claude Code","kind":"claude_subscription","models":["claude-sonnet-4-5"]`+proxyInput+`}`, 201), &service); err != nil {
		t.Fatal(err)
	}
	if service.Subscription.Provider != contract.SubscriptionProviderClaudeCode || service.Capabilities[0].Protocol != contract.ProtocolAnthropicMessages {
		t.Fatalf("wrong provider configuration: %#v", service)
	}
	path := ServicesPath + "/" + string(service.ID)
	call("POST", path+"/authorization", `{"flow":"device_code"}`, 422)
	var session contract.AuthorizationSession
	if err := json.Unmarshal(call("POST", path+"/authorization", `{"flow":"authorization_code"}`, 202), &session); err != nil {
		t.Fatal(err)
	}
	authorize, _ := url.Parse(session.AuthorizationURL)
	challenge, state = authorize.Query().Get("code_challenge"), authorize.Query().Get("state")
	if authorize.Scheme != "https" || authorize.Host != "claude.com" || challenge == "" || state == "" || session.Provider != contract.SubscriptionProviderClaudeCode {
		t.Fatalf("invalid authorization session: %#v", session)
	}
	complete := func(code string, status int) []byte {
		body, _ := json.Marshal(map[string]string{"session_id": string(session.ID), "code": code})
		return call("PUT", path+"/authorization", string(body), status)
	}
	complete("code-secret#wrong-state", 409)
	if exchanges.Load() != 0 {
		t.Fatal("invalid state reached provider")
	}
	var savedProxy *contract.ServiceProxy
	if useProxy {
		current, _ := store.GetService(ctx, service.ID)
		savedProxy = current.Service.Proxy
		current.Service.Proxy = &contract.ServiceProxy{Mode: "custom", URL: "http://127.0.0.1:1"}
		if _, err := store.UpdateService(ctx, current.Service, storage.CredentialMutation{}, current.ETag); err != nil {
			t.Fatal(err)
		}
	}
	complete("code-secret#"+state, 200)
	if useProxy {
		current, _ := store.GetService(ctx, service.ID)
		if current.Service.Proxy.URL != "http://127.0.0.1:1" {
			t.Fatal("login overwrote latest proxy configuration")
		}
		current.Service.Proxy = savedProxy
		if _, err := store.UpdateService(ctx, current.Service, storage.CredentialMutation{}, current.ETag); err != nil {
			t.Fatal(err)
		}
	}
	complete("code-secret#"+state, 409)
	models := call("POST", path+"/probe-models", `{"protocol":"openai.models"}`, 200)
	if !strings.Contains(string(models), "claude-opus-4-5") || !strings.Contains(string(models), "claude-sonnet-4-5") {
		t.Fatal("model pagination lost results")
	}
	if exchanges.Load() != 1 || refreshes.Load() != 1 {
		t.Fatal("unexpected token exchange count")
	}
	usageRaw := call("GET", path+"/usage", "", 200)
	var usage contract.SubscriptionUsage
	if err := json.Unmarshal(usageRaw, &usage); err != nil || usage.Primary == nil || usage.Primary.UsedPercent != 12 || usage.Secondary.UsedPercent != 34 ||
		len(usage.AdditionalRateLimits) != 1 || usage.AdditionalRateLimits[0].LimitName != "Sonnet" || usage.AdditionalRateLimits[0].Primary.UsedPercent != 56 || usage.PlanType != "max_20x" {
		t.Fatalf("invalid Claude usage: %s", usageRaw)
	}
	stored, err := store.GetService(ctx, service.ID)
	if err != nil || stored.Service.Kind != contract.ServiceKindClaudeSubscription || len(stored.Service.Models) != 1 {
		t.Fatal("subscription lifecycle changed service configuration")
	}
	accounts, err := store.ListSubscriptionAccounts(ctx)
	if err != nil || len(accounts) != 1 || accounts[0].Provider != contract.SubscriptionProviderClaudeCode {
		t.Fatal("Claude account missing from account store")
	}
	call("POST", path+"/logout", "", 200)
	if _, err := credentials.Get(ctx, service.ID); err == nil {
		t.Fatal("logout retained credentials")
	}
	if _, err := manager.AccessToken(ctx, service.ID); err == nil {
		t.Fatal("logged out account remains usable")
	}
}
