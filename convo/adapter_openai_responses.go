package convo

import (
	"encoding/json"

	"github.com/QuantumNous/astrlink/convo/internal/jsonx"
)

// openAIResponsesAdapter handles POST /v1/responses (and /compact).
//
// Request: input is a string or an array of items. Items are messages
// ({role, content}), function_call / function_call_output pairs joined by
// call_id, replayed output items (reasoning, message, *_call) that keep their
// server-issued id, and item_reference pointers. With previous_response_id the
// server holds the history and input carries only the new turn.
// Response: output[] items, or SSE events named response.*.
type openAIResponsesAdapter struct{}

var (
	responsesUserTextTypes = map[string]bool{"input_text": true, "text": true}
	responsesSkipTypes     = map[string]bool{}
)

func (openAIResponsesAdapter) HistoryField() string { return "input" }

func (openAIResponsesAdapter) ExplicitCursors(fields map[string]json.RawMessage) []string {
	var values []string
	if id, ok := jsonx.String(fields["previous_response_id"]); ok {
		values = appendExplicit(values, id)
	}
	for _, value := range genericExplicitCursors(fields) {
		values = appendExplicit(values, value)
	}
	return values
}

func (openAIResponsesAdapter) IsStateful(fields map[string]json.RawMessage) bool {
	id, ok := jsonx.String(fields["previous_response_id"])
	return ok && clampCursorValue(id) != ""
}

func responsesItemRole(item map[string]json.RawMessage) (itemType, role string) {
	itemType, _ = jsonx.String(item["type"])
	role, _ = jsonx.String(item["role"])
	return itemType, role
}

func responsesIsUserMessage(item map[string]json.RawMessage) bool {
	itemType, role := responsesItemRole(item)
	return role == "user" && (itemType == "" || itemType == "message")
}

func (openAIResponsesAdapter) IsUserTurn(item json.RawMessage) bool {
	object, ok := jsonx.Object(item)
	if !ok {
		return false
	}
	if !responsesIsUserMessage(object) {
		return false
	}
	texts, hasMedia := stringOrTextParts(object["content"], responsesUserTextTypes, responsesSkipTypes)
	return hasMedia || joinVisible(texts, "\n") != ""
}

func (openAIResponsesAdapter) InboundEchoIDs(item json.RawMessage) []string {
	object, ok := jsonx.Object(item)
	if !ok {
		return nil
	}
	var ids []string
	if callID, ok := jsonx.String(object["call_id"]); ok && callID != "" {
		ids = append(ids, callID)
	}
	if id, ok := jsonx.String(object["id"]); ok && id != "" && !responsesIsUserMessage(object) {
		ids = append(ids, id)
	}
	return ids
}

func (openAIResponsesAdapter) AssistantText(item json.RawMessage) (string, bool) {
	object, ok := jsonx.Object(item)
	if !ok {
		return "", false
	}
	itemType, role := responsesItemRole(object)
	if role != "assistant" || (itemType != "" && itemType != "message") {
		return "", false
	}
	texts, _ := stringOrTextParts(object["content"], map[string]bool{"output_text": true, "text": true}, map[string]bool{"refusal": true})
	return joinAll(texts, ""), true
}

func (openAIResponsesAdapter) UserText(item json.RawMessage) string {
	object, ok := jsonx.Object(item)
	if !ok {
		return ""
	}
	if !responsesIsUserMessage(object) {
		return ""
	}
	texts, _ := stringOrTextParts(object["content"], responsesUserTextTypes, responsesSkipTypes)
	return joinVisible(texts, "\n")
}

func (openAIResponsesAdapter) NewResponseParser(streaming bool) ResponseParser {
	return &openAIResponsesParser{streaming: streaming}
}

type openAIResponsesParser struct {
	streaming bool
}

func (parser *openAIResponsesParser) Observe(doc map[string]json.RawMessage, sink EventSink) {
	if !parser.streaming {
		parser.observeResponse(doc, sink, true)
		return
	}
	eventType, _ := jsonx.String(doc["type"])
	switch eventType {
	case "response.created", "response.in_progress", "response.completed", "response.done", "response.incomplete", "response.failed":
		if response, ok := jsonx.Object(doc["response"]); ok {
			// Text arrives through output_text.delta; the final snapshot only
			// contributes ids so the digest is not doubled.
			parser.observeResponse(response, sink, false)
		}
	case "response.output_item.added":
		if item, ok := jsonx.Object(doc["item"]); ok {
			itemType, role := responsesItemRole(item)
			if itemType == "message" && role == "assistant" {
				sink.ResetAssistantText()
			}
		}
	case "response.output_item.done":
		if item, ok := jsonx.Object(doc["item"]); ok {
			responsesItemIDs(item, sink)
		}
	case "response.output_text.delta":
		if delta, ok := jsonx.String(doc["delta"]); ok && delta != "" {
			sink.AssistantText(delta)
		}
	}
}

func (parser *openAIResponsesParser) observeResponse(response map[string]json.RawMessage, sink EventSink, withText bool) {
	if id, ok := jsonx.String(response["id"]); ok && id != "" {
		sink.OutputID(id)
	}
	output, ok := jsonx.Array(response["output"])
	if !ok {
		return
	}
	adapter := openAIResponsesAdapter{}
	for _, raw := range output {
		item, ok := jsonx.Object(raw)
		if !ok {
			continue
		}
		responsesItemIDs(item, sink)
		if !withText {
			continue
		}
		if text, ok := adapter.AssistantText(raw); ok {
			sink.ResetAssistantText()
			if text != "" {
				sink.AssistantText(text)
			}
		}
	}
}

func responsesItemIDs(item map[string]json.RawMessage, sink EventSink) {
	if id, ok := jsonx.String(item["id"]); ok && id != "" {
		sink.EchoID(id)
	}
	if callID, ok := jsonx.String(item["call_id"]); ok && callID != "" {
		sink.EchoID(callID)
	}
}
