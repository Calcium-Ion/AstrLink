package endpoint

import (
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestOpenCodeSelectsNativeProtocolByModel(t *testing.T) {
	for _, test := range []struct {
		kind     contract.ServiceKind
		model    string
		protocol contract.ProtocolID
	}{
		{contract.ServiceKindOpenCodeGo, "gpt-5.6-luna", contract.ProtocolOpenAIResponses},
		{contract.ServiceKindOpenCodeGo, "kimi-k3", contract.ProtocolOpenAIChat},
		{contract.ServiceKindOpenCodeGo, "minimax-m3", contract.ProtocolAnthropicMessages},
		{contract.ServiceKindOpenCodeZen, "minimax-m3", contract.ProtocolOpenAIChat},
		{contract.ServiceKindOpenCodeZen, "claude-sonnet-4-5", contract.ProtocolAnthropicMessages},
		{contract.ServiceKindOpenCodeGo, "qwen3.8-max", contract.ProtocolAnthropicMessages},
	} {
		t.Run(string(test.kind)+"/"+test.model, func(t *testing.T) {
			service := contract.Service{ID: "service_opencode", Name: "OpenCode", Kind: test.kind, Enabled: true, Models: []string{test.model},
				HTTP: &contract.HTTPConnection{BaseURL: "https://opencode.ai/zen/go/v1", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}},
				Capabilities: []contract.Capability{
					{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true},
					{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true},
					{Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeNative, Streaming: true},
				}}
			request := ResolveRequest{Protocol: contract.ProtocolAnthropicMessages, Model: test.model, Streaming: true}
			runtime := contract.RuntimeProfile{RelayKitAvailable: true, Edges: []contract.ConversionEdge{{From: request.Protocol, To: test.protocol, Streaming: true}}}
			candidates := defaultCandidates([]contract.Service{service}, request, "", runtime)
			if len(candidates) != 1 || candidates[0].UpstreamProtocol != test.protocol {
				t.Fatalf("wrong model protocol: %#v", candidates)
			}
			if test.protocol != request.Protocol {
				if candidates[0].PlanType != contract.PlanTypeRelayKit {
					t.Fatal("missing conversion plan")
				}
				if candidates := defaultCandidates([]contract.Service{service}, request, ""); len(candidates) != 0 {
					t.Fatal("routed incompatible protocol without conversion engine")
				}
			}
			route := resolverRoute("route_opencode", 0, test.model, resolverTarget("service_opencode", contract.PlanTypeNative, 0))
			candidates = routeCandidates(route, []contract.Service{service}, request, runtime, "")
			if len(candidates) != 1 || candidates[0].UpstreamProtocol != test.protocol {
				t.Fatalf("explicit route ignored model protocol: %#v", candidates)
			}
		})
	}
}

func TestOpenCodeConversionRequiresEnabledNativeCapability(t *testing.T) {
	service := contract.Service{
		ID: "service_opencode", Kind: contract.ServiceKindOpenCodeGo, Enabled: true,
		Models:       []string{"kimi-k3"},
		Capabilities: []contract.Capability{{Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeNative, Streaming: true}},
	}
	request := ResolveRequest{Protocol: contract.ProtocolAnthropicMessages, Model: "kimi-k3", Streaming: true}
	runtime := contract.RuntimeProfile{RelayKitAvailable: true, Edges: []contract.ConversionEdge{{From: request.Protocol, To: contract.ProtocolOpenAIChat, Streaming: true}}}
	if candidates := defaultCandidates([]contract.Service{service}, request, "", runtime); len(candidates) != 0 {
		t.Fatal("selected a disabled native protocol")
	}
	service.Capabilities = append(service.Capabilities, contract.Capability{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative})
	if candidates := defaultCandidates([]contract.Service{service}, request, "", runtime); len(candidates) != 0 {
		t.Fatal("selected a native protocol without streaming support")
	}
}
