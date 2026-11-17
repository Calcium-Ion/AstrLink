package format

import (
	"errors"
	"math"
	"strconv"
	"strings"

	goyaml "github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	yamltoken "github.com/goccy/go-yaml/token"
)

// YAML is edited in place: go-yaml finds where things are, and the edit
// splices only the text it changes, laid out the way the yaml-edit crate
// lays it out for the Tauri shell. Every edit is checked before it counts:
// the result must read back as exactly the data the edit means, and keep
// every comment line, line ending, and document marker. Anything else
// leaves the file as it was.
//
// What AstrLink adds is block YAML it writes itself, quoting every string
// that YAML 1.1 and 1.2 readers could read differently.

// YAMLRoot is what the root of a file must be.
type YAMLRoot int

const (
	YAMLMapping YAMLRoot = iota
	YAMLSequence
)

// YAMLStep is one step of a path into a document: a key or a list index.
type YAMLStep struct {
	key     string
	index   int
	isIndex bool
}

// YAMLKey is the step to a mapping's key.
func YAMLKey(name string) YAMLStep { return YAMLStep{key: name} }

// YAMLIndex is the step to a list's item.
func YAMLIndex(index int) YAMLStep { return YAMLStep{index: index, isIndex: true} }

// errYAMLUnsafe is why an edit was refused.
var errYAMLUnsafe = errors.New("has YAML AstrLink cannot change without touching the rest of the file")

// YAMLDocument is a YAML file edited in place.
type YAMLDocument struct {
	text string
	// value is what text holds: nil, bool, Number, float64 for an infinity
	// or NaN, string, []any, and map[string]any.
	value any
	root  YAMLRoot
}

// ParseYAML opens a YAML file whose root must be root, or empty.
func ParseYAML(text string, root YAMLRoot) (*YAMLDocument, error) {
	value, err := readYAML(text)
	if err != nil {
		return nil, err
	}
	fits := value == nil
	switch value.(type) {
	case map[string]any:
		fits = root == YAMLMapping
	case []any:
		fits = root == YAMLSequence
	}
	if !fits {
		if root == YAMLMapping {
			return nil, invalid("is not a YAML mapping", 0)
		}
		return nil, invalid("is not a YAML list", 0)
	}
	// go-yaml must place everything the file has, or no edit can keep it.
	if _, err := newYAMLTree(lineFeeds(text)); err != nil {
		return nil, invalid(errYAMLUnsafe.Error(), 0)
	}
	return &YAMLDocument{text: text, value: value, root: root}, nil
}

// lineFeeds is text with its CRLF line breaks as line feeds: go-yaml
// miscounts the lines of a CRLF file after a comment.
func lineFeeds(text string) string { return strings.ReplaceAll(text, "\r\n", "\n") }

// readYAML reads the one document a file holds.
func readYAML(text string) (any, error) {
	file, err := parser.ParseBytes([]byte(lineFeeds(text)), parser.ParseComments)
	if err != nil {
		var syntax *goyaml.SyntaxError
		if errors.As(err, &syntax) && syntax.Token != nil {
			return nil, invalid("is not valid YAML", syntax.Token.Position.Line)
		}
		return nil, invalid("is not valid YAML", 0)
	}
	if len(file.Docs) > 1 {
		return nil, invalid("is not valid YAML", 0)
	}
	if len(file.Docs) == 0 {
		return nil, nil
	}
	reader := yamlReader{anchors: map[string]any{}}
	value, err := reader.value(file.Docs[0].Body)
	if err != nil {
		return nil, invalid("is not valid YAML", 0)
	}
	return value, nil
}

// Value returns the data the file holds.
func (d *YAMLDocument) Value() any { return d.value }

// Get returns the value at path.
func (d *YAMLDocument) Get(path ...YAMLStep) (any, bool) {
	return yamlGet(d.value, path)
}

func yamlGet(value any, path []YAMLStep) (any, bool) {
	for _, step := range path {
		switch v := value.(type) {
		case map[string]any:
			if step.isIndex {
				return nil, false
			}
			next, ok := v[step.key]
			if !ok {
				return nil, false
			}
			value = next
		case []any:
			if !step.isIndex || step.index < 0 || step.index >= len(v) {
				return nil, false
			}
			value = v[step.index]
		default:
			return nil, false
		}
	}
	return value, true
}

// GetJSON returns the value at path as JSON, so reformatting the file
// changes nothing.
func (d *YAMLDocument) GetJSON(path ...YAMLStep) (any, bool) {
	value, ok := d.Get(path...)
	if !ok {
		return nil, false
	}
	return yamlJSON(value), true
}

