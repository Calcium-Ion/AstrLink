package ingress

import (
	"context"
	"fmt"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"sync"
	"time"
)

type affinityEntry struct {
	binding storage.ResponseAffinity
	at      time.Time
}
type affinityKey struct {
	principal  string
	responseID string
}
type responseAffinities struct {
	mu      sync.Mutex
	entries map[affinityKey]affinityEntry
}

func (handler *Handler) rememberResponseAffinity(ctx context.Context, session *recordSession, candidate endpoint.Resolved, plan contract.ExecutionPlan) {
	if session == nil || session.classified.Protocol != contract.ProtocolOpenAIResponses {
		return
	}
	session.captureOutputID()
	if session.outputResponseID == "" {
		return
	}
	principal, _ := AccessTokenIDFromContext(ctx)
	model := candidate.UpstreamModel
	if model == "" {
		model = session.classified.routingModel()
	}
	binding := storage.ResponseAffinity{ServiceID: candidate.CanonicalService().ID, UpstreamModel: model, UpstreamProtocol: plan.UpstreamProtocol, PlanType: plan.Type}
	key := affinityKey{string(principal), session.outputResponseID}
	handler.affinities.mu.Lock()
	if handler.affinities.entries == nil {
		handler.affinities.entries = make(map[affinityKey]affinityEntry)
	}
	for len(handler.affinities.entries) >= 10000 {
		var oldest affinityKey
		var at time.Time
		for key, entry := range handler.affinities.entries {
			if at.IsZero() || entry.at.Before(at) {
				oldest, at = key, entry.at
			}
		}
		delete(handler.affinities.entries, oldest)
	}
	handler.affinities.entries[key] = affinityEntry{binding: binding, at: time.Now()}
	handler.affinities.mu.Unlock()
	if store, ok := handler.requestRecords.(storage.ResponseAffinityStore); ok {
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)
		defer cancel()
		if err := store.PutResponseAffinity(persistCtx, key.principal, key.responseID, binding); err != nil {
			handler.recordLogger("response affinity persistence failed: %v", err)
		}
	}
}

func (handler *Handler) bindResponseAffinity(ctx context.Context, request Request, candidates []endpoint.Resolved) ([]endpoint.Resolved, error) {
	if request.PreviousResponseID == "" || (request.Protocol != contract.ProtocolOpenAIResponses && request.Protocol != contract.ProtocolOpenAIResponsesCompact) {
		return candidates, nil
	}
	principal, _ := AccessTokenIDFromContext(ctx)
	key := affinityKey{string(principal), request.PreviousResponseID}
	handler.affinities.mu.Lock()
	entry, found := handler.affinities.entries[key]
	if found && time.Since(entry.at) >= 24*time.Hour {
		delete(handler.affinities.entries, key)
		found = false
	}
	handler.affinities.mu.Unlock()
	binding := entry.binding
	if !found {
		if store, ok := handler.requestRecords.(storage.ResponseAffinityStore); ok {
			lookupCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			defer cancel()
			var err error
			binding, found, err = store.GetResponseAffinity(lookupCtx, key.principal, key.responseID)
			if err != nil {
				return nil, fmt.Errorf("response affinity could not be loaded")
			}
		}
	}
	var selected *endpoint.Resolved
	if found {
		for _, candidate := range candidates {
			model := candidate.UpstreamModel
			if model == "" {
				model = request.routingModel()
			}
			protocol := candidate.UpstreamProtocol
			if protocol == "" {
				protocol = request.Protocol
			}
			planType := candidate.PlanType
			if planType == "" {
				planType = contract.PlanTypeNative
				if candidate.Mode == contract.CapabilityModeDelegated {
					planType = contract.PlanTypeDelegated
				}
			}
			compatibleProtocol := protocol == binding.UpstreamProtocol || (protocol == contract.ProtocolOpenAIResponsesCompact && binding.UpstreamProtocol == contract.ProtocolOpenAIResponses)
			if candidate.CanonicalService().ID == binding.ServiceID && model == binding.UpstreamModel && compatibleProtocol && planType == binding.PlanType {
				copy := candidate
				selected = &copy
				break
			}
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("the previous response belongs to an unavailable or unknown API provider; use its original API provider or start a new conversation")
	}
	policy := contract.DefaultFailoverPolicy()
	if selected.Failover != nil {
		policy = *selected.Failover
	}
	policy.Enabled = false
	selected.Failover = &policy
	recordSessionFromContext(ctx).noteRoutingPin(contract.RoutingSelectionResponseAffinity, selected.CanonicalService().ID)
	return []endpoint.Resolved{*selected}, nil
}
