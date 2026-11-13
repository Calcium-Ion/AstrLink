package format

import (
	"fmt"
	"strings"
)

// JSONFormat is how a client reads a JSON file. Syntax is nil for strict
// JSON. Each of Objects must be an object where it appears; the root is one
// too unless RootList lets it be a list.
type JSONFormat struct {
	Syntax   *JSONSyntax
	Objects  []string
	RootList bool
}

// JSONDocument is a JSON file whose root is an object, or a list of
// entries, edited in place.
type JSONDocument struct {
	root *cstNode
	// object is the root object; nil when the root is a list.
	object *cstNode
	// finalNewline is whether the text ends with a line break, as a new
	// file does.
	finalNewline bool
}

// listEntry splits a `<list>[<id>]` key: `<list>` is the path of the list
// (empty for a root list) and `<id>` the entry's `id`, or the entry itself
// in a list of names.
func listEntry(path string) (list, id string, ok bool) {
	rest, found := strings.CutSuffix(path, "]")
	if !found {
		return "", "", false
	}
	return strings.Cut(rest, "[")
}

// isEntry reports whether node is the entry id names: an object with that
// `id`, or that string.
func isEntry(node *cstNode, id string) bool {
	value, ok := cstValue(node)
	if !ok {
		return false
	}
	switch v := value.(type) {
	case map[string]any:
		got, _ := v["id"].(string)
		_, isString := v["id"].(string)
		return isString && got == id
	case string:
		return v == id
	}
	return false
}

// JSONKey splits a JSON key into the objects holding it and its property
// name. No key AstrLink writes has a dot in a property name.
func JSONKey(path string) ([]string, string) {
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return strings.Split(path[:i], "."), path[i+1:]
	}
	return nil, path
}

func jsonParent(object *cstNode, parents []string) *cstNode {
	for _, name := range parents {
		if object == nil {
			return nil
		}
		object = object.objectValue(name)
	}
	return object
}

// validateJSON rejects what the client itself cannot read: the CST parser
// takes more.
func validateJSON(syntax *JSONSyntax, text string, rootList bool) *Invalid {
	var rootFits bool
	if syntax != nil {
		if _, err := parseJSONText(text, *syntax, false); err != nil {
			return invalid(fmt.Sprintf("is not valid JSON (%s)", err.kind), err.line)
		}
		// The value parser needs the tree to tell the root's kind.
		tree, _ := parseJSONText(text, anySyntax, true)
		value := tree.rootValue()
		rootFits = value != nil && (value.kind == cstObject || value.kind == cstArray && rootList)
	} else {
		value, err := DecodeJSON(text)
		if err != nil {
			line, column := strictJSONErrorPosition(text)
			return invalid(fmt.Sprintf("is not valid JSON (line %d, column %d)", line, column), line)
		}
		_, isObject := value.(map[string]any)
		_, isList := value.([]any)
		rootFits = isObject || rootList && isList
	}
	if !rootFits {
		return invalid("is not a JSON object", 0)
	}
	return nil
}

// ParseJSON opens a JSON file. A blank file is a new, empty object.
func ParseJSON(text string, format JSONFormat) (*JSONDocument, error) {
	// The CST parser takes more than the clients do, so validate first.
	if strings.TrimSpace(text) != "" {
		if err := validateJSON(format.Syntax, text, format.RootList); err != nil {
			return nil, err
		}
	}
	root, err := parseJSONText(text, anySyntax, true)
	if err != nil {
		return nil, invalid("is not valid JSON", err.line)
	}
	doc := &JSONDocument{root: root}
	// A root list stays one; only an empty file becomes an object.
	if value := root.rootValue(); value == nil || value.kind != cstArray {
		doc.object = root.rootObjectOrSet()
	}
	for _, container := range format.Objects {
		parents, name := JSONKey(container)
		if parent := jsonParent(doc.object, parents); parent != nil {
			if prop := parent.get(name); prop != nil {
				if value := prop.propValue(); value == nil || value.kind != cstObject {
					return nil, invalid(fmt.Sprintf("has a %s value that is not an object", container), 0)
				}
			}
		}
	}
	doc.finalNewline = strings.TrimSpace(text) == "" || strings.HasSuffix(text, "\n")
	return doc, nil
}

