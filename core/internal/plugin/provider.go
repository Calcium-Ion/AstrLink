package plugin

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// The hooks of a provider adapter (plugin platform v2, §4): the gateway
// hands the plugin the request it would have sent upstream, in the client's
// protocol, and the plugin describes the upstream request and turns the
// upstream's answer back into that protocol.
const (
	// HookBuildRequest describes the upstream request of one attempt, or of
	// the model list.
	HookBuildRequest = "buildRequest"
	// HookParseResponse turns a whole upstream response into the protocol's
	// response, or the model list's answer into models.
	HookParseResponse = "parseResponse"
	// HookParseStreamEvent turns one upstream event into the protocol's
	// events.
	HookParseStreamEvent = "parseStreamEvent"
	// HookFinishStream adds the protocol's last events once the upstream
	// stream ended.
	HookFinishStream = "finishStream"
	// HookParseError says what an upstream error means: its status, code,
	// message, and whether another provider may serve the request.
	HookParseError = "parseError"
)

// providerHooks are the hooks only a plugin declaring providers exports.
var providerHooks = []string{HookBuildRequest, HookParseResponse, HookParseStreamEvent, HookFinishStream, HookParseError}

// ProviderProtocols are the protocols a provider adapter may serve.
var ProviderProtocols = []contract.ProtocolID{
	contract.ProtocolOpenAIChat,
	contract.ProtocolOpenAIResponses,
	contract.ProtocolAnthropicMessages,
	contract.ProtocolGoogleGenerateContent,
}

// Bounds of meta.providers.
const (
	maxProviders      = 4
	maxProviderModels = 200
)

var providerIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Provider is a kind of provider a plugin adds: the user adds providers of
// it like any other, with its address, key and models.
type Provider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// BaseURL is the address a new provider starts with.
	BaseURL string `json:"baseUrl,omitempty"`
	// Auth is how the gateway sends the provider's key: the plugin never
	// sees it.
	Auth ProviderAuth `json:"auth,omitempty"`
	// Protocols are the protocols the plugin serves, by ID, with the modes
	// each supports.
	Protocols map[contract.ProtocolID]ProviderProtocol `json:"protocols"`
	// Convert lets the gateway convert a request in another protocol into
	// the first of Protocols, and the answer back.
	Convert bool `json:"convert,omitempty"`
	// Models are the models a new provider starts with.
	Models []string `json:"models,omitempty"`
}

// ProviderProtocol is what a provider supports of one protocol: whole
// responses unless Sync is false, streams when Stream is set.
type ProviderProtocol struct {
	Stream bool  `json:"stream,omitempty"`
	Sync   *bool `json:"sync,omitempty"`
}

// Synchronous reports whether the protocol is served with whole responses.
func (protocol ProviderProtocol) Synchronous() bool { return protocol.Sync == nil || *protocol.Sync }

// ProviderAuth is "bearer", "x-api-key", "google", "none", or
// { "header": name } for a header of the provider's own.
type ProviderAuth struct {
	contract.ServiceAuth
}

