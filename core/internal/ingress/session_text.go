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

// extractProtocolCursor reads a string field, or the id of an object field,
// as a bounded cursor. Conversation-level cursors (conversation, metadata,
// prompt_cache_key, container, cachedContent) are extracted by the convo
// adapters; this only serves the official previous_response_id chain.
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
	// Transport error text may keep host/URL (ADR 0016); only credentials
	// and placeholder originals are stripped.
	cleaned := redactTransportSecrets(raw)
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
