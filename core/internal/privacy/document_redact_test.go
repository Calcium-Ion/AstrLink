package privacy

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestAssignPlaceholdersUsesRequestScopedRandomSuffixes(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"a alice@example.com b +1-415-555-0001 c +1-415-555-0002 d alice@example.com"}]}`)
	document, extracted, err := extractDocument(contract.ProtocolOpenAIChat, body)
	if err != nil || len(extracted) == 0 {
		t.Fatalf("extract: %#v %v", extracted, err)
	}
	engine := mustTestEngine(t)
	entropy := make([]byte, 3*placeholderRandomBytes)
	for index := range entropy {
		entropy[index] = byte(index)
	}
	engine.placeholderEntropy = bytes.NewReader(entropy)
	result, err := engine.Inspect(t.Context(), Policy{
		Enabled: true, Mode: ModeRegex, Action: ActionRedact,
	}, contract.ProtocolOpenAIChat, body)
	if err != nil || result.Decision != DecisionRedact {
		t.Fatalf("inspect: %#v %v", result, err)
	}
	redacted := string(result.Body)
	const emailPlaceholder = "<PRIVATE_EMAIL_0001020304050607>"
	const firstPhonePlaceholder = "<PRIVATE_PHONE_08090a0b0c0d0e0f>"
	const secondPhonePlaceholder = "<PRIVATE_PHONE_1011121314151617>"
	if !strings.Contains(redacted, emailPlaceholder) ||
		strings.Count(redacted, emailPlaceholder) != 2 ||
		strings.Contains(redacted, "<PRIVATE_EMAIL>") {
		t.Fatalf("email placeholders = %s", redacted)
	}
	if !strings.Contains(redacted, firstPhonePlaceholder) ||
		!strings.Contains(redacted, secondPhonePlaceholder) ||
		strings.Contains(redacted, "<PRIVATE_PHONE>") {
		t.Fatalf("phone placeholders = %s", redacted)
	}
	if len(result.Redactions) != 3 {
		t.Fatalf("redactions=%#v", result.Redactions)
	}
	_ = document
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
		newPlaceholderAllocator(bytes.NewReader(make([]byte, placeholderRandomBytes))),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(placed) != 1 || placed[0].Kind != KindCommonSecret ||
		placed[0].Placeholder != "<SECRET_0000000000000000>" {
		t.Fatalf("placed=%#v", placed)
	}
	for _, item := range redactions {
		if item.Kind == KindEmail || strings.Contains(item.Placeholder, "EMAIL") {
			t.Fatalf("discarded emails must not assign placeholders: %#v", redactions)
		}
	}
}

func TestPlaceholderAllocatorUsesRandomSuffixForEveryKind(t *testing.T) {
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
	entropy := make([]byte, len(kinds)*placeholderRandomBytes)
	for index := range entropy {
		entropy[index] = byte(index)
	}
	allocator := newPlaceholderAllocator(bytes.NewReader(entropy))
	seen := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		placeholder, err := allocator.allocate(kind)
		if err != nil {
			t.Fatalf("allocate %q: %v", kind, err)
		}
		base := replacementFor(kind)
		prefix := base[:len(base)-1] + "_"
		if !strings.HasPrefix(placeholder, prefix) || !strings.HasSuffix(placeholder, ">") {
			t.Fatalf("placeholder %q does not preserve %q", placeholder, base)
		}
		suffix := strings.TrimSuffix(strings.TrimPrefix(placeholder, prefix), ">")
		if len(suffix) != placeholderRandomBytes*2 || suffix != strings.ToLower(suffix) {
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

func TestPlaceholderAllocatorAndEngineFailClosedOnEntropyProblems(t *testing.T) {
	allocator := newPlaceholderAllocator(failingPlaceholderEntropy{})
	if _, err := allocator.allocate(KindEmail); !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("entropy failure error = %v", err)
	}

	collisionEntropy := bytes.Repeat(
		[]byte{0x42},
		placeholderRandomBytes*(placeholderAllocationTrials+1),
	)
	allocator = newPlaceholderAllocator(bytes.NewReader(collisionEntropy))
	if _, err := allocator.allocate(KindEmail); err != nil {
		t.Fatalf("initial allocation: %v", err)
	}
	if _, err := allocator.allocate(KindEmail); !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("collision exhaustion error = %v", err)
	}

	engine := mustTestEngine(t)
	engine.placeholderEntropy = failingPlaceholderEntropy{}
	result, err := engine.Inspect(t.Context(), Policy{
		Enabled: true, Mode: ModeRegex, Action: ActionRedact,
	}, contract.ProtocolOpenAIResponses, []byte(`{"input":"alice@example.com"}`))
	if !errors.Is(err, ErrUnsafeRewrite) || result.Decision != DecisionBlock {
		t.Fatalf("entropy failure result=%#v error=%v", result, err)
	}
}

func TestRandomPlaceholdersRestoreThroughExistingMapping(t *testing.T) {
	engine := mustTestEngine(t)
	engine.placeholderEntropy = bytes.NewReader([]byte{0, 1, 2, 3, 4, 5, 6, 7})
	result, err := engine.Inspect(t.Context(), Policy{
		Enabled: true, Mode: ModeRegex, Action: ActionRedact,
	}, contract.ProtocolOpenAIResponses, []byte(`{"input":"alice@example.com"}`))
	if err != nil || len(result.Redactions) != 1 {
		t.Fatalf("inspect result=%#v error=%v", result, err)
	}
	response := []byte(`{"echo":"` + result.Redactions[0].Placeholder + `"}`)
	if got, want := string(RestorePlaceholders(response, result.Redactions)), `{"echo":"alice@example.com"}`; got != want {
		t.Fatalf("restored response=%q want=%q", got, want)
	}
}

func TestEngineUsesFreshPlaceholderAllocatorForEachInspection(t *testing.T) {
	engine := mustTestEngine(t)
	entropy := make([]byte, 2*placeholderRandomBytes)
	for index := range entropy {
		entropy[index] = byte(index)
	}
	engine.placeholderEntropy = bytes.NewReader(entropy)
	policy := Policy{Enabled: true, Mode: ModeRegex, Action: ActionRedact}
	body := []byte(`{"input":"alice@example.com"}`)
	first, err := engine.Inspect(t.Context(), policy, contract.ProtocolOpenAIResponses, body)
	if err != nil || len(first.Redactions) != 1 {
		t.Fatalf("first result=%#v error=%v", first, err)
	}
	second, err := engine.Inspect(t.Context(), policy, contract.ProtocolOpenAIResponses, body)
	if err != nil || len(second.Redactions) != 1 {
		t.Fatalf("second result=%#v error=%v", second, err)
	}
	if first.Redactions[0].Placeholder == second.Redactions[0].Placeholder {
		t.Fatalf("placeholder reused across inspections: %q", first.Redactions[0].Placeholder)
	}
}

type failingPlaceholderEntropy struct{}

func (failingPlaceholderEntropy) Read([]byte) (int, error) {
	return 0, errors.New("placeholder entropy unavailable")
}

func mustTestEngine(t *testing.T) *Engine {
	t.Helper()
	engine, err := New(PolicyProviderFunc(func(context.Context, Scope) (Policy, error) {
		return Policy{}, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}
