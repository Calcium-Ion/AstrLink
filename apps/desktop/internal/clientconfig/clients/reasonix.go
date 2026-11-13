package clients

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Reasonix reads `~/.reasonix/config.toml`: `[[providers]]` with one
// `kind` each and the `default_model` it starts with. A provider's key
// comes only from `~/.reasonix/.env`, by the variable it names. AstrLink
// adds a provider per protocol its models use, all reading one variable,
// and lists them in the desktop app's provider list when the user keeps
// one.

var Reasonix = cc.New(reasonix{})

type reasonix struct{}

// reasonixConfig is `config.toml`.
type reasonixConfig struct{}

// reasonixEnv is `.env`.
type reasonixEnv struct{}

// reasonixRoutes are each protocol's provider name and `kind`.
var reasonixRoutes = []struct{ protocol, name, kind string }{
	{"openai.chat", "local-chat", "openai"},
	{"openai.responses", "local-responses", "responses"},
	{"anthropic.messages", "local-anthropic", "anthropic"},
}

const (
	reasonixProvidersKey = "providers"
	reasonixDefaultKey   = "default_model"
	// reasonixAccessKey: the desktop app's model switcher shows only the
	// providers listed here, when the list is there.
	reasonixAccessKey = "desktop.provider_access"
	reasonixKeyName   = "LOCAL_GATEWAY_API_KEY"
	// reasonixVersionKey is what Reasonix adds to the file on its first
	// run.
	reasonixVersionKey = "config_version"
)

func reasonixOurs(name string) bool {
	for _, route := range reasonixRoutes {
		if route.name == name {
			return true
		}
	}
	return false
}

func reasonixRank(name string) int {
	order := []string{"name", "kind", "base_url", "models", "default", "api_key_env", "model_overrides", "context_window", "max_output_tokens"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

func reasonixOwned(entry any) bool {
	name, ok := field(entry, "name")
	return ok && reasonixOurs(name)
}

// reasonixOursIn are AstrLink's provider names among names, in its own
// order.
func reasonixOursIn(names []string) []any {
	var ours []any
	for _, route := range reasonixRoutes {
		if slices.Contains(names, route.name) {
			ours = append(ours, route.name)
		}
	}
	return ours
}

func (reasonix) ID() string        { return "reasonix" }
func (reasonix) Name() string      { return "Reasonix" }
func (reasonix) ListsModels() bool { return true }
func (reasonix) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}

// Dir: `REASONIX_HOME` from a shell is out of reach.
func (reasonix) Dir(home agents.Home, _ string) string {
	if home.Windows() {
		return home.Join("AppData", "Roaming", "reasonix")
	}
	return home.Join(".reasonix")
}

func (reasonix) Files() []cc.File { return []cc.File{reasonixConfig{}, reasonixEnv{}} }

func (r reasonix) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	dir := r.Dir(home, target)
	return []string{filepath.Join(dir, "config.toml"), filepath.Join(dir, ".env")}
}

func (reasonix) ConnectionKeys() []string { return []string{reasonixKeyName} }

// HoldsConnection: the providers hold the addresses; `.env` the token.
func (reasonix) HoldsConnection(key string) bool {
	return key == reasonixProvidersKey || key == reasonixKeyName
}

