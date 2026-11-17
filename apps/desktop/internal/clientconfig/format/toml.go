package format

import (
	"math"
	"strconv"
	"strings"
)

// The TOML model below is the toml_edit crate's: tables keep their keys in
// order with the whitespace and comments around each key, value, and
// header, and print the way the crate prints them, so that an edit comes
// out byte for byte as the Tauri shell wrote it.

// tomlDecor is the text before and after a key, value, or header; a nil
// side prints its default.
type tomlDecor struct {
	prefix, suffix *string
}

func ptr(s string) *string { return &s }

// tomlKey is a key with the text it was written as.
type tomlKey struct {
	name string
	// repr is the key as written; nil prints its default.
	repr   *string
	leaf   tomlDecor
	dotted tomlDecor
}

func (k *tomlKey) clone() *tomlKey {
	c := *k
	return &c
}

// fmt forgets how the key was written.
func (k *tomlKey) fmt() {
	k.repr = nil
	k.leaf = tomlDecor{}
	k.dotted = tomlDecor{}
}

// TOMLItem is a *TOMLValue, a *TOMLTable written with a header, or a
// *TOMLArrayOfTables.
type TOMLItem interface{ isTOMLItem() }

type tomlEntry struct {
	key  *tomlKey
	item TOMLItem
}

// TOMLTable is a table written with a header, or the table of an inline
// table value.
type TOMLTable struct {
	entries  []*tomlEntry
	decor    tomlDecor
	implicit bool
	dotted   bool
	position int
	hasPos   bool
	inline   bool
	// preamble is the whitespace before an inline table's `}`.
	preamble string
}

// TOMLArrayOfTables is the tables of `[[name]]` headers.
type TOMLArrayOfTables struct {
	Tables []*TOMLTable
}

type tomlKind int

const (
	tomlString tomlKind = iota
	tomlInteger
	tomlFloat
	tomlBool
	tomlDatetime
	tomlArray
	tomlInline
)

// TOMLValue is a value: a scalar, an array, or an inline table.
type TOMLValue struct {
	kind tomlKind
	// str is a string's value, or a datetime as toml_datetime prints it.
	str     string
	integer int64
	float   float64
	boolean bool
	// repr is the value as written; nil prints its default.
	repr  *string
	decor tomlDecor
	// items, trailing, and trailingComma are an array's.
	items         []*TOMLValue
	trailing      string
	trailingComma bool
	table         *TOMLTable
}

func (*TOMLValue) isTOMLItem()         {}
func (*TOMLTable) isTOMLItem()         {}
func (*TOMLArrayOfTables) isTOMLItem() {}

// NewTOMLTable returns an empty table to write with a header.
func NewTOMLTable() *TOMLTable { return &TOMLTable{} }

// NewTOMLInlineTable returns an empty inline table value.
func NewTOMLInlineTable() *TOMLValue {
	return &TOMLValue{kind: tomlInline, table: &TOMLTable{inline: true}}
}

// NewTOMLString returns a string value written the default way.
func NewTOMLString(value string) *TOMLValue { return &TOMLValue{kind: tomlString, str: value} }

// TableLike returns the table of a table item or an inline table value.
func TableLike(item TOMLItem) *TOMLTable {
	switch it := item.(type) {
	case *TOMLTable:
		return it
	case *TOMLValue:
		if it.kind == tomlInline {
			return it.table
		}
	}
	return nil
}

// TOMLItemString returns the string an item holds.
func TOMLItemString(item TOMLItem) (string, bool) {
	if v, ok := item.(*TOMLValue); ok && v.kind == tomlString {
		return v.str, true
	}
	return "", false
}

// Str returns the string the value holds.
func (v *TOMLValue) Str() (string, bool) {
	if v.kind == tomlString {
		return v.str, true
	}
	return "", false
}

// Integer returns the integer the value holds.
func (v *TOMLValue) Integer() (int64, bool) {
	return v.integer, v.kind == tomlInteger
}

