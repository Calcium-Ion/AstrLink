package clients

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// MiniMax Code, CLI and desktop app alike, reads `~/.minimax/config.yaml`.
// A custom provider has one `api`, so AstrLink adds one per protocol its
// models use under `custom_provider`, and picks the default in
// `defaultModel`. Changes apply on the next start.

var MiniMaxCode = cc.New(miniMaxCode{})

type miniMaxCode struct{}

// miniMaxConfig is `config.yaml`.
type miniMaxConfig struct{}

// miniMaxRoutes are each protocol's provider key, the name MiniMax Code
// shows, and `api`.
var miniMaxRoutes = []struct{ protocol, id, name, api string }{
	{"openai.chat", "local-chat", "AstrLink", "openai-completions"},
	{"openai.responses", "local-responses", "AstrLink Responses", "openai-responses"},
	{"anthropic.messages", "local-anthropic", "AstrLink Anthropic", "anthropic-messages"},
}

const (
	miniMaxParent     = "custom_provider"
	miniMaxPrefix     = "custom_provider."
	miniMaxDefaultKey = "defaultModel"
	// miniMaxChoicePrefix is what a `defaultModel` naming a custom provider
	// starts with.
	miniMaxChoicePrefix = "custom_provider:"
	// What MiniMax Code assumes of a model it is told no limits of.
	miniMaxDefaultContext = 200_000
	miniMaxDefaultOutput  = 16_384
)

// miniMaxProviderOf is the provider AstrLink owns at key.
func miniMaxProviderOf(key string) (string, bool) {
	id, ok := strings.CutPrefix(key, miniMaxPrefix)
	if !ok {
		return "", false
	}
	for _, route := range miniMaxRoutes {
		if route.id == id {
			return id, true
		}
	}
	return "", false
}

// miniMaxURL: MiniMax Code adds `/v1/messages` to an Anthropic URL, and the
// rest to the others.
func miniMaxURL(api, origin string) string {
	if api == "anthropic-messages" {
		return origin
	}
	return origin + "/v1"
}

func miniMaxRank(name string) int {
	order := []string{"name", "kind", "enabled", "api", "options", "baseURL", "apiKey", "models", "reasoning", "thinking", "effortOptions", "limit", "context", "output"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

func miniMaxModel(id string, details cc.Catalog) map[string]any {
	entry := map[string]any{"name": id, "enabled": true}
	d, ok := details[id]
	if !ok {
		return entry
	}
	if len(d.Reasoning) > 0 {
		levels := make([]any, len(d.Reasoning))
		for i, level := range d.Reasoning {
			levels[i] = level
		}
		entry["reasoning"] = true
		entry["thinking"] = map[string]any{"effortOptions": levels}
	}
	if d.ContextWindow != nil || d.MaxTokens != nil {
		context, output := uint64(miniMaxDefaultContext), uint64(miniMaxDefaultOutput)
		if d.ContextWindow != nil {
			context = *d.ContextWindow
		}
		if d.MaxTokens != nil {
			output = *d.MaxTokens
		}
		entry["limit"] = map[string]any{"context": number(context), "output": number(output)}
	}
	return entry
}

func (miniMaxCode) ID() string        { return "minimax_code" }
func (miniMaxCode) Name() string      { return "MiniMax Code" }
func (miniMaxCode) ListsModels() bool { return true }
func (miniMaxCode) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}

// Dir: `MINIMAX_DATA_DIR` and a profile's own folder are out of reach.
func (miniMaxCode) Dir(home agents.Home, _ string) string { return home.Join(".minimax") }

func (miniMaxCode) Files() []cc.File { return []cc.File{miniMaxConfig{}} }

func (m miniMaxCode) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	return []string{filepath.Join(m.Dir(home, target), "config.yaml")}
}

func (miniMaxCode) ConnectionKeys() []string { return nil }

// HoldsConnection: each provider holds an address and the token.
func (miniMaxCode) HoldsConnection(key string) bool {
	_, ok := miniMaxProviderOf(key)
	return ok
}

