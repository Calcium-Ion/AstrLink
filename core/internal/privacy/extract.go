package privacy

import (
	"sort"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// position describes what the JSON value at one place in a request holds.
// Extraction is a whitelist: it walks the typed protocol structure and reads
// only the strings a position marks as content. Everything a position does not
// name (ids, tool and server names, call ids, signatures, encrypted content,
// type tags, enums, status, unknown fields and unknown block or item types) is
// never extracted, so a detector cannot mistake it for a secret and no
// placeholder can break the request.
//
// A position reads a value by its JSON type: a string only when text is set,
// the fields of an object only through fields, the elements of an array only
// through items. The zero position reads nothing.
type position struct {
	// text marks a string here as content.
	text bool
	// fields gives the position of each field of an object here.
	fields func(record map[string]any, key string) position
	// items gives the position of each element of an array here.
	items func(item any) position
	// toolOutput marks a value produced by a tool, or by the model for a tool.
	// Its strings that hold JSON are revalidated after a rewrite, and the
	// findings in them are aligned to the JSON literals.
	toolOutput bool
	// fieldContext gives the strings nested in an object or array here the
	// tool-field-path-v1 prefix the local model reads them with.
	fieldContext bool
}

// requestRoot is a top-level request field and what it holds.
type requestRoot struct {
	key string
	at  position
}

// textPosition is a string of content.
var textPosition = position{text: true}

func (at position) reads() bool {
	return at.text || at.fields != nil || at.items != nil
}

// object reads the fields of an object.
func object(fields func(record map[string]any, key string) position) position {
	return position{fields: fields}
}

// listOf reads every element of an array at the same position.
func listOf(element position) position {
	return position{items: func(any) position { return element }}
}

// textOrList reads a string, or an array whose elements are at element.
func textOrList(element position) position {
	return position{text: true, items: func(any) position { return element }}
}

// field reads one field of an object.
func field(name string, at position) position {
	return object(func(_ map[string]any, key string) position {
		if key == name {
			return at
		}
		return position{}
	})
}

// named reads the listed fields of an object as text.
func named(keys ...string) position {
	return object(func(_ map[string]any, key string) position {
		for _, name := range keys {
			if key == name {
				return textPosition
			}
		}
		return position{}
	})
}

// anyFieldText reads every field of an object that holds a string, such as
// caller-named variables or environment values.
func anyFieldText(map[string]any, string) position { return textPosition }

// payload is arbitrary JSON a tool or the model wrote for a tool, or values a
// caller chose. Every string inside it is content whatever its key: a payload
// field named tool_name, id or signature belongs to the tool, not to the
// protocol. Only non-text media, recognised by shape, is left out.
func payload() position {
	return position{text: true, fields: payloadField, items: payloadItem}
}

func payloadItem(any) position { return payload() }

func payloadField(record map[string]any, key string) position {
	typeName, _ := record["type"].(string)
	if isNonTextMediaChild(record, normalizeJSONKey(key), record[key], normalizeJSONKey(typeName)) {
		return position{}
	}
	return payload()
}

// toolPayload is call arguments or a tool's free-form output. fieldContext is
// set only at the typed positions the tool-field-path-v1 contract covers.
func toolPayload(fieldContext bool) position {
	at := payload()
	at.toolOutput = true
	at.fieldContext = fieldContext
	return at
}

// toolOutput is a tool result: a string, typed blocks read by block, or other
// JSON read as payload. Untyped array elements are payload; typed blocks
// follow the protocol's whitelist, so a type it does not know is never read.
func toolOutput(fieldContext bool, block func(record map[string]any, key string) position) position {
	return position{
		text:         true,
		fields:       payloadField,
		items:        typedOrPayload(block),
		toolOutput:   true,
		fieldContext: fieldContext,
	}
}

// toolResultValue marks a structured server tool result as tool output.
func toolResultValue(at position) position {
	at.toolOutput = true
	return at
}

func typedOrPayload(block func(record map[string]any, key string) position) func(any) position {
	return func(item any) position {
		if record, ok := item.(map[string]any); ok {
			if _, typed := record["type"].(string); typed {
				return object(block)
			}
		}
		return payload()
	}
}

func signed(record map[string]any) bool {
	signature, _ := record["signature"].(string)
	return signature != ""
}

func normalizeJSONKey(key string) string {
	return strings.ToLower(strings.ReplaceAll(key, "_", ""))
}

// requestRoots lists the top-level fields inspected for a protocol and what
// they hold.
//
// Tool declarations are left out unless the policy opts in. A tools array
// carries schemas and author-written descriptions belonging to the agent
// harness, not text the operator typed, yet it is dense with documentation links
// and sample addresses that the detectors match. Redacting it inflated
// placeholder counts by an order of magnitude while protecting nothing. Real
// user data travelling through tools lives in call arguments and results, which
// stay covered under the conversation roots.
func requestRoots(protocol contract.ProtocolID, options InspectionOptions) ([]requestRoot, bool) {
	var roots, declarations []requestRoot
	switch protocol {
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		input := responsesInput(options)
		roots = []requestRoot{
			{"instructions", input},
			{"input", input},
			{"prompt", object(responsesPromptField)},
		}
		declarations = []requestRoot{{"tools", listOf(object(responsesToolField))}}
	case contract.ProtocolOpenAIChat:
		roots = []requestRoot{
			{"messages", listOf(object(chatMessageField))},
			// Predicted Outputs: text the caller expects the reply to repeat,
			// usually the file being edited.
			{"prediction", field("content", textOrList(object(chatPartField(false))))},
		}
		declarations = []requestRoot{
			{"tools", listOf(object(chatToolField))},
			{"functions", listOf(object(functionDeclarationField))},
		}
	case contract.ProtocolOpenAICompletions:
		// Token arrays hold numbers and are never read.
		roots = []requestRoot{{"prompt", textOrList(textPosition)}, {"suffix", textPosition}}
	case contract.ProtocolAnthropicMessages:
		roots = []requestRoot{
			{"system", anthropicContent("system")},
			{"messages", listOf(object(anthropicMessageField))},
		}
		declarations = []requestRoot{{"tools", listOf(object(anthropicToolField))}}
	case contract.ProtocolGoogleGenerateContent:
		// REST uses camelCase; the protobuf JSON mapping also accepts the
		// snake_case field names. Some clients send the system instruction as
		// a bare string.
		system := position{text: true, fields: geminiContentField}
		roots = []requestRoot{
			{"systemInstruction", system},
			{"system_instruction", system},
			{"contents", listOf(object(geminiContentField))},
		}
		declarations = []requestRoot{{"tools", listOf(object(geminiToolField))}}
	case contract.ProtocolOpenAIEmbeddings:
		roots = []requestRoot{{"input", textOrList(textPosition)}}
	case contract.ProtocolGoogleEmbeddings:
		// embedContent carries one request at the root, batchEmbedContents a
		// list of them.
		roots = []requestRoot{
			{"title", textPosition},
			{"content", object(geminiContentField)},
			{"requests", listOf(object(googleEmbeddingRequestField))},
		}
	case contract.ProtocolRerank:
		// A document is a string or a caller-shaped object.
		roots = []requestRoot{{"query", textPosition}, {"documents", listOf(payload())}}
	case contract.ProtocolOpenAIImages:
		// Reference images travel as data URLs, which are never inspected.
		roots = []requestRoot{{"prompt", textPosition}}
	case contract.ProtocolOpenAISearch:
		// The web.run commands are the model's arguments for the search tool.
		// The recent conversation Codex attaches reaches a Codex subscription
		// with the queries; the built-in search drops it first.
		roots = []requestRoot{{"commands", payload()}, {"input", responsesInput(options)}}
	default:
		return nil, false
	}
	if options.InspectToolDeclarations {
		roots = append(roots, declarations...)
	}
	return roots, true
}

func googleEmbeddingRequestField(_ map[string]any, key string) position {
	switch key {
	case "title":
		return textPosition
	case "content":
		return object(geminiContentField)
	}
	return position{}
}

// extraction collects the segments of one request in document order: roots
// in the order requestRoots lists them, object fields by sorted key, array
// elements by index.
type extraction struct {
	segments []extractedSegment
}

// scope is what a value inherits from the positions above it.
type scope struct {
	toolOutput   bool
	fieldContext bool
	// leaf is set by the containing object or array when its strings carry
	// field context.
	leaf bool
}

func (extraction *extraction) walk(value any, path string, depth int, at position, within scope) {
	if depth > maxJSONDepth {
		return
	}
	within.toolOutput = within.toolOutput || at.toolOutput
	within.fieldContext = within.fieldContext || at.fieldContext
	switch typed := value.(type) {
	case string:
		if !at.text {
			return
		}
		segment := Segment{Path: path, Value: typed}
		if within.leaf {
			segment.ContextPrefix = toolFieldContextPrefix(path)
		}
		extraction.segments = append(extraction.segments, extractedSegment{
			Segment: segment,
			// Only re-validate after rewrite when the original string was
			// already JSON. Tool transcripts often start with '{' (a truncated
			// package.json, a shell dump plus stderr) without being JSON;
			// treating those as structured payloads turns ActionRedact into an
			// unconfigurable block.
			validateStructuredJSON: within.toolOutput && validStructuredJSON(typed),
		})
	case []any:
		if at.items == nil {
			return
		}
		child := within
		child.leaf = within.fieldContext
		for index, item := range typed {
			extraction.walk(item, path+"/"+jsonIndex(index), depth+1, at.items(item), child)
		}
	case map[string]any:
		if at.fields == nil {
			return
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			next := at.fields(typed, key)
			if !next.reads() {
				continue
			}
			child := within
			child.leaf = within.fieldContext && !isToolTextBlockField(typed, key)
			extraction.walk(typed[key], path+"/"+escapeJSONPointer(key), depth+1, next, child)
		}
	}
}

// schema reads the author-written text of a JSON schema: descriptions,
// titles, comments, defaults and examples, through every subschema. Property
// names, types, enums, constants, formats, patterns, references and the
// dialect URI are never read.
func schema() position {
	return position{fields: schemaField}
}

func schemaField(_ map[string]any, key string) position {
	switch key {
	case "description", "title", "$comment":
		return textPosition
	case "default", "examples", "example":
		return payload()
	case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
		return object(func(map[string]any, string) position { return schema() })
	case "items":
		// A schema, or the tuple form of older drafts.
		return position{fields: schemaField, items: func(any) position { return schema() }}
	case "prefixItems", "anyOf", "oneOf", "allOf":
		return listOf(schema())
	case "additionalProperties", "additionalItems", "unevaluatedProperties", "unevaluatedItems",
		"not", "if", "then", "else", "contains", "propertyNames":
		return schema()
	}
	return position{}
}

// functionDeclarationField reads a function declaration: its description and
// parameter schema. Chat's legacy functions list and the function inside a
// Chat tool share this shape.
func functionDeclarationField(_ map[string]any, key string) position {
	switch key {
	case "description":
		return textPosition
	case "parameters":
		return schema()
	}
	return position{}
}
