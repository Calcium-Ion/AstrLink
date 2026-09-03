package convo

import (
	"encoding/json"

	"github.com/QuantumNous/astrlink/convo/internal/jsonx"
)

// anthropicAdapter handles POST /v1/messages.
//
// Request: messages[] with role user|assistant. Content is a string or an
// array of blocks: text, image, document, tool_use (assistant, carries id),
// tool_result (user, carries tool_use_id), server_tool_use, mcp_tool_use.
// Response: content[] blocks, or SSE events message_start /
// content_block_start / content_block_delta / message_delta / message_stop.
type anthropicAdapter struct{}

var (
	anthropicTextTypes = map[string]bool{"text": true}
	anthropicSkipTypes = map[string]bool{"tool_result": true}
	anthropicToolTypes = map[string]bool{"tool_use": true, "server_tool_use": true, "mcp_tool_use": true}
)

func (anthropicAdapter) HistoryField() string { return "messages" }

func (anthropicAdapter) ExplicitCursors(fields map[string]json.RawMessage) []string {
	var values []string
	for _, value := range genericExplicitCursors(fields) {
		values = appendExplicit(values, value)
	}
	if id, ok := jsonx.StringOrID(fields["container"]); ok {
		values = appendExplicit(values, id)
	}
	return values
}

func (anthropicAdapter) IsStateful(map[string]json.RawMessage) bool { return false }

func (anthropicAdapter) IsUserTurn(item json.RawMessage) bool {
	message, ok := jsonx.Object(item)
	if !ok {
		return false
	}
	if role, _ := jsonx.String(message["role"]); role != "user" {
		return false
	}
	texts, hasMedia := stringOrTextParts(message["content"], anthropicTextTypes, anthropicSkipTypes)
	return hasMedia || joinVisible(texts, "\n") != ""
}

func (anthropicAdapter) InboundEchoIDs(item json.RawMessage) []string {
	message, ok := jsonx.Object(item)
	if !ok {
		return nil
	}
	blocks, ok := jsonx.Array(message["content"])
	if !ok {
		return nil
	}
	var ids []string
	for _, raw := range blocks {
		block, ok := jsonx.Object(raw)
		if !ok {
			continue
		}
		blockType, _ := jsonx.String(block["type"])
		switch {
		case blockType == "tool_result":
			if id, ok := jsonx.String(block["tool_use_id"]); ok && id != "" {
				ids = append(ids, id)
			}
		case anthropicToolTypes[blockType]:
			if id, ok := jsonx.String(block["id"]); ok && id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func (anthropicAdapter) AssistantText(item json.RawMessage) (string, bool) {
	message, ok := jsonx.Object(item)
	if !ok {
		return "", false
	}
	if role, _ := jsonx.String(message["role"]); role != "assistant" {
		return "", false
	}
	// thinking / redacted_thinking blocks have no "text" member and are
	// skipped by stringOrTextParts as non-text attachments.
	texts, _ := stringOrTextParts(message["content"], anthropicTextTypes, anthropicSkipTypes)
	return joinAll(texts, "\n"), true
}

func (anthropicAdapter) UserText(item json.RawMessage) string {
	message, ok := jsonx.Object(item)
	if !ok {
		return ""
	}
	if role, _ := jsonx.String(message["role"]); role != "user" {
		return ""
	}
	texts, _ := stringOrTextParts(message["content"], anthropicTextTypes, anthropicSkipTypes)
	return joinVisible(texts, "\n")
}

func (anthropicAdapter) NewResponseParser(streaming bool) ResponseParser {
	return &anthropicParser{streaming: streaming, blockTypes: make(map[int]string)}
}

type anthropicParser struct {
	streaming  bool
	blockTypes map[int]string
	textBlocks int
}

func (parser *anthropicParser) Observe(doc map[string]json.RawMessage, sink EventSink) {
	if !parser.streaming {
		parser.observeMessage(doc, sink)
		return
	}
	eventType, _ := jsonx.String(doc["type"])
	switch eventType {
	case "message_start":
		if message, ok := jsonx.Object(doc["message"]); ok {
			if id, ok := jsonx.String(message["id"]); ok && id != "" {
				sink.OutputID(id)
			}
			if id, ok := jsonx.StringOrID(message["container"]); ok {
				sink.EchoID(id)
			}
		}
	case "content_block_start":
		index, _ := jsonx.Int(doc["index"])
		block, ok := jsonx.Object(doc["content_block"])
		if !ok {
			return
		}
		blockType, _ := jsonx.String(block["type"])
		parser.blockTypes[index] = blockType
		switch {
		case blockType == "text":
			parser.textBlocks++
			if parser.textBlocks > 1 {
				sink.AssistantText("\n")
			}
			if text, ok := jsonx.String(block["text"]); ok && text != "" {
				sink.AssistantText(text)
			}
		case anthropicToolTypes[blockType]:
			if id, ok := jsonx.String(block["id"]); ok && id != "" {
				sink.EchoID(id)
			}
		}
	case "content_block_delta":
		index, _ := jsonx.Int(doc["index"])
		if parser.blockTypes[index] != "text" {
			return
		}
		delta, ok := jsonx.Object(doc["delta"])
		if !ok {
			return
		}
		if deltaType, _ := jsonx.String(delta["type"]); deltaType != "text_delta" {
			return
		}
		if text, ok := jsonx.String(delta["text"]); ok && text != "" {
			sink.AssistantText(text)
		}
	case "message_delta":
		if delta, ok := jsonx.Object(doc["delta"]); ok {
			if id, ok := jsonx.StringOrID(delta["container"]); ok {
				sink.EchoID(id)
			}
		}
	}
}

func (parser *anthropicParser) observeMessage(message map[string]json.RawMessage, sink EventSink) {
	if id, ok := jsonx.String(message["id"]); ok && id != "" {
		sink.OutputID(id)
	}
	if id, ok := jsonx.StringOrID(message["container"]); ok {
		sink.EchoID(id)
	}
	blocks, ok := jsonx.Array(message["content"])
	if !ok {
		return
	}
	var texts []string
	for _, raw := range blocks {
		block, ok := jsonx.Object(raw)
		if !ok {
			continue
		}
		blockType, _ := jsonx.String(block["type"])
		switch {
		case blockType == "text":
			if text, ok := jsonx.String(block["text"]); ok {
				texts = append(texts, text)
			}
		case anthropicToolTypes[blockType]:
			if id, ok := jsonx.String(block["id"]); ok && id != "" {
				sink.EchoID(id)
			}
		}
	}
	if text := joinAll(texts, "\n"); text != "" {
		sink.AssistantText(text)
	}
}
