package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"unicode"
	"unicode/utf8"
)

// The validators read JSON the way the Tauri shell's serde_json did:
// objects as maps whose keys are visited in sorted order, numbers kept as
// written so an integer check refuses 1.0 and -1.

type object = map[string]any

// decodeValue parses one JSON value, keeping numbers as json.Number.
func decodeValue(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after JSON value")
	}
	return value, nil
}

// encodeValue writes a decoded value back as JSON.
func encodeValue(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		// Decoded JSON always encodes.
		panic(err)
	}
	return raw
}

func asObject(value any) (object, bool) {
	o, ok := value.(map[string]any)
	return o, ok
}

func asString(value any) (string, bool) {
	s, ok := value.(string)
	return s, ok
}

func asBool(value any) (bool, bool) {
	b, ok := value.(bool)
	return b, ok
}

func asArray(value any) ([]any, bool) {
	a, ok := value.([]any)
	return a, ok
}

// asU64 reads a non-negative integer written without a fraction or
// exponent.
func asU64(value any) (uint64, bool) {
	n, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseUint(n.String(), 10, 64)
	return parsed, err == nil
}

func asI64(value any) (int64, bool) {
	n, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseInt(n.String(), 10, 64)
	return parsed, err == nil
}

func field(o object, key string) (any, bool) {
	value, ok := o[key]
	return value, ok
}

// stringField is the string at key, "" and false when it is missing or
// not a string.
func stringField(o object, key string) (string, bool) {
	value, ok := o[key]
	if !ok {
		return "", false
	}
	return asString(value)
}

func sortedKeys(o object) []string {
	keys := make([]string, 0, len(o))
	for key := range o {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func runeCount(s string) int { return utf8.RuneCountInString(s) }

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
