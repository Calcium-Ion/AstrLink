package sqlite

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
)

func TestQuotaEstimatesValueWindowsOfTheSignedInAccount(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	if err := store.PutSubscriptionAccount(ctx, planTestAccount(now)); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetService(ctx, "service_plan")
	if err != nil {
		t.Fatal(err)
	}
	service := record.Service
	recordingSince := func(at time.Time) {
		t.Helper()
		if _, err := store.db.ExecContext(ctx, `UPDATE billing_metadata SET value=? WHERE key='recording_since'`, billingTime(at)); err != nil {
			t.Fatal(err)
		}
	}
	recordingSince(now.AddDate(0, 0, -30))
	account := pricing.AccountKey(service)
	for i, call := range []struct {
		at     time.Time
		amount string
	}{
		{now.Add(-26 * time.Hour), "40"}, // last week
		{now.Add(-time.Hour), "12"},      // this week and this 5-hour window
		{now.Add(-5 * time.Minute), "5"}, // after the last read
		{now.AddDate(0, 0, -9), "99"},    // before every window read
	} {
		if _, err := store.db.ExecContext(ctx, `INSERT INTO billing_ledger(root_id,attempt,service_id,account_key,model,started_at,terminal,amount_usd,reason) VALUES(?,1,?,?,'gpt',?,1,?,'priced')`,
			"request_"+string(rune('a'+i)), service.ID, account, billingTime(call.at), call.amount); err != nil {
			t.Fatal(err)
		}
	}
	window := func(used float64, seconds int64, reset time.Time) *contract.RateLimitWindow {
		return &contract.RateLimitWindow{UsedPercent: used, LimitWindowSeconds: &seconds, ResetAt: &reset}
	}
	const fiveHours, week = 5 * 3600, 7 * 24 * 3600
	for _, usage := range []contract.SubscriptionUsage{
		{ServiceID: service.ID, FetchedAt: now.Add(-25 * time.Hour), Secondary: window(40, week, now.Add(-24*time.Hour))},
		{ServiceID: service.ID, FetchedAt: now.Add(-10 * time.Minute), Primary: window(30, fiveHours, now.Add(3*time.Hour)), Secondary: window(10, week, now.AddDate(0, 0, 6))},
	} {
		if err := store.ObserveSubscriptionUsage(ctx, service, usage); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want []pricing.ServiceQuotaEstimate) {
		t.Helper()
		got, err := store.QuotaEstimates(ctx)
		if err != nil || !reflect.DeepEqual(got.Services, want) {
			t.Fatalf("QuotaEstimates() = %+v, %v; want %+v", got.Services, err, want)
		}
	}
	// 5 hours: $12 for 30%. Weekly: $40 + $12 for 40% + 10%.
	check([]pricing.ServiceQuotaEstimate{{ServiceID: service.ID, Windows: []pricing.QuotaWindowEstimate{
		{Kind: "primary", TotalUSD: "40.00"}, {Kind: "secondary", TotalUSD: "104.00"},
	}}})
	// Last week began before recording did, so only this week counts.
	recordingSince(now.Add(-48 * time.Hour))
	check([]pricing.ServiceQuotaEstimate{{ServiceID: service.ID, Windows: []pricing.QuotaWindowEstimate{
		{Kind: "primary", TotalUSD: "40.00"}, {Kind: "secondary", TotalUSD: "120.00"},
	}}})
	// Another provider account signed in: the windows were someone else's.
	other := planTestAccount(now)
	other.ProviderAccountID = "acct_plan_2"
	if err := store.PutSubscriptionAccount(ctx, other); err != nil {
		t.Fatal(err)
	}
	check([]pricing.ServiceQuotaEstimate{})
}
