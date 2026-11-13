package format

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/parser"
	yamltoken "github.com/goccy/go-yaml/token"
)

// yamlTree is where a YAML file's collections, keys, and items are, read
// from go-yaml's syntax tree, for edits that splice the text.
type yamlTree struct {
	text string
	// starts holds where each line starts; a final line break starts no
	// line.
	starts []int
	// comments holds the offset of the `#` of each line's comment.
	comments map[int]int
	// docEnd is the line of a `...` ending the document, else the line
	// count.
	docEnd int
	root   *yamlNode
}

type yamlKind int

const (
	yamlNone yamlKind = iota
	yamlScalarNode
	yamlMappingNode
	yamlSequenceNode
)

type yamlNode struct {
	kind yamlKind
	flow bool
	// start is where the node's text starts, its tag or anchor included;
	// -1 for a null written as nothing. inner is where its own token is.
	start, inner int
	// open and close are a flow collection's brackets.
	open, close int
	entries     []*yamlEntry
	items       []*yamlItem
}

type yamlEntry struct {
	name     string
	keyStart int
	keyLine  int
	keyCol   int
	colon    int
	// bound is the line the entry ends before: the next key's, or its
	// mapping's bound.
	bound int
	value *yamlNode
}

type yamlItem struct {
	dash  int
	line  int
	col   int
	bound int
	value *yamlNode
}

func newYAMLTree(text string) (*yamlTree, error) {
	file, err := parser.ParseBytes([]byte(text), parser.ParseComments)
	if err != nil || len(file.Docs) > 1 {
		return nil, errYAMLUnsafe
	}
	t := &yamlTree{text: text, comments: map[int]int{}}
	for i := 0; i < len(text); {
		t.starts = append(t.starts, i)
		next := strings.IndexByte(text[i:], '\n')
		if next < 0 {
			break
		}
		i += next + 1
	}
	t.docEnd = len(t.starts)
	for _, tk := range lexer.Tokenize(text) {
		switch tk.Type {
		case yamltoken.CommentType:
			at := t.offset(tk.Position)
			t.comments[t.lineOf(at)] = at
		case yamltoken.DocumentEndType:
			if line := tk.Position.Line - 1; line < t.docEnd {
				t.docEnd = line
			}
		}
	}
	if len(file.Docs) == 1 && file.Docs[0].Body != nil {
		if _, ok := file.Docs[0].Body.(*ast.CommentGroupNode); !ok {
			t.root, err = t.node(file.Docs[0].Body, t.docEnd)
			if err != nil {
				return nil, err
			}
		}
	}
	return t, nil
}

// offset is where a token's position is in the text; columns count runes.
func (t *yamlTree) offset(pos *yamltoken.Position) int {
	line := pos.Line - 1
	if line < 0 || line >= len(t.starts) {
		return len(t.text)
	}
	at, end := t.starts[line], t.end(line)
	for col := 1; col < pos.Column && at < end; col++ {
		_, size := utf8.DecodeRuneInString(t.text[at:])
		at += size
	}
	// go-yaml counts a tab short; no token starts with a blank.
	for at < end && (t.text[at] == ' ' || t.text[at] == '\t') {
		at++
	}
	return at
}

func (t *yamlTree) lineOf(offset int) int {
	return max(sort.SearchInts(t.starts, offset+1)-1, 0)
}

// end is where a line's text ends, before its line break.
func (t *yamlTree) end(line int) int {
	end := t.next(line)
	if end > t.starts[line] && t.text[end-1] == '\n' {
		end--
		if end > t.starts[line] && t.text[end-1] == '\r' {
			end--
		}
	}
	return end
}

// next is where the line after line starts.
func (t *yamlTree) next(line int) int {
	if line+1 < len(t.starts) {
		return t.starts[line+1]
	}
	return len(t.text)
}

func (t *yamlTree) blank(line int) bool {
	return strings.TrimSpace(t.text[t.starts[line]:t.end(line)]) == ""
}

// content reports whether a line holds more than blanks and a comment.
func (t *yamlTree) content(line int) bool {
	text := t.text[t.starts[line]:t.end(line)]
	trimmed := strings.TrimLeft(text, " \t")
	if trimmed == "" {
		return false
	}
	at, ok := t.comments[line]
	return !ok || at != t.starts[line]+len(text)-len(trimmed)
}

