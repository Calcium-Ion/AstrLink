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

func TestDroidSubscriptionDeviceCodeModelsUsageAndLogout(t *testing.T) {
	ctx := context.Background()
	payload, _ := json.Marshal(map[string]any{"sub": "user_01", "org_id": "org_01HWORKOS", "exp": time.Now().Add(time.Hour).Unix()})
	scoped := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	var polls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.UserAgent() != "factory-cli/"+accountauth.DefaultDroidClientVersion {
			t.Errorf("%s User-Agent = %q", r.URL.Path, r.UserAgent())
		}
		switch r.URL.Path {
		case "/authorize/device":
			io.WriteString(w, `{"device_code":"droid-device-secret","user_code":"DROI-D001","verification_uri":"https://auth.factory.ai/device","verification_uri_complete":"https://auth.factory.ai/device?user_code=DROI-D001","expires_in":300,"interval":1}`)
		case "/authenticate":
			raw, _ := io.ReadAll(r.Body)
			form, _ := url.ParseQuery(string(raw))
			if form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
				t.Errorf("unexpected grant %q", form.Get("grant_type"))
			}
			if polls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"error":"authorization_pending"}`)
				return
			}
			io.WriteString(w, `{"access_token":"`+scoped+`","refresh_token":"droid-refresh-secret","user":{"email":"dev@example.com"}}`)
		case "/api/cli/whoami":
			if r.Header.Get("Authorization") != "Bearer "+scoped || r.Header.Get("X-Factory-Org-Id") != "org_01HWORKOS" {
				t.Errorf("whoami headers = %v", r.Header)
			}
			io.WriteString(w, `{"userId":"user_01","orgId":"factory-org-7","email":"dev@example.com","region":"us"}`)
		case "/api/billing/limits":
			if r.Header.Get("Authorization") != "Bearer "+scoped || r.Header.Get("X-Factory-Org-Id") != "factory-org-7" ||
				r.Header.Get("X-Factory-Client") != "cli" || r.Header.Get("X-AstrLink-Factory-Endpoint") != "" {
				t.Errorf("wrong Droid authentication on limits: %v", r.Header)
			}
			io.WriteString(w, `{"planType":"pro","limits":{"standard":{"fiveHour":{"usedPercent":20,"windowEnd":"2099-01-01T00:00:00Z"},"weekly":{"usedPercent":5,"windowEnd":"2099-01-02T00:00:00Z"}}},"extraUsageBalanceCents":0}`)
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
		accountauth.OAuthConfig{Provider: contract.SubscriptionProviderFactoryDroid, Issuer: upstream.URL, APIBaseURL: upstream.URL,
			DevicePollMinInterval: 5 * time.Millisecond, DevicePollMaxInterval: 5 * time.Millisecond})
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
		for _, secret := range []string{scoped, "droid-refresh-secret", "droid-device-secret", "dev@example.com"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("credential leaked in public response: %s", w.Body.String())
			}
		}
		return w.Body.Bytes()
	}
	var service contract.Service
	if err := json.Unmarshal(call("POST", ServicesPath, `{"name":"Droid","kind":"droid_subscription","responses_websocket_enabled":true}`, 201), &service); err != nil {
		t.Fatal(err)
	}
	if service.Subscription.Provider != contract.SubscriptionProviderFactoryDroid || len(service.Capabilities) != 4 || service.ResponsesWebSocket() {
		t.Fatalf("wrong provider configuration: %#v", service)
	}
	path := ServicesPath + "/" + string(service.ID)
	call("POST", path+"/authorization", `{"flow":"browser"}`, 422)
	call("POST", path+"/authorization", `{"flow":"authorization_code"}`, 422)
	var session contract.AuthorizationSession
	if err := json.Unmarshal(call("POST", path+"/authorization", `{"flow":"device_code"}`, 202), &session); err != nil {
		t.Fatal(err)
	}
	if session.Provider != contract.SubscriptionProviderFactoryDroid || session.DeviceCode == nil ||
		session.DeviceCode.UserCode != "DROI-D001" || session.DeviceCode.VerificationURL != "https://auth.factory.ai/device?user_code=DROI-D001" {
		t.Fatalf("invalid authorization session: %#v", session)
	}
	for {
		if err := json.Unmarshal(call("GET", path+"/authorization", "", 200), &session); err != nil {
			t.Fatal(err)
		}
		if session.Status == contract.AuthorizationSessionStatusCompleted {
			break
		}
		if session.Status != contract.AuthorizationSessionStatusPending {
			t.Fatalf("device session did not complete: %#v", session)
		}
		time.Sleep(5 * time.Millisecond)
	}
	var connected contract.Service
	if err := json.Unmarshal(call("GET", path, "", 200), &connected); err != nil {
		t.Fatal(err)
	}
	if connected.Subscription.Status != contract.SubscriptionStatusConnected || connected.Subscription.ProviderAccountID != "factory-org-7" {
		t.Fatalf("connected service = %#v", connected.Subscription)
	}
	models := string(call("POST", path+"/probe-models", `{"protocol":"openai.models"}`, 200))
	if !strings.Contains(models, "claude-opus-4-6") || !strings.Contains(models, "gpt-5.4") || !strings.Contains(models, "glm-5.2") {
		t.Fatalf("model probe = %s", models)
	}
	usageRaw := call("GET", path+"/usage", "", 200)
	var usage contract.SubscriptionUsage
	if err := json.Unmarshal(usageRaw, &usage); err != nil || usage.PlanType != "pro" || usage.Primary == nil ||
		usage.Primary.UsedPercent != 20 || usage.Secondary == nil || usage.Secondary.UsedPercent != 5 || usage.Credits != nil {
		t.Fatalf("invalid Droid usage: %s", usageRaw)
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