// IsList reports whether the root is a list.
func (d *JSONDocument) IsList() bool { return d.object == nil }

func (d *JSONDocument) property(path string) *cstNode {
	parents, name := JSONKey(path)
	parent := jsonParent(d.object, parents)
	if parent == nil {
		return nil
	}
	return parent.get(name)
}

// Has reports whether the file has a property at path.
func (d *JSONDocument) Has(path string) bool { return d.property(path) != nil }

// list is the list at path: the root list for an empty path.
func (d *JSONDocument) list(path string) *cstNode {
	if path == "" {
		if value := d.root.rootValue(); value != nil && value.kind == cstArray {
			return value
		}
		return nil
	}
	prop := d.property(path)
	if prop == nil {
		return nil
	}
	if value := prop.propValue(); value != nil && value.kind == cstArray {
		return value
	}
	return nil
}

// entry is the entry a `<list>[<id>]` key names.
func (d *JSONDocument) entry(path string) *cstNode {
	list, id, ok := listEntry(path)
	if !ok {
		return nil
	}
	array := d.list(list)
	if array == nil {
		return nil
	}
	for _, node := range array.elements() {
		if isEntry(node, id) {
			return node
		}
	}
	return nil
}

// Get returns the string at path: present is false when there is nothing
// there, and ok false when the value is not a string. A list entry reads as
// its name, or as compact JSON with sorted keys.
func (d *JSONDocument) Get(path string) (value string, ok, present bool) {
	if _, _, isEntry := listEntry(path); isEntry {
		node := d.entry(path)
		if node == nil {
			return "", false, false
		}
		v, read := cstValue(node)
		if !read {
			return "", false, true
		}
		if name, isName := v.(string); isName {
			return name, true, true
		}
		return Encode(v), true, true
	}
	prop := d.property(path)
	if prop == nil {
		return "", false, false
	}
	node := prop.propValue()
	if node == nil || node.kind != cstString {
		return "", false, true
	}
	text, decoded := node.decoded()
	return text, decoded, true
}

// GetJSON returns the value at path, whatever it is: present is false when
// there is nothing there, and ok false when it does not read as JSON.
func (d *JSONDocument) GetJSON(path string) (value any, ok, present bool) {
	prop := d.property(path)
	if prop == nil {
		return nil, false, false
	}
	node := prop.propValue()
	if node == nil {
		return nil, false, true
	}
	value, ok = cstValue(node)
	return value, ok, true
}

// IsStringAt reports whether the value at path is a string literal.
func (d *JSONDocument) IsStringAt(path string) bool {
	prop := d.property(path)
	if prop == nil {
		return false
	}
	value := prop.propValue()
	return value != nil && value.kind == cstString
}

// IsObjectAt reports whether the value at path is an object.
func (d *JSONDocument) IsObjectAt(path string) bool {
	prop := d.property(path)
	if prop == nil {
		return false
	}
	value := prop.propValue()
	return value != nil && value.kind == cstObject
}

func (d *JSONDocument) setInput(path string, value cstInput) error {
	if list, _, ok := listEntry(path); ok {
		return d.setEntry(path, list, value)
	}
	parents, name := JSONKey(path)
	parent := d.object
	if parent == nil {
		return fmt.Errorf("is a list where an object is expected")
	}
	for _, segment := range parents {
		parent = parent.objectValueOrCreate(segment)
		if parent == nil {
			return fmt.Errorf("has a %s value that is not an object", segment)
		}
	}
	if prop := parent.get(name); prop != nil {
		prop.setPropValue(value)
	} else {
		parent.appendProp(name, value)
	}
	return nil
}

