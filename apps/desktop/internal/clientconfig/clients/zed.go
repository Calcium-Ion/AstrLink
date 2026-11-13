package clients

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Zed reads `~/.config/zed/settings.json`. AstrLink adds an
// `openai_compatible` provider for Chat and Responses models and an
// `anthropic_compatible` one for Claude's, never moving Zed's own
// providers, whose shell keys would follow. Zed reads a provider's key
// only from the keychain, kept by `api_url`, so the user pastes the token
// once per provider, and again after the port moves. Zed tells the model
// only the model's name.

var Zed = cc.New(zed{})

type zed struct{}

// zedSettings is `settings.json`.
type zedSettings struct{}

// zedProvider is Zed's provider map and the provider id, which Zed shows.
type zedProvider struct{ kind, id string }

var (
	zedOpenAI    = zedProvider{"openai_compatible", "AstrLink"}
	zedAnthropic = zedProvider{"anthropic_compatible", "AstrLink Anthropic"}
	zedProviders = []zedProvider{zedOpenAI, zedAnthropic}
	// zedLayers are keys a user's settings can set `agent` and
	// `language_models` under too.
	zedLayers = []string{"stable", "preview", "nightly", "dev", "macos", "linux", "windows"}
)

const (
	zedDefaultKey = "agent.default_model"
	// What Zed assumes of a model it is told no limits of.
	zedDefaultContext   = 128_000
	zedAnthropicContext = 200_000
	// zedAnthropicOutput: Zed's own Anthropic default is 4096.
	zedAnthropicOutput = 32_000
)

func (p zedProvider) key() string { return "language_models." + p.kind + "." + p.id }

func zedProviderOf(key string) (zedProvider, bool) {
	for _, provider := range zedProviders {
		if provider.key() == key {
			return provider, true
		}
	}
	return zedProvider{}, false
}

// zedAPIURL: Zed adds `/v1/messages` to an Anthropic URL, and the rest to
// the other.
func zedAPIURL(provider zedProvider, origin string) string {
	if provider == zedAnthropic {
		return origin
	}
	return origin + "/v1"
}