// lastContent is the last line in [from, bound) with content, at least
// from.
func (t *yamlTree) lastContent(from, bound int) int {
	for line := min(bound, len(t.starts)) - 1; line > from; line-- {
		if t.content(line) {
			return line
		}
	}
	return from
}

// atLineStart reports whether only blanks come before offset on its line.
func (t *yamlTree) atLineStart(offset int) bool {
	return strings.Trim(t.text[t.starts[t.lineOf(offset)]:offset], " \t") == ""
}

func (t *yamlTree) node(n ast.Node, bound int) (*yamlNode, error) {
	out := &yamlNode{start: -1, inner: -1}
	if tk := n.GetToken(); tk != nil && tk.Type != yamltoken.ImplicitNullType {
		out.start = t.offset(tk.Position)
	}
	inner := n
	for {
		switch v := inner.(type) {
		case *ast.TagNode:
			inner = v.Value
			continue
		case *ast.AnchorNode:
			inner = v.Value
			continue
		}
		break
	}
	if inner == nil {
		return out, nil
	}
	if tk := inner.GetToken(); tk != nil && tk.Type != yamltoken.ImplicitNullType {
		out.inner = t.offset(tk.Position)
	}
	switch v := inner.(type) {
	case *ast.MappingNode:
		return out, t.mapping(out, v.IsFlowStyle, v.Start, v.End, v.Values, bound)
	case *ast.MappingValueNode:
		return out, t.mapping(out, false, nil, nil, []*ast.MappingValueNode{v}, bound)
	case *ast.SequenceNode:
		return out, t.sequence(out, v, bound)
	case *ast.NullNode:
		if v.GetToken().Type == yamltoken.ImplicitNullType && out.start < 0 {
			return out, nil
		}
	}
	out.kind = yamlScalarNode
	return out, nil
}

func (t *yamlTree) mapping(out *yamlNode, flow bool, open, close *yamltoken.Token, values []*ast.MappingValueNode, bound int) error {
	out.kind, out.flow = yamlMappingNode, flow
	if flow {
		if open == nil || close == nil {
			return errYAMLUnsafe
		}
		out.open, out.close = t.offset(open.Position), t.offset(close.Position)
	}
	for _, mv := range values {
		key, ok := rawScalar(mv.Key)
		if !ok {
			key = "\x00"
		}
		at := t.offset(mv.Key.GetToken().Position)
		line := t.lineOf(at)
		out.entries = append(out.entries, &yamlEntry{
			name:     key,
			keyStart: at,
			keyLine:  line,
			keyCol:   at - t.starts[line],
			colon:    t.offset(mv.Start.Position),
		})
	}
	if !flow && len(out.entries) > 0 {
		out.start = out.entries[0].keyStart
		out.inner = out.start
	}
	for i, entry := range out.entries {
		entry.bound = bound
		if i+1 < len(out.entries) && !flow {
			entry.bound = out.entries[i+1].keyLine
		}
		value, err := t.node(values[i].Value, entry.bound)
		if err != nil {
			return err
		}
		entry.value = value
	}
	return nil
}

func (t *yamlTree) sequence(out *yamlNode, n *ast.SequenceNode, bound int) error {
	out.kind, out.flow = yamlSequenceNode, n.IsFlowStyle
	if out.flow {
		if n.Start == nil || n.End == nil {
			return errYAMLUnsafe
		}
		out.open, out.close = t.offset(n.Start.Position), t.offset(n.End.Position)
	}
	for i := range n.Values {
		item := &yamlItem{dash: -1}
		if !out.flow {
			if i >= len(n.Entries) || n.Entries[i] == nil || n.Entries[i].Start == nil {
				return errYAMLUnsafe
			}
			item.dash = t.offset(n.Entries[i].Start.Position)
			item.line = t.lineOf(item.dash)
			item.col = item.dash - t.starts[item.line]
		}
		out.items = append(out.items, item)
	}
	for i, item := range out.items {
		item.bound = bound
		if i+1 < len(out.items) && !out.flow {
			item.bound = out.items[i+1].line
		}
		value, err := t.node(n.Values[i], item.bound)
		if err != nil {
			return err
		}
		item.value = value
	}
	return nil
}

func (m *yamlNode) entry(name string) (int, *yamlEntry) {
	for i, entry := range m.entries {
		if entry.name == name {
			return i, entry
		}
	}
	return -1, nil
}

