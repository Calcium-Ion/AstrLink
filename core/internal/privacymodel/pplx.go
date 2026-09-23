package privacymodel

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/QuantumNous/astrlink/core/contract"
)

var pplxEntityLabels = [...]string{
	"private_person", "private_email", "private_phone", "private_address",
	"private_url", "private_date", "account_number", "secret", "other_pii",
}

func parsePrivacyModelConfig(document []byte) (hfModelConfig, contract.PrivacyModelAdapter, error) {
	var config hfModelConfig
	if err := json.Unmarshal(document, &config); err != nil {
		return config, "", ErrUnsupportedModel
	}
	adapter := contract.PrivacyModelAdapterHFToken
	if config.ModelType == "pii_masking" {
		labels, err := pplxLabels(document)
		if err != nil {
			return config, "", err
		}
		config.ID2Label = labels
		adapter = contract.PrivacyModelAdapterPPLXBIOES
	}
	return config, adapter, nil
}

// PII-Tracer keeps its fixed 37-label taxonomy in the model implementation,
// not config.id2label. Validate the config before deriving those labels.
func pplxLabels(document []byte) (map[string]string, error) {
	var config struct {
		ModelType      string   `json:"model_type"`
		Architectures  []string `json:"architectures"`
		NumTokenLabels int      `json:"num_token_labels"`
		MaxSeqLen      int      `json:"max_seq_len"`
		BBias          *float64 `json:"viterbi_b_bias"`
		EBias          *float64 `json:"viterbi_e_bias"`
		Backbone       struct {
			Bidirectional bool  `json:"use_bidirectional_attention"`
			IsCausal      *bool `json:"is_causal"`
		} `json:"backbone"`
	}
	if json.Unmarshal(document, &config) != nil || config.ModelType != "pii_masking" ||
		len(config.Architectures) != 1 || config.Architectures[0] != "PiiMaskingModel" ||
		config.NumTokenLabels != 37 || config.MaxSeqLen != 4096 ||
		config.BBias == nil || config.EBias == nil ||
		math.IsNaN(*config.BBias) || math.IsInf(*config.BBias, 0) ||
		math.IsNaN(*config.EBias) || math.IsInf(*config.EBias, 0) ||
		math.Abs(*config.BBias) > math.MaxFloat32 || math.Abs(*config.EBias) > math.MaxFloat32 ||
		(!config.Backbone.Bidirectional && (config.Backbone.IsCausal == nil || *config.Backbone.IsCausal)) ||
		(config.Backbone.IsCausal != nil && *config.Backbone.IsCausal) {
		return nil, ErrUnsupportedModel
	}
	labels := map[string]string{"0": "O"}
	for _, entity := range pplxEntityLabels {
		for _, prefix := range []string{"B", "I", "E", "S"} {
			labels[strconv.Itoa(len(labels))] = fmt.Sprintf("%s-%s", prefix, entity)
		}
	}
	return labels, nil
}

func defaultPPLXLabelMapping() map[string]*contract.CanonicalKind {
	mapping := defaultOpenAILabelMapping()
	// This mixed category has no exact gateway equivalent. Ignore it by default
	// instead of treating every hit as a secret; users can override the mapping.
	mapping["other_pii"] = nil
	return mapping
}

func probeModelLabels(
	id2label map[string]string,
	adapter contract.PrivacyModelAdapter,
) ([]contract.PrivacyModelLabel, string, bool, bool) {
	labels, scheme, complete, valid := probeLabels(id2label)
	if !valid || adapter != contract.PrivacyModelAdapterPPLXBIOES {
		return labels, scheme, complete, valid
	}
	// Reuse installation defaults, including an explicit ignore, so probing
	// does not turn a fully configured model into an unresolved mapping form.
	defaults := defaultPPLXLabelMapping()
	complete = true
	for index := range labels {
		label := &labels[index]
		if kind, exists := defaults[label.Label]; exists {
			label.SuggestedKind = kind
			label.SuggestedIgnore = kind == nil
		}
		if label.SuggestedKind == nil && !label.SuggestedIgnore {
			complete = false
		}
	}
	return labels, scheme, complete, true
}

func decoratePPLXPlans(variants []contract.PrivacyModelVariant, plans map[string]customVariantPlan) {
	for index := range variants {
		variant := &variants[index]
		plan := plans[variant.ID]
		plan.runtime.window = 4096
		if variant.Quantization == "int4" {
			// Bound quadratic attention memory for the compact CPU variant.
			// Overlap and global decoding still cover the entire request.
			plan.runtime.window = 1024
			variant.EstimatedRAMBytes = max(variant.EstimatedRAMBytes, 2_147_483_648)
			plan.variant = *variant
		}
		plan.runtime.tagScheme = "bioes"
		plans[variant.ID] = plan
	}
}
