package clients

import (
	"path/filepath"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

// WorkBuddy, Tencent's desktop agent, in its China edition (`~/.workbuddy`)
// and international one (`~/.workbuddy-ai`). Each reads custom models from
// `models.json` in its data folder and picks the default model in the app.

var WorkBuddy = cc.New(workBuddy{})

const (
	responses = "openai.responses"
	// workBuddyResponsesAPI is the `api` of an entry WorkBuddy sends to the
	// Responses API.
	workBuddyResponsesAPI = "openai-responses"
)

type workBuddy struct{}

// workBuddyRoute is the full Chat address, or the base a Responses entry
// appends to.
func workBuddyRoute(origin, api string) string {
	if api != "" {
		return origin + "/v1"
	}
	return origin + "/v1/chat/completions"
}

func (workBuddy) ID() string          { return "workbuddy" }
func (workBuddy) Name() string        { return "WorkBuddy" }
func (workBuddy) Protocols() []string { return []string{"openai.chat", responses} }
func (workBuddy) Targets() []string   { return []string{"cn", "intl"} }
func (workBuddy) ListsModels() bool   { return true }

func (workBuddy) Dir(home agents.Home, target string) string {
	if target == "intl" {
		return home.Join(".workbuddy-ai")
	}
	return home.Join(".workbuddy")
}

func (workBuddy) Files() []cc.File { return []cc.File{buddyModelsFile{}} }

func (w workBuddy) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	return []string{filepath.Join(w.Dir(home, target), "models.json")}
}

// ConnectionKeys is empty: each entry carries its own address and key.
func (workBuddy) ConnectionKeys() []string { return []string{} }

func (workBuddy) HoldsConnection(key string) bool { return buddyHoldsConnection(key) }

// CurrentModels takes the first of AstrLink's entries for the default
// model, which WorkBuddy keeps in its own preferences.
func (workBuddy) CurrentModels(config *cc.Config) cc.CurrentModels {
	return buddyCurrentModels(config, 0)
}

func (workBuddy) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	return buddyModelKeys(connection, details, func(id string) (string, string) {
		api := ""
		if connection.Protocol(WorkBuddy, id) == responses {
			api = workBuddyResponsesAPI
		}
		return workBuddyRoute(connection.Origin, api), api
	})
}

// BaseURLKey is unused: SyncedKeys points every entry elsewhere.
func (workBuddy) BaseURLKey() string { return "" }

func (workBuddy) SyncedKeys(config *cc.Config, record *cc.Record, origin string) []cc.Setting {
	return buddySyncedKeys(config, record, func(api string) string { return workBuddyRoute(origin, api) })
}
