package subscription_test

import (
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func TestDecodeGrokUsagePrefersCreditsPeriod(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	usage, err := subscription.DecodeGrokUsage([]byte(`{
		"config": {
			"creditUsagePercent": 42.567,
			"currentPeriod": {"type": "USAGE_PERIOD_TYPE_WEEKLY", "start": "2026-09-15T00:00:00Z", "end": "2026-09-22T00:00:00Z"},
			"monthlyLimit": {"val": 2000},
			"used": {"val": 850},
			"prepaidBalance": {"val": 1250},
			"history": [{"billingCycle": {"year": 2026, "month": 8}}]
		},
		"subscriptionTier": "SuperGrok Heavy"
	}`), now)
	if err != nil {
		t.Fatalf("DecodeGrokUsage() = %v", err)
	}
	if usage.PlanType != "SuperGrok Heavy" || usage.Primary == nil || usage.Primary.UsedPercent != 42.57 ||
		usage.Primary.LimitWindowSeconds == nil || *usage.Primary.LimitWindowSeconds != 7*24*3600 ||
		usage.Primary.ResetAt == nil || !usage.Primary.ResetAt.Equal(time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)) ||
		usage.LimitReached == nil || *usage.LimitReached || usage.Secondary != nil {
		t.Fatalf("usage = %#v primary=%#v", usage, usage.Primary)
	}
	if usage.Credits == nil || !usage.Credits.HasCredits || usage.Credits.Balance != "$12.50" {
		t.Fatalf("credits = %#v", usage.Credits)
	}
}

func TestDecodeGrokUsageFallsBackToLegacyMonthlyFields(t *testing.T) {
	now := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	usage, err := subscription.DecodeGrokUsage([]byte(`{"config": {
		"monthlyLimit": {"val": 2000}, "used": {"val": 2000},
		"billingPeriodStart": "2026-04-01T00:00:00Z", "billingPeriodEnd": "2026-05-01T00:00:00Z"
	}}`), now)
	if err != nil {
		t.Fatalf("DecodeGrokUsage() = %v", err)
	}
	if usage.Primary == nil || usage.Primary.UsedPercent != 100 || usage.LimitReached == nil || !*usage.LimitReached ||
		usage.Primary.LimitWindowSeconds == nil || *usage.Primary.LimitWindowSeconds != 30*24*3600 || usage.PlanType != "" || usage.Credits != nil {
		t.Fatalf("usage = %#v primary=%#v", usage, usage.Primary)
	}
}

func TestDecodeGrokUsageRejectsEmptyOrPersonalPayloads(t *testing.T) {
	now := time.Now()
	for _, body := range []string{``, `[]`, `{"config":{}}`, `{"config":null}`} {
		if _, err := subscription.DecodeGrokUsage([]byte(body), now); err == nil {
			t.Fatalf("accepted %q", body)
		}
	}
	usage, err := subscription.DecodeGrokUsage([]byte(`{"config":{"creditUsagePercent":5},"subscriptionTier":"owner@example.com"}`), now)
	if err != nil || usage.PlanType != "" || strings.Contains(usage.PlanType, "@") {
		t.Fatalf("usage = %#v err=%v", usage, err)
	}
}
