package privacy

import (
	"context"
	"reflect"
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

func TestRegexDetectorEnforcesFindingLimit(t *testing.T) {
	_, err := NewRegexDetector().Detect(context.Background(), DetectInput{
		Segments: []Segment{{Value: strings.Repeat("a@b.co ", maxDetectorFindings+1)}},
	})
	if err != ErrDetectorLimit {
		t.Fatalf("Detect error = %v", err)
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
