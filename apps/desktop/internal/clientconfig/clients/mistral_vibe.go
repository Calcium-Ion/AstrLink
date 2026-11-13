package clients

import (
	"path/filepath"
	"slices"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Mistral Vibe reads `~/.vibe/config.toml`: `[[providers]]` with one
// `api_style` each, `[[models]]` naming their provider, and the
// `active_model` alias. A provider's key comes from the environment
// variable it names, which Vibe fills from `~/.vibe/.env` before the
// keychain, so the token goes there. AstrLink adds a provider per protocol
// its models use, all reading one variable.

var MistralVibe = cc.New(mistralVibe{})

type mistralVibe struct{}

// vibeConfig is `config.toml`.
type vibeConfig struct{}

// vibeEnv is `.env`.
type vibeEnv struct{}

// vibeRoutes are each protocol's provider name and `api_style`.
var vibeRoutes = []struct{ protocol, name, style string }{
	{"openai.chat", "local-chat", "openai"},
	{"openai.responses", "local-responses", "openai-responses"},
	{"anthropic.messages", "local-anthropic", "anthropic"},
}

const (
	vibeProvidersKey = "providers"
	vibeModelsKey    = "models"
	vibeActiveKey    = "active_model"
	vibeKeyName      = "LOCAL_GATEWAY_API_KEY"
)

func vibeOurs(name string) bool {
	for _, route := range vibeRoutes {
		if route.name == name {
			return true
		}
	}
	return false
}

// vibeAPIBase: Vibe adds `/v1/messages` to an Anthropic base, and the rest
// to the others.
func vibeAPIBase(style, origin string) string {
	if style == "anthropic" {
		return origin
	}
	return origin + "/v1"
}

func vibeRank(name string) int {
	order := []string{"name", "provider", "alias", "api_base", "api_key_env_var", "api_style", "backend", "max_context_length", "max_output_tokens", "thinking"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

// vibeOwnedIn picks AstrLink's entries in list, by the field that names
// their provider.
func vibeOwnedIn(list string) func(any) bool {
	name := "provider"
	if list == vibeProvidersKey {
		name = "name"
	}
	return func(entry any) bool {
		provider, ok := field(entry, name)
		return ok && vibeOurs(provider)
	}
}

func (mistralVibe) ID() string        { return "mistral_vibe" }
func (mistralVibe) Name() string      { return "Mistral Vibe" }
func (mistralVibe) ListsModels() bool { return true }
func (mistralVibe) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}

// Dir: `VIBE_HOME` from a shell is out of reach.
func (mistralVibe) Dir(home agents.Home, _ string) string { return home.Join(".vibe") }

func (mistralVibe) Files() []cc.File { return []cc.File{vibeConfig{}, vibeEnv{}} }

func (m mistralVibe) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	dir := m.Dir(home, target)
	return []string{filepath.Join(dir, "config.toml"), filepath.Join(dir, ".env")}
}

func (mistralVibe) ConnectionKeys() []string { return []string{vibeKeyName} }

// HoldsConnection: the providers hold the addresses; `.env` the token.
func (mistralVibe) HoldsConnection(key string) bool {
	return key == vibeProvidersKey || key == vibeKeyName
}

func (mistralVibe) CurrentModels(config *cc.Config) cc.CurrentModels {
	entries := configList(config, vibeModelsKey)
	var ids []string
	for _, entry := range entries {
		name, _ := field(entry, "name")
		if id, ok := cc.CleanModel(name); ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	var models cc.CurrentModels
	if alias, ok := config.String(vibeActiveKey); ok {
		for _, entry := range entries {
			object, _ := entry.(map[string]any)
			named, present := object["alias"]
			if !present {
				named = object["name"]
			}
			if named == alias {
				if name, ok := field(entry, "name"); ok {
					models.Model = cc.Some(name)
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

func (mistralVibe) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	list := connection.ModelList()
	var providers, models []any
	for _, route := range vibeRoutes {
		var used []string
		for _, id := range list {
			if connection.Protocol(MistralVibe, id) == route.protocol {
				used = append(used, id)
			}
		}
		if len(used) == 0 {
			continue
		}
		providers = append(providers, map[string]any{
			"name":            route.name,
			"api_base":        vibeAPIBase(route.style, connection.Origin),
			"api_key_env_var": vibeKeyName,
			"api_style":       route.style,
			"backend":         "generic",
		})
		for _, id := range used {
			model := map[string]any{"name": id, "provider": route.name, "alias": id}
			if d, ok := details[id]; ok {
				if d.ContextWindow != nil {
					model["max_context_length"] = number(*d.ContextWindow)
				}
				if d.MaxTokens != nil {
					model["max_output_tokens"] = number(*d.MaxTokens)
				}
			}
			models = append(models, model)
		}
	}
	var keys []cc.Setting
	if model, ok := connection.Model("model"); ok {
		keys = append(keys, cc.Setting{Key: vibeActiveKey, Value: model})
	}
	if len(providers) > 0 {
		keys = append(keys,
			cc.Setting{Key: vibeProvidersKey, Value: format.Encode(providers)},
			cc.Setting{Key: vibeModelsKey, Value: format.Encode(models)})
	}
	return append(keys, cc.Setting{Key: vibeKeyName, Value: connection.Token})
}

// BaseURLKey is unused: SyncedKeys points every provider elsewhere.
func (mistralVibe) BaseURLKey() string { return "" }

func (mistralVibe) SyncedKeys(config *cc.Config, _ *cc.Record, origin string) []cc.Setting {
	providers := configList(config, vibeProvidersKey)
	if len(providers) == 0 {
		return nil
	}
	for _, provider := range providers {
		if object, ok := provider.(map[string]any); ok {
			style, _ := object["api_style"].(string)
			object["api_base"] = vibeAPIBase(style, origin)
		}
	}
	return []cc.Setting{{Key: vibeProvidersKey, Value: format.Encode(providers)}}
}

func (vibeConfig) Format() format.Format { return format.TOMLFormat{} }

func (vibeConfig) Holds(key string) bool { return key != vibeKeyName }

// Get reads a list as AstrLink's tables in it.
func (vibeConfig) Get(document format.Document, key string) (string, bool, bool) {
	toml, ok := document.(*format.TOMLDocument)
	if !ok || key != vibeProvidersKey && key != vibeModelsKey {
		return document.Get(key)
	}
	return getOwnedTables(toml, key, vibeOwnedIn(key))
}

// Set puts AstrLink's tables after the user's.
func (vibeConfig) Set(document format.Document, key, value string) error {
	toml, ok := document.(*format.TOMLDocument)
	if !ok || key != vibeProvidersKey && key != vibeModelsKey {
		return document.Set(key, value)
	}
	return setOwnedTables(toml, key, value, vibeOwnedIn(key), vibeRank)
}

func (vibeConfig) Remove(document format.Document, key string) {
	toml, ok := document.(*format.TOMLDocument)
	if !ok || key != vibeProvidersKey && key != vibeModelsKey {
		document.Remove(key)
		return
	}
	dropOwnedTables(toml, key, vibeOwnedIn(key))
}

func (vibeEnv) Format() format.Format { return format.EnvFormat{} }

func (vibeEnv) Secret() bool { return true }

func (vibeEnv) Holds(key string) bool { return key == vibeKeyName }
