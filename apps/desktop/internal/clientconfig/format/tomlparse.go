package format

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	gotoml "github.com/pelletier/go-toml/v2"
)

// tomlParser reads TOML into the toml_edit model, giving each key, value,
// and header the text around it as toml_edit's parser does.
type tomlParser struct {
	text string
	pos  int
	// trailingStart is where the comments and blank lines not yet given to
	// a key or header start; -1 when there are none.
	trailingStart   int
	root            *TOMLTable
	current         *TOMLTable
	currentPosition int
	currentIsArray  bool
	currentPath     []*tomlKey
	depth           int
}

type tomlError struct {
	offset  int
	message string
}

func (e *tomlError) Error() string { return e.message }

func (p *tomlParser) fail(message string) *tomlError {
	return &tomlError{offset: p.pos, message: message}
}

func (p *tomlParser) peek() (byte, bool) {
	if p.pos < len(p.text) {
		return p.text[p.pos], true
	}
	return 0, false
}

func (p *tomlParser) is(c byte) bool {
	b, ok := p.peek()
	return ok && b == c
}

func (p *tomlParser) ws() string {
	start := p.pos
	for p.pos < len(p.text) && (p.text[p.pos] == ' ' || p.text[p.pos] == '\t') {
		p.pos++
	}
	return p.text[start:p.pos]
}

// comment reads `#` and the rest of the line.
func (p *tomlParser) comment() {
	p.pos++
	for p.pos < len(p.text) {
		c := p.text[p.pos]
		if c == '\t' || c >= 0x20 && c != 0x7F {
			p.pos++
			continue
		}
		return
	}
}

func (p *tomlParser) newline() *tomlError {
	switch {
	case p.is('\n'):
		p.pos++
	case p.is('\r') && p.pos+1 < len(p.text) && p.text[p.pos+1] == '\n':
		p.pos += 2
	default:
		return p.fail("expected newline")
	}
	return nil
}

func (p *tomlParser) lineEnding() *tomlError {
	if p.pos >= len(p.text) {
		return nil
	}
	return p.newline()
}

// lineTrailing reads the spaces and comment that end a line, and the line
// break, returning the first two.
func (p *tomlParser) lineTrailing() (string, *tomlError) {
	start := p.pos
	p.ws()
	if p.is('#') {
		p.comment()
	}
	trailing := p.text[start:p.pos]
	return trailing, p.lineEnding()
}

func (p *tomlParser) onTrivia(start int) {
	if p.trailingStart < 0 {
		p.trailingStart = start
	}
}

func (p *tomlParser) takeTrailing(end int) string {
	if p.trailingStart < 0 {
		return ""
	}
	trailing := p.text[p.trailingStart:end]
	p.trailingStart = -1
	return trailing
}

// parseTOMLText reads a document as toml_edit does.
func parseTOMLText(text string) (*TOMLDocument, *tomlError) {
	p := &tomlParser{text: text, trailingStart: -1, root: &TOMLTable{}, current: &TOMLTable{}}
	if strings.HasPrefix(text, "\xEF\xBB\xBF") {
		p.pos = 3
	}
	start := p.pos
	p.ws()
	p.onTrivia(start)
	for p.pos < len(text) {
		start := p.pos
		switch text[p.pos] {
		case '#':
			p.comment()
			if err := p.lineEnding(); err != nil {
				return nil, err
			}
			p.onTrivia(start)
		case '[':
			if err := p.header(); err != nil {
				return nil, err
			}
		case '\n', '\r':
			if err := p.newline(); err != nil {
				return nil, err
			}
			p.onTrivia(start)
		default:
			if err := p.keyval(); err != nil {
				return nil, err
			}
		}
		start = p.pos
		p.ws()
		p.onTrivia(start)
	}
	if err := p.finalize(); err != nil {
		return nil, err
	}
	return &TOMLDocument{root: p.root, trailing: p.takeTrailing(len(text))}, nil
}

