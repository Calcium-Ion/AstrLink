package endpoint

import (
	"context"
	"fmt"
	"sort"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type endpointReader interface {
	ListEndpoints(context.Context, storage.EndpointListOptions) (storage.EndpointPage, error)
}

// StoreResolver builds a deterministic candidate sequence from persisted
// Routes and Endpoints. A matching Route owns selection; the legacy global
// native-then-delegated ordering remains the default only when no Route
// matches. Transient circuit state is kept in memory and exposed through
// AttemptController.
type StoreResolver struct {
	reader  endpointReader
	routes  storage.RouteReader
	breaker *circuitBreaker
}

func NewStoreResolver(reader endpointReader) (*StoreResolver, error) {
	if reader == nil {
		return nil, fmt.Errorf("endpoint reader is required")
	}
	routeReader, _ := reader.(storage.RouteReader)
	return &StoreResolver{
		reader:  reader,
		routes:  routeReader,
		breaker: newCircuitBreaker(circuitBreakerConfig{}),
	}, nil
}

// Resolve preserves the original single-candidate seam.
func (resolver *StoreResolver) Resolve(ctx context.Context, request ResolveRequest) (Resolved, error) {
	candidates, err := resolver.ResolveCandidates(ctx, request)
	if err != nil {
		return Resolved{}, err
	}
	return candidates[0], nil
}

// ResolveCandidates returns the complete stable fallback sequence. Health
// admission happens immediately before each attempt through BeginAttempt so a
// half-open probe cannot be reserved and then left unused.
func (resolver *StoreResolver) ResolveCandidates(ctx context.Context, request ResolveRequest) ([]Resolved, error) {
	if resolver == nil || resolver.reader == nil {
		return nil, ErrUnavailable
	}
	if err := request.Protocol.Validate(); err != nil {
		return nil, fmt.Errorf("resolve protocol: %w", err)
	}

	endpoints, err := resolver.readEnabledEndpoints(ctx)
	if err != nil {
		return nil, err
	}
	routes, err := resolver.readRoutes(ctx)
	if err != nil {
		return nil, err
	}
	if selected := selectMatchingRoute(routes, request); selected != nil {
		candidates := routeCandidates(*selected, endpoints, request)
		if len(candidates) == 0 {
			return nil, &CapabilityUnavailableError{
				Protocol:  request.Protocol,
				Modes:     routeCapabilityModes(*selected),
				Streaming: request.Streaming,
			}
		}
		return resolver.availableCandidates(candidates)
	}

	candidates := defaultCandidates(endpoints, request)
	if len(candidates) == 0 {
		return nil, &CapabilityUnavailableError{
			Protocol: request.Protocol,
			Modes: []contract.CapabilityMode{
				contract.CapabilityModeNative,
				contract.CapabilityModeDelegated,
			},
			Streaming: request.Streaming,
		}
	}
	return resolver.availableCandidates(candidates)
}

func (resolver *StoreResolver) availableCandidates(candidates []Resolved) ([]Resolved, error) {
	available := make([]Resolved, 0, len(candidates))
	for _, candidate := range candidates {
		if resolver.breaker.available(candidate) {
			available = append(available, candidate)
		}
	}
	if len(available) == 0 {
		return nil, ErrNoHealthyEndpoint
	}
	return available, nil
}

func (resolver *StoreResolver) readEnabledEndpoints(ctx context.Context) ([]contract.Endpoint, error) {
	enabled := true
	options := storage.EndpointListOptions{Limit: 200, Enabled: &enabled}
	byID := make(map[contract.EndpointID]contract.Endpoint)
	seenCursors := make(map[string]struct{})
	for {
		page, err := resolver.reader.ListEndpoints(ctx, options)
		if err != nil {
			return nil, fmt.Errorf("read persisted endpoints: %w", err)
		}
		for _, record := range page.Items {
			candidate := record.Endpoint
			if err := candidate.Validate(); err != nil {
				return nil, fmt.Errorf("persisted endpoint failed validation: %w", err)
			}
			if !candidate.Enabled {
				continue
			}
			if _, duplicate := byID[candidate.ID]; duplicate {
				return nil, fmt.Errorf("persisted endpoint %q is duplicated", candidate.ID)
			}
			byID[candidate.ID] = candidate
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == options.Cursor {
			return nil, fmt.Errorf("persisted endpoint pagination did not advance")
		}
		if _, duplicate := seenCursors[page.NextCursor]; duplicate {
			return nil, fmt.Errorf("persisted endpoint pagination repeated a cursor")
		}
		seenCursors[page.NextCursor] = struct{}{}
		options.Cursor = page.NextCursor
	}

	endpoints := make([]contract.Endpoint, 0, len(byID))
	for _, candidate := range byID {
		endpoints = append(endpoints, candidate)
	}
	sort.Slice(endpoints, func(left, right int) bool {
		return endpoints[left].ID < endpoints[right].ID
	})
	return endpoints, nil
}

func (resolver *StoreResolver) readRoutes(ctx context.Context) ([]contract.Route, error) {
	if resolver.routes == nil {
		return nil, nil
	}
	records, err := resolver.routes.ListRoutes(ctx)
	if err != nil {
		return nil, fmt.Errorf("read persisted routes: %w", err)
	}
	routes := make([]contract.Route, 0, len(records))
	seen := make(map[contract.RouteID]struct{}, len(records))
	for _, record := range records {
		route := record.Route
		if err := route.ValidateForAlpha(); err != nil {
			return nil, fmt.Errorf("persisted route failed validation: %w", err)
		}
		if _, duplicate := seen[route.ID]; duplicate {
			return nil, fmt.Errorf("persisted route %q is duplicated", route.ID)
		}
		seen[route.ID] = struct{}{}
		routes = append(routes, route)
	}
	return routes, nil
}

func selectMatchingRoute(routes []contract.Route, request ResolveRequest) *contract.Route {
	matches := make([]contract.Route, 0, len(routes))
	for _, route := range routes {
		if !route.Enabled || route.Match.Protocol != request.Protocol {
			continue
		}
		if route.Match.Model != "" && route.Match.Model != request.Model {
			continue
		}
		matches = append(matches, route)
	}
	sort.Slice(matches, func(left, right int) bool {
		a, b := matches[left], matches[right]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		aExact := a.Match.Model != ""
		bExact := b.Match.Model != ""
		if aExact != bExact {
			return aExact
		}
		return a.ID < b.ID
	})
	if len(matches) == 0 {
		return nil
	}
	selected := matches[0]
	return &selected
}

func routeCandidates(route contract.Route, endpoints []contract.Endpoint, request ResolveRequest) []Resolved {
	targets := append([]contract.RouteTarget(nil), route.Targets...)
	sort.Slice(targets, func(left, right int) bool {
		a, b := targets[left], targets[right]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if rankA, rankB := planTypeRank(a.PlanType), planTypeRank(b.PlanType); rankA != rankB {
			return rankA < rankB
		}
		if a.EndpointID != b.EndpointID {
			return a.EndpointID < b.EndpointID
		}
		return a.UpstreamProtocol < b.UpstreamProtocol
	})

	byID := make(map[contract.EndpointID]contract.Endpoint, len(endpoints))
	for _, candidate := range endpoints {
		byID[candidate.ID] = candidate
	}
	distinctTargets := make(map[contract.EndpointID]struct{}, len(route.Targets))
	for _, target := range route.Targets {
		distinctTargets[target.EndpointID] = struct{}{}
	}
	pinned := len(distinctTargets) == 1

	result := make([]Resolved, 0, len(targets))
	added := make(map[contract.EndpointID]struct{}, len(targets))
	for _, target := range targets {
		candidate, exists := byID[target.EndpointID]
		if !exists {
			continue
		}
		mode, ok := capabilityMode(target.PlanType)
		if !ok || !supportsRequest(candidate, request, mode) {
			continue
		}
		if _, duplicate := added[candidate.ID]; duplicate {
			// When two targets name the same endpoint, the first target in
			// sorted order wins — including its UpstreamModel rewrite.
			continue
		}
		added[candidate.ID] = struct{}{}
		result = append(result, Resolved{
			Endpoint:      candidate,
			Mode:          mode,
			RouteID:       route.ID,
			Pinned:        pinned,
			UpstreamModel: target.UpstreamModel,
		})
	}
	return result
}

func defaultCandidates(endpoints []contract.Endpoint, request ResolveRequest) []Resolved {
	result := make([]Resolved, 0, len(endpoints))
	added := make(map[contract.EndpointID]struct{}, len(endpoints))
	for _, mode := range []contract.CapabilityMode{
		contract.CapabilityModeNative,
		contract.CapabilityModeDelegated,
	} {
		for _, candidate := range endpoints {
			if _, duplicate := added[candidate.ID]; duplicate {
				continue
			}
			if !supportsRequest(candidate, request, mode) {
				continue
			}
			added[candidate.ID] = struct{}{}
			result = append(result, Resolved{Endpoint: candidate, Mode: mode})
		}
	}
	return result
}

func routeCapabilityModes(route contract.Route) []contract.CapabilityMode {
	present := make(map[contract.CapabilityMode]struct{}, 2)
	for _, target := range route.Targets {
		if mode, ok := capabilityMode(target.PlanType); ok {
			present[mode] = struct{}{}
		}
	}
	result := make([]contract.CapabilityMode, 0, len(present))
	for _, mode := range []contract.CapabilityMode{
		contract.CapabilityModeNative,
		contract.CapabilityModeDelegated,
	} {
		if _, ok := present[mode]; ok {
			result = append(result, mode)
		}
	}
	return result
}

func capabilityMode(planType contract.PlanType) (contract.CapabilityMode, bool) {
	switch planType {
	case contract.PlanTypeNative:
		return contract.CapabilityModeNative, true
	case contract.PlanTypeDelegated:
		return contract.CapabilityModeDelegated, true
	default:
		return "", false
	}
}

func planTypeRank(planType contract.PlanType) int {
	switch planType {
	case contract.PlanTypeNative:
		return 0
	case contract.PlanTypeDelegated:
		return 1
	default:
		return 2
	}
}

func supportsRequest(endpoint contract.Endpoint, request ResolveRequest, mode contract.CapabilityMode) bool {
	for _, capability := range endpoint.Capabilities {
		if capability.Protocol != request.Protocol || capability.Mode != mode {
			continue
		}
		if request.Streaming && !capability.Streaming {
			continue
		}
		if len(capability.Models) == 0 || containsModel(capability.Models, request.Model) {
			return true
		}
	}
	return false
}

func containsModel(models []string, requested string) bool {
	for _, model := range models {
		if model == requested {
			return true
		}
	}
	return false
}

func (resolver *StoreResolver) BeginAttempt(candidate Resolved) bool {
	if resolver == nil || resolver.breaker == nil {
		return false
	}
	return resolver.breaker.begin(candidate)
}

func (resolver *StoreResolver) RecordSuccess(candidate Resolved) {
	if resolver != nil && resolver.breaker != nil {
		resolver.breaker.success(candidate)
	}
}

func (resolver *StoreResolver) RecordFailure(candidate Resolved) {
	if resolver != nil && resolver.breaker != nil {
		resolver.breaker.failure(candidate)
	}
}

func (resolver *StoreResolver) AbandonAttempt(candidate Resolved) {
	if resolver != nil && resolver.breaker != nil {
		resolver.breaker.abandon(candidate)
	}
}

// ListAliasModels returns sorted, deduplicated public Match.Model names from
// enabled Routes that rewrite at least one target for the discovery family.
func (resolver *StoreResolver) ListAliasModels(
	ctx context.Context,
	discovery contract.ProtocolID,
) ([]string, error) {
	if resolver == nil || resolver.reader == nil {
		return nil, ErrUnavailable
	}
	routes, err := resolver.readRoutes(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for _, route := range routes {
		if !route.Enabled || route.Match.Model == "" {
			continue
		}
		if !aliasRouteMatchesDiscovery(route.Match.Protocol, discovery) {
			continue
		}
		hasRewrite := false
		for _, target := range route.Targets {
			if target.UpstreamModel != "" {
				hasRewrite = true
				break
			}
		}
		if !hasRewrite {
			continue
		}
		seen[route.Match.Model] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func aliasRouteMatchesDiscovery(routeProtocol, discovery contract.ProtocolID) bool {
	switch discovery {
	case contract.ProtocolGoogleModels:
		return routeProtocol == contract.ProtocolGoogleGenerateContent
	case contract.ProtocolOpenAIModels:
		switch routeProtocol {
		case contract.ProtocolOpenAIResponses,
			contract.ProtocolOpenAIResponsesCompact,
			contract.ProtocolOpenAIChat,
			contract.ProtocolOpenAICompletions,
			contract.ProtocolAnthropicMessages:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

var (
	_ Resolver          = (*StoreResolver)(nil)
	_ CandidateResolver = (*StoreResolver)(nil)
	_ AttemptController = (*StoreResolver)(nil)
	_ AliasLister       = (*StoreResolver)(nil)
)
