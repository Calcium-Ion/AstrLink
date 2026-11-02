package codingplan_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/codingplan"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
)

var now = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

func TestUsageURLDerivesProviderRoutesFromConfiguredOrigin(t *testing.T) {
	cases := []struct {
		kind contract.ServiceKind
		base string
		want string
	}{
		{contract.ServiceKindKimiCoding, "https://api.kimi.com/coding", "https://api.kimi.com/coding/v1/usages"},
		{contract.ServiceKindKimiCoding, "https://api.kimi.ai/coding/", "https://api.kimi.ai/coding/v1/usages"},
		{contract.ServiceKindGLMCoding, "https://open.bigmodel.cn/api/anthropic", "https://open.bigmodel.cn/api/monitor/usage/quota/limit"},
		{contract.ServiceKindGLMCoding, "https://api.z.ai/api/paas/v4", "https://api.z.ai/api/monitor/usage/quota/limit"},
		{contract.ServiceKindMiniMaxCoding, "https://api.minimax.cn/anthropic", "https://api.minimaxi.com/v1/api/openplatform/coding_plan/remains"},
		{contract.ServiceKindMiniMaxCoding, "https://api.minimaxi.com/v1", "https://api.minimaxi.com/v1/api/openplatform/coding_plan/remains"},
		{contract.ServiceKindMiniMaxCoding, "https://api.minimax.io/anthropic", "https://api.minimax.io/v1/api/openplatform/coding_plan/remains"},
		{contract.ServiceKindMiniMaxCoding, "http://127.0.0.1:9/anthropic", "http://127.0.0.1:9/v1/api/openplatform/coding_plan/remains"},
		{contract.ServiceKindOpenCodeGo, "https://opencode.ai/zen/go/v1", "https://opencode.ai/zen/go/v1/usage"},
		{contract.ServiceKindOpenCodeGo, "https://opencode.ai/zen/go", "https://opencode.ai/zen/go/v1/usage"},
		{contract.ServiceKindNewAPI, "https://gateway.example.com/v1", "https://gateway.example.com/api/usage/token/"},
	}
	for _, tc := range cases {
		got, err := codingplan.UsageURL(tc.kind, tc.base)
		if err != nil || got != tc.want {
			t.Errorf("UsageURL(%s, %s) = %q, %v; want %q", tc.kind, tc.base, got, err, tc.want)
		}
	}
	if _, err := codingplan.UsageURL(contract.ServiceKindOpenCodeZen, "https://opencode.ai/zen/v1"); !errors.Is(err, codingplan.ErrUnsupported) {
		t.Fatalf("OpenCode Zen err = %v", err)
	}
	if _, err := codingplan.UsageURL(contract.ServiceKindKimiCoding, "not a url"); !errors.Is(err, codingplan.ErrUsageUnavailable) {
		t.Fatalf("invalid base err = %v", err)
	}
	for _, kind := range []contract.ServiceKind{contract.ServiceKindKimiCoding, contract.ServiceKindGLMCoding, contract.ServiceKindMiniMaxCoding, contract.ServiceKindOpenCodeGo, contract.ServiceKindNewAPI} {
		if !codingplan.Supports(kind) {
			t.Errorf("Supports(%s) = false", kind)
		}
	}
	for _, kind := range []contract.ServiceKind{contract.ServiceKindOpenCodeZen, contract.ServiceKindGLM, contract.ServiceKindClaudeSubscription} {
		if codingplan.Supports(kind) {
			t.Errorf("Supports(%s) = true", kind)
		}
	}
}

