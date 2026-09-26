package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestSubscriptionRiskEventsAppendListCountAndRetain(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "risk.db"))
	defer store.Close()
	for _, id := range []contract.ServiceID{"service_risk", "service_other"} {
		if _, err := store.CreateService(ctx, contract.ServiceFromEndpoint(testEndpoint(id)), storage.CredentialMutation{}); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	for index := range subscriptionRiskEventRetention + 5 {
		code := contract.RiskCodeRateLimit5h
		if index%2 == 0 {
			code = contract.RiskCodeForbidden
		}
		until := base.Add(time.Duration(index)*time.Minute + time.Hour)
		if err := store.AppendSubscriptionRiskEvent(ctx, contract.SubscriptionRiskEvent{
			ServiceID: "service_risk", Kind: contract.SubscriptionRiskEventCooling, Code: code,
			Message: fmt.Sprintf("event %d", index), HTTPStatus: 429,
			ObservedAt: base.Add(time.Duration(index) * time.Minute), PausedUntil: &until,
		}); err != nil {
			t.Fatalf("AppendSubscriptionRiskEvent(%d): %v", index, err)
		}
	}
	if err := store.AppendSubscriptionRiskEvent(ctx, contract.SubscriptionRiskEvent{
		ServiceID: "service_other", Kind: contract.SubscriptionRiskEventSuspended,
		Code: contract.RiskCodeForbidden, ObservedAt: base,
	}); err != nil {
		t.Fatal(err)
	}

	events, err := store.ListSubscriptionRiskEvents(ctx, "service_risk", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != subscriptionRiskEventRetention {
		t.Fatalf("retained %d events, want %d", len(events), subscriptionRiskEventRetention)
	}
	newest := events[0]
	if newest.Message != fmt.Sprintf("event %d", subscriptionRiskEventRetention+4) || newest.ID == 0 ||
		newest.PausedUntil == nil || events[len(events)-1].Message != "event 5" {
		t.Fatalf("events are not newest first after retention: first=%+v last=%+v", newest, events[len(events)-1])
	}
	limited, err := store.ListSubscriptionRiskEvents(ctx, "service_risk", 3)
	if err != nil || len(limited) != 3 || limited[0].ID != newest.ID {
		t.Fatalf("limited list = %+v, %v", limited, err)
	}

	// Events 44..54 were observed at or after minute 44; six of them are 403s.
	since := base.Add(44 * time.Minute)
	count, err := store.CountSubscriptionRiskEvents(ctx, "service_risk", []string{contract.RiskCodeForbidden}, since)
	if err != nil || count != 6 {
		t.Fatalf("CountSubscriptionRiskEvents(forbidden) = %d, %v; want 6", count, err)
	}
	count, err = store.CountSubscriptionRiskEvents(ctx, "service_risk",
		[]string{contract.RiskCodeForbidden, contract.RiskCodeRateLimit5h}, since)
	if err != nil || count != 11 {
		t.Fatalf("CountSubscriptionRiskEvents(all) = %d, %v; want 11", count, err)
	}
	if count, err := store.CountSubscriptionRiskEvents(ctx, "service_risk", nil, since); err != nil || count != 0 {
		t.Fatalf("CountSubscriptionRiskEvents(no codes) = %d, %v", count, err)
	}

	other, err := store.ListSubscriptionRiskEvents(ctx, "service_other", 10)
	if err != nil || len(other) != 1 {
		t.Fatalf("other service events = %+v, %v", other, err)
	}
}

func TestSubscriptionRiskEventsRejectInvalidAndCascadeOnDelete(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "risk-cascade.db"))
	defer store.Close()
	record, err := store.CreateService(ctx, contract.ServiceFromEndpoint(testEndpoint("service_risk")), storage.CredentialMutation{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	err = store.AppendSubscriptionRiskEvent(ctx, contract.SubscriptionRiskEvent{
		ServiceID: "service_risk", Kind: contract.SubscriptionRiskEventSuspended,
		Code: contract.RiskCodeForbidden, Message: "Bearer sk-test-access-token-value-123456", ObservedAt: now,
	})
	if !errors.Is(err, storage.ErrInvalidArgument) {
		t.Fatalf("credential leak append error = %v, want ErrInvalidArgument", err)
	}
	if err := store.AppendSubscriptionRiskEvent(ctx, contract.SubscriptionRiskEvent{
		ServiceID: "service_risk", Kind: contract.SubscriptionRiskEventCleared, ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteService(ctx, "service_risk", record.ETag); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM subscription_risk_events`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("%d risk events survived service deletion", remaining)
	}
}
