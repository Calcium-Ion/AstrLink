package ingress

import (
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/tidwall/gjson"
)

// Metadata, heartbeats, empty deltas and final usage envelopes are not tokens.
// Include reasoning and tool output so tool-only agent calls have a TTFT too.
func hasGeneratedOutput(protocol contract.ProtocolID, payload []byte) bool {
	doc := gjson.ParseBytes(payload)
	nonempty := func(value gjson.Result) bool { return value.Type == gjson.String && value.Str != "" }
	switch protocol {
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		switch doc.Get("type").Str {
		case "response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta", "response.function_call_arguments.delta", "response.custom_tool_call_input.delta", "response.refusal.delta":
			return nonempty(doc.Get("delta"))
		case "response.output_item.added":
			kind := doc.Get("item.type").Str
			return (kind == "function_call" || kind == "custom_tool_call") && nonempty(doc.Get("item.name"))
		}
	case contract.ProtocolOpenAIChat, contract.ProtocolOpenAICompletions:
		for _, choice := range doc.Get("choices").Array() {
			for _, path := range []string{"text", "delta.content", "delta.reasoning_content", "delta.reasoning", "delta.refusal", "delta.function_call.name", "delta.function_call.arguments"} {
				if nonempty(choice.Get(path)) {
					return true
				}
			}
			for _, call := range choice.Get("delta.tool_calls").Array() {
				if nonempty(call.Get("function.name")) || nonempty(call.Get("function.arguments")) {
					return true
				}
			}
		}
	case contract.ProtocolAnthropicMessages:
		switch doc.Get("type").Str {
		case "content_block_delta":
			for _, key := range []string{"text", "thinking", "partial_json"} {
				if nonempty(doc.Get("delta." + key)) {
					return true
				}
			}
		case "content_block_start":
			block := doc.Get("content_block")
			kind := block.Get("type").Str
			return nonempty(block.Get("text")) || nonempty(block.Get("thinking")) ||
				((kind == "tool_use" || kind == "server_tool_use" || kind == "mcp_tool_use") && nonempty(block.Get("name")))
		}
	case contract.ProtocolGoogleGenerateContent:
		for _, candidate := range doc.Get("candidates").Array() {
			for _, part := range candidate.Get("content.parts").Array() {
				if nonempty(part.Get("text")) || nonempty(part.Get("functionCall.name")) || nonempty(part.Get("inlineData.data")) {
					return true
				}
			}
		}
	}
	return false
}
