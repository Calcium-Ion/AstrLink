package privacy

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func testDerivationKey(seed byte) []byte {
	key := make([]byte, 32)
	for index := range key {
		key[index] = seed + byte(index)
	}
	return key
}

func tokenKindRule(Kind) KindRule {
	return KindRule{Enabled: true, Style: contract.PlaceholderStyleToken}
}

func TestAssignPlaceholdersReusesOnePlaceholderPerDistinctValue(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"a alice@example.com b +1-415-555-0001 c +1-415-555-0002 d alice@example.com"}]}`)
	engine := mustTestEngine(t)
	result, err := engine.Inspect(t.Context(), tokenPolicy(), contract.ProtocolOpenAIChat, body)
	if err != nil || result.Decision != DecisionRedact {
		t.Fatalf("inspect: %#v %v", result, err)
	}
	redacted := string(result.Body)
	if len(result.Redactions) != 3 {
		t.Fatalf("redactions=%#v", result.Redactions)
	}
	emails := 0
	phones := 0
	for _, redaction := range result.Redactions {
		switch redaction.Kind {
		case KindEmail:
			emails++
			// The same address occurs twice and must collapse onto one
			// placeholder, otherwise restoration would have to disambiguate
			// identical text.
			if strings.Count(redacted, redaction.Placeholder) != 2 {
				t.Fatalf("email placeholder count = %s", redacted)
			}
		case KindPhone:
			phones++
		}
	}
	if emails != 1 || phones != 2 {
		t.Fatalf("kind distribution emails=%d phones=%d", emails, phones)
	}
	if strings.Contains(redacted, "<PRIVATE_EMAIL>") ||
		strings.Contains(redacted, "<PRIVATE_PHONE>") {
		t.Fatalf("unsuffixed placeholders leaked: %s", redacted)
	}
}

func TestAssignPlaceholdersIgnoresOverlappingDiscardedFindings(t *testing.T) {
	value := "alice@example.com and bob@example.com"
	bobStart := strings.Index(value, "bob@example.com")
	findings := []Finding{
		{Segment: 0, Start: 0, End: len(value), Kind: KindCommonSecret},
		{Segment: 0, Start: 0, End: len("alice@example.com"), Kind: KindEmail},
		{Segment: 0, Start: bobStart, End: bobStart + len("bob@example.com"), Kind: KindEmail},
	}
	extracted := []extractedSegment{{
		Segment: Segment{Path: "/messages/0/content", Value: value},
		set:     func(string) {},
	}}
	placed, redactions, err := assignPlaceholders(
		extracted,
		findings,
		newPlaceholderAllocator(testDerivationKey(1), tokenKindRule, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(placed) != 1 || placed[0].Kind != KindCommonSecret ||
		!strings.HasPrefix(placed[0].Placeholder, "<SECRET_") {
		t.Fatalf("placed=%#v", placed)
	}
	for _, item := range redactions {
		if item.Kind == KindEmail || strings.Contains(item.Placeholder, "EMAIL") {
			t.Fatalf("discarded emails must not assign placeholders: %#v", redactions)
		}
	}
}

func TestPlaceholderAllocatorPreservesTokenShapeForEveryKind(t *testing.T) {
	kinds := []Kind{
		KindEmail,
		KindPhone,
		KindAccount,
		KindPaymentCard,
		KindIPAddress,
		KindURL,
		KindCommonSecret,
		KindAddress,
		KindDate,
		KindPerson,
	}
	allocator := newPlaceholderAllocator(testDerivationKey(2), tokenKindRule, nil)
	seen := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		placeholder, style, err := allocator.allocate(kind, "value-"+string(kind))
		if err != nil {
			t.Fatalf("allocate %q: %v", kind, err)
		}
		if style != contract.PlaceholderStyleToken {
			t.Fatalf("style for %q = %q", kind, style)
		}
		base := replacementFor(kind)
		prefix := base[:len(base)-1] + "_"
		if !strings.HasPrefix(placeholder, prefix) || !strings.HasSuffix(placeholder, ">") {
			t.Fatalf("placeholder %q does not preserve %q", placeholder, base)
		}
		suffix := strings.TrimSuffix(strings.TrimPrefix(placeholder, prefix), ">")
		if len(suffix) != placeholderTokenHexLength || suffix != strings.ToLower(suffix) {
			t.Fatalf("placeholder suffix = %q", suffix)
		}
		if _, err := hex.DecodeString(suffix); err != nil {
			t.Fatalf("placeholder suffix %q is not hexadecimal: %v", suffix, err)
		}
		if _, exists := seen[placeholder]; exists {
			t.Fatalf("duplicate placeholder %q", placeholder)
		}
		seen[placeholder] = struct{}{}
	}
}

func TestTokenPlaceholdersRestoreThroughExistingMapping(t *testing.T) {
	engine := mustTestEngine(t)
	result, err := engine.Inspect(
		t.Context(),
		tokenPolicy(),
		contract.ProtocolOpenAIResponses,
		[]byte(`{"input":"alice@example.com"}`),
	)
	if err != nil || len(result.Redactions) != 1 {
		t.Fatalf("inspect result=%#v error=%v", result, err)
	}
	response := []byte(`{"echo":"` + result.Redactions[0].Placeholder + `"}`)
	if got, want := string(RestorePlaceholders(response, result.Redactions)), `{"echo":"alice@example.com"}`; got != want {
		t.Fatalf("restored response=%q want=%q", got, want)
	}
}

// TestPlaceholdersAreStableAcrossInspections pins the behaviour that replaced
// per-request random suffixes. A client resends the whole conversation each
// turn, so a value that changed placeholder every turn left the model unable to
// tell that two markers meant the same thing, and invalidated the upstream
// prefix cache from the first redacted span onwards.
func TestPlaceholdersAreStableAcrossInspections(t *testing.T) {
	engine := mustTestEngine(t)
	policy := tokenPolicy()
	body := []byte(`{"input":"alice@example.com"}`)
	first, err := engine.Inspect(t.Context(), policy, contract.ProtocolOpenAIResponses, body)
	if err != nil || len(first.Redactions) != 1 {
		t.Fatalf("first result=%#v error=%v", first, err)
	}
	second, err := engine.Inspect(t.Context(), policy, contract.ProtocolOpenAIResponses, body)
	if err != nil || len(second.Redactions) != 1 {
		t.Fatalf("second result=%#v error=%v", second, err)
	}
	if first.Redactions[0].Placeholder != second.Redactions[0].Placeholder {
		t.Fatalf(
			"placeholder changed across inspections: %q then %q",
			first.Redactions[0].Placeholder,
			second.Redactions[0].Placeholder,
		)
	}
	if !bytesEqual(first.Body, second.Body) {
		t.Fatalf("redacted body is not byte-stable across inspections")
	}
}

// TestPlaceholdersDifferAcrossDerivationKeys shows the suffix is not a bare
// digest of the value: without the key, an email or an IP placeholder would be
// reversible by enumeration.
func TestPlaceholdersDifferAcrossDerivationKeys(t *testing.T) {
	body := []byte(`{"input":"alice@example.com"}`)
	first := mustTestEngine(t)
	first.derivationKey = testDerivationKey(3)
	second := mustTestEngine(t)
	second.derivationKey = testDerivationKey(9)
	left, err := first.Inspect(t.Context(), tokenPolicy(), contract.ProtocolOpenAIResponses, body)
	if err != nil || len(left.Redactions) != 1 {
		t.Fatalf("left result=%#v error=%v", left, err)
	}
	right, err := second.Inspect(t.Context(), tokenPolicy(), contract.ProtocolOpenAIResponses, body)
	if err != nil || len(right.Redactions) != 1 {
		t.Fatalf("right result=%#v error=%v", right, err)
	}
	if left.Redactions[0].Placeholder == right.Redactions[0].Placeholder {
		t.Fatalf("placeholder is independent of the derivation key")
	}
}

func bytesEqual(left, right []byte) bool {
	return string(left) == string(right)
}

func tokenPolicy() Policy {
	rules := make(map[Kind]KindRule, len(contract.PrivacyKinds()))
	for _, kind := range contract.PrivacyKinds() {
		rules[Kind(kind)] = KindRule{Enabled: true, Style: contract.PlaceholderStyleToken}
	}
	return Policy{
		Enabled:   true,
		Mode:      ModeRegex,
		Action:    ActionRedact,
		KindRules: rules,
	}
}

func naturalPolicy() Policy {
	rules := make(map[Kind]KindRule, len(contract.PrivacyKinds()))
	for _, kind := range contract.PrivacyKinds() {
		style := contract.PlaceholderStyleNatural
		if contract.PlaceholderStyleLocked(kind) {
			style = contract.PlaceholderStyleToken
		}
		rules[Kind(kind)] = KindRule{Enabled: true, Style: style}
	}
	return Policy{
		Enabled:   true,
		Mode:      ModeRegex,
		Action:    ActionRedact,
		KindRules: rules,
	}
}

func mustTestEngine(t *testing.T) *Engine {
	t.Helper()
	engine, err := New(PolicyProviderFunc(func(context.Context, Scope) (Policy, error) {
		return Policy{}, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	engine.derivationKey = testDerivationKey(0)
	return engine
}
