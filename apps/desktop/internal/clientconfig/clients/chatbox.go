package clients

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Chatbox keeps its settings in `config.json` in its data folder: custom
// providers in `settings.customProviders`, each one's address and models in
// `settings.providers`, and the chat model in `settings.defaultChatModel`.
// A provider has one `type`, so AstrLink adds one per protocol its models
// use. Chatbox writes its whole settings back while it runs, so AstrLink
// edits the file only while Chatbox is closed, and only once Chatbox has
// made it.

var Chatbox = cc.New(chatbox{})

type chatbox struct{}

// chatboxConfig is `config.json`.
type chatboxConfig struct{}

// chatboxRoutes are each protocol's provider id, the name Chatbox shows,
// and `type`.
var chatboxRoutes = []struct{ protocol, id, name, kind string }{
	{"openai.chat", "local-chat", "AstrLink", "openai"},
	{"openai.responses", "local-responses", "AstrLink Responses", "openai-responses"},
	{"anthropic.messages", "local-anthropic", "AstrLink Anthropic", "claude"},
}

const (
	chatboxListKey    = "settings.customProviders"
	chatboxPrefix     = "settings.providers."
	chatboxDefaultKey = "settings.defaultChatModel"
)

func chatboxOurs(id string) bool {
	for _, route := range chatboxRoutes {
		if route.id == id {
			return true
		}
	}
	return false
}

func chatboxOwned(entry any) bool {
	id, ok := field(entry, "id")
	return ok && chatboxOurs(id)
}

func chatboxRank(name string) int {
	order := []string{"id", "name", "type", "isCustom", "apiHost", "apiKey", "models", "modelId", "capabilities", "contextWindow", "maxOutput", "provider", "model"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

func sameID(a, b any) bool {
	x, _ := a.(map[string]any)
	y, _ := b.(map[string]any)
	return reflect.DeepEqual(x["id"], y["id"])
}

// chatboxLocked reports Chromium's lock in the data folder while Chatbox
// runs: a link to `<host>-<pid>` on macOS and Linux, a file gone on exit on
// Windows.
func chatboxLocked(dir string, windows bool) bool {
	if windows {
		_, err := os.Stat(filepath.Join(dir, "lockfile"))
		return err == nil
	}
	target, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
	if err != nil {
		return false
	}
	at := strings.LastIndex(target, "-")
	if at < 0 {
		return true
	}
	pid, err := strconv.ParseInt(target[at+1:], 10, 32)
	if err != nil {
		return true
	}
	// A lock a crash left names a process that is gone.
	return alive(int(pid))
}

func (chatbox) ID() string        { return "chatbox" }
func (chatbox) Name() string      { return "Chatbox" }
func (chatbox) ListsModels() bool { return true }
func (chatbox) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}

// Dir: `--user-data-dir` is out of reach.
func (chatbox) Dir(home agents.Home, _ string) string {
	switch {
	case runtime.GOOS == "darwin":
		return home.Join("Library", "Application Support", "xyz.chatboxapp.app")
	case home.Windows():
		return home.Join("AppData", "Roaming", "xyz.chatboxapp.app")
	}
	return home.Join(".config", "xyz.chatboxapp.app")
}

// Detected: a file Chatbox has not made yet would skip its first-start
// setup, so Chatbox counts once it has. Its settings live on the Windows
// side of a WSL home.
func (c chatbox) Detected(home agents.Home, target string) bool {
	return home.Distribution() == "" && isFile(filepath.Join(c.Dir(home, target), "config.json"))
}

func (chatbox) Files() []cc.File { return []cc.File{chatboxConfig{}} }

func (c chatbox) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	return []string{filepath.Join(c.Dir(home, target), "config.json")}
}

func (chatbox) ConnectionKeys() []string { return nil }

// HoldsConnection: each provider's settings hold an address and the token.
func (chatbox) HoldsConnection(key string) bool {
	id, ok := strings.CutPrefix(key, chatboxPrefix)
	return ok && chatboxOurs(id)
}

