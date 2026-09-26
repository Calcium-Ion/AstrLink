package storage

import (
	"context"

	"github.com/QuantumNous/astrlink/core/contract"
)

// LearnedIdentityStore keeps the client identity last learned from each
// subscription provider's official client. Documents are opaque to storage;
// the identity registry validates them when it loads them.
type LearnedIdentityStore interface {
	GetLearnedIdentity(context.Context, contract.SubscriptionProvider) ([]byte, error)
	ListLearnedIdentities(context.Context) (map[contract.SubscriptionProvider][]byte, error)
	PutLearnedIdentity(context.Context, contract.SubscriptionProvider, []byte) error
}
