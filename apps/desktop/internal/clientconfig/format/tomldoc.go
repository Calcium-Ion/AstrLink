package format

import (
	"slices"
	"strconv"
	"strings"
)

// TOMLDocument is a TOML file edited in place.
type TOMLDocument struct {
	root     *TOMLTable
	trailing string
	crlf     bool
}

// ParseTOML opens a TOML file.
func ParseTOML(text string) (*TOMLDocument, error) {
	doc, err := parseTOMLText(text)
	if err != nil {
		end := min(err.offset, len(text))
		return nil, invalid("is not valid TOML", strings.Count(text[:end], "\n")+1)
	}
	if err := validateTOML(text); err != nil {
		return nil, err
	}
	// A file of comments alone keeps them as trailing text, below anything
	// added; as the root's leading text they stay on top.
	if doc.root.IsEmpty() && doc.trailing != "" {
		doc.root.decor.prefix = ptr(doc.trailing)
		doc.trailing = ""
	}
	doc.crlf = strings.Contains(text, "\r\n")
	return doc, nil
}

// Root returns the root table.
func (d *TOMLDocument) Root() *TOMLTable { return d.root }

// Get returns the string at a root key: present is false when there is
// nothing there, and ok false for another value.
func (d *TOMLDocument) Get(key string) (value string, ok, present bool) {
	item := d.root.Get(key)
	if item == nil {
		return "", false, false
	}
	value, ok = TOMLItemString(item)
	return value, ok, true
}

// Set sets a string at a root key.
func (d *TOMLDocument) Set(key, value string) error {
	SetTOMLString(d.root, key, value)
	return nil
}

// Remove drops a root key, keeping the comment above it.
func (d *TOMLDocument) Remove(key string) { d.removeRootKey(key) }

// IsEmpty reports whether nothing but whitespace is left.
func (d *TOMLDocument) IsEmpty() bool { return strings.TrimSpace(d.String()) == "" }

// Render returns the text; a CRLF file stays CRLF.
func (d *TOMLDocument) Render() string {
	out := d.String()
	if d.crlf {
		out = strings.ReplaceAll(strings.ReplaceAll(out, "\r\n", "\n"), "\n", "\r\n")
	}
	return out
}

// SetTOMLString replaces a string in place, keeping the comment and
// spacing around it.
func SetTOMLString(table *TOMLTable, key, value string) {
	i := table.find(key)
	if i < 0 {
		table.Insert(key, NewTOMLString(value))
		return
	}
	entry := table.entries[i]
	next := NewTOMLString(value)
	if current, ok := entry.item.(*TOMLValue); ok {
		next.decor = current.decor
	}
	entry.item = next
}

// TOMLJSON returns a value or table as JSON; false for what JSON cannot
// hold.
func TOMLJSON(item TOMLItem) (any, bool) {
	switch it := item.(type) {
	case *TOMLValue:
		return tomlValueJSON(it)
	case *TOMLTable:
		return tomlTableJSON(it)
	}
	return nil, false
}

func tomlTableJSON(table *TOMLTable) (any, bool) {
	object := map[string]any{}
	for _, entry := range table.entries {
		value, ok := TOMLJSON(entry.item)
		if !ok {
			return nil, false
		}
		object[entry.key.name] = value
	}
	return object, true
}

func tomlValueJSON(v *TOMLValue) (any, bool) {
	switch v.kind {
	case tomlString, tomlDatetime:
		return v.str, true
	case tomlInteger:
		return Number(strconv.FormatInt(v.integer, 10)), true
	case tomlFloat:
		if n, ok := ParseNumber(strconv.FormatFloat(v.float, 'g', -1, 64)); ok {
			return n, true
		}
		// serde_json holds no NaN or infinity.
		return nil, true
	case tomlBool:
		return v.boolean, true
	case tomlArray:
		items := []any{}
		for _, item := range v.items {
			value, ok := tomlValueJSON(item)
			if !ok {
				return nil, false
			}
			items = append(items, value)
		}
		return items, true
	}
	return tomlTableJSON(v.table)
}

// TOMLValueOf returns JSON as a TOML value; false for null.
func TOMLValueOf(value any) (*TOMLValue, bool) {
	switch v := value.(type) {
	case string:
		return NewTOMLString(v), true
	case bool:
		return &TOMLValue{kind: tomlBool, boolean: v}, true
	case Number:
		if n, err := strconv.ParseInt(string(v), 10, 64); err == nil {
			return &TOMLValue{kind: tomlInteger, integer: n}, true
		}
		f, err := strconv.ParseFloat(string(v), 64)
		if err != nil {
			return nil, false
		}
		return &TOMLValue{kind: tomlFloat, float: f}, true
	case []any:
		array := &TOMLValue{kind: tomlArray}
		for _, item := range v {
			inner, ok := TOMLValueOf(item)
			if !ok {
				return nil, false
			}
			array.Push(inner)
		}
		return array, true
	case map[string]any:
		inline := NewTOMLInlineTable()
		for _, name := range SortedKeys(v) {
			inner, ok := TOMLValueOf(v[name])
			if !ok {
				return nil, false
			}
			inline.table.Insert(name, inner)
		}
		return inline, true
	}
	return nil, false
}

