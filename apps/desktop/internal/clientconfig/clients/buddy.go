package clients

import (
	"slices"
	"strings"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// The `models.json` that WorkBuddy and CodeBuddy, Tencent's agents, read
// their own models from: entries that each carry an address and key, in
// `{"models": [...], "availableModels": [...]}`, or the entry list alone
// once WorkBuddy's model settings saved the file.
//
// An entry is one key, `models[<id>]`, holding the entry as compact JSON;
// an allowlist place is `availableModels[<id>]`.

const (
	// buddyVendor is the group the entries show under in the clients'
	// model pickers. Never part of a model request.
	buddyVendor  = "AstrLink"
	buddyEntries = "models"
	buddyAllowed = "availableModels"
)

// buddyEfforts are the reasoning efforts an entry may list, in the
// clients' order.
var buddyEfforts = []string{"minimal", "low", "medium", "high", "xhigh", "max"}

func buddyEntryKey(id string) string   { return buddyEntries + "[" + id + "]" }
func buddyAllowedKey(id string) string { return buddyAllowed + "[" + id + "]" }

func isBuddyEntryKey(key string) bool   { return strings.HasPrefix(key, buddyEntries+"[") }
func isBuddyAllowedKey(key string) bool { return strings.HasPrefix(key, buddyAllowed+"[") }

// buddyHolds reports whether key is a key of `models.json`.
func buddyHolds(key string) bool { return isBuddyEntryKey(key) || isBuddyAllowedKey(key) }

// buddyHoldsConnection reports whether key is an entry, which carries its
// own address and key.
func buddyHoldsConnection(key string) bool { return isBuddyEntryKey(key) }

// buddyEntryOrder places a property of an entry AstrLink writes among its
// siblings.
func buddyEntryOrder(name string) int {
	order := []string{"id", "name", "vendor", "url", "api", "apiKey", "maxInputTokens", "maxOutputTokens", "supportsReasoning", "reasoning"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return format.NoRank(name)
}

// buddyEntry is the entry for model id posting to url; api names a
// protocol other than Chat. The name is the id: the clients put it into
// the system prompt they send upstream.
func buddyEntry(id, url, api, token string, details *cc.ModelDetails) string {
	entry := map[string]any{"id": id, "name": id, "vendor": buddyVendor, "url": url, "apiKey": token}
	if api != "" {
		entry["api"] = api
	}
	if details != nil {
		if details.ContextWindow != nil {
			entry["maxInputTokens"] = number(*details.ContextWindow)
		}
		if details.MaxTokens != nil {
			entry["maxOutputTokens"] = number(*details.MaxTokens)
		}
		if details.HasReasoning {
			entry["supportsReasoning"] = true
			var efforts []any
			for _, effort := range buddyEfforts {
				if slices.Contains(details.Reasoning, effort) {
					efforts = append(efforts, effort)
				}
			}
			if len(efforts) > 0 {
				entry["reasoning"] = map[string]any{"supportedEfforts": efforts}
			}
		}
	}
	return format.Encode(entry)
}

// buddyModelKeys is the entry and allowlist place of every listed model.
// route gives each model's address and `api` value from its protocol.
func buddyModelKeys(connection *cc.Connection, details cc.Catalog, route func(id string) (url, api string)) []cc.Setting {
	var keys []cc.Setting
	for _, id := range connection.ModelList() {
		url, api := route(id)
		var d *cc.ModelDetails
		if found, ok := details[id]; ok {
			d = &found
		}
		keys = append(keys,
			cc.Setting{Key: buddyEntryKey(id), Value: buddyEntry(id, url, api, connection.Token, d)},
			cc.Setting{Key: buddyAllowedKey(id), Value: id})
	}
	return keys
}

// buddyRerouted is the entry at key with its address pointed elsewhere by
// route, which takes the entry's `api`.
func buddyRerouted(config *cc.Config, key string, route func(api string) string) (string, bool) {
	text, ok := config.String(key)
	if !ok {
		return "", false
	}
	value, err := format.DecodeJSON(text)
	entry, isObject := value.(map[string]any)
	if err != nil || !isObject {
		return "", false
	}
	api, _ := entry["api"].(string)
	entry["url"] = route(api)
	return format.Encode(entry), true
}

// buddySyncedKeys points every entry record wrote elsewhere by route.
func buddySyncedKeys(config *cc.Config, record *cc.Record, route func(api string) string) []cc.Setting {
	var keys []cc.Setting
	for _, key := range sortedKeys(record.Keys) {
		if !buddyHoldsConnection(key) {
			continue
		}
		if entry, ok := buddyRerouted(config, key, route); ok {
			keys = append(keys, cc.Setting{Key: key, Value: entry})
		}
	}
	return keys
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// buddyCurrentModels is the models AstrLink's entries hold now, in the
// file's order.
func buddyCurrentModels(config *cc.Config, file int) cc.CurrentModels {
	if file >= len(config.Documents) {
		return cc.CurrentModels{}
	}
	json, ok := config.Documents[file].(*format.JSONDocument)
	if !ok {
		return cc.CurrentModels{}
	}
	value, _ := json.Value()
	var list []any
	switch v := value.(type) {
	case []any:
		list = v
	case map[string]any:
		list, _ = v[buddyEntries].([]any)
	}
	var ids []string
	for _, item := range list {
		entry, _ := item.(map[string]any)
		if vendor, _ := entry["vendor"].(string); vendor != buddyVendor {
			continue
		}
		if id, ok := entry["id"].(string); ok {
			if id, ok := cc.CleanModel(id); ok {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return cc.CurrentModels{}
	}
	return cc.CurrentModels{Model: cc.Some(ids[0]), Models: ids}
}

// buddyModelsFile is `models.json`, strict JSON: the clients drop the
// whole file over a comment or trailing comma.
type buddyModelsFile struct{}

// buddyPath is key as the file is shaped: a root list holds the entries
// itself and has no allowlist.
func buddyPath(document format.Document, key string) (string, bool) {
	if json, ok := document.(*format.JSONDocument); ok && json.IsList() {
		if !isBuddyEntryKey(key) {
			return "", false
		}
		return key[len(buddyEntries):], true
	}
	return key, true
}

// buddyRestricts reports whether the file has an allowlist that leaves
// models out: an empty one allows every model, and a new one would hide
// the built-in ones.
func buddyRestricts(document format.Document) bool {
	json, ok := document.(*format.JSONDocument)
	if !ok {
		return false
	}
	list, _, _ := json.GetJSON(buddyAllowed)
	items, _ := list.([]any)
	return len(items) > 0
}

func (buddyModelsFile) Format() format.Format { return format.JSONFormat{RootList: true} }

func (buddyModelsFile) Holds(key string) bool { return buddyHolds(key) }

func (buddyModelsFile) Get(document format.Document, key string) (string, bool, bool) {
	path, ok := buddyPath(document, key)
	if !ok {
		return "", false, false
	}
	return document.Get(path)
}

func (buddyModelsFile) Set(document format.Document, key, value string) error {
	if isBuddyAllowedKey(key) && !buddyRestricts(document) {
		return nil
	}
	path, ok := buddyPath(document, key)
	if !ok {
		return nil
	}
	if json, ok := document.(*format.JSONDocument); ok && isBuddyEntryKey(key) {
		entry, err := format.DecodeJSON(value)
		if err != nil {
			return err
		}
		return json.SetJSON(path, entry, buddyEntryOrder)
	}
	return document.Set(path, value)
}

func (buddyModelsFile) Remove(document format.Document, key string) {
	if path, ok := buddyPath(document, key); ok {
		document.Remove(path)
	}
}

// Tidy drops an entry list a removal emptied with the entries.
func (buddyModelsFile) Tidy(document format.Document) {
	if json, ok := document.(*format.JSONDocument); ok {
		if list, _, _ := json.GetJSON(buddyEntries); list != nil {
			if items, ok := list.([]any); ok && len(items) == 0 {
				json.Remove(buddyEntries)
			}
		}
	}
}
