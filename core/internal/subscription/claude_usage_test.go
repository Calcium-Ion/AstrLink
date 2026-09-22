package subscription_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

// Captured April 2026: flat windows only, nullable dollar fields, one legacy
// per-model key populated and the others null.
const claudeLegacyUsageFixture = `{
  "five_hour": {"utilization": 33.0, "resets_at": "2026-04-11T07:00:00.528743+00:00", "limit_dollars": null, "used_dollars": null, "remaining_dollars": null},
  "seven_day": {"utilization": 13.0, "resets_at": "2026-04-17T00:59:59.951713+00:00"},
  "seven_day_oauth_apps": null,
  "seven_day_opus": null,
  "seven_day_sonnet": {"utilization": 1.0, "resets_at": "2026-04-16T03:00:00.951719+00:00"},
  "seven_day_cowork": null,
  "extra_usage": {"is_enabled": false, "monthly_limit": null, "used_credits": null, "utilization": null}
}`

// Captured September 2026: per-model weekly caps moved into limits[] and the
// legacy seven_day_<model> keys are null. The old decoder rejected this body
// because limits is an array.
const claudeLimitsUsageFixture = `{
  "five_hour": {"utilization": 9.0, "resets_at": "2026-09-14T02:10:00Z"},
  "seven_day": {"utilization": 68.0, "resets_at": "2026-09-19T09:00:00Z"},
  "seven_day_opus": null,
  "seven_day_sonnet": null,
  "limits": [
    {"kind": "session", "group": "session", "percent": 9, "severity": "normal", "is_active": false},
    {"kind": "weekly_all", "group": "weekly", "percent": 68, "severity": "normal", "is_active": false},
    {"kind": "weekly_scoped", "group": "weekly", "percent": 100, "severity": "critical", "is_active": true,
     "resets_at": "2026-09-19T09:00:00Z", "scope": {"model": {"id": null, "display_name": "Fable"}}},
    {"kind": "monthly_special", "group": "monthly", "percent": 5}
  ],
  "extra_usage": {"is_enabled": true, "monthly_limit": 100, "used_credits": 75, "utilization": 75, "currency": "USD"}
}`

