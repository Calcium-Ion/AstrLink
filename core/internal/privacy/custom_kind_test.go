package privacy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

// customKindPolicy is the runtime policy the store resolves from a policy
// that declares two custom kinds next to the built-in account kind.
func customKindPolicy(t *testing.T, orgEnabled bool) Policy {
	t.Helper()
	stored := contract.DefaultPrivacyPolicy()
	stored.RegexSource = contract.PolicyRegexSourceCustom
	stored.KindRules = append(contract.DefaultPrivacyKindRules(),
		contract.PolicyKindRule{Kind: "custom_org", Enabled: orgEnabled, Style: contract.PlaceholderStyleToken, Notify: true, Label: "公司名称"},
		contract.PolicyKindRule{Kind: "custom_agency", Enabled: true, Style: contract.PlaceholderStyleToken, Notify: true, Label: "机构名称"},
	)
	for index := range stored.KindRules {
		if stored.KindRules[index].Kind == "account" {
			stored.KindRules[index].Enabled = true
		}
	}
	stored.CustomRegexRules = []contract.PolicyRegexRule{
		{Kind: "custom_org", Pattern: `ACME-[0-9]{4,}`},
		{Kind: "custom_agency", Pattern: `Bureau of [A-Z][a-z]+`},
		{Kind: "account", Pattern: `acct-[0-9]{6}`},
	}
	policy, err := FromContractPolicy(stored)
	if err != nil {
		t.Fatalf("resolve policy: %v", err)
	}
	return policy
}

func TestEngineRedactsAndRestoresCustomKinds(t *testing.T) {
	const body = `{"model":"safe","input":"ACME-20481 asked Bureau of Records about acct-482913 and ACME-20481 again"}`
	engine := newTestEngine(t, nil)
	result, err := engine.Inspect(context.Background(), customKindPolicy(t, true), contract.ProtocolOpenAIResponses, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != DecisionRedact {
		t.Fatalf("decision = %q", result.Decision)
	}
	placeholders := map[Kind]string{}
	for _, redaction := range result.Redactions {
		if previous, exists := placeholders[redaction.Kind]; exists && previous != redaction.Placeholder {
			t.Fatalf("kind %s mapped one value to two placeholders", redaction.Kind)
		}
		placeholders[redaction.Kind] = redaction.Placeholder
		if redaction.Style != contract.PlaceholderStyleToken {
			t.Fatalf("custom kinds must use token placeholders: %+v", redaction)
		}
	}
	// Each kind keeps a marker of its own; none falls back to account or a
	// generic <PRIVATE_…>.
	for kind, prefix := range map[Kind]string{
		"custom_org":    "<PRIVATE_CUSTOM_ORG_",
		"custom_agency": "<PRIVATE_CUSTOM_AGENCY_",
		KindAccount:     "<PRIVATE_ACCOUNT_NUMBER_",
	} {
		placeholder := placeholders[kind]
		if !strings.HasPrefix(placeholder, prefix) || !strings.HasSuffix(placeholder, ">") {
			t.Fatalf("%s placeholder = %q, want prefix %q", kind, placeholder, prefix)
		}
		if _, ok := markerBody(placeholder); !ok {
			t.Fatalf("%s placeholder %q has no derived suffix", kind, placeholder)
		}
	}
	redacted := string(result.Body)
	for _, plaintext := range []string{"ACME-20481", "Bureau of Records", "acct-482913"} {
		if strings.Contains(redacted, plaintext) {
			t.Fatalf("redacted body still holds %q: %s", plaintext, redacted)
		}
	}
	if strings.Count(redacted, placeholders["custom_org"]) != 2 {
		t.Fatalf("a repeated value must reuse its placeholder: %s", redacted)
	}

	// A model answer quoting the markers comes back with the originals,
	// including a marker whose angle brackets it dropped.
	answer := `{"output":"` + placeholders["custom_org"] + ` and ` +
		strings.Trim(placeholders["custom_agency"], "<>") + ` are noted"}`
	restored := string(RestorePlaceholders([]byte(answer), result.Redactions))
	if !strings.Contains(restored, "ACME-20481 and") {
		t.Fatalf("restored answer = %s", restored)
	}
	text, count, _ := NewTextRestorer(result.Redactions, ValuePlain).Restore(
		strings.Trim(placeholders["custom_agency"], "<>")+" replied", true)
	if text != "Bureau of Records replied" || count != 1 {
		t.Fatalf("bare marker restore = %q (%d)", text, count)
	}

	// The redacted history sent back on the next turn is not redacted again.
	var document map[string]any
	if err := json.Unmarshal(result.Body, &document); err != nil {
		t.Fatal(err)
	}
	if !isTokenPlaceholderSpan(placeholders["custom_org"], 1, len(placeholders["custom_org"])-1) {
		t.Fatalf("custom marker body is not recognized as a placeholder")
	}
	if isTokenPlaceholderSpan("<PRIVATE_CUSTOM_Org_0123456789abcdef>", 1, 36) {
		t.Fatal("a marker no custom kind can produce must not pass as a placeholder")
	}

	if summary := WarningSummary(result.Findings); !strings.Contains(summary, "custom_agency=1") ||
		!strings.Contains(summary, "custom_org=2") || !strings.HasPrefix(summary, "account=1") {
		t.Fatalf("warning summary = %q", summary)
	}
}

func TestEngineHonorsCustomKindSwitch(t *testing.T) {
	const body = `{"model":"safe","input":"ACME-20481 and Bureau of Records"}`
	engine := newTestEngine(t, nil)
	result, err := engine.Inspect(context.Background(), customKindPolicy(t, false), contract.ProtocolOpenAIResponses, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result.Body), "<PRIVATE_CUSTOM_ORG_") ||
		!strings.Contains(string(result.Body), "ACME-20481") {
		t.Fatalf("a disabled custom kind was redacted: %s", result.Body)
	}
	if len(result.SuppressedFindings) != 1 ||
		result.SuppressedFindings[0].Kind != "custom_org" ||
		result.SuppressedFindings[0].Suppression != SuppressionKindDisabled {
		t.Fatalf("suppressed = %+v", result.SuppressedFindings)
	}
	if len(result.Findings) != 1 || result.Findings[0].Kind != "custom_agency" {
		t.Fatalf("findings = %+v", result.Findings)
	}
}

func TestCustomKindYieldsToBuiltinKindOnOverlap(t *testing.T) {
	detector, err := NewCustomRegexDetector([]contract.PolicyRegexRule{
		{Kind: "custom_org", Pattern: `ACME sk-[A-Za-z0-9]+`},
		{Kind: "common_secret", Pattern: `sk-[A-Za-z0-9]{20,}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	findings, err := detector.Detect(context.Background(), DetectInput{Segments: []Segment{{
		Value: "ACME sk-" + strings.Repeat("a1B2", 6),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Kind != KindCommonSecret {
		t.Fatalf("findings = %+v, want the credential to stay a secret", findings)
	}
}
