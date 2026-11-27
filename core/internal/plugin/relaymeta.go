package plugin

import (
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/new-api/pluginkit"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// A New API plugin's provider part relays ordinary API requests: each
// meta.relay route says which client path it serves, for which models,
// where the request goes upstream and in what protocol. A plugin without
// hooks is routes and static keys, and no JavaScript runs on its requests;
// prepareRequest and signRequest adjust a request before it is sent.
// pluginkit reads and checks the routes; the gateway maps their paths and
// relaykit converters to its own protocols.

// RelayRoute is one route of meta.relay: a New API Advanced Custom route.
type RelayRoute = dto.AdvancedCustomRoute

// RelayModelsPath is the management route of a provider's model list.
const RelayModelsPath = dto.AdvancedCustomModelListPath

// relayConverters are the protocols of relaykit's converters, as New API's
// routes name them: the upstream protocol each sends.
var relayConverters = map[string]contract.ProtocolID{
	"openai_chat_completions_to_anthropic_messages":      contract.ProtocolAnthropicMessages,
	"openai_chat_completions_to_openai_responses":        contract.ProtocolOpenAIResponses,
	"openai_chat_completions_to_gemini_generate_content": contract.ProtocolGoogleGenerateContent,
	"openai_responses_to_openai_chat_completions":        contract.ProtocolOpenAIChat,
	"openai_responses_to_gemini_generate_content":        contract.ProtocolGoogleGenerateContent,
	"anthropic_messages_to_openai_chat_completions":      contract.ProtocolOpenAIChat,
	"claude_messages_to_openai_responses":                contract.ProtocolOpenAIResponses,
	"gemini_generate_content_to_openai_chat_completions": contract.ProtocolOpenAIChat,
	"gemini_generate_content_to_openai_responses":        contract.ProtocolOpenAIResponses,
	dto.AdvancedCustomConverterSGLangRerank:              contract.ProtocolRerank,
}

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
	case "/v1/alpha/search":
		return contract.ProtocolOpenAISearch
	case RelayModelsPath:
		return contract.ProtocolOpenAIModels
	}
	if call, ok := contract.ParseGeminiCall(path); ok {
		return call.Protocol
	}
	return ""
}

// RelayHooks reports whether the route's plugin hooks convert it, alone or
// after a relaykit converter.
func RelayHooks(route RelayRoute) bool {
	_, hooks := pluginkit.SplitProviderConverter(route.Converter)
	return hooks
}

// RelayConverter is the relaykit converter the route names, "" for none.
func RelayConverter(route RelayRoute) string {
	converter, _ := pluginkit.SplitProviderConverter(route.Converter)
	if converter == "" || converter == "none" {
		return ""
	}
	return converter
}

// RelayUpstreamProtocol is the protocol the route's request goes upstream
// in, before the plugin's hooks convert it: the client's without a relaykit
// converter.
func RelayUpstreamProtocol(route RelayRoute) contract.ProtocolID {
	if protocol, ok := relayConverters[RelayConverter(route)]; ok {
		return protocol
	}
	return RelayProtocol(strings.TrimSpace(route.IncomingPath))
}

// RelayRouteFor is the route of meta.relay that serves model on path: a
// route that lists the model before the route of the path for every other
// model, as New API matches them. The gateway answers a Gemini call under
// /v1 and /v1beta alike, so a call under the version no route names takes
// the other version's route.
func (loaded *NewAPIPlugin) RelayRouteFor(path, model string) *RelayRoute {
	if route := loaded.relayRouteOn(path, model); route != nil {
		return route
	}
	call, ok := contract.ParseGeminiCall(path)
	if !ok {
		return nil
	}
	other := "/v1"
	if call.Version == "/v1" {
		other = "/v1beta"
	}
	return loaded.relayRouteOn(other+strings.TrimPrefix(path, call.Version), model)
}

func (loaded *NewAPIPlugin) relayRouteOn(path, model string) *RelayRoute {
	var fallback *RelayRoute
	for index := range loaded.Meta.Relay {
		route := &loaded.Meta.Relay[index]
		config := dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{*route}}
		if !config.SupportsPath(path) {
			continue
		}
		if len(route.Models) == 0 {
			if fallback == nil {
				fallback = route
			}
			continue
		}
		if route.AcceptsModel(model) {
			return route
		}
	}
	return fallback
}
