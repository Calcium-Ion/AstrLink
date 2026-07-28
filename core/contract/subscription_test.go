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
