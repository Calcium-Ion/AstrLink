package format

// jsonParser reads JSON with the syntax it is given, building the CST when
// build is set. Comments and whitespace go to the container that takes the
// next token, as the crate's CstBuilder places them.
type jsonParser struct {
	scanner jsonScanner
	build   bool
	// tok is the current token; has is false at the end of the text.
	tok     jsonToken
	has     bool
	lastEnd int
	// pending are the comment tokens scanned since lastFlushed.
	pending     []jsonToken
	lastFlushed int
	depth       int
}

// parseJSONText parses text, building its CST when build is set.
func parseJSONText(text string, opts JSONSyntax, build bool) (*cstNode, *jsonParseError) {
	p := &jsonParser{scanner: jsonScanner{text: text, opts: opts}, build: build}
	root := &cstNode{kind: cstRoot}
	if err := p.next(); err != nil {
		return nil, err
	}
	if p.has {
		if err := p.parseValue(root); err != nil {
			return nil, err
		}
		if err := p.next(); err != nil {
			return nil, err
		}
		if p.has {
			return nil, p.scanner.tokenError("Text cannot contain more than one JSON value")
		}
	}
	p.flush(root, len(text))
	return root, nil
}

func asParseError(err error) *jsonParseError {
	if err == nil {
		return nil
	}
	return err.(*jsonParseError)
}

// next scans the next token that is not a comment.
func (p *jsonParser) next() *jsonParseError {
	for {
		tok, ok, err := p.scanner.scan()
		if err != nil {
			return asParseError(err)
		}
		if ok && tok.isComment() {
			if !p.scanner.opts.Comments {
				return p.scanner.tokenError("Comments are not allowed")
			}
			p.pending = append(p.pending, tok)
			continue
		}
		p.tok, p.has = tok, ok
		return nil
	}
}

// flush adds the whitespace and comments before offset to container.
func (p *jsonParser) flush(container *cstNode, offset int) {
	if !p.build {
		return
	}
	text := p.scanner.text
	at := p.lastFlushed
	for _, comment := range p.pending {
		addWhitespace(container, text[at:comment.start])
		container.insert(nil, leaf(cstComment, text[comment.start:comment.end]))
		at = comment.end
	}
	p.pending = p.pending[:0]
	if at < offset {
		addWhitespace(container, text[at:offset])
	}
	p.lastFlushed = offset
}

// take adds the current token to container, after what precedes it.
func (p *jsonParser) take(container *cstNode, node *cstNode) {
	if !p.build {
		return
	}
	p.flush(container, p.tok.start)
	container.insert(nil, node)
	p.lastFlushed = p.tok.end
}

func addWhitespace(container *cstNode, text string) {
	last := 0
	add := func(to int) {
		if to > last {
			container.insert(nil, whitespace(text[last:to]))
		}
	}
	for i := 0; i < len(text); i++ {
		switch {
		case text[i] == '\r' && i+1 < len(text) && text[i+1] == '\n':
			add(i)
			container.insert(nil, newline(crlf))
			i++
			last = i + 1
		case text[i] == '\r':
			add(i)
			container.insert(nil, newline(carriageReturn))
			last = i + 1
		case text[i] == '\n':
			add(i)
			container.insert(nil, newline(lineFeed))
			last = i + 1
		}
	}
	add(len(text))
}

func (p *jsonParser) tokenText() string { return p.scanner.text[p.tok.start:p.tok.end] }

func (p *jsonParser) parseValue(container *cstNode) *jsonParseError {
	if p.depth > 512 {
		return p.scanner.tokenError("Maximum nesting depth exceeded")
	}
	switch p.tok.kind {
	case tokOpenBrace:
		return p.parseObject(container)
	case tokOpenBracket:
		return p.parseArray(container)
	case tokString:
		p.take(container, leaf(cstString, p.tokenText()))
	case tokBool:
		p.take(container, leaf(cstBool, p.tokenText()))
	case tokNumber:
		p.take(container, leaf(cstNumber, p.tokenText()))
	case tokNull:
		p.take(container, leaf(cstNull, "null"))
	case tokCloseBracket:
		return p.scanner.tokenError("Unexpected close bracket")
	case tokCloseBrace:
		return p.scanner.tokenError("Unexpected close brace")
	case tokComma:
		return p.scanner.tokenError("Unexpected comma")
	case tokColon:
		return p.scanner.tokenError("Unexpected colon")
	default:
		return p.scanner.tokenError("Unexpected word")
	}
	return nil
}

