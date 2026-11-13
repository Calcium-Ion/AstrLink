package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func planTestAccount(now time.Time) contract.SubscriptionAccount {
	expires := now.Add(time.Hour)
	return contract.SubscriptionAccount{
		ID: "service_plan", DisplayName: "Plan", Provider: contract.SubscriptionProviderOpenAICodex,
		Status: contract.SubscriptionStatusConnected, ProviderAccountID: "acct_plan_1",
		CredentialRef: "keyring://subscription/service_plan", Capabilities: contract.DefaultOpenAICodexCapabilities(),
		TokenExpiresAt: &expires, CreatedAt: now, UpdatedAt: now,
	}
}

func storedPlan(t *testing.T, store *Store) (string, time.Time) {
	t.Helper()
	record, err := store.GetService(context.Background(), "service_plan")
	if err != nil {
		t.Fatal(err)
	}
	return record.Service.Subscription.PlanType, record.Service.UpdatedAt
}

func TestObserveSubscriptionPlanWritesOnlyAChangeForTheSameAccount(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	account := planTestAccount(now)
	if err := store.PutSubscriptionAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	_, updatedAt := storedPlan(t, store)

	for _, step := range []struct {
		name, accountID, plan string
		wrote                 bool
		want                  string
	}{
		{"first plan", "acct_plan_1", " Plus", true, "plus"},
		{"same plan", "acct_plan_1", "plus", false, "plus"},
		{"blank plan", "acct_plan_1", " ", false, "plus"},
		{"another provider account", "acct_plan_2", "pro", false, "plus"},
		{"plan changed", "acct_plan_1", "pro", true, "pro"},
	} {
		wrote, err := store.ObserveSubscriptionPlan(ctx, "service_plan", step.accountID, step.plan)
		if err != nil || wrote != step.wrote {
			t.Fatalf("%s: ObserveSubscriptionPlan() = %v, %v; want %v", step.name, wrote, err, step.wrote)
		}
		plan, at := storedPlan(t, store)
		if plan != step.want || !at.Equal(updatedAt) {
			t.Fatalf("%s: stored plan %q updated %v, want %q updated %v", step.name, plan, at, step.want, updatedAt)
		}
	}
	if _, err := store.ObserveSubscriptionPlan(ctx, "service_plan", "acct_plan_1", strings.Repeat("p", 65)); err == nil {
		t.Fatal("ObserveSubscriptionPlan() accepted a 65-rune plan")
	}

	// A token refresh rewrites the connection and keeps the plan.
	account.PlanType = "pro"
	account.LastRefreshAt = &now
	if err := store.PutSubscriptionAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if plan, _ := storedPlan(t, store); plan != "pro" {
		t.Fatalf("plan after a refresh = %q", plan)
	}
	// A signed-out account takes no plan.
	account.Status, account.CredentialRef, account.ProviderAccountID, account.PlanType = contract.SubscriptionStatusDisconnected, "", "", ""
	account.TokenExpiresAt, account.LastRefreshAt = nil, nil
	if err := store.PutSubscriptionAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if wrote, err := store.ObserveSubscriptionPlan(ctx, "service_plan", "", "plus"); err != nil || wrote {
		t.Fatalf("ObserveSubscriptionPlan() on a signed-out account = %v, %v", wrote, err)
	}
}

func TestSyncCarriesNoSubscriptionPlan(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	if err := store.PutSubscriptionAccount(ctx, planTestAccount(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ObserveSubscriptionPlan(ctx, "service_plan", "acct_plan_1", "plus"); err != nil {
		t.Fatal(err)
	}
	entities, err := store.SyncEntities(ctx, syncKindService)
	if err != nil || len(entities) != 1 {
		t.Fatalf("SyncEntities() = %d entities, %v", len(entities), err)
	}
	exported := entities[0].Document
	if strings.Contains(string(exported), "plan_type") {
		t.Fatalf("exported service carries the plan: %s", exported)
	}
	// The change-log triggers stamp rows with SQLite's clock, not store.now.
	if err := store.ClearChangesThrough(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	// The same configuration coming back is no change, and the plan stays.
	if err := store.SyncWriteEntity(ctx, syncKindService, "service_plan", exported); err != nil {
		t.Fatal(err)
	}
	if count, err := store.PendingChanges(ctx); err != nil || count != 0 {
		t.Fatalf("re-applying the exported service recorded %d changes (%v)", count, err)
	}
	if plan, _ := storedPlan(t, store); plan != "plus" {
		t.Fatalf("plan after an unchanged import = %q", plan)
	}

	// An edit from another device keeps this device's plan for the same
	// provider account, and a plan in the document is never taken.
	var document map[string]any
	if err := json.Unmarshal(exported, &document); err != nil {
		t.Fatal(err)
	}
	document["name"] = "Renamed"
	document["subscription"].(map[string]any)["plan_type"] = "team"
	edited, _ := json.Marshal(document)
	if err := store.SyncWriteEntity(ctx, syncKindService, "service_plan", edited); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetService(ctx, "service_plan")
	if err != nil || record.Service.Name != "Renamed" || record.Service.Subscription.PlanType != "plus" {
		t.Fatalf("after an edit: name %q plan %q (%v)", record.Service.Name, record.Service.Subscription.PlanType, err)
	}

	// A document naming another provider account drops the plan.
	document["subscription"].(map[string]any)["provider_account_id"] = "acct_plan_2"
	moved, _ := json.Marshal(document)
	if err := store.SyncWriteEntity(ctx, syncKindService, "service_plan", moved); err != nil {
		t.Fatal(err)
	}
	if plan, _ := storedPlan(t, store); plan != "" {
		t.Fatalf("plan after another account arrived = %q", plan)
	}
}
