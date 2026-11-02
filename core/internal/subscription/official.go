package subscription

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/claudecli"
)

var (
	// ErrOfficialAccount means a stored token was requested for an account the
	// claude CLI serves.
	ErrOfficialAccount = errors.New("official Claude accounts keep no AstrLink token")
	// ErrOfficialUnavailable means this Core was started without the claude
	// CLI runner.
	ErrOfficialUnavailable = errors.New("official Claude accounts are unavailable")
)

// ErrCodeClaudeSignedOut marks an official account whose CLI lost its login.
const ErrCodeClaudeSignedOut = "claude_signed_out"

// codeSubmitWait bounds how long submitting a pasted code waits for the CLI
// to finish exchanging it.
const codeSubmitWait = 2 * time.Minute

type officialClaude struct {
	accounts claudecli.Accounts
	logins   *claudecli.Logins
	// stop ends the serving processes of an account; nil before the runtime
	// is wired.
	stop func(contract.ServiceID)
}

// EnableOfficialClaude lets Claude accounts in official mode sign in through
// the user's claude CLI. Call it before serving requests.
func (manager *Manager) EnableOfficialClaude(accounts claudecli.Accounts) {
	manager.official = &officialClaude{accounts: accounts}
	manager.official.logins = claudecli.NewLogins(accounts, manager.persistOfficialLogin, manager.now)
}

// SetOfficialStopper wires the hook that ends an account's serving processes
// when it signs out or is deleted.
func (manager *Manager) SetOfficialStopper(stop func(contract.ServiceID)) {
	if manager.official != nil {
		manager.official.stop = stop
	}
}

// OfficialAccounts returns the CLI account runner, if enabled.
func (manager *Manager) OfficialAccounts() (claudecli.Accounts, bool) {
	if manager.official == nil {
		return claudecli.Accounts{}, false
	}
	return manager.official.accounts, true
}

func (manager *Manager) beginOfficialAuthorization(
	ctx context.Context,
	id contract.ServiceID,
	flow contract.AuthorizationFlow,
) (contract.AuthorizationSession, error) {
	if manager.official == nil {
		return contract.AuthorizationSession{}, ErrOfficialUnavailable
	}
	if flow != contract.AuthorizationFlowClaudeCLI {
		return contract.AuthorizationSession{}, fmt.Errorf("official Claude accounts sign in with the claude CLI")
	}
	attemptID, err := manager.reserveAuthorizationAttempt(ctx, id)
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	previous, err := manager.markAuthorizing(ctx, id, attemptID)
	if err != nil {
		manager.abortAuthorizationAttempt(id, attemptID, false)
		return contract.AuthorizationSession{}, err
	}
	session, err := manager.official.logins.Begin(ctx, id)
	if err != nil {
		manager.rollbackAuthorizationAttempt(ctx, id, attemptID, previous)
		return contract.AuthorizationSession{}, err
	}
	if !manager.finishAuthorizationStart(id, attemptID, session.ID) {
		_, _ = manager.official.logins.Cancel(id)
		return contract.AuthorizationSession{}, fmt.Errorf("authorization was interrupted by an account lifecycle change")
	}
	return session, nil
}

func (manager *Manager) completeOfficialCode(
	ctx context.Context,
	id contract.ServiceID,
	sessionID contract.AuthorizationSessionID,
	code string,
) (contract.AuthorizationSession, error) {
	if manager.official == nil {
		return contract.AuthorizationSession{}, ErrOfficialUnavailable
	}
	done := manager.official.logins.Done(id)
	if _, err := manager.official.logins.SubmitCode(id, sessionID, code); err != nil {
		return contract.AuthorizationSession{}, officialLoginError(err)
	}
	// The CLI exchanges the code itself; answer with its outcome, like the
	// legacy code exchange does.
	wait := time.NewTimer(codeSubmitWait)
	defer wait.Stop()
	select {
	case <-done:
	case <-wait.C:
	case <-ctx.Done():
	}
	session, _ := manager.official.logins.Get(id)
	return session, nil
}

func officialLoginError(err error) error {
	switch {
	case errors.Is(err, claudecli.ErrLoginNotFound):
		return fmt.Errorf("%w", accountauth.ErrSessionNotFound)
	case errors.Is(err, claudecli.ErrLoginNotPending):
		return fmt.Errorf("%w", accountauth.ErrSessionNotPending)
	}
	return err
}

