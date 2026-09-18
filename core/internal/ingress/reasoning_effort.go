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
	value = strings.ToLower(strings.TrimSpace(value))
	// Keep this metadata a known level, never arbitrary client text.
	switch value {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
		return &value
	default:
		return nil
	}
}