func yamlJSON(value any) any {
	switch v := value.(type) {
	case float64:
		// JSON holds no infinity or NaN.
		return nil
	case []any:
		items := make([]any, len(v))
		for i, item := range v {
			items[i] = yamlJSON(item)
		}
		return items
	case map[string]any:
		object := make(map[string]any, len(v))
		for name, item := range v {
			object[name] = yamlJSON(item)
		}
		return object
	}
	return value
}

// IsEmpty reports whether the file holds no data.
func (d *YAMLDocument) IsEmpty() bool {
	switch v := d.value.(type) {
	case nil:
		return true
	case map[string]any:
		return len(v) == 0
	case []any:
		return len(v) == 0
	}
	return false
}

// Render returns the text.
func (d *YAMLDocument) Render() string { return d.text }

// edit runs change on the text and keeps the result once it holds
// expected. additions also asks that every comment line stays.
func (d *YAMLDocument) edit(expected any, additions bool, change func(*yamlTree) (string, error)) error {
	// The edit is made with line feeds; a CRLF file gets its CRLF back.
	// One that mixes the two cannot keep both.
	crlf := strings.Contains(d.text, "\r\n")
	if crlf && bareLineFeed(d.text) {
		return errYAMLUnsafe
	}
	tree, err := newYAMLTree(lineFeeds(d.text))
	if err != nil {
		return errYAMLUnsafe
	}
	out, err := change(tree)
	if err != nil {
		return err
	}
	if crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	read, err := readYAML(out)
	kept := err == nil && yamlSame(read, expected) &&
		!(strings.Contains(d.text, "\r\n") && bareLineFeed(out)) &&
		(hasYAMLMarker(d.text) || !hasYAMLMarker(out)) &&
		(!additions || commentsKept(d.text, out))
	if !kept {
		return errYAMLUnsafe
	}
	d.text, d.value = out, expected
	return nil
}

// Set sets key of the mapping at parent, replacing the whole value of a
// key it has.
func (d *YAMLDocument) Set(parent []YAMLStep, key string, value any, rank Rank) error {
	expected := yamlClone(d.value)
	if len(parent) == 0 && expected == nil {
		if d.root != YAMLMapping {
			return errYAMLUnsafe
		}
		expected = map[string]any{}
	}
	expected, ok := yamlReplace(expected, parent, func(at any) (any, bool) {
		mapping, ok := at.(map[string]any)
		if ok {
			mapping[key] = yamlClone(value)
		}
		return mapping, ok
	})
	if !ok {
		return errYAMLUnsafe
	}
	return d.edit(expected, true, func(t *yamlTree) (string, error) {
		return t.set(parent, key, value, rank)
	})
}

// Push appends value to the list at parent; an empty root becomes a list
// of it.
func (d *YAMLDocument) Push(parent []YAMLStep, value any, rank Rank) error {
	expected := yamlClone(d.value)
	if len(parent) == 0 && expected == nil {
		if d.root != YAMLSequence {
			return errYAMLUnsafe
		}
		expected = []any{}
	}
	expected, ok := yamlReplace(expected, parent, func(at any) (any, bool) {
		items, ok := at.([]any)
		return append(items, yamlClone(value)), ok
	})
	if !ok {
		return errYAMLUnsafe
	}
	items, _ := d.value.([]any)
	emptyRoot := len(parent) == 0 && len(items) == 0
	return d.edit(expected, true, func(t *yamlTree) (string, error) {
		if emptyRoot {
			return t.fillRoot(yamlBlock([]any{value}, yamlIndentUnit(t.text), rank)), nil
		}
		return t.push(parent, value, rank)
	})
}

// Remove drops the key or item at path. A list emptied this way stays, as
// `[]`.
func (d *YAMLDocument) Remove(path ...YAMLStep) error {
	if len(path) == 0 {
		return nil
	}
	if _, ok := d.Get(path...); !ok {
		return nil
	}
	last, parent := path[len(path)-1], path[:len(path)-1]
	// The last key of a nested mapping leaves `{}`, not a null.
	if !last.isIndex && len(parent) > 0 && !parent[len(parent)-1].isIndex {
		if mapping, ok := yamlGet(d.value, parent); ok {
			if mapping, ok := mapping.(map[string]any); ok && len(mapping) == 1 {
				name := parent[len(parent)-1].key
				return d.Set(parent[:len(parent)-1], name, map[string]any{}, func(string) int { return 0 })
			}
		}
	}
	expected, ok := yamlReplace(yamlClone(d.value), parent, func(at any) (any, bool) {
		switch v := at.(type) {
		case map[string]any:
			delete(v, last.key)
			return v, !last.isIndex
		case []any:
			if !last.isIndex {
				return nil, false
			}
			return append(v[:last.index:last.index], v[last.index+1:]...), true
		}
		return nil, false
	})
	if !ok {
		return errYAMLUnsafe
	}
	return d.edit(expected, false, func(t *yamlTree) (string, error) {
		return t.remove(path)
	})
}

