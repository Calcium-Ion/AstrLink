package contract

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestDefaultPrivacyPolicyIsFrozenAndValid(t *testing.T) {
	policy := DefaultPrivacyPolicy()
	if err := ValidatePrivacyDefault(policy); err != nil {
		t.Fatalf("ValidatePrivacyDefault: %v", err)
	}
	if policy.ID != "policy_privacy_default" || policy.Name != "隐私保护" ||
		policy.Enabled || policy.Priority != 0 || policy.Detector != PolicyDetectorRegex ||
		policy.MinConfidence != DefaultPrivacyMinConfidence ||
		policy.RequestAction != PolicyActionRedact || policy.ResponseAction != PolicyActionAllow ||
		!policy.ResponseRestore {
		t.Fatalf("default privacy policy drifted: %#v", policy)
	}
	if len(policy.Match.Protocols) != 0 || len(policy.Match.Models) != 0 || len(policy.Match.ServiceIDs) != 0 {
		t.Fatalf("default match is not global: %#v", policy.Match)
	}
	document, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(document), `"match":{}`) ||
		!strings.Contains(string(document), `"detector":"regex"`) ||
		!strings.Contains(string(document), `"regex_source":"builtin"`) ||
		!strings.Contains(string(document), `"custom_regex_rules":[]`) ||
		!strings.Contains(string(document), `"min_confidence":0.8`) ||
		!strings.Contains(string(document), `"request_action":"redact"`) ||
		!strings.Contains(string(document), `"response_restore":true`) {
		t.Fatalf("default policy wire shape = %s", document)
	}
}

func TestPolicyMinConfidenceValidation(t *testing.T) {
	for _, value := range []float64{0, 0.8, 1} {
		policy := DefaultPrivacyPolicy()
		policy.MinConfidence = value
		if err := policy.Validate(); err != nil {
			t.Fatalf("min_confidence %v rejected: %v", value, err)
		}
	}
	for _, value := range []float64{-0.01, 1.01, math.NaN(), math.Inf(1)} {
		policy := DefaultPrivacyPolicy()
		policy.MinConfidence = value
		if err := policy.Validate(); err == nil ||
			!strings.Contains(err.Error(), "min_confidence") {
			t.Fatalf("min_confidence %v error = %v", value, err)
		}
	}
}

func TestPolicyDetectorAndRedactActionValidation(t *testing.T) {
	policy := DefaultPrivacyPolicy()
	policy.Detector = PolicyDetectorOpenAIPrivacyFilter
	modelID := LegacyOpenAIPrivacyFilterInstallationID
	policy.LocalModelID = &modelID
	policy.RequestAction = PolicyActionRedact
	if err := policy.Validate(); err != nil {
		t.Fatalf("model/redact policy rejected: %v", err)
	}
	policy.Detector = "remote_service"
	if err := policy.Validate(); err == nil || !strings.Contains(err.Error(), "detector") {
		t.Fatalf("invalid detector error = %v", err)
	}
	policy = DefaultPrivacyPolicy()
	policy.RequestAction = "drop"
	if err := policy.Validate(); err == nil || !strings.Contains(err.Error(), "request action") {
		t.Fatalf("invalid action error = %v", err)
	}
}

func TestPrivacyDefaultRejectsMutableIdentityScopeAndResponseAction(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Policy)
	}{
		{name: "id", mutate: func(policy *Policy) { policy.ID = "policy_other" }},
		{name: "name", mutate: func(policy *Policy) { policy.Name = "other" }},
		{name: "priority", mutate: func(policy *Policy) { policy.Priority = 1 }},
		{name: "response action", mutate: func(policy *Policy) {
			policy.ResponseAction = PolicyActionWarn
		}},
		{name: "protocol scope", mutate: func(policy *Policy) {
			policy.Match.Protocols = []ProtocolID{ProtocolOpenAIResponses}
		}},
		{name: "model scope", mutate: func(policy *Policy) { policy.Match.Models = []string{"gpt-5"} }},
		{name: "endpoint scope", mutate: func(policy *Policy) {
			policy.Match.ServiceIDs = []ServiceID{"endpoint_01"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := DefaultPrivacyPolicy()
			test.mutate(&policy)
			if err := ValidatePrivacyDefault(policy); err == nil {
				t.Fatal("mutable default policy was accepted")
			}
		})
	}
}

