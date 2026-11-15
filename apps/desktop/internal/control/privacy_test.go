package control

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const privacyPolicyJSON = `{
	"id": "policy_privacy_default",
	"name": "隐私保护",
	"enabled": false,
	"priority": 0,
	"detector": "regex",
	"local_model_id": null,
	"min_confidence": 0.6,
	"regex_source": "builtin",
	"custom_regex_rules": [],
	"kind_rules": [
		{"kind": "common_secret", "enabled": true, "style": "token", "notify": true},
		{"kind": "payment_card", "enabled": true, "style": "natural", "notify": true},
		{"kind": "account", "enabled": true, "style": "natural", "notify": true},
		{"kind": "email", "enabled": true, "style": "natural", "notify": true},
		{"kind": "phone", "enabled": true, "style": "natural", "notify": true},
		{"kind": "url", "enabled": false, "style": "natural", "notify": true},
		{"kind": "ip_address", "enabled": false, "style": "natural", "notify": true},
		{"kind": "private_person", "enabled": true, "style": "token", "notify": true},
		{"kind": "private_address", "enabled": true, "style": "token", "notify": true},
		{"kind": "private_date", "enabled": true, "style": "token", "notify": true}
	],
	"allowlist_rules": [
		{"type": "domain_suffix", "value": "github.com"},
		{"type": "cidr", "value": "127.0.0.0/8"},
		{"type": "literal", "value": "support@astrlink.invalid"}
	],
	"match": {},
	"request_action": "redact",
	"response_action": "allow",
	"response_restore": true,
	"restore_tool_arguments": true,
	"placeholder_notice": true,
	"skip_tool_declarations": false,
	"inspect_additional_tools": false
}`

const privacyVariantJSON = `{
	"id": "cpu_int8",
	"name": "CPU INT8",
	"quantization": "int8",
	"bytes_total": 180000000,
	"estimated_ram_bytes": 420000000,
	"recommended": true,
	"supported": true,
	"unsupported_reason": null
}`

const privacyInstallationJSON = `{
	"id": "model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	"source": "catalog",
	"catalog_id": "catalog_example_privacy",
	"catalog_source": "community",
	"name": "Example Privacy",
	"license": "apache-2.0",
	"languages": ["en"],
	"repo_id": "example/privacy-filter",
	"revision": "53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088",
	"variant_id": "cpu_int8",
	"variant_name": "CPU INT8",
	"quantization": "int8",
	"adapter": "hf_token_classification",
	"status": "downloading",
	"bytes_downloaded": 45,
	"bytes_total": 180000000,
	"estimated_ram_bytes": 420000000,
	"error": null,
	"label_mapping": {"EMAIL": "email", "MISC": null},
	"installed_at": null,
	"preparation": null
}`

func catalogJSON(variant string, version string, recommended string) string {
	return `{"items": [{
		"id": "catalog_example_privacy",
		"name": "Example Privacy",
		"summary": "Small compatible privacy model.",
		"source": "community",
		"repo_id": "example/privacy-filter",
		"revision": "53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088",
		"license": "apache-2.0",
		"languages": ["en"],
		"adapter": "hf_token_classification",
		"variants": [` + variant + `],
		"version": ` + version + `,
		"recommended": ` + recommended + `
	}]}`
}

// obj decodes a JSON object literal and applies edits, each a key and the
// JSON of its new value, or a key alone to remove it.
func obj(t *testing.T, literal string, edits ...string) object {
	t.Helper()
	o, ok := v(t, literal).(object)
	if !ok {
		t.Fatalf("%s is not an object", literal)
	}
	for _, edit := range edits {
		key, value, ok := strings.Cut(edit, "=")
		if !ok {
			delete(o, key)
			continue
		}
		o[key] = v(t, value)
	}
	return o
}

