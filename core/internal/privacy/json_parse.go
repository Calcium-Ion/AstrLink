package privacy

import (
	"encoding/json"
	"unicode/utf8"
)

// parsePrivacyJSON lets the standard library validate the complete grammar,
// then builds the inspection tree while checking decoded keys and depth. The
// old Token walk followed by Decode materialized every string twice and grew
// two decoder buffers to the size of the largest string, including images
// that inspection skips. This cursor only allocates the final tree's values.
// Structure-only callers do not allocate string or number values at all.
func parsePrivacyJSON(body []byte, materialize bool) (any, bool, error) {
	if !json.Valid(body) {
		return nil, false, ErrUnsafeInput
	}
	parser := privacyJSONParser{body: body, materialize: materialize}
	value, err := parser.value(0)
	return value, parser.duplicate, err
}

type privacyJSONParser struct {
	body        []byte
	position    int
	materialize bool
	duplicate   bool
}

// value is only called after json.Valid succeeds. The cursor therefore need
// not implement a second JSON grammar or accept malformed tokens itself.
func (parser *privacyJSONParser) value(depth int) (any, error) {
	if depth > maxJSONDepth {
		return nil, ErrUnsafeInput
	}
	parser.whitespace()
	switch parser.body[parser.position] {
	case '{':
		parser.position++
		parser.whitespace()
		object := make(map[string]any)
		for parser.body[parser.position] != '}' {
			key := parser.string(true)
			parser.whitespace()
			parser.position++ // colon
			value, err := parser.value(depth + 1)
			if err != nil {
				return nil, err
			}
			if _, exists := object[key]; exists {
				parser.duplicate = true
			}
			object[key] = value
			parser.whitespace()
			if parser.body[parser.position] == ',' {
				parser.position++
				parser.whitespace()
			}
		}
		parser.position++
		if parser.materialize {
			return object, nil
		}
	case '[':
		parser.position++
		parser.whitespace()
		var array []any
		for parser.body[parser.position] != ']' {
			value, err := parser.value(depth + 1)
			if err != nil {
				return nil, err
			}
			if parser.materialize {
				array = append(array, value)
			}
			parser.whitespace()
			if parser.body[parser.position] == ',' {
				parser.position++
			}
		}
		parser.position++
		if parser.materialize {
			// Match encoding/json's non-nil empty arrays.
			if array == nil {
				array = []any{}
			}
			return array, nil
		}
	case '"':
		value := parser.string(parser.materialize)
		if parser.materialize {
			return value, nil
		}
	case 't':
		parser.position += len("true")
		return true, nil
	case 'f':
		parser.position += len("false")
		return false, nil
	case 'n':
		parser.position += len("null")
	default:
		start := parser.position
		for parser.position < len(parser.body) && isJSONNumberByte(parser.body[parser.position]) {
			parser.position++
		}
		if parser.materialize {
			return json.Number(parser.body[start:parser.position]), nil
		}
	}
	return nil, nil
}

func (parser *privacyJSONParser) string(materialize bool) string {
	start := parser.position
	parser.position++
	escaped := false
	for parser.body[parser.position] != '"' {
		if parser.body[parser.position] == '\\' {
			escaped = true
			parser.position++
		}
		parser.position++
	}
	parser.position++
	if !materialize {
		return ""
	}
	if !escaped && utf8.Valid(parser.body[start+1:parser.position-1]) {
		return string(parser.body[start+1 : parser.position-1])
	}
	var decoded string
	// The complete document was validated before the cursor ran; decoding
	// escapes here preserves encoding/json's handling of UTF-16 surrogates.
	_ = json.Unmarshal(parser.body[start:parser.position], &decoded)
	return decoded
}

func (parser *privacyJSONParser) whitespace() {
	for parser.position < len(parser.body) && isJSONWhitespace(parser.body[parser.position]) {
		parser.position++
	}
}