// setEntry replaces the entry a `<list>[<id>]` key names, or appends it,
// creating the list when the root is an object.
func (d *JSONDocument) setEntry(path, list string, value cstInput) error {
	_, id, _ := listEntry(path)
	if array := d.list(list); array != nil {
		elements := array.elements()
		for index, node := range elements {
			if isEntry(node, id) {
				node.remove()
				array.insertElement(index, value)
				return nil
			}
		}
	}
	array := d.list(list)
	if array == nil {
		if list == "" {
			return fmt.Errorf("is an object where a list is expected")
		}
		if d.property(list) != nil {
			return fmt.Errorf("has a %s value that is not a list", list)
		}
		if err := d.setInput(list, cstInput{kind: inputArray}); err != nil {
			return err
		}
		array = d.list(list)
	}
	// A one-line list of names stays on one line.
	if value.kind == inputObject {
		ensureMultiline(array)
	}
	array.appendElement(value)
	return nil
}

// Set sets a string, creating the objects around it. A list entry's value
// is its name, or its JSON when it starts with `{`.
func (d *JSONDocument) Set(path, value string) error {
	if _, _, ok := listEntry(path); ok && strings.HasPrefix(value, "{") {
		entry, err := DecodeJSON(value)
		if err != nil {
			return err
		}
		return d.SetJSON(path, entry, NoRank)
	}
	return d.setInput(path, cstInput{kind: inputString, text: value})
}

// SetJSON sets any value; rank orders the properties of its objects.
func (d *JSONDocument) SetJSON(path string, value any, rank Rank) error {
	return d.setInput(path, cstInputOf(value, rank))
}

// Remove drops the property or list entry at path.
func (d *JSONDocument) Remove(path string) {
	if _, _, ok := listEntry(path); ok {
		if node := d.entry(path); node != nil {
			node.remove()
		}
	} else if prop := d.property(path); prop != nil {
		prop.remove()
	}
}

// RemoveItems removes the items of the list at path that drop picks.
func (d *JSONDocument) RemoveItems(path string, drop func(any) bool) {
	list := d.list(path)
	if list == nil {
		return
	}
	for _, item := range list.elements() {
		if value, ok := cstValue(item); ok && drop(value) {
			item.remove()
		}
	}
}

// FindItem returns the first item of the list at path that pick picks.
func (d *JSONDocument) FindItem(path string, pick func(any) bool) (any, bool) {
	list := d.list(path)
	if list == nil {
		return nil, false
	}
	for _, item := range list.elements() {
		if value, ok := cstValue(item); ok && pick(value) {
			return value, true
		}
	}
	return nil, false
}

// PutItem replaces the first item of the list at path that pick picks with
// item, or appends item; rank orders its properties.
func (d *JSONDocument) PutItem(path string, pick func(any) bool, item any, rank Rank) error {
	list := d.list(path)
	if list == nil {
		return fmt.Errorf("has a %s value that is not a list", path)
	}
	value := cstInputOf(item, rank)
	for index, node := range list.elements() {
		if current, ok := cstValue(node); ok && pick(current) {
			node.remove()
			list.insertElement(index, value)
			return nil
		}
	}
	ensureMultiline(list)
	list.appendElement(value)
	return nil
}

// PrependItems puts items at the start of the list at path, creating the
// list; rank orders the properties of their objects.
func (d *JSONDocument) PrependItems(path string, items []any, rank Rank) error {
	list := d.list(path)
	if list == nil {
		if d.property(path) != nil {
			return fmt.Errorf("has a %s value that is not a list", path)
		}
		if err := d.setInput(path, cstInput{kind: inputArray}); err != nil {
			return err
		}
		list = d.list(path)
	}
	if len(items) == 0 {
		return nil
	}
	ensureMultiline(list)
	for index, item := range items {
		list.insertElement(index, cstInputOf(item, rank))
	}
	return nil
}

// DropEmpty drops each of containers, innermost first, that is an empty
// object.
func (d *JSONDocument) DropEmpty(containers []string) {
	for _, container := range containers {
		prop := d.property(container)
		if prop == nil {
			continue
		}
		if value := prop.propValue(); value != nil && value.kind == cstObject && len(value.properties()) == 0 {
			prop.remove()
		}
	}
}

