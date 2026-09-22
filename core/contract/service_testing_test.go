package contract

import (
	"strings"
	"testing"
)

func TestServiceTestCapabilityValidation(t *testing.T) {
	service := Service{Kind: ServiceKindOpenAI, Capabilities: []Capability{{Protocol: ProtocolOpenAIChat, Mode: CapabilityModeNative, Streaming: true}}}
	valid := ServiceTestRequest{Protocol: ProtocolOpenAIChat, Model: "custom-model", Stream: true}
	if err := valid.Validate(service); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		input ServiceTestRequest
	}{
		{"discovery", ServiceTestRequest{Protocol: ProtocolOpenAIModels, Model: "model"}},
		{"empty model", ServiceTestRequest{Protocol: ProtocolOpenAIChat}},
		{"model control", ServiceTestRequest{Protocol: ProtocolOpenAIChat, Model: "model\n"}},
		{"model too long", ServiceTestRequest{Protocol: ProtocolOpenAIChat, Model: strings.Repeat("a", 257)}},
		{"prompt too long", ServiceTestRequest{Protocol: ProtocolOpenAIChat, Model: "model", Prompt: strings.Repeat("文", 2001)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.input.Validate(service) == nil {
				t.Fatal("accepted invalid test")
			}
		})
	}
	service.Capabilities[0].Streaming = false
	if valid.Validate(service) == nil {
		t.Fatal("accepted unsupported streaming")
	}
	service.Capabilities[0].Streaming = true
	service.Capabilities[0].ConvertTo = ProtocolAnthropicMessages
	if valid.Validate(service) == nil {
		t.Fatal("accepted a local conversion capability")
	}
	service.Kind = ServiceKindCodexSubscription
	service.Capabilities = DefaultOpenAICodexCapabilities()
	if (ServiceTestRequest{Protocol: ProtocolOpenAIResponses, Model: "model"}).Validate(service) == nil {
		t.Fatal("accepted non-streaming Codex test")
	}
}