// Dotted keys, for a mapping file: `refs.NAME`.

func yamlKeyPath(key string) []YAMLStep {
	names := strings.Split(key, ".")
	path := make([]YAMLStep, len(names))
	for i, name := range names {
		path[i] = YAMLKey(name)
	}
	return path
}

// GetKey returns the string at a dotted key: present is false when there
// is nothing there, and ok false for another value.
func (d *YAMLDocument) GetKey(key string) (value string, ok, present bool) {
	at, present := d.Get(yamlKeyPath(key)...)
	value, ok = at.(string)
	return value, ok, present
}

// SetKey sets a string, creating the mappings around it.
func (d *YAMLDocument) SetKey(key, value string) error { return d.SetKeyJSON(key, value) }

// SetKeyJSON sets any value, creating the mappings around it.
func (d *YAMLDocument) SetKeyJSON(key string, value any) error {
	names := strings.Split(key, ".")
	// The deepest mapping that exists takes the rest as one block.
	depth := len(names) - 1
	for ; depth > 0; depth-- {
		if _, ok := d.Get(yamlKeyPath(strings.Join(names[:depth], "."))...); ok {
			break
		}
	}
	nested := value
	for i := len(names) - 1; i > depth; i-- {
		nested = map[string]any{names[i]: nested}
	}
	var parent []YAMLStep
	if depth > 0 {
		parent = yamlKeyPath(strings.Join(names[:depth], "."))
	}
	return d.Set(parent, names[depth], nested, NoRank)
}

// RemoveKey drops a dotted key.
func (d *YAMLDocument) RemoveKey(key string) error { return d.Remove(yamlKeyPath(key)...) }

// yamlClone copies a value, numbers in the form serde_json writes them.
func yamlClone(value any) any {
	switch v := value.(type) {
	case Number:
		if n, ok := ParseNumber(string(v)); ok {
			return n
		}
	case []any:
		items := make([]any, len(v))
		for i, item := range v {
			items[i] = yamlClone(item)
		}
		return items
	case map[string]any:
		object := make(map[string]any, len(v))
		for name, item := range v {
			object[name] = yamlClone(item)
		}
		return object
	}
	return value
}

// yamlReplace replaces the value at path in root with what change makes
// of it; false when there is nothing there or change refuses.
func yamlReplace(root any, path []YAMLStep, change func(any) (any, bool)) (any, bool) {
	if len(path) == 0 {
		return change(root)
	}
	step := path[0]
	switch v := root.(type) {
	case map[string]any:
		child, ok := v[step.key]
		if step.isIndex || !ok {
			return nil, false
		}
		next, ok := yamlReplace(child, path[1:], change)
		v[step.key] = next
		return v, ok
	case []any:
		if !step.isIndex || step.index < 0 || step.index >= len(v) {
			return nil, false
		}
		next, ok := yamlReplace(v[step.index], path[1:], change)
		v[step.index] = next
		return v, ok
	}
	return nil, false
}

// yamlSame reports whether a and b hold the same data; an empty root is
// no root.
func yamlSame(a, b any) bool {
	empty := func(value any) bool {
		mapping, ok := value.(map[string]any)
		return value == nil || ok && len(mapping) == 0
	}
	return yamlEqual(a, b) || empty(a) && empty(b)
}

func yamlEqual(a, b any) bool {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for name, item := range x {
			other, ok := y[name]
			if !ok || !yamlEqual(item, other) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !yamlEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case float64:
		y, ok := b.(float64)
		return ok && (x == y || math.IsNaN(x) && math.IsNaN(y))
	}
	return a == b
}

func bareLineFeed(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' && (i == 0 || text[i-1] != '\r') {
			return true
		}
	}
	return false
}

