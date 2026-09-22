package subscription

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

const authorizationBoundary = "Logout is local-only: AstrLink removes locally stored credentials; remote revocation is unavailable."

type authorizationAttempt struct {
	id        uint64
	sessionID contract.AuthorizationSessionID
	starting  bool
	completed bool
}

// Manager owns subscription lifecycle and isolates each provider's OAuth client.
type Manager struct {
	resolveProxy            func(context.Context, contract.ServiceID) (context.Context, error)
	usageObserver           func(context.Context, contract.SubscriptionAccount, contract.SubscriptionUsage) error
	resetObserver           func(context.Context, contract.SubscriptionAccount) error
	mu                      sync.Mutex
	accounts                AccountStore
	credentials             accountauth.AccountCredentialStore
	sessions                *accountauth.SessionManager
	tokens                  *accountauth.TokenSource
	claudeSessions          *accountauth.SessionManager
	claudeTokens            *accountauth.TokenSource
	claudeConfig            accountauth.OAuthConfig
	grokSessions            *accountauth.SessionManager
	grokTokens              *accountauth.TokenSource
	grokConfig              accountauth.OAuthConfig
	provider                *CodexProvider
	now                     func() time.Time
	newID                   func() (contract.SubscriptionAccountID, error)
	nextAuthorizationID     uint64
	authorizationAttempts   map[contract.ServiceID]authorizationAttempt
	pendingProviderAccounts map[string]contract.SubscriptionAccountID
	lifecycleTransitions    map[contract.ServiceID]bool
	usageCache              map[contract.ServiceID]usageCacheEntry
}

const usageCacheTTL = 30 * time.Second

type usageCacheEntry struct {
	usage contract.SubscriptionUsage
	until time.Time
}

// NewManager wires one OAuth client per provider. oauth configures Codex.
// overrides customize the other providers: an override whose Provider is set
// applies to that provider; unlabeled overrides apply positionally to Claude
// Code and then xAI Grok. Unspecified providers use their public defaults
// while sharing oauth's HTTP client and clock.
func NewManager(accounts AccountStore, credentials accountauth.AccountCredentialStore, oauth accountauth.OAuthConfig, overrides ...accountauth.OAuthConfig) (*Manager, error) {
	if accounts == nil {
		return nil, fmt.Errorf("subscription account store is required")
	}
	if credentials == nil {
		return nil, fmt.Errorf("account credential store is required")
	}
	oauth = oauth.Normalize()
	now := oauth.Now
	manager := &Manager{
		resolveProxy:            oauth.ResolveProxy,
		accounts:                accounts,
		credentials:             credentials,
		now:                     now,
		newID:                   randomSubscriptionAccountID,
		provider:                NewCodexProvider(oauth),
		authorizationAttempts:   make(map[contract.ServiceID]authorizationAttempt),
		pendingProviderAccounts: make(map[string]contract.SubscriptionAccountID),
		lifecycleTransitions:    make(map[contract.ServiceID]bool),
		usageCache:              make(map[contract.ServiceID]usageCacheEntry),
	}
	manager.sessions = accountauth.NewSessionManager(oauth, credentials, manager.persistAuthorizedTokens)
	tokenClient := accountauth.NewTokenClient(oauth)
	manager.tokens = accountauth.NewTokenSource(credentials, tokenClient, oauth.RefreshSkew, now)
	manager.tokens.SetHooks(manager.onTokenRotated, manager.onInvalidGrant)
	claude := providerOverride(overrides, contract.SubscriptionProviderClaudeCode, 0, oauth)
	manager.claudeConfig = claude
	manager.claudeSessions = accountauth.NewSessionManager(claude, credentials, manager.persistAuthorizedTokens)
	manager.claudeTokens = accountauth.NewTokenSource(credentials, accountauth.NewTokenClient(claude), claude.RefreshSkew, now)
	manager.claudeTokens.SetHooks(manager.onTokenRotated, manager.onInvalidGrant)
	grok := providerOverride(overrides, contract.SubscriptionProviderXAIGrok, 1, oauth)
	manager.grokConfig = grok
	manager.grokSessions = accountauth.NewSessionManager(grok, credentials, manager.persistAuthorizedTokens)
	manager.grokTokens = accountauth.NewTokenSource(credentials, accountauth.NewTokenClient(grok), grok.RefreshSkew, now)
	manager.grokTokens.SetHooks(manager.onTokenRotated, manager.onInvalidGrant)
	return manager, nil
}

