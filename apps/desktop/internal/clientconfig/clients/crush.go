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

// Crush reads `~/.config/crush/crush.json`. A provider has one `type` and
// one `base_url`, and Anthropic's takes no `/v1`, so AstrLink adds a
// provider per protocol its models use, each listing its models, and picks
// the large and small models. A model picked in Crush is kept in its data
// file, which overrides this one.

var Crush = cc.New(crush{})

type crush struct{}

// crushConfig is `crush.json`.
type crushConfig struct{}

type crushRoute struct{ protocol, id, kind string }

// crushRoutes are each protocol's provider id and `type`.
var crushRoutes = []crushRoute{
	{"openai.chat", "local-chat", "completions"},
	{"openai.responses", "local-responses", "responses"},
	{"anthropic.messages", "local-anthropic", "messages"},
}

const (
	crushProviderPrefix = "providers."
	crushLargeKey       = "models.large"
	crushSmallKey       = "models.small"
	crushSchema         = "https://charm.land/crush.json"
	// What Crush's schema requires for a model AstrLink knows no limits
	// of.
	crushDefaultContext = 128_000
	crushDefaultOutput  = 16_000
)

func crushRouteOf(protocol string) crushRoute {
	if i := slices.IndexFunc(crushRoutes, func(r crushRoute) bool { return r.protocol == protocol }); i >= 0 {
		return crushRoutes[i]
	}
	return crushRoutes[0]
}

func crushOurs(id string) bool {
	return slices.ContainsFunc(crushRoutes, func(r crushRoute) bool { return r.id == id })
}

// crushProviderURL: Anthropic's SDK adds `/v1` itself; the OpenAI ones take
// it.
func crushProviderURL(kind, origin string) string {
	if kind == "messages" {
		return origin
	}
	return origin + "/v1"
}

func crushRank(name string) int {
	order := []string{"id", "name", "type", "base_url", "api_key", "context_window", "default_max_tokens", "models", "provider"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

// crushDataFile is Crush's data file, where a model picked in Crush is
// kept.
func crushDataFile(home agents.Home) string {
	if home.Windows() {
		return home.Join("AppData", "Local", "crush", "crush.json")
	}
	return home.Join(".local", "share", "crush", "crush.json")
}

func (crush) ID() string   { return "crush" }
func (crush) Name() string { return "Crush" }
func (crush) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}
func (crush) ListsModels() bool { return true }

// Dir: `XDG_CONFIG_HOME` and `CRUSH_GLOBAL_CONFIG` from a shell are out of
// reach.
func (crush) Dir(home agents.Home, _ string) string { return home.Join(".config", "crush") }

// Detected: a first run makes the data directory, not the config one.
func (c crush) Detected(home agents.Home, target string) bool {
	return cc.IsDir(c.Dir(home, target)) || cc.IsDir(filepath.Dir(crushDataFile(home)))
}

func (crush) Files() []cc.File { return []cc.File{crushConfig{}} }

func (c crush) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	return []string{filepath.Join(c.Dir(home, target), "crush.json")}
}

func (crush) ConnectionKeys() []string { return nil }

// HoldsConnection: each provider holds an address and the token.
func (crush) HoldsConnection(key string) bool {
	id, ok := strings.CutPrefix(key, crushProviderPrefix)
	return ok && crushOurs(id)
}

