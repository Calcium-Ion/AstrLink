package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func customKindPolicy(rules ...PolicyKindRule) Policy {
	policy := DefaultPrivacyPolicy()
	policy.RegexSource = PolicyRegexSourceCustom
	policy.KindRules = append(DefaultPrivacyKindRules(), rules...)
	policy.CustomRegexRules = []PolicyRegexRule{{Kind: "custom_org", Pattern: `ACME-[0-9]+`}}
	return policy
}

func TestPolicyCustomKindDeclaredInKindRules(t *testing.T) {
	policy := customKindPolicy(PolicyKindRule{
		Kind: "custom_org", Enabled: true, Style: PlaceholderStyleToken, Notify: true, Label: "公司名称",
	})
	if err := ValidatePrivacyDefault(policy); err != nil {
		t.Fatalf("declared custom kind rejected: %v", err)
	}

	// The kind_rules entry is the declaration: a rule naming a custom kind
	// nobody declared would detect values no switch governs.
	undeclared := customKindPolicy()
	if err := ValidatePrivacyDefault(undeclared); err == nil ||
		!strings.Contains(err.Error(), "not declared") {
		t.Fatalf("undeclared custom kind error = %v", err)
	}

	// A built-in pattern stays bound to built-in kinds.
	if !IsCustomPrivacyKind("custom_org") || IsCustomPrivacyKind("account") {
		t.Fatal("custom kind shape is not told apart from built-in kinds")
	}
}

func TestPolicyCustomKindRejectsInvalidDeclarations(t *testing.T) {
	valid := PolicyKindRule{Kind: "custom_org", Enabled: true, Style: PlaceholderStyleToken, Notify: true, Label: "机构"}
	cases := []struct {
		name  string
		rules []PolicyKindRule
		want  string
	}{
		{"missing label", []PolicyKindRule{{Kind: "custom_org", Enabled: true, Style: PlaceholderStyleToken}}, "label"},
		{"padded label", []PolicyKindRule{{Kind: "custom_org", Enabled: true, Style: PlaceholderStyleToken, Label: " 机构"}}, "label"},
		{"long label", []PolicyKindRule{{Kind: "custom_org", Enabled: true, Style: PlaceholderStyleToken, Label: strings.Repeat("长", MaxPolicyCustomKindLabelRunes+1)}}, "label"},
		{"natural style", []PolicyKindRule{{Kind: "custom_org", Enabled: true, Style: PlaceholderStyleNatural, Label: "机构"}}, "token"},
		{"duplicate kind", []PolicyKindRule{valid, valid}, "duplicate kind"},
		{"duplicate label", []PolicyKindRule{valid, {Kind: "custom_agency", Enabled: true, Style: PlaceholderStyleToken, Label: "机构"}}, "duplicate label"},
		{"upper-case id", []PolicyKindRule{{Kind: "custom_Org", Enabled: true, Style: PlaceholderStyleToken, Label: "机构"}}, "unknown kind"},
		{"bare prefix", []PolicyKindRule{{Kind: "custom_", Enabled: true, Style: PlaceholderStyleToken, Label: "机构"}}, "unknown kind"},
		{"trailing underscore", []PolicyKindRule{{Kind: "custom_org_", Enabled: true, Style: PlaceholderStyleToken, Label: "机构"}}, "unknown kind"},
		{"too long", []PolicyKindRule{{Kind: "custom_" + strings.Repeat("a", MaxPolicyCustomKindBytes), Enabled: true, Style: PlaceholderStyleToken, Label: "机构"}}, "unknown kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := DefaultPrivacyPolicy()
			policy.KindRules = append(DefaultPrivacyKindRules(), tc.rules...)
			if err := policy.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one mentioning %q", err, tc.want)
			}
		})
	}

	builtinLabel := DefaultPrivacyPolicy()
	builtinLabel.KindRules[0].Label = "密钥"
	if err := builtinLabel.Validate(); err == nil || !strings.Contains(err.Error(), "cannot have a label") {
		t.Fatalf("built-in label error = %v", err)
	}

	tooMany := DefaultPrivacyPolicy()
	for index := range MaxPolicyCustomKinds + 1 {
		suffix := string(rune('a'+index/26)) + string(rune('a'+index%26))
		tooMany.KindRules = append(tooMany.KindRules, PolicyKindRule{
			Kind: "custom_" + suffix, Enabled: true, Style: PlaceholderStyleToken, Label: "类型" + suffix,
		})
	}
	if err := tooMany.Validate(); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("too many custom kinds error = %v", err)
	}
}

func TestNormalizeKindRulesKeepsCustomKindsAfterBuiltins(t *testing.T) {
	policy := DefaultPrivacyPolicy()
	// A partial list with the custom kinds first and no style on one of them.
	policy.KindRules = []PolicyKindRule{
		{Kind: "custom_org", Enabled: false, Label: "公司"},
		{Kind: "email", Enabled: true, Style: PlaceholderStyleNatural, Notify: true},
		{Kind: "custom_agency", Enabled: true, Style: PlaceholderStyleToken, Label: "机构"},
	}
	NormalizePrivacyPolicyDefaults(&policy)
	if err := policy.Validate(); err != nil {
		t.Fatalf("normalized policy invalid: %v", err)
	}
	kinds := make([]string, 0, len(policy.KindRules))
	for _, rule := range policy.KindRules {
		kinds = append(kinds, rule.Kind)
	}
	want := append(PrivacyKinds(), "custom_org", "custom_agency")
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("kind order = %v, want %v", kinds, want)
	}
	org := policy.KindRules[len(PrivacyKinds())]
	if org.Style != PlaceholderStyleToken || org.Enabled || org.Label != "公司" {
		t.Fatalf("custom rule = %+v", org)
	}
}

func TestPolicyKindRuleOmitsLabelForBuiltins(t *testing.T) {
	// A policy without custom kinds keeps the wire shape earlier versions
	// read, so syncing it to an older device still decodes.
	encoded, err := json.Marshal(DefaultPrivacyPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"label"`) {
		t.Fatalf("built-in kind rules carry a label: %s", encoded)
	}

	var decoded PolicyKindRule
	if err := json.Unmarshal([]byte(`{"kind":"custom_org","enabled":true,"style":"token","label":"公司"}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Label != "公司" || !decoded.Notify {
		t.Fatalf("decoded rule = %+v", decoded)
	}
}

func TestRecordedKindAcceptsCustomKindsOnly(t *testing.T) {
	if !CanonicalKind("custom_org").Recorded() || !CanonicalKindEmail.Recorded() {
		t.Fatal("recorded kinds must cover built-in and custom kinds")
	}
	if CanonicalKind("custom_org").Valid() {
		t.Fatal("a model label mapping must not accept a custom kind")
	}
	if CanonicalKind("organization").Recorded() {
		t.Fatal("an arbitrary label must not pass as a recorded kind")
	}
	summary := PrivacyRestoreSummary{Hits: []PrivacyHitCount{{Kind: "custom_org", Count: 1}}}
	if err := summary.Validate(); err != nil {
		t.Fatalf("custom hit rejected: %v", err)
	}
	if err := validatePrivacyFindings([]PrivacyFinding{{Kind: "custom_org", JSONPath: "/messages/0/content", Count: 2}}); err != nil {
		t.Fatalf("custom finding rejected: %v", err)
	}
}
