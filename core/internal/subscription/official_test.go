package subscription_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/claudecli"
	"github.com/QuantumNous/astrlink/core/internal/claudecli/fakeclaude"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func TestMain(m *testing.M) {
	fakeclaude.Main()
	os.Exit(m.Run())
}

var officialTestAccount = fakeclaude.Account{
	Email: "person@example.com", OrganizationID: "org-1", OrganizationName: "Example", SubscriptionType: "max",
}

type officialFixture struct {
	manager     *subscription.Manager
	accounts    *subscription.MemoryAccountStore
	credentials accountauth.AccountCredentialStore
	cli         claudecli.Accounts
	binDir      string

	mu      sync.Mutex
	stopped []contract.ServiceID
}

func newOfficialFixture(t *testing.T, settings fakeclaude.Settings, ids ...contract.ServiceID) *officialFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is linked by symlink")
	}
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	fixture := &officialFixture{
		accounts:    subscription.NewMemoryAccountStore(),
		credentials: accountauth.NewMemoryCredentialStore(),
		binDir:      t.TempDir(),
	}
	for _, id := range ids {
		if err := fixture.accounts.PutAccount(context.Background(), contract.SubscriptionAccount{
			ID: id, Provider: contract.SubscriptionProviderClaudeCode, Mode: contract.SubscriptionModeOfficial,
			Status: contract.SubscriptionStatusDisconnected, DisplayName: "Claude",
			Capabilities: contract.SubscriptionProviderClaudeCode.Capabilities(), CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	fixture.cli = claudecli.Accounts{
		Locator: &claudecli.Locator{Configured: fakeclaude.Install(t, fixture.binDir, settings)},
		Layout:  claudecli.Layout{Root: filepath.Join(root, "claude-accounts"), WorkDir: filepath.Join(root, "work")},
		Environ: []string{"PATH=/usr/bin:/bin", "HOME=" + root},
	}
	manager, err := subscription.NewManager(fixture.accounts, fixture.credentials, accountauth.OAuthConfig{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	manager.EnableOfficialClaude(fixture.cli)
	manager.SetOfficialStopper(func(id contract.ServiceID) {
		fixture.mu.Lock()
		fixture.stopped = append(fixture.stopped, id)
		fixture.mu.Unlock()
	})
	fixture.manager = manager
	return fixture
}

func (fixture *officialFixture) configDir(t *testing.T, id contract.ServiceID) string {
	t.Helper()
	dir, err := fixture.cli.Layout.ConfigDir(id)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func (fixture *officialFixture) signedIn(t *testing.T, id contract.ServiceID) bool {
	t.Helper()
	_, err := os.Stat(fakeclaude.AccountFile(fixture.configDir(t, id)))
	return err == nil
}

func (fixture *officialFixture) login(t *testing.T, id contract.ServiceID, code string) contract.AuthorizationSession {
	t.Helper()
	session, err := fixture.manager.BeginAuthorization(context.Background(), id, contract.AuthorizationFlowClaudeCLI)
	if err != nil {
		t.Fatal(err)
	}
	final, err := fixture.manager.CompleteAuthorizationCode(context.Background(), id, session.ID, code)
	if err != nil {
		t.Fatal(err)
	}
	return final
}

func TestOfficialLoginConnectsWithoutStoredToken(t *testing.T) {
	const id contract.ServiceID = "service_official_01"
	fixture := newOfficialFixture(t, fakeclaude.Settings{Code: "good-code", Account: officialTestAccount}, id)
	ctx := context.Background()

	if _, err := fixture.manager.BeginAuthorization(ctx, id, contract.AuthorizationFlowCode); err == nil {
		t.Fatal("an official account started AstrLink's own OAuth sign-in")
	}
	session, err := fixture.manager.BeginAuthorization(ctx, id, contract.AuthorizationFlowClaudeCLI)
	if err != nil {
		t.Fatal(err)
	}
	if session.Flow != contract.AuthorizationFlowClaudeCLI || session.AuthorizationURL != fakeclaude.LoginURL {
		t.Fatalf("session = %+v", session)
	}
	if account, _ := fixture.manager.Get(ctx, id); account.Status != contract.SubscriptionStatusAuthorizing {
		t.Fatalf("status while signing in = %s", account.Status)
	}
	if polled, ok := fixture.manager.GetAuthorization(ctx, id); !ok || polled.ID != session.ID {
		t.Fatalf("GetAuthorization = %+v, %v", polled, ok)
	}
	if _, err := fixture.manager.CompleteAuthorizationCode(ctx, id, "authorization_session_other", "good-code"); !errors.Is(err, accountauth.ErrSessionNotFound) {
		t.Fatalf("code for another session error = %v", err)
	}
	final, err := fixture.manager.CompleteAuthorizationCode(ctx, id, session.ID, "good-code")
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != contract.AuthorizationSessionStatusCompleted {
		t.Fatalf("final session = %+v (error %+v)", final, final.Error)
	}

	account, err := fixture.manager.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if account.Status != contract.SubscriptionStatusConnected || account.CredentialRef != "" || account.TokenExpiresAt != nil ||
		account.AccountHint != "pers***om" || account.ProviderAccountID != "org-1:person@example.com" || account.PlanType != "max" {
		t.Fatalf("account = %+v", account)
	}
	if _, err := fixture.credentials.Get(ctx, id); err == nil {
		t.Fatal("an official login stored a credential")
	}
	if _, err := fixture.manager.AccessToken(ctx, id); !errors.Is(err, subscription.ErrOfficialAccount) {
		t.Fatalf("AccessToken error = %v", err)
	}
	if _, err := fixture.manager.Usage(ctx, id); !errors.Is(err, subscription.ErrUsageUnavailable) {
		t.Fatalf("Usage error = %v", err)
	}
	if !fixture.signedIn(t, id) {
		t.Fatal("the CLI is not signed in")
	}
}

func TestOfficialLoginRejectsAnAccountConnectedElsewhere(t *testing.T) {
	const first, second contract.ServiceID = "service_official_01", "service_official_02"
	fixture := newOfficialFixture(t, fakeclaude.Settings{Code: "good-code", Account: officialTestAccount}, first, second)
	ctx := context.Background()

	if final := fixture.login(t, first, "good-code"); final.Status != contract.AuthorizationSessionStatusCompleted {
		t.Fatalf("first login = %+v", final)
	}
	final := fixture.login(t, second, "good-code")
	if final.Status != contract.AuthorizationSessionStatusFailed || final.Error == nil ||
		final.Error.Message != "this Claude account is already connected in another service" {
		t.Fatalf("duplicate login = %+v (error %+v)", final, final.Error)
	}
	if fixture.signedIn(t, second) {
		t.Fatal("the duplicate login stayed signed in")
	}
	if _, ok := fixture.manager.GetAuthorization(ctx, second); !ok {
		t.Fatal("the failed session is gone")
	}
	if account, _ := fixture.manager.Get(ctx, second); account.Status != contract.SubscriptionStatusDisconnected || account.LastError == nil {
		t.Fatalf("duplicate account = %+v", account)
	}
}

func TestOfficialCancelRestoresTheAccountState(t *testing.T) {
	const id contract.ServiceID = "service_official_01"
	fixture := newOfficialFixture(t, fakeclaude.Settings{Code: "good-code", Account: officialTestAccount}, id)
	ctx := context.Background()

	if _, err := fixture.manager.BeginAuthorization(ctx, id, contract.AuthorizationFlowClaudeCLI); err != nil {
		t.Fatal(err)
	}
	cancelled, err := fixture.manager.CancelAuthorization(ctx, id)
	if err != nil || cancelled.Status != contract.AuthorizationSessionStatusCancelled {
		t.Fatalf("CancelAuthorization = %+v, %v", cancelled, err)
	}
	if account, _ := fixture.manager.Get(ctx, id); account.Status != contract.SubscriptionStatusDisconnected {
		t.Fatalf("status after cancel = %s", account.Status)
	}
	if _, err := fixture.manager.CancelAuthorization(ctx, id); !errors.Is(err, accountauth.ErrSessionNotPending) {
		t.Fatalf("second cancel error = %v", err)
	}

	fixture.login(t, id, "good-code")
	if _, err := fixture.manager.BeginAuthorization(ctx, id, contract.AuthorizationFlowClaudeCLI); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.CancelAuthorization(ctx, id); err != nil {
		t.Fatal(err)
	}
	if account, _ := fixture.manager.Get(ctx, id); account.Status != contract.SubscriptionStatusNeedsReauth {
		t.Fatalf("status after cancelling a new sign-in = %s", account.Status)
	}
}

func TestOfficialLogoutAndDeleteSignTheCLIOut(t *testing.T) {
	const id contract.ServiceID = "service_official_01"
	fixture := newOfficialFixture(t, fakeclaude.Settings{Code: "good-code", Account: officialTestAccount}, id)
	ctx := context.Background()

	fixture.login(t, id, "good-code")
	account, err := fixture.manager.Logout(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if account.Status != contract.SubscriptionStatusDisconnected || account.ProviderAccountID != "" || account.AccountHint != "" || account.PlanType != "" {
		t.Fatalf("account after logout = %+v", account)
	}
	if fixture.signedIn(t, id) {
		t.Fatal("logout left the CLI signed in")
	}
	if _, err := os.Stat(fixture.configDir(t, id)); err != nil {
		t.Fatalf("logout removed the config dir: %v", err)
	}

	fixture.login(t, id, "good-code")
	if err := fixture.manager.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fixture.configDir(t, id)); !os.IsNotExist(err) {
		t.Fatalf("delete kept the config dir: %v", err)
	}
	fixture.mu.Lock()
	stopped := append([]contract.ServiceID(nil), fixture.stopped...)
	fixture.mu.Unlock()
	if len(stopped) != 2 || stopped[0] != id || stopped[1] != id {
		t.Fatalf("stopped processes = %v", stopped)
	}
}

func TestOfficialSignedOutNeedsReauth(t *testing.T) {
	const id contract.ServiceID = "service_official_01"
	fixture := newOfficialFixture(t, fakeclaude.Settings{Code: "good-code", Account: officialTestAccount}, id)
	ctx := context.Background()

	fixture.login(t, id, "good-code")
	if err := fixture.manager.MarkOfficialSignedOut(ctx, id); err != nil {
		t.Fatal(err)
	}
	account, _ := fixture.manager.Get(ctx, id)
	if account.Status != contract.SubscriptionStatusNeedsReauth || account.LastError == nil ||
		account.LastError.Code != subscription.ErrCodeClaudeSignedOut {
		t.Fatalf("account = %+v", account)
	}
}

func TestLegacyClaudeRefusesTheCLISignIn(t *testing.T) {
	const id contract.ServiceID = "service_legacy_01"
	fixture := newOfficialFixture(t, fakeclaude.Settings{}, id)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	if err := fixture.accounts.PutAccount(context.Background(), contract.SubscriptionAccount{
		ID: id, Provider: contract.SubscriptionProviderClaudeCode, Status: contract.SubscriptionStatusDisconnected,
		DisplayName: "Claude", Capabilities: contract.SubscriptionProviderClaudeCode.Capabilities(), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.manager.BeginAuthorization(context.Background(), id, contract.AuthorizationFlowClaudeCLI)
	if err == nil || !strings.Contains(err.Error(), "only official Claude accounts") {
		t.Fatalf("BeginAuthorization error = %v", err)
	}
}
