package contract_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestSubscriptionRiskValidate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	until := now.Add(time.Hour)
	tests := []struct {
		name  string
		risk  contract.SubscriptionRisk
		valid bool
	}{
		{"suspended", contract.SubscriptionRisk{State: contract.SubscriptionRiskSuspended, Code: "organization_disabled", ObservedAt: now}, true},
		{"cooling", contract.SubscriptionRisk{State: contract.SubscriptionRiskCooling, Code: "rate_limit_5h", ObservedAt: now, PausedUntil: &until, HTTPStatus: 429}, true},
		{"cooling without deadline", contract.SubscriptionRisk{State: contract.SubscriptionRiskCooling, Code: "rate_limit_5h", ObservedAt: now}, false},
		{"suspended with deadline", contract.SubscriptionRisk{State: contract.SubscriptionRiskSuspended, Code: "forbidden", ObservedAt: now, PausedUntil: &until}, false},
		{"unknown state", contract.SubscriptionRisk{State: "banned", Code: "forbidden", ObservedAt: now}, false},
		{"invalid code", contract.SubscriptionRisk{State: contract.SubscriptionRiskSuspended, Code: "Forbidden!", ObservedAt: now}, false},
		{"missing observed_at", contract.SubscriptionRisk{State: contract.SubscriptionRiskSuspended, Code: "forbidden"}, false},
		{"invalid http status", contract.SubscriptionRisk{State: contract.SubscriptionRiskSuspended, Code: "forbidden", ObservedAt: now, HTTPStatus: 42}, false},
		{"long message", contract.SubscriptionRisk{State: contract.SubscriptionRiskSuspended, Code: "forbidden", ObservedAt: now, Message: strings.Repeat("x", 241)}, false},
		{"credential leak", contract.SubscriptionRisk{State: contract.SubscriptionRiskSuspended, Code: "forbidden", ObservedAt: now, Message: "Bearer sk-test-access-token-value-123456"}, false},
	}
	for _, test := range tests {
		err := test.risk.Validate()
		if test.valid && err != nil {
			t.Errorf("%s: Validate() unexpected error: %v", test.name, err)
		}
		if !test.valid && err == nil {
			t.Errorf("%s: Validate() accepted invalid risk", test.name)
		}
	}
}

func TestSubscriptionRiskBlocksUntilPauseEnds(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	until := now.Add(time.Minute)
	var missing *contract.SubscriptionRisk
	suspended := &contract.SubscriptionRisk{State: contract.SubscriptionRiskSuspended}
	cooling := &contract.SubscriptionRisk{State: contract.SubscriptionRiskCooling, PausedUntil: &until}
	if missing.Blocks(now) || missing.Expired(now) {
		t.Fatal("nil risk must not block or expire")
	}
	if !suspended.Blocks(now.Add(365*24*time.Hour)) || suspended.Expired(now.Add(365*24*time.Hour)) {
		t.Fatal("suspended risk must block until cleared")
	}
	if !cooling.Blocks(now) || cooling.Expired(now) {
		t.Fatal("cooling risk must block before paused_until")
	}
	if cooling.Blocks(until) || !cooling.Expired(until) {
		t.Fatal("cooling risk must expire at paused_until")
	}
}

func TestSubscriptionRiskRoundTripsThroughServiceJSON(t *testing.T) {
	t.Parallel()
	account := validSubscriptionAccount()
	until := account.UpdatedAt.Add(5 * time.Hour)
	account.Risk = &contract.SubscriptionRisk{
		State: contract.SubscriptionRiskCooling, Code: contract.RiskCodeUsageLimitReached,
		Message: "The usage limit has been reached", HTTPStatus: 429,
		ObservedAt: account.UpdatedAt, PausedUntil: &until, Occurrences: 2,
	}
	service := contract.ServiceFromSubscriptionAccount(account)
	encoded, err := json.Marshal(service)
	if err != nil {
		t.Fatalf("Marshal(service): %v", err)
	}
	if !strings.Contains(string(encoded), `"risk":{"state":"cooling","code":"usage_limit_reached"`) {
		t.Fatalf("service JSON lacks subscription.risk: %s", encoded)
	}
	var decoded contract.Service
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal(service): %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("decoded service Validate(): %v", err)
	}
	view, err := decoded.SubscriptionAccountView()
	if err != nil || view.Risk == nil {
		t.Fatalf("SubscriptionAccountView() = %+v, %v; want risk", view, err)
	}
	if got := *view.Risk; got.Code != account.Risk.Code || !got.PausedUntil.Equal(until) || got.Occurrences != 2 {
		t.Fatalf("risk round trip = %+v, want %+v", got, *account.Risk)
	}

	decoded.Subscription.Risk.PausedUntil = nil
	if err := decoded.Validate(); err == nil {
		t.Fatal("service Validate() accepted a cooling risk without paused_until")
	}
}

func TestSubscriptionRiskEventValidate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	cleared := contract.SubscriptionRiskEvent{ServiceID: "service_01", Kind: contract.SubscriptionRiskEventCleared, ObservedAt: now}
	if err := cleared.Validate(); err != nil {
		t.Fatalf("cleared event Validate(): %v", err)
	}
	suspended := contract.SubscriptionRiskEvent{ServiceID: "service_01", Kind: contract.SubscriptionRiskEventSuspended, ObservedAt: now}
	if err := suspended.Validate(); err == nil {
		t.Fatal("suspended event without code was accepted")
	}
	suspended.Code = contract.RiskCodeOrganizationDisabled
	if err := suspended.Validate(); err != nil {
		t.Fatalf("suspended event Validate(): %v", err)
	}
	suspended.Kind = "forbidden"
	if err := suspended.Validate(); err == nil {
		t.Fatal("unknown event kind was accepted")
	}
}

func TestSanitizeSubscriptionRiskMessage(t *testing.T) {
	t.Parallel()
	if got := contract.SanitizeSubscriptionRiskMessage("  This organization\n has been   disabled. "); got != "This organization has been disabled." {
		t.Fatalf("sanitize whitespace = %q", got)
	}
	if got := contract.SanitizeSubscriptionRiskMessage("rejected Bearer sk-test-access-token-value-123456"); got != "" {
		t.Fatalf("sanitize credential = %q, want empty", got)
	}
	long := contract.SanitizeSubscriptionRiskMessage(strings.Repeat("a", 500))
	risk := contract.SubscriptionRisk{State: contract.SubscriptionRiskSuspended, Code: "forbidden", ObservedAt: time.Now(), Message: long}
	if err := risk.Validate(); err != nil {
		t.Fatalf("sanitized long message is invalid: %v", err)
	}
}
