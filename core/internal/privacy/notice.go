package privacy

import (
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// placeholderNotice explains the token placeholder convention to the model.
//
// It is only needed for token-shaped placeholders. A natural stand-in is
// already a well-formed value of its type, so a model copies it verbatim
// without being told; an opaque marker is out-of-distribution text that models
// otherwise paraphrase, reformat, or refuse.
//
// The note deliberately carries no mapping. Sending the originals alongside the
// placeholders would defeat the redaction entirely.
const placeholderNotice = "Some values in this conversation were replaced by " +
	"redaction markers of the form <PRIVATE_KIND_hex> or <SECRET_hex>. Treat each " +
	"marker as an opaque literal: copy it character for character when you need to " +
	"refer to it, never alter or invent one, and never guess the value behind it. " +
	"If a task genuinely requires the hidden value, say that it is unavailable."

// injectPlaceholderNotice prepends the convention note to the protocol's system
// instruction channel. It reports whether the document was modified.
func injectPlaceholderNotice(document jsonDocument, protocol contract.ProtocolID) bool {
	switch protocol {
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		return prependStringField(document.root, "instructions")
	case contract.ProtocolAnthropicMessages:
		return prependAnthropicSystem(document.root)
	case contract.ProtocolOpenAIChat:
		return prependChatSystemMessage(document.root)
	case contract.ProtocolGoogleGenerateContent:
		return prependGeminiSystemInstruction(document.root)
	case contract.ProtocolOpenAICompletions:
		// A raw completion has no system channel, and prefixing the prompt would
		// change what the model is asked to continue.
		return false
	default:
		return false
	}
}

func noticeAlreadyPresent(value string) bool {
	return strings.Contains(value, "redaction markers of the form")
}

func prependStringField(root map[string]any, key string) bool {
	existing, present := root[key]
	if !present || existing == nil {
		root[key] = placeholderNotice
		return true
	}
	text, ok := existing.(string)
	if !ok {
		return false
	}
	if noticeAlreadyPresent(text) {
		return false
	}
	root[key] = placeholderNotice + "\n\n" + text
	return true
}

// prependAnthropicSystem handles both accepted shapes of the system field: a
// plain string and an array of text blocks.
func prependAnthropicSystem(root map[string]any) bool {
	existing, present := root["system"]
	if !present || existing == nil {
		root["system"] = placeholderNotice
		return true
	}
	switch typed := existing.(type) {
	case string:
		if noticeAlreadyPresent(typed) {
			return false
		}
		root["system"] = placeholderNotice + "\n\n" + typed
		return true
	case []any:
		for _, block := range typed {
			blockMap, ok := block.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := blockMap["text"].(string); ok && noticeAlreadyPresent(text) {
				return false
			}
		}
		notice := map[string]any{"type": "text", "text": placeholderNotice}
		root["system"] = append([]any{notice}, typed...)
		return true
	default:
		return false
	}
}

// prependChatSystemMessage folds the note into the leading system or developer
// message when there is one, so the cacheable prefix keeps its shape, and
// otherwise inserts a new message ahead of the conversation.
func prependChatSystemMessage(root map[string]any) bool {
	messages, ok := root["messages"].([]any)
	if !ok {
		return false
	}
	for _, message := range messages {
		messageMap, ok := message.(map[string]any)
		if !ok {
			continue
		}
		role, _ := messageMap["role"].(string)
		if role != "system" && role != "developer" {
			break
		}
		switch content := messageMap["content"].(type) {
		case string:
			if noticeAlreadyPresent(content) {
				return false
			}
			messageMap["content"] = placeholderNotice + "\n\n" + content
			return true
		case []any:
			for _, part := range content {
				partMap, ok := part.(map[string]any)
				if !ok {
					continue
				}
				if text, ok := partMap["text"].(string); ok && noticeAlreadyPresent(text) {
					return false
				}
			}
			notice := map[string]any{"type": "text", "text": placeholderNotice}
			messageMap["content"] = append([]any{notice}, content...)
			return true
		}
		break
	}
	notice := map[string]any{"role": "system", "content": placeholderNotice}
	root["messages"] = append([]any{notice}, messages...)
	return true
}

func prependGeminiSystemInstruction(root map[string]any) bool {
	existing, present := root["systemInstruction"]
	if !present || existing == nil {
		root["systemInstruction"] = map[string]any{
			"parts": []any{map[string]any{"text": placeholderNotice}},
		}
		return true
	}
	instruction, ok := existing.(map[string]any)
	if !ok {
		return false
	}
	parts, ok := instruction["parts"].([]any)
	if !ok {
		instruction["parts"] = []any{map[string]any{"text": placeholderNotice}}
		return true
	}
	for _, part := range parts {
		partMap, ok := part.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := partMap["text"].(string); ok && noticeAlreadyPresent(text) {
			return false
		}
	}
	instruction["parts"] = append([]any{map[string]any{"text": placeholderNotice}}, parts...)
	return true
}