func TestDecodeClaudeUsageMapsLegacyWindows(t *testing.T) {
	usage, err := subscription.DecodeClaudeUsage([]byte(claudeLegacyUsageFixture))
	if err != nil {
		t.Fatalf("DecodeClaudeUsage() = %v", err)
	}
	if usage.Primary == nil || usage.Primary.UsedPercent != 33 || usage.Primary.LimitWindowSeconds == nil || *usage.Primary.LimitWindowSeconds != 5*3600 ||
		usage.Primary.ResetAt == nil || !usage.Primary.ResetAt.Equal(time.Date(2026, 4, 11, 7, 0, 0, 528743000, time.UTC)) {
		t.Fatalf("primary = %#v", usage.Primary)
	}
	if usage.Secondary == nil || usage.Secondary.UsedPercent != 13 || *usage.Secondary.LimitWindowSeconds != 7*24*3600 {
		t.Fatalf("secondary = %#v", usage.Secondary)
	}
	if len(usage.AdditionalRateLimits) != 1 || usage.AdditionalRateLimits[0].LimitName != "Sonnet" ||
		usage.AdditionalRateLimits[0].MeteredFeature != "seven_day_sonnet" ||
		usage.AdditionalRateLimits[0].Primary == nil || usage.AdditionalRateLimits[0].Primary.UsedPercent != 1 {
		t.Fatalf("additional = %#v", usage.AdditionalRateLimits)
	}
	if usage.LimitReached == nil || *usage.LimitReached || usage.Credits != nil || usage.PlanType != "" || usage.RateLimitResetCredits != nil {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestDecodeClaudeUsageReadsLimitsArray(t *testing.T) {
	usage, err := subscription.DecodeClaudeUsage([]byte(claudeLimitsUsageFixture))
	if err != nil {
		t.Fatalf("DecodeClaudeUsage() = %v", err)
	}
	if usage.Primary == nil || usage.Primary.UsedPercent != 9 || usage.Secondary == nil || usage.Secondary.UsedPercent != 68 {
		t.Fatalf("windows = %#v / %#v", usage.Primary, usage.Secondary)
	}
	names := make([]string, 0, len(usage.AdditionalRateLimits))
	for _, limit := range usage.AdditionalRateLimits {
		names = append(names, limit.LimitName)
	}
	if strings.Join(names, ",") != "Fable,Monthly special,Extra usage" {
		t.Fatalf("additional names = %v", names)
	}
	fable := usage.AdditionalRateLimits[0]
	if fable.MeteredFeature != "weekly_scoped" || fable.Primary == nil || fable.Primary.UsedPercent != 100 ||
		fable.Primary.LimitWindowSeconds == nil || *fable.Primary.LimitWindowSeconds != 7*24*3600 ||
		fable.Primary.ResetAt == nil || !fable.Primary.ResetAt.Equal(time.Date(2026, 9, 19, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("scoped limit = %#v primary=%#v", fable, fable.Primary)
	}
	monthly := usage.AdditionalRateLimits[1]
	if monthly.Primary == nil || monthly.Primary.UsedPercent != 5 || *monthly.Primary.LimitWindowSeconds != 30*24*3600 {
		t.Fatalf("unknown kind = %#v primary=%#v", monthly, monthly.Primary)
	}
	extra := usage.AdditionalRateLimits[2]
	if extra.MeteredFeature != "extra_usage" || extra.Primary != nil || extra.Secondary == nil || extra.Secondary.UsedPercent != 75 || extra.Secondary.LimitWindowSeconds != nil {
		t.Fatalf("extra usage = %#v secondary=%#v", extra, extra.Secondary)
	}
	// Only the account-wide windows drive limit_reached; a scoped model cap does not.
	if usage.LimitReached == nil || *usage.LimitReached {
		t.Fatalf("limit_reached = %v", usage.LimitReached)
	}
	encoded, err := json.Marshal(usage)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"USD", "monthly_limit", "used_credits", "severity", "is_active"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("public usage leaked %q: %s", leaked, encoded)
		}
	}
}

func TestDecodeClaudeUsageFillsWindowsFromLimitsWhenFlatKeysMissing(t *testing.T) {
	usage, err := subscription.DecodeClaudeUsage([]byte(`{
		"five_hour": null,
		"seven_day": {"utilization": null, "resets_at": null},
		"limits": [
			{"kind": "session", "group": "session", "percent": 100, "resets_at": "2026-09-22T10:00:00Z"},
			{"kind": "weekly_all", "group": "weekly", "percent": 41.256},
			{"kind": "weekly_scoped", "group": "weekly", "percent": 3, "scope": {"model": {"display_name": "owner@example.com"}}}
		]
	}`))
	if err != nil {
		t.Fatalf("DecodeClaudeUsage() = %v", err)
	}
	if usage.Primary == nil || usage.Primary.UsedPercent != 100 || *usage.Primary.LimitWindowSeconds != 5*3600 || usage.Primary.ResetAt == nil {
		t.Fatalf("primary = %#v", usage.Primary)
	}
	if usage.Secondary == nil || usage.Secondary.UsedPercent != 41.26 || *usage.Secondary.LimitWindowSeconds != 7*24*3600 {
		t.Fatalf("secondary = %#v", usage.Secondary)
	}
	if usage.LimitReached == nil || !*usage.LimitReached {
		t.Fatalf("limit_reached = %v", usage.LimitReached)
	}
	// An e-mail-looking display name falls back to the humanized kind.
	if len(usage.AdditionalRateLimits) != 1 || usage.AdditionalRateLimits[0].LimitName != "Weekly scoped" {
		t.Fatalf("additional = %#v", usage.AdditionalRateLimits)
	}
}

func TestDecodeClaudeUsageScopedLimitsOverrideLegacyKeys(t *testing.T) {
	usage, err := subscription.DecodeClaudeUsage([]byte(`{
		"five_hour": {"utilization": 1},
		"seven_day_sonnet": {"utilization": 30},
		"seven_day_opus": {"utilization": 20},
		"limits": [{"kind": "weekly_scoped", "group": "weekly", "percent": 5, "scope": {"model": {"display_name": "Sonnet"}}}]
	}`))
	if err != nil {
		t.Fatalf("DecodeClaudeUsage() = %v", err)
	}
	if len(usage.AdditionalRateLimits) != 2 || usage.AdditionalRateLimits[0].LimitName != "Sonnet" || usage.AdditionalRateLimits[0].Primary.UsedPercent != 5 ||
		usage.AdditionalRateLimits[0].MeteredFeature != "weekly_scoped" || usage.AdditionalRateLimits[1].LimitName != "Opus" || usage.AdditionalRateLimits[1].Primary.UsedPercent != 20 {
		t.Fatalf("additional = %#v", usage.AdditionalRateLimits)
	}
}

func TestDecodeClaudeUsageRejectsEmptyOrMalformedPayloads(t *testing.T) {
	for _, body := range []string{``, `[]`, `null`, `not json`, `{}`, `{"five_hour":null,"seven_day":null,"limits":"x","extra_usage":{"is_enabled":false}}`} {
		_, err := subscription.DecodeClaudeUsage([]byte(body))
		if !errors.Is(err, subscription.ErrUsageUnavailable) {
			t.Fatalf("DecodeClaudeUsage(%q) err = %v", body, err)
		}
	}
}

func connectClaudeAccount(t *testing.T, upstream *httptest.Server, now time.Time) (*subscription.Manager, contract.ServiceID) {
	t.Helper()
	accounts := subscription.NewMemoryAccountStore()
	credentials := accountauth.NewMemoryCredentialStore()
	manager, err := subscription.NewManager(accounts, credentials,
		accountauth.OAuthConfig{HTTPClient: upstream.Client(), Now: func() time.Time { return now }},
		accountauth.OAuthConfig{Provider: contract.SubscriptionProviderClaudeCode, APIBaseURL: upstream.URL, HTTPClient: upstream.Client(), Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatalf("NewManager() = %v", err)
	}
	expires := now.Add(time.Hour)
	account := contract.SubscriptionAccount{
		ID: "service_claude_01", Provider: contract.SubscriptionProviderClaudeCode,
		Status: contract.SubscriptionStatusConnected, DisplayName: "Claude Code",
		CredentialRef: accountauth.CredentialRefFor("service_claude_01"), TokenExpiresAt: &expires,
		Capabilities: contract.SubscriptionProviderClaudeCode.Capabilities(), CreatedAt: now, UpdatedAt: now,
	}
	if err := credentials.Put(context.Background(), account.ID, accountauth.AccountTokens{
		AccessToken: "claude-access-secret-token", RefreshToken: "claude-refresh-secret-token", AccountID: "account_claude", ExpiresAt: expires,
	}); err != nil {
		t.Fatalf("Put() = %v", err)
	}
	if err := accounts.PutAccount(context.Background(), account); err != nil {
		t.Fatalf("PutAccount() = %v", err)
	}
	return manager, account.ID
}

func TestClaudeUsageSendsClaudeCLIHeadersAndNamesProviderOnFailure(t *testing.T) {
	t.Parallel()
	var status atomic.Int32
	status.Store(http.StatusTooManyRequests)
	var wrongHeaders atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/oauth/usage" || request.Method != http.MethodGet {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer claude-access-secret-token" ||
			!strings.HasPrefix(request.Header.Get("User-Agent"), accountauth.ClaudeUserAgentPrefix) ||
			!strings.Contains(request.Header.Get("Anthropic-Beta"), "oauth-2025-04-20") ||
			request.Header.Get("ChatGPT-Account-ID") != "" || request.URL.RawQuery != "" {
			wrongHeaders.Add(1)
		}
		code := int(status.Load())
		writer.WriteHeader(code)
		if code == http.StatusOK {
			_, _ = writer.Write([]byte(claudeLimitsUsageFixture))
		} else {
			_, _ = writer.Write([]byte(`{"error":{"type":"rate_limit_error","message":"Rate limited"}}`))
		}
	}))
	t.Cleanup(upstream.Close)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	manager, id := connectClaudeAccount(t, upstream, now)

	_, err := manager.Usage(context.Background(), id)
	if !errors.Is(err, subscription.ErrUsageUnavailable) || err.Error() != "claude usage unavailable: status 429" {
		t.Fatalf("Usage() err = %v", err)
	}
	if strings.Contains(err.Error(), "codex") {
		t.Fatalf("Claude failure reported as Codex: %v", err)
	}
	status.Store(http.StatusOK)
	usage, err := manager.Usage(context.Background(), id)
	if err != nil || usage.ServiceID != id || usage.Primary == nil || usage.Primary.UsedPercent != 9 || len(usage.AdditionalRateLimits) != 3 {
		t.Fatalf("Usage() = %#v err=%v", usage, err)
	}
	if wrongHeaders.Load() != 0 {
		t.Fatalf("%d Claude usage requests carried Codex or non-CLI headers", wrongHeaders.Load())
	}
	if _, err := manager.ConsumeReset(context.Background(), id); !errors.Is(err, subscription.ErrResetUnavailable) || err.Error() != "claude usage reset unavailable" {
		t.Fatalf("ConsumeReset() err = %v", err)
	}
}