func providerOverride(overrides []accountauth.OAuthConfig, provider contract.SubscriptionProvider, position int, base accountauth.OAuthConfig) accountauth.OAuthConfig {
	config := accountauth.OAuthConfig{}
	found := false
	for _, override := range overrides {
		if override.Provider == provider {
			config, found = override, true
			break
		}
	}
	if !found && position < len(overrides) && overrides[position].Provider == "" {
		config = overrides[position]
	}
	if config.HTTPClient == nil {
		config.HTTPClient = base.HTTPClient
	}
	if config.Now == nil {
		config.Now = base.Now
	}
	if config.ResolveProxy == nil {
		config.ResolveProxy = base.ResolveProxy
	}
	config.Provider = provider
	return config.Normalize()
}

func (manager *Manager) sessionsFor(provider contract.SubscriptionProvider) *accountauth.SessionManager {
	switch provider {
	case contract.SubscriptionProviderClaudeCode:
		return manager.claudeSessions
	case contract.SubscriptionProviderXAIGrok:
		return manager.grokSessions
	default:
		return manager.sessions
	}
}

func (manager *Manager) tokensFor(provider contract.SubscriptionProvider) *accountauth.TokenSource {
	switch provider {
	case contract.SubscriptionProviderClaudeCode:
		return manager.claudeTokens
	case contract.SubscriptionProviderXAIGrok:
		return manager.grokTokens
	default:
		return manager.tokens
	}
}

func (manager *Manager) allSessions() []*accountauth.SessionManager {
	return []*accountauth.SessionManager{manager.sessions, manager.claudeSessions, manager.grokSessions}
}

func (manager *Manager) invalidateTokens(id contract.ServiceID) {
	manager.tokens.Invalidate(id)
	manager.claudeTokens.Invalidate(id)
	manager.grokTokens.Invalidate(id)
}

func (manager *Manager) activateTokens(id contract.ServiceID) {
	manager.tokens.Activate(id)
	manager.claudeTokens.Activate(id)
	manager.grokTokens.Activate(id)
}

func (manager *Manager) AuthorizationBoundary() string {
	return authorizationBoundary
}

func (manager *Manager) List(ctx context.Context) ([]contract.SubscriptionAccount, error) {
	manager.mu.Lock()
	items, err := manager.accounts.ListAccounts(ctx)
	manager.mu.Unlock()
	if err != nil {
		return nil, err
	}
	for index := range items {
		items[index] = manager.publicAccount(items[index])
	}
	return items, nil
}

func (manager *Manager) Get(ctx context.Context, id contract.SubscriptionAccountID) (contract.SubscriptionAccount, error) {
	manager.mu.Lock()
	account, err := manager.accounts.GetAccount(ctx, id)
	manager.mu.Unlock()
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
	account, err := manager.Get(ctx, id)
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	sessions := manager.sessionsFor(account.Provider)
	attemptID, err := manager.reserveAuthorizationAttempt(ctx, id)
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	manager.invalidateTokens(id)
	previous, err := manager.markAuthorizing(ctx, id, attemptID)
	if err != nil {
		manager.abortAuthorizationAttempt(
			id,
			attemptID,
			previous.Status == contract.SubscriptionStatusConnected,
		)
		return contract.AuthorizationSession{}, err
	}
	session, err := sessions.Begin(ctx, id, flow)
	if err != nil {
		manager.rollbackAuthorizationAttempt(ctx, id, attemptID, previous)
		if errors.Is(err, accountauth.ErrCredentialStoreUnavailable) {
			return contract.AuthorizationSession{}, fmt.Errorf("%w", ErrCredentialUnavailable)
		}
		return contract.AuthorizationSession{}, err
	}
	if !manager.finishAuthorizationStart(id, attemptID, session.ID) {
		_, _ = sessions.Cancel(ctx, id)
		return contract.AuthorizationSession{}, fmt.Errorf("authorization was interrupted by an account lifecycle change")
	}
	return session, nil
}

