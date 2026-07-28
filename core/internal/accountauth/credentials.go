package accountauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

var (
	ErrCredentialStoreUnavailable = errors.New("account credential store unavailable")
	ErrCredentialNotFound         = errors.New("account credential not found")
)

// AccountTokens is the recoverable OAuth material persisted only in
// AccountCredentialStore. It must never enter control responses or React state.
type AccountTokens struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	TokenType        string    `json:"token_type,omitempty"`
	Scope            string    `json:"scope,omitempty"`
	AccountID        string    `json:"account_id,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
	RawIDTokenClaims string    `json:"-"`
}

func (tokens AccountTokens) Validate() error {
	if strings.TrimSpace(tokens.AccessToken) == "" {
		return fmt.Errorf("access_token is required")
	}
	if strings.TrimSpace(tokens.RefreshToken) == "" {
		return fmt.Errorf("refresh_token is required")
	}
	if tokens.ExpiresAt.IsZero() {
		return fmt.Errorf("expires_at is required")
	}
	return nil
}

func (tokens AccountTokens) MarshalSecret() ([]byte, error) {
	if err := tokens.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(tokens)
}

func UnmarshalAccountTokens(raw []byte) (AccountTokens, error) {
	var tokens AccountTokens
	if err := json.Unmarshal(raw, &tokens); err != nil {
		return AccountTokens{}, fmt.Errorf("decode account tokens: %w", err)
	}
	if err := tokens.Validate(); err != nil {
		return AccountTokens{}, err
	}
	return tokens, nil
}

// AccountCredentialStore persists OAuth tokens outside SQLite endpoint
// credentials. Implementations must fail closed when the secure backend is
// unavailable.
type AccountCredentialStore interface {
	Available(context.Context) error
	Get(context.Context, contract.SubscriptionAccountID) (AccountTokens, error)
	Put(context.Context, contract.SubscriptionAccountID, AccountTokens) error
	Delete(context.Context, contract.SubscriptionAccountID) error
}

func CredentialRefFor(accountID contract.SubscriptionAccountID) string {
	return "keyring://astrlink/subscription/" + string(accountID)
}

// MemoryCredentialStore is a test-only in-process store.
type MemoryCredentialStore struct {
	mu      sync.Mutex
	tokens  map[contract.SubscriptionAccountID]AccountTokens
	failPut bool
}

func NewMemoryCredentialStore() *MemoryCredentialStore {
	return &MemoryCredentialStore{tokens: make(map[contract.SubscriptionAccountID]AccountTokens)}
}

func (store *MemoryCredentialStore) Available(context.Context) error { return nil }

func (store *MemoryCredentialStore) Get(_ context.Context, id contract.SubscriptionAccountID) (AccountTokens, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	tokens, ok := store.tokens[id]
	if !ok {
		return AccountTokens{}, ErrCredentialNotFound
	}
	return tokens, nil
}

func (store *MemoryCredentialStore) Put(_ context.Context, id contract.SubscriptionAccountID, tokens AccountTokens) error {
	if store.failPut {
		return ErrCredentialStoreUnavailable
	}
	if err := tokens.Validate(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.tokens[id] = tokens
	return nil
}

func (store *MemoryCredentialStore) Delete(_ context.Context, id contract.SubscriptionAccountID) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.tokens, id)
	return nil
}

// UnavailableCredentialStore always fails closed.
type UnavailableCredentialStore struct{}

func (UnavailableCredentialStore) Available(context.Context) error {
	return ErrCredentialStoreUnavailable
}

func (UnavailableCredentialStore) Get(context.Context, contract.SubscriptionAccountID) (AccountTokens, error) {
	return AccountTokens{}, ErrCredentialStoreUnavailable
}

func (UnavailableCredentialStore) Put(context.Context, contract.SubscriptionAccountID, AccountTokens) error {
	return ErrCredentialStoreUnavailable
}

func (UnavailableCredentialStore) Delete(context.Context, contract.SubscriptionAccountID) error {
	return ErrCredentialStoreUnavailable
}