// keyPath reads a dotted key. The spaces before it and after it go to the
// last key's leaf decor.
func (p *tomlParser) keyPath() ([]*tomlKey, *tomlError) {
	var keys []*tomlKey
	for {
		pre := p.ws()
		start := p.pos
		name, err := p.simpleKey()
		if err != nil {
			return nil, err
		}
		repr := p.text[start:p.pos]
		suf := p.ws()
		keys = append(keys, &tomlKey{name: name, repr: &repr, dotted: tomlDecor{ptr(pre), ptr(suf)}})
		if !p.is('.') {
			break
		}
		p.pos++
	}
	first, last := keys[0], keys[len(keys)-1]
	prefix := *first.dotted.prefix
	first.dotted.prefix = ptr("")
	suffix := *last.dotted.suffix
	last.dotted.suffix = ptr("")
	last.leaf = tomlDecor{ptr(prefix), ptr(suffix)}
	return keys, nil
}

func (p *tomlParser) simpleKey() (string, *tomlError) {
	c, ok := p.peek()
	switch {
	case ok && c == '"':
		return p.basicString()
	case ok && c == '\'':
		return p.literalString()
	}
	start := p.pos
	for p.pos < len(p.text) && isBareKeyByte(p.text[p.pos]) {
		p.pos++
	}
	if p.pos == start {
		return "", p.fail("invalid key")
	}
	return p.text[start:p.pos], nil
}

func (p *tomlParser) keyval() *tomlError {
	keyStart := p.pos
	keys, err := p.keyPath()
	if err != nil {
		return err
	}
	if !p.is('=') {
		return p.fail("expected `=`")
	}
	p.pos++
	pre := p.ws()
	value, err := p.value()
	if err != nil {
		return err
	}
	suf, err := p.lineTrailing()
	if err != nil {
		return err
	}
	value.decor = tomlDecor{ptr(pre), ptr(suf)}
	leaf := keys[len(keys)-1]
	prefix := p.takeTrailing(keyStart) + *leaf.leaf.prefix
	leaf.leaf.prefix = &prefix
	// What breaks the rules shows at the start of the line, as the
	// crate's parser reports it.
	return p.at(keyStart, p.onKeyval(keys[:len(keys)-1], leaf, value))
}

// at moves err to offset.
func (p *tomlParser) at(offset int, err *tomlError) *tomlError {
	if err != nil {
		err.offset = offset
	}
	return err
}

func (p *tomlParser) duplicate(name string) *tomlError {
	return p.fail(fmt.Sprintf("duplicate key `%s`", name))
}

func (p *tomlParser) onKeyval(path []*tomlKey, key *tomlKey, value *TOMLValue) *tomlError {
	table, err := p.descend(p.current, path, true)
	if err != nil {
		return err
	}
	if table.dotted == (len(path) == 0) || table.ContainsKey(key.name) {
		return p.duplicate(key.name)
	}
	table.entries = append(table.entries, &tomlEntry{key: key, item: value})
	return nil
}

// descend walks path from table, making implicit tables where there are
// none, as dotted keys or headers do.
func (p *tomlParser) descend(table *TOMLTable, path []*tomlKey, dotted bool) (*TOMLTable, *tomlError) {
	for _, key := range path {
		i := table.find(key.name)
		if i < 0 {
			table.entries = append(table.entries, &tomlEntry{key: key.clone(), item: &TOMLTable{implicit: true, dotted: dotted}})
			i = len(table.entries) - 1
		}
		switch it := table.entries[i].item.(type) {
		case *TOMLArrayOfTables:
			table = it.Tables[len(it.Tables)-1]
		case *TOMLTable:
			if dotted && !it.implicit {
				return nil, p.duplicate(key.name)
			}
			table = it
		default:
			return nil, p.fail(fmt.Sprintf("cannot extend value `%s`", key.name))
		}
	}
	return table, nil
}

