package privacy

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
)

const maxToolContextPathBytes = 512

func isToolTextBlockField(parent map[string]any, key string) bool {
	if key != "text" && key != "type" {
		return false
	}
	switch parent["type"] {
	case "text", "input_text", "output_text":
		return true
	default:
		return false
	}
}

// toolFieldContextPrefix is the exact tool-field-path-v1 training/inference
// format. JSON-encoding the path prevents a key containing quotes/newlines from
// fabricating another field marker. Retain the most local path for deep inputs,
// without silently truncating the actual value that will be inspected.
func toolFieldContextPrefix(path string) string {
	if len(path) > maxToolContextPathBytes {
		start := len(path) - maxToolContextPathBytes
		for start < len(path) && !utf8.RuneStart(path[start]) {
			start++
		}
		path = "..." + path[start:]
	}
	encoded, _ := json.Marshal(path)
	return "Field: " + string(encoded) + "\nValue: "
}

// Only protocol tool containers establish context. The older generic walker
// intentionally inspects a wider range of data; an arbitrary object key called
// "arguments" must not opt user prose into a different model input contract.
func structuredToolPayloadPaths(protocol contract.ProtocolID, root map[string]any) map[string]struct{} {
	paths := make(map[string]struct{})
	add := func(path, key string, record map[string]any) {
		switch record[key].(type) {
		case map[string]any, []any:
			paths[path+"/"+key] = struct{}{}
		}
	}
	visitArray := func(value any, visit func(int, map[string]any)) {
		array, _ := value.([]any)
		for index, item := range array {
			if record, ok := item.(map[string]any); ok {
				visit(index, record)
			}
		}
	}
	switch protocol {
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		visitArray(root["input"], func(index int, item map[string]any) {
			path := "/input/" + jsonIndex(index)
			if role, exists := item["role"]; exists && role != "assistant" {
				return
			}
			switch item["type"] {
			case "function_call":
				add(path, "arguments", item)
			case "custom_tool_call":
				add(path, "input", item)
			case "function_call_output", "custom_tool_call_output":
				add(path, "output", item)
			}
		})
	case contract.ProtocolOpenAIChat:
		visitArray(root["messages"], func(index int, message map[string]any) {
			path := "/messages/" + jsonIndex(index)
			switch message["role"] {
			case "tool", "function":
				add(path, "content", message)
			case "assistant":
				function, _ := message["function_call"].(map[string]any)
				add(path+"/function_call", "arguments", function)
				visitArray(message["tool_calls"], func(index int, call map[string]any) {
					if call["type"] == "function" {
						function, _ := call["function"].(map[string]any)
						add(path+"/tool_calls/"+jsonIndex(index)+"/function", "arguments", function)
					}
				})
			}
		})
	case contract.ProtocolAnthropicMessages:
		visitArray(root["messages"], func(index int, message map[string]any) {
			path := "/messages/" + jsonIndex(index) + "/content/"
			visitArray(message["content"], func(index int, part map[string]any) {
				if message["role"] == "assistant" && (part["type"] == "tool_use" || part["type"] == "server_tool_use") {
					add(path+jsonIndex(index), "input", part)
				}
				if message["role"] == "user" && part["type"] == "tool_result" {
					add(path+jsonIndex(index), "content", part)
				}
			})
		})
	case contract.ProtocolGoogleGenerateContent:
		visitArray(root["contents"], func(index int, content map[string]any) {
			path := "/contents/" + jsonIndex(index) + "/parts/"
			visitArray(content["parts"], func(index int, part map[string]any) {
				if content["role"] == "model" {
					call, _ := part["functionCall"].(map[string]any)
					add(path+jsonIndex(index)+"/functionCall", "args", call)
				}
				if content["role"] == "user" {
					response, _ := part["functionResponse"].(map[string]any)
					add(path+jsonIndex(index)+"/functionResponse", "response", response)
				}
			})
		})
	}
	return paths
}
