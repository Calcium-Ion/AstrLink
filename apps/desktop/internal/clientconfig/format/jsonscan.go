package format

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// JSONSyntax is what a client reads beyond strict JSON. The CST below
// parses JSON, JSONC, and JSON5 the way the jsonc-parser crate does, so that
// edits come out byte for byte as the Tauri shell wrote them.
type JSONSyntax struct {
	Comments           bool
	LoosePropertyNames bool
	TrailingCommas     bool
	MissingCommas      bool
	SingleQuotes       bool
	Hexadecimal        bool
	UnaryPlus          bool
	BareDecimalPoint   bool
	NonFinite          bool
	ExtendedEscapes    bool
}

// JSONC is JSON with comments.
var JSONC = JSONSyntax{Comments: true}

// JSONCTrailing is JSON with comments and trailing commas.
var JSONCTrailing = JSONSyntax{Comments: true, TrailingCommas: true}

// JSON5 is what OpenClaw reads: JSON5 without missing commas.
var JSON5 = JSONSyntax{
	Comments:           true,
	LoosePropertyNames: true,
	TrailingCommas:     true,
	SingleQuotes:       true,
	Hexadecimal:        true,
	UnaryPlus:          true,
	BareDecimalPoint:   true,
	NonFinite:          true,
	ExtendedEscapes:    true,
}

// anySyntax is everything the CST parser takes.
var anySyntax = JSONSyntax{
	Comments:           true,
	LoosePropertyNames: true,
	TrailingCommas:     true,
	MissingCommas:      true,
	SingleQuotes:       true,
	Hexadecimal:        true,
	UnaryPlus:          true,
	BareDecimalPoint:   true,
	NonFinite:          true,
	ExtendedEscapes:    true,
}

// jsonParseError is a JSON parse failure with its 1-based line.
type jsonParseError struct {
	kind   string
	line   int
	offset int
}

func (e *jsonParseError) Error() string { return e.kind }

type tokenKind int

const (
	tokOpenBrace tokenKind = iota
	tokCloseBrace
	tokOpenBracket
	tokCloseBracket
	tokComma
	tokColon
	tokString
	tokWord
	tokBool
	tokNumber
	tokNull
	tokCommentLine
	tokCommentBlock
)

var punctuation = map[byte]tokenKind{
	'{': tokOpenBrace, '}': tokCloseBrace, '[': tokOpenBracket,
	']': tokCloseBracket, ',': tokComma, ':': tokColon,
}

type jsonToken struct {
	kind       tokenKind
	start, end int
	boolean    bool
}

func (t jsonToken) isComment() bool { return t.kind == tokCommentLine || t.kind == tokCommentBlock }

func (t jsonToken) isValueStart() bool {
	switch t.kind {
	case tokOpenBrace, tokOpenBracket, tokString, tokBool, tokNumber, tokNull:
		return true
	}
	return false
}

type jsonScanner struct {
	text  string
	index int
	start int
	opts  JSONSyntax
}

func lineOf(text string, offset int) int {
	line := 1
	for i := 0; i < offset && i < len(text); i++ {
		c := text[i]
		if c == '\n' || c == '\r' && (i+1 >= len(text) || text[i+1] != '\n') {
			line++
		}
	}
	return line
}

func (s *jsonScanner) errorAt(offset int, kind string) *jsonParseError {
	return &jsonParseError{kind: kind, line: lineOf(s.text, offset), offset: offset}
}

func (s *jsonScanner) tokenError(kind string) *jsonParseError { return s.errorAt(s.start, kind) }

func (s *jsonScanner) byteAt(i int) (byte, bool) {
	if i < len(s.text) {
		return s.text[i], true
	}
	return 0, false
}

func (s *jsonScanner) skipWhitespace() {
	for s.index < len(s.text) {
		b := s.text[s.index]
		if b <= ' ' {
			switch b {
			case ' ', '\t', '\n', '\r', 0x0B, 0x0C:
				s.index++
				continue
			}
			return
		}
		if b >= 0x80 {
			r, size := utf8.DecodeRuneInString(s.text[s.index:])
			if unicode.IsSpace(r) {
				s.index += size
				continue
			}
		}
		return
	}
}

