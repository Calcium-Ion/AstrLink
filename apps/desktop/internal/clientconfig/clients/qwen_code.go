package clients

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Qwen Code reads `~/.qwen/settings.json` (comments allowed). Its models
// are entries in `modelProviders.<protocol>` lists, found by id and
// address; AstrLink's are the ones whose key is `ASTRLINK_QWEN_API_KEY`,
// from `env` in the same file. They lead each list, so a hand-written
// entry of the same id and address cannot take their place.

var QwenCode = cc.New(qwenCode{})

type qwenCode struct{}

// qwenSettings is `settings.json`.
type qwenSettings struct{}

const (
	// qwenKeyName is the variable every AstrLink entry reads its key from,
	// and so what marks an entry as AstrLink's. A name of its own, since
	// Qwen reads the shell's variables first.
	qwenKeyName     = "ASTRLINK_QWEN_API_KEY"
	qwenKeyKey      = "env.ASTRLINK_QWEN_API_KEY"
	qwenAuthKey     = "security.auth.selectedType"
	qwenModelKey    = "model.name"
	qwenModelURLKey = "model.baseUrl"
)

var qwenLists = []string{"modelProviders.openai", "modelProviders.anthropic", "modelProviders.gemini"}

// qwenRoute is the list, auth type, and `wireApi` a protocol's models take.
func qwenRoute(protocol string) (list, auth, wire string) {
	switch protocol {
	case "anthropic.messages":
		return qwenLists[1], "anthropic", ""
	case "google.generate_content":
		return qwenLists[2], "gemini", ""
	case "openai.responses":
		return qwenLists[0], "openai-responses", "responses"
	}
	return qwenLists[0], "openai", ""
}

// qwenEntryURL: Anthropic and Gemini entries take the bare origin; OpenAI
// ones `/v1`.
func qwenEntryURL(list, origin string) string {
	if list == qwenLists[0] {
		return origin + "/v1"
	}
	return origin
}

func qwenRank(name string) int {
	order := []string{"id", "name", "description", "baseUrl", "envKey", "wireApi", "generationConfig", "contextWindowSize", "samplingParams"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

func qwenOwned(entry any) bool {
	key, _ := field(entry, "envKey")
	return key == qwenKeyName
}

// qwenEntry is the entry for id, with what the catalog says of its limits.
// Its name and description label it in Qwen's model picker only.
func qwenEntry(id, list, origin, wire string, details cc.Catalog) map[string]any {
	entry := map[string]any{
		"id":          id,
		"name":        id,
		"description": "AstrLink",
		"baseUrl":     qwenEntryURL(list, origin),
		"envKey":      qwenKeyName,
	}
	if wire != "" {
		entry["wireApi"] = wire
	}
	if d, ok := details[id]; ok {
		generation := map[string]any{}
		if d.ContextWindow != nil {
			generation["contextWindowSize"] = number(*d.ContextWindow)
		}
		if d.MaxTokens != nil {
			generation["samplingParams"] = map[string]any{"max_tokens": number(*d.MaxTokens)}
		}
		if len(generation) > 0 {
			entry["generationConfig"] = generation
		}
	}
	return entry
}

func (qwenCode) ID() string   { return "qwen_code" }
func (qwenCode) Name() string { return "Qwen Code" }
func (qwenCode) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages", "google.generate_content"}
}
func (qwenCode) ListsModels() bool { return true }

// Dir is the default `~/.qwen`: `QWEN_HOME` moves it, from the shell or a
// `.env`, but AstrLink writes the default one.
func (qwenCode) Dir(home agents.Home, _ string) string { return filepath.Join(home.Dir(), ".qwen") }

func (qwenCode) Files() []cc.File { return []cc.File{qwenSettings{}} }

func (q qwenCode) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	return []string{filepath.Join(q.Dir(home, target), "settings.json")}
}

func (qwenCode) ConnectionKeys() []string { return []string{qwenKeyKey} }

// HoldsConnection: each entry carries its own address.
func (qwenCode) HoldsConnection(key string) bool { return slices.Contains(qwenLists, key) }

