package subscription_test

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/claudecli/fakeclaude"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func unifiedHeaders(values map[string]string) http.Header {
	header := http.Header{}
	for name, value := range values {
		header.Set("Anthropic-Ratelimit-Unified-"+name, value)
	}
	return header
}

func TestDecodeClaudeRateLimitHeaders(t *testing.T) {
	reset := time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)
	usage, ok := subscription.DecodeClaudeRateLimitHeaders(unifiedHeaders(map[string]string{
		"Status":          "allowed_warning",
		"5h-Utilization":  "0.425",
		"5h-Reset":        strconv.FormatInt(reset.Unix(), 10),
		"7d-Utilization":  "1.2",
		"7d_oi-Reset":     "1",
		"Overage-Status":  "rejected",
		"Fallback-Status": "available",
	}))
	if !ok {
		t.Fatal("the headers were not recognized")
	}
	if usage.Primary == nil || usage.Primary.UsedPercent != 42.5 || usage.Primary.ResetAt == nil || !usage.Primary.ResetAt.Equal(reset) ||
		*usage.Primary.LimitWindowSeconds != 5*3600 {
		t.Fatalf("primary = %+v", usage.Primary)
	}
	if usage.Secondary == nil || usage.Secondary.UsedPercent != 120 || usage.Secondary.ResetAt != nil || *usage.Secondary.LimitWindowSeconds != 7*24*3600 {
		t.Fatalf("secondary = %+v", usage.Secondary)
	}
	if usage.Allowed == nil || !*usage.Allowed || usage.LimitReached == nil || *usage.LimitReached {
		t.Fatalf("allowed = %v, limit reached = %v", usage.Allowed, usage.LimitReached)
	}

	usage, ok = subscription.DecodeClaudeRateLimitHeaders(unifiedHeaders(map[string]string{
		"Status": "rejected", "5h-Status": "rejected", "5h-Reset": strconv.FormatInt(reset.Unix(), 10),
		"7d-Utilization": "not a number",
	}))
	if !ok || usage.Primary == nil || usage.Primary.UsedPercent != 100 || usage.Secondary != nil || usage.Allowed == nil || *usage.Allowed {
		t.Fatalf("rejected = %+v, %v", usage, ok)
	}
	if _, ok := subscription.DecodeClaudeRateLimitHeaders(http.Header{"Content-Type": {"application/json"}}); ok {
		t.Fatal("a response without unified headers was recognized")
	}
}

func TestOfficialUsageComesFromResponseHeaders(t *testing.T) {
	const id, signedOut contract.ServiceID = "service_official_01", "service_official_02"
	fixture := newOfficialFixture(t, fakeclaude.Settings{Code: "good-code", Account: officialTestAccount}, id, signedOut)
	ctx := context.Background()
	var persisted []contract.SubscriptionUsage
	fixture.manager.SetUsageObservers(func(_ context.Context, _ contract.SubscriptionAccount, usage contract.SubscriptionUsage) error {
		persisted = append(persisted, usage)
		return nil
	}, nil)
	if final := fixture.login(t, id, "good-code"); final.Status != contract.AuthorizationSessionStatusCompleted {
		t.Fatalf("login = %+v", final)
	}
	if _, err := fixture.manager.Usage(ctx, id); !errors.Is(err, subscription.ErrUsageUnavailable) {
		t.Fatalf("Usage before any model call: %v", err)
	}

	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	session, weekly := now.Add(2*time.Hour), now.Add(72*time.Hour)
	allowed := unifiedHeaders(map[string]string{
		"Status":         "allowed",
		"5h-Utilization": "0.42", "5h-Reset": strconv.FormatInt(session.Unix(), 10),
		"7d-Utilization": "0.1", "7d-Reset": strconv.FormatInt(weekly.Unix(), 10),
	})
	fixture.manager.ObserveOfficialUsage(ctx, id, allowed)
	usage, err := fixture.manager.Usage(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if usage.ServiceID != id || usage.PlanType != "max" || !usage.FetchedAt.Equal(now) ||
		usage.Primary == nil || usage.Primary.UsedPercent != 42 || !usage.Primary.ResetAt.Equal(session) ||
		usage.Secondary == nil || usage.Secondary.UsedPercent != 10 || !usage.Secondary.ResetAt.Equal(weekly) ||
		usage.Allowed == nil || !*usage.Allowed {
		t.Fatalf("usage = %+v", usage)
	}
	if len(persisted) != 1 {
		t.Fatalf("persisted %d snapshots, want 1", len(persisted))
	}
	// Every call repeats the windows; an unchanged snapshot is not written
	// again within the cache period.
	fixture.manager.ObserveOfficialUsage(ctx, id, allowed)
	if len(persisted) != 1 {
		t.Fatalf("an unchanged snapshot was persisted again: %d", len(persisted))
	}

	// A rate-limited call names only the window it hit; the weekly window
	// keeps its last value.
	fixture.manager.ObserveOfficialUsage(ctx, id, unifiedHeaders(map[string]string{
		"Status": "rejected", "5h-Status": "rejected", "5h-Reset": strconv.FormatInt(session.Unix(), 10),
	}))
	usage, err = fixture.manager.Usage(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Primary == nil || usage.Primary.UsedPercent != 100 || usage.Secondary == nil || usage.Secondary.UsedPercent != 10 ||
		usage.LimitReached == nil || !*usage.LimitReached {
		t.Fatalf("rate-limited usage = %+v", usage)
	}
	if len(persisted) != 2 {
		t.Fatalf("persisted %d snapshots, want 2", len(persisted))
	}
	// ForgetUsage (refresh) has nothing to read an official account's usage
	// with, so the last snapshot stays.
	fixture.manager.ForgetUsage(id)
	if again, err := fixture.manager.Usage(ctx, id); err != nil || again.Primary.UsedPercent != 100 {
		t.Fatalf("Usage after refresh = %+v, %v", again, err)
	}

	fixture.manager.ObserveOfficialUsage(ctx, signedOut, allowed)
	if _, err := fixture.manager.Usage(ctx, signedOut); !errors.Is(err, subscription.ErrUsageUnavailable) {
		t.Fatalf("a signed-out account took usage: %v", err)
	}
	if _, err := fixture.manager.Logout(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, ok := fixture.manager.CachedUsage(id); ok {
		t.Fatal("signing out kept the usage snapshot")
	}
}
