package subscription_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func TestAntigravityUsagePreservesModelQuotas(t *testing.T) {
	for _, count := range []int{16, 17, 33, 256, 257} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			models := make(map[string]any, count+1)
			for index := range count {
				models[fmt.Sprintf("gemini-test-%03d", index)] = map[string]any{
					"quotaInfo": map[string]any{"remainingFraction": 0.75, "resetTime": "2026-09-30T12:00:00Z"},
				}
			}
			models["model-without-quota"] = map[string]any{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1internal:fetchAvailableModels" {
					t.Errorf("unexpected quota endpoint: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test-access" || r.UserAgent() != accountauth.AntigravityUserAgent() {
					t.Error("missing Antigravity identity")
				}
				for key := range r.Header {
					if strings.HasPrefix(strings.ToLower(key), "x-astrlink-") {
						t.Errorf("local header reached upstream: %s", key)
					}
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["project"] != "test-project" {
					t.Error("missing trusted project")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
			}))
			t.Cleanup(upstream.Close)

			ctx := context.Background()
			now := time.Date(2026, 9, 30, 6, 0, 0, 0, time.UTC)
			accounts := subscription.NewMemoryAccountStore()
			credentials := accountauth.NewMemoryCredentialStore()
			account := connectedAccount("service_antigravity", now, now.Add(time.Hour), "test-account")
			account.Provider = contract.SubscriptionProviderAntigravity
			account.Capabilities = account.Provider.Capabilities()
			if err := accounts.PutAccount(ctx, account); err != nil {
				t.Fatal(err)
			}
			if err := credentials.Put(ctx, account.ID, accountauth.AccountTokens{
				AccessToken: "test-access", RefreshToken: "test-refresh", ProjectID: "test-project", PlanType: "google-ai-ultra",
				ExpiresAt: now.Add(time.Hour),
			}); err != nil {
				t.Fatal(err)
			}
			manager, err := subscription.NewManager(accounts, credentials,
				accountauth.OAuthConfig{Now: func() time.Time { return now }},
				accountauth.OAuthConfig{Provider: account.Provider, APIBaseURL: upstream.URL, HTTPClient: upstream.Client()},
			)
			if err != nil {
				t.Fatal(err)
			}
			usage, err := manager.Usage(ctx, account.ID)
			if count > 256 {
				if err == nil {
					t.Fatal("accepted more than 256 quota windows")
				}
				return
			}
			if err != nil {
				t.Fatalf("Usage() = %v", err)
			}
			if usage.ServiceID != account.ID || !usage.FetchedAt.Equal(now) || usage.PlanType != "google-ai-ultra" || len(usage.AdditionalRateLimits) != count {
				t.Fatalf("unexpected quota snapshot: %#v", usage)
			}
			for index, extra := range usage.AdditionalRateLimits {
				if extra.LimitName != fmt.Sprintf("gemini-test-%03d", index) || extra.MeteredFeature != extra.LimitName ||
					extra.Primary == nil || extra.Primary.UsedPercent != 25 || extra.Primary.ResetAt == nil ||
					extra.Primary.ResetAt.Format(time.RFC3339) != "2026-09-30T12:00:00Z" {
					t.Fatalf("model quota lost or changed: %#v", extra)
				}
			}
		})
	}
}
