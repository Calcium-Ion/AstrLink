package clients

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Pi reads its `astrlink` provider and model list from `models.json` (with
// line comments and trailing commas) and the model it starts with from
// `settings.json`, both in its agent directory.

var Pi = cc.New(pi{})

const (
	PiProvider       = "astrlink"
	piProviderPrefix = "providers.astrlink."
	// piModelsKey is Pi's model list for the provider, written as
	// `[{"id": <model>}]` plus each model's reasoning levels and limits when
	// the official catalog lists them.
	piModelsKey = "providers.astrlink.models"
)

// piStartupKeys are the model Pi starts with, which its `/model` picker
// can save over.
var piStartupKeys = []string{"defaultProvider", "defaultModel"}

type pi struct{}

// piModels is `models.json`: Pi's endpoints.
type piModels struct{}

// piSettings is `settings.json`: Pi's preferences, including the model it
// starts with.
type piSettings struct{}

// piListValue is a model list's value. A list of one entry keeps the form
// configs from released builds were recorded with: the id alone for
// `[{"id": <model>}]`, else the entry.
func piListValue(value any) string {
	if list, ok := value.([]any); ok && len(list) == 1 {
		if fields, ok := list[0].(map[string]any); ok && len(fields) == 1 {
			if id, ok := fields["id"].(string); ok {
				return id
			}
		}
		return format.Encode(list[0])
	}
	return format.Encode(value)
}

// piListJSON is the JSON a model list's value stands for.
func piListJSON(value string) any {
	parsed, err := format.DecodeJSON(value)
	if err == nil {
		switch parsed.(type) {
		case []any:
			if strings.HasPrefix(value, "[") {
				return parsed
			}
		case map[string]any:
			if strings.HasPrefix(value, "{") {
				return []any{parsed}
			}
		}
	}
	return []any{map[string]any{"id": value}}
}

// piEntry is Pi's entry for a model, its levels in Pi's order.
func piEntry(id string, details *cc.ModelDetails) map[string]any {
	entry := map[string]any{"id": id}
	if details == nil {
		return entry
	}
	if details.HasReasoning {
		entry["reasoning"] = true
		if len(details.Reasoning) > 0 {
			levels := map[string]any{}
			for _, level := range cc.PiThinkingLevels {
				effort := level
				if level == "off" {
					effort = "none"
				}
				levels[level] = nil
				if slices.Contains(details.Reasoning, effort) {
					levels[level] = effort
				}
			}
			entry["thinkingLevelMap"] = levels
		}
	}
	if details.ContextWindow != nil {
		entry["contextWindow"] = number(*details.ContextWindow)
	}
	if details.MaxTokens != nil {
		entry["maxTokens"] = number(*details.MaxTokens)
	}
	return entry
}

func (pi) ID() string          { return "pi" }
func (pi) Name() string        { return "Pi" }
func (pi) Protocols() []string { return []string{"openai.responses"} }
func (pi) ListsModels() bool   { return true }
func (pi) TakesLimits() bool   { return true }

// Dir is its moved agent directory, or `~/.pi`.
func (pi) Dir(home agents.Home, _ string) string { return home.PiDir() }

func (pi) Detected(home agents.Home, _ string) bool { return home.PiDetected() }

func (pi) Files() []cc.File { return []cc.File{piModels{}, piSettings{}} }

func (pi) ConfigPaths(home agents.Home, _ string, _ *cc.Record) []string {
	agent := home.PiAgentDir()
	return []string{filepath.Join(agent, "models.json"), filepath.Join(agent, "settings.json")}
}

func (pi) ConnectionKeys() []string {
	return []string{"providers.astrlink.baseUrl", "providers.astrlink.api", "providers.astrlink.apiKey"}
}

// CurrentModels is the model Pi starts with on AstrLink, else the first
// AstrLink's list offers.
func (pi) CurrentModels(config *cc.Config) cc.CurrentModels {
	var entries []cc.ModelEntry
	if value, ok := config.String(piModelsKey); ok {
		entries = cc.ModelEntries(piListJSON(value))
	}
	var model *string
	if provider, _ := config.String("defaultProvider"); provider == PiProvider {
		if m, ok := config.Model("defaultModel"); ok {
			model = &m
		}
	}
	if model == nil && len(entries) > 0 {
		model = cc.Some(entries[0].ID)
	}
	limit := func(field string) *uint64 {
		if model == nil {
			return nil
		}
		i := slices.IndexFunc(entries, func(entry cc.ModelEntry) bool { return entry.ID == *model })
		if i < 0 {
			return nil
		}
		object, _ := entries[i].Entry.(map[string]any)
		n, ok := object[field].(format.Number)
		if !ok {
			return nil
		}
		tokens, err := strconv.ParseUint(string(n), 10, 64)
		if err != nil || tokens < 1 || tokens > cc.MaxPiLimit {
			return nil
		}
		return &tokens
	}
	return cc.CurrentModels{
		Model:         model,
		Models:        cc.ModelIDs(entries),
		ContextWindow: limit("contextWindow"),
		MaxTokens:     limit("maxTokens"),
	}
}

func (pi) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	var keys []cc.Setting
	// The display name is local UI in Pi's model picker.
	for _, kv := range [][2]string{
		{"name", "AstrLink"},
		{"baseUrl", Pi.BaseURL(connection.Origin)},
		{"api", "openai-responses"},
		{"apiKey", connection.Token},
	} {
		keys = append(keys, cc.Setting{Key: piProviderPrefix + kv[0], Value: kv[1]})
	}
	if model, ok := connection.Model("model"); ok {
		entries := []any{}
		for _, id := range connection.ModelList() {
			var d *cc.ModelDetails
			if found, ok := details[id]; ok {
				d = &found
			}
			entries = append(entries, piEntry(id, d))
		}
		keys = append(keys,
			cc.Setting{Key: piModelsKey, Value: piListValue(entries)},
			cc.Setting{Key: "defaultProvider", Value: PiProvider},
			cc.Setting{Key: "defaultModel", Value: model})
	}
	return keys
}