// persistOfficialLogin records a finished `claude auth login`. AstrLink keeps
// only what `claude auth status` reports: who signed in and their plan.
func (manager *Manager) persistOfficialLogin(
	ctx context.Context,
	id contract.ServiceID,
	sessionID contract.AuthorizationSessionID,
	status claudecli.Status,
) error {
	session := contract.AuthorizationSession{ID: sessionID, ServiceID: id, Provider: contract.SubscriptionProviderClaudeCode}
	providerAccountID := status.AccountID()
	if err := manager.reserveProviderAccount(ctx, session, providerAccountID); err != nil {
		if errors.Is(err, ErrAccountAlreadyConnected) {
			return errors.New("this Claude account is already connected in another service")
		}
		return err
	}
	defer manager.releaseProviderAccount(string(session.Provider)+":"+providerAccountID, id)
	_, err := manager.mutateAccount(ctx, id, func(account *contract.SubscriptionAccount) error {
		attempt, ok := manager.authorizationAttempts[id]
		if !ok || (attempt.sessionID != "" && attempt.sessionID != sessionID) || manager.lifecycleTransitions[id] {
			return fmt.Errorf("authorization session was superseded")
		}
		if !account.Official() {
			return fmt.Errorf("service is not an official Claude account")
		}
		now := manager.now().UTC()
		if account.ProviderAccountID != providerAccountID {
			delete(manager.usageCache, id)
		}
		hint := status.Email
		if hint == "" {
			hint = providerAccountID
		}
		account.Status = contract.SubscriptionStatusConnected
		account.CredentialRef = ""
		account.AccountHint = maskAccountHint(hint)
		account.ProviderAccountID = providerAccountID
		account.PlanType = ""
		if contract.ValidatePlanType(status.SubscriptionType) == nil {
			account.PlanType = status.SubscriptionType
		}
		account.TokenExpiresAt = nil
		account.LastRefreshAt = &now
		account.LastError = nil
		account.Risk = nil
		delete(manager.forcedRefreshAt, id)
		account.UpdatedAt = now
		if attempt.starting {
			attempt.completed = true
			attempt.sessionID = sessionID
			manager.authorizationAttempts[id] = attempt
		} else {
			delete(manager.authorizationAttempts, id)
		}
		return nil
	})
	return err
}

// signOutOfficial ends every trace of an official account's login: a pending
// sign-in, its serving processes and the CLI's own login, which the CLI
// removes from the Keychain or its config directory. With remove, the config
// directory goes too.
func (manager *Manager) signOutOfficial(ctx context.Context, id contract.ServiceID, remove bool) error {
	if manager.official == nil {
		return nil
	}
	manager.official.logins.Forget(id)
	if manager.official.stop != nil {
		manager.official.stop(id)
	}
	layout := manager.official.accounts.Layout
	configDir, err := layout.ConfigDir(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(configDir); err == nil {
		// Without the CLI the login cannot be signed out; removing the
		// directory still drops the CLI's file-based login.
		if err := manager.official.accounts.Logout(ctx, id, configDir); err != nil &&
			!errors.Is(err, claudecli.ErrNotInstalled) && !errors.Is(err, claudecli.ErrTooOld) {
			return err
		}
	}
	if remove {
		return layout.RemoveConfigDir(id)
	}
	return nil
}

// MarkOfficialSignedOut records that an official account's CLI is no longer
// signed in, so routing stops choosing it until the user signs in again.
func (manager *Manager) MarkOfficialSignedOut(ctx context.Context, id contract.ServiceID) error {
	_, err := manager.mutateAccount(ctx, id, func(account *contract.SubscriptionAccount) error {
		if !account.Official() || account.Status != contract.SubscriptionStatusConnected ||
			manager.lifecycleTransitions[id] {
			return nil
		}
		account.Status = contract.SubscriptionStatusNeedsReauth
		account.LastError = &contract.SubscriptionError{
			Code:    ErrCodeClaudeSignedOut,
			Message: "Claude Code is signed out; sign in again",
		}
		account.UpdatedAt = manager.now().UTC()
		return nil
	})
	return err
}

// hadLogin reports whether an account that stops authorizing had a working
// login before, so it needs reauthorization rather than a first sign-in.
func hadLogin(account contract.SubscriptionAccount) bool {
	if account.Official() {
		return account.ProviderAccountID != ""
	}
	return account.CredentialRef != ""
}