func TestDecodeKimiMapsRollingAndWeeklyAllowances(t *testing.T) {
	usage, err := codingplan.Decode(contract.ServiceKindKimiCoding, []byte(`{
		"limits": [{"window": {"duration": 18000}, "detail": {"limit": "200", "remaining": "150", "resetTime": "2026-09-22T15:00:00Z"}}],
		"usage": {"limit": 4000, "remaining": 1000, "resetTime": 1758758400}
	}`), now)
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	if usage.Primary == nil || usage.Primary.UsedPercent != 25 || *usage.Primary.LimitWindowSeconds != 5*3600 ||
		usage.Primary.ResetAt == nil || !usage.Primary.ResetAt.Equal(time.Date(2026, 9, 22, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("primary = %#v", usage.Primary)
	}
	if usage.Secondary == nil || usage.Secondary.UsedPercent != 75 || *usage.Secondary.LimitWindowSeconds != 7*24*3600 ||
		usage.Secondary.ResetAt == nil || !usage.Secondary.ResetAt.Equal(time.Unix(1758758400, 0).UTC()) {
		t.Fatalf("secondary = %#v", usage.Secondary)
	}
	if usage.LimitReached == nil || *usage.LimitReached || usage.PlanType != "" {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestDecodeGLMClassifiesWindowsByUnitAndKeepsLevel(t *testing.T) {
	usage, err := codingplan.Decode(contract.ServiceKindGLMCoding, []byte(`{
		"success": true, "code": 200,
		"data": {"level": "pro", "limits": [
			{"type": "TOKENS_LIMIT", "percentage": 40, "nextResetTime": 1758585600000, "unit": 6, "number": 7},
			{"type": "TOKENS_LIMIT", "percentage": 12.5, "nextResetTime": 1758560400000, "unit": 3, "number": 5},
			{"type": "TIME_LIMIT", "percentage": 99}
		]}
	}`), now)
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	if usage.PlanType != "pro" || usage.Primary == nil || usage.Primary.UsedPercent != 12.5 || *usage.Primary.LimitWindowSeconds != 5*3600 ||
		usage.Primary.ResetAt == nil || !usage.Primary.ResetAt.Equal(time.UnixMilli(1758560400000).UTC()) {
		t.Fatalf("primary = %#v plan=%q", usage.Primary, usage.PlanType)
	}
	if usage.Secondary == nil || usage.Secondary.UsedPercent != 40 || *usage.Secondary.LimitWindowSeconds != 7*24*3600 {
		t.Fatalf("secondary = %#v", usage.Secondary)
	}

	// Legacy single-window plans and entries without unit fall back to reset order.
	legacy, err := codingplan.Decode(contract.ServiceKindGLMCoding, []byte(`{"success":true,"data":{"level":"lite","limits":[
		{"type":"TOKENS_LIMIT","percentage":5,"nextResetTime":1758585600000},
		{"type":"TOKENS_LIMIT","percentage":3}
	]}}`), now)
	if err != nil || legacy.Primary == nil || legacy.Primary.UsedPercent != 3 || legacy.Secondary == nil || legacy.Secondary.UsedPercent != 5 {
		t.Fatalf("legacy = %#v err=%v", legacy, err)
	}
	if _, err := codingplan.Decode(contract.ServiceKindGLMCoding, []byte(`{"success":false,"msg":"invalid key owner@example.com"}`), now); err == nil || !strings.Contains(err.Error(), "provider error") || strings.Contains(err.Error(), "@") {
		t.Fatalf("business error = %v", err)
	}
}

func TestDecodeMiniMaxInvertsRemainingPercentAndHonoursWeeklyStatus(t *testing.T) {
	usage, err := codingplan.Decode(contract.ServiceKindMiniMaxCoding, []byte(`{
		"base_resp": {"status_code": 0, "status_msg": "success"},
		"model_remains": [
			{"model_name": "video", "current_interval_remaining_percent": 1},
			{"model_name": "general", "current_interval_remaining_percent": 80, "end_time": 1758560400000,
			 "current_weekly_status": 1, "current_weekly_remaining_percent": 55, "weekly_end_time": 1758585600000}
		]
	}`), now)
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	if usage.Primary == nil || usage.Primary.UsedPercent != 20 || usage.Primary.ResetAt == nil || *usage.Primary.LimitWindowSeconds != 5*3600 {
		t.Fatalf("primary = %#v", usage.Primary)
	}
	if usage.Secondary == nil || usage.Secondary.UsedPercent != 45 || *usage.Secondary.LimitWindowSeconds != 7*24*3600 {
		t.Fatalf("secondary = %#v", usage.Secondary)
	}
	noWeekly, err := codingplan.Decode(contract.ServiceKindMiniMaxCoding, []byte(`{"model_remains":[{"model_name":"general","current_interval_remaining_percent":0,"end_time":-1,"current_weekly_status":3,"current_weekly_remaining_percent":100}]}`), now)
	if err != nil || noWeekly.Primary == nil || noWeekly.Primary.UsedPercent != 100 || noWeekly.Primary.ResetAt != nil || noWeekly.Secondary != nil || noWeekly.LimitReached == nil || !*noWeekly.LimitReached {
		t.Fatalf("noWeekly = %#v primary=%#v err=%v", noWeekly, noWeekly.Primary, err)
	}
	if _, err := codingplan.Decode(contract.ServiceKindMiniMaxCoding, []byte(`{"base_resp":{"status_code":1004,"status_msg":"login fail"}}`), now); err == nil || !strings.Contains(err.Error(), "login fail (code 1004)") {
		t.Fatalf("business error = %v", err)
	}
}

func TestDecodeOpenCodeGoMapsThreeWindows(t *testing.T) {
	usage, err := codingplan.Decode(contract.ServiceKindOpenCodeGo, []byte(`{"usage":{
		"rolling": {"status": "ok", "percent": 0, "resetsAt": "2026-09-22T17:00:00Z"},
		"weekly": {"status": "rate-limited", "percent": 100, "resetsAt": "2026-09-25T00:00:00Z"},
		"monthly": {"status": "ok", "percent": 63, "resetsAt": "2026-10-01T00:00:00Z"}
	}}`), now)
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	// A 0% window carries a placeholder reset that must not be shown.
	if usage.Primary == nil || usage.Primary.UsedPercent != 0 || usage.Primary.ResetAt != nil {
		t.Fatalf("primary = %#v", usage.Primary)
	}
	if usage.Secondary == nil || usage.Secondary.UsedPercent != 100 || usage.Secondary.ResetAt == nil || usage.LimitReached == nil || !*usage.LimitReached {
		t.Fatalf("secondary = %#v reached=%v", usage.Secondary, usage.LimitReached)
	}
	if len(usage.AdditionalRateLimits) != 1 || usage.AdditionalRateLimits[0].LimitName != "Monthly" ||
		usage.AdditionalRateLimits[0].Primary == nil || usage.AdditionalRateLimits[0].Primary.UsedPercent != 63 ||
		*usage.AdditionalRateLimits[0].Primary.LimitWindowSeconds != 30*24*3600 {
		t.Fatalf("monthly = %#v", usage.AdditionalRateLimits)
	}
	if _, err := codingplan.Decode(contract.ServiceKindOpenCodeGo, []byte(`{"rollingUsage":1,"usagePercent":5}`), now); !errors.Is(err, codingplan.ErrUsageUnavailable) {
		t.Fatalf("legacy flat shape err = %v", err)
	}
}

func TestDecodeNewAPIConvertsKeyQuotaToUSD(t *testing.T) {
	usage, err := codingplan.Decode(contract.ServiceKindNewAPI, []byte(`{"code":true,"message":"ok","data":{
		"object":"token_usage","name":"desk","total_granted":5000000,"total_used":1250000,"total_available":3750000,
		"unlimited_quota":false,"model_limits":{},"model_limits_enabled":false,"expires_at":1790000000
	}}`), now)
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	quota := usage.Quota
	if quota == nil || quota.Unlimited || quota.UsedUSD != "2.5" || quota.RemainingUSD != "7.5" || quota.TotalUSD != "10" ||
		quota.ExpiresAt == nil || !quota.ExpiresAt.Equal(time.Unix(1790000000, 0).UTC()) {
		t.Fatalf("quota = %#v", quota)
	}
	if usage.Primary != nil || usage.LimitReached == nil || *usage.LimitReached {
		t.Fatalf("usage = %#v", usage)
	}

	// An unlimited key still counts availability down, so only its spend is kept.
	unlimited, err := codingplan.Decode(contract.ServiceKindNewAPI, []byte(`{"code":true,"data":{"total_granted":0,"total_used":1234567,"total_available":-1234567,"unlimited_quota":true,"expires_at":0}}`), now)
	if err != nil || unlimited.Quota == nil || !unlimited.Quota.Unlimited || unlimited.Quota.UsedUSD != "2.469134" ||
		unlimited.Quota.RemainingUSD != "" || unlimited.Quota.TotalUSD != "" || unlimited.Quota.ExpiresAt != nil ||
		unlimited.LimitReached == nil || *unlimited.LimitReached {
		t.Fatalf("unlimited = %#v quota=%#v err=%v", unlimited, unlimited.Quota, err)
	}

	// An overdrawn key reads as empty, not negative.
	exhausted, err := codingplan.Decode(contract.ServiceKindNewAPI, []byte(`{"code":true,"data":{"total_used":600000,"total_available":-100000,"unlimited_quota":false}}`), now)
	if err != nil || exhausted.Quota == nil || exhausted.Quota.RemainingUSD != "0" || exhausted.Quota.TotalUSD != "1.2" ||
		exhausted.LimitReached == nil || !*exhausted.LimitReached {
		t.Fatalf("exhausted = %#v quota=%#v err=%v", exhausted, exhausted.Quota, err)
	}

	if _, err := codingplan.Decode(contract.ServiceKindNewAPI, []byte(`{"success":false,"message":"令牌已过期 owner@example.com"}`), now); !errors.Is(err, codingplan.ErrUsageUnavailable) || strings.Contains(err.Error(), "@") {
		t.Fatalf("business error = %v", err)
	}
	if _, err := codingplan.Decode(contract.ServiceKindNewAPI, []byte(`{"code":true,"data":{"total_available":1}}`), now); !errors.Is(err, codingplan.ErrUsageUnavailable) {
		t.Fatalf("missing total_used err = %v", err)
	}
}

func TestDecodeRejectsMalformedPayloads(t *testing.T) {
	for _, kind := range []contract.ServiceKind{contract.ServiceKindKimiCoding, contract.ServiceKindGLMCoding, contract.ServiceKindMiniMaxCoding, contract.ServiceKindOpenCodeGo, contract.ServiceKindNewAPI} {
		for _, body := range []string{``, `[]`, `null`, `nope`, `{}`} {
			if _, err := codingplan.Decode(kind, []byte(body), now); !errors.Is(err, codingplan.ErrUsageUnavailable) {
				t.Errorf("Decode(%s, %q) err = %v", kind, body, err)
			}
		}
	}
}

type memorySecrets struct {
	mu    sync.Mutex
	items map[secretstore.Ref][]byte
}

func (store *memorySecrets) Get(_ context.Context, ref secretstore.Ref) ([]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.items[ref]
	if !ok {
		return nil, secretstore.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (store *memorySecrets) Put(_ context.Context, ref secretstore.Ref, secret []byte) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.items[ref] = append([]byte(nil), secret...)
	return nil
}

func (store *memorySecrets) Delete(_ context.Context, ref secretstore.Ref) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.items, ref)
	return nil
}

func codingPlanService(id contract.ServiceID, kind contract.ServiceKind, baseURL string) contract.Service {
	return contract.Service{
		ID: id, Name: "plan", Kind: kind, Enabled: true,
		Capabilities: []contract.Capability{{Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeNative, Streaming: true}},
		HTTP: &contract.HTTPConnection{
			BaseURL:       baseURL,
			Auth:          contract.ServiceAuth{Scheme: contract.AuthSchemeAnthropicAPIKey},
			CredentialRef: "local://service/" + string(id),
		},
		CreatedAt: now, UpdatedAt: now,
	}
}

func TestFetcherReadsNewAPIKeyQuotaAtTheSiteRate(t *testing.T) {
	var usageCalls, statusCalls, badHeaders int
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch request.URL.Path {
		case "/api/usage/token/":
			usageCalls++
			if request.Header.Get("Authorization") != "Bearer sk-newapi" {
				badHeaders++
			}
			_, _ = writer.Write([]byte(`{"code":true,"message":"ok","data":{"total_granted":300,"total_used":100,"total_available":200,"unlimited_quota":false,"expires_at":0}}`))
		case "/api/status":
			statusCalls++
			// The site rate is public; the key must not leak to it.
			if request.Header.Get("Authorization") != "" {
				badHeaders++
			}
			_, _ = writer.Write([]byte(`{"success":true,"data":{"quota_per_unit":100}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.Close)

	secrets := &memorySecrets{items: map[secretstore.Ref][]byte{"local://service/service_newapi": []byte("sk-newapi")}}
	fetcher := codingplan.New(secrets, upstream.Client())
	service := codingPlanService("service_newapi", contract.ServiceKindNewAPI, upstream.URL+"/v1")
	for range 2 {
		usage, err := fetcher.Usage(context.Background(), service)
		if err != nil || usage.Quota == nil || usage.Quota.UsedUSD != "1" || usage.Quota.RemainingUSD != "2" || usage.Quota.TotalUSD != "3" {
			t.Fatalf("Usage() = %#v err=%v", usage, err)
		}
		if err := usage.Validate(); err != nil {
			t.Fatalf("Validate() = %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if usageCalls != 1 || statusCalls != 1 || badHeaders != 0 {
		t.Fatalf("usage calls=%d status calls=%d bad headers=%d; want one of each, cached", usageCalls, statusCalls, badHeaders)
	}
}

func TestFetcherUsesProviderRouteAuthAndNamesProviderOnFailure(t *testing.T) {
	var calls, badHeaders int
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		switch request.URL.Path {
		case "/api/monitor/usage/quota/limit":
			// Zhipu takes the raw key without a Bearer prefix.
			if request.Header.Get("Authorization") != "glm-secret-key" || request.Header.Get("Accept-Language") == "" {
				badHeaders++
			}
			_, _ = writer.Write([]byte(`{"success":true,"data":{"level":"max","limits":[{"type":"TOKENS_LIMIT","percentage":7,"unit":3}]}}`))
		case "/coding/v1/usages":
			if request.Header.Get("Authorization") != "Bearer kimi-secret-key" {
				badHeaders++
			}
			http.Error(writer, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		case "/zen/go/v1/usage":
			if request.Header.Get("Authorization") != "Bearer go-secret-key" || request.Header.Get("X-Api-Key") != "" || strings.Contains(strings.ToLower(request.UserAgent()), "astrlink") {
				badHeaders++
			}
			http.Error(writer, `{"error":"EntitlementError"}`, http.StatusForbidden)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.Close)

	secrets := &memorySecrets{items: map[secretstore.Ref][]byte{
		"local://service/service_glm_plan":  []byte("glm-secret-key\n"),
		"local://service/service_kimi_plan": []byte("kimi-secret-key"),
		"local://service/service_go_plan":   []byte("go-secret-key"),
	}}
	fetcher := codingplan.New(secrets, upstream.Client())

	glm := codingPlanService("service_glm_plan", contract.ServiceKindGLMCoding, upstream.URL+"/api/anthropic")
	usage, err := fetcher.Usage(context.Background(), glm)
	if err != nil || usage.ServiceID != glm.ID || usage.PlanType != "max" || usage.Primary == nil || usage.Primary.UsedPercent != 7 || usage.FetchedAt.IsZero() {
		t.Fatalf("GLM Usage() = %#v err=%v", usage, err)
	}
	if _, err := fetcher.Usage(context.Background(), glm); err != nil {
		t.Fatalf("cached GLM Usage() = %v", err)
	}

	kimi := codingPlanService("service_kimi_plan", contract.ServiceKindKimiCoding, upstream.URL+"/coding")
	_, err = fetcher.Usage(context.Background(), kimi)
	if !errors.Is(err, codingplan.ErrUsageUnavailable) || err.Error() != "kimi usage unavailable: status 401" {
		t.Fatalf("Kimi Usage() err = %v", err)
	}

	openCodeGo := codingPlanService("service_go_plan", contract.ServiceKindOpenCodeGo, upstream.URL+"/zen/go/v1")
	_, err = fetcher.Usage(context.Background(), openCodeGo)
	if !errors.Is(err, codingplan.ErrUsageUnavailable) || !strings.Contains(err.Error(), "opencode-go usage unavailable: key has no OpenCode Go subscription") {
		t.Fatalf("OpenCode Go Usage() err = %v", err)
	}

	missing := codingPlanService("service_missing_plan", contract.ServiceKindMiniMaxCoding, upstream.URL+"/anthropic")
	if _, err := fetcher.Usage(context.Background(), missing); !errors.Is(err, codingplan.ErrCredentialUnavailable) {
		t.Fatalf("missing credential err = %v", err)
	}
	zen := codingPlanService("service_zen", contract.ServiceKindOpenCodeZen, upstream.URL+"/zen/v1")
	if _, err := fetcher.Usage(context.Background(), zen); !errors.Is(err, codingplan.ErrUnsupported) {
		t.Fatalf("OpenCode Zen err = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 3 {
		t.Fatalf("upstream calls = %d, want 3 (GLM cached, Kimi, OpenCode Go)", calls)
	}
	if badHeaders != 0 {
		t.Fatalf("%d requests carried the wrong provider auth headers", badHeaders)
	}
}

type testClock struct {
	mu sync.Mutex
	at time.Time
}

func (clock *testClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.at
}

func (clock *testClock) Advance(step time.Duration) {
	clock.mu.Lock()
	clock.at = clock.at.Add(step)
	clock.mu.Unlock()
}

func TestFetcherRefreshReusesAYoungSnapshotAndRereadsAnEditedService(t *testing.T) {
	var mu sync.Mutex
	var usageCalls, statusCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch request.URL.Path {
		case "/api/usage/token/":
			usageCalls++
			_, _ = fmt.Fprintf(writer, `{"code":true,"data":{"total_used":%d,"total_available":1000,"unlimited_quota":false}}`, usageCalls*100)
		case "/api/status":
			statusCalls++
			_, _ = writer.Write([]byte(`{"success":true,"data":{"quota_per_unit":100}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.Close)

	clock := &testClock{at: now}
	secrets := &memorySecrets{items: map[secretstore.Ref][]byte{"local://service/service_newapi": []byte("sk-newapi")}}
	fetcher := codingplan.New(secrets, upstream.Client())
	fetcher.SetClock(clock.Now)
	service := codingPlanService("service_newapi", contract.ServiceKindNewAPI, upstream.URL+"/v1")
	used := func(step string, read func(context.Context, contract.Service) (contract.SubscriptionUsage, error), service contract.Service, want string) {
		t.Helper()
		usage, err := read(context.Background(), service)
		if err != nil || usage.Quota == nil || usage.Quota.UsedUSD != want {
			t.Fatalf("%s: usage = %#v err=%v, want $%s used", step, usage.Quota, err, want)
		}
	}

	used("first read", fetcher.Usage, service, "1")
	used("click right after", fetcher.Refresh, service, "1")
	clock.Advance(11 * time.Second)
	used("later click", fetcher.Refresh, service, "2")
	used("scheduled read after the click", fetcher.Usage, service, "2")
	// Saving a new key bumps UpdatedAt inside the 5-minute window.
	edited := service
	edited.UpdatedAt = now.Add(time.Minute)
	used("read after an edit", fetcher.Usage, edited, "3")

	mu.Lock()
	defer mu.Unlock()
	if usageCalls != 3 || statusCalls != 1 {
		t.Fatalf("usage calls=%d status calls=%d; want 3 and one site rate read", usageCalls, statusCalls)
	}
}

func TestFetcherPausesASiteThatAnswers429(t *testing.T) {
	var mu sync.Mutex
	var usageCalls int
	limited, retryAfter := true, ""
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch request.URL.Path {
		case "/api/usage/token/":
			usageCalls++
			if limited {
				if retryAfter != "" {
					writer.Header().Set("Retry-After", retryAfter)
				}
				writer.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = writer.Write([]byte(`{"code":true,"data":{"total_used":0,"total_available":500000,"unlimited_quota":false}}`))
		case "/api/status":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.Close)
	set := func(isLimited bool, header string) {
		mu.Lock()
		limited, retryAfter = isLimited, header
		mu.Unlock()
	}
	calls := func() int {
		mu.Lock()
		defer mu.Unlock()
		return usageCalls
	}

	clock := &testClock{at: now}
	secrets := &memorySecrets{items: map[secretstore.Ref][]byte{
		"local://service/service_newapi_first":  []byte("sk-first"),
		"local://service/service_newapi_second": []byte("sk-second"),
	}}
	fetcher := codingplan.New(secrets, upstream.Client())
	fetcher.SetClock(clock.Now)
	first := codingPlanService("service_newapi_first", contract.ServiceKindNewAPI, upstream.URL+"/v1")
	second := codingPlanService("service_newapi_second", contract.ServiceKindNewAPI, upstream.URL)
	ctx := context.Background()

	var limit *codingplan.RateLimitError
	_, err := fetcher.Usage(ctx, first)
	if !errors.As(err, &limit) || !errors.Is(err, codingplan.ErrRateLimited) || limit.RetryAfter != 5*time.Minute ||
		err.Error() != "new-api usage lookups are rate limited; retry in 5m0s" {
		t.Fatalf("429 err = %v", err)
	}
	// New API counts the limit per client IP, so another key on the site
	// waits out the same pause without asking.
	clock.Advance(time.Minute)
	if _, err := fetcher.Refresh(ctx, second); !errors.As(err, &limit) || limit.RetryAfter != 4*time.Minute || calls() != 1 {
		t.Fatalf("paused refresh err = %v after %d calls", err, calls())
	}

	clock.Advance(4 * time.Minute)
	set(false, "")
	if usage, err := fetcher.Refresh(ctx, second); err != nil || usage.Quota == nil || usage.Quota.RemainingUSD != "1" {
		t.Fatalf("refresh after the pause = %#v err=%v", usage.Quota, err)
	}

	set(true, "42")
	if _, err := fetcher.Refresh(ctx, first); !errors.As(err, &limit) || limit.RetryAfter != 42*time.Second {
		t.Fatalf("Retry-After err = %v", err)
	}
	if calls() != 3 {
		t.Fatalf("usage calls = %d, want 3", calls())
	}
}

func TestFetcherReaderJoinsALookupInFlight(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/coding/v1/usages" {
			http.NotFound(writer, request)
			return
		}
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		_, _ = writer.Write([]byte(`{"usage":{"limit":100,"remaining":40}}`))
	}))
	t.Cleanup(upstream.Close)
	releaseUpstream := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseUpstream)

	secrets := &memorySecrets{items: map[secretstore.Ref][]byte{"local://service/service_kimi_plan": []byte("kimi-secret-key")}}
	fetcher := codingplan.New(secrets, upstream.Client())
	fetcher.SetClock(func() time.Time { return now })
	service := codingPlanService("service_kimi_plan", contract.ServiceKindKimiCoding, upstream.URL+"/coding")

	type result struct {
		usage contract.SubscriptionUsage
		err   error
	}
	leader := make(chan result, 1)
	go func() {
		usage, err := fetcher.Refresh(context.Background(), service)
		leader <- result{usage, err}
	}()
	select {
	case <-entered:
	case early := <-leader:
		t.Fatalf("lookup finished before reaching the provider: %#v", early)
	}

	// The provider is still answering the first click: a second one waits
	// for that answer instead of asking again, and gives up with its context.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fetcher.Refresh(cancelled, service); !errors.Is(err, context.Canceled) || errors.Is(err, codingplan.ErrUsageUnavailable) {
		t.Fatalf("joined reader err = %v", err)
	}

	releaseUpstream()
	answered := <-leader
	if answered.err != nil || answered.usage.Secondary == nil || answered.usage.Secondary.UsedPercent != 60 {
		t.Fatalf("lookup = %#v err=%v", answered.usage, answered.err)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", calls.Load())
	}
}

