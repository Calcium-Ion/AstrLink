package codingplan_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	for _, kind := range []contract.ServiceKind{contract.ServiceKindKimiCoding, contract.ServiceKindGLMCoding, contract.ServiceKindMiniMaxCoding, contract.ServiceKindOpenCodeGo} {
		if !codingplan.Supports(kind) {
			t.Errorf("Supports(%s) = false", kind)
		}
	}
	for _, kind := range []contract.ServiceKind{contract.ServiceKindOpenCodeZen, contract.ServiceKindNewAPI, contract.ServiceKindGLM, contract.ServiceKindClaudeSubscription} {
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

func TestDecodeRejectsMalformedPayloads(t *testing.T) {
	for _, kind := range []contract.ServiceKind{contract.ServiceKindKimiCoding, contract.ServiceKindGLMCoding, contract.ServiceKindMiniMaxCoding, contract.ServiceKindOpenCodeGo} {
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
