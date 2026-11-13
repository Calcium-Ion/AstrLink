package clients

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Droid reads `~/.factory/settings.json`. Its models are `customModels`
// entries, each with its own address and key; AstrLink's are the ones that
// reach a local gateway with an AstrLink token, and lead the list. The
// default model is `sessionDefaultSettings.model`. Droid tells the model
// its `displayName`, so that is the bare model id.

var Droid = cc.New(droid{})

type droid struct{}

// droidSettings is `settings.json`.
type droidSettings struct{}

const (
	droidModelsKey  = "customModels"
	droidDefaultKey = "sessionDefaultSettings.model"
	droidIDPrefix   = "custom:"
	// droidLocalFile holds Droid's overrides beside `settings.json`.
	droidLocalFile = "settings.local.json"
)

// droidProvider is Droid's `provider` for a gateway protocol.
func droidProvider(protocol string) string {
	switch protocol {
	case "anthropic.messages":
		return "anthropic"
	case "openai.responses":
		return "openai"
	}
	return "generic-chat-completion-api"
}

// droidEntryURL: Anthropic's SDK adds `/v1` itself; the OpenAI ones take
// it.
func droidEntryURL(provider, origin string) string {
	if provider == "anthropic" {
		return origin
	}
	return origin + "/v1"
}

func droidRank(name string) int {
	order := []string{"id", "model", "displayName", "baseUrl", "apiKey", "provider", "maxContextLimit", "maxOutputTokens"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

// droidOwned reports whether an entry sends an AstrLink token to a gateway
// on this computer.
func droidOwned(entry any) bool {
	key, _ := field(entry, "apiKey")
	url, _ := field(entry, "baseUrl")
	return strings.HasPrefix(key, "astr_") &&
		(strings.HasPrefix(url, "http://127.0.0.1:") || strings.HasPrefix(url, "http://localhost:"))
}

func droidEntry(id, protocol string, connection *cc.Connection, details cc.Catalog) map[string]any {
	provider := droidProvider(protocol)
	entry := map[string]any{
		"id":          droidIDPrefix + id,
		"model":       id,
		"displayName": id,
		"baseUrl":     droidEntryURL(provider, connection.Origin),
		"apiKey":      connection.Token,
		"provider":    provider,
	}
	if d, ok := details[id]; ok {
		if d.ContextWindow != nil {
			entry["maxContextLimit"] = number(*d.ContextWindow)
		}
		if d.MaxTokens != nil {
			entry["maxOutputTokens"] = number(*d.MaxTokens)
		}
	}
	return entry
}

func (droid) ID() string   { return "droid" }
func (droid) Name() string { return "Droid" }
func (droid) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}
func (droid) ListsModels() bool { return true }

func (droid) Dir(home agents.Home, _ string) string { return filepath.Join(home.Dir(), ".factory") }

func (droid) Files() []cc.File { return []cc.File{droidSettings{}} }

func (d droid) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	return []string{filepath.Join(d.Dir(home, target), "settings.json")}
}

func (droid) ConnectionKeys() []string { return nil }

// HoldsConnection: each entry carries its own address and key.
func (droid) HoldsConnection(key string) bool { return key == droidModelsKey }

func (droid) CurrentModels(config *cc.Config) cc.CurrentModels {
	entries := configList(config, droidModelsKey)
	var ids []string
	for _, entry := range entries {
		object, _ := entry.(map[string]any)
		model, _ := object["model"].(string)
		if id, ok := cc.CleanModel(model); ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	var models cc.CurrentModels
	if chosen, ok := config.String(droidDefaultKey); ok {
		for _, entry := range entries {
			object, _ := entry.(map[string]any)
			if id, _ := object["id"].(string); id == chosen {
				if model, ok := object["model"].(string); ok {
					models.Model = cc.Some(model)
				}
				break
			}
		}
	}
	if models.Model == nil && len(ids) > 0 {
		models.Model = cc.Some(ids[0])
	}
	if len(ids) > 0 {
		models.Models = ids
	}
	return models
}

func (droid) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	var entries []any
	for _, id := range connection.ModelList() {
		entries = append(entries, droidEntry(id, connection.Protocol(Droid, id), connection, details))
	}
	var keys []cc.Setting
	if len(entries) > 0 {
		keys = append(keys, cc.Setting{Key: droidModelsKey, Value: format.Encode(entries)})
	}
	if model, ok := connection.Model("model"); ok {
		keys = append(keys, cc.Setting{Key: droidDefaultKey, Value: droidIDPrefix + model})
	}
	return keys
}

// BaseURLKey is unused: SyncedKeys points every entry elsewhere.
func (droid) BaseURLKey() string { return "" }

func (droid) SyncedKeys(config *cc.Config, _ *cc.Record, origin string) []cc.Setting {
	entries := configList(config, droidModelsKey)
	if len(entries) == 0 {
		return nil
	}
	for _, entry := range entries {
		if object, ok := entry.(map[string]any); ok {
			provider, _ := object["provider"].(string)
			object["baseUrl"] = droidEntryURL(provider, origin)
		}
	}
	return []cc.Setting{{Key: droidModelsKey, Value: format.Encode(entries)}}
}

// Notes: `settings.local.json` overrides the models or the default.
func (d droid) Notes(home agents.Home, target string, _ *cc.Config) []string {
	text, err := os.ReadFile(filepath.Join(d.Dir(home, target), droidLocalFile))
	if err != nil {
		return nil
	}
	document, err := format.ParseJSON(string(text), format.JSONFormat{Syntax: &format.JSONC})
	if err != nil {
		return nil
	}
	value, _ := document.Value()
	local, _ := value.(map[string]any)
	settings, _ := local["sessionDefaultSettings"].(map[string]any)
	_, models := local[droidModelsKey]
	_, model := settings["model"]
	if models || model {
		return []string{"overridden"}
	}
	return nil
}

// Format is JSON with comments, as Droid reads it.
func (droidSettings) Format() format.Format {
	return format.JSONFormat{Syntax: &format.JSONC, Objects: []string{"sessionDefaultSettings"}}
}

// Get reads the list as AstrLink's entries in it, compact.
func (droidSettings) Get(document format.Document, key string) (string, bool, bool) {
	json, ok := document.(*format.JSONDocument)
	if !ok || key != droidModelsKey {
		return document.Get(key)
	}
	return getOwned(json, key, droidOwned)
}

func (droidSettings) Set(document format.Document, key, value string) error {
	json, ok := document.(*format.JSONDocument)
	if !ok || key != droidModelsKey {
		return document.Set(key, value)
	}
	return setOwned(json, key, value, droidOwned, droidRank)
}

func (droidSettings) Remove(document format.Document, key string) {
	if json, ok := document.(*format.JSONDocument); ok && key == droidModelsKey {
		json.RemoveItems(key, droidOwned)
		return
	}
	document.Remove(key)
}

// Tidy drops an emptied list, then the object the default sat in.
func (droidSettings) Tidy(document format.Document) {
	json, ok := document.(*format.JSONDocument)
	if !ok {
		return
	}
	dropEmptyList(json, droidModelsKey)
	json.DropEmpty([]string{"sessionDefaultSettings"})
}
