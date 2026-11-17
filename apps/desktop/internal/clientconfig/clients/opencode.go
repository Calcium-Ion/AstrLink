package clients

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// OpenCode reads a JSONC config with an `astrlink` provider listing every
// model, from the first of its global config files that exists.

var OpenCode = cc.New(openCode{})

const (
	openCodeProviderPrefix = "provider.astrlink."
	openCodeModelsKey      = "provider.astrlink.models"
	openCodeSchema         = "https://opencode.ai/config.json"
)

// openCodeFiles are OpenCode's global config files, highest priority
// first.
var openCodeFiles = []string{"opencode.jsonc", "opencode.json", "config.json"}

type openCode struct{}

// openCodeConfig is the global config file AstrLink writes.
type openCodeConfig struct{}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// openCodeFile is the file AstrLink writes. OpenCode merges its global
// files, the first of openCodeFiles winning. AstrLink writes the one it
// wrote before, else the first that exists, else a new `opencode.jsonc`.
func openCodeFile(home agents.Home, record *cc.Record) string {
	dir := home.OpenCodeDir()
	names := openCodeFiles
	if record != nil && record.File != nil && slices.Contains(openCodeFiles, *record.File) {
		names = append([]string{*record.File}, openCodeFiles...)
	}
	for _, name := range names {
		if path := filepath.Join(dir, name); isFile(path) {
			return path
		}
	}
	return filepath.Join(dir, openCodeFiles[0])
}

func (openCode) ID() string          { return "opencode" }
func (openCode) Name() string        { return "OpenCode" }
func (openCode) Protocols() []string { return []string{"openai.chat"} }
func (openCode) ListsModels() bool   { return true }

func (openCode) Dir(home agents.Home, _ string) string { return home.OpenCodeDir() }

func (openCode) Detected(home agents.Home, _ string) bool { return home.OpenCodeDetected() }

func (openCode) Files() []cc.File { return []cc.File{openCodeConfig{}} }

// ConfigPaths keeps OpenCode on the file AstrLink wrote before.
func (openCode) ConfigPaths(home agents.Home, _ string, record *cc.Record) []string {
	return []string{openCodeFile(home, record)}
}

func (openCode) RecordedFile(paths []string) (string, bool) {
	return filepath.Base(paths[0]), true
}

func (openCode) ConnectionKeys() []string {
	return []string{"provider.astrlink.npm", "provider.astrlink.options.baseURL", "provider.astrlink.options.apiKey"}
}

// listModels is the models of a list a config holds as JSON.
func listModels(config *cc.Config, key string) []string {
	value, ok := config.String(key)
	if !ok {
		return nil
	}
	list, err := format.DecodeJSON(value)
	if err != nil {
		return nil
	}
	return cc.ModelIDs(cc.ModelEntries(list))
}

func (openCode) CurrentModels(config *cc.Config) cc.CurrentModels {
	var models cc.CurrentModels
	if model, ok := config.ProviderModel("model"); ok {
		models.Model = &model
	}
	models.Models = listModels(config, openCodeModelsKey)
	return models
}

func number(n uint64) format.Number { return format.Number(strconv.FormatUint(n, 10)) }

func (openCode) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	var keys []cc.Setting
	if model, ok := connection.Model("model"); ok {
		keys = append(keys, cc.Setting{Key: "model", Value: cc.ProviderID + "/" + model})
	}
	// The display name is local UI in OpenCode's model picker.
	for _, kv := range [][2]string{
		{"npm", "@ai-sdk/openai-compatible"},
		{"name", "AstrLink"},
		{"options.baseURL", OpenCode.BaseURL(connection.Origin)},
		{"options.apiKey", connection.Token},
	} {
		keys = append(keys, cc.Setting{Key: openCodeProviderPrefix + kv[0], Value: kv[1]})
	}
	// Without both limits OpenCode never compacts a long session.
	models := map[string]any{}
	for _, id := range connection.ModelList() {
		entry := map[string]any{"name": id}
		if d, ok := details[id]; ok && d.ContextWindow != nil && d.MaxTokens != nil {
			entry["limit"] = map[string]any{"context": number(*d.ContextWindow), "output": number(*d.MaxTokens)}
		}
		models[id] = entry
	}
	return append(keys, cc.Setting{Key: openCodeModelsKey, Value: format.Encode(models)})
}

// StartFile names the schema in a file AstrLink creates.
func (openCode) StartFile(config *cc.Config) *cc.FileError {
	return config.Set("$schema", openCodeSchema)
}

func (openCode) BaseURLKey() string { return "provider.astrlink.options.baseURL" }

// Format is JSONC, as OpenCode reads it.
func (openCodeConfig) Format() format.Format {
	return format.JSONFormat{Syntax: &format.JSONCTrailing, Objects: []string{"provider"}}
}

func (c openCodeConfig) Group(document format.Document) []format.KeyValue {
	var keys []format.KeyValue
	cc.PushPresent(&keys, c, document, "model")
	if json, ok := document.(*format.JSONDocument); ok {
		json.ObjectKeys(openCodeProviderPrefix, []string{"options"}, func(path string) (string, bool) {
			value, ok, _ := c.Get(document, path)
			return value, ok
		}, &keys)
	}
	return keys
}

func (openCodeConfig) Get(document format.Document, key string) (string, bool, bool) {
	if key == openCodeModelsKey {
		return cc.GetList(document, key)
	}
	return document.Get(key)
}

func (openCodeConfig) Set(document format.Document, key, value string) error {
	if key == openCodeModelsKey {
		return cc.SetList(document, key, value)
	}
	return document.Set(key, value)
}

func (openCodeConfig) Emptied() []string {
	return []string{"provider.astrlink.options", "provider.astrlink", "provider"}
}

// IsEmpty lets the schema AstrLink named in a file it created go with the
// rest.
func (openCodeConfig) IsEmpty(document format.Document) bool {
	if json, ok := document.(*format.JSONDocument); ok {
		return json.HoldsOnly(func(p format.JSONProperty) bool { return p.HasName && p.Name == "$schema" })
	}
	return document.IsEmpty()
}
