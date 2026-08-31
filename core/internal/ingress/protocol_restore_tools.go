package ingress

import (
	"strconv"

	"github.com/QuantumNous/astrlink/core/contract"
)

// toolArgumentTextRefs collects the response fields the local agent harness will
// act on: tool and function call arguments.
//
// Restoring these is not a convenience. The tool call is executed on the
// operator's own machine, so the placeholder is exactly the wrong value to hand
// over: an agent asked to fetch a redacted URL reaches a reserved domain that
// cannot resolve, and an agent asked to write a redacted address into a file
// persists a stand-in that reads as genuine. Leaving these alone also hid the
// damage, because a placeholder that never appeared in visible prose was
// counted as "not restored" rather than as a value the agent had consumed.
func toolArgumentTextRefs(
	protocol contract.ProtocolID,
	root any,
	frame int,
) []visibleTextRef {
	object, ok := root.(map[string]any)
	if !ok {
		return nil
	}
	refs := make([]visibleTextRef, 0, 2)
	switch protocol {
	case contract.ProtocolOpenAIChat:
		appendChatToolArgumentRefs(&refs, object, frame)
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		appendResponsesToolArgumentRefs(&refs, object, frame)
	case contract.ProtocolAnthropicMessages:
		appendAnthropicToolArgumentRefs(&refs, object, frame)
	case contract.ProtocolGoogleGenerateContent:
		appendGeminiToolArgumentRefs(&refs, object, frame)
	}
	return refs
}

func appendChatToolArgumentRefs(
	refs *[]visibleTextRef,
	root map[string]any,
	frame int,
) {
	choices, _ := root["choices"].([]any)
	for position, value := range choices {
		choice, ok := value.(map[string]any)
		if !ok {
			continue
		}
		index := memberID(choice, "index", position)
		for _, field := range []string{"delta", "message"} {
			container, ok := choice[field].(map[string]any)
			if !ok {
				continue
			}
			prefix := "chat:" + field + ":" + index
			appendChatToolCallRefs(refs, container, prefix, frame)
			// function_call predates tool_calls but is still emitted by some
			// upstreams and reaches the harness the same way.
			if call, ok := container["function_call"].(map[string]any); ok {
				appendToolStringRef(refs, call, "arguments", prefix+":function_call", frame)
			}
		}
	}
}

func appendChatToolCallRefs(
	refs *[]visibleTextRef,
	container map[string]any,
	prefix string,
	frame int,
) {
	calls, _ := container["tool_calls"].([]any)
	for position, value := range calls {
		call, ok := value.(map[string]any)
		if !ok {
			continue
		}
		// A streamed tool call is identified by its index; the id only appears on
		// the opening chunk, so keying on it would split one call into two
		// channels and break a placeholder spanning them.
		channel := prefix + ":tool:" + memberID(call, "index", position)
		if function, ok := call["function"].(map[string]any); ok {
			appendToolStringRef(refs, function, "arguments", channel, frame)
		}
	}
}

func appendResponsesToolArgumentRefs(
	refs *[]visibleTextRef,
	root map[string]any,
	frame int,
) {
	eventType, _ := root["type"].(string)
	itemKey := responseEventItemKey(root)
	switch eventType {
	case "response.function_call_arguments.delta",
		"response.mcp_call_arguments.delta",
		"response.custom_tool_call_input.delta":
		appendToolStringRef(refs, root, "delta", "responses:tool_args:"+itemKey, frame)
	case "response.function_call_arguments.done",
		"response.mcp_call_arguments.done":
		appendToolStringRef(
			refs, root, "arguments",
			"responses:snapshot:tool_args:"+itemKey, frame,
		)
	case "response.custom_tool_call_input.done":
		appendToolStringRef(
			refs, root, "input",
			"responses:snapshot:tool_input:"+itemKey, frame,
		)
	case "response.output_item.added", "response.output_item.done":
		if item, ok := root["item"].(map[string]any); ok {
			appendResponsesToolItemRefs(refs, item, "responses:snapshot:item:"+itemKey, frame)
		}
	case "response.completed", "response.incomplete", "response.failed":
		if response, ok := root["response"].(map[string]any); ok {
			appendResponsesOutputToolRefs(refs, response, "responses:snapshot:response", frame)
		}
	default:
		appendResponsesOutputToolRefs(refs, root, "responses:response", frame)
	}
}