func (miniMaxCode) CurrentModels(config *cc.Config) cc.CurrentModels {
	var ids []string
	for _, route := range miniMaxRoutes {
		provider, ok := configObject(config, miniMaxPrefix+route.id)
		if !ok {
			continue
		}
		listed, _ := provider["models"].(map[string]any)
		for _, name := range format.SortedKeys(listed) {
			if id, ok := cc.CleanModel(name); ok && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	var models cc.CurrentModels
	if chosen, ok := config.String(miniMaxDefaultKey); ok {
		if rest, ok := strings.CutPrefix(chosen, miniMaxChoicePrefix); ok {
			if provider, model, ok := strings.Cut(rest, "/"); ok {
				if _, ours := miniMaxProviderOf(miniMaxPrefix + provider); ours {
					if model, ok := cc.CleanModel(model); ok {
						models.Model = &model
					}
				}
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

func (miniMaxCode) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	list := connection.ModelList()
	var keys []cc.Setting
	for _, route := range miniMaxRoutes {
		models := map[string]any{}
		for _, model := range list {
			if connection.Protocol(MiniMaxCode, model) == route.protocol {
				models[model] = miniMaxModel(model, details)
			}
		}
		if len(models) == 0 {
			continue
		}
		provider := map[string]any{
			"name":    route.name,
			"kind":    "custom",
			"enabled": true,
			"api":     route.api,
			"options": map[string]any{"baseURL": miniMaxURL(route.api, connection.Origin), "apiKey": connection.Token},
			"models":  models,
		}
		keys = append(keys, cc.Setting{Key: miniMaxPrefix + route.id, Value: format.Encode(provider)})
	}
	if model, ok := connection.Model("model"); ok {
		protocol := connection.Protocol(MiniMaxCode, model)
		for _, route := range miniMaxRoutes {
			if route.protocol == protocol {
				keys = append(keys, cc.Setting{Key: miniMaxDefaultKey, Value: miniMaxChoicePrefix + route.id + "/" + model})
				break
			}
		}
	}
	return keys
}

// BaseURLKey is unused: SyncedKeys points every provider elsewhere.
func (miniMaxCode) BaseURLKey() string { return "" }

func (miniMaxCode) SyncedKeys(config *cc.Config, record *cc.Record, origin string) []cc.Setting {
	var keys []cc.Setting
	for _, key := range slices.Sorted(maps.Keys(record.Keys)) {
		if _, ok := miniMaxProviderOf(key); !ok {
			continue
		}
		provider, ok := configObject(config, key)
		if !ok {
			continue
		}
		api, ok := provider["api"].(string)
		if !ok {
			continue
		}
		options, ok := provider["options"].(map[string]any)
		if !ok {
			options = map[string]any{}
			provider["options"] = options
		}
		options["baseURL"] = miniMaxURL(api, origin)
		keys = append(keys, cc.Setting{Key: key, Value: format.Encode(provider)})
	}
	return keys
}

func (miniMaxConfig) Format() format.Format { return format.YAMLFormat{Root: format.YAMLMapping} }

// Secret: MiniMax Code keeps the file to its owner.
func (miniMaxConfig) Secret() bool { return true }

// Get reads a provider as one JSON object.
func (miniMaxConfig) Get(document format.Document, key string) (string, bool, bool) {
	yaml := format.AsYAML(document)
	id, ok := miniMaxProviderOf(key)
	if yaml == nil || !ok {
		return document.Get(key)
	}
	value, ok := yaml.GetJSON(format.YAMLKey(miniMaxParent), format.YAMLKey(id))
	if !ok {
		return "", false, false
	}
	return format.Encode(value), true, true
}

func (miniMaxConfig) Set(document format.Document, key, value string) error {
	yaml := format.AsYAML(document)
	id, ok := miniMaxProviderOf(key)
	if yaml == nil || !ok {
		return document.Set(key, value)
	}
	decoded, err := format.DecodeJSON(value)
	if err != nil {
		return err
	}
	if _, ok := yaml.Get(format.YAMLKey(miniMaxParent)); ok {
		return yaml.Set([]format.YAMLStep{format.YAMLKey(miniMaxParent)}, id, decoded, miniMaxRank)
	}
	return yaml.Set(nil, miniMaxParent, map[string]any{id: decoded}, miniMaxRank)
}

// Remove drops an emptied `custom_provider`.
func (miniMaxConfig) Remove(document format.Document, key string) {
	yaml := format.AsYAML(document)
	id, ok := miniMaxProviderOf(key)
	if yaml == nil || !ok {
		document.Remove(key)
		return
	}
	_ = yaml.Remove(format.YAMLKey(miniMaxParent), format.YAMLKey(id))
	if value, ok := yaml.GetJSON(format.YAMLKey(miniMaxParent)); ok {
		if object, isObject := value.(map[string]any); isObject && len(object) == 0 {
			_ = yaml.Remove(format.YAMLKey(miniMaxParent))
		}
	}
}