func (manager *Manager) GetAuthorization(
	ctx context.Context,
	id contract.ServiceID,
) (contract.AuthorizationSession, bool) {
	var session contract.AuthorizationSession
	ok := false
	for _, sessions := range manager.allSessions() {
		if session, ok = sessions.Get(id); ok {
			break
		}
	}
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
	_, _ = manager.mutateAccount(ctx, id, func(account *contract.SubscriptionAccount) error {
		if attempt, ok := manager.authorizationAttempts[id]; ok && attempt.starting {
			return nil
		}
		if account.Status != contract.SubscriptionStatusAuthorizing {
			return nil
		}
		account.Status = contract.SubscriptionStatusDisconnected
		if account.CredentialRef != "" {
			account.Status = contract.SubscriptionStatusNeedsReauth
		}
		account.LastError = failure
		account.UpdatedAt = manager.now().UTC()
		delete(manager.authorizationAttempts, id)
		return nil
	})
}

func (manager *Manager) CancelAuthorization(ctx context.Context, id contract.ServiceID) (contract.AuthorizationSession, error) {
	account, err := manager.Get(ctx, id)
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	session, err := manager.sessionsFor(account.Provider).Cancel(ctx, id)
	if err != nil {
		return session, err
	}
	_, _ = manager.mutateAccount(ctx, id, func(account *contract.SubscriptionAccount) error {
		delete(manager.authorizationAttempts, id)
		if account.Status != contract.SubscriptionStatusAuthorizing {
			return nil
		}
		account.Status = contract.SubscriptionStatusDisconnected
		if account.CredentialRef != "" {
			account.Status = contract.SubscriptionStatusNeedsReauth
		}
		account.LastError = nil
		account.UpdatedAt = manager.now().UTC()
		return nil
	})
	return session, nil
}

func (manager *Manager) Reconnect(
	ctx context.Context,
	id contract.SubscriptionAccountID,
	flow contract.AuthorizationFlow,
) (contract.AuthorizationSession, error) {
	if _, err := manager.Get(ctx, id); err != nil {
		return contract.AuthorizationSession{}, err
	}
	return manager.BeginAuthorization(ctx, id, flow)
}

func (manager *Manager) Logout(ctx context.Context, id contract.SubscriptionAccountID) (contract.SubscriptionAccount, error) {
	if err := manager.beginLifecycleTransition(ctx, id); err != nil {
		return contract.SubscriptionAccount{}, err
	}
	defer manager.endLifecycleTransition(id)

	for _, sessions := range manager.allSessions() {
		sessions.CancelAllForService(id)
	}
	manager.clearAuthorizationAttempt(id)
	manager.invalidateTokens(id)
	manager.clearUsageCache(id)
	if err := manager.credentials.Delete(ctx, id); err != nil {
		return contract.SubscriptionAccount{}, fmt.Errorf("%w: %v", ErrCredentialUnavailable, err)
	}
	account, err := manager.mutateAccount(ctx, id, func(account *contract.SubscriptionAccount) error {
		delete(manager.authorizationAttempts, id)
		account.Status = contract.SubscriptionStatusDisconnected
		account.CredentialRef = ""
		account.AccountHint = ""
		account.ProviderAccountID = ""
		account.TokenExpiresAt = nil
		account.LastRefreshAt = nil
		account.LastError = nil
		account.UpdatedAt = manager.now().UTC()
		return nil
	})
	if err != nil {
		return contract.SubscriptionAccount{}, err
	}
	return manager.publicAccount(account), nil
}

