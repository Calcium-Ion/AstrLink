package controlapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	"github.com/QuantumNous/astrlink/core/internal/servicemodel"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func TestGrokSubscriptionDeviceCodeModelsUsageAndLogout(t *testing.T) {
	ctx := context.Background()
	var polls, refreshes atomic.Int32
	idToken := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user_grok_42","email":"grok@example.com"}`)) + ".x"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth2/device/code":
			body, _ := io.ReadAll(r.Body)
			form, _ := url.ParseQuery(string(body))
			if form.Get("client_id") != accountauth.DefaultGrokClientID {
				t.Error("wrong OAuth client")
			}
			io.WriteString(w, `{"device_code":"grok-device-secret","user_code":"GROK-CODE","verification_uri":"https://accounts.x.ai/oauth2/device","expires_in":600,"interval":0}`)
		case "/oauth2/token":
			body, _ := io.ReadAll(r.Body)
			form, _ := url.ParseQuery(string(body))
			if form.Get("grant_type") == "refresh_token" {
				refreshes.Add(1)
				if form.Get("refresh_token") != "grok-refresh-secret" {
					t.Error("wrong refresh token")
				}
				io.WriteString(w, `{"access_token":"grok-rotated-secret","refresh_token":"grok-refresh-rotated","expires_in":3600}`)
				return
			}
			if form.Get("device_code") != "grok-device-secret" {
				t.Error("wrong device code")
			}
			if polls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"error":"authorization_pending"}`)
				return
			}
			io.WriteString(w, `{"access_token":"grok-access-secret","refresh_token":"grok-refresh-secret","expires_in":1,"id_token":"`+idToken+`"}`)
		case "/v1/models", "/v1/billing":
			if r.Header.Get("Authorization") != "Bearer grok-rotated-secret" || r.Header.Get("X-XAI-Token-Auth") != "xai-grok-cli" ||
				r.Header.Get("X-Grok-Client-Version") == "" || r.Header.Get("ChatGPT-Account-ID") != "" || r.Header.Get("Anthropic-Beta") != "" {
				t.Errorf("wrong provider authentication on %s: %v", r.URL.Path, r.Header)
			}
			if r.URL.Path == "/v1/models" {
				io.WriteString(w, `{"object":"list","data":[{"id":"grok-4.5"},{"id":"grok-composer-2.5-fast"}]}`)
			} else {
				if r.URL.Query().Get("format") != "credits" {
					t.Error("usage must request the credits format")
				}
				io.WriteString(w, `{"config":{"creditUsagePercent":37.5,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-09-15T00:00:00Z","end":"2099-09-22T00:00:00Z"},"prepaidBalance":{"val":500}},"subscriptionTier":"SuperGrok"}`)
			}
		default:
			t.Errorf("unexpected upstream request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	store, err := sqlite.Open(ctx, t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	credentials := accountauth.NewMemoryCredentialStore()
	manager, err := subscription.NewManager(subscription.StorageAccountStore{Store: store}, credentials,
		accountauth.OAuthConfig{HTTPClient: upstream.Client()},
		accountauth.OAuthConfig{Provider: contract.SubscriptionProviderXAIGrok, Issuer: upstream.URL, APIBaseURL: upstream.URL,
			HTTPClient: upstream.Client(), DevicePollMinInterval: 5 * time.Millisecond, DevicePollMaxInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("test", "abc1234"), Dependencies{
		ServiceStore: store, Subscriptions: manager, ServiceModels: servicemodel.New(nil, manager, upstream.Client()), ControlToken: testControlToken,
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
		for _, secret := range []string{"grok-access-secret", "grok-refresh-secret", "grok-rotated-secret", "grok-refresh-rotated", "grok-device-secret", "grok@example.com"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("credential leaked in public response: %s", w.Body.String())
			}
		}
		return w.Body.Bytes()
	}
	var service contract.Service
	if err := json.Unmarshal(call("POST", ServicesPath, `{"name":"Grok","kind":"grok_subscription"}`, 201), &service); err != nil {
		t.Fatal(err)
	}
	if service.Subscription.Provider != contract.SubscriptionProviderXAIGrok || service.Capabilities[0].Protocol != contract.ProtocolOpenAIResponses || service.Capabilities[1].Protocol != contract.ProtocolOpenAIChat {
		t.Fatalf("wrong provider configuration: %#v", service)
	}
	path := ServicesPath + "/" + string(service.ID)
	call("POST", path+"/authorization", `{"flow":"browser"}`, 422)
	call("POST", path+"/authorization", `{"flow":"authorization_code"}`, 422)
	var session contract.AuthorizationSession
	if err := json.Unmarshal(call("POST", path+"/authorization", `{"flow":"device_code"}`, 202), &session); err != nil {
		t.Fatal(err)
	}
	if session.Provider != contract.SubscriptionProviderXAIGrok || session.Flow != contract.AuthorizationFlowDeviceCode ||
		session.DeviceCode == nil || session.DeviceCode.UserCode != "GROK-CODE" || session.DeviceCode.VerificationURL != "https://accounts.x.ai/oauth2/device" {
		t.Fatalf("invalid authorization session: %#v", session)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := json.Unmarshal(call("GET", path+"/authorization", "", 200), &session); err != nil {
			t.Fatal(err)
		}
		if session.Status == contract.AuthorizationSessionStatusCompleted {
			break
		}
		if session.Status != contract.AuthorizationSessionStatusPending || time.Now().After(deadline) {
			t.Fatalf("device session did not complete: %#v", session)
		}
		time.Sleep(5 * time.Millisecond)
	}
	var connected contract.Service
	if err := json.Unmarshal(call("GET", path, "", 200), &connected); err != nil {
		t.Fatal(err)
	}
	if connected.Subscription.Status != contract.SubscriptionStatusConnected || connected.Subscription.ProviderAccountID != "user_grok_42" ||
		!strings.HasPrefix(connected.Subscription.CredentialRef, "keyring://") {
		t.Fatalf("connected service = %#v", connected.Subscription)
	}
	models := call("POST", path+"/probe-models", `{"protocol":"openai.models"}`, 200)
	if !strings.Contains(string(models), "grok-4.5") || !strings.Contains(string(models), "grok-composer-2.5-fast") {
		t.Fatalf("model probe lost results: %s", models)
	}
	if polls.Load() != 2 || refreshes.Load() != 1 {
		t.Fatalf("polls=%d refreshes=%d", polls.Load(), refreshes.Load())
	}
	usageRaw := call("GET", path+"/usage", "", 200)
	var usage contract.SubscriptionUsage
	if err := json.Unmarshal(usageRaw, &usage); err != nil || usage.PlanType != "SuperGrok" || usage.Primary == nil || usage.Primary.UsedPercent != 37.5 ||
		usage.Primary.LimitWindowSeconds == nil || *usage.Primary.LimitWindowSeconds != 7*24*3600 || usage.Credits == nil || usage.Credits.Balance != "$5.00" {
		t.Fatalf("invalid Grok usage: %s", usageRaw)
	}
	call("POST", path+"/usage/reset", "", 502)
	call("POST", path+"/logout", "", 200)
	if _, err := credentials.Get(ctx, service.ID); err == nil {
		t.Fatal("logout retained credentials")
	}
	if _, err := manager.AccessToken(ctx, service.ID); err == nil {
		t.Fatal("logged out account remains usable")
	}
}
