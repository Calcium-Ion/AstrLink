package privacy

// Anthropic Messages content positions.

func anthropicMessageField(message map[string]any, key string) position {
	if key == "content" {
		return anthropicContent(message["role"])
	}
	return position{}
}

// anthropicContent is a message's or the system prompt's content: a string
// or content blocks.
func anthropicContent(role any) position {
	return textOrList(object(anthropicBlockField(role)))
}

// anthropicBlockField reads a content block by its type. Never read: image,
// redacted_thinking, tool_search_tool_result, container_upload, tool_reference
// and unknown blocks; tool_use name, id, server_name and caller; tool_use_id;
// signatures, citation indexes and encrypted content.
func anthropicBlockField(role any) func(block map[string]any, key string) position {
	return func(block map[string]any, key string) position {
		blockType, _ := block["type"].(string)
		switch blockType {
		case "text", "document", "search_result":
			return anthropicTextBlock(role, block, key)
		case "thinking":
			// Anthropic requires a signed assistant block complete and
			// unmodified. Unsigned reasoning, and blocks in other roles, are
			// ordinary content.
			if key == "thinking" && (role != "assistant" || !signed(block)) {
				return textPosition
			}
		case "tool_use", "server_tool_use", "mcp_tool_use":
			if key == "input" {
				return toolPayload(role == "assistant" && blockType != "mcp_tool_use")
			}
		case "tool_result", "mcp_tool_result":
			if key == "content" {
				return toolOutput(role == "user" && blockType == "tool_result", anthropicTextBlockField(role))
			}
		case "web_search_tool_result":
			if key == "content" {
				return toolResultValue(listOf(named("title", "url")))
			}
		case "web_fetch_tool_result":
			if key == "content" {
				return toolResultValue(object(func(_ map[string]any, key string) position {
					switch key {
					case "url":
						return textPosition
					case "content":
						return object(anthropicTextBlockField(role))
					}
					return position{}
				}))
			}
		case "code_execution_tool_result", "bash_code_execution_tool_result":
			if key == "content" {
				return toolResultValue(named("stdout", "stderr"))
			}
		case "text_editor_code_execution_tool_result":
			if key == "content" {
				return toolResultValue(object(func(_ map[string]any, key string) position {
					switch key {
					case "content":
						return textPosition
					case "lines":
						return listOf(textPosition)
					}
					return position{}
				}))
			}
		}
		return position{}
	}
}

// anthropicTextBlockField reads the blocks that carry text and may also appear
// in tool results: text with its citations, documents and search results.
func anthropicTextBlockField(role any) func(block map[string]any, key string) position {
	return func(block map[string]any, key string) position {
		return anthropicTextBlock(role, block, key)
	}
}

func anthropicTextBlock(role any, block map[string]any, key string) position {
	switch block["type"] {
	case "text":
		switch key {
		case "text":
			return textPosition
		case "citations":
			return listOf(named("cited_text", "document_title", "title", "url", "source"))
		}
	case "document":
		switch key {
		case "title", "context":
			return textPosition
		case "source":
			return object(anthropicDocumentSourceField(role))
		}
	case "search_result":
		switch key {
		case "source", "title":
			return textPosition
		case "content":
			return listOf(object(anthropicTextBlockField(role)))
		}
	}
	return position{}
}

// anthropicDocumentSourceField reads plain-text and content sources. Base64,
// URL and file sources are never read.
func anthropicDocumentSourceField(role any) func(source map[string]any, key string) position {
	return func(source map[string]any, key string) position {
		switch source["type"] {
		case "text":
			if key == "data" {
				return textPosition
			}
		case "content":
			if key == "content" {
				return textOrList(object(anthropicTextBlockField(role)))
			}
		}
		return position{}
	}
}

// anthropicToolField reads a custom tool declaration: its description, input
// schema and example inputs. Names, server tool settings and MCP toolsets are
// never read.
func anthropicToolField(_ map[string]any, key string) position {
	switch key {
	case "description":
		return textPosition
	case "input_schema":
		return schema()
	case "input_examples":
		return payload()
	}
	return position{}
}