func (p *tomlParser) header() *tomlError {
	start := p.pos
	array := strings.HasPrefix(p.text[p.pos:], "[[")
	close := "]"
	if array {
		close = "]]"
		p.pos += 2
	} else {
		p.pos++
	}
	path, err := p.keyPath()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(p.text[p.pos:], close) {
		return p.fail("expected `" + close + "`")
	}
	p.pos += len(close)
	suffix, err := p.lineTrailing()
	if err != nil {
		return err
	}
	if err := p.finalize(); err != nil {
		return p.at(start, err)
	}
	decor := tomlDecor{ptr(p.takeTrailing(start)), ptr(suffix)}
	return p.at(start, p.startTable(path, decor, array))
}

func (p *tomlParser) startTable(path []*tomlKey, decor tomlDecor, array bool) *tomlError {
	parent, err := p.descend(p.root, path[:len(path)-1], false)
	if err != nil {
		return err
	}
	key := path[len(path)-1]
	if array {
		i := parent.find(key.name)
		if i < 0 {
			parent.entries = append(parent.entries, &tomlEntry{key: key.clone(), item: &TOMLArrayOfTables{}})
			i = len(parent.entries) - 1
		}
		if _, ok := parent.entries[i].item.(*TOMLArrayOfTables); !ok {
			return p.duplicate(key.name)
		}
	} else if i := parent.find(key.name); i >= 0 {
		t, ok := parent.entries[i].item.(*TOMLTable)
		if !ok || !t.implicit || t.dotted {
			return p.duplicate(key.name)
		}
		parent.entries = append(parent.entries[:i], parent.entries[i+1:]...)
		p.current = t
	}
	p.currentPosition++
	p.current.decor = decor
	p.current.implicit = false
	p.current.dotted = false
	p.current.SetPosition(p.currentPosition)
	p.currentIsArray = array
	p.currentPath = path
	return nil
}

// finalize puts the table being read into the tree.
func (p *tomlParser) finalize() *tomlError {
	table, path := p.current, p.currentPath
	p.current, p.currentPath = &TOMLTable{}, nil
	if len(path) == 0 {
		p.root = table
		return nil
	}
	parent, err := p.descend(p.root, path[:len(path)-1], false)
	if err != nil {
		return err
	}
	key := path[len(path)-1]
	i := parent.find(key.name)
	if p.currentIsArray {
		if i < 0 {
			parent.entries = append(parent.entries, &tomlEntry{key: key.clone(), item: &TOMLArrayOfTables{}})
			i = len(parent.entries) - 1
		}
		tables, ok := parent.entries[i].item.(*TOMLArrayOfTables)
		if !ok {
			return p.duplicate(key.name)
		}
		tables.Push(table)
		return nil
	}
	if i < 0 {
		parent.entries = append(parent.entries, &tomlEntry{key: key.clone(), item: table})
		return nil
	}
	if t, ok := parent.entries[i].item.(*TOMLTable); ok && t.implicit {
		parent.entries[i].item = table
		return nil
	}
	return p.duplicate(key.name)
}

func (p *tomlParser) value() (*TOMLValue, *tomlError) {
	c, ok := p.peek()
	if !ok {
		return nil, p.fail("expected a value")
	}
	start := p.pos
	var v *TOMLValue
	var err *tomlError
	switch {
	case c == '"' || c == '\'':
		var s string
		s, err = p.string()
		v = &TOMLValue{kind: tomlString, str: s}
	case c == '[':
		v, err = p.array()
	case c == '{':
		v, err = p.inlineTable()
	case c == '+' || c == '-' || isASCIIDigit(c):
		v, err = p.numberOrDatetime()
	case c == 't' && strings.HasPrefix(p.text[p.pos:], "true"):
		p.pos += 4
		v = &TOMLValue{kind: tomlBool, boolean: true}
	case c == 'f' && strings.HasPrefix(p.text[p.pos:], "false"):
		p.pos += 5
		v = &TOMLValue{kind: tomlBool}
	case c == 'i' && strings.HasPrefix(p.text[p.pos:], "inf"):
		p.pos += 3
		v = &TOMLValue{kind: tomlFloat, float: math.Inf(1)}
	case c == 'n' && strings.HasPrefix(p.text[p.pos:], "nan"):
		p.pos += 3
		v = &TOMLValue{kind: tomlFloat, float: math.NaN()}
	default:
		return nil, p.fail("expected a value")
	}
	if err != nil {
		return nil, err
	}
	if v.kind != tomlArray && v.kind != tomlInline {
		repr := p.text[start:p.pos]
		v.repr = &repr
	}
	v.decor = tomlDecor{ptr(""), ptr("")}
	return v, nil
}

