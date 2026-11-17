package clients

import (
	"fmt"
	"path/filepath"
	"reflect"
	"slices"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// ZCode, desktop app and bundled CLI alike, reads
// `~/.zcode/v2/provider_config.json`. A provider rule has one API type, so
// AstrLink adds one per protocol its models use, a model rule for each
// model, and picks the default in `defaultModelSelection`. ZCode checks the
// file strictly and reads one that fails as empty. It tells the model
// `<provider id>/<model>`.

var ZCode = cc.New(zCode{})

type zCode struct{}

// zCodeProviderConfig is `provider_config.json`.
type zCodeProviderConfig struct{}

// zCodeRoutes are each protocol's provider id, the name ZCode shows, and
// `api.type`.
var zCodeRoutes = []struct{ protocol, id, name, kind string }{
	{"openai.chat", "local-chat", "AstrLink", "openai-chat-completions"},
	{"openai.responses", "local-responses", "AstrLink Responses", "openai-responses"},
	{"anthropic.messages", "local-anthropic", "AstrLink Anthropic", "anthropic-messages"},
}

const (
	zCodeProvidersKey = "config.providerConfigRules.providerRules"
	zCodeModelsKey    = "config.modelConfigRules.providerModelRules"
	zCodeOrderKey     = "config.providerOrder"
	zCodeDefaultKey   = "config.defaultModelSelection"
	// zCodeEmpty is what ZCode itself starts the file with.
	zCodeEmpty = `{
  "schemaVersion": 1,
  "config": {
    "providerOrder": [],
    "providerConfigRules": {
      "providerRules": []
    },
    "modelConfigRules": {
      "providerModelRules": [],
      "manualProviderModelRules": []
    }
  }
}
`
)

func zCodeOurs(id string) bool {
	for _, route := range zCodeRoutes {
		if route.id == id {
			return true
		}
	}
	return false
}

func zCodeOwned(entry any) bool {
	id, ok := field(entry, "providerId")
	return ok && zCodeOurs(id)
}

func zCodeOurID(entry any) bool {
	id, ok := entry.(string)
	return ok && zCodeOurs(id)
}

func zCodeRank(name string) int {
	order := []string{"providerId", "modelId", "templateId", "providerName", "enabled", "config", "group", "access", "type", "apiKey", "api", "baseUrl", "headers", "personalModelIds", "modelOrder", "visibility", "properties", "contextWindow", "supportsToolCall", "optionSpecs", "max", "options", "reasoningLevel"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

// zCodeOursIn are AstrLink's provider ids among ids, in its own order.
func zCodeOursIn(ids []string) []any {
	var ours []any
	for _, route := range zCodeRoutes {
		if slices.Contains(ids, route.id) {
			ours = append(ours, route.id)
		}
	}
	return ours
}

// zCodeSameRule reports whether entry is the rule wanted is: the same
// provider, and the same model for a model rule.
func zCodeSameRule(entry, wanted any) bool {
	a, _ := entry.(map[string]any)
	b, _ := wanted.(map[string]any)
	return reflect.DeepEqual(a["providerId"], b["providerId"]) && reflect.DeepEqual(a["modelId"], b["modelId"])
}

func (zCode) ID() string        { return "zcode" }
func (zCode) Name() string      { return "ZCode" }
func (zCode) ListsModels() bool { return true }
func (zCode) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}

// Dir: `ZCODE_DATA_BASE_DIR` and another data folder set in ZCode are out
// of reach.
func (zCode) Dir(home agents.Home, _ string) string { return home.Join(".zcode", "v2") }

func (zCode) Files() []cc.File { return []cc.File{zCodeProviderConfig{}} }

func (z zCode) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	return []string{filepath.Join(z.Dir(home, target), "provider_config.json")}
}

func (zCode) ConnectionKeys() []string { return nil }

// HoldsConnection: each provider rule holds an address and the token.
func (zCode) HoldsConnection(key string) bool { return key == zCodeProvidersKey }

func (zCode) CurrentModels(config *cc.Config) cc.CurrentModels {
	var ids []string
	for _, rule := range configList(config, zCodeProvidersKey) {
		object, _ := rule.(map[string]any)
		ruleConfig, _ := object["config"].(map[string]any)
		personal, _ := ruleConfig["personalModelIds"].([]any)
		for _, id := range personal {
			name, _ := id.(string)
			if id, ok := cc.CleanModel(name); ok && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	var models cc.CurrentModels
	if chosen, ok := configObject(config, zCodeDefaultKey); ok && zCodeOwned(chosen) {
		if model, ok := field(chosen, "modelId"); ok {
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

func (zCode) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	list := connection.ModelList()
	chosen, hasChosen := connection.Model("model")
	var providers, models, ids []any
	var chosenDefault map[string]any
	for _, route := range zCodeRoutes {
		var used []any
		for _, model := range list {
			if connection.Protocol(ZCode, model) == route.protocol {
				used = append(used, model)
			}
		}
		if len(used) == 0 {
			continue
		}
		providers = append(providers, map[string]any{
			"providerId":   route.id,
			"templateId":   nil,
			"providerName": route.name,
			"enabled":      true,
			"config": map[string]any{
				"group":            "standard-personal",
				"access":           map[string]any{"type": "api-key", "apiKey": connection.Token},
				"api":              map[string]any{"type": route.kind, "baseUrl": ZCode.BaseURL(connection.Origin), "headers": map[string]any{}},
				"personalModelIds": used,
				"modelOrder":       used,
				"visibility":       "visible",
			},
		})
		for _, item := range used {
			model := item.(string)
			d, known := details[model]
			if hasChosen && model == chosen {
				// ZCode skips a default without a level its model offers:
				// an effort for Claude's, on or off for the others.
				level := "disabled"
				switch {
				case route.protocol == "anthropic.messages":
					level = "high"
				case known && len(d.Reasoning) > 0:
					level = "enabled"
				}
				chosenDefault = map[string]any{
					"providerId": route.id,
					"modelId":    model,
					"options":    map[string]any{"reasoningLevel": level},
				}
			}
			properties := map[string]any{"supportsToolCall": true}
			ruleConfig := map[string]any{"enabled": true, "properties": properties}
			if known && d.ContextWindow != nil {
				properties["contextWindow"] = number(*d.ContextWindow)
			}
			if known && d.MaxTokens != nil {
				ruleConfig["optionSpecs"] = map[string]any{"maxOutputTokens": map[string]any{"max": number(*d.MaxTokens)}}
			}
			models = append(models, map[string]any{"providerId": route.id, "modelId": model, "config": ruleConfig})
		}
		ids = append(ids, route.id)
	}
	var keys []cc.Setting
	if len(providers) > 0 {
		keys = append(keys,
			cc.Setting{Key: zCodeProvidersKey, Value: format.Encode(providers)},
			cc.Setting{Key: zCodeModelsKey, Value: format.Encode(models)},
			cc.Setting{Key: zCodeOrderKey, Value: format.Encode(ids)})
	}
	if chosenDefault != nil {
		keys = append(keys, cc.Setting{Key: zCodeDefaultKey, Value: format.Encode(chosenDefault)})
	}
	return keys
}

func (zCode) StartFile(config *cc.Config) *cc.FileError {
	jsonFormat, _ := zCodeProviderConfig{}.Format().(format.JSONFormat)
	empty, err := format.ParseJSON(zCodeEmpty, jsonFormat)
	if err != nil {
		return &cc.FileError{Message: err.Error()}
	}
	config.Documents[0] = empty
	return nil
}

// BaseURLKey is unused: SyncedKeys points every provider elsewhere.
func (zCode) BaseURLKey() string { return "" }

func (zCode) SyncedKeys(config *cc.Config, _ *cc.Record, origin string) []cc.Setting {
	providers := configList(config, zCodeProvidersKey)
	if len(providers) == 0 {
		return nil
	}
	for _, rule := range providers {
		object, _ := rule.(map[string]any)
		ruleConfig, ok := object["config"].(map[string]any)
		if !ok {
			ruleConfig = map[string]any{}
			if object != nil {
				object["config"] = ruleConfig
			}
		}
		api, ok := ruleConfig["api"].(map[string]any)
		if !ok {
			api = map[string]any{}
			ruleConfig["api"] = api
		}
		api["baseUrl"] = ZCode.BaseURL(origin)
	}
	return []cc.Setting{{Key: zCodeProvidersKey, Value: format.Encode(providers)}}
}

// Format is strict JSON: ZCode reads a file that fails its check as empty.
func (zCodeProviderConfig) Format() format.Format {
	return format.JSONFormat{Objects: []string{"config", "config.providerConfigRules", "config.modelConfigRules", "config.defaultModelSelection"}}
}

func (zCodeProviderConfig) Secret() bool { return true }

// Get reads a list as AstrLink's rules in it, and the provider order as
// AstrLink's ids in it; without one, every provider is in order.
func (zCodeProviderConfig) Get(document format.Document, key string) (string, bool, bool) {
	json, ok := document.(*format.JSONDocument)
	if !ok {
		return "", false, false
	}
	switch key {
	case zCodeProvidersKey, zCodeModelsKey:
		return getOwned(json, key, zCodeOwned)
	case zCodeOrderKey:
		var listed []string
		value, _, _ := json.GetJSON(key)
		if order, isList := value.([]any); isList {
			for _, id := range order {
				if id, ok := id.(string); ok {
					listed = append(listed, id)
				}
			}
		} else {
			rules, _, _ := json.GetJSON(zCodeProvidersKey)
			items, _ := rules.([]any)
			for _, rule := range items {
				if id, ok := field(rule, "providerId"); ok {
					listed = append(listed, id)
				}
			}
		}
		ids := zCodeOursIn(listed)
		if len(ids) == 0 {
			return "", false, false
		}
		return format.Encode(ids), true, true
	case zCodeDefaultKey:
		return getWhole(json, key)
	}
	return document.Get(key)
}

// Set puts AstrLink's rules after the user's; its ids join an order the
// user keeps.
func (zCodeProviderConfig) Set(document format.Document, key, value string) error {
	json, ok := document.(*format.JSONDocument)
	if !ok {
		return document.Set(key, value)
	}
	decoded, err := format.DecodeJSON(value)
	if err != nil {
		return err
	}
	switch key {
	case zCodeProvidersKey, zCodeModelsKey:
		rules, isList := decoded.([]any)
		if !isList {
			return fmt.Errorf("%s is not a list", key)
		}
		json.RemoveItems(key, func(entry any) bool {
			return zCodeOwned(entry) && !slices.ContainsFunc(rules, func(rule any) bool { return zCodeSameRule(entry, rule) })
		})
		if err := json.PrependItems(key, nil, zCodeRank); err != nil {
			return err
		}
		for _, rule := range rules {
			if err := json.PutItem(key, func(entry any) bool { return zCodeSameRule(entry, rule) }, rule, zCodeRank); err != nil {
				return err
			}
		}
		return nil
	case zCodeOrderKey:
		ids, isList := decoded.([]any)
		if !isList {
			return fmt.Errorf("%s is not a list", key)
		}
		current, _, _ := json.GetJSON(key)
		order, isList := current.([]any)
		if !isList {
			return nil
		}
		json.RemoveItems(key, func(entry any) bool { return zCodeOurID(entry) && !slices.Contains(ids, entry) })
		for _, id := range ids {
			if !slices.Contains(order, id) {
				if err := json.PutItem(key, func(entry any) bool { return entry == id }, id, zCodeRank); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return json.SetJSON(key, decoded, zCodeRank)
}

func (zCodeProviderConfig) Remove(document format.Document, key string) {
	json, ok := document.(*format.JSONDocument)
	switch {
	case ok && (key == zCodeProvidersKey || key == zCodeModelsKey):
		json.RemoveItems(key, zCodeOwned)
	case ok && key == zCodeOrderKey:
		json.RemoveItems(key, zCodeOurID)
	default:
		document.Remove(key)
	}
}

// IsEmpty lets what ZCode starts a file with go with the rest.
func (zCodeProviderConfig) IsEmpty(document format.Document) bool {
	json, ok := document.(*format.JSONDocument)
	if !ok {
		return document.IsEmpty()
	}
	blankList := func(value any) bool {
		items, ok := value.([]any)
		return ok && len(items) == 0
	}
	blank := func(value any) bool {
		switch v := value.(type) {
		case []any:
			return len(v) == 0
		case map[string]any:
			for _, inner := range v {
				if !blankList(inner) {
					return false
				}
			}
			return true
		}
		return false
	}
	value, _ := json.Value()
	file, isObject := value.(map[string]any)
	if !isObject {
		return false
	}
	for name, value := range file {
		switch name {
		case "schemaVersion":
		case "config":
			fields, ok := value.(map[string]any)
			if !ok {
				return false
			}
			for _, inner := range fields {
				if !blank(inner) {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}
