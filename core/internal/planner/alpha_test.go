package planner

import (
	"errors"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func alphaEndpoint() contract.Endpoint {
	return contract.Endpoint{
		ID: "endpoint_01", Name: "new-api", Kind: contract.EndpointKindNewAPI,
		BaseURL: "https://gateway.example", Auth: contract.EndpointAuth{Scheme: contract.AuthSchemeBearer}, Enabled: true,
		Capabilities: []contract.Capability{
			{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true},
			{Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeDelegated, Streaming: false},
		},
	}
}

func TestBuildAlphaRejectsPostAlphaAndExtensionProtocols(t *testing.T) {
	for _, protocol := range []contract.ProtocolID{contract.ProtocolOpenAIRealtime, "vendor.custom_protocol"} {
		endpoint := alphaEndpoint()
		endpoint.Capabilities = []contract.Capability{{
			Protocol: protocol, Mode: contract.CapabilityModeNative, Streaming: true,
		}}
		_, err := BuildAlpha(AlphaInput{
			Endpoint: endpoint, Protocol: protocol, Mode: contract.CapabilityModeNative,
		})
		if err == nil {
			t.Fatalf("BuildAlpha accepted out-of-scope protocol %q", protocol)
		}
	}
}

func TestBuildAlphaNativeAndDelegated(t *testing.T) {
	tests := []struct {
		name     string
		protocol contract.ProtocolID
		mode     contract.CapabilityMode
		wantType contract.PlanType
	}{
		{name: "native", protocol: contract.ProtocolOpenAIResponses, mode: contract.CapabilityModeNative, wantType: contract.PlanTypeNative},
		{name: "delegated", protocol: contract.ProtocolAnthropicMessages, mode: contract.CapabilityModeDelegated, wantType: contract.PlanTypeDelegated},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := BuildAlpha(AlphaInput{Endpoint: alphaEndpoint(), Protocol: test.protocol, Mode: test.mode})
			if err != nil {
				t.Fatalf("BuildAlpha: %v", err)
			}
			if plan.Type != test.wantType || plan.InputProtocol != test.protocol || plan.UpstreamProtocol != test.protocol {
				t.Fatalf("unexpected plan: %#v", plan)
			}
			if plan.ConversionPath == nil || len(plan.ConversionPath) != 0 || plan.ConversionQuality != "" {
				t.Fatalf("Alpha plan contains conversion state: %#v", plan)
			}
		})
	}
}

func TestBuildAlphaReportsUnavailableCapability(t *testing.T) {
	chatOnly := alphaEndpoint()
	chatOnly.Capabilities = []contract.Capability{{
		Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true,
	}}
	tests := []struct {
		name      string
		input     AlphaInput
		wantError string
	}{
		{
			name: "streaming is not declared",
			input: AlphaInput{
				Endpoint: alphaEndpoint(), Protocol: contract.ProtocolAnthropicMessages,
				Mode: contract.CapabilityModeDelegated, Streaming: true,
			},
			wantError: `endpoint capability is unavailable: protocol="anthropic.messages" mode="delegated" streaming=true`,
		},
		{
			name: "requested mode is not declared",
			input: AlphaInput{
				Endpoint: alphaEndpoint(), Protocol: contract.ProtocolOpenAIResponses,
				Mode: contract.CapabilityModeDelegated,
			},
			wantError: `endpoint capability is unavailable: protocol="openai.responses" mode="delegated" streaming=false`,
		},
		{
			name: "Responses is never downgraded to Chat",
			input: AlphaInput{
				Endpoint: chatOnly, Protocol: contract.ProtocolOpenAIResponses,
				Mode: contract.CapabilityModeNative, Streaming: true,
			},
			wantError: `endpoint capability is unavailable: protocol="openai.responses" mode="native" streaming=true`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := BuildAlpha(test.input)
			if !errors.Is(err, ErrCapabilityUnavailable) {
				t.Fatalf("error = %v, want ErrCapabilityUnavailable", err)
			}
			var capabilityErr *CapabilityUnavailableError
			if !errors.As(err, &capabilityErr) {
				t.Fatalf("error type = %T, want *CapabilityUnavailableError", err)
			}
			if capabilityErr.Protocol != test.input.Protocol ||
				capabilityErr.Mode != test.input.Mode ||
				capabilityErr.Streaming != test.input.Streaming {
				t.Fatalf("capability error = %#v, want protocol=%q mode=%q streaming=%t",
					capabilityErr, test.input.Protocol, test.input.Mode, test.input.Streaming)
			}
			if err.Error() != test.wantError {
				t.Fatalf("error = %q, want %q", err, test.wantError)
			}
			if plan.Type != "" || plan.UpstreamProtocol != "" {
				t.Fatalf("unavailable capability produced a plan: %#v", plan)
			}
		})
	}
}

func TestBuildAlphaDoesNotGenerateStreamingPlanForNonStreamingProtocol(t *testing.T) {
	endpoint := alphaEndpoint()
	endpoint.Capabilities = []contract.Capability{{
		Protocol:  contract.ProtocolOpenAIResponsesCompact,
		Mode:      contract.CapabilityModeNative,
		Streaming: true,
	}}
	_, err := BuildAlpha(AlphaInput{
		Endpoint:  endpoint,
		Protocol:  contract.ProtocolOpenAIResponsesCompact,
		Mode:      contract.CapabilityModeNative,
		Streaming: true,
	})
	if err == nil {
		t.Fatal("BuildAlpha generated an illegal streaming plan")
	}
}

func TestBuildAlphaRejectsDisabledEndpoint(t *testing.T) {
	endpoint := alphaEndpoint()
	endpoint.Enabled = false
	_, err := BuildAlpha(AlphaInput{
		Endpoint: endpoint, Protocol: contract.ProtocolOpenAIResponses,
		Mode: contract.CapabilityModeNative,
	})
	if !errors.Is(err, ErrEndpointDisabled) {
		t.Fatalf("error = %v, want ErrEndpointDisabled", err)
	}
}