func appendResponsesOutputToolRefs(
	refs *[]visibleTextRef,
	response map[string]any,
	prefix string,
	frame int,
) {
	output, _ := response["output"].([]any)
	for position, value := range output {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		itemID := memberString(item, "id", strconv.Itoa(position))
		appendResponsesToolItemRefs(refs, item, prefix+":item:"+itemID, frame)
	}
}

func appendResponsesToolItemRefs(
	refs *[]visibleTextRef,
	item map[string]any,
	prefix string,
	frame int,
) {
	switch itemType, _ := item["type"].(string); itemType {
	case "function_call", "mcp_call":
		appendToolStringRef(refs, item, "arguments", prefix+":arguments", frame)
	case "custom_tool_call":
		appendToolStringRef(refs, item, "input", prefix+":input", frame)
	}
}

func appendAnthropicToolArgumentRefs(
	refs *[]visibleTextRef,
	root map[string]any,
	frame int,
) {
	eventType, _ := root["type"].(string)
	index := memberID(root, "index", 0)
	switch eventType {
	case "content_block_delta":
		delta, _ := root["delta"].(map[string]any)
		if deltaType, _ := delta["type"].(string); deltaType == "input_json_delta" {
			appendToolStringRef(
				refs, delta, "partial_json",
				"anthropic:tool_input:"+index, frame,
			)
		}
	case "content_block_start":
		block, _ := root["content_block"].(map[string]any)
		appendAnthropicToolBlockRefs(refs, block, "anthropic:snapshot:tool:"+index, frame)
	case "message_start":
		if message, ok := root["message"].(map[string]any); ok {
			appendAnthropicToolContentRefs(refs, message, "anthropic:snapshot:message", frame)
		}
	default:
		appendAnthropicToolContentRefs(refs, root, "anthropic:message", frame)
	}
}

func appendAnthropicToolContentRefs(
	refs *[]visibleTextRef,
	root map[string]any,
	prefix string,
	frame int,
) {
	content, _ := root["content"].([]any)
	for position, value := range content {
		block, ok := value.(map[string]any)
		if !ok {
			continue
		}
		appendAnthropicToolBlockRefs(refs, block, prefix+":"+strconv.Itoa(position), frame)
	}
}

// appendAnthropicToolBlockRefs handles a completed tool_use block, whose input
// is already a parsed object rather than a serialized string.
func appendAnthropicToolBlockRefs(
	refs *[]visibleTextRef,
	block map[string]any,
	prefix string,
	frame int,
) {
	if blockType, _ := block["type"].(string); blockType != "tool_use" {
		return
	}
	input, present := block["input"]
	if !present {
		return
	}
	appendStructuredToolRefs(
		refs,
		input,
		func(replacement string) { block["input"] = replacement },
		prefix+":input",
		frame,
		0,
	)
}

func appendGeminiToolArgumentRefs(
	refs *[]visibleTextRef,
	root map[string]any,
	frame int,
) {
	candidates, _ := root["candidates"].([]any)
	for candidatePosition, value := range candidates {
		candidate, ok := value.(map[string]any)
		if !ok {
			continue
		}
		candidateID := memberID(candidate, "index", candidatePosition)
		content, _ := candidate["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for partPosition, partValue := range parts {
			part, ok := partValue.(map[string]any)
			if !ok {
				continue
			}
			call, ok := part["functionCall"].(map[string]any)
			if !ok {
				continue
			}
			args, present := call["args"]
			if !present {
				continue
			}
			appendStructuredToolRefs(
				refs,
				args,
				func(replacement string) { call["args"] = replacement },
				"gemini:tool:"+candidateID+":"+strconv.Itoa(partPosition)+":args",
				frame,
				0,
			)
		}
	}
}
