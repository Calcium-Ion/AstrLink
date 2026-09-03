package convo

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/convo/internal/jsonx"
)

// harnessPrefixes mark user-role text that an agent harness injected rather
// than a person typed: whole-message notes and context-compaction summaries
// that replace the earlier history. Matching is case-insensitive on the
// trimmed prefix, after harness tag blocks have been stripped.
var harnessPrefixes = []string{
	"the following deferred tools",
	"available agent types",
	"x-anthropic-billing-header",
	"continue with the original user request",
	// Pi / Paseo compaction.
	"the conversation history before this point",
	// Claude Code compaction.
	"this session is being continued from a previous conversation",
}

// harnessTags are XML-ish wrappers a harness prepends or appends to the text a
// person typed. Their content is never the user's words, so a leading or
// trailing block is removed before the message is judged. The list is closed
// on purpose: many clients wrap the user's own text in tags (Cursor's
// <user_query>, for one), and stripping those would erase the turn.
var harnessTags = []string{
	"system-reminder",
	"skill",
}

// visibleText strips harness tag blocks from both ends of text and returns
// "" when nothing a person typed remains.
func visibleText(raw string) string {
	text := stripHarnessBlocks(raw)
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	for _, prefix := range harnessPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}
	return text
}

// stripHarnessBlocks removes every leading and trailing <tag ...>...</tag>
// block whose tag is in harnessTags and trims the remainder. An unterminated
// block is left alone: guessing where it ends risks dropping real text.
func stripHarnessBlocks(raw string) string {
	text := strings.TrimSpace(raw)
	for text != "" {
		rest, ok := stripLeadingHarnessBlock(text)
		if !ok {
			break
		}
		text = strings.TrimSpace(rest)
	}
	for text != "" {
		rest, ok := stripTrailingHarnessBlock(text)
		if !ok {
			break
		}
		text = strings.TrimSpace(rest)
	}
	return text
}

func stripLeadingHarnessBlock(text string) (string, bool) {
	for _, tag := range harnessTags {
		if !hasOpenTag(text, tag) {
			continue
		}
		closing := "</" + tag + ">"
		end := strings.Index(text, closing)
		if end < 0 {
			return text, false
		}
		return text[end+len(closing):], true
	}
	return text, false
}

func stripTrailingHarnessBlock(text string) (string, bool) {
	for _, tag := range harnessTags {
		closing := "</" + tag + ">"
		if !strings.HasSuffix(text, closing) {
			continue
		}
		body := text[:len(text)-len(closing)]
		start := strings.LastIndex(body, "<"+tag)
		for start >= 0 && !hasOpenTag(body[start:], tag) {
			start = strings.LastIndex(body[:start], "<"+tag)
		}
		if start < 0 {
			return text, false
		}
		return text[:start], true
	}
	return text, false
}

// hasOpenTag reports whether text starts with <tag> or <tag followed by an
// attribute, so <skill> matches but <skillful> does not.
func hasOpenTag(text, tag string) bool {
	open := "<" + tag
	if !strings.HasPrefix(text, open) {
		return false
	}
	rest := text[len(open):]
	if rest == "" {
		return false
	}
	switch rest[0] {
	case '>', ' ', '\t', '\n', '\r', '/':
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
