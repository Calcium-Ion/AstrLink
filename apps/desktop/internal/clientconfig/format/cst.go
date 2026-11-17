package format

import (
	"fmt"
	"strings"
	"unicode"
)

// The concrete syntax tree of a JSON file, a port of the jsonc-parser
// crate's cst module that the Tauri shell edited client configs with. Every
// comment and piece of whitespace is a node, and an edit inserts nodes the
// way the crate does, so a file comes out byte for byte as it did there.

type cstKind uint8

const (
	cstRoot cstKind = iota
	cstObject
	cstArray
	cstProp
	cstString
	cstWord
	cstNumber
	cstBool
	cstNull
	cstToken
	cstWhitespace
	cstNewline
	cstComment
)

type cstNode struct {
	kind     cstKind
	text     string
	children []*cstNode
	parent   *cstNode
}

func (n *cstNode) isContainer() bool { return n.kind <= cstProp }

func (n *cstNode) isTrivia() bool {
	return n.kind == cstWhitespace || n.kind == cstNewline || n.kind == cstComment
}

func (n *cstNode) isValueLeaf() bool { return n.kind >= cstString && n.kind <= cstNull }

func (n *cstNode) isNewline() bool    { return n.kind == cstNewline }
func (n *cstNode) isWhitespace() bool { return n.kind == cstWhitespace }
func (n *cstNode) isComment() bool    { return n.kind == cstComment }
func (n *cstNode) isComma() bool      { return n.kind == cstToken && n.text == "," }

func (n *cstNode) isLineComment() bool {
	return n.kind == cstComment && strings.HasPrefix(n.text, "//")
}

func (n *cstNode) String() string {
	if !n.isContainer() {
		return n.text
	}
	var b strings.Builder
	n.write(&b)
	return b.String()
}

func (n *cstNode) write(b *strings.Builder) {
	if !n.isContainer() {
		b.WriteString(n.text)
		return
	}
	for _, child := range n.children {
		child.write(b)
	}
}

func (n *cstNode) childIndex() int {
	if n.parent == nil {
		return 0
	}
	for i, child := range n.parent.children {
		if child == n {
			return i
		}
	}
	return 0
}

func (n *cstNode) childAt(index int) *cstNode {
	if index < 0 || index >= len(n.children) {
		return nil
	}
	return n.children[index]
}

// previousSiblings lists the siblings before n, nearest first.
func (n *cstNode) previousSiblings() []*cstNode {
	if n.parent == nil {
		return nil
	}
	index := n.childIndex()
	siblings := make([]*cstNode, 0, index)
	for i := index - 1; i >= 0; i-- {
		siblings = append(siblings, n.parent.children[i])
	}
	return siblings
}

// nextSiblings lists the siblings after n, nearest first.
func (n *cstNode) nextSiblings() []*cstNode {
	if n.parent == nil {
		return nil
	}
	return append([]*cstNode(nil), n.parent.children[n.childIndex()+1:]...)
}

func (n *cstNode) rootNode() *cstNode {
	for p := n.parent; p != nil; p = p.parent {
		if p.kind == cstRoot {
			return p
		}
	}
	return nil
}

// removeRaw takes n out of the tree without adjusting its siblings.
func (n *cstNode) removeRaw() {
	if n.parent == nil {
		return
	}
	parent := n.parent
	index := n.childIndex()
	parent.children = append(parent.children[:index], parent.children[index+1:]...)
	n.parent = nil
}

// insert puts nodes at *at (or at the end when at is nil) and moves *at
// past them.
func (n *cstNode) insert(at *int, nodes ...*cstNode) {
	index := len(n.children)
	if at != nil {
		index = *at
		*at += len(nodes)
	}
	for _, node := range nodes {
		node.parent = n
	}
	rest := append([]*cstNode(nil), n.children[index:]...)
	n.children = append(append(n.children[:index], nodes...), rest...)
}

func leaf(kind cstKind, text string) *cstNode { return &cstNode{kind: kind, text: text} }

func token(c string) *cstNode { return leaf(cstToken, c) }

type newlineKind string

const (
	lineFeed       newlineKind = "\n"
	carriageReturn newlineKind = "\r"
	crlf           newlineKind = "\r\n"
)

