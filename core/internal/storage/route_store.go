package storage

import (
	"context"

	"github.com/QuantumNous/astrlink/core/contract"
)

// RouteRecord couples a validated Route document with its strong entity tag.
// This read-only slice is intentionally smaller than the frozen control-plane
// CRUD contract; routing only needs a strict snapshot of persisted rules.
type RouteRecord struct {
	Route contract.Route
	ETag  string
}

// RouteReader returns the complete persisted route snapshot. Implementations
// must validate every row before returning any route so corrupt configuration
// fails closed rather than silently falling back to automatic routing.
type RouteReader interface {
	ListRoutes(context.Context) ([]RouteRecord, error)
}