func isASCIIAlnum(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func isASCIIDigit(b byte) bool { return b >= '0' && b <= '9' }

func isASCIIHex(b byte) bool {
	return isASCIIDigit(b) || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

func isAlnumRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }

func (s *jsonScanner) tryMoveWord(word string) bool {
	end := s.index + len(word)
	if end > len(s.text) || s.text[s.index:end] != word {
		return false
	}
	if end < len(s.text) {
		next := s.text[end]
		if isASCIIAlnum(next) || next == '$' {
			return false
		}
		if next >= 0x80 {
			if r, _ := utf8.DecodeRuneInString(s.text[end:]); isAlnumRune(r) {
				return false
			}
		}
	}
	s.index = end
	return true
}

func (s *jsonScanner) tryMoveWholeWord(word string) bool {
	if b, ok := s.byteAt(s.index + len(word)); ok && (b == '-' || b == '_') {
		return false
	}
	return s.tryMoveWord(word)
}

// scan moves to the next token; ok is false at the end of the text.
func (s *jsonScanner) scan() (jsonToken, bool, error) {
	s.skipWhitespace()
	s.start = s.index
	if s.index >= len(s.text) {
		return jsonToken{}, false, nil
	}
	tok := func(kind tokenKind) (jsonToken, bool, error) {
		return jsonToken{kind: kind, start: s.start, end: s.index}, true, nil
	}
	b := s.text[s.index]
	if kind, ok := punctuation[b]; ok {
		s.index++
		return tok(kind)
	}
	switch {
	case b == '\'':
		if !s.opts.SingleQuotes {
			return jsonToken{}, false, s.tokenError("Single-quoted strings are not allowed")
		}
		return s.scanString()
	case b == '"':
		return s.scanString()
	case b == '/':
		next, _ := s.byteAt(s.index + 1)
		switch next {
		case '/':
			s.index += 2
			for s.index < len(s.text) && s.text[s.index] != '\n' && s.text[s.index] != '\r' {
				s.index++
			}
			return tok(tokCommentLine)
		case '*':
			s.index += 2
			for {
				if s.index >= len(s.text) {
					return jsonToken{}, false, s.tokenError("Unterminated comment block")
				}
				if s.text[s.index] == '*' && s.index+1 < len(s.text) && s.text[s.index+1] == '/' {
					s.index += 2
					return tok(tokCommentBlock)
				}
				s.index++
			}
		}
		return jsonToken{}, false, s.tokenError("Unexpected token")
	case b == '-' || b == '+' || isASCIIDigit(b):
		if err := s.scanNumber(); err != nil {
			return jsonToken{}, false, err
		}
		return tok(tokNumber)
	case b == '.' && s.index+1 < len(s.text) && isASCIIDigit(s.text[s.index+1]):
		if err := s.scanNumber(); err != nil {
			return jsonToken{}, false, err
		}
		return tok(tokNumber)
	case b == 'I' && s.opts.NonFinite && s.tryMoveWholeWord("Infinity"),
		b == 'N' && s.opts.NonFinite && s.tryMoveWholeWord("NaN"):
		return tok(tokNumber)
	case b == 't' && s.tryMoveWord("true"):
		return jsonToken{kind: tokBool, start: s.start, end: s.index, boolean: true}, true, nil
	case b == 'f' && s.tryMoveWord("false"):
		return tok(tokBool)
	case b == 'n' && s.tryMoveWord("null"):
		return tok(tokNull)
	}
	return s.scanWord()
}

func (s *jsonScanner) scanString() (jsonToken, bool, error) {
	_, end, kind, at := decodeJSONString(s.text, s.index, s.opts.ExtendedEscapes)
	if kind != "" {
		return jsonToken{}, false, s.errorAt(at, kind)
	}
	s.index = end
	return jsonToken{kind: tokString, start: s.start, end: end}, true, nil
}

func (s *jsonScanner) scanNumber() error {
	switch b, _ := s.byteAt(s.index); b {
	case '+':
		if !s.opts.UnaryPlus {
			return s.tokenError("Unary plus on numbers is not allowed")
		}
		s.index++
	case '-':
		s.index++
	}
	b, _ := s.byteAt(s.index)
	switch {
	case b == '0':
		s.index++
		if x, _ := s.byteAt(s.index); x == 'x' || x == 'X' {
			if !s.opts.Hexadecimal {
				return s.tokenError("Hexadecimal numbers are not allowed")
			}
			s.index++
			if h, ok := s.byteAt(s.index); !ok || !isASCIIHex(h) {
				return s.errorAt(s.index, "Expected digit")
			}
			for s.index < len(s.text) && isASCIIHex(s.text[s.index]) {
				s.index++
			}
			return nil
		}
	case b >= '1' && b <= '9':
		s.index++
		for s.index < len(s.text) && isASCIIDigit(s.text[s.index]) {
			s.index++
		}
	case b == '.' && s.index+1 < len(s.text) && isASCIIDigit(s.text[s.index+1]):
		if !s.opts.BareDecimalPoint {
			return s.tokenError("Leading or trailing decimal points on numbers are not allowed")
		}
	case b == 'I' || b == 'N':
		word := "Infinity"
		if b == 'N' {
			word = "NaN"
		}
		if !s.tryMoveWord(word) {
			return s.errorAt(s.index, "Expected digit following negative sign")
		}
		if !s.opts.NonFinite {
			return s.tokenError("Infinity and NaN are not allowed")
		}
		return nil
	default:
		return s.errorAt(s.index, "Expected digit following negative sign")
	}
	if b, _ := s.byteAt(s.index); b == '.' {
		s.index++
		if d, ok := s.byteAt(s.index); !s.opts.BareDecimalPoint && (!ok || !isASCIIDigit(d)) {
			return s.tokenError("Leading or trailing decimal points on numbers are not allowed")
		}
		for s.index < len(s.text) && isASCIIDigit(s.text[s.index]) {
			s.index++
		}
	}
	if e, _ := s.byteAt(s.index); e == 'e' || e == 'E' {
		s.index++
		switch c, _ := s.byteAt(s.index); {
		case c == '-' || c == '+':
			s.index++
			if d, ok := s.byteAt(s.index); !ok || !isASCIIDigit(d) {
				return s.errorAt(s.index, "Expected digit")
			}
		case isASCIIDigit(c):
		default:
			return s.errorAt(s.index, "Expected plus, minus, or digit in number literal")
		}
		for s.index < len(s.text) && isASCIIDigit(s.text[s.index]) {
			s.index++
		}
	}
	return nil
}

func (s *jsonScanner) scanWord() (jsonToken, bool, error) {
	for s.index < len(s.text) {
		b := s.text[s.index]
		if b < 0x80 {
			if b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == 0x0C || b == ':' {
				break
			}
			if isASCIIAlnum(b) || b == '-' || b == '_' || b == '$' {
				s.index++
				continue
			}
			return jsonToken{}, false, s.tokenError("Unexpected token")
		}
		r, size := utf8.DecodeRuneInString(s.text[s.index:])
		if unicode.IsSpace(r) {
			break
		}
		if !isAlnumRune(r) {
			return jsonToken{}, false, s.tokenError("Unexpected token")
		}
		s.index += size
	}
	if s.index == s.start {
		return jsonToken{}, false, s.tokenError("Unexpected token")
	}
	return jsonToken{kind: tokWord, start: s.start, end: s.index}, true, nil
}

// decodeJSONString decodes the string literal starting at text[start], a
// quote. It returns the value and the offset after the closing quote, or an
// error kind and where it is.
func decodeJSONString(text string, start int, extended bool) (string, int, string, int) {
	quote := text[start]
	double := quote == '"'
	// The fast path: no escapes before the closing quote.
	for i := start + 1; i < len(text); i++ {
		if text[i] == quote {
			return text[start+1 : i], i + 1, "", 0
		}
		if text[i] == '\\' {
			break
		}
	}
	var out strings.Builder
	last := start + 1
	backslash := false
	i := start + 1
	for i < len(text) {
		r, size := utf8.DecodeRuneInString(text[i:])
		if !backslash {
			if r == rune(quote) {
				out.WriteString(text[last:i])
				return out.String(), i + 1, "", 0
			}
			backslash = r == '\\'
			i += size
			continue
		}
		backslash = false
		escape := i - 1
		next := i + size
		if extended {
			decoded, has, handled, end, kind := extendedEscape(text, i, r)
			if kind != "" {
				return "", 0, kind, escape
			}
			if handled {
				out.WriteString(text[last:escape])
				if has {
					out.WriteRune(decoded)
				}
				i, last = end, end
				continue
			}
		}
		switch r {
		case '"', '\'', '\\', '/', 'b', 'f', 'u', 'r', 'n', 't':
			if r == '"' && !double {
				return "", 0, "Invalid escape in single quote string", escape
			}
			if r == '\'' && double {
				return "", 0, "Invalid escape in double quote string", escape
			}
			out.WriteString(text[last:escape])
			if r == 'u' {
				decoded, end, kind := hexEscape(text, next)
				if kind != "" {
					return "", 0, kind, escape
				}
				out.WriteRune(decoded)
				i, last = end, end
				continue
			}
			switch r {
			case 'b':
				r = '\b'
			case 'f':
				r = '\f'
			case 't':
				r = '\t'
			case 'r':
				r = '\r'
			case 'n':
				r = '\n'
			}
			out.WriteRune(r)
			i, last = next, next
		default:
			return "", 0, "Invalid escape", escape
		}
	}
	return "", 0, "Unterminated string literal", start
}

// extendedEscape decodes a JSON5 escape whose character r is at text[at]:
// handled reports whether it is one, has whether it stands for a
// character, and end is where the text after it starts.
func extendedEscape(text string, at int, r rune) (decoded rune, has, handled bool, end int, kind string) {
	size := utf8.RuneLen(r)
	switch {
	case r == 'v':
		return 0x0B, true, true, at + 1, ""
	case r == '0':
		if at+1 < len(text) && isASCIIDigit(text[at+1]) {
			return 0, false, false, 0, "Invalid escape"
		}
		return 0, true, true, at + 1, ""
	case r == 'x':
		if at+2 >= len(text) || !isASCIIHex(text[at+1]) || !isASCIIHex(text[at+2]) {
			return 0, false, false, 0, "Expected two hex digits"
		}
		var value rune
		fmt.Sscanf(text[at+1:at+3], "%x", &value)
		return value, true, true, at + 3, ""
	case r == '\n' || r == 0x2028 || r == 0x2029:
		return 0, false, true, at + size, ""
	case r == '\r':
		if at+1 < len(text) && text[at+1] == '\n' {
			return 0, false, true, at + 2, ""
		}
		return 0, false, true, at + 1, ""
	case r >= '1' && r <= '9':
		return 0, false, false, 0, "Invalid escape"
	case strings.ContainsRune(`\/bfurnt`, r):
		return 0, false, false, 0, ""
	}
	return r, true, true, at + size, ""
}

func fourHex(text string, at int) (rune, bool) {
	if at+4 > len(text) {
		return 0, false
	}
	var value rune
	for _, c := range []byte(text[at : at+4]) {
		if !isASCIIHex(c) {
			return 0, false
		}
		value *= 16
		switch {
		case c <= '9':
			value += rune(c - '0')
		case c >= 'a':
			value += rune(c-'a') + 10
		default:
			value += rune(c-'A') + 10
		}
	}
	return value, true
}

// hexEscape decodes the four hex digits of a \u escape at text[at], with
// the low half of a surrogate pair.
func hexEscape(text string, at int) (rune, int, string) {
	high, ok := fourHex(text, at)
	if !ok {
		return 0, 0, "Expected four hex digits"
	}
	digits := text[at : at+4]
	switch {
	case high >= 0xD800 && high <= 0xDBFF:
		if at+6 > len(text) || text[at+4] != '\\' || text[at+5] != 'u' {
			return 0, 0, fmt.Sprintf("Invalid unicode escape sequence. '%s (unpaired high surrogate)' is not a valid UTF8 character", digits)
		}
		low, ok := fourHex(text, at+6)
		if !ok {
			return 0, 0, "Expected four hex digits"
		}
		if low < 0xDC00 || low > 0xDFFF {
			return 0, 0, fmt.Sprintf("Invalid unicode escape sequence. '%s (high surrogate not followed by low surrogate)' is not a valid UTF8 character", digits)
		}
		return (high-0xD800)*0x400 + (low - 0xDC00) + 0x10000, at + 10, ""
	case high >= 0xDC00 && high <= 0xDFFF:
		return 0, 0, fmt.Sprintf("Invalid unicode escape sequence. '%s (unpaired low surrogate)' is not a valid UTF8 character", digits)
	}
	return high, at + 4, ""
}