func isLooseName(tok jsonToken) bool {
	switch tok.kind {
	case tokWord, tokNumber, tokBool, tokNull:
		return true
	}
	return false
}

func (p *jsonParser) parseObject(container *cstNode) *jsonParseError {
	object := &cstNode{kind: cstObject}
	p.take(container, object)
	object.insert(nil, token("{"))
	p.depth++
	defer func() { p.depth-- }()
	if err := p.next(); err != nil {
		return err
	}
	for {
		if !p.has {
			return p.scanner.tokenError("Unterminated object")
		}
		if p.tok.kind == tokCloseBrace {
			break
		}
		var name *cstNode
		switch {
		case p.tok.kind == tokString:
			name = leaf(cstString, p.tokenText())
		case isLooseName(p.tok):
			if !p.scanner.opts.LoosePropertyNames {
				return p.scanner.tokenError("Expected string for object property")
			}
			name = leaf(cstWord, p.tokenText())
		default:
			return p.scanner.tokenError("Unexpected token in object")
		}
		prop := &cstNode{kind: cstProp}
		if p.build {
			p.flush(object, p.tok.start)
			object.insert(nil, prop)
		}
		p.take(prop, name)
		if err := p.next(); err != nil {
			return err
		}
		if !p.has || p.tok.kind != tokColon {
			return p.scanner.tokenError("Expected colon after the string or word in object property")
		}
		p.take(prop, token(":"))
		if err := p.next(); err != nil {
			return err
		}
		if !p.has {
			return p.scanner.tokenError("Expected value after colon in object property")
		}
		if err := p.parseValue(prop); err != nil {
			return err
		}
		afterValue := p.tok.end
		if err := p.next(); err != nil {
			return err
		}
		switch {
		case p.has && p.tok.kind == tokComma:
			commaAt := p.tok.start
			p.take(object, token(","))
			if err := p.next(); err != nil {
				return err
			}
			if p.has && p.tok.kind == tokCloseBrace && !p.scanner.opts.TrailingCommas {
				return p.scanner.errorAt(commaAt, "Trailing commas are not allowed")
			}
		case p.has && (p.tok.kind == tokString || isLooseName(p.tok)) && !p.scanner.opts.MissingCommas:
			return p.scanner.errorAt(afterValue, "Expected comma")
		}
	}
	p.take(object, token("}"))
	return nil
}

func (p *jsonParser) parseArray(container *cstNode) *jsonParseError {
	array := &cstNode{kind: cstArray}
	p.take(container, array)
	array.insert(nil, token("["))
	p.depth++
	defer func() { p.depth-- }()
	if err := p.next(); err != nil {
		return err
	}
	for {
		if !p.has {
			return p.scanner.tokenError("Unterminated array")
		}
		if p.tok.kind == tokCloseBracket {
			break
		}
		if err := p.parseValue(array); err != nil {
			return err
		}
		afterValue := p.tok.end
		if err := p.next(); err != nil {
			return err
		}
		switch {
		case p.has && p.tok.kind == tokComma:
			commaAt := p.tok.start
			p.take(array, token(","))
			if err := p.next(); err != nil {
				return err
			}
			if p.has && p.tok.kind == tokCloseBracket && !p.scanner.opts.TrailingCommas {
				return p.scanner.errorAt(commaAt, "Trailing commas are not allowed")
			}
		case p.has && p.tok.isValueStart() && !p.scanner.opts.MissingCommas:
			return p.scanner.errorAt(afterValue, "Expected comma")
		}
	}
	p.take(array, token("]"))
	return nil
}
