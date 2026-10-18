// Package systemprompt adds text to the system prompt of an inference request
// body in place. The system prompt is the protocol's own instruction channel:
// Responses instructions, Anthropic system, the leading system or developer
// messages of a chat, and Gemini systemInstruction. Only the touched values are
// encoded; every other byte of the body is kept.
//
// An official client identity that opens the prompt stays first. Text added at
// the start goes after Claude Code's billing line and banner and after Droid's
// opening line, so an upstream that checks them finds them where the client
// put them and nothing adds them a second time.
package systemprompt

import (
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/jsonedit"
	"github.com/QuantumNous/astrlink/core/internal/providerapi"
	"github.com/tidwall/gjson"
)

// ClaudeCodeBanner opens Claude Code's system prompt.
const ClaudeCodeBanner = "You are Claude Code, Anthropic's official CLI for Claude."

// ClaudeCodeAttributionPrefix opens the block Claude Code sends before its
// banner. Upstream interprets this block only in the first system position.
const ClaudeCodeAttributionPrefix = "x-anthropic-billing-header:"

// separator joins added text to the prompt it extends.
const separator = "\n\n"

// Prepend adds text at the start of the system prompt, after an identity
// lead, and creates the channel when the body has none. It reports whether the
// body changed; a channel of an unexpected type is left alone.
func Prepend(protocol contract.ProtocolID, body []byte, text string) ([]byte, bool, error) {
	return add(protocol, body, text, true)
}

// Append adds text at the end of the system prompt, creating the channel when
// the body has none.
func Append(protocol contract.ProtocolID, body []byte, text string) ([]byte, bool, error) {
	return add(protocol, body, text, false)
}

func add(protocol contract.ProtocolID, body []byte, text string, atStart bool) ([]byte, bool, error) {
	if text == "" {
		return body, false, nil
	}
	switch protocol {
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		return addToString(body, "instructions", text, atStart)
	case contract.ProtocolAnthropicMessages:
		if system := gjson.GetBytes(body, "system"); system.IsArray() {
			return addToBlocks(body, "system", system, text, atStart, typedTextBlock)
		}
		return addToString(body, "system", text, atStart)
	case contract.ProtocolOpenAIChat:
		return addToChat(body, text, atStart)
	case contract.ProtocolGoogleGenerateContent:
		return addToGemini(body, text, atStart)
	default:
		// A raw completion has no system channel, and prefixing the prompt
		// would change what the model is asked to continue.
		return body, false, nil
	}
}

func addToString(body []byte, path, text string, atStart bool) ([]byte, bool, error) {
	existing := gjson.GetBytes(body, path)
	switch existing.Type {
	case gjson.Null:
		return inserted(jsonedit.Set(body, path, text))
	case gjson.String:
		return extendString(body, path, existing, text, atStart)
	default:
		return body, false, nil
	}
}

// extendString adds text to the JSON string at path. The client's own
// characters keep their bytes, escapes included; only the added text is
// encoded.
func extendString(body []byte, path string, existing gjson.Result, text string, atStart bool) ([]byte, bool, error) {
	if raw, ok := joinedRaw(existing, text, atStart); ok {
		return inserted(jsonedit.SetRaw(body, path, raw))
	}
	return inserted(jsonedit.Set(body, path, joined(existing.Str, text, atStart)))
}