// locate finds the node at path, and the entry holding it when a key
// leads there.
func (t *yamlTree) locate(path []YAMLStep) (*yamlNode, *yamlEntry, bool) {
	node := t.root
	var holder *yamlEntry
	for _, step := range path {
		if node == nil {
			return nil, nil, false
		}
		if step.isIndex {
			if node.kind != yamlSequenceNode || step.index < 0 || step.index >= len(node.items) {
				return nil, nil, false
			}
			node, holder = node.items[step.index].value, nil
			continue
		}
		if node.kind != yamlMappingNode {
			return nil, nil, false
		}
		_, entry := node.entry(step.key)
		if entry == nil {
			return nil, nil, false
		}
		node, holder = entry.value, entry
	}
	return node, holder, true
}

// rootEnd is where a line added to the root collection goes: the end of
// the document, before blanks trailing its last line break. lead is set
// when the line must start with a line break.
func (t *yamlTree) rootEnd() (at int, lead bool) {
	if t.docEnd < len(t.starts) {
		return t.starts[t.docEnd], false
	}
	last := strings.LastIndexByte(t.text, '\n')
	if strings.Trim(t.text[last+1:], " \t") == "" {
		return last + 1, false
	}
	return len(t.text), true
}

// insertLines adds lines, separated by "\n" with no final one, after the
// last entry or item of node, or at the end of the document for the root.
func (t *yamlTree) insertLines(node *yamlNode, root bool, lines string) string {
	var at int
	var lead bool
	if root {
		at, lead = t.rootEnd()
	} else {
		first, bound := 0, 0
		if n := len(node.entries); n > 0 {
			first, bound = node.entries[n-1].keyLine, node.entries[n-1].bound
		} else {
			first, bound = node.items[len(node.items)-1].line, node.items[len(node.items)-1].bound
		}
		at = t.next(t.lastContent(first, bound))
		lead = at == len(t.text) && !strings.HasSuffix(t.text, "\n")
	}
	if lead {
		lines = "\n" + lines
	} else {
		lines += "\n"
	}
	return t.text[:at] + lines + t.text[at:]
}

