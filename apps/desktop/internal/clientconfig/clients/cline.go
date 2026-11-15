package clients

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Cline, CLI and editor extensions alike, reads providers from
// `~/.cline/data/settings/providers.json` and starts with
// `lastUsedProvider`. A built-in provider holds one model, so AstrLink
// writes the one its default model's protocol takes. Cline rewrites the
// file on every run, stamping `updatedAt`, so only the provider's
// `settings` and the choice are AstrLink's.

var Cline = cc.New(cline{})

type cline struct{}

// clineProviders is `providers.json`.
type clineProviders struct{}

// clineRoutes are each protocol's Cline provider id.
var clineRoutes = [][2]string{
	{"openai.chat", "openai-compatible"},
	{"openai.responses", "openai-native"},
	{"anthropic.messages", "anthropic"},
}

const (
	clinePrefix    = "providers."
	clineSuffix    = ".settings"
	clineChoiceKey = "lastUsedProvider"
	// clineEmpty is what Cline itself starts the file with.
	clineEmpty = "{\n  \"version\": 1,\n  \"modes\": {}\n}\n"
)

// clineEditors are the editors whose extension folders hold Cline.
var clineEditors = []string{".vscode", ".vscode-insiders", ".cursor", ".windsurf", ".vscode-oss"}

func clineSettingsKey(id string) string { return clinePrefix + id + clineSuffix }

func clineProviderOf(key string) (string, bool) {
	id, ok := strings.CutPrefix(key, clinePrefix)
	if !ok {
		return "", false
	}
	id, ok = strings.CutSuffix(id, clineSuffix)
	if !ok || !slices.ContainsFunc(clineRoutes, func(r [2]string) bool { return r[1] == id }) {
		return "", false
	}
	return id, true
}

func clineRouteOf(protocol string) string {
	if i := slices.IndexFunc(clineRoutes, func(r [2]string) bool { return r[0] == protocol }); i >= 0 {
		return clineRoutes[i][1]
	}
	return clineRoutes[0][1]
}

func clineRank(name string) int {
	order := []string{"provider", "apiKey", "baseUrl", "model", "contextWindow", "maxTokens"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

func (cline) ID() string   { return "cline" }
func (cline) Name() string { return "Cline" }
func (cline) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}

// Dir: `CLINE_DIR` and `CLINE_DATA_DIR` from a shell are out of reach.
func (cline) Dir(home agents.Home, _ string) string { return home.Join(".cline") }

// Detected: an extension makes `~/.cline` on its first start.
func (c cline) Detected(home agents.Home, target string) bool {
	if cc.IsDir(c.Dir(home, target)) {
		return true
	}
	for _, editor := range clineEditors {
		entries, _ := os.ReadDir(home.Join(editor, "extensions"))
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "saoudrizwan.claude-dev-") {
				return true
			}
		}
	}
	return false
}

func (cline) Files() []cc.File { return []cc.File{clineProviders{}} }

func (c cline) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	return []string{filepath.Join(c.Dir(home, target), "data", "settings", "providers.json")}
}

func (cline) ConnectionKeys() []string { return nil }

// HoldsConnection: the provider's settings hold the address and the token.
func (cline) HoldsConnection(key string) bool {
	_, ok := clineProviderOf(key)
	return ok
}

func (cline) CurrentModels(config *cc.Config) cc.CurrentModels {
	chosen, hasChoice := config.String(clineChoiceKey)
	var models cc.CurrentModels
	for _, route := range clineRoutes {
		if hasChoice && chosen != route[1] {
			continue
		}
		settings, ok := configObject(config, clineSettingsKey(route[1]))
		if !ok {
			continue
		}
		if model, ok := field(settings, "model"); ok {
			if model, ok := cc.CleanModel(model); ok {
				models.Model = &model
			}
		}
		break
	}
	return models
}

func (cline) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	model, ok := connection.Model("model")
	if !ok {
		return nil
	}
	id := clineRouteOf(connection.Protocol(Cline, model))
	settings := map[string]any{
		"provider": id,
		"apiKey":   connection.Token,
		"baseUrl":  Cline.BaseURL(connection.Origin),
		"model":    model,
	}
	if d, ok := details[model]; ok {
		if d.ContextWindow != nil {
			settings["contextWindow"] = number(*d.ContextWindow)
		}
		if d.MaxTokens != nil {
			settings["maxTokens"] = number(*d.MaxTokens)
		}
	}
	return []cc.Setting{
		{Key: clineSettingsKey(id), Value: format.Encode(settings)},
		{Key: clineChoiceKey, Value: id},
	}
}

