package clients

import (
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// VS Code's Copilot Chat reads custom models from
// `<user data>/User/chatLanguageModels.json`, a list of provider groups.
// AstrLink's is the `customendpoint` group named `AstrLink`, which the
// model picker shows beside each model; each model carries its own URL and
// token header. VS Code has no default model to write, so the default
// leads the group. The `settings` VS Code keeps in the group are the
// user's.

var VSCodeCopilot = cc.New(vscodeCopilot{})

type vscodeCopilot struct{}

// vscodeLanguageModels is `chatLanguageModels.json`.
type vscodeLanguageModels struct{}

const (
	// vscodeGroupKey is AstrLink's group, without the `settings` VS Code
	// keeps in it.
	vscodeGroupKey = "AstrLink"
	vscodeVendor   = "customendpoint"
	// VS Code's own defaults for a model of unknown size.
	vscodeDefaultInput  = 128_000
	vscodeDefaultOutput = 16_000
)

func vscodeOurs(group any) bool {
	vendor, _ := field(group, "vendor")
	name, _ := field(group, "name")
	return vendor == vscodeVendor && name == vscodeGroupKey
}

func vscodeRank(name string) int {
	order := []string{"name", "vendor", "models", "id", "url", "apiType", "toolCalling", "vision", "maxInputTokens", "maxOutputTokens", "thinking", "supportsReasoningEffort", "statefulResponses", "requestHeaders", "settings"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	return len(order)
}

// vscodeRoute is the path, `apiType`, and token header for a gateway
// protocol.
func vscodeRoute(protocol string) (path, api, header string) {
	switch protocol {
	case "anthropic.messages":
		return "/v1/messages", "messages", "x-api-key"
	case "openai.responses":
		return "/v1/responses", "responses", "Authorization"
	}
	return "/v1/chat/completions", "chat-completions", "Authorization"
}

func vscodeModelEntry(id, protocol string, connection *cc.Connection, details cc.Catalog) map[string]any {
	path, api, header := vscodeRoute(protocol)
	d, known := details[id]
	input, output := uint64(vscodeDefaultInput), uint64(vscodeDefaultOutput)
	if known && d.ContextWindow != nil && d.MaxTokens != nil && *d.ContextWindow > *d.MaxTokens {
		input, output = *d.ContextWindow-*d.MaxTokens, *d.MaxTokens
	}
	token := connection.Token
	if header == "Authorization" {
		token = "Bearer " + token
	}
	entry := map[string]any{
		"id":              id,
		"name":            id,
		"url":             connection.Origin + path,
		"apiType":         api,
		"toolCalling":     true,
		"vision":          false,
		"maxInputTokens":  number(input),
		"maxOutputTokens": number(output),
		"requestHeaders":  map[string]any{header: token},
	}
	var levels []any
	for _, level := range d.Reasoning {
		if level != "none" {
			levels = append(levels, level)
		}
	}
	if api != "messages" && len(levels) > 0 {
		entry["thinking"] = true
		entry["supportsReasoningEffort"] = levels
	}
	// Otherwise VS Code sends only the new turns after a response id.
	if api == "responses" {
		entry["statefulResponses"] = false
	}
	return entry
}

// vscodeMoved points a model entry at origin, keeping its path.
func vscodeMoved(entry any, origin string) {
	object, _ := entry.(map[string]any)
	url, ok := object["url"].(string)
	if !ok {
		return
	}
	for _, path := range []string{"/v1/messages", "/v1/responses", "/v1/chat/completions"} {
		if strings.HasSuffix(url, path) {
			object["url"] = origin + path
			return
		}
	}
}

func vscodeGroupOf(config *cc.Config) (map[string]any, bool) {
	value, ok := config.String(vscodeGroupKey)
	if !ok {
		return nil, false
	}
	decoded, err := format.DecodeJSON(value)
	group, isObject := decoded.(map[string]any)
	return group, err == nil && isObject
}

func (vscodeCopilot) ID() string   { return "vscode_copilot" }
func (vscodeCopilot) Name() string { return "VS Code Copilot" }
func (vscodeCopilot) Protocols() []string {
	return []string{"openai.chat", "openai.responses", "anthropic.messages"}
}
func (vscodeCopilot) Targets() []string { return []string{"stable", "insiders"} }
func (vscodeCopilot) ListsModels() bool { return true }

// Dir is the edition's user directory; `--user-data-dir` and portable mode
// are out of reach.
func (vscodeCopilot) Dir(home agents.Home, target string) string {
	product := "Code"
	if target == "insiders" {
		product = "Code - Insiders"
	}
	switch runtime.GOOS {
	case "darwin":
		return home.Join("Library", "Application Support", product, "User")
	case "windows":
		return home.Join("AppData", "Roaming", product, "User")
	}
	return home.Join(".config", product, "User")
}

// Detected: VS Code's settings live on the Windows side of a WSL home.
func (v vscodeCopilot) Detected(home agents.Home, target string) bool {
	return home.Distribution() == "" && cc.IsDir(v.Dir(home, target))
}

func (vscodeCopilot) Files() []cc.File { return []cc.File{vscodeLanguageModels{}} }

func (v vscodeCopilot) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	return []string{filepath.Join(v.Dir(home, target), "chatLanguageModels.json")}
}

func (vscodeCopilot) ConnectionKeys() []string { return nil }

// HoldsConnection: each model holds its URL and the token.
func (vscodeCopilot) HoldsConnection(key string) bool { return key == vscodeGroupKey }

func (vscodeCopilot) CurrentModels(config *cc.Config) cc.CurrentModels {
	var ids []string
	if group, ok := vscodeGroupOf(config); ok {
		entries, _ := group["models"].([]any)
		for _, entry := range entries {
			id, _ := field(entry, "id")
			if id, ok := cc.CleanModel(id); ok && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	var models cc.CurrentModels
	if len(ids) > 0 {
		models.Model, models.Models = cc.Some(ids[0]), ids
	}
	return models
}

func (vscodeCopilot) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	ordered := connection.ModelList()
	if model, ok := connection.Model("model"); ok {
		ordered = slices.DeleteFunc(ordered, func(id string) bool { return id == model })
		ordered = slices.Insert(ordered, 0, model)
	}
	if len(ordered) == 0 {
		return nil
	}
	var models []any
	for _, id := range ordered {
		models = append(models, vscodeModelEntry(id, connection.Protocol(VSCodeCopilot, id), connection, details))
	}
	group := map[string]any{"name": vscodeGroupKey, "vendor": vscodeVendor, "models": models}
	return []cc.Setting{{Key: vscodeGroupKey, Value: format.Encode(group)}}
}

// StartFile: the file is a list; a new one starts as VS Code's own, empty.
func (vscodeCopilot) StartFile(config *cc.Config) *cc.FileError {
	jsonFormat, _ := vscodeLanguageModels{}.Format().(format.JSONFormat)
	empty, err := format.ParseJSON("[]\n", jsonFormat)
	if err != nil {
		out := &cc.FileError{Message: err.Error()}
		if invalid, ok := err.(*format.Invalid); ok {
			out.Message, out.Line = invalid.Message, invalid.Line
		}
		return out
	}
	config.Documents[0] = empty
	return nil
}

// BaseURLKey is unused: SyncedKeys moves every model's URL.
func (vscodeCopilot) BaseURLKey() string { return "" }

func (vscodeCopilot) SyncedKeys(config *cc.Config, _ *cc.Record, origin string) []cc.Setting {
	group, ok := vscodeGroupOf(config)
	if !ok {
		return nil
	}
	if models, ok := group["models"].([]any); ok {
		for _, entry := range models {
			vscodeMoved(entry, origin)
		}
	}
	return []cc.Setting{{Key: vscodeGroupKey, Value: format.Encode(group)}}
}

// Format: VS Code reads it with a tolerant parser.
func (vscodeLanguageModels) Format() format.Format {
	return format.JSONFormat{Syntax: &format.JSONCTrailing, RootList: true}
}

func (vscodeLanguageModels) Secret() bool { return true }

func (vscodeLanguageModels) Get(document format.Document, key string) (string, bool, bool) {
	json, ok := document.(*format.JSONDocument)
	if !ok || key != vscodeGroupKey || !json.IsList() {
		return "", false, false
	}
	found, ok := json.FindItem("", vscodeOurs)
	if !ok {
		return "", false, false
	}
	if group, isObject := found.(map[string]any); isObject {
		delete(group, "settings")
	}
	return format.Encode(found), true, true
}

func (vscodeLanguageModels) Set(document format.Document, key, value string) error {
	json, ok := document.(*format.JSONDocument)
	if !ok {
		return document.Set(key, value)
	}
	if !json.IsList() {
		return errors.New("is an object where a list is expected")
	}
	decoded, err := format.DecodeJSON(value)
	if err != nil {
		return err
	}
	if current, ok := json.FindItem("", vscodeOurs); ok {
		if settings, ok := current.(map[string]any)["settings"]; ok { // vscodeOurs found an object
			if group, isObject := decoded.(map[string]any); isObject {
				group["settings"] = settings
			}
		}
	}
	return json.PutItem("", vscodeOurs, decoded, vscodeRank)
}

func (vscodeLanguageModels) Remove(document format.Document, key string) {
	if json, ok := document.(*format.JSONDocument); ok && key == vscodeGroupKey && json.IsList() {
		json.RemoveItems("", vscodeOurs)
	}
}