func (manager *Manager) Delete(ctx context.Context, id contract.ServiceID) error {
	if err := manager.CleanupCredentialsForDelete(ctx, id); err != nil {
		return err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.accounts.DeleteAccount(ctx, id)
}

func (manager *Manager) CleanupCredentialsForDelete(ctx context.Context, id contract.ServiceID) error {
	if err := manager.beginLifecycleTransition(ctx, id); err != nil {
		return err
	}
	defer manager.endLifecycleTransition(id)

	for _, sessions := range manager.allSessions() {
		sessions.CancelAllForService(id)
	}
	manager.clearAuthorizationAttempt(id)
	manager.invalidateTokens(id)
	manager.clearUsageCache(id)
	if err := manager.credentials.Delete(ctx, id); err != nil {
		return fmt.Errorf("%w: %v", ErrCredentialUnavailable, err)
	}
	return nil
}

func (manager *Manager) AccessToken(ctx context.Context, id contract.SubscriptionAccountID) (accountauth.AccountTokens, error) {
	manager.mu.Lock()
	account, err := manager.accounts.GetAccount(ctx, id)
	transitioning := manager.lifecycleTransitions[id]
	manager.mu.Unlock()
	if err != nil {
		return accountauth.AccountTokens{}, err
	}
	if transitioning {
		return accountauth.AccountTokens{}, accountauth.ErrTokenSourceInvalidated
	}
	if account.Status != contract.SubscriptionStatusConnected {
		if account.Status == contract.SubscriptionStatusNeedsReauth {
			return accountauth.AccountTokens{}, fmt.Errorf("subscription account needs reauthorization")
		}
		return accountauth.AccountTokens{}, fmt.Errorf("subscription account is not connected")
	}
	return manager.tokensFor(account.Provider).AccessToken(ctx, id)
}

func (manager *Manager) Usage(ctx context.Context, id contract.ServiceID) (contract.SubscriptionUsage, error) {
	ctx, proxyErr := manager.ProxyContext(ctx, id)
	if proxyErr != nil {
		return contract.SubscriptionUsage{}, proxyErr
	}
	now := manager.now().UTC()
	manager.mu.Lock()
	if entry, ok := manager.usageCache[id]; ok && now.Before(entry.until) {
		usage := entry.usage
		manager.mu.Unlock()
		return usage, nil
	}
	manager.mu.Unlock()

	tokens, err := manager.AccessToken(ctx, id)
	if err != nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w", ErrNotConnected)
	}
	account, err := manager.Get(ctx, id)
	if err != nil {
		return contract.SubscriptionUsage{}, err
	}
	var usage contract.SubscriptionUsage
	switch account.Provider {
	case contract.SubscriptionProviderClaudeCode:
		usage, err = manager.claudeUsage(ctx, tokens)
	case contract.SubscriptionProviderXAIGrok:
		usage, err = manager.grokUsage(ctx, tokens)
	default:
		usage, err = manager.provider.Usage(ctx, tokens)
	}
	if err != nil {
		return contract.SubscriptionUsage{}, usageError(account.Provider, err)
	}
	usage.ServiceID = id
	usage.FetchedAt = now
	if err := usage.Validate(); err != nil {
		return contract.SubscriptionUsage{}, usageError(account.Provider, fmt.Errorf("%w: invalid payload", ErrUsageUnavailable))
	}
	if manager.usageObserver != nil {
		if err := manager.usageObserver(ctx, account, usage); err != nil {
			log.Printf("subscription usage persistence: %v", err)
		}
	}
	manager.mu.Lock()
	manager.usageCache[id] = usageCacheEntry{usage: usage, until: now.Add(usageCacheTTL)}
	manager.mu.Unlock()
	return usage, nil
}

