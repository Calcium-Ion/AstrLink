package clients

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// DimAgent keeps its provider rows in a SQLite database its CLI and
// desktop UI share. The standard client uses separate columns; Rust builds
// keep a JSON record per provider. AstrLink reads the rows it manages as
// one JSON document (see dimAgentStore) and writes one provider per
// protocol.

var DimAgent = cc.New(dimAgent{})

type dimAgent struct{}

// dimAgentDatabase is the settings database, read as JSON.
type dimAgentDatabase struct{}

type dimAgentRoute struct{ protocol, id, driver string }

var dimAgentRoutes = []dimAgentRoute{
	{"openai.chat", "local-chat", "openai-compatible"},
	{"openai.responses", "local-responses", "openai-responses"},
	{"anthropic.messages", "local-anthropic", "anthropic"},
	{"google.generate_content", "local-gemini", "gemini"},
}

// dimAgentOwned reports whether a provider id is one AstrLink writes.
func dimAgentOwned(id string) bool {
	return slices.ContainsFunc(dimAgentRoutes, func(route dimAgentRoute) bool { return route.id == id })
}

func dimAgentAddress(protocol, origin string) string {
	if protocol == "google.generate_content" {
		return origin + "/v1beta"
	}
	return origin + "/v1"
}

func (dimAgent) ID() string   { return "dimagent" }
func (dimAgent) Name() string { return "DimAgent" }
func (dimAgent) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages", "google.generate_content"}
}

func (dimAgent) Targets() []string {
	return []string{"standard", "rust", "alpha", "global_standard", "global_rust"}
}

func (dimAgent) ListsModels() bool { return true }

func (dimAgent) Dir(home agents.Home, target string) string {
	base, sub := ".dimcode", "v2"
	switch target {
	case "rust":
		sub = "rust"
	case "alpha":
		base, sub = ".dimcode-alpha", "rust"
	case "global_standard":
		base = ".dim"
	case "global_rust":
		base, sub = ".dim", "rust"
	}
	return filepath.Join(home.Join(base), sub)
}

// Detected: DimAgent creates its database on its first start.
func (d dimAgent) Detected(home agents.Home, target string) bool {
	return cc.IsFile(d.ConfigPaths(home, target, nil)[0])
}

func (dimAgent) Files() []cc.File { return []cc.File{dimAgentDatabase{}} }

func (d dimAgent) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	name := "dimcode.sqlite"
	if target == "global_standard" {
		name = "dimglobal.sqlite"
	}
	return []string{filepath.Join(d.Dir(home, target), name)}
}

func (dimAgent) ConnectionKeys() []string { return nil }

// HoldsConnection: each provider holds its address and the token.
func (dimAgent) HoldsConnection(key string) bool { return strings.HasPrefix(key, "providers.") }

func (dimAgent) CurrentModels(config *cc.Config) cc.CurrentModels {
	var models []string
	for _, route := range dimAgentRoutes {
		provider, ok := configObject(config, "providers."+route.id)
		if !ok {
			continue
		}
		entries, _ := provider["models"].([]any)
		for _, entry := range entries {
			if id, ok := field(entry, "modelId"); ok && !slices.Contains(models, id) {
				models = append(models, id)
			}
		}
	}
	var current cc.CurrentModels
	if selection, ok := configObject(config, "selection"); ok {
		if provider, ok := field(selection, "providerId"); ok && dimAgentOwned(provider) {
			if model, ok := field(selection, "modelId"); ok {
				current.Model = &model
			}
		}
	}
	if current.Model == nil && len(models) > 0 {
		current.Model = cc.Some(models[0])
	}
	if len(models) > 0 {
		current.Models = models
	}
	return current
}