func (p *tomlParser) nest() *tomlError {
	p.depth++
	if p.depth > 80 {
		return p.fail("nested too deeply")
	}
	return nil
}

// wsCommentNewline reads the spaces, comments, and line breaks between
// array values.
func (p *tomlParser) wsCommentNewline() *tomlError {
	for {
		start := p.pos
		p.ws()
		c, ok := p.peek()
		if !ok {
			return nil
		}
		switch c {
		case '#':
			p.comment()
			if err := p.newline(); err != nil {
				return err
			}
		case '\n', '\r':
			if err := p.newline(); err != nil {
				return err
			}
		default:
			return nil
		}
		if p.pos == start {
			return nil
		}
	}
}

func (p *tomlParser) array() (*TOMLValue, *tomlError) {
	if err := p.nest(); err != nil {
		return nil, err
	}
	defer func() { p.depth-- }()
	p.pos++
	v := &TOMLValue{kind: tomlArray}
	if p.is(']') {
		p.pos++
		return v, nil
	}
	element := func() (bool, *tomlError) {
		start := p.pos
		if err := p.wsCommentNewline(); err != nil {
			return false, err
		}
		pre := p.text[start:p.pos]
		if c, ok := p.peek(); !ok || c == ']' || c == ',' {
			p.pos = start
			return false, nil
		}
		item, err := p.value()
		if err != nil {
			return false, err
		}
		sufStart := p.pos
		if err := p.wsCommentNewline(); err != nil {
			return false, err
		}
		item.decor = tomlDecor{ptr(pre), ptr(p.text[sufStart:p.pos])}
		v.items = append(v.items, item)
		return true, nil
	}
	more, err := element()
	if err != nil {
		return nil, err
	}
	for more && p.is(',') {
		comma := p.pos
		p.pos++
		if more, err = element(); err != nil {
			return nil, err
		}
		if !more {
			p.pos = comma
		}
	}
	if len(v.items) > 0 && p.is(',') {
		p.pos++
		v.trailingComma = true
	}
	start := p.pos
	if err := p.wsCommentNewline(); err != nil {
		return nil, err
	}
	v.trailing = p.text[start:p.pos]
	if !p.is(']') {
		return nil, p.fail("expected `]`")
	}
	p.pos++
	return v, nil
}

func (p *tomlParser) inlineTable() (*TOMLValue, *tomlError) {
	if err := p.nest(); err != nil {
		return nil, err
	}
	defer func() { p.depth-- }()
	p.pos++
	table := &TOMLTable{inline: true}
	pair := func() (bool, *tomlError) {
		start := p.pos
		p.ws()
		if c, ok := p.peek(); !ok || c == '}' || c == ',' {
			p.pos = start
			return false, nil
		}
		p.pos = start
		keys, err := p.keyPath()
		if err != nil {
			return false, err
		}
		if !p.is('=') {
			return false, p.fail("expected `=`")
		}
		p.pos++
		pre := p.ws()
		value, err := p.value()
		if err != nil {
			return false, err
		}
		value.decor = tomlDecor{ptr(pre), ptr(p.ws())}
		return true, p.inlinePair(table, keys, value)
	}
	more, err := pair()
	if err != nil {
		return nil, err
	}
	for more && p.is(',') {
		comma := p.pos
		p.pos++
		if more, err = pair(); err != nil {
			return nil, err
		}
		if !more {
			p.pos = comma
		}
	}
	table.preamble = p.ws()
	if !p.is('}') {
		return nil, p.fail("expected `}`")
	}
	p.pos++
	return &TOMLValue{kind: tomlInline, table: table}, nil
}

