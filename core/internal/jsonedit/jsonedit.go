// Package jsonedit splices values into a JSON document without re-encoding the
// rest of it: whitespace, member order, number spelling and string escapes
// outside the edited value are kept byte for byte.
package jsonedit

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/MoeForge/moego/ojson"
)

// ErrUnsafe reports an edit that cannot be applied in place.
var ErrUnsafe = errors.New("JSON document cannot be edited in place")

// Encode returns value as compact JSON without HTML escaping.
func Encode(value any) ([]byte, error) {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(encoded.Bytes(), []byte{'\n'}), nil
}

// Set encodes value and stores it at path.
func Set(body []byte, path string, value any) ([]byte, error) {
	encoded, err := Encode(value)
	if err != nil {
		return nil, err
	}
	return SetRaw(body, path, encoded)
}

// SetRaw stores raw JSON at path, replacing the old value or adding a missing
// member at the end of its object.
func SetRaw(body []byte, path string, value []byte) ([]byte, error) {
	return withEnvelope(body, func(document []byte) ([]byte, error) {
		return ojson.SetRawBytes(document, path, value)
	})
}

// Delete removes the member or element at path.
func Delete(body []byte, path string) ([]byte, error) {
	return withEnvelope(body, func(document []byte) ([]byte, error) {
		return ojson.DeleteBytes(document, path)
	})
}

// Insert adds value to the array at path as element index. An index at or
// past the end appends; a negative index prepends. The existing elements keep
// their raw bytes.
func Insert(body []byte, path string, index int, value any) ([]byte, error) {
	array := ojson.GetBytes(body, path)
	if !array.IsArray() || !located(body, array) {
		return nil, ErrUnsafe
	}
	item, err := Encode(value)
	if err != nil {
		return nil, err
	}
	elements := array.Array()
	var at int
	var text []byte
	switch {
	case index <= 0 || len(elements) == 0:
		at = array.Index + 1
		text = item
		if len(elements) > 0 {
			text = append(text, ',')
		}
	case index >= len(elements):
		last := elements[len(elements)-1]
		if !located(body, last) {
			return nil, ErrUnsafe
		}
		at = last.Index + len(last.Raw)
		text = append([]byte{','}, item...)
	default:
		next := elements[index]
		if !located(body, next) {
			return nil, ErrUnsafe
		}
		at = next.Index
		text = append(item, ',')
	}
	updated := make([]byte, 0, len(body)+len(text))
	updated = append(updated, body[:at]...)
	updated = append(updated, text...)
	return append(updated, body[at:]...), nil
}

// located reports whether value's offset points at its raw bytes in body.
func located(body []byte, value ojson.Result) bool {
	return value.Index > 0 && value.Index+len(value.Raw) <= len(body) &&
		string(body[value.Index:value.Index+len(value.Raw)]) == value.Raw
}

// withEnvelope applies edit to the document without its outer whitespace,
// which SJSON may strip when it adds a member, and then restores it.
func withEnvelope(body []byte, edit func([]byte) ([]byte, error)) ([]byte, error) {
	start := len(body) - len(bytes.TrimLeft(body, " \t\r\n"))
	end := len(bytes.TrimRight(body, " \t\r\n"))
	if start >= end {
		return nil, ErrUnsafe
	}
	updated, err := edit(body[start:end])
	if err != nil {
		return nil, err
	}
	if start == 0 && end == len(body) {
		return updated, nil
	}
	wrapped := make([]byte, 0, start+len(updated)+len(body)-end)
	wrapped = append(wrapped, body[:start]...)
	wrapped = append(wrapped, updated...)
	return append(wrapped, body[end:]...), nil
}
