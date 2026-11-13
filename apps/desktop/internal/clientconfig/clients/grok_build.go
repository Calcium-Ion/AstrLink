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

// Grok Build reads `~/.grok/config.toml`. AstrLink adds a
// `[model."<model>"]` table for each model, each with its own address and
// token, since a model without a key is sent the xAI login. Grok lists the
// table names in its prompt, so they are the bare model ids, and the
// `description` Grok keeps to itself marks AstrLink's. `[models] default`
// picks the model Grok starts with, and `session_summary` the one that
// names sessions, which otherwise asks AstrLink for a Grok model. The
// community grok-cli shares `~/.grok` and reads none of this.

var GrokBuild = cc.New(grokBuild{})

type grokBuild struct{}

// grokConfig is `config.toml`.
type grokConfig struct{}

const (
	// grokEntryPrefix is `model.<id>`: one model's table, as JSON.
	grokEntryPrefix = "model."
	grokMark        = "AstrLink"
	grokDefaultKey  = "models.default"
	grokTitleKey    = "models.session_summary"
	// grokAnthropicOutput: what Grok sends as `max_tokens` to an
	// Anthropic model whose limit it is not told is 128000, more than most
	// take.
	grokAnthropicOutput = 32_000
)

func grokRank(name string) int {
	order := []string{"model", "name", "description", "base_url", "api_key", "api_backend", "context_window", "max_completion_tokens"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

// grokBackend is `api_backend` for a gateway protocol. Every backend takes
// `/v1`.
func grokBackend(protocol string) string {
	switch protocol {
	case "anthropic.messages":
		return "messages"
	case "openai.responses":
		return "responses"
	}
	return "chat_completions"
}

func grokEntry(id, protocol string, connection *cc.Connection, details cc.Catalog) map[string]any {
	backend := grokBackend(protocol)
	entry := map[string]any{
		"model":       id,
		"name":        id,
		"description": grokMark,
		"base_url":    connection.Origin + "/v1",
		"api_key":     connection.Token,
		"api_backend": backend,
	}
	d := details[id]
	if d.ContextWindow != nil {
		entry["context_window"] = number(*d.ContextWindow)
	}
	switch {
	case d.MaxTokens != nil:
		entry["max_completion_tokens"] = number(*d.MaxTokens)
	case backend == "messages":
		entry["max_completion_tokens"] = number(grokAnthropicOutput)
	}
	return entry
}

func (grokConfig) Format() format.Format { return format.TOMLFormat{} }

func (grokConfig) Get(document format.Document, key string) (string, bool, bool) {
	toml, ok := document.(*format.TOMLDocument)
	if !ok {
		return document.Get(key)
	}
	if id, entry := strings.CutPrefix(key, grokEntryPrefix); entry {
		return getTable(toml, "model", id)
	}
	name, inModels := strings.CutPrefix(key, "models.")
	if !inModels {
		return document.Get(key)
	}
	models := format.TableLike(toml.Root().Get("models"))
	if models == nil || models.Get(name) == nil {
		return "", false, false
	}
	value, ok := format.TOMLItemString(models.Get(name))
	return value, ok, true
}

func (grokConfig) Set(document format.Document, key, value string) error {
	toml, ok := document.(*format.TOMLDocument)
	if !ok {
		return document.Set(key, value)
	}
	if id, entry := strings.CutPrefix(key, grokEntryPrefix); entry {
		return setTable(toml, "model", id, key, value, grokRank)
	}
	name, inModels := strings.CutPrefix(key, "models.")
	if !inModels {
		return document.Set(key, value)
	}
	models, err := toml.ParentTable("models")
	if err != nil {
		return err
	}
	format.SetTOMLString(models, name, value)
	return nil
}

func (grokConfig) Remove(document format.Document, key string) {
	toml, ok := document.(*format.TOMLDocument)
	if !ok {
		return
	}
	parent, name := "model", ""
	if id, entry := strings.CutPrefix(key, grokEntryPrefix); entry {
		name = id
	} else if inner, inModels := strings.CutPrefix(key, "models."); inModels {
		parent, name = "models", inner
	} else {
		document.Remove(key)
		return
	}
	if table := format.TableLike(toml.Root().Get(parent)); table != nil {
		table.Remove(name)
	}
	toml.DropEmptyParent(parent)
}

type grokOwned struct {
	id    string
	entry map[string]any
}

// grokOwnedEntries are AstrLink's model tables, by model id.
func grokOwnedEntries(config *cc.Config) []grokOwned {
	toml, ok := config.Documents[0].(*format.TOMLDocument)
	if !ok {
		return nil
	}
	models := format.TableLike(toml.Root().Get("model"))
	if models == nil {
		return nil
	}
	var owned []grokOwned
	for _, id := range models.Names() {
		value, _ := format.TOMLJSON(models.Get(id))
		entry, _ := value.(map[string]any)
		if mark, _ := entry["description"].(string); mark == grokMark {
			owned = append(owned, grokOwned{id, entry})
		}
	}
	return owned
}

// grokCommunityOnly reports whether `~/.grok` holds only the community
// grok-cli: its installer leaves `install.json` naming its repository, and
// its settings file is `user-settings.json`.
func grokCommunityOnly(dir string) bool {
	if isFile(filepath.Join(dir, "config.toml")) || cc.IsDir(filepath.Join(dir, "downloads")) {
		return false
	}
	install, err := os.ReadFile(filepath.Join(dir, "install.json"))
	return err == nil && strings.Contains(string(install), "superagent-ai/grok-cli") ||
		isFile(filepath.Join(dir, "user-settings.json"))
}

func (grokBuild) ID() string   { return "grok_build" }
func (grokBuild) Name() string { return "Grok Build" }
func (grokBuild) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}
func (grokBuild) ListsModels() bool { return true }

// Dir is `~/.grok`, or where CC Switch's settings move it; `GROK_HOME` from
// a shell is out of reach.
func (grokBuild) Dir(home agents.Home, _ string) string { return home.GrokDir() }

func (g grokBuild) Detected(home agents.Home, target string) bool {
	dir := g.Dir(home, target)
	return cc.IsDir(dir) && !grokCommunityOnly(dir)
}

func (grokBuild) Files() []cc.File { return []cc.File{grokConfig{}} }

func (g grokBuild) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	return []string{filepath.Join(g.Dir(home, target), "config.toml")}
}