func (p *tomlParser) inlinePair(table *TOMLTable, keys []*tomlKey, value *TOMLValue) *tomlError {
	path, key := keys[:len(keys)-1], keys[len(keys)-1]
	dotted := len(path) > 0
	for _, segment := range path {
		i := table.find(segment.name)
		if i < 0 {
			inner := &TOMLTable{inline: true, implicit: dotted, dotted: dotted}
			table.entries = append(table.entries, &tomlEntry{key: segment.clone(), item: &TOMLValue{kind: tomlInline, table: inner}})
			i = len(table.entries) - 1
		}
		v, ok := table.entries[i].item.(*TOMLValue)
		if !ok || v.kind != tomlInline {
			return p.fail(fmt.Sprintf("cannot extend value `%s`", segment.name))
		}
		if dotted && !v.table.implicit {
			return p.duplicate(segment.name)
		}
		table = v.table
	}
	if table.dotted == (len(path) == 0) || table.ContainsKey(key.name) {
		return p.duplicate(key.name)
	}
	table.entries = append(table.entries, &tomlEntry{key: key, item: value})
	return nil
}

func (p *tomlParser) string() (string, *tomlError) {
	switch rest := p.text[p.pos:]; {
	case strings.HasPrefix(rest, `"""`):
		return p.multilineString('"')
	case strings.HasPrefix(rest, "'''"):
		return p.multilineString('\'')
	case rest[0] == '"':
		return p.basicString()
	}
	return p.literalString()
}

func isBasicChar(c byte) bool {
	return c == ' ' || c == '\t' || c == 0x21 || c >= 0x23 && c <= 0x5B || c >= 0x5D && c <= 0x7E || c >= 0x80
}

// escape reads the escape sequence after a backslash.
func (p *tomlParser) escape(out *strings.Builder) *tomlError {
	c, ok := p.peek()
	if !ok {
		return p.fail("invalid escape sequence")
	}
	p.pos++
	switch c {
	case 'b':
		out.WriteByte('\b')
	case 'f':
		out.WriteByte('\f')
	case 'n':
		out.WriteByte('\n')
	case 'r':
		out.WriteByte('\r')
	case 't':
		out.WriteByte('\t')
	case '\\':
		out.WriteByte('\\')
	case '"':
		out.WriteByte('"')
	case 'u', 'U':
		n := 4
		if c == 'U' {
			n = 8
		}
		if p.pos+n > len(p.text) {
			return p.fail("invalid unicode escape")
		}
		digits := p.text[p.pos : p.pos+n]
		code, err := strconv.ParseUint(digits, 16, 32)
		if err != nil || strings.ContainsAny(digits, "+-") || !utf8.ValidRune(rune(code)) {
			return p.fail("invalid unicode escape")
		}
		p.pos += n
		out.WriteRune(rune(code))
	default:
		return p.fail("invalid escape sequence")
	}
	return nil
}

func (p *tomlParser) basicString() (string, *tomlError) {
	p.pos++
	var out strings.Builder
	for {
		c, ok := p.peek()
		switch {
		case !ok:
			return "", p.fail("unterminated string")
		case c == '"':
			p.pos++
			return out.String(), nil
		case c == '\\':
			p.pos++
			if err := p.escape(&out); err != nil {
				return "", err
			}
		case isBasicChar(c):
			out.WriteByte(c)
			p.pos++
		default:
			return "", p.fail("invalid character in string")
		}
	}
}