// Items returns an array's values; false for another value.
func (v *TOMLValue) Items() ([]*TOMLValue, bool) {
	return v.items, v.kind == tomlArray
}

// RetainItems keeps the values of an array that keep passes.
func (v *TOMLValue) RetainItems(keep func(*TOMLValue) bool) {
	kept := v.items[:0]
	for _, item := range v.items {
		if keep(item) {
			kept = append(kept, item)
		}
	}
	v.items = kept
}

// Push appends a value to an array, spaced as toml_edit spaces it.
func (v *TOMLValue) Push(item *TOMLValue) {
	if len(v.items) > 0 {
		item.decor = tomlDecor{ptr(" "), ptr("")}
	} else {
		item.decor = tomlDecor{ptr(""), ptr("")}
	}
	v.items = append(v.items, item)
}

// Retain keeps the tables that keep passes.
func (a *TOMLArrayOfTables) Retain(keep func(*TOMLTable) bool) {
	kept := a.Tables[:0]
	for _, table := range a.Tables {
		if keep(table) {
			kept = append(kept, table)
		}
	}
	a.Tables = kept
}

// Push appends a table.
func (a *TOMLArrayOfTables) Push(table *TOMLTable) { a.Tables = append(a.Tables, table) }

func (t *TOMLTable) find(name string) int {
	for i, entry := range t.entries {
		if entry.key.name == name {
			return i
		}
	}
	return -1
}

// Get returns the item at name, or nil.
func (t *TOMLTable) Get(name string) TOMLItem {
	if i := t.find(name); i >= 0 {
		return t.entries[i].item
	}
	return nil
}

// ContainsKey reports whether the table has an item at name.
func (t *TOMLTable) ContainsKey(name string) bool { return t.find(name) >= 0 }

// Insert sets the item at name. A key already there forgets how it was
// written; an inline table takes the item as a value.
func (t *TOMLTable) Insert(name string, item TOMLItem) {
	if t.inline {
		item = intoValue(item)
	}
	if i := t.find(name); i >= 0 {
		t.entries[i].key.fmt()
		t.entries[i].item = item
		return
	}
	t.entries = append(t.entries, &tomlEntry{key: &tomlKey{name: name}, item: item})
}

// Replace sets the item at name, keeping how its key was written; false
// when nothing is there.
func (t *TOMLTable) Replace(name string, item TOMLItem) bool {
	i := t.find(name)
	if i < 0 {
		return false
	}
	t.entries[i].item = item
	return true
}

// Remove drops the item at name, keeping the order of the rest.
func (t *TOMLTable) Remove(name string) TOMLItem {
	i := t.find(name)
	if i < 0 {
		return nil
	}
	item := t.entries[i].item
	t.entries = append(t.entries[:i], t.entries[i+1:]...)
	return item
}

// Names lists the table's keys in order.
func (t *TOMLTable) Names() []string {
	names := make([]string, len(t.entries))
	for i, entry := range t.entries {
		names[i] = entry.key.name
	}
	return names
}

// Len returns the number of items.
func (t *TOMLTable) Len() int { return len(t.entries) }

// IsEmpty reports whether the table holds nothing.
func (t *TOMLTable) IsEmpty() bool { return len(t.entries) == 0 }

// IsInline reports whether the table is an inline table's.
func (t *TOMLTable) IsInline() bool { return t.inline }

// IsImplicit reports whether the table prints without its own header.
func (t *TOMLTable) IsImplicit() bool { return t.implicit }

// SetImplicit sets whether the table hides its header while it holds no
// values.
func (t *TOMLTable) SetImplicit(implicit bool) { t.implicit = implicit }

// Position returns where the table's header was in the file.
func (t *TOMLTable) Position() (int, bool) { return t.position, t.hasPos }

// SetPosition sets where the table prints among the others.
func (t *TOMLTable) SetPosition(position int) { t.position, t.hasPos = position, true }

