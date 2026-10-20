package privacy

import (
	"bytes"
	"encoding/json"
	"html"
	"strings"
)

// ValueEncoding says how a restored value must be written into the text being
// restored.
type ValueEncoding uint8

const (
	// ValuePlain targets decoded text whose surrounding syntax is re-encoded by
	// the caller.
	ValuePlain ValueEncoding = iota
	// ValueJSONString targets text that itself holds JSON, such as a serialized
	// tool-argument payload, so every value must arrive escaped for a JSON
	// string literal.
	ValueJSONString
)

// TextRestorer puts original values back in place of the placeholders minted
// for one request.
//
// A model does not always echo a placeholder byte for byte. It writes text in
// the syntax of whatever it is producing: an HTML page escapes the angle
// brackets of a marker, and a JSON or URL encoder escapes them differently.
// Each target is therefore matched in every spelling that still identifies it
// unambiguously, and the value is written back in the same syntax, so restoring
// into an HTML page cannot inject markup and restoring into a URL cannot break
// it.
type TextRestorer struct {
	spellings []restoreSpelling
	indexes   [restoreGuardCount]restorePrefixIndex
	mappings  int
}

type matchStatus uint8

const (
	matchNone matchStatus = iota
	// matchPartial means the input ends inside the spelling, so more text could
	// still complete it.
	matchPartial
	matchFull
)

type restoreSpelling struct {
	prefixes []string
	guard    restoreGuard
	value    string
	match    func(input string, at int, final bool) (int, matchStatus)
}

// NewTextRestorer builds a restorer for the request's redactions. A redaction
// whose value cannot be written in the requested encoding is skipped rather
// than substituted lossily.
func NewTextRestorer(redactions []Redaction, encoding ValueEncoding) *TextRestorer {
	restorer := &TextRestorer{}
	seen := make(map[string]struct{}, len(redactions))
	for _, redaction := range redactions {
		if redaction.Placeholder == "" {
			continue
		}
		if _, ok := seen[redaction.Placeholder]; ok {
			continue
		}
		seen[redaction.Placeholder] = struct{}{}
		restorer.mappings++
		spellings, ok := restoreSpellings(redaction.Placeholder, redaction.Value, encoding)
		if !ok {
			continue
		}
		for _, spelling := range spellings {
			index := len(restorer.spellings)
			restorer.spellings = append(restorer.spellings, spelling)
			for _, prefix := range spelling.prefixes {
				restorer.indexes[spelling.guard].add(prefix, index)
			}
		}
	}
	for index := range restorer.indexes {
		restorer.indexes[index].sort()
	}
	return restorer
}

// Len reports the number of distinct placeholders the restorer was built from.
func (restorer *TextRestorer) Len() int {
	if restorer == nil {
		return 0
	}
	return restorer.mappings
}

// Restore replaces every recognised spelling in input. Unless final is set, a
// spelling that the end of input leaves incomplete is held back: restored then
// covers only input[:len(input)-hold], and the caller must resubmit the held
// tail together with the text that follows it.
func (restorer *TextRestorer) Restore(input string, final bool) (restored string, count, hold int) {
	if restorer == nil || len(restorer.spellings) == 0 || input == "" {
		return input, 0, 0
	}
	var out strings.Builder
	flushed := 0
	for at := 0; at < len(input); at++ {
		bestEnd := -1
		bestIndex := -1
		for guard := range restorer.indexes {
			index := &restorer.indexes[guard]
			if len(index.byStart[input[at]]) == 0 || !restoreGuard(guard).allows(input, at) {
				continue
			}
			candidate, end, partial := index.match(input, at, final, restorer.spellings)
			if partial {
				out.WriteString(input[flushed:at])
				return out.String(), count, len(input) - at
			}
			if end > bestEnd || (end == bestEnd && candidate >= 0 && candidate < bestIndex) {
				bestEnd, bestIndex = end, candidate
			}
		}
		if bestIndex < 0 {
			continue
		}
		out.WriteString(input[flushed:at])
		out.WriteString(restorer.spellings[bestIndex].value)
		flushed = bestEnd
		at = bestEnd - 1
		count++
	}
	if count == 0 {
		return input, 0, 0
	}
	out.WriteString(input[flushed:])
	return out.String(), count, 0
}

