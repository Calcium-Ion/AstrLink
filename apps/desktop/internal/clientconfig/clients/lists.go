package clients

import (
	"fmt"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Lists in JSON files that hold the user's entries beside AstrLink's, read
// and written as AstrLink's entries alone.

// field is the string an entry holds at name.
func field(entry any, name string) (string, bool) {
	object, _ := entry.(map[string]any)
	value, ok := object[name].(string)
	return value, ok
}

// configList is the list a config holds at key, decoded.
func configList(config *cc.Config, key string) []any {
	value, ok := config.String(key)
	if !ok {
		return nil
	}
	decoded, _ := format.DecodeJSON(value)
	items, _ := decoded.([]any)
	return items
}

// configObject is the object a config holds at key, decoded.
func configObject(config *cc.Config, key string) (map[string]any, bool) {
	value, ok := config.String(key)
	if !ok {
		return nil, false
	}
	decoded, err := format.DecodeJSON(value)
	object, isObject := decoded.(map[string]any)
	return object, err == nil && isObject
}

// getWhole reads the value at key, whatever it is, as compact JSON.
func getWhole(json *format.JSONDocument, key string) (string, bool, bool) {
	value, ok, present := json.GetJSON(key)
	if !ok {
		return "", false, present
	}
	return format.Encode(value), true, true
}

// setWhole writes JSON text at key as a value; rank orders its objects'
// properties.
func setWhole(json *format.JSONDocument, key, value string, rank format.Rank) error {
	decoded, err := format.DecodeJSON(value)
	if err != nil {
		return err
	}
	return json.SetJSON(key, decoded, rank)
}

// getOwned reads the list at key as the entries owned picks, compact;
// nothing when there are none.
func getOwned(json *format.JSONDocument, key string, owned func(any) bool) (string, bool, bool) {
	value, _, _ := json.GetJSON(key)
	items, _ := value.([]any)
	var entries []any
	for _, entry := range items {
		if owned(entry) {
			entries = append(entries, entry)
		}
	}
	if len(entries) == 0 {
		return "", false, false
	}
	return format.Encode(entries), true, true
}

// setOwned replaces the entries owned picks in the list at key with value's,
// leading the list.
func setOwned(json *format.JSONDocument, key, value string, owned func(any) bool, rank format.Rank) error {
	decoded, err := format.DecodeJSON(value)
	entries, isList := decoded.([]any)
	if err != nil || !isList {
		return fmt.Errorf("%s is not a list", key)
	}
	json.RemoveItems(key, owned)
	return json.PrependItems(key, entries, rank)
}

// dropEmptyList removes the list at key once it is empty.
func dropEmptyList(json *format.JSONDocument, key string) {
	if value, _, _ := json.GetJSON(key); value != nil {
		if items, isList := value.([]any); isList && len(items) == 0 {
			json.Remove(key)
		}
	}
}