func (manager *Manager) ConsumeReset(ctx context.Context, id contract.ServiceID) (contract.SubscriptionUsageReset, error) {
	ctx, proxyErr := manager.ProxyContext(ctx, id)
	if proxyErr != nil {
		return contract.SubscriptionUsageReset{}, proxyErr
	}
	account, err := manager.Get(ctx, id)
	if err != nil {
		return contract.SubscriptionUsageReset{}, err
	}
	if account.Provider != contract.SubscriptionProviderOpenAICodex {
		return contract.SubscriptionUsageReset{}, fmt.Errorf("%s %w", usageProviderLabel(account.Provider), ErrResetUnavailable)
	}
	tokens, err := manager.AccessToken(ctx, id)
	if err != nil {
		return contract.SubscriptionUsageReset{}, fmt.Errorf("%w", ErrNotConnected)
	}
	redeemID, err := newRedeemRequestID()
	if err != nil {
		return contract.SubscriptionUsageReset{}, err
	}
	result, err := manager.provider.ConsumeReset(ctx, tokens, redeemID)
	if result.Outcome == contract.UsageResetOutcomeReset ||
		result.Outcome == contract.UsageResetOutcomeAlreadyRedeemed {
		manager.clearUsageCache(id)
		if manager.resetObserver != nil && result.Outcome == contract.UsageResetOutcomeReset {
			if e := manager.resetObserver(ctx, account); e != nil {
				log.Printf("subscription reset persistence: %v", e)
			}
		}
	}
	if err != nil {
		if errors.Is(err, ErrResetUnavailable) {
			err = fmt.Errorf("codex %w", err)
		}
		return result, err
	}
	result.ServiceID = id
	if err := result.Validate(); err != nil {
		return contract.SubscriptionUsageReset{}, fmt.Errorf("codex %w: invalid payload", ErrResetUnavailable)
	}
	return result, nil
}

// usageError names the provider that failed ("claude usage unavailable: …")
// so a Claude or Grok lookup is never reported as a Codex one. The result
// still matches ErrUsageUnavailable and keeps timeout causes in the chain.
func usageError(provider contract.SubscriptionProvider, err error) error {
	if !errors.Is(err, ErrUsageUnavailable) {
		err = fmt.Errorf("%w: %w", ErrUsageUnavailable, err)
	}
	return fmt.Errorf("%s %w", usageProviderLabel(provider), err)
}

func usageProviderLabel(provider contract.SubscriptionProvider) string {
	switch provider {
	case contract.SubscriptionProviderClaudeCode:
		return "claude"
	case contract.SubscriptionProviderXAIGrok:
		return "grok"
	default:
		return "codex"
	}
}

func (manager *Manager) clearUsageCache(id contract.ServiceID) {
	manager.mu.Lock()
	delete(manager.usageCache, id)
	manager.mu.Unlock()
}

func (manager *Manager) Provider() *CodexProvider { return manager.provider }

func (manager *Manager) APIBaseURL() string { return manager.provider.APIBaseURL() }

func (manager *Manager) APIBaseURLFor(provider contract.SubscriptionProvider) string {
	switch provider {
	case contract.SubscriptionProviderClaudeCode:
		return manager.claudeConfig.APIBaseURL
	case contract.SubscriptionProviderXAIGrok:
		return manager.grokConfig.APIBaseURL
	default:
		return manager.APIBaseURL()
	}
}

// GrokClientVersion is the Grok CLI version reported on proxy requests.
func (manager *Manager) GrokClientVersion() string {
	return manager.grokConfig.ModelsClientVersion
}

func (manager *Manager) CompleteAuthorizationCode(ctx context.Context, id contract.ServiceID, sessionID contract.AuthorizationSessionID, code string) (contract.AuthorizationSession, error) {
	account, err := manager.Get(ctx, id)
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	return manager.sessionsFor(account.Provider).CompleteCode(ctx, id, sessionID, code)
}

