package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// A new-api plugin's provider part relays ordinary API requests: each
// meta.relay route says which client path it serves, for which models,
// where the request goes upstream and in what protocol. A plugin without
// hooks is routes and static keys, and no JavaScript runs on its requests;
// prepareRequest and signRequest adjust a request before it is sent.

// relayHooks are the hooks of the provider part, and of the credential
// hooks that also serve a task part.
var relayHooks = []string{
	"signRequest", "prepareRequest", "credentialRejected",
	"convertRequest", "convertResponse", "convertStreamEvent",
	"checkKey", "exchangeKey", "login", "refresh", "revoke",
	"buildBalanceRequest", "parseBalance", "scheduled",
}

// RelayRoute is one route of meta.relay, with the keys and rules of a
// new-api Advanced Custom channel route.
type RelayRoute struct {
	IncomingPath string     `json:"incoming_path"`
	UpstreamPath string     `json:"upstream_path"`
	Converter    string     `json:"converter,omitempty"`
	Models       []string   `json:"models,omitempty"`
	Auth         *RelayAuth `json:"auth,omitempty"`
	PassThrough  bool       `json:"pass_through_body_enabled,omitempty"`
}

// RelayAuth is how a route sends the provider's key: as a bearer token
// without it, none, or in a header or query parameter whose value has
// {api_key} for the key.
type RelayAuth struct {
	Type  string `json:"type"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

// Converters of a relay route besides none and the plugin's own.
const (
	RelayConverterNone   = "none"
	RelayConverterPlugin = "plugin"
)

// relayConverter is a relaykit converter a route may name: the client
// protocol it takes, at the paths it serves, and the upstream protocol it
// sends.
type relayConverter struct {
	client   contract.ProtocolID
	upstream contract.ProtocolID
	serves   func(path string) bool
}

func exactPath(paths ...string) func(string) bool {
	return func(path string) bool { return slices.Contains(paths, path) }
}

func geminiPath(path string) bool {
	return strings.Contains(path, ":generateContent") || strings.Contains(path, ":streamGenerateContent")
}

// relayConverters are relaykit's converter IDs, as new-api's routes spell
// them.
var relayConverters = map[string]relayConverter{
	"openai_chat_completions_to_anthropic_messages":      {contract.ProtocolOpenAIChat, contract.ProtocolAnthropicMessages, exactPath("/v1/chat/completions")},
	"openai_chat_completions_to_openai_responses":        {contract.ProtocolOpenAIChat, contract.ProtocolOpenAIResponses, exactPath("/v1/chat/completions")},
	"openai_chat_completions_to_gemini_generate_content": {contract.ProtocolOpenAIChat, contract.ProtocolGoogleGenerateContent, exactPath("/v1/chat/completions")},
	"openai_responses_to_openai_chat_completions":        {contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIChat, exactPath("/v1/responses")},
	"openai_responses_to_gemini_generate_content":        {contract.ProtocolOpenAIResponses, contract.ProtocolGoogleGenerateContent, exactPath("/v1/responses")},
	"anthropic_messages_to_openai_chat_completions":      {contract.ProtocolAnthropicMessages, contract.ProtocolOpenAIChat, exactPath("/v1/messages")},
	"claude_messages_to_openai_responses":                {contract.ProtocolAnthropicMessages, contract.ProtocolOpenAIResponses, exactPath("/v1/messages")},
	"gemini_generate_content_to_openai_chat_completions": {contract.ProtocolGoogleGenerateContent, contract.ProtocolOpenAIChat, geminiPath},
	"gemini_generate_content_to_openai_responses":        {contract.ProtocolGoogleGenerateContent, contract.ProtocolOpenAIResponses, geminiPath},
	"jina_rerank_to_sglang":                              {contract.ProtocolRerank, contract.ProtocolRerank, exactPath("/v1/rerank", "/rerank")},
}

// The management routes of a provider: its model list and its balance.
const (
	RelayModelsPath  = "/v1/models"
	relayBalancePath = "/v1/dashboard/billing/credit_grants"
	relaySearchPath  = "/v1/alpha/search"
	relayRealtime    = "/v1/realtime"
)

// Bounds of meta.relay.
const (
	maxRelayRoutes = 64
	maxRelayModels = 512
)

var configPrefixPattern = regexp.MustCompile(`^\{config\.([A-Za-z][A-Za-z0-9_]{0,63})\}`)

// RelayProtocol is the gateway protocol of a client path a route serves,
// "" for one the gateway does not answer.
func RelayProtocol(path string) contract.ProtocolID {
	switch path {
	case "/v1/chat/completions":
		return contract.ProtocolOpenAIChat
	case "/v1/responses":
		return contract.ProtocolOpenAIResponses
	case "/v1/responses/compact":
		return contract.ProtocolOpenAIResponsesCompact
	case "/v1/messages":
		return contract.ProtocolAnthropicMessages
	case "/v1/completions":
		return contract.ProtocolOpenAICompletions
	case "/v1/embeddings":
		return contract.ProtocolOpenAIEmbeddings
	case "/v1/rerank", "/rerank":
		return contract.ProtocolRerank
	case "/v1/images/generations", "/v1/images/edits":
		return contract.ProtocolOpenAIImages
	case relaySearchPath:
		return contract.ProtocolOpenAISearch
	case RelayModelsPath:
		return contract.ProtocolOpenAIModels
	}
	if strings.HasPrefix(path, "/v1beta/models/") && geminiPath(path) {
		return contract.ProtocolGoogleGenerateContent
	}
	return ""
}

// Plugin reports whether the route's hooks convert it, alone or after a
// relaykit converter.
func (route RelayRoute) Plugin() bool {
	return route.Converter == RelayConverterPlugin || strings.HasSuffix(route.Converter, "+"+RelayConverterPlugin)
}

// Relaykit is the relaykit converter the route names, "" for none.
func (route RelayRoute) Relaykit() string {
	name := strings.TrimSuffix(route.Converter, "+"+RelayConverterPlugin)
	if name == RelayConverterNone || name == RelayConverterPlugin {
		return ""
	}
	return name
}

// UpstreamProtocol is the protocol the route's request goes upstream in,
// before the plugin's hooks convert it: the client's without a relaykit
// converter.
func (route RelayRoute) UpstreamProtocol() contract.ProtocolID {
	if converter, ok := relayConverters[route.Relaykit()]; ok {
		return converter.upstream
	}
	return RelayProtocol(route.IncomingPath)
}

// Serves reports whether the route serves model: one of its models, or any
// model for a route without models.
func (route RelayRoute) Serves(model string) bool {
	if len(route.Models) == 0 {
		return true
	}
	for _, listed := range route.Models {
		if pattern, ok := strings.CutPrefix(listed, "re:"); ok {
			if matched, err := regexp.MatchString(pattern, model); err == nil && matched {
				return true
			}
		} else if listed == model {
			return true
		}
	}
	return false
}

// MatchesPath reports whether the route serves the client path: its
// incoming_path, with {model} standing for one segment, and a
// :generateContent path also serving :streamGenerateContent.
func (route RelayRoute) MatchesPath(path string) bool {
	for _, candidate := range []string{route.IncomingPath, strings.Replace(route.IncomingPath, ":generateContent", ":streamGenerateContent", 1)} {
		before, after, found := strings.Cut(candidate, "{model}")
		if !found {
			if candidate == path {
				return true
			}
			continue
		}
		if len(path) > len(before)+len(after) && strings.HasPrefix(path, before) && strings.HasSuffix(path, after) &&
			!strings.Contains(path[len(before):len(path)-len(after)], "/") {
			return true
		}
	}
	return false
}

// RelayRouteFor is the route of meta.relay that serves model on path: a
// route that lists the model before the route of the path for every other
// model.
func (meta *TaskMeta) RelayRouteFor(path, model string) *RelayRoute {
	var fallback *RelayRoute
	for index := range meta.Relay {
		route := &meta.Relay[index]
		if !route.MatchesPath(path) {
			continue
		}
		if len(route.Models) == 0 {
			if fallback == nil {
				fallback = route
			}
			continue
		}
		if route.Serves(model) {
			return route
		}
	}
	return fallback
}

// validateProviderPart checks meta.relay, meta.channelConfig and
// meta.keyHint, and the provider hooks against them.
func validateProviderPart(meta *TaskMeta, exported map[string]bool) error {
	if len(meta.Relay) == 0 {
		for _, hook := range []string{"prepareRequest", "credentialRejected", "convertRequest", "convertResponse", "convertStreamEvent"} {
			if exported[hook] {
				return fmt.Errorf("%s needs a provider part: list routes in meta.relay", hook)
			}
		}
	}
	if !meta.taskPart && len(meta.Relay) == 0 && (len(meta.ChannelConfig) > 0 || len(meta.KeyHint) > 0) {
		return errors.New("only a plugin with a task part or meta.relay declares channelConfig and keyHint")
	}
	if exported["buildBalanceRequest"] != exported["parseBalance"] {
		return errors.New("buildBalanceRequest and parseBalance are exported together")
	}
	if err := validateLocalized(meta.KeyHint, 256, "meta.keyHint"); err != nil {
		return err
	}
	urlFields, err := validateChannelConfig(meta.ChannelConfig)
	if err != nil {
		return err
	}
	if len(meta.Relay) > maxRelayRoutes {
		return fmt.Errorf("meta.relay lists at most %d routes", maxRelayRoutes)
	}
	type pathRoutes struct {
		fallback bool
		models   map[string]bool
	}
	paths := map[string]*pathRoutes{}
	management := map[string]bool{}
	pluginRoutes := 0
	modelsOnly := true
	for index := range meta.Relay {
		route := &meta.Relay[index]
		where := fmt.Sprintf("meta.relay[%d]", index)
		if route.Converter == "" {
			route.Converter = RelayConverterNone
		}
		if err := validateRelayPaths(route, urlFields); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		relaykit, plugged := route.Relaykit(), route.Plugin()
		if relaykit != "" {
			converter, ok := relayConverters[relaykit]
			if !ok {
				return fmt.Errorf("%s.converter %q is not none, plugin or a converter of relaykit", where, route.Converter)
			}
			if !converter.serves(route.IncomingPath) {
				return fmt.Errorf("%s.converter %s does not serve %s", where, relaykit, route.IncomingPath)
			}
		} else if route.Converter != RelayConverterNone && route.Converter != RelayConverterPlugin {
			return fmt.Errorf("%s.converter %q is not none, plugin or a converter of relaykit", where, route.Converter)
		}
		if route.PassThrough && (plugged || (relaykit != "" && relaykit != "jina_rerank_to_sglang")) {
			return fmt.Errorf("%s: pass_through_body_enabled goes with converter none or jina_rerank_to_sglang", where)
		}
		if plugged {
			pluginRoutes++
			if route.IncomingPath != RelayModelsPath {
				modelsOnly = false
			}
			if route.IncomingPath == relayRealtime || route.IncomingPath == relayBalancePath {
				return fmt.Errorf("%s: the plugin's hooks cannot convert %s", where, route.IncomingPath)
			}
		}
		if err := validateRelayAuth(route.Auth); err != nil {
			return fmt.Errorf("%s.auth: %w", where, err)
		}
		if len(route.Models) > maxRelayModels {
			return fmt.Errorf("%s.models lists at most %d models", where, maxRelayModels)
		}
		switch route.IncomingPath {
		case RelayModelsPath, relayBalancePath:
			switch {
			case management[route.IncomingPath]:
				return fmt.Errorf("%s: meta.relay has one route for %s", where, route.IncomingPath)
			case len(route.Models) > 0 || route.PassThrough || strings.Contains(route.UpstreamPath, "{model}"):
				return fmt.Errorf("%s: the management route %s has no models, {model} or pass-through", where, route.IncomingPath)
			case relaykit != "" || (plugged && route.IncomingPath != RelayModelsPath):
				return fmt.Errorf("%s: the management route %s takes converter none", where, route.IncomingPath)
			}
			management[route.IncomingPath] = true
			continue
		case relaySearchPath:
			if route.Converter != RelayConverterNone {
				return fmt.Errorf("%s: %s takes converter none", where, relaySearchPath)
			}
		}
		routes := paths[route.IncomingPath]
		if routes == nil {
			routes = &pathRoutes{models: map[string]bool{}}
			paths[route.IncomingPath] = routes
		}
		if len(route.Models) == 0 {
			if routes.fallback {
				return fmt.Errorf("%s: %s has one route for every other model", where, route.IncomingPath)
			}
			routes.fallback = true
		}
		for _, model := range route.Models {
			if pattern, ok := strings.CutPrefix(model, "re:"); ok {
				if _, err := regexp.Compile(pattern); err != nil {
					return fmt.Errorf("%s.models: %q is not a regular expression", where, model)
				}
			} else if strings.TrimSpace(model) == "" {
				return fmt.Errorf("%s.models has an empty model", where)
			}
			if routes.models[model] {
				return fmt.Errorf("%s.models: %s serves %q in two routes", where, route.IncomingPath, model)
			}
			routes.models[model] = true
		}
	}
	converts := exported["convertRequest"] || exported["convertResponse"] || exported["convertStreamEvent"]
	switch {
	case pluginRoutes == 0 && converts:
		return errors.New("convertRequest, convertResponse and convertStreamEvent serve a route with converter plugin")
	case pluginRoutes > 0 && modelsOnly && (!exported["convertResponse"] || exported["convertRequest"] || exported["convertStreamEvent"]):
		return errors.New("a plugin whose only plugin route is /v1/models exports convertResponse alone")
	case pluginRoutes > 0 && !modelsOnly && (!exported["convertRequest"] || !exported["convertResponse"] || !exported["convertStreamEvent"]):
		return errors.New("a route with converter plugin needs convertRequest, convertResponse and convertStreamEvent")
	}
	return nil
}

// validateRelayPaths checks a route's incoming and upstream paths.
// urlFields are the url fields of the channel config an upstream path may
// start with.
func validateRelayPaths(route *RelayRoute, urlFields map[string]bool) error {
	incoming := route.IncomingPath
	switch {
	case !strings.HasPrefix(incoming, "/") || strings.ContainsAny(incoming, "?# ") || len(incoming) > 512:
		return fmt.Errorf("incoming_path %q must be a path starting with /, without a query", incoming)
	case strings.Count(incoming, "{model}") > 1 || strings.Count(incoming, "{") != strings.Count(incoming, "{model}"):
		return fmt.Errorf("incoming_path %q may hold {model} once and no other placeholder", incoming)
	case strings.Contains(incoming, "{model}") && (incoming == RelayModelsPath || incoming == relayBalancePath):
		return fmt.Errorf("incoming_path %q is a management path", incoming)
	}
	upstream := route.UpstreamPath
	rest := upstream
	if match := configPrefixPattern.FindStringSubmatch(upstream); match != nil {
		if !urlFields[match[1]] {
			return fmt.Errorf("upstream_path names {config.%s}, which is not a url field of meta.channelConfig", match[1])
		}
		rest = upstream[len(match[0]):]
		if rest != "" && !strings.HasPrefix(rest, "/") {
			return fmt.Errorf("upstream_path %q continues the config address with a path", upstream)
		}
	}
	if strings.Count(rest, "{") != strings.Count(rest, "{model}") || len(upstream) > 2048 || strings.ContainsAny(upstream, " #") {
		return fmt.Errorf("upstream_path %q may hold {model} and a leading {config.<name>}, no other placeholder", upstream)
	}
	if rest == upstream {
		if strings.HasPrefix(upstream, "/") {
			if strings.HasPrefix(upstream, "//") {
				return fmt.Errorf("upstream_path %q starts with a single /", upstream)
			}
		} else if parsed, err := url.Parse(strings.ReplaceAll(upstream, "{model}", "m")); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return fmt.Errorf("upstream_path %q is a path starting with / or an absolute http(s) URL", upstream)
		}
	}
	return nil
}

func validateRelayAuth(auth *RelayAuth) error {
	if auth == nil {
		return nil
	}
	switch auth.Type {
	case "none":
		if auth.Name != "" || auth.Value != "" {
			return errors.New("type none takes no name or value")
		}
	case "header", "query":
		if auth.Name == "" || len(auth.Name) > 128 || strings.ContainsAny(auth.Name, " \r\n:") || auth.Value == "" || len(auth.Value) > 1024 || strings.ContainsAny(auth.Value, "\r\n") {
			return fmt.Errorf("type %s takes a name and a value", auth.Type)
		}
	default:
		return fmt.Errorf("type %q is not none, header or query", auth.Type)
	}
	return nil
}

// validateChannelConfig checks meta.channelConfig and returns its
// top-level url fields.
func validateChannelConfig(raw json.RawMessage) (map[string]bool, error) {
	urlFields := map[string]bool{}
	schema, err := ParseConfigSchema(raw, true)
	if err != nil {
		return nil, fmt.Errorf("meta.channelConfig: %w", err)
	}
	if schema != nil {
		for _, property := range schema.Properties {
			if property.Field.Format == "url" {
				urlFields[property.Name] = true
			}
		}
	}
	return urlFields, nil
}

// ChannelConfigDefaults are the defaults of meta.channelConfig's top-level
// fields, which a provider's settings start from.
func (meta *TaskMeta) ChannelConfigDefaults() map[string]json.RawMessage {
	defaults := map[string]json.RawMessage{}
	if len(meta.ChannelConfig) == 0 {
		return defaults
	}
	var schema struct {
		Properties map[string]struct {
			Default json.RawMessage `json:"default"`
			Format  string          `json:"format"`
		} `json:"properties"`
	}
	if json.Unmarshal(meta.ChannelConfig, &schema) != nil {
		return defaults
	}
	for name, field := range schema.Properties {
		if len(field.Default) == 0 {
			continue
		}
		value := field.Default
		if field.Format == "url" {
			var text string
			if json.Unmarshal(value, &text) == nil {
				value, _ = json.Marshal(strings.TrimRight(text, "/"))
			}
		}
		defaults[name] = value
	}
	return defaults
}

// strictJSON decodes data into target, refusing members target does not
// declare.
func strictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
