package privacy

import "github.com/QuantumNous/astrlink/core/contract"

// continuationPaths identifies protocol-owned replay data before the generic
// string walker runs. It deliberately walks actual arrays and typed records,
// never field-name patterns or JSON embedded in user/tool text. Consequently a
// tool argument called "signature" or "encrypted_content" remains inspectable.
//
// These values are not credentials: providers authenticate/decrypt them to
// resume a previous turn. Excluding them before detection protects every policy
// mode (including block/warn), not just the redaction writer. New protocol
// carriers belong here with a source and request/response round-trip tests.
func continuationPaths(protocol contract.ProtocolID, root map[string]any) map[string]struct{} {
	paths := make(map[string]struct{})
	addStrings := func(path string, record map[string]any, keys ...string) {
		for _, key := range keys {
			if _, ok := record[key].(string); ok {
				paths[path+"/"+escapeJSONPointer(key)] = struct{}{}
			}
		}
	}
	protectThinking := func(path string, block map[string]any) {
		switch block["type"] {
		case "thinking":
			addStrings(path, block, "signature")
			// Anthropic requires the signed block complete and unmodified.
			// Unsigned reasoning text is still ordinary inspectable content.
			if signature, _ := block["signature"].(string); signature != "" {
				addStrings(path, block, "thinking")
			}
		case "redacted_thinking":
			addStrings(path, block, "data")
		}
	}
	arrayRecords := func(value any, visit func(int, map[string]any)) {
		items, _ := value.([]any)
		for index, item := range items {
			if record, ok := item.(map[string]any); ok {
				visit(index, record)
			}
		}
	}

	switch protocol {
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		arrayRecords(root["input"], func(index int, item map[string]any) {
			path := "/input/" + jsonIndex(index)
			// Responses reasoning/compaction items normally have no role.
			// A user-role object with a spoofed type is not a replay record.
			if role, exists := item["role"]; exists && role != "assistant" {
				return
			}
			switch item["type"] {
			case "reasoning", "compaction", "compaction_summary":
				addStrings(path, item, "encrypted_content")
			case "function_call_output", "custom_tool_call_output":
				arrayRecords(item["output"], func(index int, part map[string]any) {
					if part["type"] == "encrypted_content" {
						addStrings(path+"/output/"+jsonIndex(index), part, "encrypted_content")
					}
				})
			}
		})
	case contract.ProtocolAnthropicMessages, contract.ProtocolOpenAIChat:
		arrayRecords(root["messages"], func(index int, message map[string]any) {
			if message["role"] != "assistant" {
				return
			}
			path := "/messages/" + jsonIndex(index)
			arrayRecords(message["content"], func(index int, part map[string]any) {
				protectThinking(path+"/content/"+jsonIndex(index), part)
			})
			if protocol != contract.ProtocolOpenAIChat {
				return
			}
			// LiteLLM preserves Anthropic signed blocks in this extension.
			arrayRecords(message["thinking_blocks"], func(index int, part map[string]any) {
				protectThinking(path+"/thinking_blocks/"+jsonIndex(index), part)
			})
			// OpenRouter's documented Chat Completions reasoning extension.
			arrayRecords(message["reasoning_details"], func(index int, detail map[string]any) {
				detailPath := path + "/reasoning_details/" + jsonIndex(index)
				switch detail["type"] {
				case "reasoning.encrypted":
					addStrings(detailPath, detail, "data")
				case "reasoning.text":
					addStrings(detailPath, detail, "signature")
					if signature, _ := detail["signature"].(string); signature != "" {
						addStrings(detailPath, detail, "text")
					}
				}
			})
			// Google's OpenAI-compatible tool-call metadata; Vertex/LiteLLM
			// also carries the same signature in provider_specific_fields.
			arrayRecords(message["tool_calls"], func(index int, call map[string]any) {
				if call["type"] != "function" {
					return
				}
				callPath := path + "/tool_calls/" + jsonIndex(index)
				extra, _ := call["extra_content"].(map[string]any)
				google, _ := extra["google"].(map[string]any)
				addStrings(callPath+"/extra_content/google", google, "thought_signature")
				fields, _ := call["provider_specific_fields"].(map[string]any)
				addStrings(callPath+"/provider_specific_fields", fields, "thought_signature")
				function, _ := call["function"].(map[string]any)
				fields, _ = function["provider_specific_fields"].(map[string]any)
				addStrings(callPath+"/function/provider_specific_fields", fields, "thought_signature")
			})
		})
	case contract.ProtocolGoogleGenerateContent:
		arrayRecords(root["contents"], func(index int, content map[string]any) {
			if content["role"] != "model" {
				return
			}
			path := "/contents/" + jsonIndex(index)
			arrayRecords(content["parts"], func(index int, part map[string]any) {
				// REST uses camelCase; the Google SDK/protobuf field is snake_case.
				// Signatures can accompany function calls or regular text parts.
				addStrings(path+"/parts/"+jsonIndex(index), part, "thoughtSignature", "thought_signature")
			})
		})
	}
	return paths
}
