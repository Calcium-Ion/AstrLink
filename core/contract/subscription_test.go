package contract_test

import (
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func validSubscriptionAccount() contract.SubscriptionAccount {
	now := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
	expires := now.Add(time.Hour)
	return contract.SubscriptionAccount{
		ID:             "subscription_01",
		Provider:       contract.SubscriptionProviderOpenAICodex,
		Status:         contract.SubscriptionStatusConnected,
		DisplayName:    "Codex subscription",
		AccountHint:    "a***@example.com",
		CredentialRef:  "keyring://astrlink/subscription/subscription_01",
		Capabilities:   contract.DefaultOpenAICodexCapabilities(),
		TokenExpiresAt: &expires,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func TestSubscriptionAccountValidateAcceptsConnectedCodex(t *testing.T) {
	t.Parallel()
	account := validSubscriptionAccount()
	if err := account.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}
}

func TestSubscriptionAccountValidateRejectsEndpointCredentialRef(t *testing.T) {
	t.Parallel()
	account := validSubscriptionAccount()
	account.CredentialRef = "local://endpoint/endpoint_01"
	if err := account.Validate(); err == nil {
		t.Fatal("Validate() expected error for local:// credential_ref")
	}
}

func TestSubscriptionAccountValidateRejectsTokenLeakInError(t *testing.T) {
	t.Parallel()
	messages := []string{
		"Bearer sk-test-access-token-value-123456",
		`device_auth_id="device-auth-secret"`,
		"code_verifier=pkce-verifier-secret",
		"authorization_code=authorization-secret",
	}
	for _, message := range messages {
		account := validSubscriptionAccount()
		account.Status = contract.SubscriptionStatusError
		account.CredentialRef = ""
		account.LastError = &contract.SubscriptionError{
			Code:    "refresh_failed",
			Message: message,
		}
		if err := account.Validate(); err == nil {
			t.Fatalf("Validate() accepted credential leak %q", message)
		}
	}
}

func TestSubscriptionAccountValidateRejectsUnknownProvider(t *testing.T) {
	t.Parallel()
	account := validSubscriptionAccount()
	account.Provider = "claude_subscription"
	if err := account.Validate(); err == nil {
		t.Fatal("Validate() expected error for unknown provider")
	}
}

func TestAuthorizationSessionValidateRequiresHTTPSURL(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
	session := contract.AuthorizationSession{
		ID:               "authorization_01",
		Provider:         contract.SubscriptionProviderOpenAICodex,
		Status:           contract.AuthorizationSessionStatusPending,
		Flow:             contract.AuthorizationFlowBrowser,
		AuthorizationURL: "http://auth.example/oauth/authorize",
		ServiceID:        "service_test",
		ExpiresAt:        now.Add(10 * time.Minute),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := session.Validate(); err == nil {
		t.Fatal("Validate() expected error for non-https remote authorization_url")
	}
	session.AuthorizationURL = "https://auth.example/oauth/authorize?" + strings.Repeat("a", 8)
	if err := session.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}
	session.AuthorizationURL = "http://127.0.0.1:9/oauth/authorize"
	if err := session.Validate(); err != nil {
		t.Fatalf("Validate() unexpected loopback http error: %v", err)
	}
}

func TestAuthorizationSessionValidateEnforcesFlowPayload(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
	session := contract.AuthorizationSession{
		ID:       "authorization_02",
		Provider: contract.SubscriptionProviderOpenAICodex,
		Status:   contract.AuthorizationSessionStatusPending,
		Flow:     contract.AuthorizationFlowDeviceCode,
		DeviceCode: &contract.AuthorizationDeviceCode{
			VerificationURL: "https://auth.openai.com/codex/device",
			UserCode:        "ABCD-EFGH",
		},
		ServiceID: "service_test",
		ExpiresAt: now.Add(15 * time.Minute),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := session.Validate(); err != nil {
		t.Fatalf("Validate() unexpected device-code error: %v", err)
	}
	session.AuthorizationURL = "https://auth.openai.com/oauth/authorize"
	if err := session.Validate(); err == nil {
		t.Fatal("Validate() accepted mixed browser and device-code instructions")
	}
	session.AuthorizationURL = ""
	session.Status = contract.AuthorizationSessionStatusCompleted
	if err := session.Validate(); err == nil {
		t.Fatal("Validate() accepted user_code on a terminal session")
	}
	session.DeviceCode = nil
	if err := session.Validate(); err != nil {
		t.Fatalf("Validate() rejected terminal session without instructions: %v", err)
	}
}

func TestSubscriptionUsageValidateAcceptsSanitizedSnapshot(t *testing.T) {
	t.Parallel()
	windowSeconds := int64(18000)
	resetAfter := int64(120)
	resetAt := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	allowed := true
	usage := contract.SubscriptionUsage{
		ServiceID:             "service_codex_01",
		FetchedAt:             time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC),
		PlanType:              "plus",
		Allowed:               &allowed,
		Primary:               &contract.RateLimitWindow{UsedPercent: 34, LimitWindowSeconds: &windowSeconds, ResetAt: &resetAt, ResetAfterSeconds: &resetAfter},
		Credits:               &contract.UsageCredits{HasCredits: false, Unlimited: false, Balance: "0"},
		RateLimitResetCredits: &contract.RateLimitResetCredits{AvailableCount: 2},
		AdditionalRateLimits: []contract.AdditionalRateLimit{{
			LimitName:      "GPT-5.3-Codex-Spark",
			MeteredFeature: "codex_bengalfox",
			Primary:        &contract.RateLimitWindow{UsedPercent: 0, LimitWindowSeconds: &windowSeconds},
		}},
	}
	if err := usage.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}
}

func TestSubscriptionUsageValidateRejectsPIIAndRangeErrors(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	valid := contract.SubscriptionUsage{ServiceID: "service_codex_01", FetchedAt: now, PlanType: "plus"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid usage: %v", err)
	}

	usage := valid
	usage.PlanType = "user@example.com"
	if err := usage.Validate(); err == nil {
		t.Fatal("accepted email plan_type")
	}

	usage = valid
	usage.Primary = &contract.RateLimitWindow{UsedPercent: -1}
	if err := usage.Validate(); err == nil {
		t.Fatal("accepted negative used_percent")
	}

	usage = valid
	usage.AdditionalRateLimits = []contract.AdditionalRateLimit{{
		LimitName: "Bearer sk-test-access-token-value-123456",
	}}
	if err := usage.Validate(); err == nil {
		t.Fatal("accepted leaked limit_name")
	}
}

func TestSubscriptionUsageResetValidateAcceptsOfficialOutcomes(t *testing.T) {
	t.Parallel()
	windows := int64(2)
	result := contract.SubscriptionUsageReset{
		ServiceID:    "service_codex_01",
		Outcome:      contract.UsageResetOutcomeReset,
		WindowsReset: &windows,
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}
	result.Outcome = "full_reset"
	if err := result.Validate(); err == nil {
		t.Fatal("accepted unknown outcome")
	}
}

func TestSubscriptionValidateCapabilitiesAcceptsProviderEgressConversions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		provider contract.SubscriptionProvider
		ingress  contract.ProtocolID
		target   contract.ProtocolID
	}{
		{contract.SubscriptionProviderOpenAICodex, contract.ProtocolOpenAIChat, contract.ProtocolOpenAIResponses},
		{contract.SubscriptionProviderOpenAICodex, contract.ProtocolAnthropicMessages, contract.ProtocolOpenAIResponses},
		{contract.SubscriptionProviderClaudeCode, contract.ProtocolOpenAIChat, contract.ProtocolAnthropicMessages},
		{contract.SubscriptionProviderXAIGrok, contract.ProtocolAnthropicMessages, contract.ProtocolOpenAIChat},
	} {
		native := test.provider.Capabilities()
		reversed := make([]contract.Capability, 0, len(native)+1)
		for index := len(native) - 1; index >= 0; index-- {
			reversed = append(reversed, native[index])
		}
		capabilities := append(reversed, contract.Capability{
			Protocol: test.ingress, Mode: contract.CapabilityModeNative, Streaming: true, ConvertTo: test.target,
		})
		if err := test.provider.ValidateCapabilities(capabilities); err != nil {
			t.Fatalf("%s %s->%s: %v", test.provider, test.ingress, test.target, err)
		}
	}
}

