package accountauth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// ErrTokenSourceInvalidated reports that the account generation was retired.
var ErrTokenSourceInvalidated = errors.New("subscription account credentials are inactive")

// TokenSource returns a valid access token, refreshing with singleflight when
// the stored token is inside the refresh skew window or already expired.
type TokenSource struct {
	mu         sync.Mutex
	inflight   map[contract.SubscriptionAccountID]*refreshCall
	lifecycles map[contract.SubscriptionAccountID]tokenLifecycle
	store      AccountCredentialStore
	client     *TokenClient
	skew       time.Duration
	now        func() time.Time
	onRotated  func(context.Context, contract.SubscriptionAccountID, AccountTokens) error
	onInvalid  func(context.Context, contract.SubscriptionAccountID, error) error
}

type tokenLifecycle struct {
	generation  uint64
	invalidated bool
}

type refreshCall struct {
	done       chan struct{}
	generation uint64
	tokens     AccountTokens
	err        error
}

func NewTokenSource(store AccountCredentialStore, client *TokenClient, skew time.Duration, now func() time.Time) *TokenSource {
	if now == nil {
		now = time.Now
	}
	if skew <= 0 {
		skew = DefaultRefreshSkew
	}
	return &TokenSource{
		inflight:   make(map[contract.SubscriptionAccountID]*refreshCall),
		lifecycles: make(map[contract.SubscriptionAccountID]tokenLifecycle),
		store:      store,
		client:     client,
		skew:       skew,
		now:        now,
	}
}

func (source *TokenSource) SetHooks(
	onRotated func(context.Context, contract.SubscriptionAccountID, AccountTokens) error,
	onInvalid func(context.Context, contract.SubscriptionAccountID, error) error,
) {
	source.onRotated = onRotated
	source.onInvalid = onInvalid
}

// Invalidate prevents new token reads or refreshes for accountID. If a refresh
// has already registered, it waits for that refresh to finish without holding
// source.mu. Callers can therefore safely delete the credential after this
// method returns, including when invalidation races with the credential Put.
func (source *TokenSource) Invalidate(accountID contract.SubscriptionAccountID) {
	source.mu.Lock()
	lifecycle := source.lifecycles[accountID]
	lifecycle.generation++
	lifecycle.invalidated = true
	source.lifecycles[accountID] = lifecycle
	call := source.inflight[accountID]
	source.mu.Unlock()

	if call != nil {
		<-call.done
	}
}

// Activate clears the invalidation tombstone after credentials from a
// successful explicit authorization have been persisted. Callers replacing
// credentials must first use Invalidate to quiesce an older refresh.
func (source *TokenSource) Activate(accountID contract.SubscriptionAccountID) {
	source.mu.Lock()
	defer source.mu.Unlock()
	lifecycle := source.lifecycles[accountID]
	lifecycle.generation++
	lifecycle.invalidated = false
	source.lifecycles[accountID] = lifecycle
}

func (source *TokenSource) AccessToken(ctx context.Context, accountID contract.SubscriptionAccountID) (AccountTokens, error) {
	if source.client.config.ResolveProxy != nil {
		var err error
		ctx, err = source.client.config.ResolveProxy(ctx, accountID)
		if err != nil {
			return AccountTokens{}, err
		}
	}
	if source.invalidated(accountID) {
		return AccountTokens{}, ErrTokenSourceInvalidated
	}
	tokens, err := source.store.Get(ctx, accountID)
	if err != nil {
		return AccountTokens{}, err
	}
	if source.now().Add(source.skew).Before(tokens.ExpiresAt) {
		if source.invalidated(accountID) {
			return AccountTokens{}, ErrTokenSourceInvalidated
		}
		return tokens, nil
	}
	return source.refresh(ctx, accountID, tokens)
}

