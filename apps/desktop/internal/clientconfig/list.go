package clientconfig

import (
	"math"
	"slices"

	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// ProviderID is the provider AstrLink adds to OpenCode and OpenClaw; local
// config only.
const ProviderID = "astrlink"

// PiThinkingLevels are the thinking levels Pi maps to a model's own.
var PiThinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// PushPresent adds key with its value when the file has it.
func PushPresent(keys *[]format.KeyValue, file File, document format.Document, key string) {
	if value, ok, present := FileGet(file, document, key); present {
		*keys = append(*keys, format.KeyValue{Key: key, Value: value, Present: ok})
	}
}

// EntryRank is where a property goes among its siblings when AstrLink
// writes a model entry: Pi's and OpenClaw's model entries read best in
// this order.
func EntryRank(name string) int {
	order := []string{"id", "name", "reasoning", "thinkingLevelMap", "contextWindow", "maxTokens", "limit", "context", "output"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	if i := slices.Index(PiThinkingLevels, name); i >= 0 {
		return len(order) + i
	}
	return math.MaxInt
}

// GetList reads a whole model list, kept as JSON with sorted keys so
// reformatting the file changes nothing.
func GetList(document format.Document, key string) (value string, ok, present bool) {
	if json, is := document.(*format.JSONDocument); is {
		list, ok, present := json.GetJSON(key)
		if !ok {
			return "", false, present
		}
		return format.Encode(list), true, true
	}
	return document.Get(key)
}

// SetList writes a whole model list.
func SetList(document format.Document, key, value string) error {
	if json, is := document.(*format.JSONDocument); is {
		list, err := format.DecodeJSON(value)
		if err != nil {
			list = nil
		}
		return json.SetJSON(key, list, EntryRank)
	}
	return document.Set(key, value)
}

// ModelEntry is one model of a list, by id.
type ModelEntry struct {
	ID    string
	Entry any
}

// ModelEntries are the models a list holds, each once, by id: the keys of
// an object, or the `id` of each entry of an array. Names AstrLink could
// not write back are left out.
func ModelEntries(list any) []ModelEntry {
	var entries []ModelEntry
	switch v := list.(type) {
	case map[string]any:
		for _, id := range format.SortedKeys(v) {
			entries = append(entries, ModelEntry{id, v[id]})
		}
	case []any:
		for _, entry := range v {
			if object, ok := entry.(map[string]any); ok {
				if id, ok := object["id"].(string); ok {
					entries = append(entries, ModelEntry{id, entry})
				}
			}
		}
	}
	seen := map[string]bool{}
	var out []ModelEntry
	for _, entry := range entries {
		id, ok := CleanModel(entry.ID)
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, ModelEntry{id, entry.Entry})
	}
	return out
}

// ModelIDs are the ids of a list's entries; nil for none.
func ModelIDs(entries []ModelEntry) []string {
	var ids []string
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}