func TestSubscriptionValidateCapabilitiesRestrictsEgress(t *testing.T) {
	t.Parallel()
	codex := contract.SubscriptionProviderOpenAICodex
	withNative := func(provider contract.SubscriptionProvider, extra ...contract.Capability) []contract.Capability {
		return append(provider.Capabilities(), extra...)
	}
	for name, test := range map[string]struct {
		provider     contract.SubscriptionProvider
		capabilities []contract.Capability
		want         string
	}{
		"codex converts only to responses": {
			provider: codex,
			capabilities: withNative(codex, contract.Capability{
				Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true,
				ConvertTo: contract.ProtocolAnthropicMessages,
			}),
			want: "openai_codex subscriptions can only convert to openai.responses",
		},
		"claude converts only to messages": {
			provider: contract.SubscriptionProviderClaudeCode,
			capabilities: withNative(contract.SubscriptionProviderClaudeCode, contract.Capability{
				Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true,
				ConvertTo: contract.ProtocolOpenAIResponses,
			}),
			want: "can only convert to anthropic.messages",
		},
		"conversion must be native": {
			provider: codex,
			capabilities: withNative(codex, contract.Capability{
				Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeDelegated, Streaming: true,
				ConvertTo: contract.ProtocolOpenAIResponses,
			}),
			want: "must use native mode",
		},
		"native capability cannot be dropped": {
			provider:     codex,
			capabilities: codex.Capabilities()[:1],
			want:         "is required",
		},
		"native capability cannot be altered": {
			provider: codex,
			capabilities: []contract.Capability{
				{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative},
				codex.Capabilities()[1], codex.Capabilities()[2],
			},
			want: "fixed by provider",
		},
		"extra native protocol is rejected": {
			provider: codex,
			capabilities: withNative(codex, contract.Capability{
				Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true,
			}),
			want: "fixed by provider",
		},
	} {
		err := test.provider.ValidateCapabilities(test.capabilities)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%s: error = %v, want %q", name, err, test.want)
		}
	}
}

func TestSubscriptionServiceRejectsConversionOfNativeProtocol(t *testing.T) {
	t.Parallel()
	service := contract.Service{
		ID: "service_grok", Name: "Grok", Kind: contract.ServiceKindGrokSubscription, Enabled: true,
		Capabilities: append(contract.DefaultXAIGrokCapabilities(), contract.Capability{
			Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true,
			ConvertTo: contract.ProtocolOpenAIResponses,
		}),
		Subscription: &contract.SubscriptionConnection{
			Provider: contract.SubscriptionProviderXAIGrok, Status: contract.SubscriptionStatusConnected,
			CredentialRef: "keyring://subscription/service_grok",
		},
	}
	if err := service.Validate(); err == nil {
		t.Fatal("Validate() accepted a conversion that duplicates a native protocol")
	}
	service.Capabilities[len(service.Capabilities)-1].Protocol = contract.ProtocolAnthropicMessages
	if err := service.Validate(); err != nil {
		t.Fatalf("Validate() rejected Grok anthropic conversion: %v", err)
	}
}
