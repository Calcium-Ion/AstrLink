package subscription

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

// Manager owns SubscriptionAccount lifecycle for openai_codex.
type Manager struct {
	mu          sync.Mutex
	accounts    AccountStore
	credentials accountauth.AccountCredentialStore
	sessions    *accountauth.SessionManager
	tokens      *accountauth.TokenSource
	provider    *CodexProvider
	now         func() time.Time
	newID       func() (contract.SubscriptionAccountID, error)
}

func NewManager(accounts AccountStore, credentials accountauth.AccountCredentialStore, oauth accountauth.OAuthConfig) (*Manager, error) {
	if accounts == nil {
		return nil, fmt.Errorf("subscription account store is required")
	}
	if credentials == nil {
		return nil, fmt.Errorf("account credential store is required")
	}
	oauth = oauth.Normalize()
	now := oauth.Now
	manager := &Manager{
		accounts:    accounts,
		credentials: credentials,
		now:         now,
		newID:       randomSubscriptionAccountID,
		provider:    NewCodexProvider(oauth),
	}
	manager.sessions = accountauth.NewSessionManager(oauth, credentials, manager.persistAuthorizedTokens)
	tokenClient := accountauth.NewTokenClient(oauth)
	manager.tokens = accountauth.NewTokenSource(credentials, tokenClient, oauth.RefreshSkew, now)
	manager.tokens.SetHooks(manager.onTokenRotated, manager.onInvalidGrant)
	return manager, nil
}

func (manager *Manager) AuthorizationBoundary() string {
	return ""
}

func (manager *Manager) List(ctx context.Context) ([]contract.SubscriptionAccount, error) {
	items, err := manager.accounts.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	for index := range items {
		items[index] = manager.publicAccount(items[index])
	}
	return items, nil
}

func (manager *Manager) Get(ctx context.Context, id contract.SubscriptionAccountID) (contract.SubscriptionAccount, error) {
	account, err := manager.accounts.GetAccount(ctx, id)
	if err != nil {
		return contract.SubscriptionAccount{}, err
	}
	return manager.publicAccount(account), nil
}

func (manager *Manager) BeginAuthorization(
	ctx context.Context,
	id contract.ServiceID,
	flow contract.AuthorizationFlow,
) (contract.AuthorizationSession, error) {
	account, err := manager.accounts.GetAccount(ctx, id)
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	session, err := manager.sessions.Begin(ctx, id, flow)
	if err != nil {
		if errors.Is(err, accountauth.ErrCredentialStoreUnavailable) {
			return contract.AuthorizationSession{}, fmt.Errorf("%w", ErrCredentialUnavailable)
		}
		return contract.AuthorizationSession{}, err
	}
	now := manager.now().UTC()
	account.Status = contract.SubscriptionStatusAuthorizing
	account.LastError = nil
	account.AuthorizationBoundary = ""
	account.UpdatedAt = now
	if err := manager.accounts.PutAccount(ctx, account); err != nil {
		_, _ = manager.sessions.Cancel(ctx, id)
		return contract.AuthorizationSession{}, err
	}
	return session, nil
}

func (manager *Manager) GetAuthorization(
	ctx context.Context,
	id contract.ServiceID,
) (contract.AuthorizationSession, bool) {
	session, ok := manager.sessions.Get(id)
	if !ok {
		manager.reconcileEndedAuthorization(ctx, id, &contract.SubscriptionError{
			Code:    accountauth.ErrCodeSessionInterrupted,
			Message: "authorization session ended; sign in again",
		})
		return session, false
	}
	if session.Status == contract.AuthorizationSessionStatusPending ||
		session.Status == contract.AuthorizationSessionStatusCompleted {
		return session, ok
	}
	manager.reconcileEndedAuthorization(ctx, id, session.Error)
	return session, ok
}

func (manager *Manager) reconcileEndedAuthorization(
	ctx context.Context,
	id contract.ServiceID,
	failure *contract.SubscriptionError,
) {
	account, err := manager.accounts.GetAccount(ctx, id)
	if err != nil || account.Status != contract.SubscriptionStatusAuthorizing {
		return
	}
	account.Status = contract.SubscriptionStatusDisconnected
	if account.CredentialRef != "" {
		account.Status = contract.SubscriptionStatusNeedsReauth
	}
	account.LastError = failure
	account.UpdatedAt = manager.now().UTC()
	_ = manager.accounts.PutAccount(ctx, account)
}

func (manager *Manager) CancelAuthorization(ctx context.Context, id contract.ServiceID) (contract.AuthorizationSession, error) {
	session, err := manager.sessions.Cancel(ctx, id)
	if err != nil {
		return session, err
	}
	account, getErr := manager.accounts.GetAccount(ctx, id)
	if getErr == nil {
		account.Status = contract.SubscriptionStatusDisconnected
		if account.CredentialRef != "" {
			account.Status = contract.SubscriptionStatusNeedsReauth
		}
		account.LastError = nil
		account.UpdatedAt = manager.now().UTC()
		_ = manager.accounts.PutAccount(ctx, account)
	}
	return session, nil
}

func (manager *Manager) Reconnect(
	ctx context.Context,
	id contract.SubscriptionAccountID,
	flow contract.AuthorizationFlow,
) (contract.AuthorizationSession, error) {
	if _, err := manager.accounts.GetAccount(ctx, id); err != nil {
		return contract.AuthorizationSession{}, err
	}
	return manager.BeginAuthorization(ctx, id, flow)
}

