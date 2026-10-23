package privacy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestRegexDetectorFindsHighConfidenceKinds(t *testing.T) {
	tests := []struct {
		name  string
		value string
		kind  Kind
	}{
		{name: "email", value: "contact alice@example.com now", kind: KindEmail},
		{name: "international phone", value: "call +65 6123 4567 today", kind: KindPhone},
		{name: "formatted phone", value: "call (415) 555-2671", kind: KindPhone},
		{name: "account context", value: "account number: 12345678901", kind: KindAccount},
		{name: "iban", value: "wire to GB82WEST12345698765432", kind: KindAccount},
		{name: "payment card", value: "card 4242 4242 4242 4242", kind: KindPaymentCard},
		{name: "ipv4", value: "host 192.168.10.4", kind: KindIPAddress},
		{name: "ipv6", value: "host 2001:db8::1", kind: KindIPAddress},
		{name: "compressed ipv6", value: "host ::1", kind: KindIPAddress},
		{name: "url", value: "open https://private.example/path?q=1", kind: KindURL},
		{name: "provider secret", value: "key sk-proj-abcdefghijklmnopqrstuvwxyz123456", kind: KindCommonSecret},
		{name: "assigned secret", value: "api_key=abcdefghijklmnop123456", kind: KindCommonSecret},
	}

	detector := NewRegexDetector()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings, err := detector.Detect(context.Background(), DetectInput{
				Segments: []Segment{{Path: "/input", Value: test.value}},
			})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, finding := range findings {
				if finding.Kind == test.kind {
					found = true
					if finding.Start < 0 || finding.End > len(test.value) || finding.Start >= finding.End {
						t.Fatalf("invalid finding = %#v", finding)
					}
				}
			}
			if !found {
				t.Fatalf("findings = %#v, want kind %q", findings, test.kind)
			}
		})
	}
}

