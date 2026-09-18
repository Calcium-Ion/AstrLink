package ingress

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
)

var (
	previewURLPattern    = regexp.MustCompile(`https?://\S+`)
	previewSecretPattern = regexp.MustCompile(`(?i)\b(sk-[A-Za-z0-9_-]{8,}|bearer\s+[A-Za-z0-9._\-+/=]{8,}|api[_-]?key\s*[:=]\s*\S+)`)
	placeholderPattern   = regexp.MustCompile(`<PRIVATE_[A-Z0-9_]+>`)
	// previewNoisePattern is leading markdown that carries no words: heading
	// marks, block quotes, and bullets ("-" only when it is a bullet).
	previewNoisePattern = regexp.MustCompile(`^(?:[#>]+\s*|[*•·]\s*|-\s+)+`)
	// previewRolePattern is a transcript-style speaker label a client
	// prepends to the text: "[User]: …", "Human: …", "用户：…".
	previewRolePattern = regexp.MustCompile(`^(?:\[[^\[\]\n]{1,16}\]|(?i:user|human|assistant|system)|用户|人类)\s*[:：]\s*`)
)

// previewTargetRunes is where a preview is cut when the first line runs on.
// It leaves room for the ellipsis inside contract.MaxInputPreviewRunes.
const previewTargetRunes = 64

// previewMinSentenceRunes stops a sentence cut from producing a stub like
// "好。" when the first sentence is that short.
const previewMinSentenceRunes = 12

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

// sanitizePreview shapes the newest visible user text into a title: secrets
// and URLs are redacted, the first line that still has words is taken with
// its leading markup and speaker label removed, and a long line is cut at
// the end of its first sentence or, failing that, at a word or punctuation
// boundary with an ellipsis. Structure only — no phrase lists — so it holds
// across clients and languages.
func sanitizePreview(raw string) string {
	cleaned := previewURLPattern.ReplaceAllString(raw, "…")
	cleaned = previewSecretPattern.ReplaceAllString(cleaned, "…")
	cleaned = placeholderPattern.ReplaceAllString(cleaned, "…")
	if !utf8.ValidString(cleaned) {
		return ""
	}
	line := firstPreviewLine(cleaned)
	if line == "" {
		return ""
	}
	if sentence, ok := cutPreviewSentence(line); ok {
		return contract.ClampRunes(sentence, contract.MaxInputPreviewRunes)
	}
	return contract.ClampRunes(truncatePreview(line, previewTargetRunes), contract.MaxInputPreviewRunes)
}

// firstPreviewLine returns the first line that keeps words after markup and
// speaker labels are removed, with inner whitespace collapsed. Lines without
// a letter or digit (rules, fences, stray punctuation) are skipped.
func firstPreviewLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = previewNoisePattern.ReplaceAllString(line, "")
		line = previewRolePattern.ReplaceAllString(line, "")
		line = strings.Join(strings.Fields(line), " ")
		if strings.ContainsFunc(line, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) {
			return line
		}
	}
	return ""
}

// cutPreviewSentence returns the first sentence of a line that would
// otherwise be truncated, when that sentence is long enough to stand alone
// and short enough to fit the contract bound whole. A closing question or
// exclamation mark is kept; a full stop is dropped. ok is false when the
// line is short enough already or no such sentence end exists.
func cutPreviewSentence(line string) (string, bool) {
	runes := []rune(line)
	if len(runes) <= previewTargetRunes {
		return "", false
	}
	for index := previewMinSentenceRunes; index < len(runes) && index < contract.MaxInputPreviewRunes; index++ {
		switch runes[index] {
		case '。':
			return string(runes[:index]), true
		case '！', '？':
			return string(runes[:index+1]), true
		case '.', '!', '?':
			if index+1 < len(runes) && !unicode.IsSpace(runes[index+1]) {
				continue
			}
			if runes[index] == '.' {
				return string(runes[:index]), true
			}
			return string(runes[:index+1]), true
		}
	}
	return "", false
}

// truncatePreview cuts line to at most limit runes plus an ellipsis, backing
// up to the nearest whitespace or punctuation within the last quarter of the
// budget so words and clauses are not split; CJK text without either is cut
// at the limit.
func truncatePreview(line string, limit int) string {
	runes := []rune(line)
	if len(runes) <= limit {
		return line
	}
	cut := limit
	for index := limit; index > limit-limit/4; index-- {
		if unicode.IsSpace(runes[index]) || unicode.IsPunct(runes[index]) {
			cut = index
			break
		}
	}
	return strings.TrimRightFunc(string(runes[:cut]), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	}) + "…"
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
