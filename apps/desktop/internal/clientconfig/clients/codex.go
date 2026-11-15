package clients

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Codex reads its provider, model, and reasoning effort from
// `config.toml`. `auth.json`, its login, is never touched.

var Codex = cc.New(codex{})

const (
	CodexProvider       = "astrlink"
	codexProviderPrefix = "model_providers.astrlink."
	// codexHeadersKey: Codex shows its own image generation and web search
	// tools only for a provider whose `http_headers` set this header. The
	// gateway answers both tools and strips the header before forwarding;
	// any non-empty value works. AstrLink owns this one header, and other
	// headers in the table stay the user's.
	codexHeadersKey       = "model_providers.astrlink.http_headers"
	codexToolsHeader      = "x-openai-actor-authorization"
	codexToolsHeaderValue = "codex-imagegen"
	// codexReasoningKey is how hard Codex's model reasons; left to Codex
	// when not set.
	codexReasoningKey = "model_reasoning_effort"
)

type codex struct{}

// codexConfig is `config.toml`.
type codexConfig struct{}

// codexToolsHeaderName is AstrLink's entry in Codex's `http_headers`,
// matched case-insensitively as Codex matches it.
func codexToolsHeaderName(headers *format.TOMLTable) (string, bool) {
	for _, name := range headers.Names() {
		if strings.EqualFold(name, codexToolsHeader) {
			return name, true
		}
	}
	return "", false
}

// setCodexToolsHeader adds AstrLink's header to the provider's
// `http_headers` next to any other header there. A value that is not a
// table is replaced.
func setCodexToolsHeader(provider *format.TOMLTable, value string) {
	if format.TableLike(provider.Get("http_headers")) == nil {
		provider.Insert("http_headers", format.NewTOMLInlineTable())
	}
	headers := format.TableLike(provider.Get("http_headers"))
	if headers == nil {
		return
	}
	name, ok := codexToolsHeaderName(headers)
	if !ok {
		name = codexToolsHeader
	}
	format.SetTOMLString(headers, name, value)
}

func (codex) ID() string          { return "codex" }
func (codex) Name() string        { return "Codex" }
func (codex) Protocols() []string { return []string{"openai.responses"} }

func (codex) Dir(home agents.Home, _ string) string { return home.CodexDir() }

func (codex) Files() []cc.File { return []cc.File{codexConfig{}} }

func (codex) ConfigPaths(home agents.Home, _ string, _ *cc.Record) []string {
	return []string{filepath.Join(home.CodexDir(), "config.toml")}
}

func (codex) ReasoningEfforts() []string {
	return []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra", "persistent"}
}

func (codex) ConnectionKeys() []string {
	return []string{
		"model_provider",
		"model_providers.astrlink.base_url",
		"model_providers.astrlink.wire_api",
		"model_providers.astrlink.experimental_bearer_token",
	}
}

func (codex) CurrentModels(config *cc.Config) cc.CurrentModels {
	var models cc.CurrentModels
	if model, ok := config.Model("model"); ok {
		models.Model = &model
	}
	if effort, ok := config.Model(codexReasoningKey); ok {
		models.ReasoningEffort = &effort
	}
	return models
}

func (codex) DesiredKeys(connection *cc.Connection, _ cc.Catalog) []cc.Setting {
	keys := []cc.Setting{{Key: "model_provider", Value: CodexProvider}}
	if model, ok := connection.Model("model"); ok {
		keys = append(keys, cc.Setting{Key: "model", Value: model})
	}
	if effort, ok := connection.Model("reasoningEffort"); ok {
		keys = append(keys, cc.Setting{Key: codexReasoningKey, Value: effort})
	}
	// The display name is local UI; a reserved ID or the name "OpenAI"
	// would make Codex treat this as its built-in provider.
	for _, kv := range [][2]string{
		{"name", "AstrLink"},
		{"base_url", Codex.BaseURL(connection.Origin)},
		{"wire_api", "responses"},
		{"experimental_bearer_token", connection.Token},
	} {
		keys = append(keys, cc.Setting{Key: codexProviderPrefix + kv[0], Value: kv[1]})
	}
	return append(keys, cc.Setting{Key: codexHeadersKey, Value: codexToolsHeaderValue})
}

func (codex) BaseURLKey() string { return "model_providers.astrlink.base_url" }

func (codex) AddedKeys() []cc.Setting {
	return []cc.Setting{{Key: codexHeadersKey, Value: codexToolsHeaderValue}}
}

func (codexConfig) Format() format.Format { return format.TOMLFormat{} }

func codexProviderTable(root *format.TOMLTable) (item format.TOMLItem, table *format.TOMLTable) {
	providers := format.TableLike(root.Get("model_providers"))
	if providers == nil {
		return nil, nil
	}
	item = providers.Get(CodexProvider)
	return item, format.TableLike(item)
}