func (grokBuild) ConnectionKeys() []string { return nil }

// HoldsConnection: each model table holds an address and the token.
func (grokBuild) HoldsConnection(key string) bool { return strings.HasPrefix(key, grokEntryPrefix) }

func (grokBuild) CurrentModels(config *cc.Config) cc.CurrentModels {
	entries := grokOwnedEntries(config)
	var ids []string
	for _, owned := range entries {
		model, _ := owned.entry["model"].(string)
		if id, ok := cc.CleanModel(model); ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	var models cc.CurrentModels
	if name, ok := config.String(grokDefaultKey); ok {
		if at := slices.IndexFunc(entries, func(owned grokOwned) bool { return owned.id == name }); at >= 0 {
			if model, ok := entries[at].entry["model"].(string); ok {
				models.Model = cc.Some(model)
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

func (grokBuild) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	var keys []cc.Setting
	for _, id := range connection.ModelList() {
		entry := grokEntry(id, connection.Protocol(GrokBuild, id), connection, details)
		keys = append(keys, cc.Setting{Key: grokEntryPrefix + id, Value: format.Encode(entry)})
	}
	if model, ok := connection.Model("model"); ok {
		keys = append(keys, cc.Setting{Key: grokDefaultKey, Value: model}, cc.Setting{Key: grokTitleKey, Value: model})
	}
	return keys
}

// BaseURLKey is unused: SyncedKeys points every table elsewhere.
func (grokBuild) BaseURLKey() string { return "" }

func (grokBuild) SyncedKeys(config *cc.Config, record *cc.Record, origin string) []cc.Setting {
	var keys []cc.Setting
	for _, key := range sortedKeys(record.Keys) {
		if !strings.HasPrefix(key, grokEntryPrefix) {
			continue
		}
		current, ok := config.String(key)
		if !ok {
			continue
		}
		decoded, err := format.DecodeJSON(current)
		entry, isObject := decoded.(map[string]any)
		if err != nil || !isObject {
			continue
		}
		entry["base_url"] = origin + "/v1"
		keys = append(keys, cc.Setting{Key: key, Value: format.Encode(entry)})
	}
	return keys
}