func TestRegexDetectorRejectsLowConfidenceNumericFalsePositives(t *testing.T) {
	value := "ids 1234567890, card 4242 4242 4242 4241, ip 999.999.1.1; code std::vector foo::bar abc::def x2001:db8::1y"
	findings, err := NewRegexDetector().Detect(context.Background(), DetectInput{
		Segments: []Segment{{Path: "/prompt", Value: value}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("low-confidence findings = %#v", findings)
	}
}

func TestRegexDetectorPreservesPackageVersionsAndSVGCoordinates(t *testing.T) {
	// Request audits showed package specifiers rewritten as email addresses and
	// these SVG coordinate runs rewritten as cards because they pass Luhn.
	for _, value := range []string{
		`npx -y agoragentic-mcp@1.3.6 --acp`,
		`npx -y @augmentcode/auggie@0.33.0 --acp`,
		`npx -y @qwen-code/qwen-code@0.20.1 --acp`,
		`bun add cline@3.0.46`,
		`npm install package@1.2.3-beta.1 other@v2.0.0`,
		`package@1.2.rc.1 package@1.2.beta-1`,
		`<path d="M0 0 c 175 105 101 38 184 51 328"/>`,
		`<path d="M0 0 c-18 1248 3 1319 4 1322 21 2"/>`,
		`<path d="M0 0 c 33 532 175 650 70 59 97 75"/>`,
		`duration=0.4242424242424242`,
		`id=4242424242424242.123`,
		`id=4242 4242 4242 4242 99`,
	} {
		t.Run(value, func(t *testing.T) {
			findings, err := NewRegexDetector().Detect(t.Context(), DetectInput{
				Segments: []Segment{{Value: value}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != 0 {
				t.Fatalf("code produced privacy findings: %#v", findings)
			}
		})
	}
}

func TestRegexDetectorKeepsRealEmailAndCardCoverage(t *testing.T) {
	for _, test := range []struct {
		value string
		kind  Kind
	}{
		{"alice+tag@team.example.com", KindEmail},
		{"ALICE@EXAMPLE.COM", KindEmail},
		{"alice@xn--bcher-kva.de", KindEmail},
		{"alice@example.xn--p1ai", KindEmail},
		{"4242424242424242", KindPaymentCard},
		{"4242 4242 4242 4242", KindPaymentCard},
		{"4242-4242-4242-4242", KindPaymentCard},
		{"3782 822463 10005", KindPaymentCard},
		{"3056 930902 5904", KindPaymentCard},
		{"4222 2222 2222 2", KindPaymentCard},
		{"4000 0000 0000 0000 006", KindPaymentCard},
	} {
		t.Run(test.value, func(t *testing.T) {
			value := "Value: " + test.value + "."
			findings, err := NewRegexDetector().Detect(t.Context(), DetectInput{
				Segments: []Segment{{Value: value}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != 1 || findings[0].Kind != test.kind ||
				value[findings[0].Start:findings[0].End] != test.value {
				t.Fatalf("findings = %#v, want complete %s span", findings, test.kind)
			}
		})
	}
}

func TestRegexDetectorDeterministicallyResolvesOverlapsAndOrder(t *testing.T) {
	input := DetectInput{Segments: []Segment{
		{Path: "/one", Value: "https://192.168.1.10/private alice@example.com"},
		{Path: "/two", Value: "4242-4242-4242-4242"},
	}}
	detector := NewRegexDetector()
	first, err := detector.Detect(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := detector.Detect(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("nondeterministic findings:\n%#v\n%#v", first, second)
	}
	if len(first) != 3 || first[0].Kind != KindURL ||
		first[1].Kind != KindEmail || first[2].Kind != KindPaymentCard {
		t.Fatalf("overlap selection = %#v", first)
	}
}

func TestRegexDetectorHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewRegexDetector().Detect(ctx, DetectInput{
		Segments: []Segment{{Value: "alice@example.com"}},
	}); err != context.Canceled {
		t.Fatalf("Detect error = %v", err)
	}
}

func TestRegexDetectorReportsEveryMatch(t *testing.T) {
	const count = 20_000
	findings, err := NewRegexDetector().Detect(context.Background(), DetectInput{
		Segments: []Segment{{Value: strings.Repeat("a@b.co ", count)}},
	})
	if err != nil || len(findings) != count {
		t.Fatalf("Detect found %d, err = %v", len(findings), err)
	}
}

func TestRegexRedactsEveryDistinctValueInALargeRequest(t *testing.T) {
	const count = 10_000
	var text strings.Builder
	for index := range count {
		fmt.Fprintf(&text, "user%d@example.com, ", index)
	}
	body, err := json.Marshal(map[string]any{
		"model": "m",
		"input": []any{map[string]any{"role": "user", "content": text.String()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(PolicyProviderFunc(func(context.Context, Scope) (Policy, error) {
		return Policy{}, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Inspect(context.Background(), Policy{
		Enabled: true, Mode: ModeRegex, Action: ActionRedact,
	}, contract.ProtocolOpenAIResponses, body)
	if err != nil || result.Decision != DecisionRedact || len(result.Redactions) != count {
		t.Fatalf("Inspect redactions = %d, decision = %q, err = %v", len(result.Redactions), result.Decision, err)
	}
	if bytes.Contains(result.Body, []byte("@example.com")) {
		t.Fatal("an address reached the rewritten body")
	}
}

func TestCustomRegexDetectorMatchesOnlyConfiguredRules(t *testing.T) {
	detector, err := NewCustomRegexDetector([]contract.PolicyRegexRule{{
		Kind:    "email",
		Pattern: `(?i)\bcustom-[a-z0-9]+@example\.com\b`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	findings, err := detector.Detect(context.Background(), DetectInput{
		Segments: []Segment{{Path: "/input", Value: "mail custom-user@example.com and alice@example.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Kind != KindEmail {
		t.Fatalf("findings = %#v", findings)
	}
	matched := "mail custom-user@example.com and alice@example.com"[findings[0].Start:findings[0].End]
	if matched != "custom-user@example.com" {
		t.Fatalf("matched = %q", matched)
	}
}

func TestBuiltinRegexRulesExportMatchesCatalog(t *testing.T) {
	rules := BuiltinRegexRules()
	if len(rules) == 0 {
		t.Fatal("expected builtin rules")
	}
	for index, rule := range rules {
		if err := rule.Validate(); err != nil {
			t.Fatalf("rule[%d]: %v", index, err)
		}
	}
}

// regexFindingTexts runs detector on text and returns each finding as
// kind:matched text, in span order.
func regexFindingTexts(t *testing.T, detector *RegexDetector, text string) []string {
	t.Helper()
	findings, err := detector.Detect(t.Context(), DetectInput{
		Segments: []Segment{{Path: "/input", Value: text}},
	})
	if err != nil {
		t.Fatal(err)
	}
	texts := make([]string, 0, len(findings))
	for _, finding := range findings {
		texts = append(texts, string(finding.Kind)+":"+text[finding.Start:finding.End])
	}
	return texts
}

func TestCustomRegexRulesReportTheValueGroupWhenTheyHaveOne(t *testing.T) {
	const text = "user=alice@example.com session=fict-session-0001"
	whole := []string{"common_secret:session=fict-session-0001"}
	value := []string{"common_secret:fict-session-0001"}
	for _, test := range []struct {
		name    string
		pattern string
		want    []string
	}{
		{"no group", `session=[a-z0-9-]+`, whole},
		{"unnamed group", `session=([a-z0-9-]+)`, whole},
		{"group with another name", `session=(?P<secret>[a-z0-9-]+)`, whole},
		{"value group", `session=(?P<value>[a-z0-9-]+)`, value},
		{"value group in the short syntax", `session=(?<value>[a-z0-9-]+)`, value},
		{
			"a value group in each alternative",
			`user=(?P<value>\S+)|session=(?P<value>\S+)`,
			[]string{"common_secret:alice@example.com", "common_secret:fict-session-0001"},
		},
		{"value group that took no part", `session=fict-(?P<value>x+)?session`, nil},
		{"empty value group", `session=(?P<value>z*)`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			rule := contract.PolicyRegexRule{Kind: string(KindCommonSecret), Pattern: test.pattern}
			if err := rule.Validate(); err != nil {
				t.Fatal(err)
			}
			detector, err := NewCustomRegexDetector([]contract.PolicyRegexRule{rule})
			if err != nil {
				t.Fatal(err)
			}
			if got := regexFindingTexts(t, detector, text); !slices.Equal(got, test.want) {
				t.Fatalf("findings = %q, want %q", got, test.want)
			}
		})
	}
}

// Fictional local access tokens ending in each character after which \b does
// not hold. testLocalToken (astrlink_identifier_test.go) ends in a digit.
const (
	testDashLocalToken       = "astr_Fx9qFx9qFx9qFx9qFx9qFx9qFx9qFx9qFx9qFx9qZ0-"
	testUnderscoreLocalToken = "astr_Fx9qFx9qFx9qFx9qFx9qFx9qFx9qFx9qFx9qFx9qZ0_"
)

func TestRegexDetectorFindsAstrLinkLocalAccessTokens(t *testing.T) {
	detector := NewRegexDetector()
	for _, token := range []string{testLocalToken, testDashLocalToken, testUnderscoreLocalToken} {
		if len(token) != len("astr_")+43 {
			t.Fatalf("fixture %q is not astr_ and 43 characters", token)
		}
		for _, form := range []string{
			"%s",
			"my AstrLink token is %s, keep it safe",
			"(%s)",
			"token=%s",
			"TOKEN=%s\n",
			"ASTRLINK_TOKEN=%s\n",
			"access_token: %s",
			"Authorization: Bearer %s",
			"curl -s -H 'authorization: bearer %s'",
			`{"token": "%s"}`,
			`{"headers":{"Authorization":"Bearer %s"}}`,
			"astrlink:\n  token: %s\n",
			"https://gateway.example/v1/records?token=%s&limit=5",
		} {
			text := fmt.Sprintf(form, token)
			t.Run(text, func(t *testing.T) {
				got := regexFindingTexts(t, detector, text)
				if want := []string{"common_secret:" + token}; !slices.Equal(got, want) {
					t.Fatalf("findings = %q, want %q", got, want)
				}
			})
		}
	}
}

func TestRegexDetectorIgnoresAstrLinkTokenShapesInsideLongerRuns(t *testing.T) {
	short := "astr_" + strings.Repeat("Qx7", 14)
	for _, text := range []string{
		"blob QUJD" + testLocalToken + " end",
		"blob " + testLocalToken + "RA end",
		"blob " + testDashLocalToken + "Qx end",
		"blob " + testDashLocalToken + "- end",
		"blob " + testUnderscoreLocalToken + "_ end",
		"blob x-" + testLocalToken + " end",
		"blob _" + testLocalToken + " end",
		"blob " + short + " end",
		"blob " + short + "Qx end",
	} {
		t.Run(text, func(t *testing.T) {
			if got := regexFindingTexts(t, NewRegexDetector(), text); len(got) != 0 {
				t.Fatalf("findings = %q", got)
			}
		})
	}
}

func TestRegexDetectorReplacesOnlyTheAssignedValue(t *testing.T) {
	const (
		value = "fictional0Secret0value42"
		jwt   = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJmaWN0aW9uYWwifQ.c2lnbmF0dXJl"
	)
	tests := []struct {
		text string
		want string
	}{
		{"api_key=" + value, value},
		{"API-KEY: " + value, value},
		{"X-API-Key: " + value, value},
		{"apiKey = '" + value + "'", value},
		{"access_token=" + value, value},
		{"accessToken: " + value, value},
		{"auth_token=" + value, value},
		{"X-Auth-Token: " + value, value},
		{"refresh-token=" + value, value},
		{"refreshToken: " + value, value},
		{"token=" + value, value},
		{"TOKEN=" + value + "\n", value},
		{"secret: " + value, value},
		{`password = "` + value + `"`, value},
		{`{"password":"` + value + `"}`, value},
		{`{"token": "` + value + `", "max_tokens": 4096}`, value},
		{"service:\n  api_key: " + value + "\n", value},
		{"/v1/items?token=" + value + "&page=2", value},
		{"curl -H 'Authorization: Bearer " + value + "'", value},
		{"Authorization: bearer " + jwt, jwt},
		{"Authorization: Bearer YWxhZGRpbjpvcGVuc2VzYW1l==", "YWxhZGRpbjpvcGVuc2VzYW1l=="},
		// A value of digits only is still a credential after one of these
		// keys: counts use plural or compound keys such as max_tokens, which
		// never match, and do not reach twelve digits.
		{"token: 123456789012", "123456789012"},
		{`{"token": 123456789012}`, "123456789012"},
	}
	detector := NewRegexDetector()
	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			got := regexFindingTexts(t, detector, test.text)
			if want := []string{"common_secret:" + test.want}; !slices.Equal(got, want) {
				t.Fatalf("findings = %q, want %q", got, want)
			}
		})
	}
}

func TestRegexDetectorIgnoresTokenCountsAndShortValues(t *testing.T) {
	for _, text := range []string{
		"max_tokens=4096",
		"max_tokens: 123456789012345",
		`{"max_tokens": 4096, "max_completion_tokens": 8192}`,
		"prompt_tokens=123456789012 completion_tokens=123456789012",
		"token_count: 123456789012",
		"tokens: abcdefghijklmnop",
		"tokenizer=cl100k_base_tokenizer",
		"token=short-value",
		"password: hunter2",
		"Authorization: Bearer short-token",
		"bearer tokens travel in the Authorization header",
		"Authorization: Bearer $OPENAI_API_KEY",
		"Authorization: Bearer <token>",
		"Authorization: Bearer {{access_token}}",
	} {
		t.Run(text, func(t *testing.T) {
			if got := regexFindingTexts(t, NewRegexDetector(), text); len(got) != 0 {
				t.Fatalf("findings = %q", got)
			}
		})
	}
}

func TestRegexKeepsAstrLinkTokenIDsAndRedactsLocalAccessTokens(t *testing.T) {
	engine, err := New(PolicyProviderFunc(func(context.Context, Scope) (Policy, error) {
		return Policy{}, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"messages":[{"role":"user","content":"access_token=` + testAccessTokenID +
		` access_token=` + testLocalToken + `"}]}`
	result, err := engine.Inspect(context.Background(), Policy{
		Enabled: true, Mode: ModeRegex, Action: ActionRedact,
	}, contract.ProtocolOpenAIChat, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	redacted := string(result.Body)
	if result.Decision != DecisionRedact || strings.Contains(redacted, testLocalToken) {
		t.Fatalf("the local access token was not redacted: %q %s", result.Decision, redacted)
	}
	if !strings.Contains(redacted, "access_token="+testAccessTokenID+" access_token=") {
		t.Fatalf("the token id or a key name was redacted: %s", redacted)
	}
	if len(result.Redactions) != 1 || result.Redactions[0].Value != testLocalToken {
		t.Fatalf("redactions = %#v", result.Redactions)
	}
	if len(result.SuppressedFindings) != 1 ||
		result.SuppressedFindings[0].Suppression != SuppressionAstrLinkIdentifier {
		t.Fatalf("suppressed = %#v", result.SuppressedFindings)
	}
}

func TestBuiltinRegexRulesKeepValueGroupsInCustomMode(t *testing.T) {
	// The catalog seeds custom rules without the built-in boundary checks;
	// the value groups are part of the patterns and still apply.
	detector, err := NewCustomRegexDetector(BuiltinRegexRules())
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"token=" + testDashLocalToken,
		"Authorization: Bearer " + testUnderscoreLocalToken,
		`{"api_key": "fictional0Secret0value42"}`,
	} {
		got := regexFindingTexts(t, detector, text)
		if len(got) != 1 || strings.ContainsAny(got[0][len("common_secret:"):], `:="' `) {
			t.Fatalf("%q findings = %q, want the value only", text, got)
		}
	}
}
