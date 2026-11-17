package clients

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Goose, desktop and CLI alike, reads `~/.config/goose`: a JSON file per
// custom provider in `custom_providers`, and the provider and model it
// starts with in `config.yaml`. AstrLink adds an `openai` engine provider
// for Chat and Responses models and an `anthropic` one for Claude's. Goose
// reads a provider's key only from the keychain, by the name in
// `api_key_env`; both providers share one, so the user pastes the token
// once. Goose picks Responses for a model by its name.

var Goose = cc.New(goose{})

type goose struct{}

// gooseConfig is `config.yaml`.
type gooseConfig struct{}

// gooseProviderFile is a file in `custom_providers`, read as one key.
type gooseProviderFile struct{ provider gooseProvider }

type gooseProvider struct {
	// name is Goose's provider name, the file's name.
	name   string
	engine string
	// display is what Goose shows.
	display string
}

var (
	gooseOpenAI    = gooseProvider{"local_gateway", "openai", "AstrLink"}
	gooseAnthropic = gooseProvider{"local_gateway_anthropic", "anthropic", "AstrLink Anthropic"}
	gooseProviders = []gooseProvider{gooseOpenAI, gooseAnthropic}
)

const (
	// gooseKeyName is the keychain entry both providers read.
	gooseKeyName    = "LOCAL_GATEWAY_API_KEY"
	gooseActiveKey  = "active_provider"
	gooseFilePrefix = "custom_providers."
	// gooseDefaultContext is what Goose assumes of a model it is told no
	// limit of.
	gooseDefaultContext = 128_000
)

func (p gooseProvider) key() string { return gooseFilePrefix + p.name }

// baseURL: Goose adds `v1/messages` to an Anthropic URL, and the rest to
// the other.
func (p gooseProvider) baseURL(origin string) string {
	if p == gooseAnthropic {
		return origin
	}
	return origin + "/v1"
}

func gooseProviderFor(protocol string) gooseProvider {
	if protocol == "anthropic.messages" {
		return gooseAnthropic
	}
	return gooseOpenAI
}

func gooseProviderOf(key string) (gooseProvider, bool) {
	for _, provider := range gooseProviders {
		if provider.key() == key {
			return provider, true
		}
	}
	return gooseProvider{}, false
}

// gooseChoiceKey is `providers.<name>.<field>` in `config.yaml`.
func gooseChoiceKey(provider gooseProvider, field string) string {
	return "providers." + provider.name + "." + field
}

func gooseRank(name string) int {
	order := []string{"name", "engine", "display_name", "base_url", "api_key_env", "requires_auth", "supports_streaming", "dynamic_models", "models", "context_limit", "reasoning"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

func (goose) ID() string        { return "goose" }
func (goose) Name() string      { return "Goose" }
func (goose) Kind() cc.Kind     { return cc.KindPaste }
func (goose) ListsModels() bool { return true }
func (goose) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}

// Dir: `GOOSE_PATH_ROOT` and `XDG_CONFIG_HOME` from a shell are out of
// reach.
func (goose) Dir(home agents.Home, _ string) string {
	if home.Windows() {
		return home.Join("AppData", "Roaming", "Block", "goose", "config")
	}
	return home.Join(".config", "goose")
}

func (goose) Files() []cc.File {
	return []cc.File{gooseConfig{}, gooseProviderFile{gooseOpenAI}, gooseProviderFile{gooseAnthropic}}
}

func (g goose) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	dir := g.Dir(home, target)
	paths := []string{filepath.Join(dir, "config.yaml")}
	for _, provider := range gooseProviders {
		paths = append(paths, filepath.Join(dir, "custom_providers", provider.name+".json"))
	}
	return paths
}

func (goose) ConnectionKeys() []string { return nil }

// HoldsConnection: each provider holds an address; the token is in the
// keychain.
func (goose) HoldsConnection(key string) bool {
	_, ok := gooseProviderOf(key)
	return ok
}

