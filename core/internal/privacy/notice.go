package privacy

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/tidwall/gjson"
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
func injectPlaceholderNotice(document *jsonDocument, protocol contract.ProtocolID) (bool, error) {
	switch protocol {
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		// A client may replay the notice in input after converting protocols.
		if messagesContainNotice(gjson.GetBytes(document.body, "input")) {
			return false, nil
		}
		return prependStringField(document, "instructions")
	case contract.ProtocolAnthropicMessages:
		return prependAnthropicSystem(document)
	case contract.ProtocolOpenAIChat:
		return prependChatSystemMessage(document)
	case contract.ProtocolGoogleGenerateContent:
		return prependGeminiSystemInstruction(document)
	case contract.ProtocolOpenAICompletions:
		// A raw completion has no system channel, and prefixing the prompt would
		// change what the model is asked to continue.
		return false, nil
	default:
		return false, nil
	}
}

func noticeAlreadyPresent(value string) bool {
	return strings.Contains(value, "redaction markers of the form")
}

func prependStringField(document *jsonDocument, path string) (bool, error) {
	existing := gjson.GetBytes(document.body, path)
	if existing.Type == gjson.Null {
		return true, document.setValue(path, placeholderNotice)
	}
	if existing.Type != gjson.String || noticeAlreadyPresent(existing.Str) {
		return false, nil
	}
	return true, document.setValue(path, placeholderNotice+"\n\n"+existing.Str)
}

// prependAnthropicSystem handles both accepted shapes of the system field: a
// plain string and an array of text blocks.
func prependAnthropicSystem(document *jsonDocument) (bool, error) {
	system := gjson.GetBytes(document.body, "system")
	if !system.IsArray() {
		return prependStringField(document, "system")
	}
	if contentContainsNotice(system) {
		return false, nil
	}
	return true, document.prependArrayValue("system", map[string]string{"type": "text", "text": placeholderNotice})
}

// prependChatSystemMessage folds the note into the leading system or developer
// message when there is one, so the cacheable prefix keeps its shape, and
// otherwise inserts a new message ahead of the conversation.
func prependChatSystemMessage(document *jsonDocument) (bool, error) {
	messages := gjson.GetBytes(document.body, "messages")
	if !messages.IsArray() || messagesContainNotice(messages) {
		return false, nil
	}
	for index, message := range messages.Array() {
		if !message.IsObject() {
			continue
		}
		role := message.Get("role").Str
		if role != "system" && role != "developer" {
			break
		}
		path := "messages." + jsonIndex(index) + ".content"
		content := message.Get("content")
		if content.Type == gjson.String {
			return prependStringField(document, path)
		}
		if content.IsArray() {
			return true, document.prependArrayValue(path, map[string]string{"type": "text", "text": placeholderNotice})
		}
		break
	}
	return true, document.prependArrayValue("messages", map[string]string{"role": "system", "content": placeholderNotice})
}

func prependGeminiSystemInstruction(document *jsonDocument) (bool, error) {
	instruction := gjson.GetBytes(document.body, "systemInstruction")
	if instruction.Type != gjson.Null && !instruction.IsObject() {
		return false, nil
	}
	parts := instruction.Get("parts")
	if contentContainsNotice(parts) {
		return false, nil
	}
	notice := map[string]string{"text": placeholderNotice}
	if !parts.IsArray() {
		return true, document.setValue("systemInstruction.parts", []any{notice})
	}
	return true, document.prependArrayValue("systemInstruction.parts", notice)
}

// Scan every system/developer message before choosing where to insert. Clients
// may add a new prefix ahead of an already annotated message on the next turn.
// User or assistant quotations do not count as system-channel instructions.
func messagesContainNotice(messages gjson.Result) bool {
	if messages.IsArray() {
		for _, message := range messages.Array() {
			role := message.Get("role").Str
			if (role == "system" || role == "developer") && contentContainsNotice(message.Get("content")) {
				return true
			}
		}
	}
	return false
}

func contentContainsNotice(content gjson.Result) bool {
	if content.Type == gjson.String {
		return noticeAlreadyPresent(content.Str)
	}
	if content.IsArray() {
		for _, block := range content.Array() {
			if text := block.Get("text"); text.Type == gjson.String && noticeAlreadyPresent(text.Str) {
				return true
			}
		}
	}
	return false
}

// SJSON replaces this array using its raw elements, so their whitespace,
// property order, numeric spelling and string escapes survive the insertion.
func (document *jsonDocument) prependArrayValue(path string, value any) error {
	existing := gjson.GetBytes(document.body, path)
	if !existing.IsArray() {
		return ErrUnsafeRewrite
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	item := bytes.TrimSuffix(encoded.Bytes(), []byte{'\n'})
	updated := make([]byte, 0, len(existing.Raw)+len(item)+1)
	updated = append(updated, '[')
	updated = append(updated, item...)
	if strings.TrimSpace(existing.Raw[1:len(existing.Raw)-1]) != "" {
		updated = append(updated, ',')
	}
	updated = append(updated, existing.Raw[1:]...)
	return document.setRaw(path, updated)
}
