package privacymodel

import (
	"encoding/json"
	"math"
	"strings"
)

const (
	sensitiveGuardViterbiCalibrationPath = "viterbi_calibration.json"
	sensitiveGuardSecretRulesPath        = "secret_rules.yaml"
	sensitiveGuardSecretCalibrationPath  = "secret_calibration.json"
	maxSensitiveGuardRules               = 64
	maxSensitiveGuardRulePatternBytes    = 16 << 10
)

var sensitiveGuardSourceLabels = []string{
	"account_number",
	"private_address",
	"private_date",
	"private_email",
	"private_person",
	"private_phone",
	"private_url",
	"secret",
}

func validateSensitiveGuardAssets(
	id2label map[string]string,
	viterbiDocument []byte,
	rulesDocument []byte,
	secretCalibrationDocument []byte,
) error {
	if !validOpenAILabelOrder(id2label) ||
		validateSensitiveGuardViterbi(viterbiDocument) != nil ||
		validateSensitiveGuardRules(rulesDocument) != nil ||
		validateSensitiveGuardSecretCalibration(
			secretCalibrationDocument,
			sha256Hex(rulesDocument),
		) != nil {
		return errModelIncompatible
	}
	return nil
}

func validateSensitiveGuardViterbi(document []byte) error {
	var root map[string]json.RawMessage
	if json.Unmarshal(document, &root) != nil || root == nil ||
		!validSensitiveGuardViterbiSchema(jsonInteger(root["schema_version"])) ||
		jsonString(root["status"]) != "fitted" ||
		jsonString(root["decoder"]) != "bioes-constrained-viterbi" {
		return errModelIncompatible
	}
	var decoderBiases map[string]json.RawMessage
	var operatingPoints map[string]json.RawMessage
	var spanConfidence map[string]json.RawMessage
	if json.Unmarshal(root["language_decoder_biases"], &decoderBiases) != nil ||
		json.Unmarshal(root["language_operating_points"], &operatingPoints) != nil ||
		json.Unmarshal(root["span_confidence"], &spanConfidence) != nil ||
		!exactLanguageKeys(decoderBiases) ||
		!exactLanguageKeys(operatingPoints) ||
		!exactLanguageKeys(spanConfidence) {
		return errModelIncompatible
	}
	for _, language := range []string{"en", "zh"} {
		var biasObject map[string]json.RawMessage
		var biases []float64
		if json.Unmarshal(decoderBiases[language], &biasObject) != nil ||
			json.Unmarshal(biasObject["emission_bias"], &biases) != nil ||
			len(biases) != 33 || !finiteNumbers(biases) {
			return errModelIncompatible
		}

		var point map[string]json.RawMessage
		var thresholds map[string]float64
		if json.Unmarshal(operatingPoints[language], &point) != nil ||
			!jsonProbability(point["default_threshold"]) ||
			json.Unmarshal(point["per_label"], &thresholds) != nil ||
			!exactSourceNumberKeys(thresholds, true) {
			return errModelIncompatible
		}

		var calibrators map[string]json.RawMessage
		if json.Unmarshal(spanConfidence[language], &calibrators) != nil ||
			!exactSourceRawKeys(calibrators) {
			return errModelIncompatible
		}
		for _, label := range sensitiveGuardSourceLabels {
			var calibrator map[string]json.RawMessage
			if json.Unmarshal(calibrators[label], &calibrator) != nil ||
				!jsonFinite(calibrator["slope"]) ||
				!jsonFinite(calibrator["intercept"]) ||
				!jsonProbability(calibrator["empirical_precision_lower_bound"]) {
				return errModelIncompatible
			}
		}
	}
	return nil
}

func validateSensitiveGuardRules(document []byte) error {
	var root map[string]json.RawMessage
	if json.Unmarshal(document, &root) != nil || root == nil ||
		jsonInteger(root["schema_version"]) != 2 ||
		jsonString(root["format"]) != "JSON-compatible YAML" {
		return errModelIncompatible
	}
	var rules []map[string]json.RawMessage
	if json.Unmarshal(root["rules"], &rules) != nil ||
		len(rules) == 0 || len(rules) > maxSensitiveGuardRules {
		return errModelIncompatible
	}
	knownValidators := map[string]struct{}{
		"": {}, "basic_auth": {}, "bearer": {}, "bip39": {},
		"cloud_pair": {}, "database_dsn": {}, "database_uri": {},
		"encryption_key": {}, "jwt": {}, "key_length": {}, "pem": {},
		"recovery_code": {}, "signed_url": {}, "totp_base32": {},
		"wallet_key": {},
	}
	seen := make(map[string]struct{}, len(rules))
	required := map[string]bool{
		"password.assignment":    false,
		"generic.keyword_secret": false,
	}
	for _, rule := range rules {
		id := jsonString(rule["rule_id"])
		pattern := jsonString(rule["regex"])
		label := jsonString(rule["label"])
		if label == "" {
			label = "secret"
		}
		validator := jsonString(rule["validator"])
		if id == "" || len(id) > 128 || strings.TrimSpace(id) != id ||
			pattern == "" || len(pattern) > maxSensitiveGuardRulePatternBytes ||
			label != "secret" {
			return errModelIncompatible
		}
		if _, duplicate := seen[id]; duplicate {
			return errModelIncompatible
		}
		if _, known := knownValidators[validator]; !known {
			return errModelIncompatible
		}
		seen[id] = struct{}{}
		if _, tracked := required[id]; tracked {
			required[id] = true
		}
		if capture, exists := rule["capture_group"]; exists {
			value := jsonInteger(capture)
			if value < 0 || value > 16 {
				return errModelIncompatible
			}
		}
		if minimum, exists := rule["min_length"]; exists {
			value := jsonInteger(minimum)
			if value < 0 || value > 16_384 {
				return errModelIncompatible
			}
		}
		if entropy, exists := rule["entropy"]; exists && string(entropy) != "null" &&
			(!jsonFinite(entropy) || jsonNumber(entropy) < 0 || jsonNumber(entropy) > 8) {
			return errModelIncompatible
		}
		for _, field := range []string{"keywords", "paired_fields", "allowlist"} {
			if value, exists := rule[field]; exists && !validStringArray(value, 64, 256) {
				return errModelIncompatible
			}
		}
	}
	for _, present := range required {
		if !present {
			return errModelIncompatible
		}
	}
	return nil
}

