package subscription

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const officialUsageFixture = `{
  "user_id": "user_REDACTED",
  "account_id": "acct_secret_should_not_leak",
  "email": "redacted@example.com",
  "plan_type": "plus",
  "rate_limit": {
    "allowed": true,
    "limit_reached": false,
    "primary_window": {
      "used_percent": 34,
      "limit_window_seconds": 18000,
      "reset_after_seconds": 5865,
      "reset_at": 1778091218
    },
    "secondary_window": {
      "used_percent": 37,
      "limit_window_seconds": 604800,
      "reset_after_seconds": 520217,
      "reset_at": 1778605571
    }
  },
  "additional_rate_limits": [
    {
      "limit_name": "GPT-5.3-Codex-Spark",
      "metered_feature": "codex_bengalfox",
      "rate_limit": {
        "allowed": true,
        "limit_reached": false,
        "primary_window": {
          "used_percent": 0,
          "limit_window_seconds": 18000,
          "reset_after_seconds": 18000,
          "reset_at": 1778103354
        },
        "secondary_window": {
          "used_percent": 0,
          "limit_window_seconds": 604800,
          "reset_after_seconds": 519837,
          "reset_at": 1778605191
        }
      }
    }
  ],
  "credits": {
    "has_credits": false,
    "unlimited": false,
    "overage_limit_reached": false,
    "balance": "0"
  },
  "rate_limit_reset_credits": { "available_count": 2 },
  "rate_limit_reached_type": null
}`

func TestDecodeCodexUsageStripsPIIAndKeepsWindows(t *testing.T) {
	usage, err := DecodeCodexUsage([]byte(officialUsageFixture))
	if err != nil {
		t.Fatalf("DecodeCodexUsage() = %v", err)
	}
	if usage.PlanType != "plus" || usage.Primary == nil || usage.Secondary == nil {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.Primary.UsedPercent != 34 || usage.Primary.LimitWindowSeconds == nil ||
		*usage.Primary.LimitWindowSeconds != 18000 || usage.Primary.ResetAt == nil {
		t.Fatalf("primary = %#v", usage.Primary)
	}
	if !usage.Primary.ResetAt.Equal(time.Unix(1778091218, 0).UTC()) {
		t.Fatalf("primary reset_at = %s", usage.Primary.ResetAt)
	}
	if usage.Credits == nil || usage.Credits.Balance != "0" {
		t.Fatalf("credits = %#v", usage.Credits)
	}
	if usage.RateLimitResetCredits == nil || usage.RateLimitResetCredits.AvailableCount != 2 {
		t.Fatalf("reset credits = %#v", usage.RateLimitResetCredits)
	}
	if len(usage.AdditionalRateLimits) != 1 || usage.AdditionalRateLimits[0].LimitName != "GPT-5.3-Codex-Spark" {
		t.Fatalf("additional = %#v", usage.AdditionalRateLimits)
	}

	encoded, err := json.Marshal(usage)
	if err != nil {
		t.Fatal(err)
	}
	leaked := []string{"user_REDACTED", "acct_secret_should_not_leak", "redacted@example.com", "email", "user_id", "account_id"}
	body := string(encoded)
	for _, secret := range leaked {
		if strings.Contains(body, secret) {
			t.Fatalf("public usage leaked %q: %s", secret, body)
		}
	}
}

func TestDecodeCodexUsageOmitsDisabledAndEmptyWindows(t *testing.T) {
	usage, err := DecodeCodexUsage([]byte(`{
		"plan_type": "pro",
		"rate_limit": {
			"primary_window": {"used_percent": 12, "limit_window_seconds": 0},
			"secondary_window": null
		},
		"additional_rate_limits": [
			{"limit_name": "empty", "rate_limit": {}},
			{"limit_name": "codex_other", "metered_feature": "codex_other", "rate_limit": {"primary_window": {"used_percent": 8.5, "limit_window_seconds": 900}}}
		]
	}`))
	if err != nil {
		t.Fatalf("DecodeCodexUsage() = %v", err)
	}
	if usage.Primary != nil || usage.Secondary != nil {
		t.Fatalf("disabled windows leaked: %#v", usage)
	}
	if len(usage.AdditionalRateLimits) != 1 || usage.AdditionalRateLimits[0].LimitName != "codex_other" {
		t.Fatalf("additional = %#v", usage.AdditionalRateLimits)
	}
	if usage.AdditionalRateLimits[0].Primary == nil || usage.AdditionalRateLimits[0].Primary.UsedPercent != 8.5 {
		t.Fatalf("additional primary = %#v", usage.AdditionalRateLimits[0].Primary)
	}
}

func TestDecodeCodexConsumeResetMapsOfficialCodes(t *testing.T) {
	result, err := DecodeCodexConsumeReset([]byte(`{
		"code": "reset",
		"credit": {"id": "RateLimitResetCredit_should_not_leak"},
		"windows_reset": 2
	}`))
	if err != nil {
		t.Fatalf("DecodeCodexConsumeReset() = %v", err)
	}
	if result.Outcome != "reset" || result.WindowsReset == nil || *result.WindowsReset != 2 {
		t.Fatalf("result = %#v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "RateLimitResetCredit") || strings.Contains(string(encoded), "credit") {
		t.Fatalf("public reset leaked credit: %s", encoded)
	}

	_, err = DecodeCodexConsumeReset([]byte(`{"code":"no_credit"}`))
	if err != ErrNoResetCredit {
		t.Fatalf("no_credit err = %v", err)
	}
	_, err = DecodeCodexConsumeReset([]byte(`{"code":"nothing_to_reset"}`))
	if err != ErrNothingToReset {
		t.Fatalf("nothing_to_reset err = %v", err)
	}
	if _, err := DecodeCodexConsumeReset([]byte(`{"code":"unknown"}`)); err == nil {
		t.Fatal("accepted unknown consume code")
	}
}

func TestDecodeCodexUsageRejectsMalformedPayloads(t *testing.T) {
	for _, body := range []string{"", "[]", "not json", "null"} {
		if _, err := DecodeCodexUsage([]byte(body)); err == nil {
			t.Fatalf("DecodeCodexUsage(%q) succeeded", body)
		}
	}
}
