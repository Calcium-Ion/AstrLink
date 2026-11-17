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

// Qoder CLI reads custom providers from `providers` in
// `~/.qoder/settings.json` (comments allowed) and the model it starts with
// from `model.name`, as `<provider>/<model>`. A provider has one
// `protocol`, so AstrLink adds one per protocol its models use. Qoder uses
// them only for an account signed in with custom models allowed.

var QoderCLI = cc.New(qoderCLI{})

type qoderCLI struct{}

// qoderSettings is `settings.json`.
type qoderSettings struct{}

// qoderRoutes are each protocol's provider id, the name Qoder shows,
// `protocol`, and `authType`.
var qoderRoutes = []struct{ protocol, id, name, kind, auth string }{
	{"openai.chat", "local-chat", "AstrLink", "openai", "bearer"},
	{"openai.responses", "local-responses", "AstrLink Responses", "openai-responses", "bearer"},
	{"anthropic.messages", "local-anthropic", "AstrLink Anthropic", "anthropic", "api-key"},
}

const (
	qoderPrefix   = "providers."
	qoderModelKey = "model.name"
)

// qoderEfforts are the thinking levels Qoder takes.
var qoderEfforts = []string{"low", "medium", "high", "xhigh", "max"}

func qoderOurs(id string) bool {
	for _, route := range qoderRoutes {
		if route.id == id {
			return true
		}
	}
	return false
}

func qoderRank(name string) int {
	order := []string{"type", "displayName", "protocol", "authType", "baseUrl", "apiKey", "model", "models", "contextWindow", "maxOutputTokens", "capabilities", "tools", "thinking", "modes", "preferredMode", "supportsEffort", "supportedEffortLevels"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

func qoderModel(id string, details cc.Catalog) map[string]any {
	capabilities := map[string]any{"tools": true}
	entry := map[string]any{"model": id}
	if d, ok := details[id]; ok {
		var levels []any
		for _, level := range d.Reasoning {
			if slices.Contains(qoderEfforts, level) {
				levels = append(levels, level)
			}
		}
		if len(levels) > 0 {
			capabilities["thinking"] = map[string]any{
				"modes":                 []any{"enabled"},
				"supportsEffort":        true,
				"supportedEffortLevels": levels,
			}
		}
		if d.ContextWindow != nil {
			entry["contextWindow"] = number(*d.ContextWindow)
		}
		if d.MaxTokens != nil {
			entry["maxOutputTokens"] = number(*d.MaxTokens)
		}
	}
	entry["capabilities"] = capabilities
	return entry
}

func (qoderCLI) ID() string        { return "qoder_cli" }
func (qoderCLI) Name() string      { return "Qoder CLI" }
func (qoderCLI) ListsModels() bool { return true }
func (qoderCLI) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}

// Dir: `QODER_CONFIG_DIR` and `--config-dir` are out of reach.
func (qoderCLI) Dir(home agents.Home, _ string) string { return home.Join(".qoder") }

func (qoderCLI) Files() []cc.File { return []cc.File{qoderSettings{}} }

func (q qoderCLI) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	return []string{filepath.Join(q.Dir(home, target), "settings.json")}
}

func (qoderCLI) ConnectionKeys() []string { return nil }

// HoldsConnection: each provider holds an address and the token.
func (qoderCLI) HoldsConnection(key string) bool {
	id, ok := strings.CutPrefix(key, qoderPrefix)
	return ok && qoderOurs(id)
}

func (qoderCLI) CurrentModels(config *cc.Config) cc.CurrentModels {
	var ids []string
	for _, route := range qoderRoutes {
		provider, ok := configObject(config, qoderPrefix+route.id)
		if !ok {
			continue
		}
		listed, _ := provider["models"].([]any)
		for _, model := range listed {
			name, _ := field(model, "model")
			if id, ok := cc.CleanModel(name); ok && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	var models cc.CurrentModels
	if chosen, ok := config.String(qoderModelKey); ok {
		if provider, model, ok := strings.Cut(chosen, "/"); ok && qoderOurs(provider) {
			if model, ok := cc.CleanModel(model); ok {
				models.Model = &model
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

func (qoderCLI) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	list := connection.ModelList()
	chosen, hasChosen := connection.Model("model")
	var keys []cc.Setting
	for _, route := range qoderRoutes {
		var used []string
		for _, model := range list {
			if connection.Protocol(QoderCLI, model) == route.protocol {
				used = append(used, model)
			}
		}
		if len(used) == 0 {
			continue
		}
		start := used[0]
		if hasChosen && slices.Contains(used, chosen) {
			start = chosen
		}
		entries := make([]any, len(used))
		for i, model := range used {
			entries[i] = qoderModel(model, details)
		}
		// Qoder adds `/messages` to an Anthropic URL ending in `/v1`.
		provider := map[string]any{
			"type":        "openai-compatible",
			"displayName": route.name,
			"protocol":    route.kind,
			"authType":    route.auth,
			"baseUrl":     QoderCLI.BaseURL(connection.Origin),
			"apiKey":      connection.Token,
			"model":       start,
			"models":      entries,
		}
		keys = append(keys, cc.Setting{Key: qoderPrefix + route.id, Value: format.Encode(provider)})
	}
	if hasChosen {
		protocol := connection.Protocol(QoderCLI, chosen)
		for _, route := range qoderRoutes {
			if route.protocol == protocol {
				keys = append(keys, cc.Setting{Key: qoderModelKey, Value: route.id + "/" + chosen})
				break
			}
		}
	}
	return keys
}

// BaseURLKey is unused: SyncedKeys points every provider elsewhere.
func (qoderCLI) BaseURLKey() string { return "" }

func (q qoderCLI) SyncedKeys(config *cc.Config, record *cc.Record, origin string) []cc.Setting {
	var keys []cc.Setting
	for _, key := range slices.Sorted(maps.Keys(record.Keys)) {
		if !q.HoldsConnection(key) {
			continue
		}
		provider, ok := configObject(config, key)
		if !ok {
			continue
		}
		provider["baseUrl"] = QoderCLI.BaseURL(origin)
		keys = append(keys, cc.Setting{Key: key, Value: format.Encode(provider)})
	}
	return keys
}

func (qoderCLI) Notes(agents.Home, string, *cc.Config) []string { return []string{"account"} }

func (qoderSettings) Format() format.Format {
	return format.JSONFormat{Syntax: &format.JSONCTrailing, Objects: []string{"providers", "model"}}
}

func (qoderSettings) Secret() bool { return true }

// Get reads a provider as one JSON object.
func (qoderSettings) Get(document format.Document, key string) (string, bool, bool) {
	if json, ok := document.(*format.JSONDocument); ok && strings.HasPrefix(key, qoderPrefix) {
		return getWhole(json, key)
	}
	return document.Get(key)
}

func (qoderSettings) Set(document format.Document, key, value string) error {
	if json, ok := document.(*format.JSONDocument); ok && strings.HasPrefix(key, qoderPrefix) {
		return setWhole(json, key, value, qoderRank)
	}
	return document.Set(key, value)
}

// Remove drops an emptied `providers` or `model`.
func (qoderSettings) Remove(document format.Document, key string) {
	document.Remove(key)
	if json, ok := document.(*format.JSONDocument); ok {
		json.DropEmpty([]string{"providers", "model"})
	}
}
