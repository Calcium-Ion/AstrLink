package clients

import (
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Aider reads `~/.aider.conf.yml` and sends every request through LiteLLM,
// whose OpenAI-compatible route takes a model named `openai/<model>`.
// LiteLLM prefers `OPENAI_BASE_URL` to the address Aider sets, so AstrLink
// also sets that variable in `set-env`, keeping one the user's shell
// exports from taking the token elsewhere. Aider gives its helper models
// AstrLink's model too, since its own picks may not be on the gateway.

var Aider = cc.New(aider{})

type aider struct{}

// aiderConfig is `.aider.conf.yml`.
type aiderConfig struct{}

const (
	aiderBaseKey  = "openai-api-base"
	aiderTokenKey = "openai-api-key"
	aiderList     = "set-env"
	aiderVariable = "OPENAI_BASE_URL"
	// aiderVariableKey is the `OPENAI_BASE_URL=<address>` item of
	// `set-env`, by its value.
	aiderVariableKey = "set-env.OPENAI_BASE_URL"
	aiderPrefix      = "openai/"
)

var aiderModelKeys = []string{"model", "weak-model", "editor-model"}

func (aider) ID() string          { return "aider" }
func (aider) Name() string        { return "Aider" }
func (aider) Protocols() []string { return []string{"openai.chat"} }

func (aider) Dir(home agents.Home, _ string) string { return home.Dir() }

// Detected: Aider keeps its state in `~/.aider` after a first run; uv and
// pipx install it as `aider-chat`.
func (aider) Detected(home agents.Home, _ string) bool {
	installs := []string{home.Join(".local", "share", "uv", "tools", "aider-chat"), home.Join(".local", "pipx", "venvs", "aider-chat")}
	if home.Windows() {
		installs = []string{home.Join("AppData", "Roaming", "uv", "data", "tools", "aider-chat"), home.Join("pipx", "venvs", "aider-chat")}
	}
	if cc.IsDir(home.Join(".aider")) || isFile(home.Join(".aider.conf.yml")) {
		return true
	}
	for _, dir := range installs {
		if cc.IsDir(dir) {
			return true
		}
	}
	return false
}

func (aider) Files() []cc.File { return []cc.File{aiderConfig{}} }

func (aider) ConfigPaths(home agents.Home, _ string, _ *cc.Record) []string {
	return []string{home.Join(".aider.conf.yml")}
}

func (aider) ConnectionKeys() []string {
	return []string{aiderBaseKey, aiderTokenKey, aiderVariableKey}
}

func (aider) CurrentModels(config *cc.Config) cc.CurrentModels {
	var models cc.CurrentModels
	if model, ok := config.String(aiderModelKeys[0]); ok {
		if model, ok := strings.CutPrefix(model, aiderPrefix); ok {
			if model, ok := cc.CleanModel(model); ok {
				models.Model = &model
			}
		}
	}
	return models
}

func (aider) DesiredKeys(connection *cc.Connection, _ cc.Catalog) []cc.Setting {
	base := Aider.BaseURL(connection.Origin)
	keys := []cc.Setting{
		{Key: aiderBaseKey, Value: base},
		{Key: aiderVariableKey, Value: base},
		{Key: aiderTokenKey, Value: connection.Token},
	}
	if model, ok := connection.Model("model"); ok {
		for _, key := range aiderModelKeys {
			keys = append(keys, cc.Setting{Key: key, Value: aiderPrefix + model})
		}
	}
	return keys
}

func (aider) BaseURLKey() string { return aiderBaseKey }

func (aider) SyncedKeys(_ *cc.Config, _ *cc.Record, origin string) []cc.Setting {
	base := Aider.BaseURL(origin)
	return []cc.Setting{{Key: aiderBaseKey, Value: base}, {Key: aiderVariableKey, Value: base}}
}

// aiderVariableAt is the index and value of the `OPENAI_BASE_URL` item of
// `set-env`.
func aiderVariableAt(yaml *format.YAMLDocument) (int, string, bool) {
	value, _ := yaml.GetJSON(format.YAMLKey(aiderList))
	items, _ := value.([]any)
	for i, item := range items {
		text, _ := item.(string)
		name, value, ok := strings.Cut(text, "=")
		if ok && strings.TrimSpace(name) == aiderVariable {
			return i, strings.TrimSpace(value), true
		}
	}
	return 0, "", false
}

func (aiderConfig) Format() format.Format { return format.YAMLFormat{Root: format.YAMLMapping} }

func (aiderConfig) Secret() bool { return true }

func (aiderConfig) Get(document format.Document, key string) (string, bool, bool) {
	if yaml := format.AsYAML(document); yaml != nil && key == aiderVariableKey {
		_, value, ok := aiderVariableAt(yaml)
		return value, ok, ok
	}
	return document.Get(key)
}

func flat(string) int { return 0 }

// Set: the item replaces one of the same name, at the end of the list.
func (aiderConfig) Set(document format.Document, key, value string) error {
	yaml := format.AsYAML(document)
	if yaml == nil || key != aiderVariableKey {
		return document.Set(key, value)
	}
	item := aiderVariable + "=" + value
	list := []format.YAMLStep{format.YAMLKey(aiderList)}
	current, hasList := yaml.GetJSON(list...)
	items, isList := current.([]any)
	at, _, found := aiderVariableAt(yaml)
	switch {
	// A list of the item alone is written anew, keeping its style.
	case found && isList && len(items) == 1, !found && !(hasList && isList):
		return yaml.Set(nil, aiderList, []any{item}, flat)
	case found:
		if err := yaml.Remove(format.YAMLKey(aiderList), format.YAMLIndex(at)); err != nil {
			return err
		}
	}
	return yaml.Push(list, item, flat)
}

// Remove drops an emptied `set-env`.
func (aiderConfig) Remove(document format.Document, key string) {
	yaml := format.AsYAML(document)
	if yaml == nil || key != aiderVariableKey {
		document.Remove(key)
		return
	}
	if at, _, ok := aiderVariableAt(yaml); ok {
		_ = yaml.Remove(format.YAMLKey(aiderList), format.YAMLIndex(at))
	}
	if value, ok := yaml.GetJSON(format.YAMLKey(aiderList)); ok {
		if items, isList := value.([]any); isList && len(items) == 0 {
			_ = yaml.Remove(format.YAMLKey(aiderList))
		}
	}
}