func (manager *Manager) persistAuthorizedTokens(ctx context.Context, session contract.AuthorizationSession, tokens accountauth.AccountTokens) error {
	if session.ServiceID == "" {
		return fmt.Errorf("authorization session is missing service_id")
	}
	if err := manager.reserveProviderAccount(ctx, session, tokens.AccountID); err != nil {
		return err
	}
	reservedProviderID := string(session.Provider) + ":" + tokens.AccountID
	defer manager.releaseProviderAccount(reservedProviderID, session.ServiceID)

	// An explicit authorization replaces any older credential generation. The
	// token-source barrier waits without holding manager.mu, so an in-flight
	// refresh cannot overwrite the newly authorized credentials.
	manager.invalidateTokens(session.ServiceID)
	if err := manager.credentials.Put(ctx, session.ServiceID, tokens); err != nil {
		return err
	}
	_, err := manager.mutateAccount(ctx, session.ServiceID, func(account *contract.SubscriptionAccount) error {
		attempt, ok := manager.authorizationAttempts[session.ServiceID]
		if !ok || (attempt.sessionID != "" && attempt.sessionID != session.ID) ||
			manager.lifecycleTransitions[session.ServiceID] {
			return fmt.Errorf("authorization session was superseded")
		}
		now := manager.now().UTC()
		expires := tokens.ExpiresAt.UTC()
		account.Status = contract.SubscriptionStatusConnected
		account.CredentialRef = accountauth.CredentialRefFor(account.ID)
		account.AccountHint = maskAccountHint(tokens.AccountID)
		account.ProviderAccountID = tokens.AccountID
		account.TokenExpiresAt = &expires
		account.LastRefreshAt = &now
		account.LastError = nil
		account.UpdatedAt = now
		if attempt.starting {
			attempt.completed = true
			attempt.sessionID = session.ID
			manager.authorizationAttempts[session.ServiceID] = attempt
		} else {
			delete(manager.authorizationAttempts, session.ServiceID)
		}
		return nil
	})
	if err != nil {
		_ = manager.credentials.Delete(ctx, session.ServiceID)
		return err
	}
	manager.activateTokens(session.ServiceID)
	return nil
}

func (manager *Manager) onTokenRotated(ctx context.Context, id contract.SubscriptionAccountID, tokens accountauth.AccountTokens) error {
	_, err := manager.mutateAccount(ctx, id, func(account *contract.SubscriptionAccount) error {
		if account.Status != contract.SubscriptionStatusConnected ||
			manager.lifecycleTransitions[id] {
			return nil
		}
		now := manager.now().UTC()
		expires := tokens.ExpiresAt.UTC()
		account.TokenExpiresAt = &expires
		account.LastRefreshAt = &now
		account.LastError = nil
		account.UpdatedAt = now
		return nil
	})
	return err
}

func (manager *Manager) onInvalidGrant(ctx context.Context, id contract.SubscriptionAccountID, cause error) error {
	_ = cause
	_, err := manager.mutateAccount(ctx, id, func(account *contract.SubscriptionAccount) error {
		if account.Status != contract.SubscriptionStatusConnected ||
			manager.lifecycleTransitions[id] {
			return nil
		}
		account.Status = contract.SubscriptionStatusNeedsReauth
		delete(manager.usageCache, id)
		account.LastError = &contract.SubscriptionError{
			Code:    accountauth.ErrCodeInvalidGrant,
			Message: "subscription refresh failed; sign in again",
		}
		account.UpdatedAt = manager.now().UTC()
		return nil
	})
	return err
}

func (manager *Manager) reserveAuthorizationAttempt(
	ctx context.Context,
	id contract.ServiceID,
) (uint64, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.lifecycleTransitions[id] {
		return 0, fmt.Errorf("subscription account lifecycle transition is in progress")
	}
	if _, exists := manager.authorizationAttempts[id]; exists {
		return 0, fmt.Errorf(
			"an authorization session is already active for service %q",
			id,
		)
	}
	if _, err := manager.accounts.GetAccount(ctx, id); err != nil {
		return 0, err
	}
	manager.nextAuthorizationID++
	attemptID := manager.nextAuthorizationID
	manager.authorizationAttempts[id] = authorizationAttempt{
		id:       attemptID,
		starting: true,
	}
	return attemptID, nil
}

