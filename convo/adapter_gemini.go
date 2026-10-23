package convo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/QuantumNous/astrlink/convo/internal/jsonx"
)

// geminiAdapter handles :generateContent and :streamGenerateContent.
//
// Request: contents[] with role user|model (role may be omitted for a single
// user turn). Parts are text, inlineData, fileData, functionCall (model, may
// carry id), functionResponse (user), and text parts flagged thought:true.
// Thinking models attach thoughtSignature to parts; clients must replay it
// verbatim, which makes it an echo id once hashed to a bounded value.
// Response: candidates[0].content.parts[] plus a top-level responseId.
type geminiAdapter struct{}

func (geminiAdapter) HistoryField() string { return "contents" }

func (geminiAdapter) ExplicitCursors(fields map[string]json.RawMessage) []string {
	var values []string
	if id, ok := jsonx.String(fields["cachedContent"]); ok {
		values = appendExplicit(values, id)
	}
	if id, ok := jsonx.String(fields["cached_content"]); ok {
		values = appendExplicit(values, id)
	}
	return values
}

func (geminiAdapter) IsStateful(map[string]json.RawMessage) bool { return false }

func (geminiAdapter) inspectItem(item json.RawMessage) requestItem {
	content, ok := jsonx.Object(item)
	if !ok {
		return requestItem{}
	}
	role := geminiRole(content)
	result := requestItem{assistant: role == "model", echoIDs: geminiInboundEchoIDs(content)}
	if role == "user" {
		texts, hasMedia, answered := scanGeminiParts(content, false)
		result.userText = joinVisible(texts, "\n")
		result.user = hasMedia || result.userText != ""
		result.toolResult = answered
	} else if role != "model" {
		result.toolResult = geminiHasFunctionResponse(content)
	}
	return result
}

// geminiHasFunctionResponse reports whether a content answers a function
// call, whatever text sits beside the functionResponse parts.
func geminiHasFunctionResponse(content map[string]json.RawMessage) bool {
	parts, ok := jsonx.Array(content["parts"])
	if !ok {
		return false
	}
	for _, raw := range parts {
		part, ok := jsonx.Object(raw)
		if !ok {
			continue
		}
		if _, isResponse := part["functionResponse"]; isResponse {
			return true
		}
	}
	return false
}

func geminiRole(content map[string]json.RawMessage) string {
	role, ok := jsonx.String(content["role"])
	if !ok || role == "" {
		return "user"
	}
	return role
}

// geminiParts splits a content's parts into visible texts and a flag for
// non-text user attachments. functionResponse parts are neither.
func geminiParts(content map[string]json.RawMessage, includeThoughts bool) (texts []string, hasMedia bool) {
	texts, hasMedia, _ = scanGeminiParts(content, includeThoughts)
	return texts, hasMedia
}

// scanGeminiParts is geminiParts that also reports whether a part answers a
// function call, from the same decode of the parts.
func scanGeminiParts(content map[string]json.RawMessage, includeThoughts bool) (texts []string, hasMedia, functionResponse bool) {
	parts, ok := jsonx.Array(content["parts"])
	if !ok {
		return nil, false, false
	}
	for _, raw := range parts {
		part, ok := jsonx.Object(raw)
		if !ok {
			continue
		}
		if _, isResponse := part["functionResponse"]; isResponse {
			functionResponse = true
			continue
		}
		if _, isCall := part["functionCall"]; isCall {
			continue
		}
		if text, ok := jsonx.String(part["text"]); ok {
			if thought, _ := jsonx.Bool(part["thought"]); thought && !includeThoughts {
				continue
			}
			texts = append(texts, text)
			continue
		}
		if _, ok := part["inlineData"]; ok {
			hasMedia = true
		}
		if _, ok := part["fileData"]; ok {
			hasMedia = true
		}
		if _, ok := part["inline_data"]; ok {
			hasMedia = true
		}
		if _, ok := part["file_data"]; ok {
			hasMedia = true
		}
	}
	return texts, hasMedia, functionResponse
}

