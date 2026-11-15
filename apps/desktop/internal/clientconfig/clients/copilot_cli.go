package clients

import (
	"path/filepath"
	"slices"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Copilot CLI reads `~/.copilot/providers.json` for providers and their
// models, and starts with `model` from `~/.copilot/settings.json`. One
// provider has one wire API, so AstrLink adds a provider per protocol its
// models use, and a model entry for each model. Copilot tells the model
// `<provider>/<id>`, so the providers are named `local-<protocol>`.

var CopilotCLI = cc.New(copilotCLI{})

type copilotCLI struct{}

// copilotProviders is `providers.json`.
type copilotProviders struct{}

// copilotSettings is `settings.json`.
type copilotSettings struct{}

const (
	copilotProvidersKey = "providers"
	copilotModelsKey    = "models"
	copilotModelKey     = "model"
)

type copilotRoute struct{ protocol, name, kind, wire string }

// copilotRoutes are each protocol's provider name, `type`, and `wireApi`.
var copilotRoutes = []copilotRoute{
	{"openai.chat", "local-chat", "openai", "completions"},
	{"openai.responses", "local-responses", "openai", "responses"},
	{"anthropic.messages", "local-anthropic", "anthropic", ""},
}

func copilotRouteOf(protocol string) copilotRoute {
	if i := slices.IndexFunc(copilotRoutes, func(r copilotRoute) bool { return r.protocol == protocol }); i >= 0 {
		return copilotRoutes[i]
	}
	return copilotRoutes[0]
}

func copilotOurs(name string) bool {
	return slices.ContainsFunc(copilotRoutes, func(r copilotRoute) bool { return r.name == name })
}

// copilotProviderURL: Anthropic's SDK adds `/v1` itself; the OpenAI ones
// take it.
func copilotProviderURL(kind, origin string) string {
	if kind == "anthropic" {
		return origin
	}
	return origin + "/v1"
}

func copilotRank(name string) int {
	order := []string{"name", "id", "type", "provider", "wireApi", "baseUrl", "apiKey", "maxContextWindowTokens", "maxPromptTokens", "maxOutputTokens"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

// copilotOwnedIn picks AstrLink's entries in list, by the field that names
// their provider.
func copilotOwnedIn(list string) func(any) bool {
	name := "provider"
	if list == copilotProvidersKey {
		name = "name"
	}
	return func(entry any) bool {
		provider, ok := field(entry, name)
		return ok && copilotOurs(provider)
	}
}

func (copilotCLI) ID() string   { return "copilot_cli" }
func (copilotCLI) Name() string { return "Copilot CLI" }
func (copilotCLI) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}
func (copilotCLI) ListsModels() bool { return true }

// Dir is the default `~/.copilot`: `COPILOT_HOME` moves it from a shell,
// but AstrLink writes the default.
func (copilotCLI) Dir(home agents.Home, _ string) string {
	return filepath.Join(home.Dir(), ".copilot")
}

// Detected: VS Code keeps chats in `~/.copilot` too; the CLI writes
// `config.json` on its first run.
func (c copilotCLI) Detected(home agents.Home, target string) bool {
	return isFile(filepath.Join(c.Dir(home, target), "config.json"))
}

func (copilotCLI) Files() []cc.File { return []cc.File{copilotProviders{}, copilotSettings{}} }

func (c copilotCLI) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	dir := c.Dir(home, target)
	return []string{filepath.Join(dir, "providers.json"), filepath.Join(dir, "settings.json")}
}

func (copilotCLI) ConnectionKeys() []string { return nil }

// HoldsConnection: each provider holds an address and the token.
func (copilotCLI) HoldsConnection(key string) bool { return key == copilotProvidersKey }

func (copilotCLI) CurrentModels(config *cc.Config) cc.CurrentModels {
	entries := configList(config, copilotModelsKey)
	var ids []string
	for _, entry := range entries {
		id, _ := field(entry, "id")
		if id, ok := cc.CleanModel(id); ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	var models cc.CurrentModels
	if chosen, ok := config.String(copilotModelKey); ok {
		for _, entry := range entries {
			provider, hasProvider := field(entry, "provider")
			id, hasID := field(entry, "id")
			if hasProvider && hasID && provider+"/"+id == chosen {
				models.Model = cc.Some(id)
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

func (copilotCLI) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	list := connection.ModelList()
	var providers, models []any
	for _, route := range copilotRoutes {
		var used []string
		for _, id := range list {
			if connection.Protocol(CopilotCLI, id) == route.protocol {
				used = append(used, id)
			}
		}
		if len(used) == 0 {
			continue
		}
		provider := map[string]any{
			"name":    route.name,
			"type":    route.kind,
			"baseUrl": copilotProviderURL(route.kind, connection.Origin),
			"apiKey":  connection.Token,
		}
		if route.wire != "" {
			provider["wireApi"] = route.wire
		}
		providers = append(providers, provider)
		for _, id := range used {
			model := map[string]any{"id": id, "provider": route.name}
			if d, ok := details[id]; ok && d.ContextWindow != nil && d.MaxTokens != nil && *d.ContextWindow > *d.MaxTokens {
				model["maxContextWindowTokens"] = number(*d.ContextWindow)
				model["maxPromptTokens"] = number(*d.ContextWindow - *d.MaxTokens)
				model["maxOutputTokens"] = number(*d.MaxTokens)
			}
			models = append(models, model)
		}
	}
	var keys []cc.Setting
	if len(providers) > 0 {
		keys = append(keys,
			cc.Setting{Key: copilotProvidersKey, Value: format.Encode(providers)},
			cc.Setting{Key: copilotModelsKey, Value: format.Encode(models)})
	}
	if model, ok := connection.Model("model"); ok {
		route := copilotRouteOf(connection.Protocol(CopilotCLI, model))
		keys = append(keys, cc.Setting{Key: copilotModelKey, Value: route.name + "/" + model})
	}
	return keys
}

// BaseURLKey is unused: SyncedKeys points every provider elsewhere.
func (copilotCLI) BaseURLKey() string { return "" }

func (copilotCLI) SyncedKeys(config *cc.Config, _ *cc.Record, origin string) []cc.Setting {
	providers := configList(config, copilotProvidersKey)
	if len(providers) == 0 {
		return nil
	}
	for _, provider := range providers {
		if object, ok := provider.(map[string]any); ok {
			kind, _ := object["type"].(string)
			object["baseUrl"] = copilotProviderURL(kind, origin)
		}
	}
	return []cc.Setting{{Key: copilotProvidersKey, Value: format.Encode(providers)}}
}

// Format is strict JSON: a comment fails the whole file.
func (copilotProviders) Format() format.Format { return format.JSONFormat{} }

func (copilotProviders) Secret() bool { return true }

func (copilotProviders) Holds(key string) bool { return key != copilotModelKey }

// Get reads a list as AstrLink's entries in it, compact.
func (copilotProviders) Get(document format.Document, key string) (string, bool, bool) {
	json, ok := document.(*format.JSONDocument)
	if !ok {
		return "", false, false
	}
	return getOwned(json, key, copilotOwnedIn(key))
}

func (copilotProviders) Set(document format.Document, key, value string) error {
	json, ok := document.(*format.JSONDocument)
	if !ok {
		return document.Set(key, value)
	}
	return setOwned(json, key, value, copilotOwnedIn(key), copilotRank)
}

func (copilotProviders) Remove(document format.Document, key string) {
	if json, ok := document.(*format.JSONDocument); ok {
		json.RemoveItems(key, copilotOwnedIn(key))
	}
}

// Tidy drops emptied lists.
func (copilotProviders) Tidy(document format.Document) {
	if json, ok := document.(*format.JSONDocument); ok {
		dropEmptyList(json, copilotProvidersKey)
		dropEmptyList(json, copilotModelsKey)
	}
}

func (copilotSettings) Format() format.Format {
	return format.JSONFormat{Syntax: &format.JSONCTrailing}
}

func (copilotSettings) Holds(key string) bool { return key == copilotModelKey }
