package controlapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// getRequestConversationContent reads a call's audit content cut down to
// what the conversation view draws from it: the operator's words and the
// tool results the call sent back, and the response. A long session's
// request bodies replay its whole history, megabytes per call, and the
// desktop host hands a reply to the page on its main thread, so sending
// the full body for every call stalled the page while a session opened.
func (handler *Handler) getRequestConversationContent(writer http.ResponseWriter, request *http.Request, rawID string) {
	handler.readRequestAudit(writer, request, rawID, func(reader *auditReader, id contract.RequestID, blobs []storage.AuditBlob) (any, error) {
		content, err := reader.content(id, blobs)
		if err != nil {
			return nil, err
		}
		return conversationContent(string(reader.record.InputProtocol), content), nil
	})
}

// conversationContent keeps the parts the conversation reads. The provider's
// response stands in only when the client's was not kept; the provider's
// request never does.
func conversationContent(protocol string, content contract.AuditContent) contract.AuditContent {
	content.UpstreamRequestBody = nil
	content.UpstreamHTTPMeta = nil
	content.HTTPMeta = nil
	if content.ResponseContent != nil {
		content.UpstreamResponseContent = nil
	}
	if part := content.RequestBody; part != nil && !part.Withheld {
		slim := *part
		slim.Content = conversationRequestBody(protocol, part.Content)
		content.RequestBody = &slim
	}
	return content
}

type conversationShape int

const (
	conversationShapeNone conversationShape = iota
	conversationShapeResponses
	conversationShapeMessages
	conversationShapeContents
)

// Keep in step with bodyShape in apps/desktop/src/request-conversation-model.ts.
func conversationBodyShape(protocol string) (conversationShape, string) {
	switch {
	case strings.HasPrefix(protocol, "openai.responses"):
		return conversationShapeResponses, "input"
	case protocol == "openai.chat" || protocol == "anthropic.messages":
		return conversationShapeMessages, "messages"
	case protocol == "google.generate_content":
		return conversationShapeContents, "contents"
	}
	return conversationShapeNone, ""
}

// conversationItem is what classifying one history item needs.
type conversationItem struct {
	Role    string            `json:"role"`
	Type    string            `json:"type"`
	Content json.RawMessage   `json:"content"`
	Parts   []json.RawMessage `json:"parts"`
}

type conversationBlock struct {
	Type             string          `json:"type"`
	Text             *string         `json:"text"`
	FunctionResponse json.RawMessage `json:"functionResponse"`
}

// conversationRequestBody keeps the history items the desktop's
// extractLastUserText and extractToolResults read, in order: every user
// message, and the trailing run of tool results and user messages whole.
// Assistant turns and earlier tool results shrink to stubs that keep only
// what tells them apart, so a client whose notes follow the prompt still
// finds where turns open. Everything else in the body goes. A body the view
// could not read anyway comes back empty.
func conversationRequestBody(protocol, body string) string {
	shape, key := conversationBodyShape(protocol)
	if shape == conversationShapeNone {
		return ""
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &root); err != nil || len(root) == 0 {
		return ""
	}
	raw, ok := root[key]
	if !ok {
		return marshalConversationBody(key, nil)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		// A string prompt, or a value the view ignores: small either way.
		return marshalConversationBody(key, raw)
	}
	decoded := make([]conversationItem, len(items))
	for index, item := range items {
		// An item that is not an object decodes to nothing and is dropped.
		_ = json.Unmarshal(item, &decoded[index])
	}
	tail := len(items)
	for tail > 0 {
		item := decoded[tail-1]
		if !conversationToolResult(item, shape) && !conversationUser(item, shape) {
			break
		}
		tail--
	}
	kept := make([]json.RawMessage, 0, len(items))
	for index, item := range items {
		if index >= tail {
			kept = append(kept, item)
			continue
		}
		if stub := conversationStub(item, decoded[index], shape); stub != nil {
			kept = append(kept, stub)
		}
	}
	encoded, err := json.Marshal(kept)
	if err != nil {
		return ""
	}
	return marshalConversationBody(key, encoded)
}

