// Package jsonpatch applies RFC 6902 JSON Patch documents to a JSON body.
// Objects keep their member order and numbers their spelling, so a patch
// changes only what its operations name.
package jsonpatch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Operation is one operation of a patch.
type Operation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	From  string          `json:"from,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

// ErrTestFailed is a test operation whose value did not match.
var ErrTestFailed = errors.New("a test operation failed")

// Document is a decoded JSON value: nil, bool, json.Number, string, []any or
// *Object.
type Document struct{ root any }

// Object is a JSON object in member order.
type Object struct {
	keys   []string
	values map[string]any
}

// Parse decodes a JSON body.
func Parse(data []byte) (*Document, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	root, err := decodeValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("the body holds more than one JSON value")
	}
	return &Document{root: root}, nil
}

func decodeValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch delim := token.(type) {
	case json.Delim:
		switch delim {
		case '{':
			object := &Object{values: map[string]any{}}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, _ := keyToken.(string)
				value, err := decodeValue(decoder)
				if err != nil {
					return nil, err
				}
				object.set(key, value)
			}
			_, err := decoder.Token()
			return object, err
		case '[':
			items := []any{}
			for decoder.More() {
				value, err := decodeValue(decoder)
				if err != nil {
					return nil, err
				}
				items = append(items, value)
			}
			_, err := decoder.Token()
			return items, err
		}
		return nil, fmt.Errorf("unexpected %v", delim)
	default:
		return token, nil
	}
}

func (object *Object) set(key string, value any) {
	if _, ok := object.values[key]; !ok {
		object.keys = append(object.keys, key)
	}
	object.values[key] = value
}

func (object *Object) remove(key string) {
	delete(object.values, key)
	for index, name := range object.keys {
		if name == key {
			object.keys = append(object.keys[:index], object.keys[index+1:]...)
			return
		}
	}
}

// MarshalJSON writes the object in member order.
func (object *Object) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, key := range object.keys {
		if index > 0 {
			buffer.WriteByte(',')
		}
		name, err := encode(key)
		if err != nil {
			return nil, err
		}
		buffer.Write(name)
		buffer.WriteByte(':')
		value, err := encode(object.values[key])
		if err != nil {
			return nil, err
		}
		buffer.Write(value)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

func encode(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// Bytes encodes the document.
func (document *Document) Bytes() ([]byte, error) { return encode(document.root) }

// Get returns the encoded value at a JSON Pointer, and whether it exists.
func (document *Document) Get(pointer string) ([]byte, bool) {
	value, err := document.find(pointer)
	if err != nil {
		return nil, false
	}
	encoded, err := encode(value)
	return encoded, err == nil
}

// Apply applies the operations in order; on an error the document is left
// partly patched, so callers apply a patch to a copy they can drop.
func (document *Document) Apply(operations []Operation) error {
	for index, operation := range operations {
		if err := document.apply(operation); err != nil {
			return fmt.Errorf("operation %d (%s %s): %w", index, operation.Op, operation.Path, err)
		}
	}
	return nil
}

func (document *Document) apply(operation Operation) error {
	switch operation.Op {
	case "add", "replace", "test":
		if len(operation.Value) == 0 {
			return errors.New("value is required")
		}
		value, err := Parse(operation.Value)
		if err != nil {
			return errors.New("value is not JSON")
		}
		switch operation.Op {
		case "add":
			return document.add(operation.Path, value.root)
		case "replace":
			return document.replace(operation.Path, value.root)
		default:
			current, err := document.find(operation.Path)
			if err != nil {
				return err
			}
			if !equal(current, value.root) {
				return ErrTestFailed
			}
			return nil
		}
	case "remove":
		_, err := document.removeAt(operation.Path)
		return err
	case "move", "copy":
		value, err := document.find(operation.From)
		if err != nil {
			return fmt.Errorf("from: %w", err)
		}
		if operation.Op == "move" {
			if operation.Path == operation.From {
				return nil
			}
			if strings.HasPrefix(operation.Path, operation.From+"/") {
				return errors.New("a value cannot move into itself")
			}
			if _, err := document.removeAt(operation.From); err != nil {
				return err
			}
		} else {
			value = clone(value)
		}
		return document.add(operation.Path, value)
	}
	return fmt.Errorf("op %q is not add, remove, replace, move, copy or test", operation.Op)
}

// tokens splits a JSON Pointer.
func tokens(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("path %q is not a JSON Pointer", pointer)
	}
	parts := strings.Split(pointer[1:], "/")
	for index, part := range parts {
		parts[index] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
	}
	return parts, nil
}

func (document *Document) find(pointer string) (any, error) {
	parts, err := tokens(pointer)
	if err != nil {
		return nil, err
	}
	current := document.root
	for _, part := range parts {
		switch container := current.(type) {
		case *Object:
			value, ok := container.values[part]
			if !ok {
				return nil, fmt.Errorf("%s does not exist", pointer)
			}
			current = value
		case []any:
			index, err := arrayIndex(part, len(container), false)
			if err != nil {
				return nil, err
			}
			current = container[index]
		default:
			return nil, fmt.Errorf("%s does not exist", pointer)
		}
	}
	return current, nil
}

// parent finds the container of the pointer's last token.
func (document *Document) parent(pointer string) (any, string, []string, error) {
	parts, err := tokens(pointer)
	if err != nil {
		return nil, "", nil, err
	}
	if len(parts) == 0 {
		return nil, "", nil, nil
	}
	prefix := parts[:len(parts)-1]
	current := document.root
	for _, part := range prefix {
		switch container := current.(type) {
		case *Object:
			value, ok := container.values[part]
			if !ok {
				return nil, "", nil, fmt.Errorf("the parent of %s does not exist", pointer)
			}
			current = value
		case []any:
			index, err := arrayIndex(part, len(container), false)
			if err != nil {
				return nil, "", nil, err
			}
			current = container[index]
		default:
			return nil, "", nil, fmt.Errorf("the parent of %s does not exist", pointer)
		}
	}
	return current, parts[len(parts)-1], prefix, nil
}

func (document *Document) add(pointer string, value any) error {
	container, last, prefix, err := document.parent(pointer)
	if err != nil {
		return err
	}
	if pointer == "" {
		document.root = value
		return nil
	}
	switch target := container.(type) {
	case *Object:
		target.set(last, value)
		return nil
	case []any:
		index, err := arrayIndex(last, len(target), true)
		if err != nil {
			return err
		}
		grown := make([]any, 0, len(target)+1)
		grown = append(grown, target[:index]...)
		grown = append(grown, value)
		grown = append(grown, target[index:]...)
		return document.replaceContainer(prefix, grown)
	}
	return fmt.Errorf("the parent of %s is not an object or array", pointer)
}

// replace stores value in place of the one at pointer, where it stood
// among its object's members.
func (document *Document) replace(pointer string, value any) error {
	if _, err := document.find(pointer); err != nil {
		return err
	}
	container, last, _, err := document.parent(pointer)
	if err != nil {
		return err
	}
	switch target := container.(type) {
	case nil:
		document.root = value
	case *Object:
		target.values[last] = value
	case []any:
		index, err := arrayIndex(last, len(target), false)
		if err != nil {
			return err
		}
		target[index] = value
	}
	return nil
}

func (document *Document) removeAt(pointer string) (any, error) {
	container, last, prefix, err := document.parent(pointer)
	if err != nil {
		return nil, err
	}
	if pointer == "" {
		return nil, errors.New("the whole document cannot be removed")
	}
	switch target := container.(type) {
	case *Object:
		value, ok := target.values[last]
		if !ok {
			return nil, fmt.Errorf("%s does not exist", pointer)
		}
		target.remove(last)
		return value, nil
	case []any:
		index, err := arrayIndex(last, len(target), false)
		if err != nil {
			return nil, err
		}
		value := target[index]
		shrunk := append(append([]any{}, target[:index]...), target[index+1:]...)
		return value, document.replaceContainer(prefix, shrunk)
	}
	return nil, fmt.Errorf("%s does not exist", pointer)
}

// replaceContainer stores a resized array at the path of tokens.
func (document *Document) replaceContainer(prefix []string, value []any) error {
	if len(prefix) == 0 {
		document.root = value
		return nil
	}
	pointer := ""
	for _, part := range prefix {
		pointer += "/" + strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1")
	}
	container, last, _, err := document.parent(pointer)
	if err != nil {
		return err
	}
	switch target := container.(type) {
	case *Object:
		target.values[last] = value
	case []any:
		index, err := arrayIndex(last, len(target), false)
		if err != nil {
			return err
		}
		target[index] = value
	}
	return nil
}

func arrayIndex(token string, length int, adding bool) (int, error) {
	if adding && token == "-" {
		return length, nil
	}
	if token == "" || (len(token) > 1 && token[0] == '0') {
		return 0, fmt.Errorf("%q is not an array index", token)
	}
	index, err := strconv.Atoi(token)
	if err != nil || index < 0 || index > length || (!adding && index == length) {
		return 0, fmt.Errorf("array index %q is out of range", token)
	}
	return index, nil
}

func clone(value any) any {
	switch typed := value.(type) {
	case *Object:
		copied := &Object{keys: append([]string(nil), typed.keys...), values: make(map[string]any, len(typed.values))}
		for key, member := range typed.values {
			copied.values[key] = clone(member)
		}
		return copied
	case []any:
		copied := make([]any, len(typed))
		for index, item := range typed {
			copied[index] = clone(item)
		}
		return copied
	}
	return value
}

func equal(left, right any) bool {
	switch a := left.(type) {
	case *Object:
		b, ok := right.(*Object)
		if !ok || len(a.values) != len(b.values) {
			return false
		}
		for key, value := range a.values {
			other, ok := b.values[key]
			if !ok || !equal(value, other) {
				return false
			}
		}
		return true
	case []any:
		b, ok := right.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for index := range a {
			if !equal(a[index], b[index]) {
				return false
			}
		}
		return true
	case json.Number:
		b, ok := right.(json.Number)
		if !ok {
			return false
		}
		x, errA := a.Float64()
		y, errB := b.Float64()
		return errA == nil && errB == nil && x == y
	}
	return left == right
}

// Member returns an object's member, and whether it has it.
func (object *Object) Member(key string) (any, bool) {
	value, ok := object.values[key]
	return value, ok
}

// Strings calls visit with each string value of the document, in document
// order: its JSON Pointer and the object that holds it, nil for an array
// item. A value visit returns with true replaces it.
func (document *Document) Strings(visit func(pointer string, holder *Object, value string) (string, bool)) {
	var walk func(pointer string, node any, holder *Object) any
	walk = func(pointer string, node any, holder *Object) any {
		switch typed := node.(type) {
		case string:
			if replaced, ok := visit(pointer, holder, typed); ok {
				return replaced
			}
		case *Object:
			for _, key := range typed.keys {
				typed.values[key] = walk(pointer+"/"+escapeToken(key), typed.values[key], typed)
			}
		case []any:
			for index, item := range typed {
				typed[index] = walk(pointer+"/"+strconv.Itoa(index), item, nil)
			}
		}
		return node
	}
	document.root = walk("", document.root, nil)
}

// Value is the document's root as Go values: maps keep no order.
func (document *Document) Value() any { return document.root }

// Replace stores value, a JSON text, at a pointer that exists.
func (document *Document) Replace(pointer string, value json.RawMessage) error {
	parsed, err := Parse(value)
	if err != nil {
		return err
	}
	return document.replace(pointer, parsed.root)
}

func escapeToken(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}
