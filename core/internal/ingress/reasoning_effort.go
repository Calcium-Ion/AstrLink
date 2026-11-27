package ingress

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// extractReasoningEffort reads only explicit strength controls from the fields
// already inspected for routing. Missing/malformed values never reject a call,
// and neither a model name nor a token budget implies a reasoning level.
func extractReasoningEffort(protocol contract.ProtocolID, fields map[string]json.RawMessage) *string {
	var path []string
	switch protocol {
	case contract.ProtocolOpenAIChat, contract.ProtocolOpenAICompletions:
		path = []string{"reasoning_effort"}
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		path = []string{"reasoning", "effort"}
	case contract.ProtocolAnthropicMessages:
		path = []string{"output_config", "effort"}
	case contract.ProtocolGoogleGenerateContent:
		path = []string{"generationConfig", "thinkingConfig", "thinkingLevel"}
	default:
		return nil
	}
	for _, key := range path[:len(path)-1] {
		var nested map[string]json.RawMessage
		if json.Unmarshal(fields[key], &nested) != nil || nested == nil {
			return nil
		}
		fields = nested
	}
	var value string
	if json.Unmarshal(fields[path[len(path)-1]], &value) != nil {
		return nil
	}
	return knownReasoningLevel(value)
}

// knownReasoningLevel keeps this metadata a known level, never arbitrary
// client text.
func knownReasoningLevel(value string) *string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
		return &value
	default:
		return nil
	}
}

// extractReasoningUpdate reads the level the latest configuration_update
// item in a Responses input set. Clients such as Pi change the level mid
// conversation this way and leave the top-level reasoning as it was, so the
// record shows both. It follows extractReasoningEffort's rules: a known level
// or nothing.
func extractReasoningUpdate(protocol contract.ProtocolID, fields map[string]json.RawMessage) *string {
	switch protocol {
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
	default:
		return nil
	}
	var items []struct {
		Type      string                     `json:"type"`
		Reasoning map[string]json.RawMessage `json:"reasoning"`
	}
	if json.Unmarshal(fields["input"], &items) != nil {
		return nil
	}
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Type != "configuration_update" || items[i].Reasoning == nil {
			continue
		}
		var value string
		if json.Unmarshal(items[i].Reasoning["effort"], &value) != nil {
			continue
		}
		if level := knownReasoningLevel(value); level != nil {
			return level
		}
	}
	return nil
}
