package storage

import (
	"context"
	"github.com/QuantumNous/astrlink/core/contract"
)

type RoutingGraphStore interface {
	GetRoutingGraph(context.Context) (contract.RoutingGraphDocument, error)
	GetActiveRoutingGraph(context.Context) (contract.RoutingGraph, int64, error)
	SaveRoutingGraph(context.Context, contract.RoutingGraph, contract.RoutingLayout, string, bool) (contract.RoutingGraphDocument, error)
	GetRoutingGraphRevision(context.Context, int64) (contract.RoutingGraph, error)
}

type RoutingQuotaReader interface {
	GetRoutingQuota(context.Context) (map[contract.ServiceID]contract.SubscriptionUsage, error)
}
