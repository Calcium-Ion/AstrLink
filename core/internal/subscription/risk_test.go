package subscription_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

type riskTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *riskTestClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *riskTestClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	clock.mu.Unlock()
}

type riskTestFixture struct {
	accounts    *subscription.MemoryAccountStore
	credentials *accountauth.MemoryCredentialStore
	events      *subscription.MemoryRiskEventStore
	manager     *subscription.Manager
	clock       *riskTestClock
	id          contract.ServiceID
}

func newRiskTestFixture(t *testing.T, config accountauth.OAuthConfig) riskTestFixture {
	t.Helper()
	ctx := context.Background()
	clock := &riskTestClock{now: time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)}
	accounts := subscription.NewMemoryAccountStore()
	credentials := accountauth.NewMemoryCredentialStore()
	account := connectedAccount("service_codex_risk", clock.Now(), clock.Now().Add(time.Hour), "acct_risk_123456")
	if err := accounts.PutAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if err := credentials.Put(ctx, account.ID, accountauth.AccountTokens{
		AccessToken: "risk-access-1", RefreshToken: "risk-refresh-1",
		AccountID: account.ProviderAccountID, ExpiresAt: clock.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	config.Now = clock.Now
	manager, err := subscription.NewManager(accounts, credentials, config)
	if err != nil {
		t.Fatal(err)
	}
	events := subscription.NewMemoryRiskEventStore()
	manager.SetRiskEventStore(events)
	return riskTestFixture{
		accounts: accounts, credentials: credentials, events: events,
		manager: manager, clock: clock, id: account.ID,
	}
}

func (fixture riskTestFixture) risk(t *testing.T) *contract.SubscriptionRisk {
	t.Helper()
	account, err := fixture.accounts.GetAccount(context.Background(), fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	return account.Risk
}

func (fixture riskTestFixture) eventKinds(t *testing.T) []contract.SubscriptionRiskEventKind {
	t.Helper()
	events, err := fixture.manager.RiskEvents(context.Background(), fixture.id, 50)
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]contract.SubscriptionRiskEventKind, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	return kinds
}

func coolingObservation(code string, until time.Time) contract.SubscriptionRiskObservation {
	return contract.SubscriptionRiskObservation{
		State: contract.SubscriptionRiskCooling, Code: code, HTTPStatus: http.StatusTooManyRequests,
		Message: "limit reached", PausedUntil: &until,
	}
}

func TestReportRiskPrecedenceAndDeduplication(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newRiskTestFixture(t, accountauth.OAuthConfig{})
	now := fixture.clock.Now()

	if err := fixture.manager.ReportRisk(ctx, fixture.id, coolingObservation(contract.RiskCodeRateLimit5h, now.Add(2*time.Hour))); err != nil {
		t.Fatal(err)
	}
	// A concurrent in-flight failure with an earlier reset keeps the later
	// deadline and does not add history.
	if err := fixture.manager.ReportRisk(ctx, fixture.id, coolingObservation(contract.RiskCodeRateLimit5h, now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	risk := fixture.risk(t)
	if risk == nil || risk.State != contract.SubscriptionRiskCooling || risk.Occurrences != 2 ||
		!risk.PausedUntil.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("cooling risk after duplicate = %+v", risk)
	}
	if kinds := fixture.eventKinds(t); len(kinds) != 1 {
		t.Fatalf("duplicate cooling signal added history: %v", kinds)
	}

	suspended := contract.SubscriptionRiskObservation{
		State: contract.SubscriptionRiskSuspended, Code: contract.RiskCodeAccountDeactivated,
		HTTPStatus: http.StatusPaymentRequired, Message: "workspace   deactivated\n",
	}
	if err := fixture.manager.ReportRisk(ctx, fixture.id, suspended); err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.ReportRisk(ctx, fixture.id, coolingObservation(contract.RiskCodeUsageLimitReached, now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	risk = fixture.risk(t)
	if risk == nil || risk.State != contract.SubscriptionRiskSuspended || risk.Code != contract.RiskCodeAccountDeactivated ||
		risk.PausedUntil != nil || risk.Occurrences != 1 || risk.Message != "workspace deactivated" {
		t.Fatalf("suspended risk was downgraded or unsanitized: %+v", risk)
	}
	if kinds := fixture.eventKinds(t); len(kinds) != 2 || kinds[0] != contract.SubscriptionRiskEventSuspended {
		t.Fatalf("risk history = %v, want suspended then cooling", kinds)
	}

	// A suspended account stays paused regardless of elapsed time.
	fixture.clock.Advance(30 * 24 * time.Hour)
	if !fixture.risk(t).Blocks(fixture.clock.Now()) {
		t.Fatal("suspended risk stopped blocking")
	}
}

func TestReportRiskIgnoresDisconnectedAccounts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newRiskTestFixture(t, accountauth.OAuthConfig{})
	if _, err := fixture.manager.Logout(ctx, fixture.id); err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.ReportRisk(ctx, fixture.id, contract.SubscriptionRiskObservation{
		State: contract.SubscriptionRiskSuspended, Code: contract.RiskCodeOrganizationDisabled,
	}); err != nil {
		t.Fatal(err)
	}
	if risk := fixture.risk(t); risk != nil {
		t.Fatalf("disconnected account recorded risk %+v", risk)
	}
	if err := fixture.manager.ReportRisk(ctx, "service_missing", contract.SubscriptionRiskObservation{
		State: contract.SubscriptionRiskSuspended, Code: contract.RiskCodeOrganizationDisabled,
	}); err != nil {
		t.Fatalf("ReportRisk(missing) = %v, want nil", err)
	}
}

func TestReportRiskEscalatesRepeatedForbidden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newRiskTestFixture(t, accountauth.OAuthConfig{})
	forbidden := func() contract.SubscriptionRiskObservation {
		until := fixture.clock.Now().Add(10 * time.Minute)
		return contract.SubscriptionRiskObservation{
			State: contract.SubscriptionRiskCooling, Code: contract.RiskCodeForbidden,
			HTTPStatus: http.StatusForbidden, PausedUntil: &until, Forbidden: true,
		}
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := fixture.manager.ReportRisk(ctx, fixture.id, forbidden()); err != nil {
			t.Fatal(err)
		}
		risk := fixture.risk(t)
		if risk == nil || risk.State != contract.SubscriptionRiskCooling ||
			!risk.PausedUntil.Equal(fixture.clock.Now().Add(10*time.Minute)) {
			t.Fatalf("403 #%d risk = %+v, want 10 minute cooling", attempt, risk)
		}
		fixture.clock.Advance(11 * time.Minute)
	}
	if err := fixture.manager.ReportRisk(ctx, fixture.id, forbidden()); err != nil {
		t.Fatal(err)
	}
	risk := fixture.risk(t)
	if risk == nil || risk.State != contract.SubscriptionRiskSuspended || risk.Code != contract.RiskCodeRepeatedForbidden ||
		risk.PausedUntil != nil {
		t.Fatalf("third 403 risk = %+v, want suspended repeated_forbidden", risk)
	}
}

func TestReportRiskForbiddenOutsideWindowDoesNotEscalate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newRiskTestFixture(t, accountauth.OAuthConfig{})
	for range 3 {
		until := fixture.clock.Now().Add(10 * time.Minute)
		if err := fixture.manager.ReportRisk(ctx, fixture.id, contract.SubscriptionRiskObservation{
			State: contract.SubscriptionRiskCooling, Code: contract.RiskCodeClientIdentityRejected,
			HTTPStatus: http.StatusForbidden, PausedUntil: &until, Forbidden: true,
		}); err != nil {
			t.Fatal(err)
		}
		fixture.clock.Advance(2 * time.Hour)
	}
	if risk := fixture.risk(t); risk == nil || risk.State != contract.SubscriptionRiskCooling {
		t.Fatalf("403s spread over six hours escalated: %+v", risk)
	}
}

func TestClearRiskRestoresSchedulingAndRecordsHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newRiskTestFixture(t, accountauth.OAuthConfig{})
	if err := fixture.manager.ReportRisk(ctx, fixture.id, contract.SubscriptionRiskObservation{
		State: contract.SubscriptionRiskSuspended, Code: contract.RiskCodeOAuthNotAllowed, HTTPStatus: http.StatusForbidden,
	}); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Advance(time.Minute)
	account, err := fixture.manager.ClearRisk(ctx, fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	if account.Risk != nil || account.Status != contract.SubscriptionStatusConnected || fixture.risk(t) != nil {
		t.Fatalf("ClearRisk() account = %+v", account)
	}
	if kinds := fixture.eventKinds(t); len(kinds) != 2 || kinds[0] != contract.SubscriptionRiskEventCleared {
		t.Fatalf("risk history after clear = %v", kinds)
	}
	// Clearing an account without risk is idempotent and adds no history.
	if _, err := fixture.manager.ClearRisk(ctx, fixture.id); err != nil {
		t.Fatal(err)
	}
	if kinds := fixture.eventKinds(t); len(kinds) != 2 {
		t.Fatalf("idempotent clear added history: %v", kinds)
	}
	if _, err := fixture.manager.ClearRisk(ctx, "service_missing"); err == nil {
		t.Fatal("ClearRisk(missing) succeeded")
	}
}

func TestClearExpiredRiskOnlyDropsElapsedCooling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newRiskTestFixture(t, accountauth.OAuthConfig{})
	until := fixture.clock.Now().Add(time.Hour)
	if err := fixture.manager.ReportRisk(ctx, fixture.id, coolingObservation(contract.RiskCodeRateLimit7d, until)); err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.ClearExpiredRisk(ctx, fixture.id); err != nil {
		t.Fatal(err)
	}
	if fixture.risk(t) == nil {
		t.Fatal("ClearExpiredRisk() dropped an active cooling risk")
	}
	fixture.clock.Advance(time.Hour)
	if err := fixture.manager.ClearExpiredRisk(ctx, fixture.id); err != nil {
		t.Fatal(err)
	}
	if risk := fixture.risk(t); risk != nil {
		t.Fatalf("ClearExpiredRisk() kept elapsed cooling %+v", risk)
	}

	if err := fixture.manager.ReportRisk(ctx, fixture.id, contract.SubscriptionRiskObservation{
		State: contract.SubscriptionRiskSuspended, Code: contract.RiskCodeOrganizationDisabled,
	}); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Advance(365 * 24 * time.Hour)
	if err := fixture.manager.ClearExpiredRisk(ctx, fixture.id); err != nil {
		t.Fatal(err)
	}
	if fixture.risk(t) == nil {
		t.Fatal("ClearExpiredRisk() dropped a suspended risk")
	}
}

