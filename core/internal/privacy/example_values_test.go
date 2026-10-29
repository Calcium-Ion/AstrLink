package privacy

import (
	"context"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

// A user who turns on more kinds than the default still keeps the example
// addresses and test cards that fill code and docs.
func TestDocumentationValuesStayWhenMoreKindsAreOn(t *testing.T) {
	stored := contract.DefaultPrivacyPolicy()
	for index := range stored.KindRules {
		stored.KindRules[index].Enabled = true
	}
	policy, err := FromContractPolicy(stored)
	if err != nil {
		t.Fatal(err)
	}
	engine := newTestEngine(t, nil)
	const body = `{"model":"gpt-5","input":"mail alice@example.com, bob@corp.test, eve@x.invalid and carol@acme.io. ` +
		`Cards 4242 4242 4242 4242, 4000-0566-5566-5556 and 4539 1488 0343 6467. ` +
		`Docs https://api.example.org/v1 and https://acme.io/private"}`
	result, err := engine.Inspect(context.Background(), policy, contract.ProtocolOpenAIResponses, []byte(body))
	if err != nil || result.Decision != DecisionRedact {
		t.Fatalf("result = %#v, %v", result, err)
	}
	redacted := string(result.Body)
	for _, kept := range []string{
		"alice@example.com", "bob@corp.test", "eve@x.invalid",
		"4242 4242 4242 4242", "4000-0566-5566-5556", "https://api.example.org/v1",
	} {
		if !strings.Contains(redacted, kept) {
			t.Errorf("%q was replaced: %s", kept, redacted)
		}
	}
	for _, replaced := range []string{"carol@acme.io", "4539 1488 0343 6467", "https://acme.io/private"} {
		if strings.Contains(redacted, replaced) {
			t.Errorf("%q reached the provider: %s", replaced, redacted)
		}
	}
	reasons := map[SuppressionReason]int{}
	for _, finding := range result.SuppressedFindings {
		reasons[finding.Suppression]++
	}
	// The .invalid address reads as a natural placeholder, which also stays.
	if reasons[SuppressionExampleValue] != 2 || reasons[SuppressionAllowlisted] != 3 {
		t.Fatalf("suppressed = %#v", result.SuppressedFindings)
	}
}

func TestOnlyPublishedTestCardsAreExampleValues(t *testing.T) {
	for value, want := range map[string]bool{
		"4242 4242 4242 4242": true,
		"4242-4242-4242-4242": true,
		"4111111111111111":    true,
		"3782 822463 10005":   true,
		"4539 1488 0343 6467": false,
		"5425 2334 3010 9903": false,
	} {
		if got := isExampleValue(KindPaymentCard, value); got != want {
			t.Errorf("isExampleValue(%q) = %v, want %v", value, got, want)
		}
	}
	if isExampleValue(KindAccount, "4242424242424242") {
		t.Error("an account number matched the card list")
	}
}