func encoded(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPrivacyPolicyToolDeclarationsRequireBooleans(t *testing.T) {
	for _, field := range []string{"skip_tool_declarations", "inspect_additional_tools"} {
		for _, value := range []string{"false", "true"} {
			policy, err := parsePrivacyPolicy(encoded(t, obj(t, privacyPolicyJSON, field+"="+value)))
			if err != nil {
				t.Fatalf("%s=%s: %v", field, value, err)
			}
			if got := obj(t, string(policy))[field]; fmt.Sprint(got) != value {
				t.Errorf("%s = %v", field, got)
			}
			if err := validatePrivacyPolicyPatch(v(t, `{"`+field+`":`+value+`}`)); err != nil {
				t.Errorf("patch %s=%s: %v", field, value, err)
			}
		}
		for _, value := range []string{"null", `"false"`, "0"} {
			if _, err := parsePrivacyPolicy(encoded(t, obj(t, privacyPolicyJSON, field+"="+value))); err == nil {
				t.Errorf("policy %s=%s accepted", field, value)
			}
			if err := validatePrivacyPolicyPatch(v(t, `{"`+field+`":`+value+`}`)); err == nil {
				t.Errorf("patch %s=%s accepted", field, value)
			}
		}
	}
}

func TestStrictlyParsesThePrivacyPolicyControlContract(t *testing.T) {
	if _, err := parsePrivacyPolicyPage([]byte(`{"items":[` + privacyPolicyJSON + `],"next_cursor":null}`)); err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{
		`{"items":[` + privacyPolicyJSON + `],"next_cursor":"next"}`,
		`{"items":[],"next_cursor":null}`,
		`{"items":[` + privacyPolicyJSON + `]}`,
	} {
		if _, err := parsePrivacyPolicyPage([]byte(page)); err == nil {
			t.Errorf("page %.60s accepted", page)
		}
	}
	record, err := PolicyRecordFrom(`"sha256:`+strings.Repeat("a", 64)+`"`, []byte(privacyPolicyJSON))
	if err != nil {
		t.Fatal(err)
	}
	if policy := obj(t, string(record.Policy)); policy["id"] != "policy_privacy_default" || policy["regex_source"] != "builtin" {
		t.Fatalf("policy = %s", record.Policy)
	}
	legacy := `{
		"id": "policy_privacy_default",
		"name": "隐私保护",
		"enabled": false,
		"priority": 0,
		"detector": "regex",
		"local_model_id": null,
		"min_confidence": 0.6,
		"match": {},
		"request_action": "redact",
		"response_action": "allow",
		"response_restore": true
	}`
	record, err = PolicyRecordFrom(`"sha256:`+strings.Repeat("b", 64)+`"`, []byte(legacy))
	if err != nil {
		t.Fatalf("legacy policy without regex fields should normalize: %v", err)
	}
	policy := obj(t, string(record.Policy))
	if rules, _ := policy["custom_regex_rules"].([]any); policy["regex_source"] != "builtin" || rules == nil || len(rules) != 0 {
		t.Fatalf("legacy policy = %s", record.Policy)
	}
	if err := validatePrivacyPolicyValue(obj(t, privacyPolicyJSON, `secret="must-not-cross-ipc"`)); err == nil {
		t.Error("unexpected field accepted")
	}
	if _, err := PolicyRecordFrom(`"weak"`, []byte(privacyPolicyJSON)); err == nil {
		t.Error("weak etag accepted")
	}
	for _, edit := range []string{
		`id="policy_other"`,
		`priority=1`,
		`priority=0.0`,
		`name=""`,
		`detector="local_model"`,
		`local_model_id="model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`,
		`response_action="warn"`,
		`match={"protocol":"x"}`,
		`regex_source="custom"`,
	} {
		if err := validatePrivacyPolicyValue(obj(t, privacyPolicyJSON, edit)); err == nil {
			t.Errorf("%s accepted", edit)
		}
	}
	if err := validatePrivacyPolicyValue(obj(t, privacyPolicyJSON,
		`detector="local_model"`, `local_model_id="model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`)); err != nil {
		t.Errorf("local model policy: %v", err)
	}
}

func TestRejectsPrivacyKindRulesThatBreakASafetyInvariant(t *testing.T) {
	for _, rules := range []string{
		`[{"kind":"common_secret","enabled":true,"style":"natural"}]`,
		`[{"kind":"email","enabled":true,"style":"natural"},{"kind":"email","enabled":false,"style":"token"}]`,
		`[{"kind":"postal_code","enabled":true,"style":"token"}]`,
		`[{"kind":"email","enabled":true,"style":"plausible"}]`,
		`[{"kind":"email","enabled":true}]`,
		`[{"kind":"email","enabled":true,"style":"natural","notify":"no"}]`,
	} {
		if err := validatePrivacyPolicyValue(obj(t, privacyPolicyJSON, "kind_rules="+rules)); err == nil {
			t.Errorf("%s accepted", rules)
		}
	}
}

func TestPrivacyKindRulesAcceptNotifyAndItsAbsence(t *testing.T) {
	for _, rules := range []string{
		`[{"kind":"email","enabled":true,"style":"natural","notify":false}]`,
		`[{"kind":"email","enabled":true,"style":"natural"}]`,
	} {
		if err := validatePrivacyPolicyValue(obj(t, privacyPolicyJSON, "kind_rules="+rules)); err != nil {
			t.Errorf("%s: %v", rules, err)
		}
		if err := validatePrivacyPolicyPatch(v(t, `{"kind_rules":`+rules+`}`)); err != nil {
			t.Errorf("patch %s: %v", rules, err)
		}
	}
}

func TestRejectsMalformedPrivacyAllowlistRules(t *testing.T) {
	tooMany := make([]string, maxPrivacyAllowlistRules+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf(`{"type":"literal","value":"%d"}`, i)
	}
	for _, rules := range []string{
		`[{"type":"regex","value":".*"}]`,
		`[{"type":"literal","value":""}]`,
		`[{"type":"literal"}]`,
		`[{"type":"literal","value":` + repeatJSON("x", maxPrivacyAllowlistValueChars+1) + `}]`,
		`[` + strings.Join(tooMany, ",") + `]`,
	} {
		if err := validatePrivacyPolicyValue(obj(t, privacyPolicyJSON, "allowlist_rules="+rules)); err == nil {
			t.Errorf("%.60s accepted", rules)
		}
	}
	if err := validatePrivacyPolicyValue(obj(t, privacyPolicyJSON, "allowlist_rules=[]")); err != nil {
		t.Error(err)
	}
}

func TestRejectsNonBooleanPrivacyRestoreScopeFlags(t *testing.T) {
	for _, field := range []string{"restore_tool_arguments", "placeholder_notice"} {
		if err := validatePrivacyPolicyValue(obj(t, privacyPolicyJSON, field+`="yes"`)); err == nil {
			t.Errorf("%s accepted a string", field)
		}
	}
}

func TestValidatesSelectablePrivacyPolicyPatchFields(t *testing.T) {
	for _, patch := range []string{
		`{"enabled":true,"detector":"local_model","local_model_id":"model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","min_confidence":0.91,"request_action":"block","response_restore":false}`,
		`{"detector":"regex","local_model_id":null,"regex_source":"custom","custom_regex_rules":[{"kind":"email","pattern":"alice@example\\.com"}]}`,
		`{"detector":"regex","local_model_id":null}`,
		`{"kind_rules":[{"kind":"url","enabled":true,"style":"natural"}],"allowlist_rules":[{"type":"domain_suffix","value":"internal.example"}],"restore_tool_arguments":false,"placeholder_notice":false}`,
	} {
		if err := validatePrivacyPolicyPatch(v(t, patch)); err != nil {
			t.Errorf("%.60s: %v", patch, err)
		}
	}
	for _, patch := range []string{
		`{"kind_rules":[{"kind":"private_date","enabled":true,"style":"natural"}]}`,
		`{"restore_tool_arguments":"yes"}`,
		`{"custom_regex_rules":[{"kind":"private_person","pattern":"alice"}]}`,
		`{"enabled":null}`,
		`{"name":"renamed"}`,
		`{"response_action":"warn"}`,
		`{"response_restore":"yes"}`,
		`{"min_confidence":-0.01}`,
		`{"min_confidence":1.01}`,
		`{"local_model_id":"model_bad"}`,
		`{"local_model_id":7}`,
		`{}`,
		`[]`,
	} {
		if err := validatePrivacyPolicyPatch(v(t, patch)); err == nil {
			t.Errorf("%s accepted", patch)
		}
	}
}

func TestParsesPrivacyRegexBuiltinRules(t *testing.T) {
	if _, err := parsePrivacyRegexBuiltinRules([]byte(`{"rules":[{"kind":"email","pattern":"(?i)alice"},{"kind":"common_secret","pattern":"sk-[A-Za-z0-9]+"}]}`)); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"rules":[{"kind":"private_person","pattern":"alice"}]}`,
		`{"rules":[{"kind":"email","pattern":""}]}`,
		`{"rules":[],"extra":1}`,
	} {
		if _, err := parsePrivacyRegexBuiltinRules([]byte(body)); err == nil {
			t.Errorf("%s accepted", body)
		}
	}
}

func TestValidatesPrivacyDryRunInput(t *testing.T) {
	if err := validatePrivacyDryRunInput(v(t, `{"protocol":"openai.chat","sample_text":"hello","policy":{"enabled":true}}`)); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		`{"protocol":"openai.chat"}`,
		`{"protocol":"openai.chat","sample_text":""}`,
		`{"protocol":"openai.chat","sample_text":` + repeatJSON("x", maxDryRunSampleBytes+1) + `}`,
		`{"protocol":"grpc","sample_text":"x"}`,
		`{"protocol":"openai.chat","sample_text":"x","policy":{"name":"x"}}`,
		`{"protocol":"openai.chat","sample_text":"x","extra":1}`,
	} {
		if err := validatePrivacyDryRunInput(v(t, input)); err == nil {
			t.Errorf("%.60s accepted", input)
		}
	}
}

const dryRunConfidenceJSON = `{
	"decision": "allow",
	"findings_summary": "",
	"findings": [],
	"suppressed_findings": [{
		"kind": "private_person",
		"path": "/input",
		"start": 0,
		"end": 21,
		"confidence": 0.696717
	}],
	"inspected_body": "{\"input\":\"画一张猫的图片\"}"
}`

const dryRunStylesJSON = `{
	"decision": "redact",
	"findings_summary": "email 1",
	"findings": [{
		"kind": "email",
		"path": "/messages/0/content",
		"start": 3,
		"end": 20,
		"confidence": 0.99
	}],
	"suppressed_findings": [{
		"kind": "url",
		"path": "/messages/0/content",
		"start": 30,
		"end": 52,
		"confidence": 0.99,
		"reason": "kind_disabled"
	}],
	"inspected_body": "{}",
	"redacted_body": "{}",
	"redactions": [{
		"placeholder": "redacted-9f2c1d@private.invalid",
		"kind": "email",
		"value": "alice@corp.example",
		"style": "natural"
	}]
}`

// editFirst decodes literal and edits the first element of its array
// field.
func editFirst(t *testing.T, literal, field string, edits ...string) []byte {
	t.Helper()
	o := obj(t, literal)
	items := o[field].([]any)
	element := items[0].(object)
	for _, edit := range edits {
		key, value, ok := strings.Cut(edit, "=")
		if !ok {
			delete(element, key)
			continue
		}
		element[key] = v(t, value)
	}
	return encoded(t, o)
}

func TestStrictlyParsesPrivacyDryRunConfidenceResults(t *testing.T) {
	if _, err := parsePrivacyDryRunResult([]byte(dryRunConfidenceJSON)); err != nil {
		t.Fatal(err)
	}
	for _, body := range [][]byte{
		editFirst(t, dryRunConfidenceJSON, "suppressed_findings", "confidence"),
		editFirst(t, dryRunConfidenceJSON, "suppressed_findings", "confidence=1.01"),
		editFirst(t, dryRunConfidenceJSON, "suppressed_findings", "end=0"),
		editFirst(t, dryRunConfidenceJSON, "suppressed_findings", "start=-1"),
		editFirst(t, dryRunConfidenceJSON, "suppressed_findings", `path=""`),
	} {
		if _, err := parsePrivacyDryRunResult(body); err == nil {
			t.Errorf("%s accepted", body)
		}
	}
}

func TestStrictlyParsesPrivacyDryRunSuppressionAndPlaceholderStyles(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(dryRunStylesJSON),
		editFirst(t, dryRunStylesJSON, "suppressed_findings", `kind="common_secret"`, `reason="astrlink_identifier"`),
		editFirst(t, dryRunStylesJSON, "suppressed_findings", `reason="example_value"`),
	} {
		if _, err := parsePrivacyDryRunResult(body); err != nil {
			t.Errorf("%s: %v", body, err)
		}
	}
	for _, body := range [][]byte{
		editFirst(t, dryRunStylesJSON, "suppressed_findings", `reason="vibes"`),
		editFirst(t, dryRunStylesJSON, "redactions", `style="plausible"`),
		editFirst(t, dryRunStylesJSON, "redactions", `placeholder=""`),
	} {
		if _, err := parsePrivacyDryRunResult(body); err == nil {
			t.Errorf("%s accepted", body)
		}
	}
	// A Core predating the styles omits both additive fields.
	legacy := obj(t, dryRunStylesJSON)
	delete(legacy["suppressed_findings"].([]any)[0].(object), "reason")
	delete(legacy["redactions"].([]any)[0].(object), "style")
	if _, err := parsePrivacyDryRunResult(encoded(t, legacy)); err != nil {
		t.Error(err)
	}
}

func TestPrivacyModelVariantsAcceptOnlyKnownUnsupportedReasons(t *testing.T) {
	unsupported := func(reason string, edits ...string) object {
		return obj(t, privacyVariantJSON, append([]string{`id="coreml_fp16_gpu"`, "recommended=false", "supported=false", "unsupported_reason=" + reason}, edits...)...)
	}
	for _, reason := range []string{`"cpu_only"`, `"requires_macos_26"`} {
		if err := validatePrivacyModelVariants([]any{obj(t, privacyVariantJSON), unsupported(reason)}); err != nil {
			t.Errorf("%s: %v", reason, err)
		}
	}
	for _, variant := range []object{
		unsupported(`"requires_macos_15"`),
		unsupported("null"),
		unsupported(`"requires_macos_26"`, "supported=true"),
	} {
		if err := validatePrivacyModelVariants([]any{variant}); err == nil {
			t.Errorf("%v accepted", variant)
		}
	}
	if err := validatePrivacyModelVariants([]any{obj(t, privacyVariantJSON), obj(t, privacyVariantJSON)}); err == nil {
		t.Error("duplicate variant accepted")
	}
	if err := validatePrivacyModelVariants([]any{obj(t, privacyVariantJSON, "bytes_total=0")}); err == nil {
		t.Error("empty supported variant accepted")
	}
}

func readyInstallation(t *testing.T, edits ...string) object {
	return obj(t, privacyInstallationJSON, append([]string{`status="ready"`, "bytes_downloaded=180000000", `installed_at="2026-10-17T10:30:00Z"`}, edits...)...)
}

func TestValidatesPolicyModelPreparationState(t *testing.T) {
	if err := validatePrivacyModelInstallation(readyInstallation(t)); err != nil {
		t.Fatal(err)
	}
	for _, preparation := range []string{
		`{"state":"preparing","fallback":"local_model"}`,
		`{"state":"preparing","fallback":null}`,
		`{"state":"failed","fallback":"regex"}`,
		`{"state":"ready","fallback":null}`,
	} {
		if err := validatePrivacyModelInstallation(readyInstallation(t, "preparation="+preparation)); err != nil {
			t.Errorf("%s: %v", preparation, err)
		}
	}
	for _, preparation := range []string{
		`{"state":"compiling","fallback":null}`,
		`{"state":"ready","fallback":"regex"}`,
		`{"state":"preparing","fallback":"cloud"}`,
		`{"state":"preparing"}`,
		`{"state":"preparing","fallback":null,"progress":1}`,
		`"preparing"`,
	} {
		if err := validatePrivacyModelInstallation(readyInstallation(t, "preparation="+preparation)); err == nil {
			t.Errorf("%s accepted", preparation)
		}
	}
	if err := validatePrivacyModelInstallation(obj(t, privacyInstallationJSON, `preparation={"state":"preparing","fallback":null}`)); err == nil {
		t.Error("preparation of a downloading model accepted")
	}
	if err := validatePrivacyModelInstallation(obj(t, privacyInstallationJSON, "preparation")); err == nil {
		t.Error("missing preparation accepted")
	}
}

func TestStrictlyValidatesLocalPrivacyModelProbePathsWithoutLeakingThem(t *testing.T) {
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{directory, filepath.Join(directory, "model.onnx")} {
		if err := validateLocalPrivacyModelProbeInput(map[string]any{"path": path}); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
	for _, input := range []any{
		map[string]any{},
		map[string]any{"path": directory, "unexpected": true},
		map[string]any{"directory": directory},
		map[string]any{"path": ""},
		map[string]any{"path": "relative/model-directory"},
		map[string]any{"path": "smb://fileserver.private/model-secret"},
		map[string]any{"path": "file:///private/model-secret"},
		map[string]any{"path": "/private/model-secret\n"},
		map[string]any{"path": json.Number("42")},
		map[string]any{"path": filepath.Join(directory, strings.Repeat("x", 4097))},
	} {
		err := validateLocalPrivacyModelProbeInput(input)
		if err == nil {
			t.Errorf("%v accepted", input)
			continue
		}
		if strings.Contains(err.Error(), "fileserver.private") || strings.Contains(err.Error(), "model-secret") {
			t.Errorf("error leaks the path: %v", err)
		}
	}
	for value, want := range map[string]bool{
		"smb://server/share":    true,
		"file:///private/model": true,
		"/private/model:name":   false,
		`C:\models\privacy`:     false,
	} {
		if got := looksLikeURI(value); got != want {
			t.Errorf("looksLikeURI(%q) = %v", value, got)
		}
	}
}

func TestStrictlyParsesPrivacyModelCatalogProbeAndInstallations(t *testing.T) {
	if err := validatePrivacyModelAdapter("pplx_bioes_viterbi"); err != nil {
		t.Fatal(err)
	}
	catalog := catalogJSON(privacyVariantJSON, `"0.2.0"`, "true")
	if _, err := parsePrivacyModelCatalog([]byte(catalog)); err != nil {
		t.Fatal(err)
	}
	model := obj(t, catalog)["items"].([]any)[0].(object)
	for _, tc := range []struct {
		edit  string
		valid bool
	}{
		{"version=null", true},
		{`version="10.0.123456"`, true},
		{`version="v0.2.0"`, false},
		{`version="0.02.0"`, false},
		{`version="0.2"`, false},
		{`version="1234567.0.0"`, false},
		{"version=2", false},
		{`recommended="yes"`, false},
		{"recommended=null", false},
		{"recommended", false},
	} {
		edited := maps.Clone(model)
		key, value, ok := strings.Cut(tc.edit, "=")
		if ok {
			edited[key] = v(t, value)
		} else {
			delete(edited, key)
		}
		if err := validatePrivacyCatalogModel(edited); (err == nil) != tc.valid {
			t.Errorf("%s: %v", tc.edit, err)
		}
	}
	duplicate := `{"items":[` + strings.TrimSuffix(strings.TrimPrefix(catalog, `{"items": [`), `]}`) + `,` +
		strings.TrimSuffix(strings.TrimPrefix(catalog, `{"items": [`), `]}`) + `]}`
	if _, err := parsePrivacyModelCatalog([]byte(duplicate)); err == nil {
		t.Error("duplicate catalog ids accepted")
	}

	probe := `{
		"repo_id": "example/privacy-filter",
		"requested_revision": "main",
		"revision": "53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088",
		"name": "Example Privacy",
		"license": "apache-2.0",
		"languages": ["en"],
		"adapter": "hf_token_classification",
		"variants": [` + privacyVariantJSON + `],
		"labels": [
			{"label": "EMAIL", "suggested_kind": "email"},
			{"label": "MISC", "suggested_kind": null}
		],
		"requires_label_mapping": true
	}`
	if _, err := parsePrivacyModelProbe([]byte(probe)); err != nil {
		t.Fatal(err)
	}
	if err := validatePrivacyModelInstallation(obj(t, privacyInstallationJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err := parsePrivacyModelInstallationList([]byte(`{"items":[` + privacyInstallationJSON + `]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := parsePrivacyModelInstallationList([]byte(`{"items":[` + privacyInstallationJSON + `,` + privacyInstallationJSON + `]}`)); err == nil {
		t.Error("duplicate installation ids accepted")
	}
	if err := validatePrivacyModelInstallation(obj(t, privacyInstallationJSON, "bytes_downloaded=9007199254740992")); err == nil {
		t.Error("unsafe integer accepted")
	}
	if err := validatePrivacyModelInstallation(obj(t, privacyInstallationJSON, `path="/private/model"`)); err == nil {
		t.Error("leaked path accepted")
	}
}

func TestParsesPrivacyModelDefaultIgnoreWithoutRequiringManualMapping(t *testing.T) {
	probe := `{
		"repo_id": "example/pii-tracer",
		"requested_revision": "main",
		"revision": "53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088",
		"name": "PII-Tracer",
		"license": "mit",
		"languages": ["en"],
		"adapter": "pplx_bioes_viterbi",
		"variants": [` + privacyVariantJSON + `],
		"labels": [
			{"label": "private_email", "suggested_kind": "email", "suggested_ignore": false},
			{"label": "other_pii", "suggested_kind": null, "suggested_ignore": true}
		],
		"requires_label_mapping": false
	}`
	parse := func(o object) error {
		_, err := parsePrivacyModelProbe(encoded(t, o))
		return err
	}
	label := func(o object, index int) object { return o["labels"].([]any)[index].(object) }
	if err := parse(obj(t, probe)); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"true"`, "null", "1"} {
		o := obj(t, probe)
		label(o, 1)["suggested_ignore"] = v(t, value)
		if parse(o) == nil {
			t.Errorf("suggested_ignore %s accepted", value)
		}
	}
	conflicting := obj(t, probe)
	label(conflicting, 0)["suggested_ignore"] = true
	if parse(conflicting) == nil {
		t.Error("map and ignore accepted")
	}
	unexpected := obj(t, probe)
	label(unexpected, 0)["unexpected"] = false
	if parse(unexpected) == nil {
		t.Error("unexpected label field accepted")
	}
	unmapped := obj(t, probe)
	label(unmapped, 1)["suggested_ignore"] = false
	if parse(unmapped) == nil {
		t.Error("unmapped label without requires_label_mapping accepted")
	}
	unmapped["requires_label_mapping"] = true
	if err := parse(unmapped); err != nil {
		t.Error(err)
	}
	delete(label(unmapped, 1), "suggested_ignore")
	if err := parse(unmapped); err != nil {
		t.Error(err)
	}
	inconsistent := obj(t, probe, "requires_label_mapping=true")
	if parse(inconsistent) == nil {
		t.Error("inconsistent requires_label_mapping accepted")
	}
}

func TestAcceptsTheLargestValidPrivacyModelDirectoryResponse(t *testing.T) {
	mapping := map[string]any{}
	for i := range 256 {
		mapping[fmt.Sprintf("LABEL_%03d_%s", i, strings.Repeat("X", 118))] = nil
	}
	items := make([]any, 100)
	for i := range items {
		installation := obj(t, privacyInstallationJSON)
		installation["id"] = fmt.Sprintf("model_%032x", i)
		installation["label_mapping"] = mapping
		items[i] = installation
	}
	body := encoded(t, map[string]any{"items": items})
	if len(body) <= 2<<20 || len(body) > MaxControlBody {
		t.Fatalf("directory is %d bytes", len(body))
	}
	if _, err := parsePrivacyModelInstallationList(body); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptsPausedModelAndRejectsInconsistentTerminalFields(t *testing.T) {
	if err := validatePrivacyModelInstallation(obj(t, privacyInstallationJSON, `status="paused"`)); err != nil {
		t.Fatal(err)
	}
	for _, edits := range [][]string{
		{`status="paused"`, `error="download_failed"`},
		{`status="paused"`, `installed_at="2026-09-19T00:00:00Z"`},
		{`status="error"`},
		{`status="error"`, `error="unknown_failure"`},
	} {
		if err := validatePrivacyModelInstallation(obj(t, privacyInstallationJSON, edits...)); err == nil {
			t.Errorf("%v accepted", edits)
		}
	}
	if err := validatePrivacyModelInstallation(obj(t, privacyInstallationJSON, `status="error"`, `error="integrity_failed"`)); err != nil {
		t.Error(err)
	}
	id := "model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if got := Timeout("POST", PrivacyModelsPath+"/"+id+"/resume"); got != PrivacyModelMetadataTimeout {
		t.Errorf("resume timeout = %v", got)
	}
	if got := Timeout("POST", PrivacyModelsPath+"/"+id+"/pause"); got != PrivacyMutationTimeout {
		t.Errorf("pause timeout = %v", got)
	}
}

func TestRejectsPrivacyModelContractBoundaryDrift(t *testing.T) {
	emptyLabels := `{
		"repo_id": "example/privacy-filter",
		"requested_revision": "main",
		"revision": "53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088",
		"name": "Example Privacy",
		"license": null,
		"languages": [],
		"adapter": "hf_token_classification",
		"variants": [` + privacyVariantJSON + `],
		"labels": [],
		"requires_label_mapping": false
	}`
	if _, err := parsePrivacyModelProbe([]byte(emptyLabels)); err == nil {
		t.Error("probe without labels accepted")
	}
	for _, installation := range []object{
		readyInstallation(t, "bytes_downloaded=0", "bytes_total=0"),
		obj(t, privacyInstallationJSON, "variant_name="+repeatJSON("x", 65)),
		obj(t, privacyInstallationJSON, `name=" Example Privacy"`),
		obj(t, privacyInstallationJSON, `variant_name="CPU INT8\n"`),
		obj(t, privacyInstallationJSON, `quantization=" int8"`),
		obj(t, privacyInstallationJSON, "catalog_source=null"),
		obj(t, privacyInstallationJSON, `languages=["en","en"]`),
		obj(t, privacyInstallationJSON, `bytes_downloaded=180000001`),
		readyInstallation(t, `installed_at="watTeverZ"`),
		readyInstallation(t, `installed_at="2026-02-30T10:30:00Z"`),
	} {
		if err := validatePrivacyModelInstallation(installation); err == nil {
			t.Errorf("%v accepted", installation)
		}
	}
	for _, variant := range []string{
		obj2JSON(t, privacyVariantJSON, `name=" CPU INT8"`),
		obj2JSON(t, privacyVariantJSON, `quantization="int8\u0000"`),
	} {
		if _, err := parsePrivacyModelCatalog([]byte(catalogJSON(variant, "null", "false"))); err == nil {
			t.Errorf("catalog variant %s accepted", variant)
		}
	}
	custom := []string{`source="custom"`, "catalog_id=null", "catalog_source=null", "license=null", "languages=[]"}
	if err := validatePrivacyModelInstallation(obj(t, privacyInstallationJSON, custom...)); err != nil {
		t.Errorf("custom installation: %v", err)
	}
	local := append(append([]string{}, custom...), `source="local"`, `repo_id="local/model-0123456789ab"`)
	if err := validatePrivacyModelInstallation(obj(t, privacyInstallationJSON, local...)); err != nil {
		t.Errorf("local installation: %v", err)
	}
	if err := validatePrivacyModelInstallation(obj(t, privacyInstallationJSON, append(local, `catalog_id="catalog_example_privacy"`)...)); err == nil {
		t.Error("local installation with a catalog id accepted")
	}
	if err := validatePrivacyModelInstallation(obj(t, privacyInstallationJSON, append(custom, `repo_id="local/model-0123456789ab"`)...)); err == nil {
		t.Error("custom installation with a local repo id accepted")
	}
	if err := validatePrivacyModelInstallation(readyInstallation(t, `installed_at="2026-07-24T18:30:00.123456789+08:00"`)); err != nil {
		t.Error(err)
	}
}

func obj2JSON(t *testing.T, literal string, edits ...string) string {
	return string(encoded(t, obj(t, literal, edits...)))
}

func TestValidatesPrivacyModelInputs(t *testing.T) {
	for _, input := range []string{
		`{"repo_id":"example/privacy-filter","revision":"main"}`,
		`{"repo_id":"example/privacy-filter","revision":"refs/pr/1"}`,
	} {
		if err := validatePrivacyModelProbeInput(v(t, input)); err != nil {
			t.Errorf("%s: %v", input, err)
		}
	}
	for _, input := range []string{
		`{"repo_id":"example","revision":"main"}`,
		`{"repo_id":"a/b/c","revision":"main"}`,
		`{"repo_id":"example/../x","revision":"main"}`,
		`{"repo_id":"-x/privacy","revision":"main"}`,
		`{"repo_id":"example/privacy-filter","revision":"/main"}`,
		`{"repo_id":"example/privacy-filter","revision":"a//b"}`,
		`{"repo_id":"example/privacy-filter","revision":"main/"}`,
		`{"repo_id":"example/privacy-filter"}`,
	} {
		if err := validatePrivacyModelProbeInput(v(t, input)); err == nil {
			t.Errorf("%s accepted", input)
		}
	}
	install := `{"repo_id":"example/privacy-filter","revision":"53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088","variant_id":"cpu_int8","label_mapping":{"EMAIL":"email","MISC":null}}`
	if err := validatePrivacyModelInstallInput(v(t, install)); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []string{
		`revision="main"`,
		`variant_id="CPU"`,
		`variant_id="8bit"`,
		`label_mapping={"EMAIL":"postal"}`,
		`label_mapping={"-x":null}`,
		"label_mapping",
	} {
		if err := validatePrivacyModelInstallInput(obj(t, install, edit)); err == nil {
			t.Errorf("%s accepted", edit)
		}
	}
	for id, valid := range map[string]bool{
		"model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":  true,
		"model_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA":  false,
		"model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":   false,
		"catalog_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": false,
	} {
		if err := ValidatePrivacyModelID(id); (err == nil) != valid {
			t.Errorf("%s: %v", id, err)
		}
	}
}

func TestPrivacyModelControlPathsAreSeparateAndPluralized(t *testing.T) {
	for got, want := range map[string]string{
		PrivacyModelCatalogPath:    "/control/v1/privacy-model-catalog",
		PrivacyModelProbePath:      "/control/v1/privacy-models/probe",
		LocalPrivacyModelProbePath: "/control/v1/privacy-models/local/probe",
		PrivacyModelsPath:          "/control/v1/privacy-models",
		PrivacyModelReleasesPath:   "/control/v1/privacy-model-catalog/releases",
	} {
		if got != want {
			t.Errorf("path %s, want %s", got, want)
		}
	}
}
