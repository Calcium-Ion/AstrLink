package endpoint

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/routinggraph"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type RoutingGraphPlan struct {
	Graph       contract.RoutingGraph
	Entry       contract.RoutingGraphNode
	Revision    int64
	Candidates  []Resolved
	Facts       routinggraph.Facts
	MaxAttempts int
}

type RoutingGraphResolver interface {
	ResolveRoutingGraph(context.Context, ResolveRequest) (*RoutingGraphPlan, error)
	ListRoutingGraphModels(context.Context) ([]string, error)
}

func (resolver *StoreResolver) ResolveRoutingGraph(ctx context.Context, request ResolveRequest) (*RoutingGraphPlan, error) {
	store, ok := resolver.reader.(storage.RoutingGraphStore)
	if !ok {
		return nil, nil
	}
	graph, revision, err := store.GetActiveRoutingGraph(ctx)
	if err != nil {
		return nil, fmt.Errorf("read routing graph: %w", err)
	}
	entry, found := graph.Entry(request.Model)
	if !found {
		return nil, nil
	}
	return resolver.PrepareRoutingGraph(ctx, graph, entry, revision, request)
}

func (resolver *StoreResolver) PrepareRoutingGraph(ctx context.Context, graph contract.RoutingGraph, entry contract.RoutingGraphNode, revision int64, request ResolveRequest) (*RoutingGraphPlan, error) {
	services, err := resolver.readEnabledServices(ctx)
	if err != nil {
		return nil, err
	}
	settings := contract.DefaultRoutingSettings()
	if resolver.routingSettings != nil {
		settings, err = resolver.routingSettings.GetRoutingSettings(ctx)
		if err != nil {
			return nil, err
		}
	}
	limit := settings.MaxAttempts
	if entry.MaxAttempts > 0 {
		limit = entry.MaxAttempts
	}
	plan := &RoutingGraphPlan{Graph: graph, Entry: entry, Revision: revision, MaxAttempts: limit, Candidates: []Resolved{}, Facts: routinggraph.Facts{Model: request.Model, Protocol: string(request.Protocol), Streaming: request.Streaming, Now: time.Now()}}
	if quota, ok := resolver.reader.(storage.RoutingQuotaReader); ok {
		plan.Facts.Quota, _ = quota.GetRoutingQuota(ctx)
	}
	reachable := graph.Reachable(entry.ID)
	for _, node := range graph.Nodes {
		if node.Kind != "call" || !reachable[node.ID] {
			continue
		}
		candidate := Resolved{Service: contract.Service{ID: node.ServiceID}, RequestedModel: request.Model, UpstreamModel: node.UpstreamModel, GraphNodeID: node.ID, Unavailable: "target_unavailable"}
		for _, service := range services {
			if service.ID != node.ServiceID {
				continue
			}
			effective := request
			effective.Model = node.UpstreamModel
			resolved := defaultCandidates([]contract.Service{service}, effective, resolver.subscriptionBaseURL, resolver.runtime)
			if len(resolved) > 0 {
				candidate = resolved[0]
				candidate.RequestedModel = request.Model
				candidate.UpstreamModel = node.UpstreamModel
				candidate.GraphNodeID = node.ID
			} else {
				candidate.Service = service
				candidate.Unavailable = "missing_protocol_capability"
			}
			break
		}
		policy := settings.DefaultFailurePolicy
		if candidate.CanonicalService().FailurePolicy != nil {
			policy = *candidate.CanonicalService().FailurePolicy
		}
		if node.FailurePolicy != nil {
			policy = *node.FailurePolicy
		}
		// Ordinary graph calls try once unless this node explicitly enables retry.
		if node.FailurePolicy == nil {
			policy.MaxRetries = 0
		}
		candidate.FailurePolicy = &policy
		candidate.Failover = &contract.FailoverPolicy{Enabled: true, Strategy: contract.RetryFirst, MaxAttempts: limit}
		if !node.Enabled {
			candidate.Unavailable = "node_disabled"
		} else if candidate.Unavailable == "" && !resolver.breaker.available(candidate) {
			candidate.Unavailable = "circuit_open"
		}
		plan.Candidates = append(plan.Candidates, candidate)
	}
	return plan, nil
}

func (resolver *StoreResolver) ListRoutingGraphModels(ctx context.Context) ([]string, error) {
	store, ok := resolver.reader.(storage.RoutingGraphStore)
	if !ok {
		return nil, nil
	}
	graph, _, err := store.GetActiveRoutingGraph(ctx)
	if err != nil {
		return nil, err
	}
	models := []string{}
	for _, node := range graph.Nodes {
		if node.Kind == "entry" && node.Enabled {
			models = append(models, node.Model)
		}
	}
	return models, nil
}
