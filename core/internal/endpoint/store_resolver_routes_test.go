package endpoint

import (
	"context"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type resolverStore struct {
	endpoints []contract.Endpoint
	routes    []contract.Route
	routeErr  error
}

func (store resolverStore) ListEndpoints(context.Context, storage.EndpointListOptions) (storage.EndpointPage, error) {
	items := make([]storage.EndpointRecord, 0, len(store.endpoints))
	for _, candidate := range store.endpoints {
		items = append(items, storage.EndpointRecord{Endpoint: candidate})
	}
	return storage.EndpointPage{Items: items}, nil
}

func (store resolverStore) ListRoutes(context.Context) ([]storage.RouteRecord, error) {
	if store.routeErr != nil {
		return nil, store.routeErr
	}
	items := make([]storage.RouteRecord, 0, len(store.routes))
	for _, route := range store.routes {
		items = append(items, storage.RouteRecord{Route: route})
	}
	return items, nil
}

func resolverRoute(
	id contract.RouteID,
	priority int,
	model string,
	targets ...contract.RouteTarget,
) contract.Route {
	return contract.Route{
		ID:       id,
		Name:     string(id),
		Enabled:  true,
		Priority: priority,
		Match: contract.RouteMatch{
			Protocol: contract.ProtocolOpenAIResponses,
			Model:    model,
		},
		Targets: targets,
	}
}

func resolverTarget(
	id contract.ServiceID,
	planType contract.PlanType,
	priority int,
) contract.RouteTarget {
	return contract.RouteTarget{
		ServiceID:        id,
		PlanType:         planType,
		UpstreamProtocol: contract.ProtocolOpenAIResponses,
		Priority:         priority,
	}
}