func TestCodexUsageFailureKeepsCodexLabel(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "forbidden", http.StatusForbidden)
	}))
	t.Cleanup(upstream.Close)
	accounts := subscription.NewMemoryAccountStore()
	credentials := accountauth.NewMemoryCredentialStore()
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	manager, err := subscription.NewManager(accounts, credentials, accountauth.OAuthConfig{
		APIBaseURL: upstream.URL, HTTPClient: upstream.Client(), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewManager() = %v", err)
	}
	expires := now.Add(time.Hour)
	account := contract.SubscriptionAccount{
		ID: "service_codex_03", Provider: contract.SubscriptionProviderOpenAICodex,
		Status: contract.SubscriptionStatusConnected, DisplayName: "Codex",
		CredentialRef: accountauth.CredentialRefFor("service_codex_03"), TokenExpiresAt: &expires,
		Capabilities: contract.DefaultOpenAICodexCapabilities(), CreatedAt: now, UpdatedAt: now,
	}
	if err := credentials.Put(context.Background(), account.ID, accountauth.AccountTokens{
		AccessToken: "codex-access-secret-token", RefreshToken: "codex-refresh-secret-token", AccountID: "acct_12345678", ExpiresAt: expires,
	}); err != nil {
		t.Fatalf("Put() = %v", err)
	}
	if err := accounts.PutAccount(context.Background(), account); err != nil {
		t.Fatalf("PutAccount() = %v", err)
	}
	_, err = manager.Usage(context.Background(), account.ID)
	if !errors.Is(err, subscription.ErrUsageUnavailable) || err.Error() != "codex usage unavailable: status 403" {
		t.Fatalf("Usage() err = %v", err)
	}
}