func (chatbox) CurrentModels(config *cc.Config) cc.CurrentModels {
	var ids []string
	for _, route := range chatboxRoutes {
		provider, ok := configObject(config, chatboxPrefix+route.id)
		if !ok {
			continue
		}
		listed, _ := provider["models"].([]any)
		for _, model := range listed {
			name, _ := field(model, "modelId")
			if id, ok := cc.CleanModel(name); ok && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	var models cc.CurrentModels
	if chosen, ok := configObject(config, chatboxDefaultKey); ok {
		if provider, ok := field(chosen, "provider"); ok && chatboxOurs(provider) {
			if model, ok := field(chosen, "model"); ok {
				if model, ok := cc.CleanModel(model); ok {
					models.Model = &model
				}
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

func (chatbox) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	list := connection.ModelList()
	var entries []any
	var keys []cc.Setting
	for _, route := range chatboxRoutes {
		var models []any
		for _, model := range list {
			if connection.Protocol(Chatbox, model) != route.protocol {
				continue
			}
			d, known := details[model]
			capabilities := []any{"tool_use"}
			if known && len(d.Reasoning) > 0 {
				capabilities = append(capabilities, "reasoning")
			}
			entry := map[string]any{"modelId": model, "type": "chat", "capabilities": capabilities}
			if known && d.ContextWindow != nil {
				entry["contextWindow"] = number(*d.ContextWindow)
			}
			if known && d.MaxTokens != nil {
				entry["maxOutput"] = number(*d.MaxTokens)
			}
			models = append(models, entry)
		}
		if len(models) == 0 {
			continue
		}
		entries = append(entries, map[string]any{"id": route.id, "name": route.name, "type": route.kind, "isCustom": true})
		// Chatbox adds `/v1` to Anthropic's own address only.
		settings := map[string]any{
			"apiHost": Chatbox.BaseURL(connection.Origin),
			"apiKey":  connection.Token,
			"models":  models,
		}
		keys = append(keys, cc.Setting{Key: chatboxPrefix + route.id, Value: format.Encode(settings)})
	}
	if len(entries) > 0 {
		keys = append([]cc.Setting{{Key: chatboxListKey, Value: format.Encode(entries)}}, keys...)
	}
	if model, ok := connection.Model("model"); ok {
		protocol := connection.Protocol(Chatbox, model)
		for _, route := range chatboxRoutes {
			if route.protocol == protocol {
				chosen := map[string]any{"provider": route.id, "model": model}
				keys = append(keys, cc.Setting{Key: chatboxDefaultKey, Value: format.Encode(chosen)})
				break
			}
		}
	}
	return keys
}

// BaseURLKey is unused: SyncedKeys points every provider elsewhere.
func (chatbox) BaseURLKey() string { return "" }

func (c chatbox) SyncedKeys(config *cc.Config, record *cc.Record, origin string) []cc.Setting {
	var keys []cc.Setting
	for _, key := range slices.Sorted(maps.Keys(record.Keys)) {
		if !c.HoldsConnection(key) {
			continue
		}
		settings, ok := configObject(config, key)
		if !ok {
			continue
		}
		settings["apiHost"] = Chatbox.BaseURL(origin)
		keys = append(keys, cc.Setting{Key: key, Value: format.Encode(settings)})
	}
	return keys
}

func (c chatbox) Running(home agents.Home, target string) bool {
	return chatboxLocked(c.Dir(home, target), home.Windows())
}

func (chatboxConfig) Format() format.Format {
	return format.JSONFormat{Objects: []string{"settings", "settings.providers"}}
}

func (chatboxConfig) Secret() bool { return true }

// Get reads the list as AstrLink's entries in it; a provider and the
// choice as JSON.
func (chatboxConfig) Get(document format.Document, key string) (string, bool, bool) {
	json, ok := document.(*format.JSONDocument)
	if !ok {
		return "", false, false
	}
	if key == chatboxListKey {
		return getOwned(json, key, chatboxOwned)
	}
	if strings.HasPrefix(key, chatboxPrefix) || key == chatboxDefaultKey {
		return getWhole(json, key)
	}
	return document.Get(key)
}

// Set puts AstrLink's entries after the user's.
func (chatboxConfig) Set(document format.Document, key, value string) error {
	json, ok := document.(*format.JSONDocument)
	if !ok || !strings.HasPrefix(key, chatboxPrefix) && key != chatboxListKey && key != chatboxDefaultKey {
		return document.Set(key, value)
	}
	if key != chatboxListKey {
		return setWhole(json, key, value, chatboxRank)
	}
	decoded, err := format.DecodeJSON(value)
	if err != nil {
		return err
	}
	entries, isList := decoded.([]any)
	if !isList {
		return fmt.Errorf("%s is not a list", key)
	}
	json.RemoveItems(key, func(entry any) bool {
		return chatboxOwned(entry) && !slices.ContainsFunc(entries, func(wanted any) bool { return sameID(wanted, entry) })
	})
	if err := json.PrependItems(key, nil, chatboxRank); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := json.PutItem(key, func(current any) bool { return sameID(current, entry) }, entry, chatboxRank); err != nil {
			return err
		}
	}
	return nil
}

// Remove drops an emptied list.
func (chatboxConfig) Remove(document format.Document, key string) {
	json, ok := document.(*format.JSONDocument)
	if !ok || key != chatboxListKey {
		document.Remove(key)
		return
	}
	json.RemoveItems(key, chatboxOwned)
	dropEmptyList(json, key)
}