func (dimAgent) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	native := slices.Contains([]string{"rust", "alpha", "global_rust"}, connection.Target)
	list := connection.ModelList()
	chosen, hasChosen := connection.Model("model")
	var keys []cc.Setting
	for _, route := range dimAgentRoutes {
		var models []string
		for _, model := range list {
			if connection.Protocol(DimAgent, model) == route.protocol {
				models = append(models, model)
			}
		}
		if len(models) == 0 {
			continue
		}
		entries := make([]any, 0, len(models))
		for _, model := range models {
			detail := details[model]
			context, output := uint64(128_000), uint64(16_384)
			if detail.ContextWindow != nil {
				context = *detail.ContextWindow
			}
			if detail.MaxTokens != nil {
				output = *detail.MaxTokens
			}
			entries = append(entries, map[string]any{
				"modelId": model, "displayName": model, "status": "active",
				"capabilities": map[string]any{
					"modalities": []any{"text"}, "toolCalling": true, "streaming": true,
					"structuredOutput": true, "vision": false, "audio": false,
					"reasoning":       detail.HasReasoning,
					"contextWindow":   number(context),
					"maxOutputTokens": number(output),
				},
			})
		}
		provider := map[string]any{
			"displayName": "AstrLink (" + route.driver + ")", "driverKind": route.driver,
			"baseUrl": dimAgentAddress(route.protocol, connection.Origin),
			"auth":    map[string]any{"type": "api_key", "headerName": "Authorization", "prefix": "Bearer "},
			"headers": map[string]any{"User-Agent": "DimAgent"}, "models": entries, "enabled": true,
		}
		selected := hasChosen && slices.Contains(models, chosen)
		if native {
			provider["kind"] = "custom"
			provider["credential"] = connection.Token
		} else {
			provider["source"] = "custom"
			provider["credential"] = map[string]any{"type": "apiKey", "apiKey": connection.Token}
			provider["modelsSource"] = "user"
			provider["activeModelId"] = models[0]
			if selected {
				provider["activeModelId"] = chosen
			}
		}
		keys = append(keys, cc.Setting{Key: "providers." + route.id, Value: format.Encode(provider)})
		if selected {
			keys = append(keys, cc.Setting{Key: "selection", Value: format.Encode(map[string]any{"providerId": route.id, "modelId": chosen})})
		}
	}
	return keys
}

// BaseURLKey is unused: SyncedKeys moves each provider.
func (dimAgent) BaseURLKey() string { return "" }

func (dimAgent) SyncedKeys(config *cc.Config, record *cc.Record, origin string) []cc.Setting {
	var keys []cc.Setting
	for _, route := range dimAgentRoutes {
		key := "providers." + route.id
		if _, written := record.Keys[key]; !written {
			continue
		}
		provider, ok := configObject(config, key)
		if !ok {
			continue
		}
		provider["baseUrl"] = dimAgentAddress(route.protocol, origin)
		keys = append(keys, cc.Setting{Key: key, Value: format.Encode(provider)})
	}
	return keys
}

func (dimAgent) Notes(agents.Home, string, *cc.Config) []string { return []string{"restart"} }

func (dimAgentDatabase) Format() format.Format {
	return format.JSONFormat{Objects: []string{"providers"}}
}

func (dimAgentDatabase) Read(path string) (*string, error) { return dimAgentRead(path) }

func (dimAgentDatabase) Write(path string, before, after *string, _ bool) error {
	return dimAgentWrite(path, before, after)
}

// Preview hides custom headers and proxy settings, which may carry
// credentials under names the general preview cannot recognize.
func (dimAgentDatabase) Preview(text string) string {
	value, err := format.DecodeJSON(text)
	if err != nil {
		return ""
	}
	if root, ok := value.(map[string]any); ok {
		providers, _ := root["providers"].(map[string]any)
		for _, entry := range providers {
			provider, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if headers, ok := provider["headers"].(map[string]any); ok {
				for name := range headers {
					headers[name] = "…"
				}
			}
			for _, key := range []string{"proxy", "network", "accountUsageSettings", "metadata"} {
				if _, ok := provider[key]; ok {
					provider[key] = "…"
				}
			}
		}
	}
	return format.EncodePretty(value)
}

// Get reads each provider and the selection as compact JSON.
func (dimAgentDatabase) Get(document format.Document, key string) (string, bool, bool) {
	json, ok := document.(*format.JSONDocument)
	if !ok {
		return "", false, false
	}
	return getWhole(json, key)
}

func (dimAgentDatabase) Set(document format.Document, key, value string) error {
	json, ok := document.(*format.JSONDocument)
	if !ok {
		return errDimAgentSettings
	}
	if setWhole(json, key, value, format.NoRank) != nil {
		return errDimAgentSettings
	}
	return nil
}

func (dimAgentDatabase) Emptied() []string { return []string{"providers"} }
