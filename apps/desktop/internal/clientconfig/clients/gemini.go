package clients

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Gemini CLI reads its endpoint, key, and model from `~/.gemini/.env`, and
// its login from `settings.json`.

var Gemini = cc.New(gemini{})

const (
	geminiBaseURLKey = "GOOGLE_GEMINI_BASE_URL"
	geminiAPIKeyKey  = "GEMINI_API_KEY"
	geminiModelKey   = "GEMINI_MODEL"
	// geminiAuthKey: without an explicit API-key login, Gemini CLI infers
	// an auth type it then refuses from the base URL alone.
	geminiAuthKey  = "security.auth.selectedType"
	geminiAuthType = "gemini-api-key"
	// autoModel is the gateway's automatic route, the one model name with
	// a slash its Gemini API takes.
	autoModel = "astrlink/auto"
)

// geminiEnvGroup are the `.env` names that choose Gemini CLI's endpoint,
// credential, or model.
var geminiEnvGroup = []string{geminiBaseURLKey, geminiAPIKeyKey, geminiModelKey}

type gemini struct{}

// geminiEnv is `.env`, as `NAME=value` lines.
type geminiEnv struct{}

// geminiSettings is `settings.json`, which picks the API-key login.
type geminiSettings struct{}

func (gemini) ID() string          { return "gemini" }
func (gemini) Name() string        { return "Gemini CLI" }
func (gemini) Protocols() []string { return []string{"google.generate_content"} }

func (gemini) Dir(home agents.Home, _ string) string { return home.GeminiDir() }

func (gemini) Files() []cc.File { return []cc.File{geminiEnv{}, geminiSettings{}} }

func (gemini) ConfigPaths(home agents.Home, _ string, _ *cc.Record) []string {
	dir := home.GeminiDir()
	return []string{filepath.Join(dir, ".env"), filepath.Join(dir, "settings.json")}
}

// CheckModel rejects a slash: Gemini CLI puts the model in the URL path,
// where the gateway takes no slash but its own automatic route's.
func (gemini) CheckModel(model string) error {
	if strings.Contains(model, "/") && model != autoModel {
		return errors.New("invalid model")
	}
	return nil
}

// BaseURL is the bare origin: Gemini CLI appends its own API paths.
func (gemini) BaseURL(origin string) string { return origin }

func (gemini) ConnectionKeys() []string {
	return []string{geminiBaseURLKey, geminiAPIKeyKey, geminiAuthKey}
}

func (gemini) CurrentModels(config *cc.Config) cc.CurrentModels {
	var models cc.CurrentModels
	if model, ok := config.Model(geminiModelKey); ok {
		models.Model = &model
	}
	return models
}

func (gemini) DesiredKeys(connection *cc.Connection, _ cc.Catalog) []cc.Setting {
	keys := []cc.Setting{
		{Key: geminiBaseURLKey, Value: connection.Origin},
		{Key: geminiAPIKeyKey, Value: connection.Token},
	}
	if model, ok := connection.Model("model"); ok {
		keys = append(keys, cc.Setting{Key: geminiModelKey, Value: model})
	}
	return append(keys, cc.Setting{Key: geminiAuthKey, Value: geminiAuthType})
}

func (gemini) BaseURLKey() string { return geminiBaseURLKey }

func (geminiEnv) Format() format.Format { return format.EnvFormat{SingleLine: geminiEnvGroup} }

// Holds is true for `.env` names, which have no dots; settings keys are
// dotted paths.
func (geminiEnv) Holds(key string) bool { return !strings.Contains(key, ".") }

func (geminiEnv) Group(document format.Document) []format.KeyValue {
	var keys []format.KeyValue
	if env := format.AsEnv(document); env != nil {
		for _, name := range env.Names() {
			if slices.Contains(geminiEnvGroup, name) {
				value, ok := env.Get(name)
				keys = append(keys, format.KeyValue{Key: name, Value: value, Present: ok})
			}
		}
	}
	return keys
}

// Format is JSON with comments: Gemini CLI strips comments, then parses
// strict JSON.
func (geminiSettings) Format() format.Format {
	return format.JSONFormat{Syntax: &format.JSONC, Objects: []string{"security", "security.auth"}}
}

func (geminiSettings) Holds(key string) bool { return strings.Contains(key, ".") }

func (geminiSettings) Emptied() []string { return []string{"security.auth", "security"} }
