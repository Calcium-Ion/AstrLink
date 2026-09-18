package convo

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/convo/internal/jsonx"
)

// droppedTags name the one wrapper whose content is never the user's words and
// whose presence must not make a message count: Claude Code appends
// <system-reminder> blocks to tool results and to typed prompts alike, so
// without this rule every tool result would be a turn. The list is one entry
// on purpose. Every other harness convention (skill injections, compaction
// summaries, attached-file wrappers) differs per client and per version, so
// it is handled structurally by unwrapping instead of by name.
var droppedTags = map[string]bool{"system-reminder": true}

// maxUnwrapDepth bounds how many nested wrappers visibleText opens.
const maxUnwrapDepth = 4

// visibleText returns the words a person typed in one user message.
//
// Dropped blocks are removed from both ends. Then any closed <tag …>…</tag>
// blocks at either end are peeled: text left outside them is the user's
// words (a prompt after a <skill> injection, a note before a pasted <pre>).
// When nothing is left the message was wholly wrapped and the last block in
// document order is opened and judged the same way, so Cursor's
// <additional_data>…</additional_data><user_query>q</user_query> yields q.
// Unterminated tags are left alone: guessing where they end risks dropping
// real text.
func visibleText(raw string) string {
	return visibleTextDepth(raw, maxUnwrapDepth)
}

func visibleTextDepth(raw string, depth int) string {
	_, text := peelBlocks(raw, func(name string) bool { return droppedTags[name] })
	if text == "" || depth == 0 {
		return text
	}
	blocks, rest := peelBlocks(text, func(string) bool { return true })
	if rest != "" || len(blocks) == 0 {
		return rest
	}
	return visibleTextDepth(blocks[len(blocks)-1], depth-1)
}

// peelBlocks removes every leading and trailing closed block whose tag name
// satisfies accept and returns the removed inner texts in document order plus
// the trimmed remainder.
func peelBlocks(raw string, accept func(name string) bool) (blocks []string, rest string) {
	text := strings.TrimSpace(raw)
	var trailing []string
	for text != "" {
		inner, after, ok := leadingBlock(text, accept)
		if !ok {
			break
		}
		blocks = append(blocks, inner)
		text = strings.TrimSpace(after)
	}
	for text != "" {
		inner, before, ok := trailingBlock(text, accept)
		if !ok {
			break
		}
		trailing = append(trailing, inner)
		text = strings.TrimSpace(before)
	}
	for index := len(trailing) - 1; index >= 0; index-- {
		blocks = append(blocks, trailing[index])
	}
	return blocks, text
}

// leadingBlock matches <name …>inner</name> at the start of text.
func leadingBlock(text string, accept func(string) bool) (inner, rest string, ok bool) {
	name, innerStart, ok := openTagAt(text, 0)
	if !ok || !accept(name) {
		return "", "", false
	}
	closeStart, closeEnd, ok := matchingClose(text, name, innerStart)
	if !ok {
		return "", "", false
	}
	return text[innerStart:closeStart], text[closeEnd:], true
}

// trailingBlock matches <name …>inner</name> at the end of text.
func trailingBlock(text string, accept func(string) bool) (inner, rest string, ok bool) {
	if !strings.HasSuffix(text, ">") {
		return "", "", false
	}
	closeStart := strings.LastIndex(text, "</")
	if closeStart < 0 {
		return "", "", false
	}
	name := text[closeStart+2 : len(text)-1]
	if !validTagName(name) || !accept(name) {
		return "", "", false
	}
	openStart, innerStart, ok := matchingOpen(text, name, closeStart)
	if !ok {
		return "", "", false
	}
	return text[innerStart:closeStart], text[:openStart], true
}

// matchingClose finds the </name> that closes the block whose content starts
// at from, honouring nested blocks of the same name.
func matchingClose(text, name string, from int) (closeStart, closeEnd int, ok bool) {
	depth := 1
	closing := "</" + name + ">"
	for index := from; index < len(text); {
		next := strings.IndexByte(text[index:], '<')
		if next < 0 {
			return 0, 0, false
		}
		index += next
		if strings.HasPrefix(text[index:], closing) {
			depth--
			if depth == 0 {
				return index, index + len(closing), true
			}
			index += len(closing)
			continue
		}
		if opened, innerStart, isOpen := openTagAt(text, index); isOpen && opened == name {
			depth++
			index = innerStart
			continue
		}
		index++
	}
	return 0, 0, false
}

// matchingOpen finds the <name …> that the closing tag at closeStart pairs
// with, honouring nested blocks of the same name.
func matchingOpen(text, name string, closeStart int) (openStart, innerStart int, ok bool) {
	type opener struct{ start, innerStart int }
	var stack []opener
	closing := "</" + name + ">"
	for index := 0; index < closeStart; {
		next := strings.IndexByte(text[index:closeStart], '<')
		if next < 0 {
			break
		}
		index += next
		if strings.HasPrefix(text[index:], closing) {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			index += len(closing)
			continue
		}
		if opened, inner, isOpen := openTagAt(text, index); isOpen && opened == name {
			if inner > closeStart {
				// The opener's '>' is the closing tag's own: "<a </a>".
				break
			}
			stack = append(stack, opener{index, inner})
			index = inner
			continue
		}
		index++
	}
	if len(stack) == 0 {
		return 0, 0, false
	}
	top := stack[len(stack)-1]
	return top.start, top.innerStart, true
}

