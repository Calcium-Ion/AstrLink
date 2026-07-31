package privacy

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

// RestorePlaceholders replaces exact request-scoped placeholders with their
// original values. Values are JSON-string-escaped before substitution; if the
// escaped form differs from the raw value (quotes, backslashes, controls), that
// placeholder is left unrestored so the surrounding JSON stays well-formed.
func RestorePlaceholders(data []byte, redactions []Redaction) []byte {
	if len(data) == 0 || len(redactions) == 0 {
		return data
	}
	replacements := prepareRestoreReplacements(redactions)
	if len(replacements) == 0 {
		return data
	}
	return applyRestoreReplacements(data, replacements)
}

type restoreReplacement struct {
	placeholder string
	value       []byte
}

func prepareRestoreReplacements(redactions []Redaction) []restoreReplacement {
	replacements := make([]restoreReplacement, 0, len(redactions))
	seen := make(map[string]struct{}, len(redactions))
	for _, redaction := range redactions {
		if redaction.Placeholder == "" {
			continue
		}
		if _, exists := seen[redaction.Placeholder]; exists {
			continue
		}
		seen[redaction.Placeholder] = struct{}{}
		escaped, ok := jsonStringContent(redaction.Value)
		if !ok {
			continue
		}
		replacements = append(replacements, restoreReplacement{
			placeholder: redaction.Placeholder,
			value:       escaped,
		})
	}
	sort.Slice(replacements, func(left, right int) bool {
		return len(replacements[left].placeholder) > len(replacements[right].placeholder)
	})
	return replacements
}

func jsonStringContent(value string) ([]byte, bool) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) < 2 || encoded[0] != '"' || encoded[len(encoded)-1] != '"' {
		return nil, false
	}
	content := encoded[1 : len(encoded)-1]
	if string(content) != value {
		// Value needs JSON escaping; skip rather than corrupt wire JSON.
		return nil, false
	}
	return []byte(value), true
}

func applyRestoreReplacements(data []byte, replacements []restoreReplacement) []byte {
	for _, replacement := range replacements {
		data = bytes.ReplaceAll(data, []byte(replacement.placeholder), replacement.value)
	}
	return data
}

// CarryRestorer restores placeholders across arbitrary chunks within one
// logical text value. Protocol framing and cross-event text channels are owned
// by ingress's protocol-aware response writer.
type CarryRestorer struct {
	replacements []restoreReplacement
	maxPrefix    int
	pending      []byte
}

func NewCarryRestorer(redactions []Redaction) *CarryRestorer {
	replacements := prepareRestoreReplacements(redactions)
	maxPrefix := 0
	for _, replacement := range replacements {
		if length := len(replacement.placeholder); length > maxPrefix {
			maxPrefix = length
		}
	}
	return &CarryRestorer{replacements: replacements, maxPrefix: maxPrefix}
}

func (restorer *CarryRestorer) Push(chunk []byte) []byte {
	if restorer == nil || (len(restorer.replacements) == 0 && len(restorer.pending) == 0) {
		return chunk
	}
	if len(chunk) == 0 {
		return nil
	}
	combined := chunk
	if len(restorer.pending) > 0 {
		combined = append(append([]byte{}, restorer.pending...), chunk...)
		restorer.pending = nil
	}
	restored := applyRestoreReplacements(combined, restorer.replacements)
	if restorer.maxPrefix <= 1 {
		return restored
	}
	hold := trailingPlaceholderPrefix(restored, restorer.replacements, restorer.maxPrefix)
	if hold == 0 {
		return restored
	}
	restorer.pending = append([]byte{}, restored[len(restored)-hold:]...)
	return restored[:len(restored)-hold]
}

func (restorer *CarryRestorer) Flush() []byte {
	if restorer == nil || len(restorer.pending) == 0 {
		return nil
	}
	pending := restorer.pending
	restorer.pending = nil
	return applyRestoreReplacements(pending, restorer.replacements)
}

func trailingPlaceholderPrefix(data []byte, replacements []restoreReplacement, maxPrefix int) int {
	limit := maxPrefix - 1
	if limit <= 0 || len(data) == 0 {
		return 0
	}
	if len(data) < limit {
		limit = len(data)
	}
	for hold := limit; hold > 0; hold-- {
		suffix := data[len(data)-hold:]
		for _, replacement := range replacements {
			placeholder := []byte(replacement.placeholder)
			if len(placeholder) > hold && bytes.HasPrefix(placeholder, suffix) {
				return hold
			}
		}
	}
	return 0
}

// ShouldRestoreContentType reports whether response bytes of this media type
// are eligible for placeholder restoration.
func ShouldRestoreContentType(contentType string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch mediaType {
	case "application/json", "text/event-stream", "text/plain":
		return true
	default:
		return strings.HasPrefix(mediaType, "application/json") ||
			strings.Contains(mediaType, "+json")
	}
}