// yamlLines splits text as Rust's str::lines does.
func yamlLines(text string) []string {
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") || text == "" {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

func hasYAMLMarker(text string) bool {
	for _, line := range yamlLines(text) {
		if line == "---" || strings.HasPrefix(line, "--- ") {
			return true
		}
	}
	return false
}

// commentsKept reports whether every comment line of before is a line of
// after.
func commentsKept(before, after string) bool {
	lines := map[string]bool{}
	for _, line := range yamlLines(after) {
		lines[line] = true
	}
	for _, line := range yamlLines(before) {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") && !lines[line] {
			return false
		}
	}
	return true
}

// yamlReader turns go-yaml's syntax tree into data as yaml_serde reads it.
type yamlReader struct {
	anchors map[string]any
}

func (r *yamlReader) value(node ast.Node) (any, error) {
	switch n := node.(type) {
	case nil, *ast.CommentGroupNode:
		return nil, nil
	case *ast.DocumentNode:
		return r.value(n.Body)
	case *ast.StringNode:
		if quoted(n.GetToken()) {
			return n.Value, nil
		}
		return yamlPlain(plainText(n)), nil
	case *ast.NullNode, *ast.BoolNode, *ast.IntegerNode, *ast.FloatNode, *ast.InfinityNode, *ast.NanNode:
		tk := n.GetToken()
		if tk == nil || tk.Type == yamltoken.ImplicitNullType {
			return nil, nil
		}
		return yamlPlain(tk.Value), nil
	case *ast.LiteralNode:
		return n.Value.Value, nil
	case *ast.AnchorNode:
		value, err := r.value(n.Value)
		if err != nil {
			return nil, err
		}
		r.anchors[n.Name.GetToken().Value] = value
		return value, nil
	case *ast.AliasNode:
		value, ok := r.anchors[n.Value.GetToken().Value]
		if !ok {
			return nil, errYAMLUnsafe
		}
		return yamlClone(value), nil
	case *ast.TagNode:
		return r.tagged(n)
	case *ast.MappingKeyNode:
		return r.value(n.Value)
	case *ast.MergeKeyNode:
		return "<<", nil
	case *ast.MappingNode:
		return r.mapping(n.Values)
	case *ast.MappingValueNode:
		return r.mapping([]*ast.MappingValueNode{n})
	case *ast.SequenceNode:
		items := make([]any, 0, len(n.Values))
		for _, item := range n.Values {
			value, err := r.value(item)
			if err != nil {
				return nil, err
			}
			items = append(items, value)
		}
		return items, nil
	}
	return nil, errYAMLUnsafe
}

func (r *yamlReader) mapping(values []*ast.MappingValueNode) (any, error) {
	mapping := make(map[string]any, len(values))
	for _, entry := range values {
		key, err := r.value(entry.Key)
		if err != nil {
			return nil, err
		}
		value, err := r.value(entry.Value)
		if err != nil {
			return nil, err
		}
		mapping[yamlKeyText(key)] = value
	}
	return mapping, nil
}

// tagged reads a value with a tag: the core schema's as what they name,
// another as yaml_serde's tagged value serializes, `{"!tag": value}`.
func (r *yamlReader) tagged(n *ast.TagNode) (any, error) {
	tag := n.Start.Value
	raw, isScalar := rawScalar(n.Value)
	switch tag {
	case "!!str":
		if isScalar {
			return raw, nil
		}
	case "!!int", "!!float", "!!bool", "!!null":
		if isScalar {
			return yamlPlain(raw), nil
		}
	case "!!map", "!!seq":
		return r.value(n.Value)
	}
	value, err := r.value(n.Value)
	if err != nil {
		return nil, err
	}
	return map[string]any{"!" + strings.TrimLeft(tag, "!"): value}, nil
}

// rawScalar returns a scalar's text, unresolved.
func rawScalar(node ast.Node) (string, bool) {
	switch n := node.(type) {
	case *ast.StringNode:
		if quoted(n.GetToken()) {
			return n.Value, true
		}
		return plainText(n), true
	case *ast.LiteralNode:
		return n.Value.Value, true
	case *ast.NullNode, *ast.BoolNode, *ast.IntegerNode, *ast.FloatNode, *ast.InfinityNode, *ast.NanNode:
		tk := n.GetToken()
		if tk.Type == yamltoken.ImplicitNullType {
			return "", true
		}
		return tk.Value, true
	}
	return "", false
}

