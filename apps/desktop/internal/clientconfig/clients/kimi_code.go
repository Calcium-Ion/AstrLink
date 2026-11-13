package clients

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Kimi Code reads `~/.kimi-code/config.toml`. AstrLink adds one provider
// table per protocol its models use, an `astrlink/<model>` alias table for
// each model, and picks the default with `default_model`.

var KimiCode = cc.New(kimiCode{})

type kimiCode struct{}

// kimiConfig is `config.toml`.
type kimiConfig struct{}

// kimiProviders are each protocol's provider id and `type`.
var kimiProviders = []struct{ protocol, id, kind string }{
	{"openai.chat", "astrlink-chat", "openai"},
	{"anthropic.messages", "astrlink-anthropic", "anthropic"},
	{"openai.responses", "astrlink-responses", "openai_responses"},
	{"google.generate_content", "astrlink-gemini", "google-genai"},
}

const (
	kimiProviderPrefix = "providers."
	// kimiModelsKey is every `astrlink/<model>` alias table, as one JSON
	// object by alias.
	kimiModelsKey   = `models."astrlink/*"`
	kimiAliasPrefix = "astrlink/"
	kimiDefaultKey  = "default_model"
	// kimiDefaultContext is what Kimi takes for a model of unknown
	// context: a size is required.
	kimiDefaultContext = 131_072
)

var kimiEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// kimiProviderURL: Anthropic and Gemini take the bare origin; OpenAI's APIs
// `/v1`.
func kimiProviderURL(id, origin string) string {
	if id == "astrlink-anthropic" || id == "astrlink-gemini" {
		return origin
	}
	return origin + "/v1"
}