func (manager *Manager) markAuthorizing(
	ctx context.Context,
	id contract.ServiceID,
	attemptID uint64,
) (contract.SubscriptionAccount, error) {
	var previous contract.SubscriptionAccount
	_, err := manager.mutateAccount(ctx, id, func(account *contract.SubscriptionAccount) error {
		attempt, ok := manager.authorizationAttempts[id]
		if !ok || attempt.id != attemptID || manager.lifecycleTransitions[id] {
			return fmt.Errorf("authorization was interrupted by an account lifecycle change")
		}
		previous = *account
		account.Status = contract.SubscriptionStatusAuthorizing
		account.LastError = nil
		account.UpdatedAt = manager.now().UTC()
		return nil
	})
	return previous, err
}

func (manager *Manager) rollbackAuthorizationAttempt(
	ctx context.Context,
	id contract.ServiceID,
	attemptID uint64,
	previous contract.SubscriptionAccount,
) {
	manager.mu.Lock()
	attempt, ok := manager.authorizationAttempts[id]
	if ok && attempt.id == attemptID && !manager.lifecycleTransitions[id] {
		delete(manager.authorizationAttempts, id)
		previous.AuthorizationBoundary = manager.AuthorizationBoundary()
		previous.UpdatedAt = manager.now().UTC()
		if manager.accounts.PutAccount(ctx, previous) == nil &&
			previous.Status == contract.SubscriptionStatusConnected {
			manager.activateTokens(id)
		}
	}
	manager.mu.Unlock()
}

func (manager *Manager) abortAuthorizationAttempt(
	id contract.ServiceID,
	attemptID uint64,
	reactivate bool,
) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if attempt, ok := manager.authorizationAttempts[id]; ok && attempt.id == attemptID {
		delete(manager.authorizationAttempts, id)
		if reactivate && !manager.lifecycleTransitions[id] {
			manager.activateTokens(id)
		}
	}
}

func (manager *Manager) finishAuthorizationStart(
	id contract.ServiceID,
	attemptID uint64,
	sessionID contract.AuthorizationSessionID,
) bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	attempt, ok := manager.authorizationAttempts[id]
	if !ok || attempt.id != attemptID || manager.lifecycleTransitions[id] {
		return false
	}
	if attempt.sessionID != "" && attempt.sessionID != sessionID {
		return false
	}
	if attempt.completed {
		delete(manager.authorizationAttempts, id)
		return true
	}
	attempt.sessionID = sessionID
	attempt.starting = false
	manager.authorizationAttempts[id] = attempt
	return true
}

func (manager *Manager) beginLifecycleTransition(ctx context.Context, id contract.ServiceID) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.lifecycleTransitions[id] {
		return fmt.Errorf("subscription account lifecycle transition is already in progress")
	}
	if _, err := manager.accounts.GetAccount(ctx, id); err != nil {
		return err
	}
	manager.lifecycleTransitions[id] = true
	return nil
}

func (manager *Manager) endLifecycleTransition(id contract.ServiceID) {
	manager.mu.Lock()
	delete(manager.lifecycleTransitions, id)
	manager.mu.Unlock()
}

func (manager *Manager) clearAuthorizationAttempt(id contract.ServiceID) {
	manager.mu.Lock()
	delete(manager.authorizationAttempts, id)
	manager.mu.Unlock()
}