// HeaderComment reports whether a comment sits above the table's header.
func (t *TOMLTable) HeaderComment() bool {
	return t.decor.prefix != nil && strings.Contains(*t.decor.prefix, "#")
}

// TakeLayout keeps other's header text and place for t, which replaces it.
func (t *TOMLTable) TakeLayout(other *TOMLTable) {
	t.decor = other.decor
	if other.hasPos {
		t.SetPosition(other.position)
	}
}

// intoValue is an item as an inline table's value.
func intoValue(item TOMLItem) TOMLItem {
	switch it := item.(type) {
	case *TOMLTable:
		inline := &TOMLTable{inline: true, entries: it.entries}
		for _, entry := range inline.entries {
			entry.item = intoValue(entry.item)
			entry.key.leaf = tomlDecor{}
			entry.key.dotted = tomlDecor{}
			entry.item.(*TOMLValue).decor = tomlDecor{}
		}
		return &TOMLValue{kind: tomlInline, table: inline}
	case *TOMLArrayOfTables:
		array := &TOMLValue{kind: tomlArray}
		for i, table := range it.Tables {
			value := intoValue(table).(*TOMLValue)
			if i == 0 {
				value.decor = tomlDecor{ptr(""), ptr("")}
			} else {
				value.decor = tomlDecor{ptr(" "), ptr("")}
			}
			array.items = append(array.items, value)
		}
		return array
	}
	return item
}

// tomlPathValue is a value with the keys leading to it from a table.
type tomlPathValue struct {
	path  []*tomlKey
	value *TOMLValue
}

// values lists what prints in the table's body: its values, and those of
// dotted keys.
func (t *TOMLTable) values(parent []*tomlKey, out *[]tomlPathValue) {
	for _, entry := range t.entries {
		path := append(append([]*tomlKey(nil), parent...), entry.key)
		switch it := entry.item.(type) {
		case *TOMLTable:
			if it.dotted {
				it.values(path, out)
			}
		case *TOMLValue:
			if it.kind == tomlInline && it.table.dotted {
				it.table.values(path, out)
			} else {
				*out = append(*out, tomlPathValue{path, it})
			}
		}
	}
}

func (t *TOMLTable) bodyValues() []tomlPathValue {
	var out []tomlPathValue
	t.values(nil, &out)
	return out
}

type tomlWriter struct{ b strings.Builder }

// raw writes text the file had, without carriage returns.
func (w *tomlWriter) raw(s string) {
	if strings.IndexByte(s, '\r') >= 0 {
		s = strings.ReplaceAll(s, "\r", "")
	}
	w.b.WriteString(s)
}

func (w *tomlWriter) or(s *string, def string) {
	if s != nil {
		w.raw(*s)
	} else {
		w.b.WriteString(def)
	}
}

func (w *tomlWriter) keyPath(path []*tomlKey, prefix, suffix string) {
	leaf := path[len(path)-1].leaf
	for i, key := range path {
		if i == 0 {
			w.or(leaf.prefix, prefix)
		} else {
			w.b.WriteByte('.')
			w.or(key.dotted.prefix, "")
		}
		if key.repr != nil {
			w.raw(*key.repr)
		} else {
			w.b.WriteString(tomlKeyRepr(key.name))
		}
		if i == len(path)-1 {
			w.or(leaf.suffix, suffix)
		} else {
			w.or(key.dotted.suffix, "")
		}
	}
}