func (p *tomlParser) literalString() (string, *tomlError) {
	p.pos++
	start := p.pos
	for p.pos < len(p.text) && isLiteralChar(rune(p.text[p.pos])) {
		p.pos++
	}
	if !p.is('\'') {
		return "", p.fail("unterminated string")
	}
	value := p.text[start:p.pos]
	p.pos++
	return value, nil
}

// multilineString reads a multi-line string, basic or literal.
func (p *tomlParser) multilineString(quote byte) (string, *tomlError) {
	p.pos += 3
	if p.is('\n') {
		p.pos++
	} else if strings.HasPrefix(p.text[p.pos:], "\r\n") {
		p.pos += 2
	}
	var out strings.Builder
	for {
		c, ok := p.peek()
		switch {
		case !ok:
			return "", p.fail("unterminated string")
		case c == quote:
			n := 0
			for p.pos+n < len(p.text) && p.text[p.pos+n] == quote {
				n++
			}
			if n < 3 {
				out.WriteString(p.text[p.pos : p.pos+n])
				p.pos += n
				continue
			}
			if n > 5 {
				return "", p.fail("too many quotes")
			}
			out.WriteString(p.text[p.pos : p.pos+n-3])
			p.pos += n
			return out.String(), nil
		case c == '\n' || c == '\r':
			if err := p.newline(); err != nil {
				return "", err
			}
			out.WriteByte('\n')
		case quote == '"' && c == '\\':
			p.pos++
			if err := p.escapedNewline(); err != nil {
				return "", err
			} else if p.pos > 0 && p.text[p.pos-1] == '\\' {
				if err := p.escape(&out); err != nil {
					return "", err
				}
			}
		case quote == '"' && isBasicChar(c), quote == '\'' && isLiteralChar(rune(c)):
			out.WriteByte(c)
			p.pos++
		default:
			return "", p.fail("invalid character in string")
		}
	}
}

// escapedNewline skips a line-ending backslash, already read, and the
// whitespace after it; it leaves the position after the backslash when the
// backslash starts an escape instead.
func (p *tomlParser) escapedNewline() *tomlError {
	start := p.pos
	p.ws()
	if !p.is('\n') && !strings.HasPrefix(p.text[p.pos:], "\r\n") {
		p.pos = start
		return nil
	}
	for {
		p.ws()
		if p.is('\n') || strings.HasPrefix(p.text[p.pos:], "\r\n") {
			_ = p.newline()
			continue
		}
		return nil
	}
}

func digits(text string, at, n int) bool {
	if at+n > len(text) {
		return false
	}
	for i := at; i < at+n; i++ {
		if !isASCIIDigit(text[i]) {
			return false
		}
	}
	return true
}

func (p *tomlParser) numberOrDatetime() (*TOMLValue, *tomlError) {
	t := p.text
	if digits(t, p.pos, 4) && p.pos+4 < len(t) && t[p.pos+4] == '-' {
		return p.datetime()
	}
	if digits(t, p.pos, 2) && p.pos+2 < len(t) && t[p.pos+2] == ':' {
		return p.datetime()
	}
	start := p.pos
	for p.pos < len(t) {
		c := t[p.pos]
		if isASCIIAlnum(c) || c == '_' || c == '.' || c == '+' || c == '-' {
			p.pos++
			continue
		}
		break
	}
	raw := strings.ReplaceAll(t[start:p.pos], "_", "")
	unsigned := strings.TrimLeft(raw, "+-")
	switch {
	case unsigned == "inf":
		return &TOMLValue{kind: tomlFloat, float: math.Inf(map[bool]int{true: -1, false: 1}[strings.HasPrefix(raw, "-")])}, nil
	case unsigned == "nan":
		return &TOMLValue{kind: tomlFloat, float: math.Copysign(math.NaN(), map[bool]float64{true: -1, false: 1}[strings.HasPrefix(raw, "-")])}, nil
	case len(raw) > 2 && raw[0] == '0' && strings.ContainsRune("xob", rune(raw[1])):
		base := map[byte]int{'x': 16, 'o': 8, 'b': 2}[raw[1]]
		n, err := strconv.ParseInt(raw[2:], base, 64)
		if err != nil {
			return nil, &tomlError{offset: start, message: "invalid integer"}
		}
		return &TOMLValue{kind: tomlInteger, integer: n}, nil
	case strings.ContainsAny(raw, ".eE"):
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsInf(f, 1) {
			return nil, &tomlError{offset: start, message: "invalid float"}
		}
		return &TOMLValue{kind: tomlFloat, float: f}, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, &tomlError{offset: start, message: "invalid integer"}
	}
	return &TOMLValue{kind: tomlInteger, integer: n}, nil
}

