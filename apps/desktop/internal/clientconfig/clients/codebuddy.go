package clients

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// CodeBuddy, Tencent's coding agent. Its CLI, IDE, and VS Code and
// JetBrains plugins share `~/.codebuddy/models.json`; the CLI's default
// model is `model` in `~/.codebuddy/settings.json`. The IDE and plugins
// post every model to the Chat API, so every entry is a Chat one.

var CodeBuddy = cc.New(codeBuddy{})

const (
	codeBuddyModelKey = "model"
	// codeBuddyEntryPrefix is what the CLI's default model names a
	// `models.json` entry by, so an entry sharing a built-in model's id is
	// not taken for that model.
	codeBuddyEntryPrefix = "custom-local:"
)

type codeBuddy struct{}

// codeBuddySettings is `settings.json`, strict JSON, of which AstrLink
// writes `model` only.
type codeBuddySettings struct{}

func codeBuddyRoute(origin string) string { return origin + "/v1/chat/completions" }

func (codeBuddy) ID() string          { return "codebuddy" }
func (codeBuddy) Name() string        { return "CodeBuddy" }
func (codeBuddy) Protocols() []string { return []string{"openai.chat"} }
func (codeBuddy) ListsModels() bool   { return true }

// Dir is the default folder: only the CLI reads `CODEBUDDY_CONFIG_DIR`;
// the IDE and plugins keep to the default one.
func (codeBuddy) Dir(home agents.Home, _ string) string { return home.Join(".codebuddy") }

func (codeBuddy) Files() []cc.File { return []cc.File{buddyModelsFile{}, codeBuddySettings{}} }

func (c codeBuddy) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	dir := c.Dir(home, target)
	return []string{filepath.Join(dir, "models.json"), filepath.Join(dir, "settings.json")}
}

// ConnectionKeys is empty: each entry carries its own address and key.
func (codeBuddy) ConnectionKeys() []string { return []string{} }

func (codeBuddy) HoldsConnection(key string) bool { return buddyHoldsConnection(key) }

func (codeBuddy) CurrentModels(config *cc.Config) cc.CurrentModels {
	models := buddyCurrentModels(config, 0)
	if model, ok := config.Model(codeBuddyModelKey); ok {
		model = strings.TrimPrefix(model, codeBuddyEntryPrefix)
		if slices.Contains(models.Models, model) {
			models.Model = &model
		}
	}
	return models
}

func (codeBuddy) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	keys := buddyModelKeys(connection, details, func(string) (string, string) { return codeBuddyRoute(connection.Origin), "" })
	if model, ok := connection.Model("model"); ok {
		keys = append(keys, cc.Setting{Key: codeBuddyModelKey, Value: codeBuddyEntryPrefix + model})
	}
	return keys
}

// BaseURLKey is unused: SyncedKeys points every entry elsewhere.
func (codeBuddy) BaseURLKey() string { return "" }

func (codeBuddy) SyncedKeys(config *cc.Config, record *cc.Record, origin string) []cc.Setting {
	return buddySyncedKeys(config, record, func(string) string { return codeBuddyRoute(origin) })
}

func (codeBuddySettings) Format() format.Format { return format.JSONFormat{} }

func (codeBuddySettings) Holds(key string) bool { return key == codeBuddyModelKey }
