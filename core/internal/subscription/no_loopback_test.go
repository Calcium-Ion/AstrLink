package subscription_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func TestNoLoopbackCallbackReachesEveryProvider(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	accounts := subscription.NewMemoryAccountStore()
	account := contract.SubscriptionAccount{
		ID: "service_antigravity_server", Provider: contract.SubscriptionProviderAntigravity,
		Status: contract.SubscriptionStatusDisconnected, DisplayName: "Antigravity",
		Capabilities: contract.SubscriptionProviderAntigravity.Capabilities(), CreatedAt: now, UpdatedAt: now,
	}
	if err := accounts.PutAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	// The server edition sets it on the base config only; the Grok override
	// mirrors main's wiring.
	manager, err := subscription.NewManager(accounts, accountauth.NewMemoryCredentialStore(),
		accountauth.OAuthConfig{NoLoopbackCallback: true, Now: func() time.Time { return now }},
		accountauth.OAuthConfig{Provider: contract.SubscriptionProviderXAIGrok},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.BeginAuthorization(ctx, account.ID, contract.AuthorizationFlowBrowser); !errors.Is(err, accountauth.ErrLoopbackCallbackUnavailable) {
		t.Fatalf("BeginAuthorization() = %v, want ErrLoopbackCallbackUnavailable", err)
	}
	account, err = manager.Get(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if account.Status != contract.SubscriptionStatusDisconnected {
		t.Fatalf("status = %s, want the refused sign-in rolled back", account.Status)
	}
}