func TestFetcherForgetsTheSnapshotOfARejectedKey(t *testing.T) {
	var mu sync.Mutex
	var calls int
	rejected := false
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if request.URL.Path != "/coding/v1/usages" {
			http.NotFound(writer, request)
			return
		}
		calls++
		if rejected {
			http.Error(writer, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		_, _ = writer.Write([]byte(`{"usage":{"limit":100,"remaining":40}}`))
	}))
	t.Cleanup(upstream.Close)

	clock := &testClock{at: now}
	secrets := &memorySecrets{items: map[secretstore.Ref][]byte{"local://service/service_kimi_plan": []byte("kimi-secret-key")}}
	fetcher := codingplan.New(secrets, upstream.Client())
	fetcher.SetClock(clock.Now)
	service := codingPlanService("service_kimi_plan", contract.ServiceKindKimiCoding, upstream.URL+"/coding")
	ctx := context.Background()

	if _, err := fetcher.Usage(ctx, service); err != nil {
		t.Fatalf("Usage() = %v", err)
	}
	mu.Lock()
	rejected = true
	mu.Unlock()
	clock.Advance(11 * time.Second)
	if _, err := fetcher.Refresh(ctx, service); !errors.Is(err, codingplan.ErrUsageUnavailable) {
		t.Fatalf("Refresh() err = %v", err)
	}
	// Still inside the 30s window, the next scheduled read asks again rather
	// than reporting the rejected key's last numbers as current.
	if _, err := fetcher.Usage(ctx, service); !errors.Is(err, codingplan.ErrUsageUnavailable) {
		t.Fatalf("Usage() after rejection err = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 3 {
		t.Fatalf("provider calls = %d, want 3", calls)
	}
}