// addToChat folds the text into the leading system or developer messages,
// the start into the first and the end into the last, so the cacheable prefix
// keeps its shape. Without one, a system message is inserted ahead of the
// conversation.
func addToChat(body []byte, text string, atStart bool) ([]byte, bool, error) {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body, false, nil
	}
	elements := messages.Array()
	first, last := -1, -1
	for index, message := range elements {
		if !message.IsObject() {
			continue
		}
		role := message.Get("role").Str
		if role != "system" && role != "developer" {
			break
		}
		if first < 0 {
			first = index
		}
		last = index
	}
	message := map[string]string{"role": "system", "content": text}
	if first < 0 {
		return inserted(jsonedit.Insert(body, "messages", 0, message))
	}
	target := first
	if !atStart {
		target = last
	}
	path := "messages." + strconv.Itoa(target) + ".content"
	content := elements[target].Get("content")
	switch {
	case content.Type == gjson.String:
		return extendString(body, path, content, text, atStart)
	case content.IsArray():
		return addToBlocks(body, path, content, text, atStart, typedTextBlock)
	case atStart:
		return inserted(jsonedit.Insert(body, "messages", 0, message))
	default:
		return inserted(jsonedit.Insert(body, "messages", target+1, message))
	}
}

func addToGemini(body []byte, text string, atStart bool) ([]byte, bool, error) {
	// Gemini rejects a system instruction beside cached content, which already
	// carries the cached prompt.
	for _, cached := range []string{"cachedContent", "cached_content"} {
		if value := gjson.GetBytes(body, cached); value.Exists() && value.Type != gjson.Null {
			return body, false, nil
		}
	}
	key := "systemInstruction"
	if !gjson.GetBytes(body, key).Exists() && gjson.GetBytes(body, "system_instruction").Exists() {
		key = "system_instruction"
	}
	instruction := gjson.GetBytes(body, key)
	if instruction.Type != gjson.Null && !instruction.IsObject() {
		return body, false, nil
	}
	parts := instruction.Get("parts")
	if !parts.IsArray() {
		return inserted(jsonedit.Set(body, key+".parts", []any{geminiTextPart(text)}))
	}
	return addToBlocks(body, key+".parts", parts, text, atStart, geminiTextPart)
}

// addToBlocks adds a text block to a block list. At the start it goes after
// the blocks that hold only an identity lead; when the lead opens a block that
// also holds the client's own prompt, the text joins that block after it.
func addToBlocks(
	body []byte,
	path string,
	blocks gjson.Result,
	text string,
	atStart bool,
	block func(string) any,
) ([]byte, bool, error) {
	elements := blocks.Array()
	if !atStart {
		return inserted(jsonedit.Insert(body, path, len(elements), block(text)))
	}
	index := 0
	for ; index < len(elements); index++ {
		existing, ok := blockText(elements[index])
		if !ok {
			break
		}
		lead := identityLead(existing)
		if lead == 0 {
			break
		}
		if strings.TrimSpace(existing[lead:]) != "" {
			textPath := path + "." + strconv.Itoa(index) + ".text"
			return extendString(body, textPath, elements[index].Get("text"), text, true)
		}
	}
	return inserted(jsonedit.Insert(body, path, index, block(text)))
}

func inserted(body []byte, err error) ([]byte, bool, error) {
	if err != nil {
		return nil, false, err
	}
	return body, true, nil
}

func typedTextBlock(text string) any {
	return map[string]string{"type": "text", "text": text}
}

func geminiTextPart(text string) any {
	return map[string]string{"text": text}
}

// blockText reads a text block: a typed Anthropic or chat block, or a Gemini
// part that carries only text.
func blockText(block gjson.Result) (string, bool) {
	if !block.IsObject() {
		return "", false
	}
	if kind := block.Get("type"); kind.Exists() && kind.Str != "text" {
		return "", false
	}
	text := block.Get("text")
	return text.Str, text.Type == gjson.String
}

// joined adds text to existing at the requested end. At the start, the text
// goes after the identity lead. An empty existing prompt is replaced.
func joined(existing, text string, atStart bool) string {
	at, added := insertion(existing, text, atStart)
	return existing[:at] + added + existing[at:]
}

// insertion is where text joins existing, and the text with its separators.
func insertion(existing, text string, atStart bool) (int, string) {
	if existing == "" {
		return 0, text
	}
	if !atStart {
		return len(existing), separator + text
	}
	lead := identityLead(existing)
	if lead == 0 {
		return 0, text + separator
	}
	rest := existing[lead:]
	if rest == "" || rest[0] == '\n' || rest[0] == '\r' {
		// Keep the client's own break between the identity and its prompt.
		return lead, separator + text
	}
	return lead, separator + text + separator
}

