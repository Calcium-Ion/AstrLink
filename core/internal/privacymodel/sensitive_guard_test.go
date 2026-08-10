package privacymodel

import (
	"encoding/json"
	"testing"
)

func TestValidateSensitiveGuardViterbiAcceptsSchemaOneAndTwo(t *testing.T) {
	t.Parallel()
	document := sensitiveGuardViterbiFixture(t, 1, nil)
	if err := validateSensitiveGuardViterbi(document); err != nil {
		t.Fatalf("schema 1: %v", err)
	}
	document = sensitiveGuardViterbiFixture(t, 2, map[string]any{
		"confidence_revision": 2,
		"ranking_policy":      "monotonic_platt_without_empirical_floor",
	})
	if err := validateSensitiveGuardViterbi(document); err != nil {
		t.Fatalf("schema 2 with compatibility metadata: %v", err)
	}
	document = sensitiveGuardViterbiFixture(t, 3, nil)
	if err := validateSensitiveGuardViterbi(document); err == nil {
		t.Fatal("schema 3 should be rejected")
	}
}

func sensitiveGuardViterbiFixture(
	t *testing.T,
	schemaVersion int,
	compatibility map[string]any,
) []byte {
	t.Helper()
	zeros := make([]float64, 33)
	thresholds := make(map[string]float64, len(sensitiveGuardSourceLabels))
	calibrators := make(map[string]any, len(sensitiveGuardSourceLabels))
	for _, source := range sensitiveGuardSourceLabels {
		thresholds[source] = 0
		calibrators[source] = map[string]any{
			"slope": 1.0, "intercept": 0.0,
			"empirical_precision_lower_bound": 0.9,
		}
	}
	root := map[string]any{
		"schema_version": schemaVersion,
		"status":         "fitted",
		"decoder":        "bioes-constrained-viterbi",
		"language_decoder_biases": map[string]any{
			"en": map[string]any{"emission_bias": zeros},
			"zh": map[string]any{"emission_bias": zeros},
		},
		"language_operating_points": map[string]any{
			"en": map[string]any{"default_threshold": 0.0, "per_label": thresholds},
			"zh": map[string]any{"default_threshold": 0.0, "per_label": thresholds},
		},
		"span_confidence": map[string]any{"en": calibrators, "zh": calibrators},
	}
	if compatibility != nil {
		root["compatibility"] = compatibility
	}
	document, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return document
}
