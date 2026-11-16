package control

import "testing"

func TestPrivacyPolicyAcceptsDeclaredCustomKinds(t *testing.T) {
	const declared = `[{"kind":"custom_org","enabled":true,"style":"token","notify":true,"label":"公司名称"}]`
	const rules = `[{"kind":"custom_org","pattern":"ACME-[0-9]+"}]`
	if err := validatePrivacyPolicyValue(obj(t, privacyPolicyJSON,
		`regex_source="custom"`, "kind_rules="+declared, "custom_regex_rules="+rules)); err != nil {
		t.Fatalf("declared custom kind: %v", err)
	}
	if err := validatePrivacyPolicyPatch(v(t, `{"kind_rules":`+declared+`,"custom_regex_rules":`+rules+`}`)); err != nil {
		t.Fatalf("custom kind patch: %v", err)
	}
	// A rule naming a kind no kind rule declares is refused, as Core does.
	if err := validatePrivacyPolicyValue(obj(t, privacyPolicyJSON,
		`regex_source="custom"`, "custom_regex_rules="+rules)); err == nil {
		t.Error("undeclared custom kind accepted")
	}
	for _, invalid := range []string{
		`[{"kind":"custom_org","enabled":true,"style":"token"}]`,
		`[{"kind":"custom_org","enabled":true,"style":"natural","label":"公司"}]`,
		`[{"kind":"custom_org","enabled":true,"style":"token","label":" 公司"}]`,
		`[{"kind":"custom_Org","enabled":true,"style":"token","label":"公司"}]`,
		`[{"kind":"email","enabled":true,"style":"natural","label":"邮箱"}]`,
		`[{"kind":"custom_org","enabled":true,"style":"token","label":"公司"},{"kind":"custom_firm","enabled":true,"style":"token","label":"公司"}]`,
	} {
		if err := validatePrivacyPolicyValue(obj(t, privacyPolicyJSON, "kind_rules="+invalid)); err == nil {
			t.Errorf("%s accepted", invalid)
		}
	}
	if err := validatePrivacyDryRunKind("custom_org"); err != nil {
		t.Errorf("dry-run custom kind: %v", err)
	}
	if err := validatePrivacyDryRunKind("organization"); err == nil {
		t.Error("dry-run arbitrary kind accepted")
	}
}
