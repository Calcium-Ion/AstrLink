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

// DeepSeek Harness (`dsh`), in its web profile (`dsh web`) and desktop
// app. Each profile reads `~/.dsh/profiles/<profile>/cordis.patch.yml`, a
// YAML list of patch rows: AstrLink adds its routes, one per protocol, to
// the `llm-pi-ai` row and picks the default model in the
// `agent-default-model` row. The token goes to the credentials file every
// profile shares, under a name of the profile's own.
//
// AstrLink owns the dshRouteFields of its routes. Whatever else the user
// adds to one, like `compat`, stays through every write and sync.
//
// The `deepseek-official` route stays as it is: it uploads session logs.

var DeepSeekHarness = cc.New(deepSeekHarness{})

type deepSeekHarness struct{}

// dshRoute is one route per protocol, since a route has one API: its
// protocol, route name, `api`, and the name the model picker groups it
// under.
type dshRoute struct{ protocol, name, api, display string }

var dshRoutes = []dshRoute{
	{"openai.chat", "astrlink-chat", "openai-completions", "AstrLink (OpenAI Chat)"},
	{"anthropic.messages", "astrlink-anthropic", "anthropic-messages", "AstrLink (Anthropic)"},
	{"openai.responses", "astrlink-responses", "openai-responses", "AstrLink (Responses)"},
}

// dshRouteFields are the properties of a route AstrLink writes, in the
// order it writes them.
var dshRouteFields = []string{"displayName", "apiKeyEnv", "api", "baseURL", "models"}

const (
	dshModelsRow      = "llm-pi-ai"
	dshModelsPackage  = "@deepseek-ai/dsh-llm-pi-ai"
	dshDefaultRow     = "agent-default-model"
	dshDefaultPackage = "@deepseek-ai/dsh-agent-default-model"
	dshRoutePrefix    = "llm-pi-ai.providers."
	dshDefaultKey     = "agent-default-model.config"
	dshRefPrefix      = "refs."
)

// dshEfforts are dsh's reasoning levels, in its order; the catalog's
// `none` is `off`.
var dshEfforts = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

func dshRouteKey(route string) string { return dshRoutePrefix + route }

// dshReference is the credential name the place's routes read the token
// from.
func dshReference(target string) string {
	if target == "" {
		target = "web"
	}
	return "ASTRLINK_" + strings.ToUpper(target) + "_API_KEY"
}

// dshRouteURL is the bare origin for the Anthropic route, `/v1` for the
// others.
func dshRouteURL(route, origin string) string {
	if route == "astrlink-anthropic" {
		return origin
	}
	return origin + "/v1"
}

// dshRank places AstrLink's properties among their siblings.
func dshRank(name string) int {
	order := []string{"id", "config", "providers", "displayName", "apiKeyEnv", "api", "baseURL", "models", "contextWindow", "maxTokens", "reasoningEfforts", "provider"}
	if i := slices.Index(order, name); i >= 0 {
		return i
	}
	if i := slices.Index(dshEfforts, name); i >= 0 {
		return len(order) + i
	}
	return format.NoRank(name)
}

// dshModelEntry is the model entry of a route, with what the catalog says
// of the model.
func dshModelEntry(id string, details cc.Catalog) map[string]any {
	entry := map[string]any{"id": id}
	d, ok := details[id]
	if !ok {
		return entry
	}
	if d.ContextWindow != nil {
		entry["contextWindow"] = number(*d.ContextWindow)
	}
	if d.MaxTokens != nil {
		entry["maxTokens"] = number(*d.MaxTokens)
	}
	efforts := map[string]any{}
	for _, level := range d.Reasoning {
		name := level
		if level == "none" {
			name = "off"
		}
		if slices.Contains(dshEfforts, name) {
			efforts[name] = level
		}
	}
	// dsh takes no levels, or `off` alone, as a broken route.
	for name := range efforts {
		if name != "off" {
			entry["reasoningEfforts"] = efforts
			break
		}
	}
	return entry
}