func newline(kind newlineKind) *cstNode { return leaf(cstNewline, string(kind)) }

func whitespace(text string) *cstNode { return leaf(cstWhitespace, text) }

// escapedString is a string literal for value.
func escapedString(value string) *cstNode {
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range value {
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\b':
			b.WriteString(`\b`)
		case c == '\f':
			b.WriteString(`\f`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case unicode.IsControl(c):
			fmt.Fprintf(&b, `\u%04x`, c)
		default:
			b.WriteRune(c)
		}
	}
	b.WriteByte('"')
	return leaf(cstString, b.String())
}

// decoded is the value of a string literal, or a word used as a name.
func (n *cstNode) decoded() (string, bool) {
	switch n.kind {
	case cstWord:
		return n.text, true
	case cstString:
		value, _, kind, _ := decodeJSONString(n.text, 0, true)
		return value, kind == ""
	}
	return "", false
}

// propName is a property's name node.
func (n *cstNode) propName() *cstNode {
	for _, child := range n.children {
		if child.kind == cstString || child.kind == cstWord {
			return child
		}
	}
	return nil
}

func (n *cstNode) decodedName() (string, bool) {
	name := n.propName()
	if name == nil {
		return "", false
	}
	return name.decoded()
}

// propValue is a property's value node.
func (n *cstNode) propValue() *cstNode {
	name := n.propName()
	if name == nil {
		return nil
	}
	children := n.children[name.childIndex()+1:]
	i := 0
	for ; i < len(children); i++ {
		if children[i].kind == cstToken && children[i].text == ":" {
			i++
			break
		}
	}
	for ; i < len(children); i++ {
		child := children[i]
		switch {
		case child.isValueLeaf(), child.kind == cstObject, child.kind == cstArray:
			return child
		case child.kind == cstRoot, child.kind == cstProp:
			return nil
		}
	}
	return nil
}

func (n *cstNode) properties() []*cstNode {
	var props []*cstNode
	for _, child := range n.children {
		if child.kind == cstProp {
			props = append(props, child)
		}
	}
	return props
}

func (n *cstNode) elements() []*cstNode {
	var elements []*cstNode
	for _, child := range n.children {
		if child.isContainer() || child.isValueLeaf() {
			elements = append(elements, child)
		}
	}
	return elements
}

// get is the object's property name.
func (n *cstNode) get(name string) *cstNode {
	for _, prop := range n.properties() {
		if decoded, ok := prop.decodedName(); ok && decoded == name {
			return prop
		}
	}
	return nil
}

func (n *cstNode) objectValue(name string) *cstNode {
	prop := n.get(name)
	if prop == nil {
		return nil
	}
	if value := prop.propValue(); value != nil && value.kind == cstObject {
		return value
	}
	return nil
}

// objectValueOrCreate is the object at name, appended when missing; nil
// when name holds something else.
func (n *cstNode) objectValueOrCreate(name string) *cstNode {
	if prop := n.get(name); prop != nil {
		if value := prop.propValue(); value != nil && value.kind == cstObject {
			return value
		}
		return nil
	}
	n.appendProp(name, cstInput{kind: inputObject})
	return n.objectValue(name)
}

// rootValue is the root's value.
func (n *cstNode) rootValue() *cstNode {
	for _, child := range n.children {
		if !child.isTrivia() {
			return child
		}
	}
	return nil
}

func (n *cstNode) indentText() (string, bool) {
	last, found := "", false
	for _, previous := range n.previousSiblings() {
		switch previous.kind {
		case cstNewline:
			return last, found
		case cstWhitespace:
			last, found = previous.text+last, true
		case cstComment:
			last, found = "", false
		default:
			return "", false
		}
	}
	return last, found
}

func (n *cstNode) trailingComma() *cstNode {
	for _, next := range n.nextSiblings() {
		switch {
		case next.isTrivia():
		case next.kind == cstToken && next.text == ",":
			return next
		default:
			return nil
		}
	}
	return nil
}

