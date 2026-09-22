package controlapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/codingplan"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func TestGetServiceUsageReturnsSanitizedSnapshot(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/backend-api/wham/usage" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer access-secret-token-value" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"user_id":    "user_secret",
			"account_id": "acct_secret",
			"email":      "owner@example.com",
			"plan_type":  "plus",
			"rate_limit": map[string]any{
				"allowed":       true,
				"limit_reached": false,
				"primary_window": map[string]any{
					"used_percent":         34,
					"limit_window_seconds": 18000,
					"reset_after_seconds":  120,
					"reset_at":             time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC).Unix(),
				},
				"secondary_window": map[string]any{
					"used_percent":         12,
					"limit_window_seconds": 604800,
					"reset_after_seconds":  86400,
					"reset_at":             time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC).Unix(),
				},
			},
			"rate_limit_reset_credits": map[string]any{"available_count": 1},
		})
	}))
	t.Cleanup(upstream.Close)

	store, credentials, handler := newUsageHandler(t, upstream, "service_codex_usage")
	service := createServiceForTest(t, handler, `{"name":"Codex usage","kind":"codex_subscription"}`)
	connectSubscriptionForTest(t, store, credentials, service.ID)

	response := serviceRequestForTest(t, handler, http.MethodGet, ServicesPath+"/"+string(service.ID)+"/usage", "", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var usage contract.SubscriptionUsage
	decode(t, response, &usage)
	if usage.ServiceID != service.ID || usage.PlanType != "plus" || usage.Primary == nil || usage.Primary.UsedPercent != 34 {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.RateLimitResetCredits == nil || usage.RateLimitResetCredits.AvailableCount != 1 {
		t.Fatalf("reset credits = %#v", usage.RateLimitResetCredits)
	}
	body := response.Body.String()
	for _, leaked := range []string{"owner@example.com", "user_secret", "acct_secret", "access-secret-token-value"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("usage leaked %q: %s", leaked, body)
		}
	}
}

func TestGetServiceUsageRefreshBypassesTheSnapshotCache(t *testing.T) {
	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/backend-api/wham/usage" {
			http.NotFound(writer, request)
			return
		}
		n := atomic.AddInt32(&hits, 1)
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"plan_type": "plus",
			"rate_limit": map[string]any{
				"allowed":        true,
				"primary_window": map[string]any{"used_percent": 10 * n, "limit_window_seconds": 18000},
			},
		})
	}))
	t.Cleanup(upstream.Close)

	store, credentials, handler := newUsageHandler(t, upstream, "service_codex_refresh")
	service := createServiceForTest(t, handler, `{"name":"Codex refresh","kind":"codex_subscription"}`)
	connectSubscriptionForTest(t, store, credentials, service.ID)
	path := ServicesPath + "/" + string(service.ID) + "/usage"

	read := func(query string) float64 {
		response := serviceRequestForTest(t, handler, http.MethodGet, path+query, "", "", "")
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var usage contract.SubscriptionUsage
		decode(t, response, &usage)
		if usage.Primary == nil {
			t.Fatalf("usage = %#v", usage)
		}
		return usage.Primary.UsedPercent
	}

	// Scheduled readers get the 30s snapshot: one upstream call serves both.
	if first, second := read(""), read(""); first != 10 || second != 10 || atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("cached reads = %v, %v with %d upstream hits", first, second, hits)
	}
	// An operator refresh must show the provider's current numbers.
	if fresh := read("?refresh=1"); fresh != 20 || atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("refresh read = %v with %d upstream hits", fresh, hits)
	}
	// And it re-primes the snapshot for the readers that follow.
	if again := read(""); again != 20 || atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("post-refresh read = %v with %d upstream hits", again, hits)
	}
}

func TestGetServiceUsageRejectsDisconnectedAndHTTPServices(t *testing.T) {
	_, handler := newServiceHandler(t, "service_codex_disconnected", "service_http_usage")
	codex := createServiceForTest(t, handler, `{"name":"Codex","kind":"codex_subscription"}`)
	gateway := createServiceForTest(t, handler, `{
		"name":"new-api","kind":"newapi",
		"http":{"base_url":"https://gateway.example/v1","auth":{"scheme":"none"}},
		"capabilities":[{"protocol":"openai.responses","mode":"delegated","streaming":true}]
	}`)

	disconnected := serviceRequestForTest(t, handler, http.MethodGet, ServicesPath+"/"+string(codex.ID)+"/usage", "", "", "")
	if disconnected.Code != http.StatusConflict || !strings.Contains(disconnected.Body.String(), "service_not_connected") {
		t.Fatalf("disconnected status=%d body=%s", disconnected.Code, disconnected.Body.String())
	}

	httpService := serviceRequestForTest(t, handler, http.MethodGet, ServicesPath+"/"+string(gateway.ID)+"/usage", "", "", "")
	if httpService.Code != http.StatusConflict || !strings.Contains(httpService.Body.String(), "service_not_subscription") {
		t.Fatalf("http status=%d body=%s", httpService.Code, httpService.Body.String())
	}

	missing := serviceRequestForTest(t, handler, http.MethodGet, ServicesPath+"/service_missing_usage/usage", "", "", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d body=%s", missing.Code, missing.Body.String())
	}
}