// dshRow is the last row dsh reads for id: one with no `insert`, naming no
// other package.
func dshRow(value any, id, pkg string) (int, bool) {
	rows, _ := value.([]any)
	for i := len(rows) - 1; i >= 0; i-- {
		row, ok := format.YAMLMappingOf(rows[i])
		if !ok || row["id"] != id {
			continue
		}
		if _, insert := row["insert"]; insert {
			continue
		}
		if name, named := row["name"]; !named || name == pkg {
			return i, true
		}
	}
	return 0, false
}

// dshPath is where key is in the patch file, if its row is there.
func dshPath(document *format.YAMLDocument, key string) ([]format.YAMLStep, bool) {
	if route, ok := strings.CutPrefix(key, dshRoutePrefix); ok {
		at, ok := dshRow(document.Value(), dshModelsRow, dshModelsPackage)
		if !ok {
			return nil, false
		}
		return []format.YAMLStep{format.YAMLIndex(at), format.YAMLKey("config"), format.YAMLKey("providers"), format.YAMLKey(route)}, true
	}
	if key != dshDefaultKey {
		return nil, false
	}
	at, ok := dshRow(document.Value(), dshDefaultRow, dshDefaultPackage)
	if !ok {
		return nil, false
	}
	return []format.YAMLStep{format.YAMLIndex(at), format.YAMLKey("config")}, true
}

func dshJoin(path []format.YAMLStep, name string) []format.YAMLStep {
	return append(slices.Clone(path), format.YAMLKey(name))
}

// dshRoutePart is AstrLink's part of the route at path: its
// dshRouteFields. A route that is not a mapping is all AstrLink's to
// replace.
func dshRoutePart(document *format.YAMLDocument, path []format.YAMLStep) (any, bool) {
	value, ok := document.Get(path...)
	if !ok {
		return nil, false
	}
	route, isMapping := format.YAMLMappingOf(value)
	if !isMapping {
		return document.GetJSON(path...)
	}
	part := map[string]any{}
	for _, name := range dshRouteFields {
		if _, ok := route[name]; ok {
			part[name], _ = document.GetJSON(dshJoin(path, name)...)
		}
	}
	return part, true
}

// dshPatch is `cordis.patch.yml` of one profile.
type dshPatch struct{}

// dshCredentials is `~/.dsh/.credentials.yaml`, which every profile reads.
type dshCredentials struct{}

func dshSetRoute(document *format.YAMLDocument, route string, value any) error {
	at, ok := dshRow(document.Value(), dshModelsRow, dshModelsPackage)
	if !ok {
		row := map[string]any{"id": dshModelsRow, "config": map[string]any{"providers": map[string]any{route: value}}}
		return document.Push(nil, row, dshRank)
	}
	here := []format.YAMLStep{format.YAMLIndex(at)}
	if disabled, _ := document.Get(dshJoin(here, "disabled")...); disabled == true {
		return fmt.Errorf("has its %s row turned off", dshModelsRow)
	}
	config := dshJoin(here, "config")
	switch current, _ := document.Get(config...); {
	case current == nil:
		return document.Set(here, "config", map[string]any{"providers": map[string]any{route: value}}, dshRank)
	default:
		if _, ok := format.YAMLMappingOf(current); !ok {
			return fmt.Errorf("has a %s config that is not a mapping", dshModelsRow)
		}
	}
	providers := dshJoin(config, "providers")
	current, _ := document.Get(providers...)
	if current == nil {
		return document.Set(config, "providers", map[string]any{route: value}, dshRank)
	}
	if _, ok := format.YAMLMappingOf(current); !ok {
		return fmt.Errorf("has %s providers that are not a mapping", dshModelsRow)
	}
	path := dshJoin(providers, route)
	now, _ := document.Get(path...)
	_, nowMapping := format.YAMLMappingOf(now)
	fields, isObject := value.(map[string]any)
	if nowMapping && isObject {
		return dshMergeRoute(document, path, fields)
	}
	return document.Set(providers, route, value, dshRank)
}