// trailingCommentsSameLine lists the comments after n on its line.
func (n *cstNode) trailingCommentsSameLine() []*cstNode {
	next := n.nextSiblings()
	for _, sibling := range next {
		if sibling.isNewline() {
			break
		}
		if !sibling.isComment() && !sibling.isWhitespace() {
			return nil
		}
	}
	var comments []*cstNode
	for _, sibling := range next {
		if !sibling.isWhitespace() && !sibling.isComment() {
			break
		}
		if sibling.isComment() {
			comments = append(comments, sibling)
		}
	}
	return comments
}

func (n *cstNode) newlineKind() newlineKind {
	queue := []*cstNode{n}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for _, child := range node.children {
			if child.isContainer() {
				queue = append(queue, child)
			} else if child.isNewline() {
				return newlineKind(child.text)
			}
		}
	}
	return lineFeed
}

func usesTrailingCommas(node *cstNode) bool {
	if !node.isContainer() {
		return false
	}
	queue := []*cstNode{node}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		if node.kind == cstObject || node.kind == cstArray {
			hasWhitespace := false
			for _, child := range node.children {
				if child.isWhitespace() {
					hasWhitespace = true
					break
				}
			}
			if hasWhitespace {
				var items []*cstNode
				if node.kind == cstObject {
					items = node.properties()
				} else {
					items = node.elements()
				}
				if len(items) > 0 {
					return items[len(items)-1].trailingComma() != nil
				}
			}
		}
		for _, child := range node.children {
			if child.isContainer() {
				queue = append(queue, child)
			}
		}
	}
	return false
}

type indents struct {
	current, single string
}

func (i indents) indent() indents {
	return indents{current: i.current + i.single, single: i.single}
}

func computeIndents(node *cstNode) indents {
	level := 0
	stored, hasStored := node.indentText()
	var ancestors []*cstNode
	for p := node.parent; p != nil; p = p.parent {
		ancestors = append(ancestors, p)
	}
	i := 0
	for i < len(ancestors) && ancestors[i].kind == cstProp {
		i++
	}
	for ; i < len(ancestors); i++ {
		ancestor := ancestors[i]
		if ancestor.kind == cstRoot {
			break
		}
		if i+1 < len(ancestors) && ancestors[i+1].kind == cstProp {
			continue
		}
		level++
		if text, ok := ancestor.indentText(); ok {
			if hasStored {
				if single, ok := strings.CutPrefix(stored, text); ok {
					return indents{current: stored + strings.Repeat(single, level-1), single: single}
				}
				hasStored = false
			} else {
				stored, hasStored = text, true
			}
		} else {
			hasStored = false
		}
	}
	if level == 1 {
		if text, ok := node.indentText(); ok {
			return indents{current: text, single: text}
		}
	}
	if root := node.rootOrSelf(); root != nil {
		if value := root.rootValue(); value != nil {
			for _, child := range value.children {
				if single, ok := child.indentText(); ok {
					return indents{current: strings.Repeat(single, level), single: single}
				}
			}
		}
	}
	return indents{current: strings.Repeat("  ", level), single: "  "}
}

// rootOrSelf is the root above n; n itself has none above it when it is
// the root.
func (n *cstNode) rootOrSelf() *cstNode {
	if root := n.rootNode(); root != nil {
		return root
	}
	return nil
}

func (n *cstNode) rootNewlineKind() newlineKind {
	if root := n.rootNode(); root != nil {
		return root.newlineKind()
	}
	return lineFeed
}

type styleInfo struct {
	trailingCommas bool
	newline        newlineKind
}

// cstInputKind is the kind of a value written into the tree.
type cstInputKind uint8

const (
	inputNull cstInputKind = iota
	inputBool
	inputNumber
	inputString
	inputArray
	inputObject
)

type cstInput struct {
	kind    cstInputKind
	boolean bool
	text    string
	items   []cstInput
	props   []cstInputProp
}

type cstInputProp struct {
	name  string
	value cstInput
}

func (v cstInput) forceMultiline() bool {
	switch v.kind {
	case inputArray:
		for _, item := range v.items {
			if item.kind == inputArray && len(item.items) > 0 || item.kind == inputObject && len(item.props) > 0 {
				return true
			}
		}
	case inputObject:
		return len(v.props) > 0
	}
	return false
}