func kimiRank(name string) int {
	order := []string{"type", "base_url", "api_key", "provider", "model", "max_context_size", "max_output_size", "capabilities", "support_efforts", "off_effort"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

func kimiAliasEntry(id, provider string, details cc.Catalog) map[string]any {
	d, ok := details[id]
	context := uint64(kimiDefaultContext)
	if ok && d.ContextWindow != nil {
		context = *d.ContextWindow
	}
	entry := map[string]any{"provider": provider, "model": id, "max_context_size": number(context)}
	if !ok {
		return entry
	}
	if d.MaxTokens != nil {
		entry["max_output_size"] = number(*d.MaxTokens)
	}
	if len(d.Reasoning) > 0 {
		entry["capabilities"] = []any{"thinking"}
		var efforts []any
		for _, effort := range kimiEfforts {
			if slices.Contains(d.Reasoning, effort) {
				efforts = append(efforts, effort)
			}
		}
		if len(efforts) > 0 {
			entry["support_efforts"] = efforts
		}
		if slices.Contains(d.Reasoning, "none") {
			entry["off_effort"] = "none"
		}
	}
	return entry
}

func (kimiConfig) Format() format.Format { return format.TOMLFormat{} }

func (kimiConfig) Get(document format.Document, key string) (string, bool, bool) {
	toml, ok := document.(*format.TOMLDocument)
	if !ok {
		return document.Get(key)
	}
	if key == kimiModelsKey {
		aliases, ok := toml.PrefixedTables("models", kimiAliasPrefix)
		if !ok {
			return "", false, false
		}
		return format.Encode(aliases), true, true
	}
	id, provider := strings.CutPrefix(key, kimiProviderPrefix)
	if !provider {
		return document.Get(key)
	}
	return getTable(toml, "providers", id)
}

func (kimiConfig) Set(document format.Document, key, value string) error {
	toml, ok := document.(*format.TOMLDocument)
	id, provider := strings.CutPrefix(key, kimiProviderPrefix)
	if !ok || key != kimiModelsKey && !provider {
		return document.Set(key, value)
	}
	if key == kimiModelsKey {
		decoded, err := format.DecodeJSON(value)
		if err != nil {
			return err
		}
		return toml.SetPrefixed("models", kimiAliasPrefix, decoded, kimiRank)
	}
	return setTable(toml, "providers", id, key, value, kimiRank)
}

func (kimiConfig) Remove(document format.Document, key string) {
	toml, ok := document.(*format.TOMLDocument)
	if !ok {
		return
	}
	if key == kimiModelsKey {
		toml.RemovePrefixed("models", kimiAliasPrefix)
	} else if id, provider := strings.CutPrefix(key, kimiProviderPrefix); provider {
		removeTable(toml, "providers", id)
	} else {
		document.Remove(key)
	}
}

func (kimiCode) ID() string   { return "kimi_code" }
func (kimiCode) Name() string { return "Kimi Code" }
func (kimiCode) Protocols() []string {
	return []string{"openai.chat", "anthropic.messages", "openai.responses", "google.generate_content"}
}
func (kimiCode) ListsModels() bool { return true }

// Dir is the default `~/.kimi-code`: `KIMI_CODE_HOME` moves it from a
// shell, but AstrLink writes the default.
func (kimiCode) Dir(home agents.Home, _ string) string {
	return filepath.Join(home.Dir(), ".kimi-code")
}

func (kimiCode) Files() []cc.File { return []cc.File{kimiConfig{}} }

func (k kimiCode) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	return []string{filepath.Join(k.Dir(home, target), "config.toml")}
}

func (kimiCode) ConnectionKeys() []string { return nil }

// HoldsConnection: each provider table holds an address and the token.
func (kimiCode) HoldsConnection(key string) bool { return strings.HasPrefix(key, kimiProviderPrefix) }

func (kimiCode) CurrentModels(config *cc.Config) cc.CurrentModels {
	aliases := map[string]any{}
	if value, ok := config.String(kimiModelsKey); ok {
		if decoded, err := format.DecodeJSON(value); err == nil {
			if object, ok := decoded.(map[string]any); ok {
				aliases = object
			}
		}
	}
	var ids []string
	for _, alias := range format.SortedKeys(aliases) {
		model, _ := kimiModelOf(aliases[alias])
		if id, ok := cc.CleanModel(model); ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	var models cc.CurrentModels
	if alias, ok := config.String(kimiDefaultKey); ok {
		if model, ok := kimiModelOf(aliases[alias]); ok {
			models.Model = cc.Some(model)
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

// kimiModelOf is the model an alias table names.
func kimiModelOf(alias any) (string, bool) {
	table, _ := alias.(map[string]any)
	model, ok := table["model"].(string)
	return model, ok
}

func (kimiCode) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	list := connection.ModelList()
	var keys []cc.Setting
	aliases := map[string]any{}
	for _, provider := range kimiProviders {
		var models []string
		for _, model := range list {
			if connection.Protocol(KimiCode, model) == provider.protocol {
				models = append(models, model)
			}
		}
		if len(models) == 0 {
			continue
		}
		table := map[string]any{
			"type":     provider.kind,
			"base_url": kimiProviderURL(provider.id, connection.Origin),
			"api_key":  connection.Token,
		}
		keys = append(keys, cc.Setting{Key: kimiProviderPrefix + provider.id, Value: format.Encode(table)})
		for _, model := range models {
			aliases[kimiAliasPrefix+model] = kimiAliasEntry(model, provider.id, details)
		}
	}
	if len(aliases) > 0 {
		keys = append(keys, cc.Setting{Key: kimiModelsKey, Value: format.Encode(aliases)})
	}
	if model, ok := connection.Model("model"); ok {
		keys = append(keys, cc.Setting{Key: kimiDefaultKey, Value: kimiAliasPrefix + model})
	}
	return keys
}

// BaseURLKey is unused: SyncedKeys points every provider elsewhere.
func (kimiCode) BaseURLKey() string { return "" }

func (kimiCode) SyncedKeys(config *cc.Config, record *cc.Record, origin string) []cc.Setting {
	var keys []cc.Setting
	for _, key := range sortedKeys(record.Keys) {
		id, ok := strings.CutPrefix(key, kimiProviderPrefix)
		if !ok {
			continue
		}
		current, ok := config.String(key)
		if !ok {
			continue
		}
		decoded, err := format.DecodeJSON(current)
		table, isObject := decoded.(map[string]any)
		if err != nil || !isObject {
			continue
		}
		table["base_url"] = kimiProviderURL(id, origin)
		keys = append(keys, cc.Setting{Key: key, Value: format.Encode(table)})
	}
	return keys
}
