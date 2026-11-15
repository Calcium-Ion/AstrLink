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

// Step Code reads its providers from `~/.stepcode/models.json`, Pi's
// format, and the model it starts with from `~/.stepcode/config.toml`. A
// provider has one `api`, so AstrLink adds one per protocol its models use.

var StepCode = cc.New(stepCode{})

type stepCode struct{}

// stepModels is `models.json`.
type stepModels struct{}

// stepConfig is `config.toml`.
type stepConfig struct{}

// stepRoutes are each protocol's provider id and `api`.
var stepRoutes = []struct{ protocol, id, api string }{
	{"openai.chat", "local-chat", "openai-completions"},
	{"openai.responses", "local-responses", "openai-responses"},
	{"anthropic.messages", "local-anthropic", "anthropic-messages"},
}

const stepPrefix = "providers."

var stepStartupKeys = []string{"defaultProvider", "defaultModel"}

func stepOurs(id string) bool {
	for _, route := range stepRoutes {
		if route.id == id {
			return true
		}
	}
	return false
}

// stepURL: Step Code adds `/v1/messages` to an Anthropic URL, and the rest
// to the others.
func stepURL(api, origin string) string {
	if api == "anthropic-messages" {
		return origin
	}
	return origin + "/v1"
}

func stepRank(name string) int {
	order := []string{"baseUrl", "api", "apiKey", "models", "id", "reasoning", "contextWindow", "maxTokens"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

func (stepCode) ID() string        { return "step_code" }
func (stepCode) Name() string      { return "Step Code" }
func (stepCode) ListsModels() bool { return true }
func (stepCode) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}

// Dir: `STEPCODE_CONFIG_DIR` and `STEP_CODING_AGENT_DIR` from a shell are
// out of reach.
func (stepCode) Dir(home agents.Home, _ string) string { return home.Join(".stepcode") }

func (stepCode) Files() []cc.File { return []cc.File{stepModels{}, stepConfig{}} }

func (s stepCode) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	dir := s.Dir(home, target)
	return []string{filepath.Join(dir, "models.json"), filepath.Join(dir, "config.toml")}
}

func (stepCode) ConnectionKeys() []string { return nil }

// HoldsConnection: each provider holds an address and the token.
func (stepCode) HoldsConnection(key string) bool {
	id, ok := strings.CutPrefix(key, stepPrefix)
	return ok && stepOurs(id)
}

func (stepCode) CurrentModels(config *cc.Config) cc.CurrentModels {
	var ids []string
	for _, route := range stepRoutes {
		provider, ok := configObject(config, stepPrefix+route.id)
		if !ok {
			continue
		}
		listed, _ := provider["models"].([]any)
		for _, model := range listed {
			name, _ := field(model, "id")
			if id, ok := cc.CleanModel(name); ok && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	var models cc.CurrentModels
	if provider, ok := config.String(stepStartupKeys[0]); ok && stepOurs(provider) {
		if model, ok := config.Model(stepStartupKeys[1]); ok {
			models.Model = &model
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

func (stepCode) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	list := connection.ModelList()
	var keys []cc.Setting
	for _, route := range stepRoutes {
		var models []any
		for _, model := range list {
			if connection.Protocol(StepCode, model) != route.protocol {
				continue
			}
			entry := map[string]any{"id": model}
			if d, ok := details[model]; ok {
				// Pi takes a model it is not told reasons as one that
				// cannot.
				if len(d.Reasoning) > 0 {
					entry["reasoning"] = true
				}
				if d.ContextWindow != nil {
					entry["contextWindow"] = number(*d.ContextWindow)
				}
				if d.MaxTokens != nil {
					entry["maxTokens"] = number(*d.MaxTokens)
				}
			}
			models = append(models, entry)
		}
		if len(models) == 0 {
			continue
		}
		provider := map[string]any{
			"baseUrl": stepURL(route.api, connection.Origin),
			"api":     route.api,
			"apiKey":  connection.Token,
			"models":  models,
		}
		keys = append(keys, cc.Setting{Key: stepPrefix + route.id, Value: format.Encode(provider)})
	}
	if model, ok := connection.Model("model"); ok {
		protocol := connection.Protocol(StepCode, model)
		for _, route := range stepRoutes {
			if route.protocol == protocol {
				keys = append(keys,
					cc.Setting{Key: stepStartupKeys[0], Value: route.id},
					cc.Setting{Key: stepStartupKeys[1], Value: model})
				break
			}
		}
	}
	return keys
}

// BaseURLKey is unused: SyncedKeys points every provider elsewhere.
func (stepCode) BaseURLKey() string { return "" }

func (s stepCode) SyncedKeys(config *cc.Config, record *cc.Record, origin string) []cc.Setting {
	var keys []cc.Setting
	for _, key := range slices.Sorted(maps.Keys(record.Keys)) {
		if !s.HoldsConnection(key) {
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
		provider["baseUrl"] = stepURL(api, origin)
		keys = append(keys, cc.Setting{Key: key, Value: format.Encode(provider)})
	}
	return keys
}

// Format is JSON with line comments and trailing commas, which Pi strips.
func (stepModels) Format() format.Format {
	return format.JSONFormat{Syntax: &format.JSONCTrailing, Objects: []string{"providers"}}
}

func (stepModels) Secret() bool { return true }

func (stepModels) Holds(key string) bool { return !slices.Contains(stepStartupKeys, key) }

// Get reads a provider as one JSON object.
func (stepModels) Get(document format.Document, key string) (string, bool, bool) {
	if json, ok := document.(*format.JSONDocument); ok && strings.HasPrefix(key, stepPrefix) {
		return getWhole(json, key)
	}
	return document.Get(key)
}

func (stepModels) Set(document format.Document, key, value string) error {
	if json, ok := document.(*format.JSONDocument); ok && strings.HasPrefix(key, stepPrefix) {
		return setWhole(json, key, value, stepRank)
	}
	return document.Set(key, value)
}

// IsEmpty lets an emptied `providers` stay until the file goes.
func (stepModels) IsEmpty(document format.Document) bool {
	if json, ok := document.(*format.JSONDocument); ok {
		return json.HoldsOnly(func(p format.JSONProperty) bool {
			return p.HasName && p.Name == "providers" && p.IsEmptyObject()
		})
	}
	return document.IsEmpty()
}

func (stepConfig) Format() format.Format { return format.TOMLFormat{} }

func (stepConfig) Holds(key string) bool { return slices.Contains(stepStartupKeys, key) }