// insertValue writes value, or the property name: value when isProp, at
// *at in n.
func (n *cstNode) insertValue(at *int, isProp bool, name string, value cstInput, style styleInfo, ind indents) {
	if isProp {
		prop := &cstNode{kind: cstProp}
		n.insert(at, prop)
		prop.insert(nil, escapedString(name), token(":"), whitespace(" "))
		prop.insertValue(nil, false, "", value, style, ind)
		return
	}
	multiline := value.forceMultiline()
	switch value.kind {
	case inputNull:
		n.insert(at, leaf(cstNull, "null"))
	case inputBool:
		text := "false"
		if value.boolean {
			text = "true"
		}
		n.insert(at, leaf(cstBool, text))
	case inputNumber:
		n.insert(at, leaf(cstNumber, value.text))
	case inputString:
		n.insert(at, escapedString(value.text))
	case inputArray:
		array := &cstNode{kind: cstArray}
		n.insert(at, array)
		array.insert(nil, token("["))
		if len(value.items) > 0 {
			inner := ind.indent()
			for i, item := range value.items {
				if multiline {
					array.insert(nil, newline(style.newline), whitespace(inner.current))
				}
				array.insertValue(nil, false, "", item, style, inner)
				if style.trailingCommas && multiline || i+1 < len(value.items) {
					if multiline {
						array.insert(nil, token(","))
					} else {
						array.insert(nil, token(","), whitespace(" "))
					}
				}
			}
		}
		if multiline {
			array.insert(nil, newline(style.newline), whitespace(ind.current))
		}
		array.insert(nil, token("]"))
	case inputObject:
		object := &cstNode{kind: cstObject}
		n.insert(at, object)
		object.insert(nil, token("{"))
		if len(value.props) > 0 {
			inner := ind.indent()
			for i, prop := range value.props {
				object.insert(nil, newline(style.newline), whitespace(inner.current))
				object.insertValue(nil, true, prop.name, prop.value, style, inner)
				if style.trailingCommas || i+1 < len(value.props) {
					object.insert(nil, token(","))
				}
			}
			object.insert(nil, newline(style.newline), whitespace(ind.current))
		}
		object.insert(nil, token("}"))
	}
}

// setRootValue replaces the root's value.
func (n *cstNode) setRootValue(value cstInput) {
	style := styleInfo{newline: n.newlineKind(), trailingCommas: usesTrailingCommas(n)}
	ind := computeIndents(n)
	var index int
	if current := n.rootValue(); current != nil {
		index = current.childIndex()
		current.removeRaw()
	} else {
		children := n.children
		index = len(children)
		if index > 0 && children[index-1].isNewline() {
			index--
		}
		if index > 0 && children[index-1].isComment() {
			n.insert(&index, newline(style.newline))
		}
		if n.childAt(index) == nil {
			n.insert(&index, newline(style.newline))
			index--
		}
	}
	n.insertValue(&index, false, "", value, style, ind)
}

// rootObjectOrSet is the root object, made when the root holds none.
func (n *cstNode) rootObjectOrSet() *cstNode {
	if value := n.rootValue(); value != nil && value.kind == cstObject {
		return value
	}
	n.setRootValue(cstInput{kind: inputObject})
	return n.rootValue()
}

// setPropValue replaces a property's value.
func (n *cstNode) setPropValue(value cstInput) {
	current := n.propValue()
	index := len(n.children)
	trailing := n
	if current != nil {
		index = current.childIndex()
		trailing = current
	}
	ind := computeIndents(n)
	style := styleInfo{newline: n.rootNewlineKind(), trailingCommas: usesTrailingCommas(trailing)}
	if index < len(n.children) {
		n.children[index].removeRaw()
	}
	n.insertValue(&index, false, "", value, style, ind)
}

func (n *cstNode) appendProp(name string, value cstInput) *cstNode {
	ensureMultiline(n)
	return insertOrAppend(n, n.properties(), -1, true, name, value)
}

func (n *cstNode) insertElement(index int, value cstInput) *cstNode {
	return insertOrAppend(n, n.elements(), index, false, "", value)
}

func (n *cstNode) appendElement(value cstInput) *cstNode {
	return insertOrAppend(n, n.elements(), -1, false, "", value)
}