// dshMergeRoute writes fields into the route at path one property at a
// time, so the user's own properties and comments around them stay. A
// property AstrLink owns and fields lacks goes.
func dshMergeRoute(document *format.YAMLDocument, path []format.YAMLStep, fields map[string]any) error {
	for _, name := range dshRouteFields {
		if _, ok := fields[name]; !ok {
			if err := document.Remove(dshJoin(path, name)...); err != nil {
				return err
			}
		}
	}
	for _, name := range format.SortedKeys(fields) {
		// Unchanged properties keep their text.
		if now, ok := document.GetJSON(dshJoin(path, name)...); ok && format.Encode(now) == format.Encode(fields[name]) {
			continue
		}
		if err := document.Set(path, name, fields[name], dshRank); err != nil {
			return err
		}
	}
	return nil
}

func dshSetDefault(document *format.YAMLDocument, value any) error {
	if at, ok := dshRow(document.Value(), dshDefaultRow, dshDefaultPackage); ok {
		return document.Set([]format.YAMLStep{format.YAMLIndex(at)}, "config", value, dshRank)
	}
	return document.Push(nil, map[string]any{"id": dshDefaultRow, "config": value}, dshRank)
}

// dshSpent reports whether a row holds nothing beyond what AstrLink leaves
// once its values went: its id, and an empty route list.
func dshSpent(value any) bool {
	row, ok := format.YAMLMappingOf(value)
	if !ok {
		return false
	}
	id, _ := row["id"].(string)
	var rest []string
	for name := range row {
		if name != "id" {
			rest = append(rest, name)
		}
	}
	switch {
	case id == dshDefaultRow && len(rest) == 0:
		return true
	case id == dshModelsRow && len(rest) == 1 && rest[0] == "config":
		config, ok := format.YAMLMappingOf(row["config"])
		if !ok {
			return false
		}
		if len(config) == 0 {
			return true
		}
		providers, ok := format.YAMLMappingOf(config["providers"])
		return len(config) == 1 && ok && len(providers) == 0
	}
	return false
}

func (dshPatch) Format() format.Format { return format.YAMLFormat{Root: format.YAMLSequence} }

func (dshPatch) Holds(key string) bool {
	return strings.HasPrefix(key, dshRoutePrefix) || key == dshDefaultKey
}

func (dshPatch) Get(document format.Document, key string) (string, bool, bool) {
	yaml := format.AsYAML(document)
	if yaml == nil {
		return "", false, false
	}
	path, ok := dshPath(yaml, key)
	if !ok {
		return "", false, false
	}
	var value any
	if strings.HasPrefix(key, dshRoutePrefix) {
		value, ok = dshRoutePart(yaml, path)
	} else {
		value, ok = yaml.GetJSON(path...)
	}
	if !ok {
		return "", false, false
	}
	return format.Encode(value), true, true
}

func (dshPatch) Set(document format.Document, key, value string) error {
	yaml := format.AsYAML(document)
	if yaml == nil {
		return fmt.Errorf("is not YAML")
	}
	parsed, err := format.DecodeJSON(value)
	if err != nil {
		return err
	}
	if route, ok := strings.CutPrefix(key, dshRoutePrefix); ok {
		return dshSetRoute(yaml, route, parsed)
	}
	return dshSetDefault(yaml, parsed)
}

func (dshPatch) Remove(document format.Document, key string) {
	if yaml := format.AsYAML(document); yaml != nil {
		if path, ok := dshPath(yaml, key); ok {
			_ = yaml.Remove(path...)
		}
	}
}