// openTagAt parses <name attrs…> at text[at:]. Self-closing tags and tags
// with an invalid name are not openers.
func openTagAt(text string, at int) (name string, innerStart int, ok bool) {
	if at >= len(text) || text[at] != '<' {
		return "", 0, false
	}
	end := at + 1
	for end < len(text) && isTagNameByte(text[end], end > at+1) {
		end++
	}
	name = text[at+1 : end]
	if name == "" || end >= len(text) {
		return "", 0, false
	}
	switch text[end] {
	case '>', ' ', '\t', '\n', '\r', '/':
	default:
		return "", 0, false
	}
	close := strings.IndexByte(text[end:], '>')
	if close < 0 {
		return "", 0, false
	}
	innerStart = end + close + 1
	if strings.HasSuffix(text[at:innerStart], "/>") {
		return "", 0, false
	}
	return name, innerStart, true
}

func validTagName(name string) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		if !isTagNameByte(name[index], index > 0) {
			return false
		}
	}
	return true
}

func isTagNameByte(char byte, notFirst bool) bool {
	switch {
	case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char == '_':
		return true
	case notFirst && (char >= '0' && char <= '9' || char == '-' || char == '.' || char == ':'):
		return true
	}
	return false
}

// joinVisible joins non-empty visible texts with sep.
func joinVisible(parts []string, sep string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if text := visibleText(part); text != "" {
			kept = append(kept, text)
		}
	}
	return strings.Join(kept, sep)
}

// truncateBytes cuts text to at most max bytes on a rune boundary.
func truncateBytes(text string, max int) string {
	if max <= 0 || len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// appendExplicit appends a cleaned, deduplicated cursor value.
func appendExplicit(values []string, value string) []string {
	value = clampCursorValue(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// genericExplicitCursors collects the conversation identifiers that OpenAI-
// shaped protocols and their compatible servers accept, most trusted first:
// conversation_id, conversation, metadata.session_id, metadata.conversation_id,
// the session segment of Claude Code's metadata.user_id, and prompt_cache_key.
func genericExplicitCursors(fields map[string]json.RawMessage) []string {
	var values []string
	if id, ok := jsonx.StringOrID(fields["conversation_id"]); ok {
		values = appendExplicit(values, id)
	}
	if id, ok := jsonx.StringOrID(fields["conversation"]); ok {
		values = appendExplicit(values, id)
	}
	values = appendMetadataCursors(values, fields["metadata"])
	if id, ok := jsonx.String(fields["prompt_cache_key"]); ok {
		values = appendExplicit(values, id)
	}
	return values
}

func appendMetadataCursors(values []string, raw json.RawMessage) []string {
	metadata, ok := jsonx.Object(raw)
	if !ok {
		return values
	}
	if id, ok := jsonx.StringOrID(metadata["session_id"]); ok {
		values = appendExplicit(values, id)
	}
	if id, ok := jsonx.StringOrID(metadata["conversation_id"]); ok {
		values = appendExplicit(values, id)
	}
	if id := claudeSessionFromUserID(metadata["user_id"]); id != "" {
		values = appendExplicit(values, id)
	}
	return values
}

// claudeSessionFromUserID pulls the per-chat session out of Claude Code's
// metadata.user_id, which is documented only as an abuse-tracking id but in
// practice encodes a session:
//
//	user_{hash}_account_{uuid}_session_{uuid}
//	{"device_id":"...","account_uuid":"...","session_id":"..."}
//
// A bare user hash is never used: it would merge every chat of one account.
func claudeSessionFromUserID(raw json.RawMessage) string {
	if object, ok := jsonx.Object(raw); ok {
		id, _ := jsonx.String(object["session_id"])
		return strings.TrimSpace(id)
	}
	value, ok := jsonx.String(raw)
	if !ok {
		return ""
	}
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "{") {
		var payload struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal([]byte(value), &payload) == nil {
			return strings.TrimSpace(payload.SessionID)
		}
		return ""
	}
	for _, marker := range []string{"__session_", "_session_"} {
		if index := strings.LastIndex(value, marker); index >= 0 {
			return strings.TrimSpace(value[index+len(marker):])
		}
	}
	return ""
}

// stringOrTextParts handles the "content is either a string or an array of
// typed parts" shape shared by every protocol. textTypes lists the part
// types whose "text" member is visible text; any other part type counts as a
// non-text attachment (image, file, audio) and is reported through hasMedia.
func stringOrTextParts(raw json.RawMessage, textTypes map[string]bool, skipTypes map[string]bool) (texts []string, hasMedia bool) {
	if value, ok := jsonx.String(raw); ok {
		return []string{value}, false
	}
	items, ok := jsonx.Array(raw)
	if !ok {
		return nil, false
	}
	for _, item := range items {
		part, ok := jsonx.Object(item)
		if !ok {
			if value, ok := jsonx.String(item); ok {
				texts = append(texts, value)
			}
			continue
		}
		partType, _ := jsonx.String(part["type"])
		if skipTypes[partType] {
			continue
		}
		if partType == "" || textTypes[partType] {
			if text, ok := jsonx.String(part["text"]); ok {
				texts = append(texts, text)
				continue
			}
			if partType == "" {
				continue
			}
		}
		hasMedia = true
	}
	return texts, hasMedia
}