// blockLines is value as block YAML, its lines indented by base, with no
// final line break.
func blockLines(block string, base int) string {
	lines := strings.Split(strings.TrimSuffix(block, "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.Repeat(" ", base) + line
	}
	return strings.Join(lines, "\n")
}

func (t *yamlTree) set(parent []YAMLStep, key string, value any, rank Rank) (string, error) {
	node, _, ok := t.locate(parent)
	if !ok {
		return "", errYAMLUnsafe
	}
	if node == nil || node.kind == yamlNone {
		if len(parent) > 0 || node != nil {
			return "", errYAMLUnsafe
		}
		return t.insertKey(nil, true, key, value, rank), nil
	}
	if node.kind != yamlMappingNode {
		return "", errYAMLUnsafe
	}
	if node.flow {
		return t.flowSet(node, key, value, rank)
	}
	if _, entry := node.entry(key); entry != nil {
		return t.replace(entry, value, rank), nil
	}
	return t.insertKey(node, len(parent) == 0, key, value, rank), nil
}

func (t *yamlTree) insertKey(mapping *yamlNode, root bool, key string, value any, rank Rank) string {
	col := 0
	if mapping != nil {
		col = mapping.entries[0].keyCol
	}
	line := strings.Repeat(" ", col) + yamlString(key, true, 0) + ":"
	if yamlIsLeaf(value) {
		line += " " + yamlScalar(value, col+2)
	} else {
		base := col + 2
		if _, list := value.([]any); list && col == 0 {
			base = 0
		}
		line += "\n" + blockLines(yamlBlock(value, yamlIndentUnit(t.text), rank), base)
	}
	return t.insertLines(mapping, root, line)
}

// keyLineTail is the comment after a key's value on the key's line, with
// the blanks before it.
func (t *yamlTree) keyLineTail(entry *yamlEntry) string {
	at, ok := t.comments[entry.keyLine]
	if !ok || at <= entry.colon {
		return ""
	}
	start := at
	for start > entry.colon+1 && (t.text[start-1] == ' ' || t.text[start-1] == '\t') {
		start--
	}
	return t.text[start:t.end(entry.keyLine)]
}

// valueEnd is where a value written on one line ends.
func (t *yamlTree) valueEnd(value *yamlNode, line int) int {
	if value.flow {
		return value.close + 1
	}
	end := t.end(line)
	if at, ok := t.comments[line]; ok && at > value.start {
		end = at
	}
	for end > value.start && (t.text[end-1] == ' ' || t.text[end-1] == '\t') {
		end--
	}
	return end
}

// replace sets an entry's value: a scalar where the old one was, a block
// on the lines below the key.
func (t *yamlTree) replace(entry *yamlEntry, value any, rank Rank) string {
	old := entry.value
	last := t.lastContent(entry.keyLine, entry.bound)
	single := old.start >= 0 && t.lineOf(old.start) == entry.keyLine && last == entry.keyLine
	tail := t.keyLineTail(entry)
	if yamlIsLeaf(value) {
		text := yamlScalar(value, entry.keyCol+2)
		if single {
			return t.text[:old.start] + text + t.text[t.valueEnd(old, entry.keyLine):]
		}
		return t.text[:entry.colon+1] + " " + text + tail + t.text[t.end(last):]
	}
	block := blockLines(yamlBlock(value, yamlIndentUnit(t.text), rank), entry.keyCol+2)
	return t.text[:entry.colon+1] + tail + "\n" + block + t.text[t.end(last):]
}

// flowEnd is where a value in a flow collection ends.
func (t *yamlTree) flowEnd(value *yamlNode) int {
	if value.flow {
		return value.close + 1
	}
	text, at := t.text, value.inner
	switch {
	case at < 0:
		return value.start
	case text[at] == '"':
		for at++; at < len(text); at++ {
			switch text[at] {
			case '\\':
				at++
			case '"':
				return at + 1
			}
		}
	case text[at] == '\'':
		for at++; at < len(text); at++ {
			if text[at] == '\'' {
				if at+1 < len(text) && text[at+1] == '\'' {
					at++
					continue
				}
				return at + 1
			}
		}
	default:
		for ; at < len(text) && !strings.ContainsRune(",]}\r\n", rune(text[at])); at++ {
			if text[at] == '#' && (text[at-1] == ' ' || text[at-1] == '\t') {
				break
			}
		}
		for at > value.inner && (text[at-1] == ' ' || text[at-1] == '\t') {
			at--
		}
	}
	return at
}

func (t *yamlTree) flowSet(mapping *yamlNode, key string, value any, rank Rank) (string, error) {
	text := yamlFlowValue(value, rank)
	if _, entry := mapping.entry(key); entry != nil {
		if entry.value.start < 0 {
			return "", errYAMLUnsafe
		}
		return t.text[:entry.value.start] + text + t.text[t.flowEnd(entry.value):], nil
	}
	pair := yamlFlowString(key) + ": " + text
	if len(mapping.entries) == 0 {
		return t.text[:mapping.open+1] + pair + t.text[mapping.open+1:], nil
	}
	last := mapping.entries[len(mapping.entries)-1]
	if last.value.start < 0 {
		return "", errYAMLUnsafe
	}
	end := t.flowEnd(last.value)
	return t.text[:end] + ", " + pair + t.text[end:], nil
}

func (t *yamlTree) push(parent []YAMLStep, value any, rank Rank) (string, error) {
	list, _, ok := t.locate(parent)
	if !ok || list == nil || list.kind != yamlSequenceNode {
		return "", errYAMLUnsafe
	}
	if list.flow {
		text := yamlFlowValue(value, rank)
		if len(list.items) == 0 {
			return t.text[:list.open+1] + text + t.text[list.open+1:], nil
		}
		end := t.flowEnd(list.items[len(list.items)-1].value)
		return t.text[:end] + ", " + text + t.text[end:], nil
	}
	col := list.items[0].col
	var item string
	if yamlIsLeaf(value) {
		item = "- " + yamlScalar(value, col+2)
	} else {
		item = "- " + strings.ReplaceAll(strings.TrimSuffix(yamlBlock(value, yamlIndentUnit(t.text), rank), "\n"), "\n", "\n  ")
	}
	return t.insertLines(list, len(parent) == 0, blockLines(item, col)), nil
}

// fillRoot writes block items into an empty root list: over `[]`, or below
// the comments of a file with nothing else. yaml-edit would grow `[]` into
// one long line.
func (t *yamlTree) fillRoot(items string) string {
	if root := t.root; root != nil && root.kind == yamlSequenceNode && root.flow && len(root.items) == 0 &&
		strings.TrimSpace(t.text[root.open:root.close+1]) == "[]" {
		rest := t.text[root.close+1:]
		if rest != "" {
			items = strings.TrimRight(items, " \t\r\n")
		}
		return t.text[:root.open] + items + rest
	}
	out := t.text
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out + items
}

func (t *yamlTree) remove(path []YAMLStep) (string, error) {
	parent, holder, ok := t.locate(path[:len(path)-1])
	last := path[len(path)-1]
	if !ok || parent == nil {
		return "", errYAMLUnsafe
	}
	if !last.isIndex {
		if parent.kind != yamlMappingNode {
			return "", errYAMLUnsafe
		}
		index, _ := parent.entry(last.key)
		if index < 0 {
			return "", errYAMLUnsafe
		}
		if parent.flow {
			return t.flowRemove(parent, index)
		}
		return t.removeEntry(parent, index)
	}
	if parent.kind != yamlSequenceNode {
		return "", errYAMLUnsafe
	}
	if parent.flow {
		return t.flowRemove(parent, last.index)
	}
	return t.removeItem(parent, last.index, holder)
}

// cut drops [from, to); a line cut from the end of a file with no final
// line break takes the break before it.
func (t *yamlTree) cut(from, to int) string {
	if to == len(t.text) && !strings.HasSuffix(t.text, "\n") && from > 0 {
		from--
		if from > 0 && t.text[from-1] == '\r' {
			from--
		}
	}
	return t.text[:from] + t.text[to:]
}

func (t *yamlTree) removeEntry(mapping *yamlNode, index int) (string, error) {
	entry := mapping.entries[index]
	last := t.lastContent(entry.keyLine, entry.bound)
	if len(mapping.entries) == 1 {
		return t.text[:entry.keyStart] + "{}" + t.text[t.end(last):], nil
	}
	if !t.atLineStart(entry.keyStart) {
		if index+1 >= len(mapping.entries) {
			return "", errYAMLUnsafe
		}
		return t.text[:entry.keyStart] + t.text[mapping.entries[index+1].keyStart:], nil
	}
	to := t.next(last)
	// A one-line entry takes the blank lines between it and the next key.
	if last == entry.keyLine && index+1 < len(mapping.entries) {
		next, line := mapping.entries[index+1].keyLine, last+1
		for line < next && t.blank(line) {
			line++
		}
		if line == next {
			to = t.starts[next]
		}
	}
	return t.cut(t.starts[entry.keyLine], to), nil
}

func (t *yamlTree) removeItem(list *yamlNode, index int, holder *yamlEntry) (string, error) {
	item := list.items[index]
	last := t.lastContent(item.line, item.bound)
	if len(list.items) == 1 {
		if holder != nil {
			return t.text[:holder.colon+1] + " []" + t.text[t.end(last):], nil
		}
		return t.text[:item.dash] + "[]" + t.text[t.end(last):], nil
	}
	if !t.atLineStart(item.dash) {
		if index+1 >= len(list.items) {
			return "", errYAMLUnsafe
		}
		return t.text[:item.dash] + t.text[list.items[index+1].dash:], nil
	}
	return t.cut(t.starts[item.line], t.next(last)), nil
}

// flowRemove drops an entry or item of a flow collection with the comma
// beside it.
func (t *yamlTree) flowRemove(collection *yamlNode, index int) (string, error) {
	type span struct{ start, end int }
	var spans []span
	for _, entry := range collection.entries {
		if entry.value.start < 0 {
			return "", errYAMLUnsafe
		}
		spans = append(spans, span{entry.keyStart, t.flowEnd(entry.value)})
	}
	for _, item := range collection.items {
		if item.value.start < 0 {
			return "", errYAMLUnsafe
		}
		spans = append(spans, span{item.value.start, t.flowEnd(item.value)})
	}
	if index < 0 || index >= len(spans) {
		return "", errYAMLUnsafe
	}
	switch {
	case len(spans) == 1:
		return t.text[:spans[0].start] + t.text[spans[0].end:], nil
	case index+1 < len(spans):
		return t.text[:spans[index].start] + t.text[spans[index+1].start:], nil
	}
	return t.text[:spans[index-1].end] + t.text[spans[index].end:], nil
}