func TestPolicyCustomRegexRulesValidation(t *testing.T) {
	policy := DefaultPrivacyPolicy()
	policy.RegexSource = PolicyRegexSourceCustom
	policy.CustomRegexRules = []PolicyRegexRule{{
		Kind:    "email",
		Pattern: `(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`,
	}}
	if err := policy.Validate(); err != nil {
		t.Fatalf("valid custom rules rejected: %v", err)
	}

	policy.CustomRegexRules = nil
	if err := policy.Validate(); err == nil ||
		!strings.Contains(err.Error(), "custom_regex_rules") {
		t.Fatalf("empty custom rules error = %v", err)
	}

	policy = DefaultPrivacyPolicy()
	policy.RegexSource = PolicyRegexSourceCustom
	policy.CustomRegexRules = []PolicyRegexRule{{Kind: "private_person", Pattern: "alice"}}
	if err := policy.Validate(); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("model-only kind error = %v", err)
	}

	policy = DefaultPrivacyPolicy()
	policy.RegexSource = PolicyRegexSourceCustom
	policy.CustomRegexRules = []PolicyRegexRule{{Kind: "email", Pattern: "("}}
	if err := policy.Validate(); err == nil || !strings.Contains(err.Error(), "regex pattern") {
		t.Fatalf("invalid pattern error = %v", err)
	}

	policy = DefaultPrivacyPolicy()
	policy.RegexSource = "remote"
	if err := policy.Validate(); err == nil || !strings.Contains(err.Error(), "regex_source") {
		t.Fatalf("invalid regex_source error = %v", err)
	}

	policy = DefaultPrivacyPolicy()
	policy.Detector = PolicyDetectorLocalModel
	modelID := LegacyOpenAIPrivacyFilterInstallationID
	policy.LocalModelID = &modelID
	policy.RegexSource = PolicyRegexSourceCustom
	policy.CustomRegexRules = nil
	if err := policy.Validate(); err != nil {
		t.Fatalf("local_model may keep empty custom rules: %v", err)
	}
}

func TestDefaultKindRulesCoverEveryKindAndDisableTheNoisyOnes(t *testing.T) {
	rules := DefaultPrivacyKindRules()
	if len(rules) != len(PrivacyKinds()) {
		t.Fatalf("kind rules = %#v", rules)
	}
	for index, rule := range rules {
		if rule.Kind != PrivacyKinds()[index] {
			t.Fatalf("kind rules are not in canonical order: %#v", rules)
		}
		if err := rule.Validate(); err != nil {
			t.Fatalf("default rule %q rejected: %v", rule.Kind, err)
		}
		wantStyle := PlaceholderStyleNatural
		if PlaceholderStyleLocked(rule.Kind) {
			wantStyle = PlaceholderStyleToken
		}
		if rule.Style != wantStyle {
			t.Fatalf("kind %q default style = %q", rule.Kind, rule.Style)
		}
		wantEnabled := rule.Kind != "url" && rule.Kind != "ip_address"
		if rule.Enabled != wantEnabled {
			t.Fatalf("kind %q default enabled = %v", rule.Kind, rule.Enabled)
		}
	}
}