func (w *tomlWriter) value(v *TOMLValue, prefix, suffix string) {
	w.or(v.decor.prefix, prefix)
	switch v.kind {
	case tomlArray:
		w.b.WriteByte('[')
		for i, item := range v.items {
			if i == 0 {
				w.value(item, "", "")
			} else {
				w.b.WriteByte(',')
				w.value(item, " ", "")
			}
		}
		if v.trailingComma && len(v.items) > 0 {
			w.b.WriteByte(',')
		}
		w.raw(v.trailing)
		w.b.WriteByte(']')
	case tomlInline:
		w.b.WriteByte('{')
		w.raw(v.table.preamble)
		children := v.table.bodyValues()
		for i, child := range children {
			if i > 0 {
				w.b.WriteByte(',')
			}
			w.keyPath(child.path, " ", " ")
			w.b.WriteByte('=')
			if i == len(children)-1 {
				w.value(child.value, " ", " ")
			} else {
				w.value(child.value, " ", "")
			}
		}
		w.b.WriteByte('}')
	default:
		if v.repr != nil {
			w.raw(*v.repr)
		} else {
			w.b.WriteString(v.defaultRepr())
		}
	}
	w.or(v.decor.suffix, suffix)
}

func (v *TOMLValue) defaultRepr() string {
	switch v.kind {
	case tomlString:
		return tomlStringRepr(v.str, styleNone, literalNone)
	case tomlInteger:
		return strconv.FormatInt(v.integer, 10)
	case tomlFloat:
		return tomlFloatRepr(v.float)
	case tomlBool:
		return strconv.FormatBool(v.boolean)
	}
	return v.str
}

