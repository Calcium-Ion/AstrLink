package contract_test

import (
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func officialClaudeAccount() contract.SubscriptionAccount {
	account := validSubscriptionAccount()
	account.Provider = contract.SubscriptionProviderClaudeCode
	account.Capabilities = contract.SubscriptionProviderClaudeCode.Capabilities()
	account.Mode = contract.SubscriptionModeOfficial
	account.CredentialRef = ""
	account.TokenExpiresAt = nil
	return account
}

func TestOfficialClaudeAccountsKeepNoCredential(t *testing.T) {
	t.Parallel()
	account := officialClaudeAccount()
	if err := account.Validate(); err != nil {
		t.Fatalf("connected official account: %v", err)
	}
	if !account.Official() {
		t.Fatal("Official() = false")
	}
	account.CredentialRef = "local://subscription/" + string(account.ID)
	if err := account.Validate(); err == nil {
		t.Fatal("official account accepted a credential_ref")
	}

	legacy := officialClaudeAccount()
	legacy.Mode = contract.SubscriptionModeLegacy
	if err := legacy.Validate(); err == nil {
		t.Fatal("connected legacy account without credential_ref validated")
	}
}

func TestSubscriptionModeIsClaudeOnly(t *testing.T) {
	t.Parallel()
	account := validSubscriptionAccount()
	account.Mode = contract.SubscriptionModeOfficial
	if err := account.Validate(); err == nil {
		t.Fatal("official Codex account validated")
	}
	account.Mode = contract.SubscriptionModeLegacy
	if err := account.Validate(); err != nil {
		t.Fatalf("legacy Codex account: %v", err)
	}
	account.Mode = "cli"
	if err := account.Validate(); err == nil {
		t.Fatal("unknown mode validated")
	}
}

func TestAuthorizationFlowSupportedByMode(t *testing.T) {
	t.Parallel()
	claude := contract.SubscriptionProviderClaudeCode
	for _, test := range []struct {
		flow contract.AuthorizationFlow
		mode contract.SubscriptionMode
		want bool
	}{
		{contract.AuthorizationFlowClaudeCLI, contract.SubscriptionModeOfficial, true},
		{contract.AuthorizationFlowCode, contract.SubscriptionModeOfficial, false},
		{contract.AuthorizationFlowCode, "", true},
		{contract.AuthorizationFlowCode, contract.SubscriptionModeLegacy, true},
		{contract.AuthorizationFlowClaudeCLI, "", false},
		{contract.AuthorizationFlowBrowser, contract.SubscriptionModeOfficial, false},
	} {
		if got := test.flow.SupportedByMode(claude, test.mode); got != test.want {
			t.Errorf("%s in mode %q = %v, want %v", test.flow, test.mode, got, test.want)
		}
	}
	if contract.AuthorizationFlowClaudeCLI.SupportedByMode(contract.SubscriptionProviderOpenAICodex, "") {
		t.Error("Codex accepted claude_cli")
	}
	if !contract.AuthorizationFlowDeviceCode.SupportedByMode(contract.SubscriptionProviderOpenAICodex, "") {
		t.Error("Codex refused device_code")
	}
}
