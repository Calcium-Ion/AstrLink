package convo

import (
	"errors"
	"sort"
)

// Protocol identifies the wire format of a request/response pair. The values
// are stable strings so hosts can persist them; they intentionally do not
// reuse any vendor SDK identifiers.
type Protocol string

const (
	// OpenAIChat is POST /v1/chat/completions. History lives in messages[].
	OpenAIChat Protocol = "openai.chat"
	// OpenAIResponses is POST /v1/responses and /v1/responses/compact. History
	// lives in input[] or, with previous_response_id, on the server.
	OpenAIResponses Protocol = "openai.responses"
	// AnthropicMessages is POST /v1/messages. History lives in messages[].
	AnthropicMessages Protocol = "anthropic.messages"
	// GeminiGenerateContent is POST /v1beta/models/{model}:generateContent and
	// :streamGenerateContent. History lives in contents[].
	GeminiGenerateContent Protocol = "gemini.generate_content"
)

// ErrUnsupportedProtocol is returned when no adapter is registered for a
// protocol. Callers should treat the request as a singleton session.
var ErrUnsupportedProtocol = errors.New("convo: unsupported protocol")

// Registry maps protocols to adapters. It is a plain value type: build one,
// register adapters, and pass it through Policy.Registry. The registry
// returned by DefaultRegistry is a fresh copy every call, so mutating it never
// affects other callers.
type Registry struct {
	adapters map[Protocol]Adapter
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{adapters: make(map[Protocol]Adapter)}
}

// DefaultRegistry returns a new registry populated with the built-in adapters.
func DefaultRegistry() *Registry {
	registry := NewRegistry()
	for protocol, adapter := range builtinAdapters {
		registry.adapters[protocol] = adapter
	}
	return registry
}

// Register adds or replaces the adapter for protocol. A nil adapter removes
// the protocol.
func (registry *Registry) Register(protocol Protocol, adapter Adapter) {
	if registry.adapters == nil {
		registry.adapters = make(map[Protocol]Adapter)
	}
	if adapter == nil {
		delete(registry.adapters, protocol)
		return
	}
	registry.adapters[protocol] = adapter
}

// Lookup returns the adapter for protocol.
func (registry *Registry) Lookup(protocol Protocol) (Adapter, bool) {
	if registry == nil || registry.adapters == nil {
		return nil, false
	}
	adapter, ok := registry.adapters[protocol]
	return adapter, ok
}

// Protocols lists registered protocols in sorted order.
func (registry *Registry) Protocols() []Protocol {
	if registry == nil {
		return nil
	}
	protocols := make([]Protocol, 0, len(registry.adapters))
	for protocol := range registry.adapters {
		protocols = append(protocols, protocol)
	}
	sort.Slice(protocols, func(i, j int) bool { return protocols[i] < protocols[j] })
	return protocols
}

// builtinAdapters is read-only after package initialisation. DefaultRegistry
// copies it so no caller can mutate shared state.
var builtinAdapters = map[Protocol]Adapter{
	OpenAIChat:            openAIChatAdapter{},
	OpenAIResponses:       openAIResponsesAdapter{},
	AnthropicMessages:     anthropicAdapter{},
	GeminiGenerateContent: geminiAdapter{},
}

func (policy Policy) adapterFor(protocol Protocol) (Adapter, bool) {
	if policy.Registry != nil {
		return policy.Registry.Lookup(protocol)
	}
	adapter, ok := builtinAdapters[protocol]
	return adapter, ok
}
