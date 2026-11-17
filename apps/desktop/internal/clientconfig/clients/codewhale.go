package clients

import (
	"path/filepath"
	"slices"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Codewhale reads `~/.codewhale/config.toml` and starts with the provider
// table root `provider` names. AstrLink adds one table whose `wire` is the
// default model's protocol; Codewhale lists the other models from the
// gateway. A custom table reads its key only from the file.

var CodeWhale = cc.New(codeWhale{})

type codeWhale struct{}

// codeWhaleConfig is `config.toml`.
type codeWhaleConfig struct{}

const (
	codeWhaleName        = "local-gateway"
	codeWhaleProviderKey = "providers.local-gateway"
	codeWhaleChoiceKey   = "provider"
)

// codeWhaleWire is a protocol's `wire`; Chat Completions is what a table
// without one speaks.
func codeWhaleWire(protocol string) string {
	switch protocol {
	case "openai.responses":
		return "responses"
	case "anthropic.messages":
		return "anthropic"
	}
	return ""
}

func codeWhaleRank(name string) int {
	order := []string{"kind", "base_url", "api_key", "model", "wire", "model_context_windows"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

func (codeWhale) ID() string   { return "codewhale" }
func (codeWhale) Name() string { return "Codewhale" }
func (codeWhale) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}

// Dir: `CODEWHALE_HOME` from a shell is out of reach.
func (codeWhale) Dir(home agents.Home, _ string) string { return home.Join(".codewhale") }

func (codeWhale) Files() []cc.File { return []cc.File{codeWhaleConfig{}} }

func (c codeWhale) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	return []string{filepath.Join(c.Dir(home, target), "config.toml")}
}

func (codeWhale) ConnectionKeys() []string { return nil }

// HoldsConnection: the table holds the address and the token.
func (codeWhale) HoldsConnection(key string) bool { return key == codeWhaleProviderKey }

func (codeWhale) CurrentModels(config *cc.Config) cc.CurrentModels {
	var models cc.CurrentModels
	table, ok := configObject(config, codeWhaleProviderKey)
	if choice, chosen := config.String(codeWhaleChoiceKey); !ok || !chosen || choice != codeWhaleName {
		return models
	}
	if model, ok := field(table, "model"); ok {
		if model, ok := cc.CleanModel(model); ok {
			models.Model = &model
		}
	}
	return models
}

func (codeWhale) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	model, ok := connection.Model("model")
	if !ok {
		return nil
	}
	table := map[string]any{
		"kind":     "openai-compatible",
		"base_url": CodeWhale.BaseURL(connection.Origin),
		"api_key":  connection.Token,
		"model":    model,
	}
	if wire := codeWhaleWire(connection.Protocol(CodeWhale, model)); wire != "" {
		table["wire"] = wire
	}
	if d, ok := details[model]; ok && d.ContextWindow != nil {
		table["model_context_windows"] = map[string]any{model: number(*d.ContextWindow)}
	}
	return []cc.Setting{
		{Key: codeWhaleChoiceKey, Value: codeWhaleName},
		{Key: codeWhaleProviderKey, Value: format.Encode(table)},
	}
}

// BaseURLKey is unused: SyncedKeys moves the table.
func (codeWhale) BaseURLKey() string { return "" }

func (codeWhale) SyncedKeys(config *cc.Config, _ *cc.Record, origin string) []cc.Setting {
	table, ok := configObject(config, codeWhaleProviderKey)
	if !ok {
		return nil
	}
	table["base_url"] = CodeWhale.BaseURL(origin)
	return []cc.Setting{{Key: codeWhaleProviderKey, Value: format.Encode(table)}}
}

func (codeWhaleConfig) Format() format.Format { return format.TOMLFormat{} }

func (codeWhaleConfig) Secret() bool { return true }

// Get reads the table as one JSON object.
func (codeWhaleConfig) Get(document format.Document, key string) (string, bool, bool) {
	toml, ok := document.(*format.TOMLDocument)
	if !ok || key != codeWhaleProviderKey {
		return document.Get(key)
	}
	return getTable(toml, "providers", codeWhaleName)
}

func (codeWhaleConfig) Set(document format.Document, key, value string) error {
	toml, ok := document.(*format.TOMLDocument)
	if !ok || key != codeWhaleProviderKey {
		return document.Set(key, value)
	}
	return setTable(toml, "providers", codeWhaleName, key, value, codeWhaleRank)
}

func (codeWhaleConfig) Remove(document format.Document, key string) {
	toml, ok := document.(*format.TOMLDocument)
	if !ok || key != codeWhaleProviderKey {
		document.Remove(key)
		return
	}
	removeTable(toml, "providers", codeWhaleName)
}
