package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// routingSettingsDimensions reads the classifier-dimension singleton that a
// routing-settings validation needs. A dimension-aware rule names a
// user-defined classification dimension, and those live in the separate
// classifier-settings document rather than on RoutingSettings, so every store
// path that validates a group has to supply them.
//
// The read is deliberately best-effort and returns nil on any problem: absent
// row, unreadable document, or malformed JSON. nil is the strict reading —
// only built-in routing fields validate — so a failure here degrades to
// refusing a custom-dimension rule, never to admitting an unvalidated one.
func (store *Store) routingSettingsDimensions(ctx context.Context) []contract.ClassifierDimension {
	var document string
	if err := store.db.QueryRowContext(ctx, `SELECT document_json FROM classifier_settings WHERE id = 1`).Scan(&document); err != nil {
		return nil
	}
	var settings contract.ClassifierSettings
	if err := json.Unmarshal([]byte(document), &settings); err != nil {
		return nil
	}
	return settings.Dimensions
}

func (store *Store) GetRoutingSettings(ctx context.Context) (contract.RoutingSettings, error) {
	var document string
	settings := contract.DefaultRoutingSettings()
	if err := store.db.QueryRowContext(ctx, `SELECT document_json FROM routing_settings WHERE id = 1`).Scan(&document); err != nil {
		return settings, err
	}
	// Keys the document lacks keep their defaults: a document saved before the
	// adaptive ranges loads the everyday preset and the custom range low
	// through max.
	if err := json.Unmarshal([]byte(document), &settings); err != nil {
		return settings, fmt.Errorf("%w: routing settings", storage.ErrInvalidRecord)
	}
	// Documents saved before redirects existed, or with an explicit null,
	// load as an empty table so readers never see a nil list.
	if settings.ModelRedirects == nil {
		settings.ModelRedirects = []contract.ModelRedirect{}
	}
	if settings.ModelRoutingGroups == nil {
		settings.ModelRoutingGroups = []contract.ModelRoutingGroup{}
	}
	// Builds made before the reasoning modes got their current names saved
	// passthrough as follow and adaptive as auto. They load under the current
	// names, which the next save writes; a PATCH accepts only the current names.
	switch settings.ReasoningMode {
	case "follow":
		settings.ReasoningMode = contract.ReasoningModePassthrough
	case "auto":
		settings.ReasoningMode = contract.ReasoningModeAdaptive
	}
	if err := settings.ValidateWithDimensions(store.routingSettingsDimensions(ctx)); err != nil {
		return settings, fmt.Errorf("%w: routing settings: %v", storage.ErrInvalidRecord, err)
	}
	return settings, nil
}

func (store *Store) UpdateRoutingSettings(ctx context.Context, settings contract.RoutingSettings) error {
	return store.UpdateRoutingSettingsAtomic(ctx, settings)
}