// RefreshRejected refreshes accountID after the provider rejected
// rejectedAccessToken. Callers holding the same rejected token share one
// refresh, and nothing is refreshed once the stored token has already rotated.
func (source *TokenSource) RefreshRejected(ctx context.Context, accountID contract.SubscriptionAccountID, rejectedAccessToken string) (bool, error) {
	if source.client.config.ResolveProxy != nil {
		var err error
		ctx, err = source.client.config.ResolveProxy(ctx, accountID)
		if err != nil {
			return false, err
		}
	}
	if source.invalidated(accountID) {
		return false, ErrTokenSourceInvalidated
	}
	tokens, err := source.store.Get(ctx, accountID)
	if err != nil {
		return false, err
	}
	if rejectedAccessToken == "" || tokens.AccessToken != rejectedAccessToken {
		return false, nil
	}
	if _, err := source.refresh(ctx, accountID, tokens); err != nil {
		return false, err
	}
	return true, nil
}

func (source *TokenSource) refresh(ctx context.Context, accountID contract.SubscriptionAccountID, current AccountTokens) (AccountTokens, error) {
	source.mu.Lock()
	lifecycle := source.lifecycles[accountID]
	if lifecycle.invalidated {
		source.mu.Unlock()
		return AccountTokens{}, ErrTokenSourceInvalidated
	}
	if call, ok := source.inflight[accountID]; ok {
		source.mu.Unlock()
		return waitForRefresh(ctx, call)
	}
	call := &refreshCall{
		done:       make(chan struct{}),
		generation: lifecycle.generation,
	}
	source.inflight[accountID] = call
	source.mu.Unlock()

	refreshed, err := source.client.Refresh(ctx, current.RefreshToken)
	if err != nil {
		if errors.Is(err, ErrInvalidGrant) {
			source.markInvalidated(accountID)
			if source.onInvalid != nil {
				_ = source.onInvalid(ctx, accountID, err)
			}
		}
		return source.finishRefresh(accountID, call, AccountTokens{}, err)
	}
	if !source.refreshCanPersist(accountID, call) {
		return source.finishRefresh(
			accountID,
			call,
			AccountTokens{},
			ErrTokenSourceInvalidated,
		)
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = current.RefreshToken
	}
	if refreshed.AccountID == "" {
		refreshed.AccountID = current.AccountID
	}
	if err := source.store.Put(ctx, accountID, refreshed); err != nil {
		return source.finishRefresh(accountID, call, AccountTokens{}, err)
	}
	if source.onRotated != nil {
		if err := source.onRotated(ctx, accountID, refreshed); err != nil {
			return source.finishRefresh(
				accountID,
				call,
				AccountTokens{},
				fmt.Errorf("persist rotated token metadata: %w", err),
			)
		}
	}
	return source.finishRefresh(accountID, call, refreshed, nil)
}

func (source *TokenSource) invalidated(accountID contract.SubscriptionAccountID) bool {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.lifecycles[accountID].invalidated
}

func (source *TokenSource) markInvalidated(accountID contract.SubscriptionAccountID) {
	source.mu.Lock()
	defer source.mu.Unlock()
	lifecycle := source.lifecycles[accountID]
	lifecycle.generation++
	lifecycle.invalidated = true
	source.lifecycles[accountID] = lifecycle
}

func (source *TokenSource) refreshCanPersist(
	accountID contract.SubscriptionAccountID,
	call *refreshCall,
) bool {
	source.mu.Lock()
	defer source.mu.Unlock()
	lifecycle := source.lifecycles[accountID]
	return !lifecycle.invalidated &&
		lifecycle.generation == call.generation &&
		source.inflight[accountID] == call
}

func (source *TokenSource) finishRefresh(
	accountID contract.SubscriptionAccountID,
	call *refreshCall,
	tokens AccountTokens,
	err error,
) (AccountTokens, error) {
	source.mu.Lock()
	lifecycle := source.lifecycles[accountID]
	if lifecycle.invalidated || lifecycle.generation != call.generation {
		tokens = AccountTokens{}
		if err == nil {
			err = ErrTokenSourceInvalidated
		}
	}
	if err != nil {
		tokens = AccountTokens{}
	}
	call.tokens = tokens
	call.err = err
	if source.inflight[accountID] == call {
		delete(source.inflight, accountID)
	}
	close(call.done)
	source.mu.Unlock()

	if err != nil {
		return AccountTokens{}, err
	}
	return tokens, nil
}

func waitForRefresh(ctx context.Context, call *refreshCall) (AccountTokens, error) {
	select {
	case <-ctx.Done():
		return AccountTokens{}, ctx.Err()
	case <-call.done:
		return call.tokens, call.err
	}
}