func (goose) CurrentModels(config *cc.Config) cc.CurrentModels {
	var ids []string
	for _, provider := range gooseProviders {
		file, ok := configObject(config, provider.key())
		if !ok {
			continue
		}
		models, _ := file["models"].([]any)
		for _, model := range models {
			name, _ := field(model, "name")
			if id, ok := cc.CleanModel(name); ok && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	var models cc.CurrentModels
	if active, ok := config.String(gooseActiveKey); ok {
		for _, provider := range gooseProviders {
			if provider.name != active {
				continue
			}
			if model, ok := config.Model(gooseChoiceKey(provider, "model")); ok {
				models.Model = &model
			}
			break
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

func (goose) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	list := connection.ModelList()
	var keys []cc.Setting
	for _, provider := range gooseProviders {
		var models []any
		for _, id := range list {
			if gooseProviderFor(connection.Protocol(Goose, id)) != provider {
				continue
			}
			d, known := details[id]
			context := uint64(gooseDefaultContext)
			if known && d.ContextWindow != nil {
				context = *d.ContextWindow
			}
			model := map[string]any{"name": id, "context_limit": number(context)}
			if known && len(d.Reasoning) > 0 {
				model["reasoning"] = true
			}
			models = append(models, model)
		}
		if len(models) == 0 {
			continue
		}
		file := map[string]any{
			"name":               provider.name,
			"engine":             provider.engine,
			"display_name":       provider.display,
			"base_url":           provider.baseURL(connection.Origin),
			"api_key_env":        gooseKeyName,
			"requires_auth":      true,
			"supports_streaming": true,
			"dynamic_models":     false,
			"models":             models,
		}
		keys = append(keys, cc.Setting{Key: provider.key(), Value: format.Encode(file)})
	}
	if model, ok := connection.Model("model"); ok {
		provider := gooseProviderFor(connection.Protocol(Goose, model))
		keys = append(keys,
			cc.Setting{Key: gooseActiveKey, Value: provider.name},
			cc.Setting{Key: gooseChoiceKey(provider, "enabled"), Value: "true"},
			cc.Setting{Key: gooseChoiceKey(provider, "model"), Value: model},
			cc.Setting{Key: gooseChoiceKey(provider, "configured"), Value: "true"})
	}
	return keys
}

// BaseURLKey is unused: SyncedKeys points every provider elsewhere.
func (goose) BaseURLKey() string { return "" }

func (goose) SyncedKeys(config *cc.Config, record *cc.Record, origin string) []cc.Setting {
	var keys []cc.Setting
	for _, key := range sortedKeys(record.Keys) {
		provider, ok := gooseProviderOf(key)
		if !ok {
			continue
		}
		file, ok := configObject(config, key)
		if !ok {
			continue
		}
		file["base_url"] = provider.baseURL(origin)
		keys = append(keys, cc.Setting{Key: key, Value: format.Encode(file)})
	}
	return keys
}

// PasteInto: either provider takes the one key, so it is pasted once.
func (goose) PasteInto(config *cc.Config) []string {
	for _, provider := range gooseProviders {
		if _, ok := configObject(config, provider.key()); ok {
			return []string{provider.display}
		}
	}
	return nil
}

func gooseSteps(key string) []format.YAMLStep {
	var steps []format.YAMLStep
	for _, name := range strings.Split(key, ".") {
		steps = append(steps, format.YAMLKey(name))
	}
	return steps
}

func gooseIsSwitch(key string) bool {
	return strings.HasPrefix(key, "providers.") && (strings.HasSuffix(key, ".enabled") || strings.HasSuffix(key, ".configured"))
}

func (gooseConfig) Format() format.Format { return format.YAMLFormat{Root: format.YAMLMapping} }

func (gooseConfig) Holds(key string) bool { return !strings.HasPrefix(key, gooseFilePrefix) }

// Get reads a switch as `true` or `false`.
func (gooseConfig) Get(document format.Document, key string) (string, bool, bool) {
	yaml := format.AsYAML(document)
	if yaml == nil || !gooseIsSwitch(key) {
		return document.Get(key)
	}
	value, present := yaml.GetJSON(gooseSteps(key)...)
	if !present {
		return "", false, false
	}
	on, ok := value.(bool)
	if !ok {
		return "", false, true
	}
	return strconv.FormatBool(on), true, true
}

func (gooseConfig) Set(document format.Document, key, value string) error {
	if yaml := format.AsYAML(document); yaml != nil && gooseIsSwitch(key) {
		return yaml.SetKeyJSON(key, value == "true")
	}
	return document.Set(key, value)
}

// Remove lets a provider's emptied block go, then the emptied `providers`.
func (gooseConfig) Remove(document format.Document, key string) {
	document.Remove(key)
	yaml := format.AsYAML(document)
	if yaml == nil {
		return
	}
	emptied := func(path ...format.YAMLStep) bool {
		value, ok := yaml.GetJSON(path...)
		object, isObject := value.(map[string]any)
		return ok && isObject && len(object) == 0
	}
	for _, provider := range gooseProviders {
		path := []format.YAMLStep{format.YAMLKey("providers"), format.YAMLKey(provider.name)}
		if emptied(path...) {
			_ = yaml.Remove(path...)
		}
	}
	if emptied(format.YAMLKey("providers")) {
		_ = yaml.Remove(format.YAMLKey("providers"))
	}
}

// Format is strict JSON: a file that fails to read keeps Goose from
// loading every custom provider.
func (gooseProviderFile) Format() format.Format { return format.JSONFormat{} }

func (f gooseProviderFile) Holds(key string) bool { return key == f.provider.key() }

func (f gooseProviderFile) Get(document format.Document, key string) (string, bool, bool) {
	json, ok := document.(*format.JSONDocument)
	if !ok || key != f.provider.key() || json.IsEmpty() {
		return "", false, false
	}
	value, ok := json.Value()
	if !ok {
		return "", false, true
	}
	return format.Encode(value), true, true
}

func gooseNames(json *format.JSONDocument) []string {
	value, _ := json.Value()
	object, _ := value.(map[string]any)
	return format.SortedKeys(object)
}

// Set makes the file the value, property by property.
func (gooseProviderFile) Set(document format.Document, key, value string) error {
	json, ok := document.(*format.JSONDocument)
	if !ok {
		return document.Set(key, value)
	}
	decoded, err := format.DecodeJSON(value)
	file, isObject := decoded.(map[string]any)
	if err != nil || !isObject {
		return fmt.Errorf("%s is not an object", key)
	}
	for _, name := range gooseNames(json) {
		if _, kept := file[name]; !kept {
			json.Remove(name)
		}
	}
	for _, name := range format.SortedKeys(file) {
		if err := json.SetJSON(name, file[name], gooseRank); err != nil {
			return err
		}
	}
	return nil
}

func (gooseProviderFile) Remove(document format.Document, _ string) {
	if json, ok := document.(*format.JSONDocument); ok {
		for _, name := range gooseNames(json) {
			json.Remove(name)
		}
	}
}