func zedRank(name string) int {
	order := []string{"provider", "model", "api_url", "available_models", "name", "max_tokens", "max_output_tokens", "capabilities", "tools", "images", "parallel_tool_calls", "prompt_cache_key", "chat_completions", "interleaved_reasoning", "max_tokens_parameter"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

func zedModelEntry(id, protocol string, details cc.Catalog) map[string]any {
	d := details[id]
	if protocol == "anthropic.messages" {
		context, output := uint64(zedAnthropicContext), uint64(zedAnthropicOutput)
		if d.ContextWindow != nil {
			context = *d.ContextWindow
		}
		if d.MaxTokens != nil {
			output = *d.MaxTokens
		}
		return map[string]any{
			"name":              id,
			"max_tokens":        number(context),
			"max_output_tokens": number(output),
			"capabilities":      map[string]any{"tools": true, "images": false, "prompt_caching": true},
		}
	}
	context := uint64(zedDefaultContext)
	if d.ContextWindow != nil {
		context = *d.ContextWindow
	}
	// Zed has no default for each capability, so all of them are written.
	entry := map[string]any{
		"name":       id,
		"max_tokens": number(context),
		"capabilities": map[string]any{
			"tools":                 true,
			"images":                false,
			"parallel_tool_calls":   false,
			"prompt_cache_key":      false,
			"chat_completions":      protocol != "openai.responses",
			"interleaved_reasoning": false,
			"max_tokens_parameter":  false,
		},
	}
	if d.MaxTokens != nil {
		entry["max_output_tokens"] = number(*d.MaxTokens)
	}
	return entry
}

func zedSettingsFile(home agents.Home) string {
	return filepath.Join(Zed.Dir(home, ""), "settings.json")
}

func (zed) ID() string        { return "zed" }
func (zed) Name() string      { return "Zed" }
func (zed) Kind() cc.Kind     { return cc.KindPaste }
func (zed) ListsModels() bool { return true }
func (zed) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}

// Dir: `--user-data-dir` and Flatpak's directory are out of reach.
func (zed) Dir(home agents.Home, _ string) string {
	if home.Windows() {
		return home.Join("AppData", "Roaming", "Zed")
	}
	return home.Join(".config", "zed")
}

func (zed) Files() []cc.File { return []cc.File{zedSettings{}} }

func (zed) ConfigPaths(home agents.Home, _ string, _ *cc.Record) []string {
	return []string{zedSettingsFile(home)}
}

func (zed) ConnectionKeys() []string { return nil }

// HoldsConnection: each provider holds an address; the token is in the
// keychain.
func (zed) HoldsConnection(key string) bool {
	_, ok := zedProviderOf(key)
	return ok
}

func (zed) CurrentModels(config *cc.Config) cc.CurrentModels {
	var ids []string
	for _, provider := range zedProviders {
		entry, ok := configObject(config, provider.key())
		if !ok {
			continue
		}
		models, _ := entry["available_models"].([]any)
		for _, model := range models {
			name, _ := field(model, "name")
			if id, ok := cc.CleanModel(name); ok && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	var models cc.CurrentModels
	if chosen, ok := configObject(config, zedDefaultKey); ok {
		provider, hasProvider := field(chosen, "provider")
		ours := hasProvider && slices.ContainsFunc(zedProviders, func(p zedProvider) bool { return p.id == provider })
		if model, ok := field(chosen, "model"); ours && ok {
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

func (zed) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	list := connection.ModelList()
	var keys []cc.Setting
	for _, provider := range zedProviders {
		var models []any
		for _, id := range list {
			protocol := connection.Protocol(Zed, id)
			if (protocol == "anthropic.messages") == (provider == zedAnthropic) {
				models = append(models, zedModelEntry(id, protocol, details))
			}
		}
		if len(models) == 0 {
			continue
		}
		entry := map[string]any{"api_url": zedAPIURL(provider, connection.Origin), "available_models": models}
		keys = append(keys, cc.Setting{Key: provider.key(), Value: format.Encode(entry)})
	}
	if model, ok := connection.Model("model"); ok {
		provider := zedOpenAI
		if connection.Protocol(Zed, model) == "anthropic.messages" {
			provider = zedAnthropic
		}
		chosen := map[string]any{"provider": provider.id, "model": model}
		keys = append(keys, cc.Setting{Key: zedDefaultKey, Value: format.Encode(chosen)})
	}
	return keys
}

// BaseURLKey is unused: SyncedKeys points every provider elsewhere.
func (zed) BaseURLKey() string { return "" }

func (zed) SyncedKeys(config *cc.Config, record *cc.Record, origin string) []cc.Setting {
	var keys []cc.Setting
	for _, key := range sortedKeys(record.Keys) {
		provider, ok := zedProviderOf(key)
		if !ok {
			continue
		}
		entry, ok := configObject(config, key)
		if !ok {
			continue
		}
		entry["api_url"] = zedAPIURL(provider, origin)
		keys = append(keys, cc.Setting{Key: key, Value: format.Encode(entry)})
	}
	return keys
}

// Notes: a release channel or platform section that sets the model or
// providers wins over the top level.
func (zed) Notes(home agents.Home, _ string, _ *cc.Config) []string {
	text, err := os.ReadFile(zedSettingsFile(home))
	if err != nil {
		return nil
	}
	document, err := format.ParseJSON(string(text), format.JSONFormat{Syntax: &format.JSONCTrailing})
	if err != nil {
		return nil
	}
	value, _ := document.Value()
	settings, _ := value.(map[string]any)
	for _, name := range zedLayers {
		layer, ok := settings[name].(map[string]any)
		if !ok {
			continue
		}
		agent, _ := layer["agent"].(map[string]any)
		_, models := layer["language_models"]
		_, model := agent["default_model"]
		if models || model {
			return []string{"overridden"}
		}
	}
	return nil
}

func (zed) KeepsTokenByAddress() bool { return true }

func (zed) PasteInto(config *cc.Config) []string {
	var names []string
	for _, provider := range zedProviders {
		if _, ok := configObject(config, provider.key()); ok {
			names = append(names, provider.id)
		}
	}
	return names
}

// Format is JSONC: Zed keeps comments when it edits the file itself.
func (zedSettings) Format() format.Format {
	return format.JSONFormat{Syntax: &format.JSONCTrailing, Objects: []string{"language_models", "agent"}}
}

func zedWhole(key string) bool {
	_, provider := zedProviderOf(key)
	return provider || key == zedDefaultKey
}

func (zedSettings) Get(document format.Document, key string) (string, bool, bool) {
	json, ok := document.(*format.JSONDocument)
	if !ok || !zedWhole(key) {
		return document.Get(key)
	}
	return getWhole(json, key)
}

func (zedSettings) Set(document format.Document, key, value string) error {
	json, ok := document.(*format.JSONDocument)
	if !ok || !zedWhole(key) {
		return document.Set(key, value)
	}
	return setWhole(json, key, value, zedRank)
}

func (zedSettings) Emptied() []string {
	return []string{"language_models.openai_compatible", "language_models.anthropic_compatible", "language_models", "agent"}
}
