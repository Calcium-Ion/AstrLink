package contract_test

import (
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestDroidSubscriptionProviderMapsToKindAndDeviceCodeOnly(t *testing.T) {
	t.Parallel()
	provider := contract.SubscriptionProviderFactoryDroid
	kind := contract.ServiceKindDroidSubscription
	if !provider.Valid() || provider.ServiceKind() != kind || kind.SubscriptionProvider() != provider ||
		!kind.IsSubscription() || kind.IsHTTP() || !kind.Valid() {
		t.Fatal("droid provider and kind are not linked")
	}
	if !contract.AuthorizationFlowDeviceCode.SupportedBy(provider) ||
		contract.AuthorizationFlowBrowser.SupportedBy(provider) ||
		contract.AuthorizationFlowCode.SupportedBy(provider) {
		t.Fatal("droid must support only device_code")
	}
	protocols := map[contract.ProtocolID]bool{}
	for _, capability := range provider.Capabilities() {
		protocols[capability.Protocol] = capability.Mode == contract.CapabilityModeNative
	}
	for _, protocol := range []contract.ProtocolID{
		contract.ProtocolAnthropicMessages, contract.ProtocolOpenAIResponses,
		contract.ProtocolOpenAIChat, contract.ProtocolOpenAIModels,
	} {
		if !protocols[protocol] {
			t.Fatalf("capabilities = %#v", provider.Capabilities())
		}
	}
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	enabled := true
	service := contract.Service{
		ID: "service_droid_01", Name: "Droid", Kind: kind, Enabled: true,
		Models: []string{}, Capabilities: provider.Capabilities(), ResponsesWebSocketEnabled: &enabled,
		Subscription: &contract.SubscriptionConnection{Provider: provider, Status: contract.SubscriptionStatusDisconnected},
		CreatedAt:    now, UpdatedAt: now,
	}
	if err := service.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	if service.ResponsesWebSocket() {
		t.Fatal("Droid must keep Responses on HTTP")
	}
	service.Capabilities = append(service.Capabilities, contract.Capability{
		Protocol: contract.ProtocolGoogleGenerateContent, Mode: contract.CapabilityModeNative,
		ConvertTo: contract.ProtocolAnthropicMessages, Streaming: true,
	})
	if err := service.Validate(); err != nil {
		t.Fatalf("Validate() rejected a conversion into Messages: %v", err)
	}
	service.Subscription.Provider = contract.SubscriptionProviderGitHubCopilot
	if err := service.Validate(); err == nil {
		t.Fatal("Validate() accepted a mismatched provider")
	}
}

func TestDroidModelsUseTheWireDroidSendsThemOn(t *testing.T) {
	t.Parallel()
	for model, want := range map[string]contract.ProtocolID{
		"claude-opus-4-6":           contract.ProtocolAnthropicMessages,
		"claude-haiku-4-5-20251001": contract.ProtocolAnthropicMessages,
		"Claude-Fable-5.1":          contract.ProtocolAnthropicMessages,
		"minimax-m2.7":              contract.ProtocolAnthropicMessages,
		"gpt-5.4":                   contract.ProtocolOpenAIResponses,
		"gpt-5.3-codex":             contract.ProtocolOpenAIResponses,
		"grok-4.7":                  contract.ProtocolOpenAIResponses,
		"glm-5.2":                   contract.ProtocolOpenAIChat,
		"kimi-k3":                   contract.ProtocolOpenAIChat,
		"minimax-m3":                contract.ProtocolOpenAIChat,
		"nemotron-3-ultra":          contract.ProtocolOpenAIChat,
	} {
		if got := contract.ServiceKindDroidSubscription.ModelNativeProtocol(model); got != want {
			t.Errorf("ModelNativeProtocol(%q) = %q, want %q", model, got, want)
		}
	}
}