// JSONProperty is a property of the root object, as HoldsOnly shows it.
type JSONProperty struct {
	Name    string
	HasName bool
	node    *cstNode
}

// IsEmptyObject reports whether the property holds an object without
// properties.
func (p JSONProperty) IsEmptyObject() bool {
	value := p.node.propValue()
	return value != nil && value.kind == cstObject && len(value.properties()) == 0
}

// HoldsOnly reports whether every property of the root is one kept
// passes; true for an empty object. A root list holds only what it is empty
// of.
func (d *JSONDocument) HoldsOnly(kept func(JSONProperty) bool) bool {
	if d.object == nil {
		list := d.root.rootValue()
		return list == nil || len(list.elements()) == 0
	}
	for _, prop := range d.object.properties() {
		name, ok := prop.decodedName()
		if !kept(JSONProperty{Name: name, HasName: ok, node: prop}) {
			return false
		}
	}
	return true
}

// IsEmpty reports whether nothing is left.
func (d *JSONDocument) IsEmpty() bool {
	return d.HoldsOnly(func(JSONProperty) bool { return false })
}

// KeyValue is a key with its value; Present is false for a value that is
// not a string.
type KeyValue struct {
	Key     string
	Value   string
	Present bool
}

// ObjectKeys appends the properties of the object at prefix (ending in
// `.`) as keys, or the object itself when it is something else. Properties
// named in expand that hold objects list their own properties. get reads
// each value as the file's client reads it.
func (d *JSONDocument) ObjectKeys(prefix string, expand []string, get func(string) (string, bool), keys *[]KeyValue) {
	path := strings.TrimRight(prefix, ".")
	prop := d.property(path)
	if prop == nil {
		return
	}
	provider := prop.propValue()
	if provider == nil || provider.kind != cstObject {
		*keys = append(*keys, KeyValue{Key: path})
		return
	}
	for _, property := range provider.properties() {
		name, ok := property.decodedName()
		if !ok {
			continue
		}
		path := prefix + name
		value := property.propValue()
		if contains(expand, name) && value != nil && value.kind == cstObject {
			d.ObjectKeys(path+".", nil, get, keys)
		} else {
			v, present := get(path)
			*keys = append(*keys, KeyValue{Key: path, Value: v, Present: present})
		}
	}
}

// ObjectNames lists the property names of the object at path.
func (d *JSONDocument) ObjectNames(path string) []string {
	var object *cstNode
	if path == "" {
		object = d.object
	} else if prop := d.property(path); prop != nil {
		if value := prop.propValue(); value != nil && value.kind == cstObject {
			object = value
		}
	}
	if object == nil {
		return nil
	}
	var names []string
	for _, prop := range object.properties() {
		if name, ok := prop.decodedName(); ok {
			names = append(names, name)
		}
	}
	return names
}

// Value returns the whole file as JSON.
func (d *JSONDocument) Value() (any, bool) {
	value := d.root.rootValue()
	if value == nil {
		return nil, false
	}
	return cstValue(value)
}

// Render returns the text, ending as the file did: some clients save
// without a final line break.
func (d *JSONDocument) Render() string {
	text := d.root.String()
	if d.finalNewline && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text
}

func contains(list []string, item string) bool {
	for _, known := range list {
		if known == item {
			return true
		}
	}
	return false
}

// strictJSONErrorPosition is the 1-based line and column a strict JSON
// reader stops at in text. Like serde_json, it counts the character it
// stopped at, so at the end of the text the column is the last one's.
func strictJSONErrorPosition(text string) (int, int) {
	offset := len(text)
	if _, err := parseJSONText(text, JSONSyntax{}, false); err != nil {
		offset = err.offset
	}
	line, column := 1, 0
	for _, c := range text[:offset] {
		if c == '\n' {
			line, column = line+1, 0
		} else {
			column++
		}
	}
	if offset < len(text) {
		column++
	}
	return line, column
}