// joinedRaw is joined applied to a raw JSON string: the added text is encoded
// and spliced between the client's raw bytes.
func joinedRaw(existing gjson.Result, text string, atStart bool) ([]byte, bool) {
	raw := existing.Raw
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return nil, false
	}
	content := raw[1 : len(raw)-1]
	at, added := insertion(existing.Str, text, atStart)
	rawAt, ok := 0, true
	switch at {
	case 0:
	case len(existing.Str):
		rawAt = len(content)
	default:
		rawAt, ok = rawOffset(content, at)
	}
	if !ok {
		return nil, false
	}
	encoded, err := jsonedit.Encode(added)
	if err != nil {
		return nil, false
	}
	if existing.Str == "" {
		return encoded, true
	}
	spliced := make([]byte, 0, len(raw)+len(encoded))
	spliced = append(spliced, '"')
	spliced = append(spliced, content[:rawAt]...)
	spliced = append(spliced, encoded[1:len(encoded)-1]...)
	spliced = append(spliced, content[rawAt:]...)
	return append(spliced, '"'), true
}

// rawOffset maps a byte offset in a decoded JSON string to the offset in its
// raw content, between characters.
func rawOffset(content string, decoded int) (int, bool) {
	count := 0
	for index := 0; index < len(content); {
		if count == decoded {
			return index, true
		}
		if count > decoded {
			return 0, false
		}
		if content[index] != '\\' {
			count++
			index++
			continue
		}
		if index+1 >= len(content) {
			return 0, false
		}
		if content[index+1] != 'u' {
			count++
			index += 2
			continue
		}
		r, size := escapedRune(content[index:])
		if size == 0 {
			return 0, false
		}
		count += utf8.RuneLen(r)
		index += size
	}
	return len(content), count == decoded
}

// escapedRune decodes a \uXXXX escape the way gjson does: a surrogate
// followed by another escape consumes both, and an invalid pair or a lone
// surrogate reads as U+FFFD.
func escapedRune(escape string) (rune, int) {
	value := func(text string) (rune, bool) {
		if len(text) < 6 || text[0] != '\\' || text[1] != 'u' {
			return 0, false
		}
		number, err := strconv.ParseUint(text[2:6], 16, 32)
		return rune(number), err == nil
	}
	first, ok := value(escape)
	if !ok {
		return 0, 0
	}
	if !utf16.IsSurrogate(first) {
		return first, 6
	}
	if second, ok := value(escape[6:]); ok {
		return utf16.DecodeRune(first, second), 12
	}
	return utf8.RuneError, 6
}

// identityLead is the length of the official client identity that opens text:
// Claude Code's billing line and banner, or Droid's opening line. Consecutive
// identity lines count as one lead; it ends right after the last of them.
func identityLead(text string) int {
	lead := identityLine(text)
	if lead == 0 {
		return 0
	}
	for {
		rest := strings.TrimLeft(text[lead:], "\r\n")
		next := identityLine(rest)
		if next == 0 {
			return lead
		}
		lead = len(text) - len(rest) + next
	}
}

func identityLine(text string) int {
	if strings.HasPrefix(text, ClaudeCodeAttributionPrefix) {
		if end := strings.IndexAny(text, "\r\n"); end >= 0 {
			return end
		}
		return len(text)
	}
	for _, line := range [...]string{ClaudeCodeBanner, providerapi.DroidSystemPromptLine} {
		if strings.HasPrefix(text, line) && (len(text) == len(line) || isSpace(text[len(line)])) {
			return len(line)
		}
	}
	return 0
}

func isSpace(character byte) bool {
	return character == ' ' || character == '\t' || character == '\n' || character == '\r'
}