func validateSensitiveGuardSecretCalibration(document []byte, rulesSHA256 string) error {
	var root map[string]json.RawMessage
	if json.Unmarshal(document, &root) != nil || root == nil ||
		jsonInteger(root["schema_version"]) != 2 ||
		jsonString(root["status"]) != "fitted" ||
		jsonString(root["mode"]) != "rules" ||
		!jsonProbability(root["minimum_confidence"]) {
		return errModelIncompatible
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(root["fit_metadata"], &metadata) != nil ||
		jsonString(metadata["rules_sha256"]) != rulesSHA256 {
		return errModelIncompatible
	}
	var platt map[string]json.RawMessage
	if json.Unmarshal(root["platt"], &platt) != nil ||
		!jsonFinite(platt["slope"]) || !jsonFinite(platt["intercept"]) {
		return errModelIncompatible
	}
	var score map[string]json.RawMessage
	if json.Unmarshal(root["score_model"], &score) != nil ||
		jsonString(score["link"]) != "linear_clip" ||
		!jsonFinite(score["intercept"]) ||
		!jsonFinite(score["entropy_offset"]) ||
		!jsonFinite(score["entropy_cap"]) {
		return errModelIncompatible
	}
	var clip []float64
	if json.Unmarshal(score["clip"], &clip) != nil || len(clip) != 2 ||
		!finiteNumbers(clip) || clip[0] < 0 || clip[1] > 1 || clip[0] >= clip[1] {
		return errModelIncompatible
	}
	var weights map[string]float64
	if json.Unmarshal(score["weights"], &weights) != nil {
		return errModelIncompatible
	}
	expectedWeights := []string{
		"context", "encoded", "entropy_excess", "explicit_assignment",
		"model_score", "negative", "pair", "pattern",
		"structurally_invalid", "structurally_valid",
	}
	if len(weights) != len(expectedWeights) {
		return errModelIncompatible
	}
	for _, name := range expectedWeights {
		value, exists := weights[name]
		if !exists || math.IsNaN(value) || math.IsInf(value, 0) {
			return errModelIncompatible
		}
	}
	return nil
}

func exactLanguageKeys(values map[string]json.RawMessage) bool {
	if len(values) != 2 {
		return false
	}
	_, en := values["en"]
	_, zh := values["zh"]
	return en && zh
}

func exactSourceRawKeys(values map[string]json.RawMessage) bool {
	if len(values) != len(sensitiveGuardSourceLabels) {
		return false
	}
	for _, label := range sensitiveGuardSourceLabels {
		if _, exists := values[label]; !exists {
			return false
		}
	}
	return true
}

func exactSourceNumberKeys(values map[string]float64, probability bool) bool {
	if len(values) != len(sensitiveGuardSourceLabels) {
		return false
	}
	for _, label := range sensitiveGuardSourceLabels {
		value, exists := values[label]
		if !exists || math.IsNaN(value) || math.IsInf(value, 0) ||
			(probability && (value < 0 || value > 1)) {
			return false
		}
	}
	return true
}

func validSensitiveGuardViterbiSchema(version int) bool {
	// Schema 1 and 2 share the numeric fields AstrLink consumes today.
	// Schema 2 may carry compatibility metadata that Core ignores.
	return version == 1 || version == 2
}

func validStringArray(document json.RawMessage, maximum, maxBytes int) bool {
	var values []string
	if json.Unmarshal(document, &values) != nil || len(values) > maximum {
		return false
	}
	for _, value := range values {
		if value == "" || len(value) > maxBytes || strings.TrimSpace(value) != value {
			return false
		}
	}
	return true
}

func finiteNumbers(values []float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > math.MaxFloat32 {
			return false
		}
	}
	return true
}

func jsonString(document json.RawMessage) string {
	var value string
	_ = json.Unmarshal(document, &value)
	return value
}

func jsonInteger(document json.RawMessage) int {
	var value int
	if json.Unmarshal(document, &value) != nil {
		return -1
	}
	return value
}

func jsonNumber(document json.RawMessage) float64 {
	var value float64
	if json.Unmarshal(document, &value) != nil {
		return math.NaN()
	}
	return value
}

func jsonFinite(document json.RawMessage) bool {
	value := jsonNumber(document)
	return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Abs(value) <= math.MaxFloat32
}

func jsonProbability(document json.RawMessage) bool {
	value := jsonNumber(document)
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}