// StartFile: without `version` Cline reads the file as empty.
func (cline) StartFile(config *cc.Config) *cc.FileError {
	jsonFormat, _ := clineProviders{}.Format().(format.JSONFormat)
	empty, err := format.ParseJSON(clineEmpty, jsonFormat)
	if err != nil {
		return &cc.FileError{Message: err.Error()}
	}
	config.Documents[0] = empty
	return nil
}

// BaseURLKey is unused: SyncedKeys moves the provider's settings.
func (cline) BaseURLKey() string { return "" }

func (c cline) SyncedKeys(config *cc.Config, record *cc.Record, origin string) []cc.Setting {
	var keys []cc.Setting
	for _, key := range sortedKeys(record.Keys) {
		if !c.HoldsConnection(key) {
			continue
		}
		settings, ok := configObject(config, key)
		if !ok {
			continue
		}
		settings["baseUrl"] = Cline.BaseURL(origin)
		keys = append(keys, cc.Setting{Key: key, Value: format.Encode(settings)})
	}
	return keys
}

// Notes: the extension's older settings choose its provider and win over
// this file.
func (c cline) Notes(home agents.Home, target string, _ *cc.Config) []string {
	text, err := os.ReadFile(filepath.Join(c.Dir(home, target), "data", "globalState.json"))
	if err != nil {
		return nil
	}
	state, err := format.DecodeJSON(string(text))
	object, _ := state.(map[string]any)
	if err != nil {
		return nil
	}
	for _, key := range []string{"actModeApiProvider", "planModeApiProvider"} {
		if _, ok := object[key]; ok {
			return []string{"legacy"}
		}
	}
	return nil
}

// Format is strict JSON: a file that fails Cline's schema reads as signed
// out.
func (clineProviders) Format() format.Format {
	return format.JSONFormat{Objects: []string{"providers", "modes"}}
}

func (clineProviders) Secret() bool { return true }

func (clineProviders) Get(document format.Document, key string) (string, bool, bool) {
	json, ok := document.(*format.JSONDocument)
	if _, provider := clineProviderOf(key); !ok || !provider {
		return document.Get(key)
	}
	return getWhole(json, key)
}

// Set gives a provider AstrLink adds the stamp Cline gives its own.
func (clineProviders) Set(document format.Document, key, value string) error {
	json, ok := document.(*format.JSONDocument)
	id, provider := clineProviderOf(key)
	if !ok || !provider {
		return document.Set(key, value)
	}
	settings, err := format.DecodeJSON(value)
	if err != nil {
		return err
	}
	if err := json.SetJSON(key, settings, clineRank); err != nil {
		return err
	}
	entry := clinePrefix + id
	if _, _, present := json.Get(entry + ".updatedAt"); !present {
		if err := json.Set(entry+".updatedAt", time.Now().UTC().Format("2006-01-02T15:04:05.000Z")); err != nil {
			return err
		}
	}
	if _, _, present := json.Get(entry + ".tokenSource"); !present {
		return json.Set(entry+".tokenSource", "manual")
	}
	return nil
}

// Remove lets a provider left with only Cline's stamps go.
func (clineProviders) Remove(document format.Document, key string) {
	json, ok := document.(*format.JSONDocument)
	if !ok {
		document.Remove(key)
		return
	}
	json.Remove(key)
	id, provider := clineProviderOf(key)
	if !provider {
		return
	}
	entry := clinePrefix + id
	value, _, _ := json.GetJSON(entry)
	object, isObject := value.(map[string]any)
	if !isObject {
		return
	}
	for name := range object {
		if name != "updatedAt" && name != "tokenSource" {
			return
		}
	}
	json.Remove(entry)
}

func (clineProviders) Emptied() []string { return []string{"providers"} }

// IsEmpty lets what Cline starts a file with go with the rest.
func (clineProviders) IsEmpty(document format.Document) bool {
	json, ok := document.(*format.JSONDocument)
	if !ok {
		return document.IsEmpty()
	}
	value, _ := json.Value()
	object, isObject := value.(map[string]any)
	if !isObject {
		return false
	}
	for name, value := range object {
		switch name {
		case "version":
		case "modes":
			if modes, ok := value.(map[string]any); !ok || len(modes) > 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