// CurrentModels: the model Qwen starts with when it is one of AstrLink's,
// else the first of them.
func (qwenCode) CurrentModels(config *cc.Config) cc.CurrentModels {
	var entries []any
	for _, list := range qwenLists {
		entries = append(entries, configList(config, list)...)
	}
	var ids []string
	for _, entry := range entries {
		object, _ := entry.(map[string]any)
		id, _ := object["id"].(string)
		if id, ok := cc.CleanModel(id); ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	var models cc.CurrentModels
	url, hasURL := config.String(qwenModelURLKey)
	if model, ok := config.Model(qwenModelKey); ok && slices.ContainsFunc(entries, func(entry any) bool {
		object, _ := entry.(map[string]any)
		id, _ := object["id"].(string)
		base, isString := object["baseUrl"].(string)
		return id == model && (!hasURL || isString && base == url)
	}) {
		models.Model = cc.Some(model)
	} else if len(ids) > 0 {
		models.Model = cc.Some(ids[0])
	}
	if len(ids) > 0 {
		models.Models = ids
	}
	return models
}

func (qwenCode) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	model, hasModel := connection.Model("model")
	ordered := connection.ModelList()
	if at := slices.Index(ordered, model); hasModel && at >= 0 {
		ordered = slices.Insert(slices.Delete(ordered, at, at+1), 0, model)
	}
	lists := map[string][]any{}
	for _, id := range ordered {
		list, _, wire := qwenRoute(connection.Protocol(QwenCode, id))
		lists[list] = append(lists[list], qwenEntry(id, list, connection.Origin, wire, details))
	}
	var keys []cc.Setting
	for _, list := range qwenLists {
		if entries := lists[list]; len(entries) > 0 {
			keys = append(keys, cc.Setting{Key: list, Value: format.Encode(entries)})
		}
	}
	keys = append(keys, cc.Setting{Key: qwenKeyKey, Value: connection.Token})
	if hasModel {
		list, auth, _ := qwenRoute(connection.Protocol(QwenCode, model))
		keys = append(keys,
			cc.Setting{Key: qwenAuthKey, Value: auth},
			cc.Setting{Key: qwenModelKey, Value: model},
			cc.Setting{Key: qwenModelURLKey, Value: qwenEntryURL(list, connection.Origin)})
	}
	return keys
}

// StartFile names the settings version in a file AstrLink creates.
func (qwenCode) StartFile(config *cc.Config) *cc.FileError {
	if json, ok := config.Documents[0].(*format.JSONDocument); ok {
		if err := json.SetJSON("$version", format.Number("4"), qwenRank); err != nil {
			return &cc.FileError{Message: err.Error()}
		}
	}
	return nil
}

// BaseURLKey is unused: SyncedKeys points every entry elsewhere.
func (qwenCode) BaseURLKey() string { return "" }

func (qwenCode) SyncedKeys(config *cc.Config, record *cc.Record, origin string) []cc.Setting {
	var keys []cc.Setting
	for _, list := range qwenLists {
		if _, ok := record.Keys[list]; !ok {
			continue
		}
		entries := configList(config, list)
		for _, entry := range entries {
			if object, ok := entry.(map[string]any); ok {
				object["baseUrl"] = qwenEntryURL(list, origin)
			}
		}
		if len(entries) > 0 {
			keys = append(keys, cc.Setting{Key: list, Value: format.Encode(entries)})
		}
	}
	// The default model's address follows when it is AstrLink's.
	old := strings.TrimSuffix(record.BaseURL, "/v1")
	switch url, ok := config.String(qwenModelURLKey); {
	case ok && url == old:
		keys = append(keys, cc.Setting{Key: qwenModelURLKey, Value: origin})
	case ok && url == record.BaseURL:
		keys = append(keys, cc.Setting{Key: qwenModelURLKey, Value: origin + "/v1"})
	}
	return keys
}

// Format is JSON with comments: Qwen strips comments before a strict parse.
func (qwenSettings) Format() format.Format {
	return format.JSONFormat{Syntax: &format.JSONC, Objects: []string{"modelProviders", "env", "security", "security.auth", "model"}}
}

// Get reads a list key as AstrLink's entries in it, compact.
func (qwenSettings) Get(document format.Document, key string) (string, bool, bool) {
	json, ok := document.(*format.JSONDocument)
	if !ok || !slices.Contains(qwenLists, key) {
		return document.Get(key)
	}
	return getOwned(json, key, qwenOwned)
}

func (qwenSettings) Set(document format.Document, key, value string) error {
	json, ok := document.(*format.JSONDocument)
	if !ok || !slices.Contains(qwenLists, key) {
		return document.Set(key, value)
	}
	return setOwned(json, key, value, qwenOwned, qwenRank)
}

func (qwenSettings) Remove(document format.Document, key string) {
	if json, ok := document.(*format.JSONDocument); ok && slices.Contains(qwenLists, key) {
		json.RemoveItems(key, qwenOwned)
		return
	}
	document.Remove(key)
}

// Tidy drops emptied lists, then the objects around them.
func (qwenSettings) Tidy(document format.Document) {
	json, ok := document.(*format.JSONDocument)
	if !ok {
		return
	}
	for _, list := range qwenLists {
		dropEmptyList(json, list)
	}
	json.DropEmpty([]string{"modelProviders", "env", "security.auth", "security", "model"})
}

// IsEmpty: `$version` alone is left.
func (qwenSettings) IsEmpty(document format.Document) bool {
	if json, ok := document.(*format.JSONDocument); ok {
		return json.HoldsOnly(func(p format.JSONProperty) bool { return p.HasName && p.Name == "$version" })
	}
	return document.IsEmpty()
}