func (auth *ProviderAuth) UnmarshalJSON(data []byte) error {
	var scheme string
	if json.Unmarshal(data, &scheme) == nil {
		switch scheme {
		case "", "bearer":
			auth.Scheme = contract.AuthSchemeBearer
		case "x-api-key":
			auth.Scheme = contract.AuthSchemeAnthropicAPIKey
		case "google":
			auth.Scheme = contract.AuthSchemeGoogleAPIKey
		case "none":
			auth.Scheme = contract.AuthSchemeNone
		default:
			return fmt.Errorf("auth must be bearer, x-api-key, google, none or { header }")
		}
		return nil
	}
	var header struct {
		Header string `json:"header"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&header); err != nil || header.Header == "" {
		return fmt.Errorf("auth must be bearer, x-api-key, google, none or { header }")
	}
	auth.Scheme, auth.HeaderName = contract.AuthSchemeCustomHeader, header.Header
	return nil
}

func (auth ProviderAuth) MarshalJSON() ([]byte, error) {
	switch auth.Scheme {
	case contract.AuthSchemeAnthropicAPIKey:
		return json.Marshal("x-api-key")
	case contract.AuthSchemeGoogleAPIKey:
		return json.Marshal("google")
	case contract.AuthSchemeNone:
		return json.Marshal("none")
	case contract.AuthSchemeCustomHeader:
		return json.Marshal(map[string]string{"header": auth.HeaderName})
	}
	return json.Marshal("bearer")
}

// Service is how the gateway sends the key, bearer when unset.
func (auth ProviderAuth) Service() contract.ServiceAuth {
	if auth.Scheme == "" {
		return contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}
	}
	return auth.ServiceAuth
}

// ProtocolOrder lists a provider's protocols in the order of
// ProviderProtocols: the first is what conversion targets.
func (provider Provider) ProtocolOrder() []contract.ProtocolID {
	var order []contract.ProtocolID
	for _, protocol := range ProviderProtocols {
		if _, ok := provider.Protocols[protocol]; ok {
			order = append(order, protocol)
		}
	}
	return order
}

// Streams reports whether any of the provider's protocols streams.
func (provider Provider) Streams() bool {
	for _, protocol := range provider.Protocols {
		if protocol.Stream {
			return true
		}
	}
	return false
}

// Provider returns the plugin's provider id, nil when it declares none.
func (meta Meta) Provider(id string) *Provider {
	for index := range meta.Providers {
		if meta.Providers[index].ID == id {
			return &meta.Providers[index]
		}
	}
	return nil
}

// validateProviders checks meta.providers.
func validateProviders(meta Meta) error {
	offers := slices.Contains(meta.Capabilities, CapabilityProvider)
	switch {
	case len(meta.Providers) > 0 && meta.APIVersion < 2:
		return fmt.Errorf("meta.providers needs apiVersion 2")
	case len(meta.Providers) > 0 && !offers:
		return fmt.Errorf("meta.providers needs the %s capability", CapabilityProvider)
	case offers && len(meta.Providers) == 0:
		return fmt.Errorf("the %s capability needs meta.providers", CapabilityProvider)
	case len(meta.Providers) > maxProviders:
		return fmt.Errorf("meta.providers has more than %d entries", maxProviders)
	}
	for index, provider := range meta.Providers {
		at := fmt.Sprintf("meta.providers[%d]", index)
		if !providerIDPattern.MatchString(provider.ID) {
			return fmt.Errorf("%s.id %q must be a lowercase letter, then up to 31 lowercase letters, digits and hyphens", at, provider.ID)
		}
		if slices.ContainsFunc(meta.Providers[:index], func(other Provider) bool { return other.ID == provider.ID }) {
			return fmt.Errorf("meta.providers names %q twice", provider.ID)
		}
		if !displayText(provider.Name, 64, "") {
			return fmt.Errorf("%s.name must be 1 to 64 characters", at)
		}
		if provider.BaseURL != "" {
			parsed, err := url.Parse(provider.BaseURL)
			if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil ||
				parsed.RawQuery != "" || parsed.Fragment != "" || len(provider.BaseURL) > 2048 {
				return fmt.Errorf("%s.baseUrl must be an http or https address without credentials, query or fragment", at)
			}
		}
		if err := provider.Auth.Service().Validate(); err != nil {
			return fmt.Errorf("%s.auth: %v", at, err)
		}
		if len(provider.Protocols) == 0 {
			return fmt.Errorf("%s.protocols must name at least one protocol", at)
		}
		for protocol, modes := range provider.Protocols {
			if !slices.Contains(ProviderProtocols, protocol) {
				return fmt.Errorf("%s.protocols: %q is not one of %s", at, protocol, joinProtocols(ProviderProtocols))
			}
			if !modes.Stream && !modes.Synchronous() {
				return fmt.Errorf("%s.protocols.%s supports neither whole responses nor streams", at, protocol)
			}
		}
		if len(provider.Models) > maxProviderModels {
			return fmt.Errorf("%s.models has more than %d entries", at, maxProviderModels)
		}
		for _, model := range provider.Models {
			if strings.TrimSpace(model) == "" || len(model) > 200 || strings.ContainsAny(model, "\r\n\t") {
				return fmt.Errorf("%s.models: %q is not a model name", at, model)
			}
		}
	}
	return nil
}

func joinProtocols(protocols []contract.ProtocolID) string {
	names := make([]string, len(protocols))
	for index, protocol := range protocols {
		names[index] = string(protocol)
	}
	return strings.Join(names, ", ")
}

// checkProviderHooks checks the hooks a provider adapter exports against
// what its providers declare, both ways: a provider declaring a stream
// needs parseStreamEvent, and a plugin exporting a hook its providers
// never call is refused.
func checkProviderHooks(meta Meta, exported map[string]bool) error {
	if len(meta.Providers) == 0 {
		for _, hook := range providerHooks {
			if exported[hook] {
				return fmt.Errorf("exports %q, a provider adapter's hook, without meta.providers", hook)
			}
		}
		return nil
	}
	synchronous, streams := false, false
	for _, provider := range meta.Providers {
		for _, protocol := range provider.Protocols {
			synchronous = synchronous || protocol.Synchronous()
			streams = streams || protocol.Stream
		}
	}
	switch {
	case !exported[HookBuildRequest]:
		return fmt.Errorf("declares providers but does not export %s", HookBuildRequest)
	case synchronous && !exported[HookParseResponse]:
		return fmt.Errorf("declares whole responses but does not export %s", HookParseResponse)
	case streams && !exported[HookParseStreamEvent]:
		return fmt.Errorf("declares streams but does not export %s", HookParseStreamEvent)
	case !streams && (exported[HookParseStreamEvent] || exported[HookFinishStream]):
		return fmt.Errorf("exports %s or %s but declares no stream", HookParseStreamEvent, HookFinishStream)
	}
	return nil
}

// providerLimits are the limits of a plugin that adapts providers: a call
// takes the whole request or response, as large as the gateway lets one
// be, and parsing it takes longer than a hook that reads a summary.
func providerLimits(limits Limits) Limits {
	limits.Memory = max(limits.Memory, 256<<20)
	limits.ResultBytes = max(limits.ResultBytes, 64<<20)
	limits.ResultNodes = max(limits.ResultNodes, 4<<20)
	limits.ScriptTime = max(limits.ScriptTime, time.Second)
	return limits
}
