package ingress

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
)

var (
	previewURLPattern    = regexp.MustCompile(`https?://\S+`)
	previewSecretPattern = regexp.MustCompile(`(?i)\b(sk-[A-Za-z0-9_-]{8,}|bearer\s+[A-Za-z0-9._\-+/=]{8,}|api[_-]?key\s*[:=]\s*\S+)`)
	placeholderPattern   = regexp.MustCompile(`<PRIVATE_[A-Z0-9_]+>`)
)

func extractProtocolCursor(fields map[string]json.RawMessage, key string) string {
	raw, ok := fields[key]
	if !ok {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return clampCursor(strings.TrimSpace(value))
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return ""
	}
	idRaw, ok := object["id"]
	if !ok {
		return ""
	}
	if json.Unmarshal(idRaw, &value) != nil {
		return ""
	}
	return clampCursor(strings.TrimSpace(value))
}

func extractConversationCursor(fields map[string]json.RawMessage) string {
	if id := extractProtocolCursor(fields, "conversation_id"); id != "" {
		return id
	}
	if id := extractProtocolCursor(fields, "conversation"); id != "" {
		return id
	}
	if raw, ok := fields["metadata"]; ok {
		var metadata map[string]json.RawMessage
		if json.Unmarshal(raw, &metadata) == nil {
			if id := extractProtocolCursor(metadata, "session_id"); id != "" {
				return id
			}
			if id := extractProtocolCursor(metadata, "conversation_id"); id != "" {
				return id
			}
			if id := parseClaudeMetadataUserIDRaw(metadata["user_id"]); id != "" {
				return id
			}
		}
	}
	// Cherry Agent / Codex CLI pin a conversation on prompt_cache_key when
	// they replay full input and omit previous_response_id. Official
	// Responses chains still prefer previous_response_id in resolveSession.
	return extractProtocolCursor(fields, "prompt_cache_key")
}

// parseClaudeMetadataUserID pulls the conversation session out of Claude Code's
// metadata.user_id. Official Anthropic only documents this as an abuse-tracking
// user id; Claude Code / Claude CLI encode a per-chat session inside it:
//
//	user_{hash}_account_{uuid}_session_{uuid}
//	{"device_id":"...","account_uuid":"...","session_id":"..."}
//
// A bare user hash is not used as a session key — that would merge every chat
// from the same account.
func parseClaudeMetadataUserIDRaw(raw json.RawMessage) string {
	if id := parseClaudeMetadataUserID(jsonString(raw)); id != "" {
		return id
	}
	var payload struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(raw, &payload) == nil {
		return clampCursor(strings.TrimSpace(payload.SessionID))
	}
	return ""
}

func parseClaudeMetadataUserID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "{") {
		var payload struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal([]byte(raw), &payload) == nil {
			return clampCursor(strings.TrimSpace(payload.SessionID))
		}
	}
	for _, marker := range []string{"_session_", "__session_"} {
		if index := strings.LastIndex(raw, marker); index >= 0 {
			return clampCursor(strings.TrimSpace(raw[index+len(marker):]))
		}
	}
	return ""
}

func extractInputPreview(fields map[string]json.RawMessage) string {
	if raw, ok := fields["input"]; ok {
		if text := firstUserText(raw); text != "" {
			return sanitizePreview(text)
		}
	}
	if raw, ok := fields["messages"]; ok {
		if text := firstUserText(raw); text != "" {
			return sanitizePreview(text)
		}
	}
	if raw, ok := fields["contents"]; ok {
		if text := firstUserText(raw); text != "" {
			return sanitizePreview(text)
		}
	}
	return ""
}

func firstUserText(raw json.RawMessage) string {
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return strings.TrimSpace(asString)
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return extractTextValue(raw)
	}
	for _, item := range items {
		if text := userItemText(item); text != "" {
			return text
		}
	}
	return ""
}

func userItemText(raw json.RawMessage) string {
	var item map[string]json.RawMessage
	if json.Unmarshal(raw, &item) != nil {
		return extractTextValue(raw)
	}
	role := jsonString(item["role"])
	if role != "" && role != "user" {
		return ""
	}
	if text := firstVisibleUserText(item["content"]); text != "" {
		return text
	}
	if text := extractTextValue(item["parts"]); text != "" {
		return text
	}
	if text := jsonString(item["text"]); text != "" {
		return text
	}
	return ""
}

func firstVisibleUserText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return visibleUserText(asString)
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) == nil {
		for _, item := range items {
			if text := firstVisibleUserText(item); text != "" {
				return text
			}
		}
		return ""
	}
	return visibleUserText(extractTextValue(raw))
}

func visibleUserText(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	switch {
	case strings.HasPrefix(lower, "<system-reminder>"),
		strings.HasPrefix(lower, "the following deferred tools"),
		strings.HasPrefix(lower, "available agent types"),
		strings.HasPrefix(lower, "x-anthropic-billing-header"),
		strings.HasPrefix(lower, "continue with the original user request"):
		return ""
	}
	return text
}

func extractTextValue(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return strings.TrimSpace(asString)
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) == nil {
		for _, item := range items {
			if text := extractTextValue(item); text != "" {
				return text
			}
		}
		return ""
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return ""
	}
	for _, key := range []string{"text", "input_text", "content"} {
		if text := jsonString(object[key]); text != "" {
			return text
		}
	}
	return ""
}

func jsonString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func sanitizePreview(raw string) string {
	cleaned := previewURLPattern.ReplaceAllString(raw, "…")
	cleaned = previewSecretPattern.ReplaceAllString(cleaned, "…")
	cleaned = placeholderPattern.ReplaceAllString(cleaned, "…")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	cleaned = contract.ClampRunes(cleaned, contract.MaxInputPreviewRunes)
	if cleaned == "" || !utf8.ValidString(cleaned) {
		return ""
	}
	return cleaned
}

func sanitizeSummary(raw string) string {
	cleaned := previewURLPattern.ReplaceAllString(raw, "…")
	cleaned = previewSecretPattern.ReplaceAllString(cleaned, "…")
	cleaned = placeholderPattern.ReplaceAllString(cleaned, "…")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	return contract.ClampRunes(cleaned, contract.MaxEventSummaryRunes)
}

func clampCursor(value string) string {
	if value == "" {
		return ""
	}
	value = contract.ClampRunes(value, contract.MaxProtocolCursorRunes)
	if strings.ContainsAny(value, "\x00\n\r") {
		return ""
	}
	return value
}
