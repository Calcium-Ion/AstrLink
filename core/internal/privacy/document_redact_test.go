package privacy

import (
	"context"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestAssignPlaceholdersIndexesOnlyWhenKindHasMultipleDistinctValues(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"a alice@example.com b +1-415-555-0001 c +1-415-555-0002 d alice@example.com"}]}`)
	document, extracted, err := extractDocument(contract.ProtocolOpenAIChat, body)
	if err != nil || len(extracted) == 0 {
		t.Fatalf("extract: %#v %v", extracted, err)
	}
	engine := mustTestEngine(t)
	result, err := engine.Inspect(t.Context(), Policy{
		Enabled: true, Mode: ModeRegex, Action: ActionRedact,
	}, contract.ProtocolOpenAIChat, body)
	if err != nil || result.Decision != DecisionRedact {
		t.Fatalf("inspect: %#v %v", result, err)
	}
	redacted := string(result.Body)
	if !strings.Contains(redacted, "<PRIVATE_EMAIL>") ||
		strings.Contains(redacted, "<PRIVATE_EMAIL_0>") ||
		strings.Count(redacted, "<PRIVATE_EMAIL>") != 2 {
		t.Fatalf("email placeholders = %s", redacted)
	}
	if !strings.Contains(redacted, "<PRIVATE_PHONE_0>") ||
		!strings.Contains(redacted, "<PRIVATE_PHONE_1>") ||
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
	placed, redactions, err := assignPlaceholders(extracted, findings)
	if err != nil {
		t.Fatal(err)
	}
	if len(placed) != 1 || placed[0].Kind != KindCommonSecret || placed[0].Placeholder != "<SECRET>" {
		t.Fatalf("placed=%#v", placed)
	}
	for _, item := range redactions {
		if item.Kind == KindEmail || strings.Contains(item.Placeholder, "EMAIL") {
			t.Fatalf("discarded emails must not assign placeholders: %#v", redactions)
		}
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
	return engine
}
