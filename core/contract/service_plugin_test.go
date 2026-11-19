package contract_test

import (
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestTaskPluginProviderKeepsResponsesOnHTTP(t *testing.T) {
	t.Parallel()
	enabled := true
	service := contract.Service{
		Kind:   contract.ServiceKindPlugin,
		Plugin: &contract.ServicePlugin{Key: "sora", Provider: contract.PluginProviderTask},
		Capabilities: []contract.Capability{{
			Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true,
		}},
		ResponsesWebSocketEnabled: &enabled,
	}
	if service.ResponsesWebSocket() {
		t.Fatal("a task plugin's provider answers Responses through its tasks over HTTP")
	}
	service.Plugin.Provider = "chat"
	if !service.ResponsesWebSocket() {
		t.Fatal("another plugin provider keeps its setting")
	}
}