func hasSeparatingNewline(siblings []*cstNode) bool {
	for _, sibling := range siblings {
		if sibling.isNewline() {
			return true
		}
		if !sibling.isTrivia() {
			break
		}
	}
	return false
}

func insertOrAppend(container *cstNode, elements []*cstNode, index int, isProp bool, name string, value cstInput) *cstNode {
	trimInnerBlankLines(container)
	children := container.children
	if index < 0 || index > len(elements) {
		index = len(elements)
	}
	var next, previous *cstNode
	if index < len(elements) {
		next = elements[index]
	}
	if index > 0 {
		previous = elements[index-1]
	}
	style := styleInfo{newline: container.rootNewlineKind(), trailingCommas: usesTrailingCommas(container)}
	ind := computeIndents(container)
	childIndents := ind.indent()
	if len(elements) > 0 {
		childIndents = computeIndents(elements[0])
	}
	hasNewline := false
	for _, child := range children {
		if child.isNewline() {
			hasNewline = true
			break
		}
	}
	force := hasNewline || isProp || value.forceMultiline()
	var at int
	var inserted *cstNode
	if previous != nil {
		if previous.trailingComma() == nil {
			commaAt := previous.childIndex() + 1
			container.insert(&commaAt, token(","))
		}
		comma := previous.trailingComma()
		at = comma.childIndex() + 1
		if comments := comma.trailingCommentsSameLine(); len(comments) > 0 {
			at = comments[len(comments)-1].childIndex() + 1
		}
		if force {
			container.insert(&at, newline(style.newline), whitespace(childIndents.current))
		} else {
			container.insert(&at, whitespace(" "))
		}
		container.insertValue(&at, isProp, name, value, style, childIndents)
		inserted = container.children[at-1]
	} else {
		if len(elements) == 0 {
			// The first of the blank run before the closing token.
			at = children[len(children)-1].childIndex()
			for i := len(children) - 2; i >= 0; i-- {
				if !children[i].isWhitespace() && !children[i].isNewline() {
					break
				}
				at = children[i].childIndex()
			}
		} else {
			at = children[0].childIndex() + 1
		}
		if force {
			container.insert(&at, newline(style.newline), whitespace(childIndents.current))
			container.insertValue(&at, isProp, name, value, style, childIndents)
			inserted = container.children[at-1]
			if next == nil && !hasSeparatingNewline(inserted.nextSiblings()) {
				container.insert(&at, newline(style.newline), whitespace(ind.current))
			}
		} else {
			container.insertValue(&at, isProp, name, value, style, childIndents)
			inserted = container.children[at-1]
		}
	}
	if next != nil {
		container.insert(&at, token(","))
		if force {
			comma := container.children[at-1]
			if !hasSeparatingNewline(comma.nextSiblings()) {
				container.insert(&at, newline(style.newline), whitespace(ind.current))
			}
		} else {
			container.insert(&at, whitespace(" "))
		}
	} else if style.trailingCommas && force {
		container.insert(&at, token(","))
	}
	return inserted
}

func trimInnerBlankLines(node *cstNode) {
	children := node.children
	if len(children) < 2 {
		return
	}
	inner := append([]*cstNode(nil), children[1:len(children)-1]...)
	// removeAfterFirst drops the blank lines after the first line break
	// of the run next yields, and reports how many nodes it consumed.
	removeAfterFirst := func(next func() *cstNode) {
		for {
			child := next()
			if child == nil {
				return
			}
			if child.isWhitespace() {
				continue
			}
			if child.isNewline() {
				break
			}
			return
		}
		var pending []*cstNode
		for {
			child := next()
			if child == nil {
				return
			}
			switch {
			case child.isWhitespace():
				pending = append(pending, child)
			case child.isNewline():
				child.removeRaw()
				for _, p := range pending {
					p.removeRaw()
				}
				pending = nil
			default:
				return
			}
		}
	}
	front, back := 0, len(inner)
	removeAfterFirst(func() *cstNode {
		if front >= back {
			return nil
		}
		front++
		return inner[front-1]
	})
	removeAfterFirst(func() *cstNode {
		if back <= front {
			return nil
		}
		back--
		return inner[back]
	})
}