func (manager *Manager) Logout(ctx context.Context, id contract.SubscriptionAccountID) (contract.SubscriptionAccount, error) {
	account, err := manager.accounts.GetAccount(ctx, id)
	if err != nil {
		return contract.SubscriptionAccount{}, err
	}
	if err := manager.credentials.Delete(ctx, id); err != nil {
		return contract.SubscriptionAccount{}, fmt.Errorf("%w: %v", ErrCredentialUnavailable, err)
	}
	now := manager.now().UTC()
	account.Status = contract.SubscriptionStatusDisconnected
	account.CredentialRef = ""
	account.AccountHint = ""
	account.ProviderAccountID = ""
	account.TokenExpiresAt = nil
	account.LastRefreshAt = nil
	account.LastError = nil
	account.AuthorizationBoundary = manager.AuthorizationBoundary()
	account.UpdatedAt = now
	if err := manager.accounts.PutAccount(ctx, account); err != nil {
		return contract.SubscriptionAccount{}, err
	}
	return manager.publicAccount(account), nil
}

func (manager *Manager) Delete(ctx context.Context, id contract.ServiceID) error {
	if err := manager.CleanupCredentialsForDelete(ctx, id); err != nil {
		return err
	}
	return manager.accounts.DeleteAccount(ctx, id)
}

func (manager *Manager) CleanupCredentialsForDelete(ctx context.Context, id contract.ServiceID) error {
	account, err := manager.accounts.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	manager.sessions.CancelAllForService(id)
	if account.CredentialRef != "" {
		if err := manager.credentials.Delete(ctx, id); err != nil {
			return fmt.Errorf("%w: %v", ErrCredentialUnavailable, err)
		}
	}
	return nil
}

func (manager *Manager) AccessToken(ctx context.Context, id contract.SubscriptionAccountID) (accountauth.AccountTokens, error) {
	account, err := manager.accounts.GetAccount(ctx, id)
	if err != nil {
		return accountauth.AccountTokens{}, err
	}
	if account.Status != contract.SubscriptionStatusConnected {
		return accountauth.AccountTokens{}, fmt.Errorf("subscription account is not connected")
	}
	return manager.tokens.AccessToken(ctx, id)
}

func (manager *Manager) Provider() *CodexProvider { return manager.provider }

func (manager *Manager) APIBaseURL() string { return manager.provider.APIBaseURL() }

func (manager *Manager) persistAuthorizedTokens(ctx context.Context, session contract.AuthorizationSession, tokens accountauth.AccountTokens) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	if session.ServiceID == "" {
		return fmt.Errorf("authorization session is missing service_id")
	}
	account, err := manager.accounts.GetAccount(ctx, session.ServiceID)
	if err != nil {
		return err
	}
	accounts, err := manager.accounts.ListAccounts(ctx)
	if err != nil {
		return err
	}
	for _, existing := range accounts {
		if existing.ID != session.ServiceID && tokens.AccountID != "" && existing.ProviderAccountID == tokens.AccountID {
			return fmt.Errorf("%w: service %q", ErrAccountAlreadyConnected, existing.ID)
		}
	}
	now := manager.now().UTC()
	if err := manager.credentials.Put(ctx, account.ID, tokens); err != nil {
		return err
	}
	expires := tokens.ExpiresAt.UTC()
	account.Status = contract.SubscriptionStatusConnected
	account.CredentialRef = accountauth.CredentialRefFor(account.ID)
	account.AccountHint = maskAccountHint(tokens.AccountID)
	account.ProviderAccountID = tokens.AccountID
	account.TokenExpiresAt = &expires
	account.LastRefreshAt = &now
	account.LastError = nil
	account.AuthorizationBoundary = ""
	account.UpdatedAt = now
	if err := account.Validate(); err != nil {
		_ = manager.credentials.Delete(ctx, account.ID)
		return err
	}
	if err := manager.accounts.PutAccount(ctx, account); err != nil {
		_ = manager.credentials.Delete(ctx, account.ID)
		return err
	}
	return nil
}

func (manager *Manager) onTokenRotated(ctx context.Context, id contract.SubscriptionAccountID, tokens accountauth.AccountTokens) error {
	account, err := manager.accounts.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	now := manager.now().UTC()
	expires := tokens.ExpiresAt.UTC()
	account.TokenExpiresAt = &expires
	account.LastRefreshAt = &now
	account.Status = contract.SubscriptionStatusConnected
	account.LastError = nil
	account.UpdatedAt = now
	return manager.accounts.PutAccount(ctx, account)
}

func (manager *Manager) onInvalidGrant(ctx context.Context, id contract.SubscriptionAccountID, cause error) error {
	account, err := manager.accounts.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	account.Status = contract.SubscriptionStatusNeedsReauth
	account.LastError = &contract.SubscriptionError{
		Code:    accountauth.ErrCodeInvalidGrant,
		Message: "subscription refresh failed; sign in again",
	}
	account.UpdatedAt = manager.now().UTC()
	_ = cause
	return manager.accounts.PutAccount(ctx, account)
}

func (manager *Manager) publicAccount(account contract.SubscriptionAccount) contract.SubscriptionAccount {
	account.AuthorizationBoundary = manager.AuthorizationBoundary()
	return account
}

func randomSubscriptionAccountID() (contract.SubscriptionAccountID, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return contract.ServiceID("service_" + hex.EncodeToString(raw[:])), nil
}

func maskAccountHint(accountID string) string {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return ""
	}
	if len(accountID) <= 8 {
		return accountID[:1] + "***"
	}
	return accountID[:4] + "***" + accountID[len(accountID)-2:]
}
