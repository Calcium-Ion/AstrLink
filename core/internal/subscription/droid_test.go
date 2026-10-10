package subscription_test

import (
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func TestDecodeDroidUsageMapsTheStandardAndCorePools(t *testing.T) {
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	usage, err := subscription.DecodeDroidUsage([]byte(`{
		"planType": "max",
		"usesTokenRateLimitsBilling": true,
		"extraUsageBalanceCents": 1250,
		"extraUsageAllowed": true,
		"limits": {
			"standard": {
				"fiveHour": {"usedPercent": 42.456, "windowEnd": "2026-10-10T11:30:00Z", "secondsRemaining": 12600},
				"weekly": {"usedPercent": 12, "windowEnd": "2026-10-15T00:00:00Z"},
				"monthly": {"usedPercent": 3, "secondsRemaining": 864000}
			},
			"core": {
				"fiveHour": {"usedPercent": 0, "windowEnd": "2026-10-01T00:00:00Z"},
				"weekly": {"usedPercent": 100, "windowEnd": "2026-10-12T00:00:00Z"},
				"monthly": {"usedPercent": 0}
			}
		}
	}`), now)
	if err != nil {
		t.Fatalf("DecodeDroidUsage() = %v", err)
	}
	usage.ServiceID, usage.FetchedAt = "service_droid", now
	if err := usage.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	if usage.PlanType != "max" || usage.Primary == nil || usage.Primary.UsedPercent != 42.46 ||
		usage.Primary.ResetAt == nil || !usage.Primary.ResetAt.Equal(time.Date(2026, 10, 10, 11, 30, 0, 0, time.UTC)) ||
		usage.Primary.LimitWindowSeconds == nil || *usage.Primary.LimitWindowSeconds != 18000 ||
		usage.LimitReached == nil || *usage.LimitReached {
		t.Fatalf("primary = %#v", usage.Primary)
	}
	if usage.Secondary == nil || usage.Secondary.UsedPercent != 12 || *usage.Secondary.LimitWindowSeconds != 604800 {
		t.Fatalf("secondary = %#v", usage.Secondary)
	}
	if usage.Credits == nil || !usage.Credits.HasCredits || usage.Credits.Balance != "$12.50" {
		t.Fatalf("credits = %#v", usage.Credits)
	}
	// The monthly window falls back to the relative reset; the Core pool keeps
	// only its live weekly window and its expired or windowless buckets are
	// left out.
	names := make([]string, 0, len(usage.AdditionalRateLimits))
	for _, extra := range usage.AdditionalRateLimits {
		names = append(names, extra.LimitName)
	}
	if strings.Join(names, ",") != "Monthly,Droid Core" {
		t.Fatalf("additional = %#v", usage.AdditionalRateLimits)
	}
	monthly := usage.AdditionalRateLimits[0].Primary
	if monthly.ResetAfterSeconds == nil || *monthly.ResetAfterSeconds != 864000 || monthly.ResetAt == nil ||
		!monthly.ResetAt.Equal(now.Add(864000*time.Second)) {
		t.Fatalf("monthly = %#v", monthly)
	}
	core := usage.AdditionalRateLimits[1]
	if core.Primary == nil || core.Primary.UsedPercent != 100 || *core.Primary.LimitWindowSeconds != 604800 || core.Secondary != nil {
		t.Fatalf("core = %#v", core)
	}

	if _, err := subscription.DecodeDroidUsage([]byte(`{"detail":"Unauthorized"}`), now); err == nil {
		t.Fatal("accepted a payload without limits")
	}
	if _, err := subscription.DecodeDroidUsage([]byte(`{"limits":{"standard":{"fiveHour":{"usedPercent":1,"windowEnd":"2026-01-01T00:00:00Z"}}}}`), now); err == nil {
		t.Fatal("accepted a snapshot without a live window")
	}
}

func TestDroidModelsListsTheDocumentedCatalog(t *testing.T) {
	models := subscription.DroidModels()
	seen := map[string]bool{}
	for _, model := range models {
		if seen[model] {
			t.Fatalf("duplicate model %q", model)
		}
		seen[model] = true
	}
	for _, want := range []string{"claude-opus-4-6", "gpt-5.4", "grok-4.7", "glm-5.2", "kimi-k3", "minimax-m3"} {
		if !seen[want] {
			t.Fatalf("catalog lacks %q: %v", want, models)
		}
	}
	for _, unwanted := range []string{"gemini-3.1-pro-preview", "minimax-m2.7", "kimi-k2.5"} {
		if seen[unwanted] {
			t.Fatalf("catalog lists %q", unwanted)
		}
	}
}