func ensureMultiline(container *cstNode) {
	children := append([]*cstNode(nil), container.children...)
	for _, child := range children {
		if child.isNewline() {
			return
		}
	}
	ind := computeIndents(container)
	childIndents := ind.indent()
	kind := container.rootNewlineKind()
	inner := children[1 : len(children)-1]
	index := 1
	for i := 0; i < len(inner); i++ {
		child := inner[i]
		if child.isWhitespace() {
			child.removeRaw()
			continue
		}
		container.insert(&index, newline(kind), whitespace(childIndents.current))
		index++
		var trailing []*cstNode
		for i+1 < len(inner) {
			i++
			next := inner[i]
			if next.isWhitespace() {
				trailing = append(trailing, next)
				continue
			}
			index += 1 + len(trailing)
			trailing = nil
			if next.kind == cstToken && next.text == "," {
				break
			}
		}
		for _, t := range trailing {
			t.removeRaw()
		}
	}
	container.insert(&index, newline(kind))
	if ind.current != "" {
		container.insert(&index, whitespace(ind.current))
	}
}

// remove takes node out of the tree with what separates it from its
// siblings.
func (n *cstNode) remove() {
	switch {
	case n.kind == cstRoot:
		for _, child := range n.children {
			child.parent = nil
		}
		n.children = nil
	case n.kind == cstComment:
		if n.isLineComment() {
			for _, previous := range n.previousSiblings() {
				if previous.isWhitespace() {
					previous.removeRaw()
					continue
				}
				if previous.isNewline() {
					previous.removeRaw()
				}
				break
			}
		}
		n.removeRaw()
	case n.kind == cstToken || n.kind == cstWhitespace || n.kind == cstNewline:
		n.removeRaw()
	default:
		removeCommaSeparated(n)
	}
}

func removeCommaSeparated(node *cstNode) {
	nextNodeSameLine := func(comma *cstNode) bool {
		for _, sibling := range comma.nextSiblings() {
			switch {
			case sibling.isWhitespace(), sibling.isComment():
			case sibling.isNewline():
				return false
			default:
				return true
			}
		}
		return true
	}
	parent := node.parent
	comma := node.trailingComma()
	inList := parent != nil && (parent.kind == cstArray || parent.kind == cstObject)
	removeUpToNextLine := comma == nil || !nextNodeSameLine(comma)

	for _, previous := range node.previousSiblings() {
		if previous.isTrivia() && !previous.isNewline() {
			previous.removeRaw()
		} else {
			break
		}
	}
	foundNewline := false
	if comma != nil {
		for _, next := range node.nextSiblings() {
			if next.isNewline() {
				foundNewline = true
			}
			next.removeRaw()
			if next.isComma() {
				break
			}
		}
	} else if inList {
		for _, previous := range node.previousSiblings() {
			if previous.isComma() {
				previous.remove()
				break
			}
		}
	}
	if removeUpToNextLine && !foundNewline {
		next := node.nextSiblings()
		for i, sibling := range next {
			if !sibling.isTrivia() {
				break
			}
			if sibling.isNewline() {
				sibling.removeRaw()
				break
			}
			if sibling.isWhitespace() && i+1 < len(next) {
				peek := next[i+1]
				if !peek.isWhitespace() && !peek.isNewline() && !peek.isComment() {
					break
				}
			}
			sibling.removeRaw()
		}
	}
	node.removeRaw()
	if parent == nil {
		return
	}
	switch parent.kind {
	case cstRoot:
		for _, child := range parent.children {
			if !child.isWhitespace() && !child.isNewline() {
				return
			}
		}
		parent.remove()
	case cstObject, cstArray:
		children := append([]*cstNode(nil), parent.children...)
		for _, child := range children[1 : len(children)-1] {
			if !child.isWhitespace() && !child.isNewline() {
				return
			}
		}
		for _, child := range children {
			if child.isWhitespace() || child.isNewline() {
				child.removeRaw()
			}
		}
	}
}

func (n *cstNode) insertProp(index int, name string, value cstInput) *cstNode {
	ensureMultiline(n)
	return insertOrAppend(n, n.properties(), index, true, name, value)
}
