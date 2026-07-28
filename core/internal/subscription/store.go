package subscription

import (
	"context"

	"github.com/QuantumNous/astrlink/core/contract"
)

// AccountStore persists non-sensitive SubscriptionAccount documents.
type AccountStore interface {
	ListAccounts(context.Context) ([]contract.SubscriptionAccount, error)
	GetAccount(context.Context, contract.SubscriptionAccountID) (contract.SubscriptionAccount, error)
	PutAccount(context.Context, contract.SubscriptionAccount) error
	DeleteAccount(context.Context, contract.SubscriptionAccountID) error
}

// MemoryAccountStore is an in-process store for tests and headless wiring.
type MemoryAccountStore struct {
	accounts map[contract.SubscriptionAccountID]contract.SubscriptionAccount
}

func NewMemoryAccountStore() *MemoryAccountStore {
	return &MemoryAccountStore{accounts: make(map[contract.SubscriptionAccountID]contract.SubscriptionAccount)}
}

func (store *MemoryAccountStore) ListAccounts(context.Context) ([]contract.SubscriptionAccount, error) {
	items := make([]contract.SubscriptionAccount, 0, len(store.accounts))
	for _, account := range store.accounts {
		items = append(items, account)
	}
	return items, nil
}

func (store *MemoryAccountStore) GetAccount(_ context.Context, id contract.SubscriptionAccountID) (contract.SubscriptionAccount, error) {
	account, ok := store.accounts[id]
	if !ok {
		return contract.SubscriptionAccount{}, ErrNotFound
	}
	return account, nil
}

func (store *MemoryAccountStore) PutAccount(_ context.Context, account contract.SubscriptionAccount) error {
	if err := account.Validate(); err != nil {
		return err
	}
	store.accounts[account.ID] = account
	return nil
}

func (store *MemoryAccountStore) DeleteAccount(_ context.Context, id contract.SubscriptionAccountID) error {
	delete(store.accounts, id)
	return nil
}
