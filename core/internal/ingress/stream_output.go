package ingress

import (
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/tidwall/gjson"
)

// streamOutput classifies one decoded stream event by the generated content
// it carries. Metadata, heartbeats, empty deltas and final usage envelopes
// carry none.
type streamOutput uint8

const (
	streamOutputNone streamOutput = iota
	// streamOutputReasoning is thinking the provider exposes before the answer.
	streamOutputReasoning
	// streamOutputAnswer is content addressed to the caller: text, a refusal,
	// a tool call, or media. Tool-only agent calls answer too.
	streamOutputAnswer
)

// classifyStreamOutput reports the strongest content class in one event. An
// event mixing reasoning with an answer counts as the answer, because the
// model has stopped only thinking.
func classifyStreamOutput(protocol contract.ProtocolID, payload []byte) streamOutput {
	doc := gjson.ParseBytes(payload)
	nonempty := func(value gjson.Result) bool { return value.Type == gjson.String && value.Str != "" }
	kind := streamOutputNone
	note := func(class streamOutput, present bool) {
		if present && class > kind {
			kind = class
		}
	}
	switch protocol {
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		eventType := doc.Get("type").Str
		switch eventType {
		case "response.output_text.delta", "response.function_call_arguments.delta", "response.custom_tool_call_input.delta", "response.refusal.delta":
			note(streamOutputAnswer, nonempty(doc.Get("delta")))
		case "response.output_item.added":
			item := doc.Get("item.type").Str
			note(streamOutputAnswer, (item == "function_call" || item == "custom_tool_call") && nonempty(doc.Get("item.name")))
		default:
			// response.reasoning_text.delta, response.reasoning_summary_text.delta
			// and gateway variants such as response.reasoning.delta.
			if strings.HasPrefix(eventType, "response.reasoning") && strings.HasSuffix(eventType, ".delta") {
				note(streamOutputReasoning, nonempty(doc.Get("delta")))
			}
		}
	case contract.ProtocolOpenAIChat, contract.ProtocolOpenAICompletions:
		for _, choice := range doc.Get("choices").Array() {
			for _, path := range []string{"text", "delta.content", "delta.refusal", "delta.function_call.name", "delta.function_call.arguments"} {
				note(streamOutputAnswer, nonempty(choice.Get(path)))
			}
			for _, path := range []string{"delta.reasoning_content", "delta.reasoning"} {
				note(streamOutputReasoning, nonempty(choice.Get(path)))
			}
			for _, call := range choice.Get("delta.tool_calls").Array() {
				note(streamOutputAnswer, nonempty(call.Get("function.name")) || nonempty(call.Get("function.arguments")))
			}
		}
	case contract.ProtocolAnthropicMessages:
		switch doc.Get("type").Str {
		case "content_block_delta":
			note(streamOutputAnswer, nonempty(doc.Get("delta.text")) || nonempty(doc.Get("delta.partial_json")))
			note(streamOutputReasoning, nonempty(doc.Get("delta.thinking")))
		case "content_block_start":
			block := doc.Get("content_block")
			blockType := block.Get("type").Str
			note(streamOutputAnswer, nonempty(block.Get("text")) ||
				((blockType == "tool_use" || blockType == "server_tool_use" || blockType == "mcp_tool_use") && nonempty(block.Get("name"))))
			note(streamOutputReasoning, nonempty(block.Get("thinking")))
		}
	case contract.ProtocolGoogleGenerateContent:
		for _, candidate := range doc.Get("candidates").Array() {
			for _, part := range candidate.Get("content.parts").Array() {
				if nonempty(part.Get("text")) {
					if part.Get("thought").Bool() {
						note(streamOutputReasoning, true)
					} else {
						note(streamOutputAnswer, true)
					}
				}
				note(streamOutputAnswer, nonempty(part.Get("functionCall.name")) || nonempty(part.Get("inlineData.data")))
			}
		}
	}
	return kind
}