// TOMLTableOf returns a table of an object's properties in rank order;
// false for what TOML cannot hold.
func TOMLTableOf(value any, rank Rank) (*TOMLTable, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	names := SortedKeys(object)
	slices.SortStableFunc(names, func(a, b string) int {
		ra, rb := rank(a), rank(b)
		switch {
		case ra < rb:
			return -1
		case ra > rb:
			return 1
		}
		return 0
	})
	table := NewTOMLTable()
	for _, name := range names {
		inner, ok := TOMLValueOf(object[name])
		if !ok {
			return nil, false
		}
		table.Insert(name, inner)
	}
	return table, true
}

// ParentTable returns the table at `[parent]`, adding an implicit one when
// missing.
func (d *TOMLDocument) ParentTable(parent string) (*TOMLTable, error) {
	if !d.root.ContainsKey(parent) {
		table := NewTOMLTable()
		table.SetImplicit(true)
		d.root.Insert(parent, table)
	}
	table, ok := d.root.Get(parent).(*TOMLTable)
	if !ok {
		return nil, errorf("has a %s value that is not a table", parent)
	}
	return table, nil
}

// DropEmptyParent drops `[parent]` once a removal left it empty, unless a
// comment sits on its header.
func (d *TOMLDocument) DropEmptyParent(parent string) {
	table, ok := d.root.Get(parent).(*TOMLTable)
	if ok && table.IsEmpty() && (table.implicit || !table.HeaderComment()) {
		d.root.Remove(parent)
	}
}

// PrefixedTables returns the tables in `[parent]` whose names start with
// prefix, as one JSON object by name; false when there are none.
func (d *TOMLDocument) PrefixedTables(parent, prefix string) (map[string]any, bool) {
	table := TableLike(d.root.Get(parent))
	if table == nil {
		return nil, false
	}
	tables := map[string]any{}
	for _, entry := range table.entries {
		if !strings.HasPrefix(entry.key.name, prefix) {
			continue
		}
		if value, ok := TOMLJSON(entry.item); ok {
			tables[entry.key.name] = value
		}
	}
	return tables, len(tables) > 0
}

// RemovePrefixed drops the tables PrefixedTables reads.
func (d *TOMLDocument) RemovePrefixed(parent, prefix string) {
	table := TableLike(d.root.Get(parent))
	if table == nil {
		return
	}
	for _, name := range table.Names() {
		if strings.HasPrefix(name, prefix) {
			table.Remove(name)
		}
	}
	d.DropEmptyParent(parent)
}

// SetPrefixed replaces the tables PrefixedTables reads with value's.
func (d *TOMLDocument) SetPrefixed(parent, prefix string, value any, rank Rank) error {
	broken := errorf("%s tables cannot be written as TOML", parent)
	tables, ok := value.(map[string]any)
	if !ok {
		return broken
	}
	d.RemovePrefixed(parent, prefix)
	into, err := d.ParentTable(parent)
	if err != nil {
		return err
	}
	for _, name := range SortedKeys(tables) {
		table, ok := TOMLTableOf(tables[name], rank)
		if !ok {
			return broken
		}
		into.Insert(name, table)
	}
	return nil
}

// removeRootKey removes a root key but keeps the comment above it, handing
// it to the neighbouring key: a file's header note often sits above
// `model`.
func (d *TOMLDocument) removeRootKey(key string) {
	root := d.root
	index := root.find(key)
	if index < 0 {
		return
	}
	prefix := root.entries[index].key.leaf.prefix
	root.Remove(key)
	if prefix == nil || !strings.Contains(*prefix, "#") {
		return
	}
	comment := *prefix
	var values []int
	for i, entry := range root.entries {
		if _, ok := entry.item.(*TOMLValue); ok {
			values = append(values, i)
		}
	}
	raw := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	for _, i := range values {
		if i >= index {
			leaf := &root.entries[i].key.leaf
			leaf.prefix = ptr(comment + raw(leaf.prefix))
			return
		}
	}
	if len(values) > 0 {
		value := root.entries[values[len(values)-1]].item.(*TOMLValue)
		value.decor.suffix = ptr(raw(value.decor.suffix) + "\n" + strings.TrimRight(comment, "\n"))
		return
	}
	root.decor.prefix = ptr(comment + raw(root.decor.prefix))
}