func (c codexConfig) Group(document format.Document) []format.KeyValue {
	var keys []format.KeyValue
	toml, ok := document.(*format.TOMLDocument)
	if !ok {
		return keys
	}
	for _, key := range []string{"model_provider", "model"} {
		if item := toml.Root().Get(key); item != nil {
			value, ok := format.TOMLItemString(item)
			keys = append(keys, format.KeyValue{Key: key, Value: value, Present: ok})
		}
	}
	switch item, table := codexProviderTable(toml.Root()); {
	case table != nil:
		for _, name := range table.Names() {
			path := codexProviderPrefix + name
			// Headers without AstrLink's own are the user's.
			if value, ok, present := c.Get(document, path); present {
				keys = append(keys, format.KeyValue{Key: path, Value: value, Present: ok})
			}
		}
	case item != nil:
		keys = append(keys, format.KeyValue{Key: "model_providers.astrlink"})
	}
	return keys
}

func (codexConfig) Get(document format.Document, path string) (string, bool, bool) {
	toml, ok := document.(*format.TOMLDocument)
	key, provider := strings.CutPrefix(path, codexProviderPrefix)
	if !ok || !provider {
		return document.Get(path)
	}
	_, table := codexProviderTable(toml.Root())
	if table == nil {
		return "", false, false
	}
	item := table.Get(key)
	if item == nil {
		return "", false, false
	}
	if path != codexHeadersKey {
		value, ok := format.TOMLItemString(item)
		return value, ok, true
	}
	headers := format.TableLike(item)
	if headers == nil {
		return "", false, true
	}
	name, found := codexToolsHeaderName(headers)
	if !found {
		return "", false, false
	}
	value, ok := format.TOMLItemString(headers.Get(name))
	return value, ok, true
}

func (codexConfig) Set(document format.Document, path, value string) error {
	toml, ok := document.(*format.TOMLDocument)
	key, provider := strings.CutPrefix(path, codexProviderPrefix)
	if !ok || !provider {
		return document.Set(path, value)
	}
	root := toml.Root()
	if !root.ContainsKey("model_providers") {
		providers := format.NewTOMLTable()
		providers.SetImplicit(true)
		root.Insert("model_providers", providers)
	}
	providers := format.TableLike(root.Get("model_providers"))
	if providers == nil {
		return errors.New("has a model_providers value that is not a table")
	}
	if format.TableLike(providers.Get(CodexProvider)) == nil {
		if providers.IsInline() {
			providers.Insert(CodexProvider, format.NewTOMLInlineTable())
		} else {
			providers.Insert(CodexProvider, format.NewTOMLTable())
		}
	}
	table := format.TableLike(providers.Get(CodexProvider))
	if table == nil {
		return errors.New("unable to create model_providers.astrlink")
	}
	if path == codexHeadersKey {
		setCodexToolsHeader(table, value)
	} else {
		format.SetTOMLString(table, key, value)
	}
	return nil
}

func (codexConfig) Remove(document format.Document, path string) {
	toml, ok := document.(*format.TOMLDocument)
	if !ok {
		document.Remove(path)
		return
	}
	providers := format.TableLike(toml.Root().Get("model_providers"))
	if path == "model_providers.astrlink" {
		if providers != nil {
			providers.Remove(CodexProvider)
		}
		return
	}
	key, provider := strings.CutPrefix(path, codexProviderPrefix)
	if !provider {
		toml.Remove(path)
		return
	}
	if providers == nil {
		return
	}
	table := format.TableLike(providers.Get(CodexProvider))
	if table == nil {
		return
	}
	if headers := format.TableLike(table.Get("http_headers")); headers != nil && path == codexHeadersKey {
		if name, ok := codexToolsHeaderName(headers); ok {
			headers.Remove(name)
		}
		return
	}
	table.Remove(key)
}

func (codexConfig) Tidy(document format.Document) {
	toml, ok := document.(*format.TOMLDocument)
	if !ok {
		return
	}
	empty := func(item format.TOMLItem) bool {
		table := format.TableLike(item)
		return table != nil && table.IsEmpty()
	}
	if providers := format.TableLike(toml.Root().Get("model_providers")); providers != nil {
		if provider := format.TableLike(providers.Get(CodexProvider)); provider != nil && empty(provider.Get("http_headers")) {
			provider.Remove("http_headers")
		}
		if empty(providers.Get(CodexProvider)) {
			providers.Remove(CodexProvider)
		}
	}
	if empty(toml.Root().Get("model_providers")) {
		toml.Root().Remove("model_providers")
	}
}

// ReadCodexConfig reads Codex's config.toml as Codex does, for importing
// its providers.
func ReadCodexConfig(text string) (*format.TOMLDocument, error) {
	toml, err := format.ParseTOML(text)
	if err != nil {
		if invalid, ok := err.(*format.Invalid); ok {
			return nil, errors.New(invalid.Message)
		}
		return nil, err
	}
	return toml, nil
}