func (pi) BaseURLKey() string { return "providers.astrlink.baseUrl" }

// Format is JSON with line comments and trailing commas, which Pi strips.
func (piModels) Format() format.Format {
	return format.JSONFormat{Syntax: &format.JSONCTrailing, Objects: []string{"providers"}}
}

func (piModels) Holds(key string) bool { return !slices.Contains(piStartupKeys, key) }

func (p piModels) Group(document format.Document) []format.KeyValue {
	var keys []format.KeyValue
	if json, ok := document.(*format.JSONDocument); ok {
		json.ObjectKeys(piProviderPrefix, nil, func(path string) (string, bool) {
			value, ok, _ := p.Get(document, path)
			return value, ok
		}, &keys)
	}
	return keys
}

func (piModels) Get(document format.Document, key string) (string, bool, bool) {
	if json, ok := document.(*format.JSONDocument); ok && key == piModelsKey {
		value, ok, present := json.GetJSON(key)
		if !ok {
			return "", false, present
		}
		return piListValue(value), true, true
	}
	return document.Get(key)
}

func (piModels) Set(document format.Document, key, value string) error {
	if json, ok := document.(*format.JSONDocument); ok && key == piModelsKey {
		return json.SetJSON(key, piListJSON(value), cc.EntryRank)
	}
	return document.Set(key, value)
}

// Emptied keeps an emptied `providers`, which Pi requires.
func (piModels) Emptied() []string { return []string{"providers.astrlink"} }

// IsEmpty lets an emptied `providers` stay until the file goes.
func (piModels) IsEmpty(document format.Document) bool {
	if json, ok := document.(*format.JSONDocument); ok {
		return json.HoldsOnly(func(p format.JSONProperty) bool {
			return p.HasName && p.Name == "providers" && p.IsEmptyObject()
		})
	}
	return document.IsEmpty()
}

func (piSettings) Format() format.Format { return format.JSONFormat{} }

func (piSettings) Holds(key string) bool { return slices.Contains(piStartupKeys, key) }

// Group is the startup model alone, the only part AstrLink takes over.
func (s piSettings) Group(document format.Document) []format.KeyValue {
	var keys []format.KeyValue
	for _, key := range piStartupKeys {
		cc.PushPresent(&keys, s, document, key)
	}
	return keys
}

// ReadPiModels reads Pi's models.json as Pi does, for importing the
// providers it configures.
func ReadPiModels(text string) (any, error) { return cc.ReadJSON(piModels{}, text) }
