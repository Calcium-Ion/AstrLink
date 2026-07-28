package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestDefaultPrivacyPolicyMigrationAndETagUpdate(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "astrlink.db")
	store := openTestStore(t, databasePath)
	ctx := context.Background()

	page, err := store.ListPolicies(ctx)
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	if len(page.Items) != 1 || !reflect.DeepEqual(page.Items[0].Policy, contract.DefaultPrivacyPolicy()) {
		t.Fatalf("default page = %#v", page)
	}
	created := page.Items[0]
	if created.ETag == "" {
		t.Fatal("default policy ETag is empty")
	}
	loaded, err := store.GetPolicy(ctx, contract.DefaultPrivacyPolicyID)
	if err != nil || !reflect.DeepEqual(loaded, created) {
		t.Fatalf("GetPolicy = %#v, %v", loaded, err)
	}

	updatedPolicy := loaded.Policy
	updatedPolicy.Enabled = true
	updatedPolicy.Detector = contract.PolicyDetectorOpenAIPrivacyFilter
	modelID := contract.LegacyOpenAIPrivacyFilterInstallationID
	updatedPolicy.LocalModelID = &modelID
	updatedPolicy.RequestAction = contract.PolicyActionBlock
	if _, err := store.UpdatePolicy(ctx, updatedPolicy, `"stale"`); !errors.Is(err, storagecontract.ErrPrecondition) {
		t.Fatalf("stale UpdatePolicy error = %v", err)
	}
	updated, err := store.UpdatePolicy(ctx, updatedPolicy, loaded.ETag)
	if err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}
	if updated.ETag == loaded.ETag || !reflect.DeepEqual(updated.Policy, updatedPolicy) {
		t.Fatalf("updated = %#v", updated)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store = openTestStore(t, databasePath)
	defer store.Close()
	reloaded, err := store.GetPolicy(ctx, contract.DefaultPrivacyPolicyID)
	if err != nil || !reflect.DeepEqual(reloaded, updated) {
		t.Fatalf("policy after restart = %#v, %v", reloaded, err)
	}
}

func TestPolicyStoreRejectsMutableIdentityMissingAndCorruptState(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	record, err := store.GetPolicy(ctx, contract.DefaultPrivacyPolicyID)
	if err != nil {
		t.Fatal(err)
	}

	invalid := record.Policy
	invalid.Match.Models = []string{"gpt-5"}
	if _, err := store.UpdatePolicy(ctx, invalid, record.ETag); !errors.Is(err, storagecontract.ErrInvalidArgument) {
		t.Fatalf("mutable scope error = %v", err)
	}
	invalid = record.Policy
	invalid.ResponseAction = contract.PolicyActionWarn
	if _, err := store.UpdatePolicy(ctx, invalid, record.ETag); !errors.Is(err, storagecontract.ErrInvalidArgument) {
		t.Fatalf("mutable response action error = %v", err)
	}
	if _, err := store.GetPolicy(ctx, "policy_missing"); !errors.Is(err, storagecontract.ErrNotFound) {
		t.Fatalf("missing policy error = %v", err)
	}
	if _, err := store.db.Exec(
		`UPDATE policies SET document_json = json_remove(document_json, '$.min_confidence')
WHERE id = 'policy_privacy_default'`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPolicy(ctx, contract.DefaultPrivacyPolicyID); !errors.Is(err, storagecontract.ErrInvalidRecord) {
		t.Fatalf("missing min_confidence error = %v", err)
	}
	originalDocument, err := json.Marshal(record.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(
		`UPDATE policies SET document_json = ? WHERE id = 'policy_privacy_default'`,
		string(originalDocument),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(
		`UPDATE policies SET document_json = '{"id":"policy_privacy_default","enabled":true,"secret":"must-not-leak"}'
WHERE id = 'policy_privacy_default'`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPolicy(ctx, contract.DefaultPrivacyPolicyID); !errors.Is(err, storagecontract.ErrInvalidRecord) {
		t.Fatalf("corrupt policy error = %v", err)
	}
	if _, err := store.ListPolicies(ctx); !errors.Is(err, storagecontract.ErrInvalidRecord) {
		t.Fatalf("corrupt list error = %v", err)
	}
	if _, err := store.db.Exec(`DELETE FROM policies WHERE id = 'policy_privacy_default'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListPolicies(ctx); !errors.Is(err, storagecontract.ErrInvalidRecord) {
		t.Fatalf("missing singleton list error = %v", err)
	}
}