func marshalConversationBody(key string, items json.RawMessage) string {
	if items == nil {
		items = json.RawMessage("[]")
	}
	encoded, err := json.Marshal(map[string]json.RawMessage{key: items})
	if err != nil {
		return ""
	}
	return string(encoded)
}

// Keep in step with isUserItem in request-conversation-model.ts.
func conversationUser(item conversationItem, shape conversationShape) bool {
	if shape == conversationShapeResponses {
		return item.Role == "user" && (item.Type == "" || item.Type == "message")
	}
	return item.Role == "user" || (shape == conversationShapeContents && item.Role == "")
}

// Keep in step with isAssistantItem in request-conversation-model.ts.
func conversationAssistant(item conversationItem, shape conversationShape) bool {
	switch shape {
	case conversationShapeResponses:
		return item.Role == "assistant" && (item.Type == "" || item.Type == "message")
	case conversationShapeContents:
		return item.Role == "model"
	}
	return item.Role == "assistant"
}

// Keep in step with isToolResultItem in request-conversation-model.ts.
func conversationToolResult(item conversationItem, shape conversationShape) bool {
	switch shape {
	case conversationShapeResponses:
		return strings.HasSuffix(item.Type, "_call_output")
	case conversationShapeContents:
		if item.Role == "model" {
			return false
		}
		for _, part := range item.Parts {
			if conversationBlockOf(part).FunctionResponse != nil {
				return true
			}
		}
		return false
	}
	if item.Role == "tool" {
		return true
	}
	if item.Role != "user" {
		return false
	}
	for _, block := range conversationBlocks(item.Content) {
		if conversationBlockOf(block).Type == "tool_result" {
			return true
		}
	}
	return false
}

// conversationStub is what an item before the tail keeps: user messages
// their words, tool results and assistant turns only their kind. Nil drops
// the item.
func conversationStub(raw json.RawMessage, item conversationItem, shape conversationShape) json.RawMessage {
	switch {
	case conversationToolResult(item, shape):
		switch shape {
		case conversationShapeResponses:
			return mustConversationJSON(map[string]string{"type": item.Type})
		case conversationShapeContents:
			parts := conversationTextBlocks(item.Parts)
			parts = append(parts, json.RawMessage(`{"functionResponse":{}}`))
			return mustConversationJSON(map[string]any{"role": item.Role, "parts": parts})
		}
		if item.Role == "tool" {
			return json.RawMessage(`{"role":"tool"}`)
		}
		blocks := conversationTextBlocks(conversationBlocks(item.Content))
		blocks = append(blocks, json.RawMessage(`{"type":"tool_result"}`))
		return mustConversationJSON(map[string]any{"role": item.Role, "content": blocks})
	case conversationUser(item, shape):
		return raw
	case conversationAssistant(item, shape):
		stub := map[string]string{"role": item.Role}
		if item.Type != "" {
			stub["type"] = item.Type
		}
		return mustConversationJSON(stub)
	}
	return nil
}

func conversationBlocks(content json.RawMessage) []json.RawMessage {
	var blocks []json.RawMessage
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	return blocks
}

func conversationBlockOf(raw json.RawMessage) conversationBlock {
	var block conversationBlock
	_ = json.Unmarshal(raw, &block)
	if string(block.FunctionResponse) == "null" {
		block.FunctionResponse = nil
	}
	return block
}

// conversationTextBlocks keeps the blocks textOfBlocks reads.
func conversationTextBlocks(blocks []json.RawMessage) []json.RawMessage {
	kept := []json.RawMessage{}
	for _, raw := range blocks {
		block := conversationBlockOf(raw)
		switch block.Type {
		case "text", "input_text", "output_text":
			kept = append(kept, raw)
		case "":
			if block.Text != nil {
				kept = append(kept, raw)
			}
		}
	}
	return kept
}

func mustConversationJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}
