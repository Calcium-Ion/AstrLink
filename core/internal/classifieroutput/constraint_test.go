package classifieroutput

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestPlanDefaultsToJSONOnly(t *testing.T) {
	protocols := []contract.ProtocolID{
		contract.ProtocolOpenAIChat,
		contract.ProtocolOpenAIResponses,
		contract.ProtocolAnthropicMessages,
		contract.ProtocolGoogleGenerateContent,
		contract.ProtocolOpenAICompletions,
	}
	for _, protocol := range protocols {
		constraint, err := Plan(protocol, nil, defaultLabels)
		if err != nil {
			t.Fatalf("Plan(%s): %v", protocol, err)
		}
		if constraint.Mode() != ConstraintJSONOnly {
			t.Errorf("Plan(%s) with no verified capability = %s, want json_only", protocol, constraint.Mode())
		}
		if constraint.ChatResponseFormat() != nil || constraint.ResponsesTextFormat() != nil {
			t.Errorf("Plan(%s) JSON-only mode attached a native constraint", protocol)
		}
	}
	// Unverified and unsupported are treated identically: no probing, no
	// guessing, JSON-only either way.
	capabilities := CapabilitySet{
		contract.ProtocolOpenAIChat:      NativeUnverified,
		contract.ProtocolOpenAIResponses: NativeUnsupported,
	}
	for _, protocol := range []contract.ProtocolID{contract.ProtocolOpenAIChat, contract.ProtocolOpenAIResponses} {
		constraint, err := Plan(protocol, capabilities, defaultLabels)
		if err != nil {
			t.Fatalf("Plan(%s): %v", protocol, err)
		}
		if constraint.Mode() != ConstraintJSONOnly {
			t.Errorf("Plan(%s) unverified/unsupported = %s, want json_only", protocol, constraint.Mode())
		}
	}
}

func TestPlanNativeOnlyForVerifiedProtocolsWithConstructors(t *testing.T) {
	capabilities := CapabilitySet{
		contract.ProtocolOpenAIChat:      NativeVerified,
		contract.ProtocolOpenAIResponses: NativeVerified,
		// Verified but no native constructor exists: the plan stays JSON-only
		// instead of attempting an unimplemented native shape.
		contract.ProtocolAnthropicMessages: NativeVerified,
	}
	chat, err := Plan(contract.ProtocolOpenAIChat, capabilities, defaultLabels)
	if err != nil {
		t.Fatal(err)
	}
	if chat.Mode() != ConstraintChatJSONSchema || chat.ChatResponseFormat() == nil {
		t.Fatalf("verified chat plan = %s, response_format=%v", chat.Mode(), chat.ChatResponseFormat())
	}
	responses, err := Plan(contract.ProtocolOpenAIResponses, capabilities, defaultLabels)
	if err != nil {
		t.Fatal(err)
	}
	if responses.Mode() != ConstraintResponsesTextFormat || responses.ResponsesTextFormat() == nil {
		t.Fatalf("verified responses plan = %s, text.format=%v", responses.Mode(), responses.ResponsesTextFormat())
	}
	if _, err := Plan(contract.ProtocolAnthropicMessages, capabilities, defaultLabels); err != nil {
		t.Fatal(err)
	}
	anthropic, err := Plan(contract.ProtocolAnthropicMessages, capabilities, defaultLabels)
	if err != nil {
		t.Fatal(err)
	}
	if anthropic.Mode() != ConstraintJSONOnly {
		t.Errorf("verified protocol without a constructor = %s, want json_only", anthropic.Mode())
	}
}

func TestPlanRejectsEmptyCatalog(t *testing.T) {
	if _, err := Plan(contract.ProtocolOpenAIChat, nil, nil); err == nil {
		t.Error("empty label catalog accepted")
	}
	if _, err := Plan(contract.ProtocolOpenAIChat, nil, []string{"general", ""}); err == nil {
		t.Error("empty label entry accepted")
	}
}

func TestSystemPromptCarriesSchemaWithoutGatewayIdentity(t *testing.T) {
	constraint, err := Plan(contract.ProtocolOpenAIChat, nil, defaultLabels)
	if err != nil {
		t.Fatal(err)
	}
	prompt := constraint.SystemPrompt()
	for _, label := range defaultLabels {
		if !strings.Contains(prompt, label) {
			t.Errorf("prompt schema omits label %q", label)
		}
	}
	// Every answer must carry its score: a label without one reads as 0 and
	// can never clear the confidence threshold.
	if !strings.Contains(prompt, `"required":["label","score"]`) {
		t.Error("prompt schema does not require a label and a score")
	}
	if strings.Contains(strings.ToLower(prompt), "astrlink") {
		t.Error("system prompt leaks the gateway identity")
	}
	// The user text must never be part of the prompt; the caller sends it as
	// its own message. The prompt is fully determined by the catalog.
	if strings.Contains(prompt, "%s") || strings.Contains(prompt, "{}") {
		t.Error("prompt still contains an unfilled placeholder")
	}
}

func TestNativeConstraintsMatchLocalValidation(t *testing.T) {
	capabilities := CapabilitySet{contract.ProtocolOpenAIChat: NativeVerified}
	constraint, err := Plan(contract.ProtocolOpenAIChat, capabilities, defaultLabels)
	if err != nil {
		t.Fatal(err)
	}
	var responseFormat struct {
		Type       string `json:"type"`
		JSONSchema struct {
			Name   string          `json:"name"`
			Strict bool            `json:"strict"`
			Schema json.RawMessage `json:"schema"`
		} `json:"json_schema"`
	}
	if err := json.Unmarshal(constraint.ChatResponseFormat(), &responseFormat); err != nil {
		t.Fatalf("chat response_format is not JSON: %v", err)
	}
	if responseFormat.Type != "json_schema" || responseFormat.JSONSchema.Name == "" || !responseFormat.JSONSchema.Strict {
		t.Fatalf("unexpected response_format shape: %s", constraint.ChatResponseFormat())
	}
	// A reply honoring the strict schema must satisfy layer-2/3 validation:
	// every key required, so ParseTaskType accepts it.
	strictReply := `{"label":"coding","score":0.9,"candidates":[{"label":"research","score":0.3}]}`
	if _, err := ParseTaskType(strictReply, defaultLabels); err != nil {
		t.Errorf("strict-schema reply rejected by local validation: %v", err)
	}
	// And the prompt schema must not promise anything local validation
	// rejects: an empty candidates array is legal output.
	if _, err := ParseTaskType(`{"label":"coding","score":0.9,"candidates":[]}`, defaultLabels); err != nil {
		t.Errorf("empty candidates reply rejected: %v", err)
	}
	responses, err := Plan(contract.ProtocolOpenAIResponses, CapabilitySet{contract.ProtocolOpenAIResponses: NativeVerified}, defaultLabels)
	if err != nil {
		t.Fatal(err)
	}
	var textFormat map[string]any
	if err := json.Unmarshal(responses.ResponsesTextFormat(), &textFormat); err != nil {
		t.Fatalf("responses text.format is not JSON: %v", err)
	}
	if textFormat["type"] != "json_schema" || textFormat["strict"] != true {
		t.Fatalf("unexpected text.format shape: %s", responses.ResponsesTextFormat())
	}
	// The two dialects are attached at different request members; assert they
	// are not byte-identical documents to guard against sending one shape to
	// every protocol.
	if string(responses.ResponsesTextFormat()) == string(constraint.ChatResponseFormat()) {
		t.Error("chat and responses constraints are the same document")
	}
}
