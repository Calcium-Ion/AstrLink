package storage

import (
	"context"

	"github.com/QuantumNous/astrlink/core/contract"
)

type PolicyRecord struct {
	Policy contract.Policy
	ETag   string
}

type PolicyPage struct {
	Items []PolicyRecord
}

// PolicyStore is intentionally update-only for the MVP singleton policy.
// Creation and deletion remain absent until policy ordering and references are
// implemented as a complete feature.
type PolicyStore interface {
	GetPolicy(context.Context, contract.PolicyID) (PolicyRecord, error)
	ListPolicies(context.Context) (PolicyPage, error)
	UpdatePolicy(context.Context, contract.Policy, string) (PolicyRecord, error)
}