// Tidy drops rows AstrLink added once they hold nothing.
func (dshPatch) Tidy(document format.Document) {
	yaml := format.AsYAML(document)
	if yaml == nil {
		return
	}
	for {
		rows, _ := yaml.Value().([]any)
		at := -1
		for i := len(rows) - 1; i >= 0; i-- {
			if dshSpent(rows[i]) {
				at = i
				break
			}
		}
		if at < 0 || yaml.Remove(format.YAMLIndex(at)) != nil {
			return
		}
	}
}

func (dshCredentials) Format() format.Format { return format.YAMLFormat{Root: format.YAMLMapping} }

func (dshCredentials) Secret() bool { return true }

func (dshCredentials) Holds(key string) bool { return strings.HasPrefix(key, dshRefPrefix) }

// Set starts a new file with the version dsh reads.
func (dshCredentials) Set(document format.Document, key, value string) error {
	if yaml := format.AsYAML(document); yaml != nil && yaml.Value() == nil {
		if err := yaml.Set(nil, "version", format.Number("1"), func(string) int { return 0 }); err != nil {
			return err
		}
	}
	return document.Set(key, value)
}

// Tidy drops an emptied ref list.
func (dshCredentials) Tidy(document format.Document) {
	if yaml := format.AsYAML(document); yaml != nil {
		if refs, ok := yaml.Get(format.YAMLKey("refs")); ok {
			if refs, ok := format.YAMLMappingOf(refs); ok && len(refs) == 0 {
				_ = yaml.Remove(format.YAMLKey("refs"))
			}
		}
	}
}

// IsEmpty reports whether nothing but the version is left.
func (dshCredentials) IsEmpty(document format.Document) bool {
	yaml := format.AsYAML(document)
	if yaml == nil {
		return document.IsEmpty()
	}
	root, ok := format.YAMLMappingOf(yaml.Value())
	if !ok {
		return true
	}
	for name := range root {
		if name != "version" {
			return false
		}
	}
	return true
}

func dshDir(home agents.Home) string { return home.Join(".dsh") }

func (deepSeekHarness) ID() string   { return "deepseek_harness" }
func (deepSeekHarness) Name() string { return "DeepSeek Harness" }
func (deepSeekHarness) Protocols() []string {
	return []string{"openai.chat", "anthropic.messages", "openai.responses"}
}

// Targets are the `web` profile `dsh web` runs, and the desktop app's own.
func (deepSeekHarness) Targets() []string { return []string{"web", "desktop"} }
func (deepSeekHarness) ListsModels() bool { return true }

func (deepSeekHarness) Dir(home agents.Home, target string) string {
	if target == "" {
		target = "web"
	}
	return filepath.Join(dshDir(home), "profiles", target)
}

func (deepSeekHarness) Files() []cc.File { return []cc.File{dshPatch{}, dshCredentials{}} }

func (d deepSeekHarness) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	return []string{filepath.Join(d.Dir(home, target), "cordis.patch.yml"), filepath.Join(dshDir(home), ".credentials.yaml")}
}

// ConnectionKeys is empty: the routes and the token carry the connection.
func (deepSeekHarness) ConnectionKeys() []string { return []string{} }

func (deepSeekHarness) HoldsConnection(key string) bool {
	return strings.HasPrefix(key, dshRoutePrefix) || strings.HasPrefix(key, dshRefPrefix)
}