func (geminiAdapter) IsUserTurn(item json.RawMessage) bool {
	content, ok := jsonx.Object(item)
	if !ok {
		return false
	}
	if geminiRole(content) != "user" {
		return false
	}
	texts, hasMedia := geminiParts(content, false)
	return hasMedia || joinVisible(texts, "\n") != ""
}

func (geminiAdapter) IsToolResult(item json.RawMessage) bool {
	content, ok := jsonx.Object(item)
	if !ok {
		return false
	}
	return geminiRole(content) != "model" && geminiHasFunctionResponse(content)
}

func (geminiAdapter) InboundEchoIDs(item json.RawMessage) []string {
	content, ok := jsonx.Object(item)
	if !ok {
		return nil
	}
	return geminiInboundEchoIDs(content)
}

func geminiInboundEchoIDs(content map[string]json.RawMessage) []string {
	parts, ok := jsonx.Array(content["parts"])
	if !ok {
		return nil
	}
	var ids []string
	for _, raw := range parts {
		part, ok := jsonx.Object(raw)
		if !ok {
			continue
		}
		ids = appendGeminiPartIDs(ids, part)
	}
	return ids
}

func appendGeminiPartIDs(ids []string, part map[string]json.RawMessage) []string {
	for _, key := range []string{"functionCall", "functionResponse"} {
		if call, ok := jsonx.Object(part[key]); ok {
			if id, ok := jsonx.String(call["id"]); ok && id != "" {
				ids = append(ids, id)
			}
		}
	}
	for _, key := range []string{"thoughtSignature", "thought_signature"} {
		if signature, ok := jsonx.String(part[key]); ok && signature != "" {
			ids = append(ids, hashThoughtSignature(signature))
		}
	}
	return ids
}

// hashThoughtSignature bounds an arbitrarily long opaque signature to a
// cursor-sized value. Signatures are random per response, so the hash keeps
// their entropy.
func hashThoughtSignature(signature string) string {
	sum := sha256.Sum256([]byte(signature))
	return "ts_" + hex.EncodeToString(sum[:])[:32]
}

func (geminiAdapter) AssistantText(item json.RawMessage) (string, bool) {
	content, ok := jsonx.Object(item)
	if !ok {
		return "", false
	}
	if geminiRole(content) != "model" {
		return "", false
	}
	texts, _ := geminiParts(content, false)
	return joinAll(texts, ""), true
}

func (geminiAdapter) UserText(item json.RawMessage) string {
	content, ok := jsonx.Object(item)
	if !ok {
		return ""
	}
	if geminiRole(content) != "user" {
		return ""
	}
	texts, _ := geminiParts(content, false)
	return joinVisible(texts, "\n")
}

func (geminiAdapter) NewResponseParser(streaming bool) ResponseParser {
	return &geminiParser{streaming: streaming}
}

type geminiParser struct {
	streaming bool
}

func (parser *geminiParser) Observe(doc map[string]json.RawMessage, sink EventSink) {
	for _, key := range []string{"responseId", "response_id"} {
		if id, ok := jsonx.String(doc[key]); ok && id != "" {
			sink.OutputID(id)
			break
		}
	}
	candidates, ok := jsonx.Array(doc["candidates"])
	if !ok {
		return
	}
	for position, raw := range candidates {
		candidate, ok := jsonx.Object(raw)
		if !ok {
			continue
		}
		index := position
		if value, ok := jsonx.Int(candidate["index"]); ok {
			index = value
		}
		content, ok := jsonx.Object(candidate["content"])
		if !ok {
			continue
		}
		parts, ok := jsonx.Array(content["parts"])
		if !ok {
			continue
		}
		for _, rawPart := range parts {
			part, ok := jsonx.Object(rawPart)
			if !ok {
				continue
			}
			for _, id := range appendGeminiPartIDs(nil, part) {
				sink.EchoID(id)
			}
			if index != 0 {
				continue
			}
			if thought, _ := jsonx.Bool(part["thought"]); thought {
				continue
			}
			if text, ok := jsonx.String(part["text"]); ok && text != "" {
				sink.AssistantText(text)
			}
		}
	}
}
