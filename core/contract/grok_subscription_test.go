package contract_test

import (
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestGrokSubscriptionProviderMapsToKindAndDeviceCodeOnly(t *testing.T) {
	t.Parallel()
	provider := contract.SubscriptionProviderXAIGrok
	if !provider.Valid() || provider.ServiceKind() != contract.ServiceKindGrokSubscription ||
		contract.ServiceKindGrokSubscription.SubscriptionProvider() != provider ||
		!contract.ServiceKindGrokSubscription.IsSubscription() || contract.ServiceKindGrokSubscription.IsHTTP() {
		t.Fatal("grok provider and kind are not linked")
	}
	if !contract.AuthorizationFlowDeviceCode.SupportedBy(provider) ||
		contract.AuthorizationFlowBrowser.SupportedBy(provider) ||
		contract.AuthorizationFlowCode.SupportedBy(provider) {
		t.Fatal("grok must support only device_code")
	}
	protocols := map[contract.ProtocolID]bool{}
	for _, capability := range provider.Capabilities() {
		protocols[capability.Protocol] = true
	}
	if !protocols[contract.ProtocolOpenAIResponses] || !protocols[contract.ProtocolOpenAIChat] || !protocols[contract.ProtocolOpenAIModels] {
		t.Fatalf("capabilities = %#v", provider.Capabilities())
	}
	now := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	service := contract.Service{
		ID: "service_grok_01", Name: "Grok", Kind: contract.ServiceKindGrokSubscription, Enabled: true,
		Models: []string{}, Capabilities: provider.Capabilities(),
		Subscription: &contract.SubscriptionConnection{Provider: provider, Status: contract.SubscriptionStatusDisconnected},
		CreatedAt:    now, UpdatedAt: now,
	}
	if err := service.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	service.Subscription.Provider = contract.SubscriptionProviderOpenAICodex
	if err := service.Validate(); err == nil {
		t.Fatal("Validate() accepted a mismatched provider")
	}
}
