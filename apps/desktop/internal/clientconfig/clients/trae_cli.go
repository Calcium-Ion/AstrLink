package clients

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// TRAE CLI reads `~/.trae/traecli.toml`. Each `[[models]]` entry is one
// model in its `/model` list, with the address and key in a subtable named
// for its protocol; AstrLink's are the ones that send an AstrLink token to
// a gateway on this computer, after the user's. `model_provider = "custom"`
// with `model` picks one.

var TraeCLI = cc.New(traeCLI{})

type traeCLI struct{}

// traeConfig is `traecli.toml`.
type traeConfig struct{}

// traeBackends are each protocol and the subtable that takes it.
var traeBackends = []struct{ protocol, backend string }{
	{"openai.chat", "open_ai"},
	{"openai.responses", "byted_gpt"},
	{"anthropic.messages", "claude"},
}

const (
	traeModelsKey   = "models"
	traeProviderKey = "model_provider"
	traeModelKey    = "model"
	// traeCustom is what `model_provider` picks `[[models]]` by `model`
	// with.
	traeCustom = "custom"
)

func traeRank(name string) int {
	order := []string{"name", "context_window", "output_tokens_hard_limit", "base_url", "api_key", "max_tokens"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

// traeOwned reports an entry that sends an AstrLink token to a gateway on
// this computer.
func traeOwned(entry any) bool {
	object, _ := entry.(map[string]any)
	for _, route := range traeBackends {
		address := object[route.backend]
		key, _ := field(address, "api_key")
		url, _ := field(address, "base_url")
		if strings.HasPrefix(key, "astr_") && (strings.HasPrefix(url, "http://127.0.0.1:") || strings.HasPrefix(url, "http://localhost:")) {
			return true
		}
	}
	return false
}

func traeEntry(id, protocol string, connection *cc.Connection, details cc.Catalog) map[string]any {
	backend := traeBackends[0].backend
	for _, route := range traeBackends {
		if route.protocol == protocol {
			backend = route.backend
		}
	}
	entry := map[string]any{"name": id}
	// TRAE adds `/chat/completions`, `/responses`, or `/messages`.
	address := map[string]any{"base_url": connection.Origin + "/v1", "api_key": connection.Token}
	if d, ok := details[id]; ok {
		if d.ContextWindow != nil {
			entry["context_window"] = number(*d.ContextWindow)
		}
		if d.MaxTokens != nil {
			entry["output_tokens_hard_limit"] = number(*d.MaxTokens)
			address["max_tokens"] = number(*d.MaxTokens)
		}
	}
	entry[backend] = address
	return entry
}

func (traeCLI) ID() string        { return "trae_cli" }
func (traeCLI) Name() string      { return "TRAE CLI" }
func (traeCLI) ListsModels() bool { return true }
func (traeCLI) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}

// Dir: `TRAE_HOME` from a shell is out of reach.
func (traeCLI) Dir(home agents.Home, _ string) string { return home.Join(".trae") }

// Detected: the TRAE editor keeps `~/.trae` too; the CLI keeps its state
// in `~/.trae/cli` after a first run.
func (t traeCLI) Detected(home agents.Home, target string) bool {
	dir := t.Dir(home, target)
	installs := []string{home.Join(".local", "bin", "traecli"), home.Join(".local", "bin", "traex")}
	if home.Windows() {
		installs = []string{home.Join("AppData", "Local", "Programs", "TraeCLI", "bin")}
	}
	if cc.IsDir(filepath.Join(dir, "cli")) || isFile(filepath.Join(dir, "traecli.toml")) {
		return true
	}
	for _, path := range installs {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

func (traeCLI) Files() []cc.File { return []cc.File{traeConfig{}} }

func (t traeCLI) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	return []string{filepath.Join(t.Dir(home, target), "traecli.toml")}
}

func (traeCLI) ConnectionKeys() []string { return nil }

// HoldsConnection: each entry carries its own address and key.
func (traeCLI) HoldsConnection(key string) bool { return key == traeModelsKey }

func (traeCLI) CurrentModels(config *cc.Config) cc.CurrentModels {
	var ids []string
	for _, entry := range configList(config, traeModelsKey) {
		name, _ := field(entry, "name")
		if id, ok := cc.CleanModel(name); ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	var models cc.CurrentModels
	if provider, ok := config.String(traeProviderKey); ok && provider == traeCustom {
		if model, ok := config.Model(traeModelKey); ok && slices.Contains(ids, model) {
			models.Model = &model
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

func (traeCLI) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	var entries []any
	for _, id := range connection.ModelList() {
		entries = append(entries, traeEntry(id, connection.Protocol(TraeCLI, id), connection, details))
	}
	var keys []cc.Setting
	if model, ok := connection.Model("model"); ok {
		keys = append(keys, cc.Setting{Key: traeModelKey, Value: model}, cc.Setting{Key: traeProviderKey, Value: traeCustom})
	}
	if len(entries) > 0 {
		keys = append(keys, cc.Setting{Key: traeModelsKey, Value: format.Encode(entries)})
	}
	return keys
}

// BaseURLKey is unused: SyncedKeys points every entry elsewhere.
func (traeCLI) BaseURLKey() string { return "" }

func (traeCLI) SyncedKeys(config *cc.Config, _ *cc.Record, origin string) []cc.Setting {
	entries := configList(config, traeModelsKey)
	if len(entries) == 0 {
		return nil
	}
	for _, entry := range entries {
		object, _ := entry.(map[string]any)
		for _, route := range traeBackends {
			if address, ok := object[route.backend].(map[string]any); ok {
				address["base_url"] = origin + "/v1"
			}
		}
	}
	return []cc.Setting{{Key: traeModelsKey, Value: format.Encode(entries)}}
}

func (traeConfig) Format() format.Format { return format.TOMLFormat{} }

func (traeConfig) Secret() bool { return true }

// Get reads the list as AstrLink's entries in it.
func (traeConfig) Get(document format.Document, key string) (string, bool, bool) {
	toml, ok := document.(*format.TOMLDocument)
	if !ok {
		return "", false, false
	}
	if key != traeModelsKey {
		return document.Get(key)
	}
	return getOwnedTables(toml, key, traeOwned)
}

// Set gives each entry's address a `[models.<backend>]` table of its own.
func (traeConfig) Set(document format.Document, key, value string) error {
	if key != traeModelsKey {
		return document.Set(key, value)
	}
	toml, ok := document.(*format.TOMLDocument)
	if !ok {
		return fmt.Errorf("%s is not TOML", key)
	}
	decoded, err := format.DecodeJSON(value)
	entries, isList := decoded.([]any)
	if err != nil || !isList {
		return fmt.Errorf("%s is not a list", key)
	}
	dropOwnedTables(toml, key, traeOwned)
	root := toml.Root()
	if !root.ContainsKey(key) {
		root.Insert(key, &format.TOMLArrayOfTables{})
	}
	tables := arrayOfTables(toml, key)
	if tables == nil {
		return fmt.Errorf("has a %s value that is not a list of tables", key)
	}
	for _, entry := range entries {
		fields := map[string]any{}
		if object, ok := entry.(map[string]any); ok {
			for name, value := range object {
				fields[name] = value
			}
		}
		type address struct {
			backend string
			value   any
		}
		var addresses []address
		for _, route := range traeBackends {
			if value, ok := fields[route.backend]; ok {
				addresses = append(addresses, address{route.backend, value})
				delete(fields, route.backend)
			}
		}
		table, ok := format.TOMLTableOf(fields, traeRank)
		if !ok {
			return fmt.Errorf("%s holds a value TOML cannot", key)
		}
		for _, a := range addresses {
			sub, ok := format.TOMLTableOf(a.value, traeRank)
			if !ok {
				return fmt.Errorf("%s holds a value TOML cannot", key)
			}
			table.Insert(a.backend, sub)
		}
		tables.Push(table)
	}
	return nil
}

func (traeConfig) Remove(document format.Document, key string) {
	if toml, ok := document.(*format.TOMLDocument); ok && key == traeModelsKey {
		dropOwnedTables(toml, key, traeOwned)
		return
	}
	document.Remove(key)
}
