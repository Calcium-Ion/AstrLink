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

// Kilo Code, CLI and VS Code extension alike, reads a JSONC config from the
// first of its global files in `~/.config/kilo`, as OpenCode does. AstrLink
// adds one provider that takes every protocol, each model naming its SDK.
// Kilo tells the model `<provider>/<model>`, so the provider id carries no
// product name; its display name is local.

var KiloCode = cc.New(kiloCode{})

type kiloCode struct{}

// kiloConfig is the global config file AstrLink writes.
type kiloConfig struct{}

const (
	kiloProvider   = "local-gateway"
	kiloPrefix     = "provider.local-gateway."
	kiloModelsKey  = "provider.local-gateway.models"
	kiloBaseURLKey = "provider.local-gateway.options.baseURL"
	kiloSchema     = "https://app.kilo.ai/config.json"
)

var (
	// kiloFiles are the files Kilo writes, the first that exists; it
	// merges them in the order of kiloMerged.
	kiloFiles = []string{"kilo.jsonc", "kilo.json", "opencode.jsonc", "opencode.json", "config.json"}
	// kiloMerged is Kilo's merge order, later files winning.
	kiloMerged = []string{"config.json", "kilo.json", "kilo.jsonc", "opencode.json", "opencode.jsonc"}
)

// kiloSDK is the SDK a model of protocol takes, when not the provider's.
func kiloSDK(protocol string) string {
	switch protocol {
	case "anthropic.messages":
		return "@ai-sdk/anthropic"
	case "openai.responses":
		return "@ai-sdk/openai"
	}
	return ""
}

func kiloConfigFile(home agents.Home, record *cc.Record) string {
	dir := KiloCode.Dir(home, "")
	names := kiloFiles
	if record != nil && record.File != nil && slices.Contains(kiloFiles, *record.File) {
		names = append([]string{*record.File}, kiloFiles...)
	}
	for _, name := range names {
		if path := filepath.Join(dir, name); isFile(path) {
			return path
		}
	}
	return filepath.Join(dir, kiloFiles[0])
}

func (kiloCode) ID() string   { return "kilo_code" }
func (kiloCode) Name() string { return "Kilo Code" }
func (kiloCode) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}
func (kiloCode) ListsModels() bool { return true }

// Dir: every Kilo process makes it; `KILO_CONFIG_DIR` and
// `XDG_CONFIG_HOME` from a shell are out of reach.
func (kiloCode) Dir(home agents.Home, _ string) string { return home.Join(".config", "kilo") }

func (kiloCode) Files() []cc.File { return []cc.File{kiloConfig{}} }

// ConfigPaths keeps Kilo on the file AstrLink wrote before.
func (kiloCode) ConfigPaths(home agents.Home, _ string, record *cc.Record) []string {
	return []string{kiloConfigFile(home, record)}
}

func (kiloCode) RecordedFile(paths []string) (string, bool) { return filepath.Base(paths[0]), true }

func (kiloCode) ConnectionKeys() []string {
	return []string{"provider.local-gateway.npm", "provider.local-gateway.options.baseURL", "provider.local-gateway.options.apiKey"}
}

func (kiloCode) CurrentModels(config *cc.Config) cc.CurrentModels {
	var models cc.CurrentModels
	if value, ok := config.Model("model"); ok {
		if model, ok := strings.CutPrefix(value, kiloProvider+"/"); ok {
			if model, ok := cc.CleanModel(model); ok {
				models.Model = &model
			}
		}
	}
	models.Models = listModels(config, kiloModelsKey)
	return models
}

func (kiloCode) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	var keys []cc.Setting
	if model, ok := connection.Model("model"); ok {
		keys = append(keys, cc.Setting{Key: "model", Value: kiloProvider + "/" + model})
	}
	for _, kv := range [][2]string{
		{"npm", "@ai-sdk/openai-compatible"},
		{"name", "AstrLink"},
		{"options.baseURL", KiloCode.BaseURL(connection.Origin)},
		{"options.apiKey", connection.Token},
	} {
		keys = append(keys, cc.Setting{Key: kiloPrefix + kv[0], Value: kv[1]})
	}
	models := map[string]any{}
	for _, id := range connection.ModelList() {
		entry := map[string]any{"name": id}
		if npm := kiloSDK(connection.Protocol(KiloCode, id)); npm != "" {
			entry["provider"] = map[string]any{"npm": npm}
		}
		d, ok := details[id]
		if ok && len(d.Reasoning) > 0 {
			entry["reasoning"] = true
		}
		// Without both limits Kilo never compacts a long session.
		if ok && d.ContextWindow != nil && d.MaxTokens != nil {
			entry["limit"] = map[string]any{"context": number(*d.ContextWindow), "output": number(*d.MaxTokens)}
		}
		models[id] = entry
	}
	return append(keys, cc.Setting{Key: kiloModelsKey, Value: format.Encode(models)})
}

// StartFile names the schema Kilo would add to the file otherwise.
func (kiloCode) StartFile(config *cc.Config) *cc.FileError { return config.Set("$schema", kiloSchema) }

func (kiloCode) BaseURLKey() string { return kiloBaseURLKey }

// Notes: a file Kilo merges after AstrLink's sets the model or this
// provider.
func (k kiloCode) Notes(home agents.Home, target string, _ *cc.Config) []string {
	after := slices.Index(kiloMerged, filepath.Base(kiloConfigFile(home, nil)))
	if after < 0 {
		return nil
	}
	dir := k.Dir(home, target)
	for _, name := range kiloMerged[after+1:] {
		text, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		document, err := format.ParseJSON(string(text), format.JSONFormat{Syntax: &format.JSONCTrailing})
		if err != nil {
			continue
		}
		value, _ := document.Value()
		object, _ := value.(map[string]any)
		providers, _ := object["provider"].(map[string]any)
		_, model := object["model"]
		_, provider := providers[kiloProvider]
		if model || provider {
			return []string{"overridden"}
		}
	}
	return nil
}

func (kiloConfig) Format() format.Format {
	return format.JSONFormat{Syntax: &format.JSONCTrailing, Objects: []string{"provider"}}
}

func (c kiloConfig) Group(document format.Document) []format.KeyValue {
	var keys []format.KeyValue
	cc.PushPresent(&keys, c, document, "model")
	if json, ok := document.(*format.JSONDocument); ok {
		json.ObjectKeys(kiloPrefix, []string{"options"}, func(path string) (string, bool) {
			value, ok, _ := c.Get(document, path)
			return value, ok
		}, &keys)
	}
	return keys
}

func (kiloConfig) Get(document format.Document, key string) (string, bool, bool) {
	if key == kiloModelsKey {
		return cc.GetList(document, key)
	}
	return document.Get(key)
}

func (kiloConfig) Set(document format.Document, key, value string) error {
	if key == kiloModelsKey {
		return cc.SetList(document, key, value)
	}
	return document.Set(key, value)
}

func (kiloConfig) Emptied() []string {
	return []string{"provider.local-gateway.options", "provider.local-gateway", "provider"}
}

// IsEmpty lets the schema AstrLink named in a file it created go with the
// rest.
func (kiloConfig) IsEmpty(document format.Document) bool {
	if json, ok := document.(*format.JSONDocument); ok {
		return json.HoldsOnly(func(p format.JSONProperty) bool { return p.HasName && p.Name == "$schema" })
	}
	return document.IsEmpty()
}