// datetime reads a date, a time, or both, and keeps it as toml_datetime
// prints it.
func (p *tomlParser) datetime() (*TOMLValue, *tomlError) {
	t := p.text
	var out strings.Builder
	readTime := func() bool {
		if !digits(t, p.pos, 2) || p.pos+8 > len(t) || t[p.pos+2] != ':' || !digits(t, p.pos+3, 2) || t[p.pos+5] != ':' || !digits(t, p.pos+6, 2) {
			return false
		}
		out.WriteString(t[p.pos : p.pos+8])
		p.pos += 8
		if p.is('.') && digits(t, p.pos+1, 1) {
			p.pos++
			start := p.pos
			for p.pos < len(t) && isASCIIDigit(t[p.pos]) {
				p.pos++
			}
			frac := t[start:p.pos]
			if len(frac) > 9 {
				frac = frac[:9]
			}
			frac = strings.TrimRight(frac+strings.Repeat("0", 9-len(frac)), "0")
			if frac != "" {
				out.WriteString("." + frac)
			}
		}
		return true
	}
	if t[p.pos+2] == ':' {
		if !readTime() {
			return nil, p.fail("invalid time")
		}
		return &TOMLValue{kind: tomlDatetime, str: out.String()}, nil
	}
	if !digits(t, p.pos+5, 2) || p.pos+10 > len(t) || t[p.pos+7] != '-' || !digits(t, p.pos+8, 2) {
		return nil, p.fail("invalid date")
	}
	out.WriteString(t[p.pos : p.pos+10])
	p.pos += 10
	if c, ok := p.peek(); ok && (c == 'T' || c == 't' || c == ' ') {
		save := p.pos
		p.pos++
		out.WriteByte('T')
		if !readTime() {
			if c != ' ' {
				return nil, p.fail("invalid time")
			}
			p.pos = save
			return &TOMLValue{kind: tomlDatetime, str: strings.TrimSuffix(out.String(), "T")}, nil
		}
		switch c, _ := p.peek(); {
		case c == 'Z' || c == 'z':
			p.pos++
			out.WriteByte('Z')
		case (c == '+' || c == '-') && digits(t, p.pos+1, 2) && p.pos+6 <= len(t) && t[p.pos+3] == ':' && digits(t, p.pos+4, 2):
			hours, _ := strconv.Atoi(t[p.pos+1 : p.pos+3])
			minutes, _ := strconv.Atoi(t[p.pos+4 : p.pos+6])
			total := hours*60 + minutes
			sign := '+'
			if c == '-' && total != 0 {
				sign = '-'
			}
			out.WriteString(fmt.Sprintf("%c%02d:%02d", sign, total/60, total%60))
			p.pos += 6
		}
	}
	return &TOMLValue{kind: tomlDatetime, str: out.String()}, nil
}

// validateTOML checks what the model parser does not: dates, numbers, and
// the other rules of TOML 1.0.
func validateTOML(text string) *Invalid {
	var value map[string]any
	err := gotoml.Unmarshal([]byte(strings.TrimPrefix(text, "\xEF\xBB\xBF")), &value)
	if err == nil {
		return nil
	}
	line := 0
	var decodeErr *gotoml.DecodeError
	if errors.As(err, &decodeErr) {
		line, _ = decodeErr.Position()
	}
	return invalid("is not valid TOML", line)
}
