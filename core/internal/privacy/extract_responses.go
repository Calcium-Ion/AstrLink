package privacy

// OpenAI Responses content positions, shared by Responses compact and by the
// conversation a Codex search request carries.

func responsesInput(options InspectionOptions) position {
	item := object(responsesItemField(options))
	return textOrList(item)
}

// responsesItemField reads an input item by its type. Any item with a role is
// also read as a message, so a role-bearing record that claims a replay type
// cannot hide its content. Never read: item_reference, compaction and unknown
// items; ids, call ids, names, namespaces, server labels, status;
// encrypted_content; computer screenshots and generated images.
func responsesItemField(options InspectionOptions) func(item map[string]any, key string) position {
	return func(item map[string]any, key string) position {
		role, hasRole := item["role"]
		itemType, _ := item["type"].(string)
		if key == "content" && (itemType == "message" || hasRole) {
			return responsesMessageContent()
		}
		// Field context covers the replayed call records of the model.
		replayed := !hasRole || role == "assistant"
		switch itemType {
		case "reasoning":
			if key == "summary" || key == "content" {
				return listOf(named("text"))
			}
		case "function_call":
			if key == "arguments" {
				return toolPayload(replayed)
			}
		case "custom_tool_call":
			if key == "input" {
				return toolPayload(replayed)
			}
		case "tool_search_call", "mcp_approval_request":
			if key == "arguments" {
				return toolPayload(false)
			}
		case "mcp_call":
			switch key {
			case "arguments", "output":
				return toolPayload(false)
			case "error":
				return textPosition
			}
		case "mcp_approval_response":
			if key == "reason" {
				return textPosition
			}
		case "function_call_output", "custom_tool_call_output":
			if key == "output" {
				return toolOutput(replayed, responsesOutputPartField)
			}
		case "local_shell_call_output", "apply_patch_call_output":
			if key == "output" {
				return toolOutput(false, responsesOutputPartField)
			}
		case "shell_call_output":
			if key == "output" {
				return toolResultValue(listOf(named("stdout", "stderr")))
			}
		case "web_search_call":
			if key == "action" {
				return object(func(_ map[string]any, key string) position {
					switch key {
					case "query", "url", "pattern":
						return textPosition
					case "queries":
						return listOf(textPosition)
					case "sources":
						// The pages a search consulted.
						return listOf(named("url"))
					}
					return position{}
				})
			}
		case "file_search_call":
			switch key {
			case "queries":
				return listOf(textPosition)
			case "results":
				return listOf(named("text", "filename"))
			}
		case "code_interpreter_call":
			switch key {
			case "code":
				return textPosition
			case "outputs":
				return listOf(object(func(output map[string]any, key string) position {
					if output["type"] == "logs" && key == "logs" {
						return textPosition
					}
					return position{}
				}))
			}
		case "local_shell_call":
			if key == "action" {
				return object(func(_ map[string]any, key string) position {
					switch key {
					case "command":
						return listOf(textPosition)
					case "env":
						return object(anyFieldText)
					case "working_directory":
						return textPosition
					}
					return position{}
				})
			}
		case "shell_call":
			if key == "action" {
				return field("commands", listOf(textPosition))
			}
		case "apply_patch_call":
			if key == "operation" {
				return named("path", "diff")
			}
		case "computer_call":
			if key == "action" {
				return named("text")
			}
		case "image_generation_call":
			if key == "revised_prompt" {
				return textPosition
			}
		case "tool_search_output", "mcp_list_tools":
			// Tools a search loaded or an MCP server listed are declarations.
			if key == "tools" && options.InspectToolDeclarations {
				return listOf(object(responsesToolField))
			}
			// The server's listing error, as mcp_call reports its own.
			if key == "error" && itemType == "mcp_list_tools" {
				return textPosition
			}
		case "additional_tools":
			// The declarations Codex responses-lite carries in a developer
			// item; the per-client switch skips them.
			if key == "tools" && (!options.SkipAdditionalTools || role != "developer") {
				return listOf(object(responsesToolField))
			}
		}
		return position{}
	}
}

// responsesMessageContent is a message's content: a string or content parts.
func responsesMessageContent() position {
	return textOrList(object(responsesContentPartField))
}

// responsesContentPartField reads a message part. Never read: images, audio,
// file data, file ids and URLs, annotation ids and indexes, log probabilities.
func responsesContentPartField(part map[string]any, key string) position {
	switch part["type"] {
	case "input_text", "output_text", "text", "summary_text", "reasoning_text":
		switch key {
		case "text":
			return textPosition
		case "annotations":
			if part["type"] == "output_text" {
				return listOf(named("title", "url", "filename"))
			}
		}
	case "refusal":
		if key == "refusal" {
			return textPosition
		}
	case "input_file":
		if key == "filename" {
			return textPosition
		}
	}
	return position{}
}

// responsesOutputPartField reads the parts of a tool output. Images, file
// data and ids, and encrypted content parts are never read.
func responsesOutputPartField(part map[string]any, key string) position {
	switch part["type"] {
	case "input_text", "output_text", "text":
		if key == "text" {
			return textPosition
		}
	case "input_file":
		if key == "filename" {
			return textPosition
		}
	}
	return position{}
}

// responsesPromptField reads a reusable prompt's variables. Variable names are
// chosen by the caller, so every variable is read: a string, or an input part.
// The prompt id and version are never read.
func responsesPromptField(_ map[string]any, key string) position {
	if key == "variables" {
		return object(func(map[string]any, string) position {
			return position{text: true, fields: responsesContentPartField}
		})
	}
	return position{}
}

// responsesToolField reads a tool declaration: function, custom and namespace
// descriptions, function parameters, MCP server descriptions, and listed MCP
// tools. Names, namespaces, server labels and URLs, connector ids, allowed and
// approval tool lists, vector store ids and custom tool grammars are never
// read.
func responsesToolField(_ map[string]any, key string) position {
	switch key {
	case "description", "server_description":
		return textPosition
	case "parameters", "input_schema":
		return schema()
	case "tools":
		return listOf(object(responsesToolField))
	}
	return position{}
}
