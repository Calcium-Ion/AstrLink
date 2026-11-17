package clients

import (
	"path/filepath"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Claude Code reads its gateway, token, and models from `env` in
// `settings.json`, strict JSON.

var Claude = cc.New(claude{})

// ClaudeModelKeys are Claude Code's model settings, by the Models field
// each comes from.
var ClaudeModelKeys = [][2]string{
	{"model", "env.ANTHROPIC_MODEL"},
	{"haikuModel", "env.ANTHROPIC_DEFAULT_HAIKU_MODEL"},
	{"sonnetModel", "env.ANTHROPIC_DEFAULT_SONNET_MODEL"},
	{"opusModel", "env.ANTHROPIC_DEFAULT_OPUS_MODEL"},
	{"fableModel", "env.ANTHROPIC_DEFAULT_FABLE_MODEL"},
	{"subagentModel", "env.CLAUDE_CODE_SUBAGENT_MODEL"},
}

type claude struct{}

// claudeSettings is `settings.json`.
type claudeSettings struct{}

// claudeGroupEnv reports whether a Claude Code settings key chooses the
// endpoint, credential, or model.
func claudeGroupEnv(name string) bool {
	switch name {
	case "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_SUBAGENT_MODEL", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY":
		return true
	}
	return strings.HasPrefix(name, "ANTHROPIC_")
}

func (claude) ID() string          { return "claude" }
func (claude) Name() string        { return "Claude Code" }
func (claude) Protocols() []string { return []string{"anthropic.messages"} }

func (claude) Dir(home agents.Home, _ string) string { return home.ClaudeDir() }

func (claude) Detected(home agents.Home, _ string) bool { return home.ClaudeDetected() }

func (claude) Files() []cc.File { return []cc.File{claudeSettings{}} }

func (claude) ConfigPaths(home agents.Home, _ string, _ *cc.Record) []string {
	return []string{filepath.Join(home.ClaudeDir(), "settings.json")}
}

func (claude) ModelFields() []string {
	fields := make([]string, len(ClaudeModelKeys))
	for i, key := range ClaudeModelKeys {
		fields[i] = key[0]
	}
	return fields
}

// RequiresModel is false: each tier may be left to Claude Code.
func (claude) RequiresModel() bool { return false }

// BaseURL is the bare origin: Claude Code appends its own API paths.
func (claude) BaseURL(origin string) string { return origin }

func (claude) ConnectionKeys() []string {
	return []string{"env.ANTHROPIC_BASE_URL", "env.ANTHROPIC_AUTH_TOKEN"}
}

func (claude) CurrentModels(config *cc.Config) cc.CurrentModels {
	var models cc.CurrentModels
	for _, key := range ClaudeModelKeys {
		if model, ok := config.Model(key[1]); ok {
			models.Set(key[0], model)
		}
	}
	return models
}

func (c claude) DesiredKeys(connection *cc.Connection, _ cc.Catalog) []cc.Setting {
	keys := []cc.Setting{
		{Key: "env.ANTHROPIC_BASE_URL", Value: c.BaseURL(connection.Origin)},
		// AUTH_TOKEN, not API_KEY: an API key asks for approval in the
		// interactive client and conflicts with a login.
		{Key: "env.ANTHROPIC_AUTH_TOKEN", Value: connection.Token},
	}
	for _, key := range ClaudeModelKeys {
		if model, ok := connection.Model(key[0]); ok {
			keys = append(keys, cc.Setting{Key: key[1], Value: model})
		}
	}
	return keys
}

func (claude) BaseURLKey() string { return "env.ANTHROPIC_BASE_URL" }

func (claudeSettings) Format() format.Format {
	return format.JSONFormat{Objects: []string{"env"}}
}

func (s claudeSettings) Group(document format.Document) []format.KeyValue {
	var keys []format.KeyValue
	if json, ok := document.(*format.JSONDocument); ok {
		for _, name := range json.ObjectNames("env") {
			if claudeGroupEnv(name) {
				path := "env." + name
				value, ok, _ := cc.FileGet(s, document, path)
				keys = append(keys, format.KeyValue{Key: path, Value: value, Present: ok})
			}
		}
	}
	cc.PushPresent(&keys, s, document, "apiKeyHelper")
	return keys
}

func (claudeSettings) Emptied() []string { return []string{"env"} }

// ReadClaudeSettings reads Claude Code's settings as it does, for
// importing the providers they configure.
func ReadClaudeSettings(text string) (any, error) {
	return cc.ReadJSON(claudeSettings{}, text)
}