// TestLockedKindsRejectNaturalPlaceholderStyle pins the safety property: a
// credential dressed up as a usable-looking key invites the model to call an
// API with it, and a plausible fake name, address, or date would be taken as
// fact because those kinds have no reserved namespace to draw stand-ins from.
func TestLockedKindsRejectNaturalPlaceholderStyle(t *testing.T) {
	for _, kind := range PrivacyKinds() {
		rule := PolicyKindRule{Kind: kind, Enabled: true, Style: PlaceholderStyleNatural}
		err := rule.Validate()
		if !PlaceholderStyleLocked(kind) {
			if err != nil {
				t.Fatalf("kind %q rejected natural style: %v", kind, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "placeholder style") {
			t.Fatalf("kind %q accepted natural style: %v", kind, err)
		}
		policy := DefaultPrivacyPolicy()
		policy.KindRules = []PolicyKindRule{rule}
		if err := ValidatePrivacyDefault(policy); err == nil ||
			!strings.Contains(err.Error(), "kind_rules") {
			t.Fatalf("policy accepted natural style for %q: %v", kind, err)
		}
	}
}

func TestPolicyKindAndAllowlistRuleValidation(t *testing.T) {
	policy := DefaultPrivacyPolicy()
	policy.KindRules = append(DefaultPrivacyKindRules(), PolicyKindRule{
		Kind: "email", Enabled: true, Style: PlaceholderStyleToken,
	})
	if err := policy.Validate(); err == nil ||
		!strings.Contains(err.Error(), "duplicate kind") {
		t.Fatalf("duplicate kind error = %v", err)
	}

	policy = DefaultPrivacyPolicy()
	policy.KindRules = []PolicyKindRule{{
		Kind: "postal_code", Enabled: true, Style: PlaceholderStyleToken,
	}}
	if err := policy.Validate(); err == nil ||
		!strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("unknown kind error = %v", err)
	}

	policy = DefaultPrivacyPolicy()
	policy.AllowlistRules = []PolicyAllowlistRule{
		{Type: PolicyAllowlistTypeDomainSuffix, Value: "github.com"},
		{Type: PolicyAllowlistTypeCIDR, Value: "10.0.0.0/8"},
		{Type: PolicyAllowlistTypeLiteral, Value: "localhost"},
	}
	if err := policy.Validate(); err != nil {
		t.Fatalf("valid allowlist rejected: %v", err)
	}

	policy.AllowlistRules = append(
		policy.AllowlistRules,
		PolicyAllowlistRule{Type: PolicyAllowlistTypeCIDR, Value: "10.0.0.0/8"},
	)
	if err := policy.Validate(); err == nil ||
		!strings.Contains(err.Error(), "duplicate allowlist rule") {
		t.Fatalf("duplicate allowlist error = %v", err)
	}

	policy = DefaultPrivacyPolicy()
	policy.AllowlistRules = []PolicyAllowlistRule{{
		Type: PolicyAllowlistTypeCIDR, Value: "10.0.0.0/33",
	}}
	if err := policy.Validate(); err == nil ||
		!strings.Contains(err.Error(), "allowlist_rules") {
		t.Fatalf("malformed CIDR error = %v", err)
	}
}

// TestNormalizeFillsKindRulesForLegacyPolicies covers a row written before the
// field existed: a partial list would otherwise leave the effective shape of
// the omitted kinds ambiguous once a patch replaced the whole list.
func TestNormalizeFillsKindRulesForLegacyPolicies(t *testing.T) {
	policy := DefaultPrivacyPolicy()
	policy.KindRules = []PolicyKindRule{{
		Kind: "email", Enabled: false, Style: PlaceholderStyleToken,
	}}
	NormalizePrivacyPolicyDefaults(&policy)
	if len(policy.KindRules) != len(PrivacyKinds()) {
		t.Fatalf("kind rules were not completed: %#v", policy.KindRules)
	}
	for index, rule := range policy.KindRules {
		if rule.Kind != PrivacyKinds()[index] {
			t.Fatalf("kind rules are not in canonical order: %#v", policy.KindRules)
		}
	}
	for _, rule := range policy.KindRules {
		if rule.Kind != "email" {
			continue
		}
		if rule.Enabled || rule.Style != PlaceholderStyleToken {
			t.Fatalf("explicit email rule was overwritten: %#v", rule)
		}
	}

	policy = DefaultPrivacyPolicy()
	policy.KindRules = nil
	NormalizePrivacyPolicyDefaults(&policy)
	if len(policy.KindRules) != len(PrivacyKinds()) {
		t.Fatalf("empty kind rules were not seeded: %#v", policy.KindRules)
	}
}