func TestResetServiceUsageRedeemsOfficialCredit(t *testing.T) {
	var consumeBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/backend-api/wham/usage":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"plan_type":                "plus",
				"rate_limit_reset_credits": map[string]any{"available_count": 1},
			})
		case request.Method == http.MethodPost && request.URL.Path == "/backend-api/wham/rate-limit-reset-credits/consume":
			if request.Header.Get("Authorization") != "Bearer access-secret-token-value" {
				http.Error(writer, "unauthorized", http.StatusUnauthorized)
				return
			}
			raw, _ := io.ReadAll(request.Body)
			consumeBody = string(raw)
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"code":          "reset",
				"credit":        map[string]any{"id": "RateLimitResetCredit_secret"},
				"windows_reset": 2,
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.Close)

	store, credentials, handler := newUsageHandler(t, upstream, "service_codex_usage_reset")
	service := createServiceForTest(t, handler, `{"name":"Codex usage","kind":"codex_subscription"}`)
	connectSubscriptionForTest(t, store, credentials, service.ID)

	response := serviceRequestForTest(t, handler, http.MethodPost, ServicesPath+"/"+string(service.ID)+"/usage/reset", "", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result contract.SubscriptionUsageReset
	decode(t, response, &result)
	if result.ServiceID != service.ID || result.Outcome != contract.UsageResetOutcomeReset ||
		result.WindowsReset == nil || *result.WindowsReset != 2 {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(consumeBody, `"redeem_request_id"`) || strings.Contains(consumeBody, "credit_id") {
		t.Fatalf("consume body = %s", consumeBody)
	}
	if strings.Contains(response.Body.String(), "RateLimitResetCredit_secret") {
		t.Fatalf("reset leaked credit: %s", response.Body.String())
	}
}

func TestResetServiceUsageRejectsNoCreditAndHTTPServices(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/backend-api/wham/rate-limit-reset-credits/consume" {
			_ = json.NewEncoder(writer).Encode(map[string]any{"code": "no_credit"})
			return
		}
		http.NotFound(writer, request)
	}))
	t.Cleanup(upstream.Close)

	store, credentials, handler := newUsageHandler(t, upstream, "service_codex_usage_reset_fail")
	service := createServiceForTest(t, handler, `{"name":"Codex usage","kind":"codex_subscription"}`)
	connectSubscriptionForTest(t, store, credentials, service.ID)

	missing := serviceRequestForTest(t, handler, http.MethodPost, ServicesPath+"/"+string(service.ID)+"/usage/reset", "", "", "")
	if missing.Code != http.StatusConflict || !strings.Contains(missing.Body.String(), "no_reset_credit") {
		t.Fatalf("no credit status=%d body=%s", missing.Code, missing.Body.String())
	}

	_, httpHandler := newServiceHandler(t, "service_http_usage_reset")
	gateway := createServiceForTest(t, httpHandler, `{
		"name":"new-api","kind":"newapi",
		"http":{"base_url":"https://gateway.example/v1","auth":{"scheme":"none"}},
		"capabilities":[{"protocol":"openai.responses","mode":"delegated","streaming":true}]
	}`)
	httpService := serviceRequestForTest(t, httpHandler, http.MethodPost, ServicesPath+"/"+string(gateway.ID)+"/usage/reset", "", "", "")
	if httpService.Code != http.StatusConflict || !strings.Contains(httpService.Body.String(), "service_not_subscription") {
		t.Fatalf("http status=%d body=%s", httpService.Code, httpService.Body.String())
	}
}

func TestGetServiceUsageMapsUpstreamFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, `{"email":"owner@example.com"}`, http.StatusBadGateway)
	}))
	t.Cleanup(upstream.Close)

	store, credentials, handler := newUsageHandler(t, upstream, "service_codex_usage_fail")
	service := createServiceForTest(t, handler, `{"name":"Codex usage","kind":"codex_subscription"}`)
	connectSubscriptionForTest(t, store, credentials, service.ID)

	response := serviceRequestForTest(t, handler, http.MethodGet, ServicesPath+"/"+string(service.ID)+"/usage", "", "", "")
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "owner@example.com") ||
		strings.Contains(response.Body.String(), "access-secret-token-value") {
		t.Fatalf("error leaked secrets: %s", response.Body.String())
	}
}

