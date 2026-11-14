package privacy

// Gemini generateContent content positions. REST uses camelCase; the
// protobuf JSON mapping also accepts the snake_case field names.

func geminiContentField(content map[string]any, key string) position {
	if key == "parts" {
		return listOf(object(geminiPartField(content["role"])))
	}
	return position{}
}

// geminiPartField reads a part. Never read: function names and ids, thought
// signatures, inline and file data, video metadata.
func geminiPartField(role any) func(part map[string]any, key string) position {
	return func(_ map[string]any, key string) position {
		switch key {
		case "text":
			return textPosition
		case "executableCode", "executable_code":
			return named("code")
		case "codeExecutionResult", "code_execution_result":
			return named("output")
		case "functionCall", "function_call":
			return field("args", toolPayload(role == "model" && key == "functionCall"))
		case "functionResponse", "function_response":
			return field("response", toolPayload(role == "user" && key == "functionResponse"))
		}
		return position{}
	}
}

// geminiToolField reads function declarations: descriptions and parameter and
// response schemas.
func geminiToolField(_ map[string]any, key string) position {
	switch key {
	case "functionDeclarations", "function_declarations":
		return listOf(object(func(_ map[string]any, key string) position {
			switch key {
			case "description":
				return textPosition
			case "parameters", "parametersJsonSchema", "parameters_json_schema",
				"response", "responseJsonSchema", "response_json_schema":
				return schema()
			}
			return position{}
		}))
	}
	return position{}
}