func tomlFloatRepr(f float64) string {
	switch {
	case math.IsNaN(f):
		if math.Signbit(f) {
			return "-nan"
		}
		return "nan"
	case f == 0:
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	repr := strconv.FormatFloat(f, 'f', -1, 64)
	if f == math.Trunc(f) {
		repr += ".0"
	}
	return repr
}

type tomlTableVisit struct {
	position int
	table    *TOMLTable
	path     []*tomlKey
	isArray  bool
}

func visitTables(table *TOMLTable, path []*tomlKey, isArray bool, visit func(*TOMLTable, []*tomlKey, bool)) {
	if !table.dotted {
		visit(table, path, isArray)
	}
	for _, entry := range table.entries {
		inner := append(append([]*tomlKey(nil), path...), entry.key)
		switch it := entry.item.(type) {
		case *TOMLTable:
			visitTables(it, inner, false, visit)
		case *TOMLArrayOfTables:
			for _, t := range it.Tables {
				visitTables(t, inner, true, visit)
			}
		}
	}
}

// String prints the document as toml_edit does, with line feeds.
func (d *TOMLDocument) String() string {
	w := &tomlWriter{}
	w.or(d.root.decor.prefix, "")
	var tables []tomlTableVisit
	last := 0
	visitTables(d.root, nil, false, func(t *TOMLTable, path []*tomlKey, isArray bool) {
		if t.hasPos {
			last = t.position
		}
		tables = append(tables, tomlTableVisit{last, t, path, isArray})
	})
	sortStable(tables)
	first := true
	for _, visit := range tables {
		w.table(visit, &first)
	}
	w.or(d.root.decor.suffix, "")
	w.raw(d.trailing)
	return w.b.String()
}

func sortStable(tables []tomlTableVisit) {
	for i := 1; i < len(tables); i++ {
		for j := i; j > 0 && tables[j].position < tables[j-1].position; j-- {
			tables[j], tables[j-1] = tables[j-1], tables[j]
		}
	}
}

func (w *tomlWriter) table(visit tomlTableVisit, first *bool) {
	t := visit.table
	children := t.bodyValues()
	header := func(open, close string) {
		prefix := "\n"
		if *first {
			*first = false
			prefix = ""
		}
		w.or(t.decor.prefix, prefix)
		w.b.WriteString(open)
		w.keyPath(visit.path, "", "")
		w.b.WriteString(close)
		w.or(t.decor.suffix, "")
		w.b.WriteByte('\n')
	}
	switch {
	case len(visit.path) == 0:
		if len(children) > 0 {
			*first = false
		}
	case visit.isArray:
		header("[[", "]]")
	case !(t.implicit && len(children) == 0):
		header("[", "]")
	}
	for _, child := range children {
		w.keyPath(child.path, "", " ")
		w.b.WriteByte('=')
		w.value(child.value, " ", "")
		w.b.WriteByte('\n')
	}
}

type stringStyle int

const (
	styleNone stringStyle = iota
	newlineTriple
	onelineTriple
	onelineSingle
)

type literalChoice int

const (
	literalNone literalChoice = iota
	literalNo
	literalYes
)

// isLiteralChar reports whether c may sit in a literal string.
func isLiteralChar(c rune) bool {
	return c == '\t' || c >= 0x20 && c <= 0x26 || c >= 0x28 && c <= 0x7E || c >= 0x80
}

func inferLiteral(value string) bool {
	if !strings.ContainsAny(value, "\"\\") {
		return false
	}
	for _, c := range value {
		if !isLiteralChar(c) {
			return false
		}
	}
	return true
}

func inferAllStyle(value string) (stringStyle, bool) {
	style := onelineSingle
	maxSingles, singles := 0, 0
	preferLiteral, canBePretty := false, true
	for _, c := range value {
		if canBePretty {
			if c == '\'' {
				singles++
				if singles >= 3 {
					canBePretty = false
				}
			} else {
				if singles > maxSingles {
					maxSingles = singles
				}
				singles = 0
			}
			switch {
			case c == '\t':
			case c == '"' || c == '\\':
				preferLiteral = true
			case c == '\n':
				style = newlineTriple
			case c <= 0x1f || c == 0x7f:
				canBePretty = false
			}
		} else if c == '\n' {
			style = newlineTriple
		}
	}
	if singles > 0 && strings.HasSuffix(value, "'") {
		canBePretty = false
	}
	if !preferLiteral {
		canBePretty = false
	}
	if !canBePretty {
		return style, false
	}
	if singles > maxSingles {
		maxSingles = singles
	}
	if style == onelineSingle && maxSingles >= 1 {
		style = onelineTriple
	}
	return style, true
}

// tomlStringRepr writes a string as toml_edit's to_string_repr does.
func tomlStringRepr(value string, style stringStyle, literal literalChoice) string {
	var isLiteral bool
	switch {
	case style == styleNone && literal == literalNone:
		style, isLiteral = inferAllStyle(value)
	case style == styleNone:
		style, _ = inferAllStyle(value)
		isLiteral = literal == literalYes
	case literal == literalNone:
		isLiteral = inferLiteral(value)
	default:
		isLiteral = literal == literalYes
	}
	var b strings.Builder
	if isLiteral {
		switch style {
		case newlineTriple:
			b.WriteString("'''\n" + value + "'''")
		case onelineTriple:
			b.WriteString("'''" + value + "'''")
		default:
			b.WriteString("'" + value + "'")
		}
		return b.String()
	}
	if style == newlineTriple {
		b.WriteString(`"""` + "\n")
	} else {
		b.WriteByte('"')
	}
	for _, c := range value {
		switch {
		case c == '\b':
			b.WriteString(`\b`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\n':
			if style == newlineTriple {
				b.WriteByte('\n')
			} else {
				b.WriteString(`\n`)
			}
		case c == '\f':
			b.WriteString(`\f`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c <= 0x1f || c == 0x7f:
			b.WriteString(`\u` + strings.ToUpper(leftPad(strconv.FormatInt(int64(c), 16), 4)))
		default:
			b.WriteRune(c)
		}
	}
	if style == newlineTriple {
		b.WriteString(`"""`)
	} else {
		b.WriteByte('"')
	}
	return b.String()
}

func leftPad(s string, width int) string {
	for len(s) < width {
		s = "0" + s
	}
	return s
}

func isBareKeyByte(c byte) bool {
	return isASCIIAlnum(c) || c == '-' || c == '_'
}

// tomlKeyRepr writes a key as toml_edit writes a new one.
func tomlKeyRepr(name string) string {
	bare := name != ""
	for i := 0; i < len(name); i++ {
		if !isBareKeyByte(name[i]) {
			bare = false
			break
		}
	}
	if bare {
		return name
	}
	return tomlStringRepr(name, onelineSingle, literalNone)
}
