package classifieroutput

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrInvalidJSON marks every shape the strict layer refuses: non-objects,
// trailing content, duplicate keys, fences, and depth or size overruns. The
// caller maps it to one fallback code; the detail stays in the error text.
var ErrInvalidJSON = errors.New("classifier output is not one strict JSON object")

// Object is one decoded JSON object preserving its top-level keys. Values stay
// raw so layer 3 can validate each field by expected type.
type Object map[string]json.RawMessage

// ParseObject accepts exactly one complete JSON object, allowing only leading
// and trailing whitespace. Everything else is rejected: arrays, scalars,
// fenced blocks, trailing prose or a second value, duplicate keys at any
// depth, and structures beyond the package limits. There is no first-brace
// scanning, no regex recovery, and no fence tolerance: the classification
// input is routinely JSON itself, and a lenient extractor would return the
// question instead of the answer.
func ParseObject(text string) (Object, error) {
	if len(text) > MaxJSONRunes {
		return nil, fmt.Errorf("%w: output exceeds %d runes", ErrInvalidJSON, MaxJSONRunes)
	}
	trimmed := bytes.TrimSpace([]byte(text))
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("%w: output is empty", ErrInvalidJSON)
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if err := rejectDuplicateKeys(decoder, 0); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	// Re-decode for values: the walk above consumed the stream position.
	decoder = json.NewDecoder(bytes.NewReader(trimmed))
	var object Object
	if err := decoder.Decode(&object); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	if object == nil {
		return nil, fmt.Errorf("%w: output is not an object", ErrInvalidJSON)
	}
	if len(object) > MaxObjectKeys {
		return nil, fmt.Errorf("%w: object has more than %d keys", ErrInvalidJSON, MaxObjectKeys)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing content after the object", ErrInvalidJSON)
	}
	return object, nil
}

// rejectDuplicateKeys walks one JSON value and refuses duplicate object keys
// at any depth, mirroring the contract wire decoder's rule so both ends of
// the pipeline share one tolerance level.
func rejectDuplicateKeys(decoder *json.Decoder, depth int) error {
	if depth > MaxDepth {
		return fmt.Errorf("nesting exceeds %d levels", MaxDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key must be a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate key %q", key)
			}
			seen[key] = struct{}{}
			if err := rejectDuplicateKeys(decoder, depth+1); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := rejectDuplicateKeys(decoder, depth+1); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return nil
	}
}
