package contract_test

import (
	"slices"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestDefaultOpenAICodexCapabilitiesIncludeNativeAlphaSearch(t *testing.T) {
	t.Parallel()
	want := []contract.Capability{
		{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true},
		{Protocol: contract.ProtocolOpenAIResponsesCompact, Mode: contract.CapabilityModeNative},
		{Protocol: contract.ProtocolOpenAIAlphaSearch, Mode: contract.CapabilityModeNative},
		{Protocol: contract.ProtocolOpenAIModels, Mode: contract.CapabilityModeNative},
	}
	got := contract.DefaultOpenAICodexCapabilities()
	if !slices.Equal(got, want) {
		t.Fatalf("capabilities = %#v, want %#v", got, want)
	}
	provider := contract.SubscriptionProviderOpenAICodex
	if err := provider.ValidateCapabilities(got); err != nil {
		t.Fatal(err)
	}
	for _, capability := range got {
		if err := capability.Validate(); err != nil {
			t.Fatalf("capability %s: %v", capability.Protocol, err)
		}
	}
	if slices.Contains(provider.ConversionTargets(), contract.ProtocolOpenAIAlphaSearch) {
		t.Fatal("alpha_search must not be a conversion target")
	}
}

func TestCodexAlphaSearchNativeCapabilityIsRequiredAndNonStreaming(t *testing.T) {
	t.Parallel()
	for _, mutation := range []string{"missing", "streaming", "duplicate"} {
		t.Run(mutation, func(t *testing.T) {
			capabilities := contract.DefaultOpenAICodexCapabilities()
			index := slices.IndexFunc(capabilities, func(capability contract.Capability) bool {
				return capability.Protocol == contract.ProtocolOpenAIAlphaSearch
			})
			if index < 0 {
				t.Fatal("alpha_search is missing")
			}
			switch mutation {
			case "missing":
				capabilities = slices.Delete(capabilities, index, index+1)
			case "streaming":
				capabilities[index].Streaming = true
			case "duplicate":
				capabilities = append(capabilities, capabilities[index])
			}
			if err := contract.SubscriptionProviderOpenAICodex.ValidateCapabilities(capabilities); err == nil {
				t.Fatalf("accepted %s alpha_search capability", mutation)
			}
		})
	}
}
