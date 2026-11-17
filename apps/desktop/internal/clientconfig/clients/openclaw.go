package clients

import (
	"path/filepath"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// OpenClaw reads JSON5 with an `astrlink` provider listing every model,
// and the model its agents start with.

var OpenClaw = cc.New(openClaw{})

const (
	openClawProviderPrefix = "models.providers.astrlink."
	openClawModelsKey      = "models.providers.astrlink.models"
	// openClawPrimaryKey is the model OpenClaw's agents start with;
	// openClawModelKey may also be that model's name alone.
	openClawPrimaryKey = "agents.defaults.model.primary"
	openClawModelKey   = "agents.defaults.model"
)

type openClaw struct{}

// openClawConfig is `openclaw.json`, JSON5.
type openClawConfig struct{}

// openClawPath is the key path is written at: the model's name alone
// stays a name alone.
func openClawPath(document format.Document, path string) string {
	if json, ok := document.(*format.JSONDocument); ok && path == openClawPrimaryKey && json.IsStringAt(openClawModelKey) {
		return openClawModelKey
	}
	return path
}

func (openClaw) ID() string          { return "openclaw" }
func (openClaw) Name() string        { return "OpenClaw" }
func (openClaw) Protocols() []string { return []string{"openai.chat"} }
func (openClaw) ListsModels() bool   { return true }

func (openClaw) Dir(home agents.Home, _ string) string { return home.OpenClawDir() }

func (openClaw) Files() []cc.File { return []cc.File{openClawConfig{}} }

func (openClaw) ConfigPaths(home agents.Home, _ string, _ *cc.Record) []string {
	return []string{filepath.Join(home.OpenClawDir(), "openclaw.json")}
}

func (openClaw) ConnectionKeys() []string {
	return []string{"models.providers.astrlink.baseUrl", "models.providers.astrlink.api", "models.providers.astrlink.apiKey"}
}

func (openClaw) CurrentModels(config *cc.Config) cc.CurrentModels {
	var models cc.CurrentModels
	if model, ok := config.ProviderModel(openClawPrimaryKey); ok {
		models.Model = &model
	}
	models.Models = listModels(config, openClawModelsKey)
	return models
}

func (openClaw) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	var keys []cc.Setting
	for _, kv := range [][2]string{
		{"baseUrl", OpenClaw.BaseURL(connection.Origin)},
		{"apiKey", connection.Token},
		{"api", "openai-completions"},
	} {
		keys = append(keys, cc.Setting{Key: openClawProviderPrefix + kv[0], Value: kv[1]})
	}
	// OpenClaw requires a name for every model, and caps output at 8192
	// tokens without a limit.
	models := []any{}
	for _, id := range connection.ModelList() {
		entry := map[string]any{"id": id, "name": id}
		if d, ok := details[id]; ok {
			if d.ContextWindow != nil {
				entry["contextWindow"] = number(*d.ContextWindow)
			}
			if d.MaxTokens != nil {
				entry["maxTokens"] = number(*d.MaxTokens)
			}
		}
		models = append(models, entry)
	}
	keys = append(keys, cc.Setting{Key: openClawModelsKey, Value: format.Encode(models)})
	if model, ok := connection.Model("model"); ok {
		keys = append(keys, cc.Setting{Key: openClawPrimaryKey, Value: cc.ProviderID + "/" + model})
	}
	return keys
}

func (openClaw) BaseURLKey() string { return "models.providers.astrlink.baseUrl" }

func (openClawConfig) Format() format.Format {
	return format.JSONFormat{Syntax: &format.JSON5, Objects: []string{"models", "models.providers", "agents", "agents.defaults"}}
}

func (c openClawConfig) Group(document format.Document) []format.KeyValue {
	var keys []format.KeyValue
	if json, ok := document.(*format.JSONDocument); ok {
		json.ObjectKeys(openClawProviderPrefix, nil, func(path string) (string, bool) {
			value, ok, _ := c.Get(document, path)
			return value, ok
		}, &keys)
	}
	cc.PushPresent(&keys, c, document, openClawPrimaryKey)
	return keys
}

func (openClawConfig) Get(document format.Document, path string) (string, bool, bool) {
	// `model` may be the primary model's name alone.
	path = openClawPath(document, path)
	if path == openClawModelsKey {
		return cc.GetList(document, path)
	}
	return document.Get(path)
}

func (openClawConfig) Set(document format.Document, path, value string) error {
	path = openClawPath(document, path)
	if path == openClawModelsKey {
		return cc.SetList(document, path, value)
	}
	return document.Set(path, value)
}

func (openClawConfig) Remove(document format.Document, path string) {
	document.Remove(openClawPath(document, path))
}

func (openClawConfig) Emptied() []string {
	return []string{"models.providers.astrlink", "models.providers", "models", openClawModelKey, "agents.defaults", "agents"}
}
