package privacy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestWrapSampleTextBuildsInspectableBodies(t *testing.T) {
	sample := "reach me at alice@example.com"
	for _, protocol := range []contract.ProtocolID{
		contract.ProtocolOpenAIChat,
		contract.ProtocolAnthropicMessages,
		contract.ProtocolOpenAICompletions,
		contract.ProtocolOpenAIResponses,
		contract.ProtocolOpenAIResponsesCompact,
		contract.ProtocolGoogleGenerateContent,
	} {
		body, err := WrapSampleText(protocol, sample)
		if err != nil {
			t.Fatalf("%s: %v", protocol, err)
		}
		_, extracted, err := extractDocument(protocol, body)
		if err != nil || len(extracted) == 0 {
			t.Fatalf("%s extract=%#v err=%v body=%s", protocol, extracted, err, body)
		}
		found := false
		for _, segment := range extracted {
			if strings.Contains(segment.Value, sample) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s did not embed sample: %s", protocol, body)
		}
	}
}

func TestWrapSampleTextRejectsInvalidInput(t *testing.T) {
	if _, err := WrapSampleText(contract.ProtocolOpenAIModels, "x"); err == nil {
		t.Fatal("expected unsupported protocol")
	}
	if _, err := WrapSampleText(contract.ProtocolOpenAIChat, ""); err == nil {
		t.Fatal("expected empty sample rejection")
	}
	if _, err := WrapSampleText(contract.ProtocolOpenAIChat, strings.Repeat("a", contract.MaxPolicyDryRunSampleBytes+1)); err == nil {
		t.Fatal("expected oversized sample rejection")
	}
}

func TestLocateFindingsMapsPathsWithoutPlaintext(t *testing.T) {
	body, err := WrapSampleText(contract.ProtocolOpenAIChat, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	findings := []Finding{{
		Segment: 0, Start: 0, End: len("alice@example.com"),
		Kind: KindEmail, Confidence: 0.91,
	}}
	locations, err := LocateFindings(contract.ProtocolOpenAIChat, body, findings)
	if err != nil {
		t.Fatal(err)
	}
	if len(locations) != 1 || locations[0].Kind != "email" ||
		locations[0].Path == "" || locations[0].Confidence != 0.91 {
		t.Fatalf("locations=%#v", locations)
	}
	encoded, _ := json.Marshal(locations)
	if strings.Contains(string(encoded), "alice@example.com") {
		t.Fatalf("plaintext leaked: %s", encoded)
	}
}
