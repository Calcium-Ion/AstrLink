package contract

import "testing"

func TestEligibleForRoutingAcceptsInstalledTiers(t *testing.T) {
	if !EligibleForRouting(AutoClassifierArtifactExperimental) {
		t.Fatal("an installed experimental artifact is routing-eligible")
	}
	if !EligibleForRouting(AutoClassifierArtifactReleased) {
		t.Fatal("released artifacts must be routing-eligible")
	}
	if EligibleForRouting("") {
		t.Fatal("empty tier is not routing-eligible")
	}
}

func TestAutoClassifierClassifyPreviewRequestRequiresOneInput(t *testing.T) {
	if (AutoClassifierClassifyPreviewRequest{}).Validate() == nil {
		t.Fatal("empty preview request must fail")
	}
	if (AutoClassifierClassifyPreviewRequest{
		Text: "hello",
		Body: []byte(`{"messages":[]}`),
	}).Validate() == nil {
		t.Fatal("text and body together must fail")
	}
	if err := (AutoClassifierClassifyPreviewRequest{Text: "hello"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (AutoClassifierClassifyPreviewRequest{
		Protocol: ProtocolOpenAIChat,
		Body:     []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
	}).Validate(); err != nil {
		t.Fatal(err)
	}
}