func newUsageHandler(t *testing.T, upstream *httptest.Server, id contract.ServiceID) (*sqlite.Store, accountauth.AccountCredentialStore, *Handler) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatalf("sqlite.Open() = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	credentials := accountauth.NewMemoryCredentialStore()
	manager, err := subscription.NewManager(
		subscription.StorageAccountStore{Store: store},
		credentials,
		accountauth.OAuthConfig{
			ClientID:   "astrlink_usage_test_client",
			APIBaseURL: upstream.URL + "/backend-api/codex",
			HTTPClient: upstream.Client(),
			Now:        func() time.Time { return time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC) },
		},
	)
	if err != nil {
		t.Fatalf("subscription.NewManager() = %v", err)
	}
	handler, err := NewWithDependencies(
		contract.DefaultVersionResponse("0.1.0-test", "abc1234"),
		Dependencies{
			ServiceStore:  store,
			Subscriptions: manager,
			CodingPlans:   codingplan.New(store, upstream.Client()),
			ControlToken:  testControlToken,
			NewServiceID:  func() (contract.ServiceID, error) { return id, nil },
		},
	)
	if err != nil {
		t.Fatalf("NewWithDependencies() = %v", err)
	}
	return store, credentials, handler
}

func connectSubscriptionForTest(
	t *testing.T,
	store *sqlite.Store,
	credentials accountauth.AccountCredentialStore,
	id contract.ServiceID,
) {
	t.Helper()
	record, err := store.GetService(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	expires := now.Add(time.Hour)
	if err := credentials.Put(context.Background(), id, accountauth.AccountTokens{
		AccessToken:  "access-secret-token-value",
		RefreshToken: "refresh-secret-token-value",
		AccountID:    "acct_12345678",
		ExpiresAt:    expires,
	}); err != nil {
		t.Fatal(err)
	}
	record.Service.Subscription.Status = contract.SubscriptionStatusConnected
	record.Service.Subscription.CredentialRef = accountauth.CredentialRefFor(id)
	record.Service.Subscription.AccountHint = "acct_***5678"
	record.Service.Subscription.ProviderAccountID = "acct_12345678"
	record.Service.Subscription.TokenExpiresAt = &expires
	if _, err := store.UpdateService(context.Background(), record.Service, storage.CredentialMutation{}, record.ETag); err != nil {
		t.Fatal(err)
	}
}

func TestGetServiceUsageReadsCodingPlanQuotaWithServiceKey(t *testing.T) {
	var authorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/coding/v1/usages" {
			http.NotFound(writer, request)
			return
		}
		authorization = request.Header.Get("Authorization")
		_, _ = writer.Write([]byte(`{
			"user": {"email": "owner@example.com"},
			"limits": [{"detail": {"limit": 100, "remaining": 40, "resetTime": "2026-09-22T15:00:00Z"}}],
			"usage": {"limit": 1000, "remaining": 900, "resetTime": "2026-09-25T00:00:00Z"}
		}`))
	}))
	t.Cleanup(upstream.Close)

	_, _, handler := newUsageHandler(t, upstream, "service_kimi_plan")
	service := createServiceForTest(t, handler, `{
		"name":"Kimi Coding","kind":"kimi_coding","models":["kimi-k2-thinking"],
		"http":{"base_url":"`+upstream.URL+`/coding","auth":{"scheme":"anthropic_api_key"},"credential":{"secret":"kimi-plan-secret"}},
		"capabilities":[{"protocol":"anthropic.messages","mode":"native","streaming":true}]
	}`)

	response := serviceRequestForTest(t, handler, http.MethodGet, ServicesPath+"/"+string(service.ID)+"/usage", "", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if authorization != "Bearer kimi-plan-secret" {
		t.Fatalf("Kimi usage authorization = %q", authorization)
	}
	var usage contract.SubscriptionUsage
	decode(t, response, &usage)
	if usage.ServiceID != service.ID || usage.Primary == nil || usage.Primary.UsedPercent != 60 || usage.Secondary == nil || usage.Secondary.UsedPercent != 10 {
		t.Fatalf("usage = %#v", usage)
	}
	if strings.Contains(response.Body.String(), "owner@example.com") || strings.Contains(response.Body.String(), "kimi-plan-secret") {
		t.Fatalf("usage leaked account material: %s", response.Body.String())
	}

	// newUsageHandler pins one service ID per handler, so the keyless case gets its own.
	_, _, keylessHandler := newUsageHandler(t, upstream, "service_glm_keyless")
	keyless := createServiceForTest(t, keylessHandler, `{
		"name":"GLM Coding","kind":"glm_coding",
		"http":{"base_url":"`+upstream.URL+`/api/anthropic","auth":{"scheme":"none"}},
		"capabilities":[{"protocol":"anthropic.messages","mode":"native","streaming":true}]
	}`)
	missingKey := serviceRequestForTest(t, keylessHandler, http.MethodGet, ServicesPath+"/"+string(keyless.ID)+"/usage", "", "", "")
	if missingKey.Code != http.StatusConflict || !strings.Contains(missingKey.Body.String(), "service_credential_unavailable") {
		t.Fatalf("keyless status=%d body=%s", missingKey.Code, missingKey.Body.String())
	}
}