func (reasonix) CurrentModels(config *cc.Config) cc.CurrentModels {
	var ids []string
	for _, entry := range configList(config, reasonixProvidersKey) {
		if !reasonixOwned(entry) {
			continue
		}
		object, _ := entry.(map[string]any)
		models, _ := object["models"].([]any)
		for _, model := range models {
			name, _ := model.(string)
			if id, ok := cc.CleanModel(name); ok && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	var models cc.CurrentModels
	if chosen, ok := config.String(reasonixDefaultKey); ok {
		if provider, model, ok := strings.Cut(chosen, "/"); ok && reasonixOurs(provider) {
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

func (reasonix) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	list := connection.ModelList()
	chosen, hasChosen := connection.Model("model")
	var entries, names []any
	var chosenDefault string
	for _, route := range reasonixRoutes {
		var used []any
		var start string
		for _, id := range list {
			if connection.Protocol(Reasonix, id) != route.protocol {
				continue
			}
			used = append(used, id)
			if start == "" || hasChosen && id == chosen && start != chosen {
				start = id
			}
		}
		if len(used) == 0 {
			continue
		}
		if hasChosen && start == chosen {
			chosenDefault = route.name + "/" + start
		}
		entry := map[string]any{
			"name":        route.name,
			"kind":        route.kind,
			"base_url":    Reasonix.BaseURL(connection.Origin),
			"models":      used,
			"default":     start,
			"api_key_env": reasonixKeyName,
		}
		overrides := map[string]any{}
		for _, id := range used {
			d, ok := details[id.(string)]
			if !ok {
				continue
			}
			limits := map[string]any{}
			if d.ContextWindow != nil {
				limits["context_window"] = number(*d.ContextWindow)
			}
			if d.MaxTokens != nil {
				limits["max_output_tokens"] = number(*d.MaxTokens)
			}
			if len(limits) > 0 {
				overrides[id.(string)] = limits
			}
		}
		if len(overrides) > 0 {
			entry["model_overrides"] = overrides
		}
		entries = append(entries, entry)
		names = append(names, route.name)
	}
	var keys []cc.Setting
	if chosenDefault != "" {
		keys = append(keys, cc.Setting{Key: reasonixDefaultKey, Value: chosenDefault})
	}
	if len(entries) > 0 {
		keys = append(keys,
			cc.Setting{Key: reasonixProvidersKey, Value: format.Encode(entries)},
			cc.Setting{Key: reasonixAccessKey, Value: format.Encode(names)})
	}
	return append(keys, cc.Setting{Key: reasonixKeyName, Value: connection.Token})
}

// BaseURLKey is unused: SyncedKeys points every provider elsewhere.
func (reasonix) BaseURLKey() string { return "" }

func (reasonix) SyncedKeys(config *cc.Config, _ *cc.Record, origin string) []cc.Setting {
	entries := configList(config, reasonixProvidersKey)
	if len(entries) == 0 {
		return nil
	}
	for _, entry := range entries {
		if object, ok := entry.(map[string]any); ok {
			object["base_url"] = Reasonix.BaseURL(origin)
		}
	}
	return []cc.Setting{{Key: reasonixProvidersKey, Value: format.Encode(entries)}}
}

// reasonixAccess is the desktop app's provider list, when the user keeps
// one.
func reasonixAccess(toml *format.TOMLDocument) *format.TOMLValue {
	desktop := format.TableLike(toml.Root().Get("desktop"))
	if desktop == nil {
		return nil
	}
	list, ok := desktop.Get("provider_access").(*format.TOMLValue)
	if !ok {
		return nil
	}
	if _, isArray := list.Items(); !isArray {
		return nil
	}
	return list
}

func (reasonixConfig) Format() format.Format { return format.TOMLFormat{} }

func (reasonixConfig) Holds(key string) bool { return key != reasonixKeyName }

// Get reads the providers as AstrLink's tables, and the desktop list as
// AstrLink's names in it; without one, every provider is listed.
func (reasonixConfig) Get(document format.Document, key string) (string, bool, bool) {
	toml, ok := document.(*format.TOMLDocument)
	if !ok {
		return "", false, false
	}
	switch key {
	case reasonixProvidersKey:
		return getOwnedTables(toml, key, reasonixOwned)
	case reasonixAccessKey:
		var listed []string
		if access := reasonixAccess(toml); access != nil {
			items, _ := access.Items()
			for _, item := range items {
				if name, ok := item.Str(); ok {
					listed = append(listed, name)
				}
			}
		} else if tables := arrayOfTables(toml, reasonixProvidersKey); tables != nil {
			for _, table := range tables.Tables {
				if name, ok := format.TOMLItemString(table.Get("name")); ok {
					listed = append(listed, name)
				}
			}
		}
		names := reasonixOursIn(listed)
		if len(names) == 0 {
			return "", false, false
		}
		return format.Encode(names), true, true
	}
	return document.Get(key)
}

// Set puts AstrLink's tables after the user's; its names join a desktop
// list the user keeps.
func (reasonixConfig) Set(document format.Document, key, value string) error {
	if key != reasonixProvidersKey && key != reasonixAccessKey {
		return document.Set(key, value)
	}
	toml, ok := document.(*format.TOMLDocument)
	if !ok {
		return fmt.Errorf("%s is not TOML", key)
	}
	if key == reasonixProvidersKey {
		dropOwnedTables(toml, key, reasonixOwned)
		return setOwnedTables(toml, key, value, reasonixOwned, reasonixRank)
	}
	decoded, err := format.DecodeJSON(value)
	list, isList := decoded.([]any)
	if err != nil || !isList {
		return fmt.Errorf("%s is not a list", key)
	}
	var wanted []string
	for _, name := range list {
		if name, ok := name.(string); ok {
			wanted = append(wanted, name)
		}
	}
	access := reasonixAccess(toml)
	if access == nil {
		return nil
	}
	access.RetainItems(func(item *format.TOMLValue) bool {
		name, ok := item.Str()
		return !ok || !reasonixOurs(name) || slices.Contains(wanted, name)
	})
	for _, name := range wanted {
		items, _ := access.Items()
		if !slices.ContainsFunc(items, func(item *format.TOMLValue) bool {
			known, ok := item.Str()
			return ok && known == name
		}) {
			access.Push(format.NewTOMLString(name))
		}
	}
	return nil
}

func (reasonixConfig) Remove(document format.Document, key string) {
	toml, ok := document.(*format.TOMLDocument)
	if !ok || key != reasonixProvidersKey && key != reasonixAccessKey {
		document.Remove(key)
		return
	}
	if key == reasonixProvidersKey {
		dropOwnedTables(toml, key, reasonixOwned)
	} else if access := reasonixAccess(toml); access != nil {
		access.RetainItems(func(item *format.TOMLValue) bool {
			name, ok := item.Str()
			return !ok || !reasonixOurs(name)
		})
	}
}

// IsEmpty lets the version Reasonix stamped on a file AstrLink created go
// with the rest.
func (reasonixConfig) IsEmpty(document format.Document) bool {
	toml, ok := document.(*format.TOMLDocument)
	if !ok {
		return document.IsEmpty()
	}
	for _, name := range toml.Root().Names() {
		if name != reasonixVersionKey {
			return false
		}
	}
	return true
}

func (reasonixEnv) Format() format.Format { return format.EnvFormat{} }

func (reasonixEnv) Secret() bool { return true }

func (reasonixEnv) Holds(key string) bool { return key == reasonixKeyName }