// plainText is a plain scalar's text. go-yaml drops the tabs inside one,
// so a scalar on one line is read from the text it was written as.
func plainText(n *ast.StringNode) string {
	tk := n.GetToken()
	if tk == nil {
		return n.Value
	}
	origin := strings.Trim(tk.Origin, " \t\r\n")
	if origin == "" || strings.ContainsAny(origin, "\r\n") {
		return n.Value
	}
	return origin
}

func quoted(tk *yamltoken.Token) bool {
	return tk != nil && (tk.Type == yamltoken.DoubleQuoteType || tk.Type == yamltoken.SingleQuoteType)
}

func yamlKeyText(key any) string {
	switch k := key.(type) {
	case string:
		return k
	case Number:
		return string(k)
	case bool:
		return strconv.FormatBool(k)
	case nil:
		return "null"
	}
	return Encode(yamlJSON(key))
}

// yamlPlain resolves a plain scalar as yaml_serde does.
func yamlPlain(text string) any {
	switch text {
	case "", "~", "null", "Null", "NULL":
		return nil
	case "true", "True", "TRUE":
		return true
	case "false", "False", "FALSE":
		return false
	}
	if n, ok := yamlInt(text); ok {
		return n
	}
	if digitsButNotNumber(text) {
		return text
	}
	if f, ok := yamlFloat(text); ok {
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return f
		}
		return Number(FormatFloat(f))
	}
	return text
}

// digitsButNotNumber reports a leading zero followed by digits, a string
// in YAML 1.2.
func digitsButNotNumber(text string) bool {
	text = strings.TrimPrefix(strings.TrimPrefix(text, "-"), "+")
	return len(text) > 1 && text[0] == '0' && allDigits(text[1:], 10)
}

func yamlInt(text string) (Number, bool) {
	unsigned := strings.TrimPrefix(text, "+")
	negative := strings.HasPrefix(text, "-")
	for _, radix := range []struct {
		prefix string
		base   int
	}{{"0x", 16}, {"0o", 8}, {"0b", 2}} {
		digits, ok := strings.CutPrefix(strings.TrimPrefix(unsigned, "-"), radix.prefix)
		if !ok || digits == "" || digits[0] == '+' || digits[0] == '-' {
			continue
		}
		if negative {
			if n, err := strconv.ParseInt("-"+digits, radix.base, 64); err == nil {
				return Number(strconv.FormatInt(n, 10)), true
			}
		} else if n, err := strconv.ParseUint(digits, radix.base, 64); err == nil {
			return Number(strconv.FormatUint(n, 10)), true
		}
	}
	if digitsButNotNumber(text) || unsigned != text && (strings.HasPrefix(unsigned, "+") || strings.HasPrefix(unsigned, "-")) {
		return "", false
	}
	if !negative {
		if n, err := strconv.ParseUint(unsigned, 10, 64); err == nil && allDigits(unsigned, 10) {
			return Number(strconv.FormatUint(n, 10)), true
		}
		return "", false
	}
	if n, err := strconv.ParseInt(text, 10, 64); err == nil && allDigits(text[1:], 10) {
		return Number(strconv.FormatInt(n, 10)), true
	}
	return "", false
}

// yamlFloat parses a float as yaml_serde does: the `.inf` forms, or what
// Rust's f64 parser takes and holds finitely.
func yamlFloat(text string) (float64, bool) {
	unsigned := text
	if rest, ok := strings.CutPrefix(text, "+"); ok {
		if strings.HasPrefix(rest, "+") || strings.HasPrefix(rest, "-") {
			return 0, false
		}
		unsigned = rest
	}
	switch {
	case unsigned == ".inf" || unsigned == ".Inf" || unsigned == ".INF":
		return math.Inf(1), true
	case text == "-.inf" || text == "-.Inf" || text == "-.INF":
		return math.Inf(-1), true
	case text == ".nan" || text == ".NaN" || text == ".NAN":
		return math.NaN(), true
	}
	if !isRustFloat(unsigned) {
		return 0, false
	}
	f, err := strconv.ParseFloat(unsigned, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// ReadYAML reads the data of a YAML file of any shape, for a file
// AstrLink only looks at.
func ReadYAML(text string) (any, error) { return readYAML(text) }

// YAMLMappingOf returns a mapping value; a tagged value, which reads as
// `{"!tag": value}`, is none.
func YAMLMappingOf(value any) (map[string]any, bool) {
	mapping, ok := value.(map[string]any)
	if ok && len(mapping) == 1 {
		for name := range mapping {
			if strings.HasPrefix(name, "!") {
				return nil, false
			}
		}
	}
	return mapping, ok
}