func TestLogoutClearsRisk(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newRiskTestFixture(t, accountauth.OAuthConfig{})
	if err := fixture.manager.ReportRisk(ctx, fixture.id, contract.SubscriptionRiskObservation{
		State: contract.SubscriptionRiskSuspended, Code: contract.RiskCodeAccountDeactivated,
	}); err != nil {
		t.Fatal(err)
	}
	account, err := fixture.manager.Logout(ctx, fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	if account.Risk != nil || fixture.risk(t) != nil {
		t.Fatalf("Logout() kept risk: %+v", account.Risk)
	}
}

func TestHandleUnauthorizedRefreshesOnceThenRequiresReauthorization(t *testing.T) {
	t.Parallel()
	var refreshCalls atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls := refreshCalls.Add(1)
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token":  "risk-access-" + string(rune('1'+calls)),
			"refresh_token": "risk-refresh-rotated",
			"expires_in":    3600,
		})
	}))
	t.Cleanup(issuer.Close)
	ctx := context.Background()
	fixture := newRiskTestFixture(t, accountauth.OAuthConfig{
		ClientID: "astrlink_test_client", Issuer: issuer.URL, HTTPClient: issuer.Client(),
	})

	// Concurrent 401s for the same rejected token share one refresh.
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			if err := fixture.manager.HandleUnauthorized(ctx, fixture.id, "risk-access-1"); err != nil {
				t.Errorf("HandleUnauthorized(first) = %v", err)
			}
		})
	}
	group.Wait()
	if refreshCalls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls.Load())
	}
	stored, err := fixture.credentials.Get(ctx, fixture.id)
	if err != nil || stored.AccessToken != "risk-access-2" {
		t.Fatalf("stored credential after refresh = %q, %v", stored.AccessToken, err)
	}
	account, err := fixture.accounts.GetAccount(ctx, fixture.id)
	if err != nil || account.Status != contract.SubscriptionStatusConnected {
		t.Fatalf("account after forced refresh = %+v, %v", account, err)
	}

	// The refreshed credential is rejected again: the account needs a login.
	fixture.clock.Advance(30 * time.Second)
	if err := fixture.manager.HandleUnauthorized(ctx, fixture.id, "risk-access-2"); err != nil {
		t.Fatal(err)
	}
	account, err = fixture.accounts.GetAccount(ctx, fixture.id)
	if err != nil {
		t.Fatal(err)
	}
	if account.Status != contract.SubscriptionStatusNeedsReauth || account.LastError == nil ||
		account.LastError.Code != subscription.ErrCodeUpstreamUnauthorized || account.CredentialRef == "" {
		t.Fatalf("account after repeated 401 = %+v", account)
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("repeated 401 refreshed again: %d calls", refreshCalls.Load())
	}
}

