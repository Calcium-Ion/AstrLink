package privacy

// OpenAI Chat Completions content positions.

// chatMessageField reads a message in any role. Never read: name,
// tool_call_id, audio, call ids and function or custom tool names, and the
// signatures providers attach to tool calls (extra_content,
// provider_specific_fields).
func chatMessageField(message map[string]any, key string) position {
	role := message["role"]
	assistant := role == "assistant"
	switch key {
	case "content":
		if role == "tool" || role == "function" {
			// A tool's output: a string, content parts, or JSON. Its objects
			// and arrays carry field context; its strings are not
			// revalidated as JSON.
			return position{
				text:         true,
				fields:       payloadField,
				items:        typedOrPayload(chatPartField(assistant)),
				fieldContext: true,
			}
		}
		return textOrList(object(chatPartField(assistant)))
	case "refusal", "reasoning_content", "reasoning":
		return textPosition
	case "reasoning_details":
		// OpenRouter's reasoning extension.
		return listOf(object(func(detail map[string]any, key string) position {
			switch detail["type"] {
			case "reasoning.text":
				if key == "text" && (!assistant || !signed(detail)) {
					return textPosition
				}
			case "reasoning.summary":
				if key == "summary" {
					return textPosition
				}
			}
			return position{}
		}))
	case "thinking_blocks":
		// LiteLLM preserves Anthropic thinking blocks in this extension.
		return listOf(object(chatThinkingField(assistant)))
	case "annotations":
		return listOf(object(func(annotation map[string]any, key string) position {
			if annotation["type"] == "url_citation" && key == "url_citation" {
				return named("title", "url")
			}
			return position{}
		}))
	case "tool_calls":
		return listOf(object(func(call map[string]any, key string) position {
			switch key {
			case "function":
				return field("arguments", toolPayload(assistant && call["type"] == "function"))
			case "custom":
				// Free-form input: not revalidated as JSON.
				return field("input", payload())
			}
			return position{}
		}))
	case "function_call":
		return field("arguments", toolPayload(assistant))
	}
	return position{}
}

// chatPartField reads a content part: text, refusals, file names and
// unsigned thinking. Images, audio, file data and file ids are never read.
func chatPartField(assistant bool) func(part map[string]any, key string) position {
	return func(part map[string]any, key string) position {
		switch part["type"] {
		case "text":
			if key == "text" {
				return textPosition
			}
		case "refusal":
			if key == "refusal" {
				return textPosition
			}
		case "file":
			if key == "file" {
				return named("filename")
			}
		case "thinking":
			return chatThinkingField(assistant)(part, key)
		}
		return position{}
	}
}

// chatThinkingField keeps a signed assistant thinking block unmodified, as
// Anthropic requires. Unsigned reasoning is ordinary content.
func chatThinkingField(assistant bool) func(block map[string]any, key string) position {
	return func(block map[string]any, key string) position {
		if block["type"] == "thinking" && key == "thinking" && (!assistant || !signed(block)) {
			return textPosition
		}
		return position{}
	}
}

// chatToolField reads a declaration in the tools list. A custom tool's
// format grammar is a syntax definition, like a schema pattern, and is never
// read.
func chatToolField(_ map[string]any, key string) position {
	switch key {
	case "function":
		return object(functionDeclarationField)
	case "custom":
		return named("description")
	}
	return position{}
}
