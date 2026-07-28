package storage

import (
	"context"

	"github.com/QuantumNous/astrlink/core/contract"
)

// RouteRecord couples a validated Route document with its strong entity tag.
type RouteRecord struct {
	Route contract.Route
	ETag  string
}

type RouteListOptions struct {
	Limit   int
	Cursor  string
	Enabled *bool
}

type RoutePage struct {
	Items      []RouteRecord
	NextCursor string
}

// RouteReader returns the complete persisted route snapshot. Implementations
// must validate every row before returning any route so corrupt configuration
// fails closed rather than silently falling back to automatic routing.
type RouteReader interface {
	ListRoutes(context.Context) ([]RouteRecord, error)
}

// RouteStore owns authenticated control-plane CRUD in addition to the strict
// complete snapshot consumed by routing. Create and update implementations
// must reject routes that the current runtime cannot execute.
type RouteStore interface {
	RouteReader
	CreateRoute(context.Context, contract.Route) (RouteRecord, error)
	GetRoute(context.Context, contract.RouteID) (RouteRecord, error)
	ListRoutePage(context.Context, RouteListOptions) (RoutePage, error)
	UpdateRoute(context.Context, contract.Route, string) (RouteRecord, error)
	DeleteRoute(context.Context, contract.RouteID, string) error
}