func (deepSeekHarness) CurrentModels(config *cc.Config) cc.CurrentModels {
	if len(config.Documents) == 0 {
		return cc.CurrentModels{}
	}
	yaml := format.AsYAML(config.Documents[0])
	if yaml == nil {
		return cc.CurrentModels{}
	}
	var ids []string
	for _, route := range dshRoutes {
		path, ok := dshPath(yaml, dshRouteKey(route.name))
		if !ok {
			continue
		}
		models, _ := yaml.Get(dshJoin(path, "models")...)
		entries, _ := models.([]any)
		for _, item := range entries {
			entry, _ := format.YAMLMappingOf(item)
			if id, ok := entry["id"].(string); ok {
				if id, ok := cc.CleanModel(id); ok && !slices.Contains(ids, id) {
					ids = append(ids, id)
				}
			}
		}
	}
	if len(ids) == 0 {
		return cc.CurrentModels{}
	}
	models := cc.CurrentModels{Model: cc.Some(ids[0]), Models: ids}
	if path, ok := dshPath(yaml, dshDefaultKey); ok {
		value, _ := yaml.Get(path...)
		chosen, _ := format.YAMLMappingOf(value)
		provider, _ := chosen["provider"].(string)
		model, isString := chosen["model"].(string)
		if strings.HasPrefix(provider, "astrlink-") && isString && slices.Contains(ids, model) {
			models.Model = &model
		}
	}
	return models
}

func (d deepSeekHarness) DesiredKeys(connection *cc.Connection, details cc.Catalog) []cc.Setting {
	reference := dshReference(connection.Target)
	list := connection.ModelList()
	var keys []cc.Setting
	for _, route := range dshRoutes {
		var models []any
		for _, id := range list {
			if connection.Protocol(DeepSeekHarness, id) == route.protocol {
				models = append(models, dshModelEntry(id, details))
			}
		}
		if len(models) == 0 {
			continue
		}
		value := map[string]any{
			"displayName": route.display,
			"apiKeyEnv":   reference,
			"api":         route.api,
			"baseURL":     dshRouteURL(route.name, connection.Origin),
			"models":      models,
		}
		keys = append(keys, cc.Setting{Key: dshRouteKey(route.name), Value: format.Encode(value)})
	}
	if model, ok := connection.Model("model"); ok {
		protocol := connection.Protocol(DeepSeekHarness, model)
		for _, route := range dshRoutes {
			if route.protocol == protocol {
				value := map[string]any{"provider": route.name, "model": model}
				keys = append(keys, cc.Setting{Key: dshDefaultKey, Value: format.Encode(value)})
				break
			}
		}
	}
	return append(keys, cc.Setting{Key: dshRefPrefix + reference, Value: connection.Token})
}

// BaseURLKey is unused: SyncedKeys points every route elsewhere.
func (deepSeekHarness) BaseURLKey() string { return "" }

func (deepSeekHarness) SyncedKeys(config *cc.Config, record *cc.Record, origin string) []cc.Setting {
	var keys []cc.Setting
	for _, key := range sortedKeys(record.Keys) {
		route, ok := strings.CutPrefix(key, dshRoutePrefix)
		if !ok {
			continue
		}
		text, ok := config.String(key)
		if !ok {
			continue
		}
		value, err := format.DecodeJSON(text)
		object, isObject := value.(map[string]any)
		if err != nil || !isObject {
			continue
		}
		object["baseURL"] = dshRouteURL(route, origin)
		keys = append(keys, cc.Setting{Key: key, Value: format.Encode(object)})
	}
	return keys
}

// Notes has `overridden` when a patch in `~/.dsh` itself overrides every
// profile's routes or default model.
func (deepSeekHarness) Notes(home agents.Home, _ string, _ *cc.Config) []string {
	text, err := os.ReadFile(filepath.Join(dshDir(home), "cordis.patch.yml"))
	if err != nil {
		return nil
	}
	value, err := format.ReadYAML(string(text))
	if err != nil {
		return nil
	}
	rows, _ := value.([]any)
	for _, row := range [][2]string{{dshModelsRow, dshModelsPackage}, {dshDefaultRow, dshDefaultPackage}} {
		if at, ok := dshRow(value, row[0], row[1]); ok {
			if mapping, ok := format.YAMLMappingOf(rows[at]); ok {
				if _, configured := mapping["config"]; configured {
					return []string{"overridden"}
				}
			}
		}
	}
	return nil
}
