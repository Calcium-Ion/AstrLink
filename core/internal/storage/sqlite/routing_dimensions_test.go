package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func dimensionRoutingSettings() contract.RoutingSettings {
	settings := contract.DefaultRoutingSettings()
	settings.ModelRoutingGroups = []contract.ModelRoutingGroup{{
		ID: "superclaude", Enabled: true,
		Members: []contract.ModelRoutingMember{
			{ID: "fast", ServiceID: "service_a", Model: "model_a"},
			{ID: "strong", ServiceID: "service_a", Model: "model_b"},
		},
		DefaultMemberID: "fast",
		Rules: []contract.ModelRoutingRule{{
			ID: "billing", Priority: 1,
			When:           contract.ModelRoutingCondition{Field: "topic", In: []string{"billing"}},
			TargetMemberID: "strong",
		}},
	}}
	return settings
}

// A rule naming a custom dimension must survive the store's own write and
// load validation, not only the control API's precheck. The store used to
// validate with no dimensions, so the API accepted the save and the store
// rejected it with an opaque invalid-argument error.
func TestRoutingSettingsWithCustomDimensionRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "dimensions.db"))
	defer store.Close()
	if _, err := store.CreateService(ctx, pathTestService("service_a"), storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}

	// Without the dimension declared the rule is still refused: the store
	// reads the strict, built-in-only field set.
	if err := store.UpdateRoutingSettings(ctx, dimensionRoutingSettings()); !errors.Is(err, storage.ErrInvalidArgument) {
		t.Fatalf("undeclared dimension save err = %v, want ErrInvalidArgument", err)
	}

	classifier := contract.ClassifierSettings{
		ClassifierSource: contract.ClassifierSourceInline,
		Dimensions: []contract.ClassifierDimension{{
			Name:    "topic",
			Options: []contract.ClassifierDimensionOption{{Name: "billing"}, {Name: "support"}},
		}},
	}
	if err := store.UpdateClassifierSettings(ctx, classifier); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRoutingSettings(ctx, dimensionRoutingSettings()); err != nil {
		t.Fatalf("declared dimension save: %v", err)
	}
	loaded, err := store.GetRoutingSettings(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.ModelRoutingGroups) != 1 || loaded.ModelRoutingGroups[0].Rules[0].When.Field != "topic" {
		t.Fatalf("loaded groups = %+v", loaded.ModelRoutingGroups)
	}
	// A service write re-validates the stored routing document inside its
	// transaction; that path must also see the dimension.
	if _, err := store.CreateService(ctx, pathTestService("service_b"), storage.CredentialMutation{}); err != nil {
		t.Fatalf("service write after dimension rule: %v", err)
	}
}

// Dropping a dimension a stored rule still names would make the routing
// document fail its own load validation, and every request reads that
// document. The classifier write is refused instead, inside its transaction.
func TestClassifierSettingsRefusesRemovingDimensionInUse(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "dimension-in-use.db"))
	defer store.Close()
	if _, err := store.CreateService(ctx, pathTestService("service_a"), storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	topic := contract.ClassifierDimension{Name: "topic", Options: []contract.ClassifierDimensionOption{{Name: "billing"}, {Name: "support"}}}
	tone := contract.ClassifierDimension{Name: "tone", Options: []contract.ClassifierDimensionOption{{Name: "formal"}}}
	classifier := contract.ClassifierSettings{ClassifierSource: contract.ClassifierSourceInline, Dimensions: []contract.ClassifierDimension{topic, tone}}
	if err := store.UpdateClassifierSettings(ctx, classifier); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRoutingSettings(ctx, dimensionRoutingSettings()); err != nil {
		t.Fatal(err)
	}

	for name, dimensions := range map[string][]contract.ClassifierDimension{
		"removed": {tone},
		"renamed": {{Name: "subject", Options: topic.Options}, tone},
	} {
		classifier.Dimensions = dimensions
		err := store.UpdateClassifierSettings(ctx, classifier)
		var inUse *contract.ClassifierDimensionInUseError
		if !errors.As(err, &inUse) || inUse.Dimension != "topic" || inUse.GroupID != "superclaude" || !errors.Is(err, storage.ErrInvalidArgument) {
			t.Fatalf("%s: err = %v, want topic in use by superclaude", name, err)
		}
	}
	if _, err := store.GetRoutingSettings(ctx); err != nil {
		t.Fatalf("routing settings after refused writes: %v", err)
	}

	// A dimension no rule names can go, and so can the used one once the rule
	// that names it is gone.
	classifier.Dimensions = []contract.ClassifierDimension{topic}
	if err := store.UpdateClassifierSettings(ctx, classifier); err != nil {
		t.Fatalf("removing an unused dimension: %v", err)
	}
	settings := dimensionRoutingSettings()
	settings.ModelRoutingGroups[0].Rules = []contract.ModelRoutingRule{}
	if err := store.UpdateRoutingSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	classifier.Dimensions = nil
	if err := store.UpdateClassifierSettings(ctx, classifier); err != nil {
		t.Fatalf("removing a dimension no rule names any more: %v", err)
	}
}