func TestFetcherNamesTheReasonOfAFailedRead(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Header.Get("Authorization") {
		case "Bearer rejected":
			http.Error(writer, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		case "Bearer down":
			http.Error(writer, "bad gateway", http.StatusBadGateway)
		case "Bearer html":
			_, _ = writer.Write([]byte("<html>login</html>"))
		case "Bearer empty":
			_, _ = writer.Write([]byte(`{"user":{}}`))
		case "Bearer minimax":
			_, _ = writer.Write([]byte(`{"base_resp":{"status_code":2049,"status_msg":"invalid api key"}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.Close)
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	cases := []struct {
		key, base string
		kind      contract.ServiceKind
		want      codingplan.FailureReason
	}{
		{"rejected", upstream.URL + "/coding", contract.ServiceKindKimiCoding, codingplan.ReasonKeyRejected},
		{"down", upstream.URL + "/coding", contract.ServiceKindKimiCoding, codingplan.ReasonProviderDown},
		{"missing", upstream.URL + "/coding", contract.ServiceKindKimiCoding, codingplan.ReasonWrongAddress},
		{"html", upstream.URL + "/coding", contract.ServiceKindKimiCoding, codingplan.ReasonUnexpectedReply},
		{"empty", upstream.URL + "/coding", contract.ServiceKindKimiCoding, codingplan.ReasonNoQuota},
		{"minimax", upstream.URL + "/v1", contract.ServiceKindMiniMaxCoding, codingplan.ReasonKeyRejected},
		{"closed", closedURL + "/coding", contract.ServiceKindKimiCoding, codingplan.ReasonUnreachable},
	}
	for index, test := range cases {
		id := contract.ServiceID(fmt.Sprintf("service_reason_%d", index))
		secrets := &memorySecrets{items: map[secretstore.Ref][]byte{
			secretstore.Ref("local://service/" + string(id)): []byte(test.key),
		}}
		fetcher := codingplan.New(secrets, upstream.Client())
		_, err := fetcher.Usage(context.Background(), codingPlanService(id, test.kind, test.base))
		if got := codingplan.ReasonOf(err); got != test.want || !errors.Is(err, codingplan.ErrUsageUnavailable) {
			t.Errorf("%s: reason = %q (err %v), want %q", test.key, got, err, test.want)
		}
	}
}
