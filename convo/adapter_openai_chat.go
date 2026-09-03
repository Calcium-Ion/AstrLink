package convo

import (
	"encoding/json"

	"github.com/QuantumNous/astrlink/convo/internal/jsonx"
)

// openAIChatAdapter handles POST /v1/chat/completions.
//
// Request: messages[] with role user|assistant|tool|system|developer. Tool
// loops echo assistant tool_calls[].id back as tool messages' tool_call_id.
// Response: choices[].message (or delta) with content and tool_calls.
type openAIChatAdapter struct{}

var (
	chatUserTextTypes = map[string]bool{"text": true, "input_text": true}
	chatSkipTypes     = map[string]bool{}
)

func (openAIChatAdapter) HistoryField() string { return "messages" }

func (openAIChatAdapter) ExplicitCursors(fields map[string]json.RawMessage) []string {
	return genericExplicitCursors(fields)
}

func (openAIChatAdapter) IsStateful(map[string]json.RawMessage) bool { return false }

func (openAIChatAdapter) IsUserTurn(item json.RawMessage) bool {
	message, ok := jsonx.Object(item)
	if !ok {
		return false
	}
	if role, _ := jsonx.String(message["role"]); role != "user" {
		return false
	}
	texts, hasMedia := stringOrTextParts(message["content"], chatUserTextTypes, chatSkipTypes)
	return hasMedia || joinVisible(texts, "\n") != ""
}

func (openAIChatAdapter) InboundEchoIDs(item json.RawMessage) []string {
	message, ok := jsonx.Object(item)
	if !ok {
		return nil
	}
	role, _ := jsonx.String(message["role"])
	switch role {
	case "tool":
		if id, ok := jsonx.String(message["tool_call_id"]); ok {
			return []string{id}
		}
	case "assistant":
		return chatToolCallIDs(message["tool_calls"])
	}
	return nil
}

func chatToolCallIDs(raw json.RawMessage) []string {
	calls, ok := jsonx.Array(raw)
	if !ok {
		return nil
	}
	var ids []string
	for _, item := range calls {
		call, ok := jsonx.Object(item)
		if !ok {
			continue
		}
		if id, ok := jsonx.String(call["id"]); ok && id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func (openAIChatAdapter) AssistantText(item json.RawMessage) (string, bool) {
	message, ok := jsonx.Object(item)
	if !ok {
		return "", false
	}
	if role, _ := jsonx.String(message["role"]); role != "assistant" {
		return "", false
	}
	// reasoning_content is deliberately not read: clients drop it on replay.
	texts, _ := stringOrTextParts(message["content"], chatUserTextTypes, chatSkipTypes)
	return joinAll(texts, ""), true
}

func (openAIChatAdapter) UserText(item json.RawMessage) string {
	message, ok := jsonx.Object(item)
	if !ok {
		return ""
	}
	if role, _ := jsonx.String(message["role"]); role != "user" {
		return ""
	}
	texts, _ := stringOrTextParts(message["content"], chatUserTextTypes, chatSkipTypes)
	return joinVisible(texts, "\n")
}

func (openAIChatAdapter) NewResponseParser(streaming bool) ResponseParser {
	return &openAIChatParser{streaming: streaming}
}

type openAIChatParser struct {
	streaming bool
}

func (parser *openAIChatParser) Observe(doc map[string]json.RawMessage, sink EventSink) {
	if id, ok := jsonx.String(doc["id"]); ok && id != "" {
		sink.OutputID(id)
	}
	choices, ok := jsonx.Array(doc["choices"])
	if !ok {
		return
	}
	for position, item := range choices {
		choice, ok := jsonx.Object(item)
		if !ok {
			continue
		}
		index := position
		if value, ok := jsonx.Int(choice["index"]); ok {
			index = value
		}
		payloadKey := "message"
		if parser.streaming {
			payloadKey = "delta"
		}
		payload, ok := jsonx.Object(choice[payloadKey])
		if !ok {
			continue
		}
		for _, id := range chatToolCallIDs(payload["tool_calls"]) {
			sink.EchoID(id)
		}
		if index != 0 {
			continue
		}
		texts, _ := stringOrTextParts(payload["content"], chatUserTextTypes, chatSkipTypes)
		if text := joinAll(texts, ""); text != "" {
			sink.AssistantText(text)
		}
	}
}

// joinAll joins texts without the visibility filter; assistant output is never
// harness noise.
func joinAll(parts []string, sep string) string {
	if len(parts) == 1 {
		return parts[0]
	}
	total := 0
	for _, part := range parts {
		total += len(part) + len(sep)
	}
	joined := make([]byte, 0, total)
	for index, part := range parts {
		if index > 0 {
			joined = append(joined, sep...)
		}
		joined = append(joined, part...)
	}
	return string(joined)
}