func (manager *Manager) reserveProviderAccount(
	ctx context.Context,
	session contract.AuthorizationSession,
	providerAccountID string,
) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.lifecycleTransitions[session.ServiceID] {
		return fmt.Errorf("authorization session was superseded")
	}
	attempt, ok := manager.authorizationAttempts[session.ServiceID]
	if !ok || (attempt.sessionID != "" && attempt.sessionID != session.ID) {
		return fmt.Errorf("authorization session was superseded")
	}
	account, err := manager.accounts.GetAccount(ctx, session.ServiceID)
	if err != nil {
		return err
	}
	if account.Provider != session.Provider {
		return fmt.Errorf("authorization provider does not match service")
	}
	accounts, err := manager.accounts.ListAccounts(ctx)
	if err != nil {
		return err
	}
	for _, existing := range accounts {
		if existing.ID != session.ServiceID && providerAccountID != "" &&
			existing.Provider == session.Provider && existing.ProviderAccountID == providerAccountID {
			return fmt.Errorf("%w: service %q", ErrAccountAlreadyConnected, existing.ID)
		}
	}
	key := string(session.Provider) + ":" + providerAccountID
	if owner, exists := manager.pendingProviderAccounts[key]; providerAccountID != "" &&
		exists && owner != session.ServiceID {
		return fmt.Errorf("%w: service %q", ErrAccountAlreadyConnected, owner)
	}
	if providerAccountID != "" {
		manager.pendingProviderAccounts[key] = session.ServiceID
	}
	return nil
}

func (manager *Manager) releaseProviderAccount(
	providerAccountID string,
	id contract.SubscriptionAccountID,
) {
	if providerAccountID == "" {
		return
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.pendingProviderAccounts[providerAccountID] == id {
		delete(manager.pendingProviderAccounts, providerAccountID)
	}
}

func (manager *Manager) mutateAccount(
	ctx context.Context,
	id contract.SubscriptionAccountID,
	mutate func(*contract.SubscriptionAccount) error,
) (contract.SubscriptionAccount, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	account, err := manager.accounts.GetAccount(ctx, id)
	if err != nil {
		return contract.SubscriptionAccount{}, err
	}
	if err := mutate(&account); err != nil {
		return contract.SubscriptionAccount{}, err
	}
	account.AuthorizationBoundary = manager.AuthorizationBoundary()
	if err := manager.accounts.PutAccount(ctx, account); err != nil {
		return contract.SubscriptionAccount{}, err
	}
	return account, nil
}

func (manager *Manager) publicAccount(account contract.SubscriptionAccount) contract.SubscriptionAccount {
	account.AuthorizationBoundary = manager.AuthorizationBoundary()
	return account
}

func newRedeemRequestID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		raw[0:4],
		raw[4:6],
		raw[6:8],
		raw[8:10],
		raw[10:],
	), nil
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

// SetUsageObservers is configured before the manager starts serving requests.
func (manager *Manager) SetUsageObservers(usage func(context.Context, contract.SubscriptionAccount, contract.SubscriptionUsage) error, reset func(context.Context, contract.SubscriptionAccount) error) {
	manager.usageObserver, manager.resetObserver = usage, reset
}

// RunUsageMonitor collects quota windows even when the service page is closed.
// Per-account failures back off to avoid repeatedly hitting an unavailable API.
func (manager *Manager) RunUsageMonitor(ctx context.Context) {
	next := map[contract.ServiceID]time.Time{}
	failures := map[contract.ServiceID]int{}
	timer := time.NewTicker(time.Minute)
	defer timer.Stop()
	for {
		accounts, err := manager.List(ctx)
		if err == nil {
			for _, account := range accounts {
				if account.Status != contract.SubscriptionStatusConnected || time.Now().Before(next[account.ID]) {
					continue
				}
				child, cancel := context.WithTimeout(ctx, 20*time.Second)
				_, err := manager.Usage(child, account.ID)
				cancel()
				delay := 5 * time.Minute
				if err != nil {
					failures[account.ID]++
					delay *= time.Duration(1 << min(failures[account.ID], 4))
				} else {
					failures[account.ID] = 0
				}
				next[account.ID] = time.Now().Add(delay)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

// ProxyContext binds gateway-initiated provider operations to their account.
func (manager *Manager) ProxyContext(ctx context.Context, id contract.ServiceID) (context.Context, error) {
	if manager.resolveProxy == nil {
		return ctx, nil
	}
	return manager.resolveProxy(ctx, id)
}
