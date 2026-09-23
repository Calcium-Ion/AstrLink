package privacy

import "sort"

// A tool payload string that is itself JSON (function arguments, an exec
// result envelope) is inspected as one raw segment, so a detector sees the
// keys and punctuation around each value. Its spans follow tokenizer
// boundaries rather than JSON syntax: a URL can start at the `":"` before a
// path, or a JSON number can be labelled as a date. Replacing such a span
// verbatim breaks the embedded document, which used to reject the whole
// request. Aligning findings to literal contents keeps the payload valid
// without narrowing what a detector reported inside those literals.

type structuredJSONLiteral struct {
	// For strings the range excludes the quotes and keeps escapes raw.
	start  int
	end    int
	number bool
	key    bool
}

type structuredJSONLiterals struct {
	literals []structuredJSONLiteral
	// escapes holds the raw byte range of every string escape sequence.
	escapes [][2]int
}

// scanStructuredJSONLiterals expects a value validStructuredJSON accepted, so
// it only has to tell strings and numbers apart from structural bytes and the
// true/false/null keywords.
func scanStructuredJSONLiterals(value string) structuredJSONLiterals {
	var scanned structuredJSONLiterals
	for position := 0; position < len(value); {
		character := value[position]
		switch {
		case character == '"':
			start := position + 1
			position = start
			for position < len(value) && value[position] != '"' {
				if value[position] != '\\' {
					position++
					continue
				}
				length := 2
				if position+1 < len(value) && value[position+1] == 'u' {
					length = 6
				}
				end := min(position+length, len(value))
				scanned.escapes = append(scanned.escapes, [2]int{position, end})
				position = end
			}
			literal := structuredJSONLiteral{start: start, end: position}
			position++
			next := position
			for next < len(value) && isJSONWhitespace(value[next]) {
				next++
			}
			literal.key = next < len(value) && value[next] == ':'
			scanned.literals = append(scanned.literals, literal)
		case character == '-' || isASCIIDigit(character):
			start := position
			for position < len(value) && isJSONNumberByte(value[position]) {
				position++
			}
			scanned.literals = append(scanned.literals, structuredJSONLiteral{
				start: start, end: position, number: true,
			})
		default:
			position++
		}
	}
	return scanned
}

// alignStructuredFindings runs before suppression, so an allowlist entry, the
// reported finding, and the redact decision all see the aligned span. Each
// piece still counts toward the detector limit: a span is not allowed to fan
// out across an arbitrary number of literals.
func alignStructuredFindings(extracted []extractedSegment, findings []Finding) ([]Finding, error) {
	scans := make(map[int]structuredJSONLiterals)
	aligned := make([]Finding, 0, len(findings))
	changed := false
	for _, finding := range findings {
		segment := extracted[finding.Segment]
		if !segment.validateStructuredJSON {
			aligned = append(aligned, finding)
			continue
		}
		scanned, exists := scans[finding.Segment]
		if !exists {
			scanned = scanStructuredJSONLiterals(segment.Value)
			scans[finding.Segment] = scanned
		}
		aligned = scanned.align(aligned, finding)
		changed = true
		if len(aligned) > maxDetectorFindings {
			return nil, ErrDetectorLimit
		}
	}
	if !changed {
		return findings, nil
	}
	return uniqueFindings(aligned), nil
}

// align clips a finding to the literals it overlaps. A string piece never
// splits an escape sequence; a number piece always covers the whole number,
// which redaction then quotes. When a span crosses several literals, keys are
// dropped in favour of the values around them; a span confined to one key is
// kept, because a map can be keyed by the private value itself. A finding
// that touches no literal covers only syntax and is discarded.
func (scanned structuredJSONLiterals) align(aligned []Finding, finding Finding) []Finding {
	first := sort.Search(len(scanned.literals), func(index int) bool {
		return scanned.literals[index].end > finding.Start
	})
	pieces := make([]structuredJSONLiteral, 0, 1)
	hasValue := false
	for index := first; index < len(scanned.literals) &&
		scanned.literals[index].start < finding.End; index++ {
		literal := scanned.literals[index]
		piece := literal
		if !literal.number {
			piece.start = scanned.escapeStart(max(finding.Start, literal.start))
			piece.end = scanned.escapeEnd(min(finding.End, literal.end))
		}
		if piece.start >= piece.end {
			continue
		}
		hasValue = hasValue || !literal.key
		pieces = append(pieces, piece)
	}
	for _, piece := range pieces {
		if piece.key && hasValue {
			continue
		}
		clipped := finding
		clipped.Start = piece.start
		clipped.End = piece.end
		aligned = append(aligned, clipped)
	}
	return aligned
}

// isNumber reports whether a range is exactly one number literal, which must be
// replaced by a quoted placeholder to stay valid JSON.
func (scanned structuredJSONLiterals) isNumber(start, end int) bool {
	index := sort.Search(len(scanned.literals), func(index int) bool {
		return scanned.literals[index].start >= start
	})
	return index < len(scanned.literals) &&
		scanned.literals[index].number &&
		scanned.literals[index].start == start &&
		scanned.literals[index].end == end
}

// escapeStart moves a position inside an escape sequence back to its
// backslash, so a piece covers the whole escape rather than half of it.
func (scanned structuredJSONLiterals) escapeStart(position int) int {
	if escape, inside := scanned.escapeContaining(position); inside {
		return escape[0]
	}
	return position
}

func (scanned structuredJSONLiterals) escapeEnd(position int) int {
	if escape, inside := scanned.escapeContaining(position); inside {
		return escape[1]
	}
	return position
}

func (scanned structuredJSONLiterals) escapeContaining(position int) ([2]int, bool) {
	index := sort.Search(len(scanned.escapes), func(index int) bool {
		return scanned.escapes[index][1] > position
	})
	if index < len(scanned.escapes) && scanned.escapes[index][0] < position {
		return scanned.escapes[index], true
	}
	return [2]int{}, false
}

func isJSONWhitespace(character byte) bool {
	return character == ' ' || character == '\t' || character == '\n' || character == '\r'
}

func isASCIIDigit(character byte) bool {
	return character >= '0' && character <= '9'
}

func isJSONNumberByte(character byte) bool {
	return isASCIIDigit(character) || character == '-' || character == '+' ||
		character == '.' || character == 'e' || character == 'E'
}