// restoreSpellings lists the forms a placeholder is recognised in, each paired
// with the value written in that form's syntax.
func restoreSpellings(placeholder, value string, encoding ValueEncoding) ([]restoreSpelling, bool) {
	encoded, ok := encodeRestoredValue(value, encoding)
	if !ok {
		return nil, false
	}
	spellings := []restoreSpelling{literalSpelling(placeholder, encoded)}
	if body, ok := markerBody(placeholder); ok {
		spellings = append(spellings, bareMarkerSpelling(body, encoded))
		if htmlValue, ok := encodeRestoredValue(html.EscapeString(value), encoding); ok {
			spellings = append(spellings,
				escapedMarkerSpelling(htmlMarkerEscapes, body, htmlValue, false))
		}
		// In decoded text a \u003c is literal, so the value is written for the
		// JSON document the model is producing. In serialized JSON the escape
		// decodes to the marker itself, which needs the same escaped value.
		if jsonValue, ok := EscapeJSONStringContent(value); ok {
			spellings = append(spellings,
				escapedMarkerSpelling(unicodeMarkerEscapes, body, jsonValue, true))
		}
		if percentValue, ok := encodeRestoredValue(percentEncode(value), encoding); ok {
			spellings = append(spellings,
				escapedMarkerSpelling(percentMarkerEscapes, body, percentValue, false))
		}
	}
	return spellings, true
}

func encodeRestoredValue(value string, encoding ValueEncoding) (string, bool) {
	if encoding == ValueJSONString {
		return EscapeJSONStringContent(value)
	}
	return value, true
}

// EscapeJSONStringContent returns the value as it must appear inside a JSON
// string literal. A value that does not survive the round trip (invalid UTF-8)
// is refused rather than silently substituted with replacement characters.
func EscapeJSONStringContent(value string) (string, bool) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	// The value lands in text the model wrote, which spells & < > literally.
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", false
	}
	encoded := bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
	var round string
	if err := json.Unmarshal(encoded, &round); err != nil || round != value || len(encoded) < 2 {
		return "", false
	}
	return string(encoded[1 : len(encoded)-1]), true
}

// markerBody returns the text between the angle brackets of a suffixed token
// marker. Legacy fixed markers are excluded: without the derived suffix their
// body is an ordinary word that genuine text may contain.
func markerBody(placeholder string) (string, bool) {
	if len(placeholder) < 3 || placeholder[0] != '<' || placeholder[len(placeholder)-1] != '>' {
		return "", false
	}
	body := placeholder[1 : len(placeholder)-1]
	separator := strings.LastIndexByte(body, '_')
	if separator <= 0 || len(body)-separator-1 != placeholderTokenHexLength {
		return "", false
	}
	for _, character := range []byte(body[separator+1:]) {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return "", false
		}
	}
	return body, true
}

var (
	htmlMarkerEscapes = newMarkerEscapes(
		[]string{"&lt;", "&#60;", "&#x3c;"}, []string{"&gt;", "&#62;", "&#x3e;"})
	unicodeMarkerEscapes = newMarkerEscapes([]string{`\u003c`}, []string{`\u003e`})
	percentMarkerEscapes = newMarkerEscapes([]string{"%3c"}, []string{"%3e"})
)

func literalSpelling(placeholder, value string) restoreSpelling {
	return restoreSpelling{
		prefixes: []string{placeholder},
		value:    value,
		match: func(input string, at int, final bool) (int, matchStatus) {
			return matchLiteral(input, at, placeholder, false, final)
		},
	}
}

// bareMarkerSpelling matches a marker whose angle brackets were dropped, as
// happens when Markdown or HTML rendering would otherwise swallow it as a tag.
// The derived suffix keeps the body unambiguous; the word boundaries stop it
// from matching inside a longer identifier.
func bareMarkerSpelling(body, value string) restoreSpelling {
	return restoreSpelling{
		prefixes: []string{body},
		guard:    restoreWordStart,
		value:    value,
		match: func(input string, at int, final bool) (int, matchStatus) {
			if at > 0 && isASCIIAlphanumeric(input[at-1]) {
				return 0, matchNone
			}
			end, status := matchLiteral(input, at, body, false, final)
			if status != matchFull {
				return 0, status
			}
			return matchWordEnd(input, end, final)
		},
	}
}

