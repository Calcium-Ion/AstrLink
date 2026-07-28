package accountauth

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// TokenSource returns a valid access token, refreshing with singleflight when
// the stored token is inside the refresh skew window or already expired.
type TokenSource struct {
	mu        sync.Mutex
	inflight  map[contract.SubscriptionAccountID]*refreshCall
	store     AccountCredentialStore
	client    *TokenClient
	skew      time.Duration
	now       func() time.Time
	onRotated func(context.Context, contract.SubscriptionAccountID, AccountTokens) error
	onInvalid func(context.Context, contract.SubscriptionAccountID, error) error
}

type refreshCall struct {
	done   chan struct{}
	tokens AccountTokens
	err    error
}

func NewTokenSource(store AccountCredentialStore, client *TokenClient, skew time.Duration, now func() time.Time) *TokenSource {
	if now == nil {
		now = time.Now
	}
	if skew <= 0 {
		skew = DefaultRefreshSkew
	}
	return &TokenSource{
		inflight: make(map[contract.SubscriptionAccountID]*refreshCall),
		store:    store,
		client:   client,
		skew:     skew,
		now:      now,
	}
}

func (source *TokenSource) SetHooks(
	onRotated func(context.Context, contract.SubscriptionAccountID, AccountTokens) error,
	onInvalid func(context.Context, contract.SubscriptionAccountID, error) error,
) {
	source.onRotated = onRotated
	source.onInvalid = onInvalid
}

func (source *TokenSource) AccessToken(ctx context.Context, accountID contract.SubscriptionAccountID) (AccountTokens, error) {
	tokens, err := source.store.Get(ctx, accountID)
	if err != nil {
		return AccountTokens{}, err
	}
	if source.now().Add(source.skew).Before(tokens.ExpiresAt) {
		return tokens, nil
	}
	return source.refresh(ctx, accountID, tokens)
}

func (source *TokenSource) refresh(ctx context.Context, accountID contract.SubscriptionAccountID, current AccountTokens) (AccountTokens, error) {
	source.mu.Lock()
	if call, ok := source.inflight[accountID]; ok {
		source.mu.Unlock()
		select {
		case <-ctx.Done():
			return AccountTokens{}, ctx.Err()
		case <-call.done:
			return call.tokens, call.err
		}
	}
	call := &refreshCall{done: make(chan struct{})}
	source.inflight[accountID] = call
	source.mu.Unlock()

	refreshed, err := source.client.Refresh(ctx, current.RefreshToken)
	if err != nil {
		if err == ErrInvalidGrant && source.onInvalid != nil {
			_ = source.onInvalid(ctx, accountID, err)
		}
		call.err = err
		close(call.done)
		source.mu.Lock()
		delete(source.inflight, accountID)
		source.mu.Unlock()
		return AccountTokens{}, err
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = current.RefreshToken
	}
	if refreshed.AccountID == "" {
		refreshed.AccountID = current.AccountID
	}
	if err := source.store.Put(ctx, accountID, refreshed); err != nil {
		call.err = err
		close(call.done)
		source.mu.Lock()
		delete(source.inflight, accountID)
		source.mu.Unlock()
		return AccountTokens{}, err
	}
	if source.onRotated != nil {
		if err := source.onRotated(ctx, accountID, refreshed); err != nil {
			call.err = err
			close(call.done)
			source.mu.Lock()
			delete(source.inflight, accountID)
			source.mu.Unlock()
			return AccountTokens{}, fmt.Errorf("persist rotated token metadata: %w", err)
		}
	}
	call.tokens = refreshed
	close(call.done)
	source.mu.Lock()
	delete(source.inflight, accountID)
	source.mu.Unlock()
	return refreshed, nil
}
