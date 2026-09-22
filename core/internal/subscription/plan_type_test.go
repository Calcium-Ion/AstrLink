package subscription_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func TestUsageIncludesProviderPlanType(t *testing.T) {
	for _, test := range []struct {
		name     string
		provider contract.SubscriptionProvider
		metadata string
		status   int
		want     string
	}{
		{"claude max 20x", contract.SubscriptionProviderClaudeCode, `{"organization":{"organization_type":"claude_max","rate_limit_tier":"default_claude_max_20x"}}`, 200, "max_20x"},
		{"claude max 5x", contract.SubscriptionProviderClaudeCode, `{"organization":{"organization_type":"claude_max","rate_limit_tier":"default_claude_max_5x"}}`, 200, "max_5x"},
		{"claude pro", contract.SubscriptionProviderClaudeCode, `{"organization":{"organization_type":"claude_pro"}}`, 200, "pro"},
		{"claude team", contract.SubscriptionProviderClaudeCode, `{"organization":{"organization_type":"claude_team"}}`, 200, "team"},
		{"claude malformed tier", contract.SubscriptionProviderClaudeCode, `{"organization":{"organization_type":"claude_max","rate_limit_tier":{}}}`, 200, "max"},
		{"claude missing organization", contract.SubscriptionProviderClaudeCode, `{}`, 200, ""},
		{"claude unavailable", contract.SubscriptionProviderClaudeCode, `{}`, 403, ""},
		{"claude malformed", contract.SubscriptionProviderClaudeCode, `not json`, 200, ""},
		{"claude private", contract.SubscriptionProviderClaudeCode, `{"organization":{"organization_type":"owner@example.com"}}`, 200, ""},
		{"grok display", contract.SubscriptionProviderXAIGrok, `{"subscription_tier_display":"SuperGrok Heavy","subscription_tier":"supergrok"}`, 200, "SuperGrok Heavy"},
		{"grok fallback", contract.SubscriptionProviderXAIGrok, `{"subscription_tier":"supergrok"}`, 200, "supergrok"},
		{"grok malformed display", contract.SubscriptionProviderXAIGrok, `{"subscription_tier_display":{},"subscription_tier":"supergrok"}`, 200, "supergrok"},
		{"grok unavailable", contract.SubscriptionProviderXAIGrok, `{}`, 503, ""},
		{"grok malformed", contract.SubscriptionProviderXAIGrok, `not json`, 200, ""},
		{"grok private", contract.SubscriptionProviderXAIGrok, `{"subscription_tier_display":"owner@example.com","subscription_tier":"supergrok"}`, 200, "supergrok"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var metadataCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer test-access-token" {
					t.Error("missing authenticated GET")
				}
				switch r.URL.Path {
				case "/base/api/oauth/usage":
					_, _ = w.Write([]byte(`{"five_hour":{"utilization":25}}`))
				case "/base/v1/billing":
					if r.URL.RawQuery != "format=credits" {
						t.Error("missing credits format")
					}
					_, _ = w.Write([]byte(`{"config":{"creditUsagePercent":25}}`))
				case "/base/api/oauth/profile", "/base/v1/settings":
					metadataCalls.Add(1)
					if r.URL.RawQuery != "" {
						t.Error("usage query leaked to metadata request")
					}
					if test.provider == contract.SubscriptionProviderClaudeCode && r.Header.Get("Anthropic-Beta") == "" ||
						test.provider == contract.SubscriptionProviderXAIGrok && r.Header.Get("X-XAI-Token-Auth") == "" {
						t.Error("missing provider headers")
					}
					w.WriteHeader(test.status)
					_, _ = w.Write([]byte(test.metadata))
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
			expires := now.Add(time.Hour)
			accounts := subscription.NewMemoryAccountStore()
			credentials := accountauth.NewMemoryCredentialStore()
			account := contract.SubscriptionAccount{
				ID: "service_plan", Provider: test.provider, DisplayName: "Plan test",
				Status: contract.SubscriptionStatusConnected, CredentialRef: accountauth.CredentialRefFor("service_plan"),
				TokenExpiresAt: &expires, Capabilities: test.provider.Capabilities(), CreatedAt: now, UpdatedAt: now,
			}
			if err := accounts.PutAccount(context.Background(), account); err != nil {
				t.Fatal(err)
			}
			if err := credentials.Put(context.Background(), account.ID, accountauth.AccountTokens{
				AccessToken: "test-access-token", RefreshToken: "test-refresh-token", ExpiresAt: expires,
			}); err != nil {
				t.Fatal(err)
			}
			manager, err := subscription.NewManager(accounts, credentials,
				accountauth.OAuthConfig{HTTPClient: upstream.Client(), Now: func() time.Time { return now }},
				accountauth.OAuthConfig{Provider: test.provider, APIBaseURL: upstream.URL + "/base"},
			)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				usage, err := manager.Usage(context.Background(), account.ID)
				if err != nil || usage.PlanType != test.want || usage.Primary == nil || usage.Primary.UsedPercent != 25 {
					t.Fatalf("Usage() = %#v, err = %v; want plan %q and 25%% usage", usage, err, test.want)
				}
			}
			if metadataCalls.Load() != 1 {
				t.Fatalf("metadata calls = %d; want 1 with cached usage", metadataCalls.Load())
			}
		})
	}
}