func (crush) CurrentModels(config *cc.Config) cc.CurrentModels {
	var ids []string
	for _, route := range crushRoutes {
		entry, ok := configObject(config, crushProviderPrefix+route.id)
		if !ok {
			continue
		}
		models, _ := entry["models"].([]any)
		for _, model := range models {
			id, _ := field(model, "id")
			if id, ok := cc.CleanModel(id); ok && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	var models cc.CurrentModels
	if large, ok := configObject(config, crushLargeKey); ok {
		provider, _ := field(large, "provider")
		if model, ok := field(large, "model"); ok && crushOurs(provider) {
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

func (crush) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	list := connection.ModelList()
	var keys []cc.Setting
	for _, route := range crushRoutes {
		var models []any
		for _, model := range list {
			if connection.Protocol(Crush, model) != route.protocol {
				continue
			}
			context, output := uint64(crushDefaultContext), uint64(crushDefaultOutput)
			if d, ok := details[model]; ok {
				if d.ContextWindow != nil {
					context = *d.ContextWindow
				}
				if d.MaxTokens != nil {
					output = *d.MaxTokens
				}
			}
			models = append(models, map[string]any{
				"id": model, "name": model, "context_window": number(context), "default_max_tokens": number(output),
			})
		}
		if len(models) == 0 {
			continue
		}
		provider := map[string]any{
			"name":     "AstrLink",
			"type":     route.kind,
			"base_url": crushProviderURL(route.kind, connection.Origin),
			"api_key":  connection.Token,
			"models":   models,
		}
		keys = append(keys, cc.Setting{Key: crushProviderPrefix + route.id, Value: format.Encode(provider)})
	}
	if model, ok := connection.Model("model"); ok {
		route := crushRouteOf(connection.Protocol(Crush, model))
		picked := format.Encode(map[string]any{"provider": route.id, "model": model})
		keys = append(keys, cc.Setting{Key: crushLargeKey, Value: picked}, cc.Setting{Key: crushSmallKey, Value: picked})
	}
	return keys
}

func (crush) StartFile(config *cc.Config) *cc.FileError { return config.Set("$schema", crushSchema) }

// BaseURLKey is unused: SyncedKeys points every provider elsewhere.
func (crush) BaseURLKey() string { return "" }

func (c crush) SyncedKeys(config *cc.Config, record *cc.Record, origin string) []cc.Setting {
	var keys []cc.Setting
	for _, key := range sortedKeys(record.Keys) {
		if !c.HoldsConnection(key) {
			continue
		}
		provider, ok := configObject(config, key)
		if !ok {
			continue
		}
		kind, ok := field(provider, "type")
		if !ok {
			continue
		}
		provider["base_url"] = crushProviderURL(kind, origin)
		keys = append(keys, cc.Setting{Key: key, Value: format.Encode(provider)})
	}
	return keys
}

// Notes: a large model picked in Crush, other than one of AstrLink's, wins.
func (crush) Notes(home agents.Home, _ string, _ *cc.Config) []string {
	text, err := os.ReadFile(crushDataFile(home))
	if err != nil {
		return nil
	}
	data, err := format.DecodeJSON(string(text))
	if err != nil {
		return nil
	}
	object, _ := data.(map[string]any)
	models, _ := object["models"].(map[string]any)
	if provider, ok := field(models["large"], "provider"); ok && !crushOurs(provider) {
		return []string{"overridden"}
	}
	return nil
}

func (crushConfig) Format() format.Format {
	return format.JSONFormat{Syntax: &format.JSONC, Objects: []string{"providers", "models"}}
}

func crushWhole(key string) bool {
	return strings.HasPrefix(key, crushProviderPrefix) || key == crushLargeKey || key == crushSmallKey
}

func (crushConfig) Get(document format.Document, key string) (string, bool, bool) {
	json, ok := document.(*format.JSONDocument)
	if !ok || !crushWhole(key) {
		return document.Get(key)
	}
	return getWhole(json, key)
}

func (crushConfig) Set(document format.Document, key, value string) error {
	json, ok := document.(*format.JSONDocument)
	if !ok || !crushWhole(key) {
		return document.Set(key, value)
	}
	return setWhole(json, key, value, crushRank)
}

func (crushConfig) Emptied() []string { return []string{"providers", "models"} }

// IsEmpty lets the schema AstrLink named in a file it created go with the
// rest.
func (crushConfig) IsEmpty(document format.Document) bool {
	if json, ok := document.(*format.JSONDocument); ok {
		return json.HoldsOnly(func(p format.JSONProperty) bool { return p.HasName && p.Name == "$schema" })
	}
	return document.IsEmpty()
}