// escapedMarkerSpelling matches a marker whose angle brackets were escaped for
// HTML, JSON or a URL. Escape sequences are case-insensitive; the body is not.
func escapedMarkerSpelling(escapes markerEscapes, body, value string, backslash bool) restoreSpelling {
	guard := restoreUnrestricted
	if backslash {
		guard = restoreUnescapedBackslash
	}
	prefixes := make([]string, len(escapes.prefixes))
	for index, prefix := range escapes.prefixes {
		prefixes[index] = prefix + body
	}
	opens, closes := escapes.opens, escapes.closes
	return restoreSpelling{
		prefixes: prefixes,
		guard:    guard,
		value:    value,
		match: func(input string, at int, final bool) (int, matchStatus) {
			// An escape preceded by an odd run of backslashes is itself escaped,
			// so it is literal text rather than the bracket it spells.
			if backslash && precedingBackslashes(input, at)%2 == 1 {
				return 0, matchNone
			}
			end, status := matchOneOf(input, at, opens, final)
			if status != matchFull {
				return 0, status
			}
			end, status = matchLiteral(input, end, body, false, final)
			if status != matchFull {
				return 0, status
			}
			return matchOneOf(input, end, closes, final)
		},
	}
}

// matchLiteral matches literal at input[at:], optionally folding ASCII case.
func matchLiteral(input string, at int, literal string, fold, final bool) (int, matchStatus) {
	rest := input[at:]
	if len(rest) >= len(literal) {
		if equalASCII(rest[:len(literal)], literal, fold) {
			return at + len(literal), matchFull
		}
		return 0, matchNone
	}
	if !final && equalASCII(rest, literal[:len(rest)], fold) {
		return 0, matchPartial
	}
	return 0, matchNone
}

// matchOneOf matches the first full alternative, folding ASCII case.
func matchOneOf(input string, at int, literals []string, final bool) (int, matchStatus) {
	status := matchNone
	for _, literal := range literals {
		end, current := matchLiteral(input, at, literal, true, final)
		if current == matchFull {
			return end, matchFull
		}
		if current == matchPartial {
			status = matchPartial
		}
	}
	return 0, status
}

// matchWordEnd accepts a match ending at end unless an alphanumeric byte
// continues it. At the end of non-final input the next byte is still unknown.
func matchWordEnd(input string, end int, final bool) (int, matchStatus) {
	if end == len(input) {
		if final {
			return end, matchFull
		}
		return 0, matchPartial
	}
	if isASCIIAlphanumeric(input[end]) {
		return 0, matchNone
	}
	return end, matchFull
}

func equalASCII(left, right string, fold bool) bool {
	if !fold {
		return left == right
	}
	if len(left) != len(right) {
		return false
	}
	for index := range len(left) {
		if asciiLower(left[index]) != asciiLower(right[index]) {
			return false
		}
	}
	return true
}

func asciiLower(character byte) byte {
	if character >= 'A' && character <= 'Z' {
		return character + 'a' - 'A'
	}
	return character
}

func isASCIIAlphanumeric(character byte) bool {
	return isASCIIDigit(character) ||
		(character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
}

func precedingBackslashes(input string, at int) int {
	count := 0
	for at-count > 0 && input[at-count-1] == '\\' {
		count++
	}
	return count
}

// percentEncode escapes everything outside the RFC 3986 unreserved set, which
// is safe in every URL component.
func percentEncode(value string) string {
	const hexDigits = "0123456789ABCDEF"
	var out strings.Builder
	for index := range len(value) {
		character := value[index]
		if isASCIIAlphanumeric(character) || strings.IndexByte("-_.~", character) >= 0 {
			out.WriteByte(character)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hexDigits[character>>4])
		out.WriteByte(hexDigits[character&0x0f])
	}
	return out.String()
}

// restorableSpellingIn reports whether any spelling the restorer would match
// for this placeholder already occurs in body. Restoring would otherwise
// rewrite that genuine occurrence as well.
func restorableSpellingIn(placeholder, body string) bool {
	if body == "" {
		return false
	}
	if strings.Contains(body, placeholder) {
		return true
	}
	// Every marker spelling contains the body verbatim.
	marker, ok := markerBody(placeholder)
	return ok && strings.Contains(body, marker)
}