func TestHandleUnauthorizedRefreshesAgainAfterWindow(t *testing.T) {
	t.Parallel()
	var refreshCalls atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls := refreshCalls.Add(1)
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token": "risk-access-" + string(rune('1'+calls)), "expires_in": 3600,
		})
	}))
	t.Cleanup(issuer.Close)
	ctx := context.Background()
	fixture := newRiskTestFixture(t, accountauth.OAuthConfig{
		ClientID: "astrlink_test_client", Issuer: issuer.URL, HTTPClient: issuer.Client(),
	})
	if err := fixture.manager.HandleUnauthorized(ctx, fixture.id, "risk-access-1"); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Advance(10 * time.Minute)
	if err := fixture.manager.HandleUnauthorized(ctx, fixture.id, "risk-access-2"); err != nil {
		t.Fatal(err)
	}
	account, err := fixture.accounts.GetAccount(ctx, fixture.id)
	if err != nil || account.Status != contract.SubscriptionStatusConnected || refreshCalls.Load() != 2 {
		t.Fatalf("account = %+v, refresh calls = %d, err = %v", account.Status, refreshCalls.Load(), err)
	}
	// A 401 for a token that already rotated does nothing.
	if err := fixture.manager.HandleUnauthorized(ctx, fixture.id, "risk-access-1"); err != nil || refreshCalls.Load() != 2 {
		t.Fatalf("stale 401 refreshed: calls = %d, err = %v", refreshCalls.Load(), err)
	}
}
