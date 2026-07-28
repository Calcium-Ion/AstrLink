package storage

import (
	"context"

	"github.com/QuantumNous/astrlink/core/contract"
)

// SubscriptionAccountStore persists non-sensitive SubscriptionAccount JSON.
// OAuth tokens never enter this store.
type SubscriptionAccountStore interface {
	ListSubscriptionAccounts(context.Context) ([]contract.SubscriptionAccount, error)
	GetSubscriptionAccount(context.Context, contract.SubscriptionAccountID) (contract.SubscriptionAccount, error)
	PutSubscriptionAccount(context.Context, contract.SubscriptionAccount) error
	DeleteSubscriptionAccount(context.Context, contract.SubscriptionAccountID) error
}
