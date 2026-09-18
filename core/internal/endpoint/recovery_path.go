package endpoint

import (
	"context"
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// RecoveryPathSnapshot is immutable for the lifetime of a client request.
type RecoveryPathSnapshot struct {
	ID                          contract.RecoveryPathID
	Name, Version, Mode, StepID string
}

func (resolver *StoreResolver) ResolvePath(ctx context.Context, record contract.RecoveryPathRecord, request ResolveRequest, route *contract.Route, settings contract.RoutingSettings) ([]Resolved, error) {
	services, err := resolver.readEnabledServices(ctx)
	if err != nil {
		return nil, err
	}
	return resolver.pathCandidates(record, request, route, settings, services)
}
func (resolver *StoreResolver) pathCandidates(record contract.RecoveryPathRecord, request ResolveRequest, route *contract.Route, settings contract.RoutingSettings, services []contract.Service) ([]Resolved, error) {
	path := record.Path
	if path.Protocol != request.Protocol {
		return nil, fmt.Errorf("recovery path protocol mismatch")
	}
	failover := settings.FailoverPolicy()
	failover.Enabled = route != nil
	if path.Strategy != nil {
		failover.Strategy = *path.Strategy
	}
	if path.MaxAttempts != nil {
		failover.MaxAttempts = *path.MaxAttempts
	}
	if route != nil && route.Failover != nil {
		failover = *route.Failover
	}
	identities := map[string]bool{}
	serviceIDs := map[contract.ServiceID]bool{}
	for _, node := range path.Nodes() {
		identities[node.Key()] = true
		serviceIDs[node.ServiceID] = true
	}
	candidates := make([]Resolved, 0, len(path.Nodes()))
	for i, node := range path.Nodes() {
		local := contract.Route{ID: "path_resolution", Name: path.Name, Enabled: true, Match: contract.RouteMatch{Protocol: request.Protocol, Model: request.Model}, Targets: []contract.RouteTarget{node.Target(i)}}
		resolved := routeCandidates(local, services, request, resolver.runtime, resolver.subscriptionBaseURL)
		candidate := Resolved{Service: contract.Service{ID: node.ServiceID}, UpstreamModel: node.UpstreamModel, RequestedModel: request.Model, PlanType: node.PlanType, UpstreamProtocol: node.UpstreamProtocol, Unavailable: "target_unavailable"}
		if len(resolved) > 0 {
			candidate = resolved[0]
		}
		candidate.RouteID = ""
		if route != nil {
			candidate.RouteID = route.ID
		}
		candidate.SingleTargetRoute = route != nil && len(identities) == 1
		candidate.Pinned = len(serviceIDs) == 1 && route != nil
		candidate.Path = &RecoveryPathSnapshot{ID: path.ID, Name: path.Name, Version: record.ETag, Mode: path.Mode, StepID: node.ID}
		policy := settings.DefaultFailurePolicy
		if candidate.CanonicalService().FailurePolicy != nil {
			policy = *candidate.CanonicalService().FailurePolicy
		}
		if path.FailurePolicy != nil {
			policy = *path.FailurePolicy
		}
		if node.MaxRetries != nil {
			policy.MaxRetries = *node.MaxRetries
		}
		if route != nil && route.FailurePolicy != nil {
			policy = *route.FailurePolicy
		}
		candidate.FailurePolicy = &policy
		candidate.Failover = &failover
		if candidate.Unavailable == "" && !resolver.breaker.available(candidate) {
			candidate.Unavailable = "circuit_open"
		}
		candidates = append(candidates, candidate)
	}
	if !failover.Enabled && path.Mode == "automatic" && !request.Continuation {
		candidates = candidates[:1]
	}
	return candidates, nil
}
func (resolver *StoreResolver) selectedRecoveryPath(ctx context.Context, route contract.Route, request ResolveRequest) (*contract.RecoveryPathRecord, error) {
	id := route.RecoveryPathID
	for _, category := range route.Categories {
		if category.CategoryID == request.Category {
			id = category.RecoveryPathID
			break
		}
	}
	if id == "" {
		return nil, nil
	}
	if resolver.paths == nil {
		return nil, storage.ErrNotFound
	}
	record, err := resolver.paths.GetRecoveryPath(ctx, id)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// Expand aliases for discovery without changing the persisted route.
func (resolver *StoreResolver) pathAliasTargets(ctx context.Context, route contract.Route) ([]contract.RouteTarget, error) {
	targets := route.ExecutableTargets()
	ids := []contract.RecoveryPathID{route.RecoveryPathID}
	for _, category := range route.Categories {
		ids = append(ids, category.RecoveryPathID)
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if resolver.paths == nil {
			return nil, storage.ErrNotFound
		}
		record, err := resolver.paths.GetRecoveryPath(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, node := range record.Path.Nodes() {
			targets = append(targets, node.Target(len(targets)))
		}
	}
	return targets, nil
}
