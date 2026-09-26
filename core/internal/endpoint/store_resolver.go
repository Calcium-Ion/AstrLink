package endpoint

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type serviceReader interface {
	ListServices(context.Context, storage.ServiceListOptions) (storage.ServicePage, error)
}

type endpointReader interface {
	ListEndpoints(context.Context, storage.EndpointListOptions) (storage.EndpointPage, error)
}

type endpointReaderAdapter struct {
	legacy endpointReader
}

func (adapter endpointReaderAdapter) ListServices(
	ctx context.Context,
	options storage.ServiceListOptions,
) (storage.ServicePage, error) {
	legacyOptions := storage.EndpointListOptions{
		Limit: options.Limit, Cursor: options.Cursor, Enabled: options.Enabled,
	}
	if options.Kind != nil && options.Kind.IsHTTP() {
		kind := contract.EndpointKind(*options.Kind)
		legacyOptions.Kind = &kind
	}
	page, err := adapter.legacy.ListEndpoints(ctx, legacyOptions)
	if err != nil {
		return storage.ServicePage{}, err
	}
	items := make([]storage.ServiceRecord, 0, len(page.Items))
	for _, record := range page.Items {
		items = append(items, storage.ServiceRecord{
			Service: contract.ServiceFromEndpoint(record.Endpoint), ETag: record.ETag,
		})
	}
	return storage.ServicePage{Items: items, NextCursor: page.NextCursor}, nil
}

// StoreResolver orders eligible services by persisted priority.
// AttemptController owns transient circuit admission.
type StoreResolver struct {
	routingSettings     storage.RoutingSettingsStore
	reader              serviceReader
	breaker             *circuitBreaker
	runtime             contract.RuntimeProfile
	subscriptionBaseURL string
}

// WithRuntimeProfile enables candidates backed by optional local runtimes.
func (resolver *StoreResolver) WithRuntimeProfile(profile contract.RuntimeProfile) *StoreResolver {
	if resolver != nil {
		resolver.runtime = profile
	}
	return resolver
}

func (resolver *StoreResolver) WithSubscriptionBaseURL(baseURL string) *StoreResolver {
	if resolver != nil && baseURL != "" {
		resolver.subscriptionBaseURL = baseURL
	}
	return resolver
}

func NewStoreResolver(source any) (*StoreResolver, error) {
	var reader serviceReader
	switch candidate := source.(type) {
	case serviceReader:
		reader = candidate
	case endpointReader:
		reader = endpointReaderAdapter{legacy: candidate}
	default:
		return nil, fmt.Errorf("service reader is required")
	}
	if reader == nil {
		return nil, fmt.Errorf("service reader is required")
	}
	routingSettings, _ := source.(storage.RoutingSettingsStore)
	return &StoreResolver{
		reader:              reader,
		routingSettings:     routingSettings,
		breaker:             newCircuitBreaker(circuitBreakerConfig{}),
		subscriptionBaseURL: accountauth.DefaultCodexAPIBaseURL,
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
	candidates, _, err := resolver.ResolveRankedCandidates(ctx, request)
	return candidates, err
}

// ResolveRankedCandidates is ResolveCandidates plus the ranking of every
// configured provider, including disabled ones when the service order is
// available.
func (resolver *StoreResolver) ResolveRankedCandidates(ctx context.Context, request ResolveRequest) ([]Resolved, []RankedService, error) {
	if resolver == nil || resolver.reader == nil {
		return nil, nil, ErrUnavailable
	}
	if err := request.Protocol.Validate(); err != nil {
		return nil, nil, fmt.Errorf("resolve protocol: %w", err)
	}

	endpoints, ranking, err := resolver.readEnabledServices(ctx)
	if err != nil {
		return nil, nil, err
	}
	settings := contract.DefaultRoutingSettings()
	if resolver.routingSettings != nil && !request.Protocol.IsModelDiscovery() {
		settings, err = resolver.routingSettings.GetRoutingSettings(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("read routing settings: %w", err)
		}
	}
	applyDefaults := func(candidates []Resolved) []Resolved {
		for index := range candidates {
			if candidates[index].FailurePolicy == nil && candidates[index].CanonicalService().FailurePolicy == nil {
				policy := settings.DefaultFailurePolicy
				candidates[index].FailurePolicy = &policy
			}
		}
		return candidates
	}
	candidates := rankCandidates(endpoints, request, resolver.subscriptionBaseURL, resolver.runtime, ranking)
	if !request.Protocol.IsModelDiscovery() {
		policy := settings.FailoverPolicy()
		for index := range candidates {
			candidates[index].Failover = &policy
		}
	}
	if len(candidates) == 0 {
		return nil, ranking, &CapabilityUnavailableError{
			Protocol: request.Protocol,
			Model:    request.Model,
			Modes: []contract.CapabilityMode{
				contract.CapabilityModeNative,
				contract.CapabilityModeDelegated,
			},
			Streaming: request.Streaming,
		}
	}
	candidates, err = resolver.availableCandidates(applyDefaults(candidates), ranking)
	if err != nil {
		return nil, ranking, err
	}
	if !request.Protocol.IsModelDiscovery() && !request.Continuation && !request.AllCandidates && !settings.AllowUnmatchedFailover {
		candidates = candidates[:1]
	}
	return candidates, ranking, nil
}

// ResolveService returns an enabled HTTP service without consulting
// capabilities or circuit health; the caller owns the single attempt.
func (resolver *StoreResolver) ResolveService(ctx context.Context, id contract.ServiceID) (Resolved, error) {
	if resolver == nil || resolver.reader == nil {
		return Resolved{}, ErrUnavailable
	}
	services, _, err := resolver.readEnabledServices(ctx)
	if err != nil {
		return Resolved{}, err
	}
	for _, service := range services {
		if service.ID == id && service.Kind.IsHTTP() && service.HTTP != nil {
			return Resolved{Service: service, BaseURL: service.HTTP.BaseURL, Mode: contract.CapabilityModeNative}, nil
		}
	}
	return Resolved{}, ErrNoEndpoint
}

func (resolver *StoreResolver) availableCandidates(candidates []Resolved, ranking []RankedService) ([]Resolved, error) {
	available := make([]Resolved, 0, len(candidates))
	for _, candidate := range candidates {
		if reason := resolver.breaker.unavailableReason(candidate); reason != "" {
			MarkSkipped(ranking, candidate.CanonicalService().ID, reason)
			continue
		}
		available = append(available, candidate)
	}
	if len(available) == 0 {
		skipped := make([]contract.ServiceID, 0, len(candidates))
		for _, candidate := range candidates {
			if id := candidate.CanonicalService().ID; !slices.Contains(skipped, id) {
				skipped = append(skipped, id)
			}
		}
		return nil, &UnhealthyCandidatesError{Services: skipped}
	}
	return available, nil
}

// readEnabledServices returns the schedulable services in priority order and
// the ranking of every known service, with the reason unschedulable ones are
// left out. Disabled services are only known through the service order.
func (resolver *StoreResolver) readEnabledServices(ctx context.Context) ([]contract.Service, []RankedService, error) {
	enabled := true
	options := storage.ServiceListOptions{Limit: 200, Enabled: &enabled}
	byID := make(map[contract.ServiceID]contract.Service)
	excluded := make(map[contract.ServiceID]contract.RoutingSkipReason)
	seenCursors := make(map[string]struct{})
	for {
		page, err := resolver.reader.ListServices(ctx, options)
		if err != nil {
			return nil, nil, fmt.Errorf("read persisted endpoints: %w", err)
		}
		for _, record := range page.Items {
			candidate := record.Service
			if err := candidate.Validate(); err != nil {
				return nil, nil, fmt.Errorf("persisted endpoint failed validation: %w", err)
			}
			if !candidate.Enabled {
				excluded[candidate.ID] = contract.RoutingSkipDisabled
				continue
			}
			if candidate.Kind.IsSubscription() &&
				(candidate.Subscription == nil || candidate.Subscription.Status != contract.SubscriptionStatusConnected) {
				excluded[candidate.ID] = contract.RoutingSkipNotConnected
				continue
			}
			// Accounts paused by an upstream risk signal stay out of scheduling
			// until the pause expires or the user restores them.
			if candidate.Kind.IsSubscription() && candidate.Subscription.Risk.Blocks(resolver.breaker.now()) {
				excluded[candidate.ID] = contract.RoutingSkipRiskPaused
				continue
			}
			if _, duplicate := byID[candidate.ID]; duplicate {
				return nil, nil, fmt.Errorf("persisted endpoint %q is duplicated", candidate.ID)
			}
			byID[candidate.ID] = candidate
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == options.Cursor {
			return nil, nil, fmt.Errorf("persisted endpoint pagination did not advance")
		}
		if _, duplicate := seenCursors[page.NextCursor]; duplicate {
			return nil, nil, fmt.Errorf("persisted endpoint pagination repeated a cursor")
		}
		seenCursors[page.NextCursor] = struct{}{}
		options.Cursor = page.NextCursor
	}

	endpoints := make([]contract.Service, 0, len(byID))
	for _, candidate := range byID {
		endpoints = append(endpoints, candidate)
	}
	positions := map[contract.ServiceID]int{}
	if ordered, ok := resolver.reader.(storage.ServiceOrderStore); ok {
		record, err := ordered.GetServiceOrder(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("read service order: %w", err)
		}
		for index, id := range record.Order.ServiceIDs {
			positions[id] = index + 1
			if _, listed := byID[id]; !listed {
				if _, known := excluded[id]; !known {
					// The enabled-only listing never returns disabled services.
					excluded[id] = contract.RoutingSkipDisabled
				}
			}
		}
	}
	before := func(a, b contract.ServiceID) bool {
		left, right := positions[a], positions[b]
		if left == 0 {
			left = len(positions) + 1
		}
		if right == 0 {
			right = len(positions) + 1
		}
		if left != right {
			return left < right
		}
		return a < b
	}
	sort.Slice(endpoints, func(left, right int) bool {
		return before(endpoints[left].ID, endpoints[right].ID)
	})
	ranking := make([]RankedService, 0, len(byID)+len(excluded))
	for _, candidate := range endpoints {
		ranking = append(ranking, RankedService{ServiceID: candidate.ID})
	}
	for id, reason := range excluded {
		ranking = append(ranking, RankedService{ServiceID: id, Skip: reason})
	}
	sort.SliceStable(ranking, func(left, right int) bool {
		return before(ranking[left].ServiceID, ranking[right].ServiceID)
	})
	return endpoints, ranking, nil
}

// MarkSkipped records why routing excluded a ranked provider that was still
// eligible; the first recorded reason wins.
func MarkSkipped(ranking []RankedService, id contract.ServiceID, reason contract.RoutingSkipReason) {
	for index := range ranking {
		if ranking[index].ServiceID == id {
			if ranking[index].Skip == "" {
				ranking[index].Skip = reason
			}
			return
		}
	}
}

func defaultCandidates(endpoints []contract.Service, request ResolveRequest, subscriptionBaseURL string, runtimes ...contract.RuntimeProfile) []Resolved {
	var runtime contract.RuntimeProfile
	if len(runtimes) > 0 {
		runtime = runtimes[0]
	}
	return rankCandidates(endpoints, request, subscriptionBaseURL, runtime, nil)
}

// rankCandidates plans request on each service in order and marks the ranked
// services that cannot serve it.
func rankCandidates(endpoints []contract.Service, request ResolveRequest, subscriptionBaseURL string, runtime contract.RuntimeProfile, ranking []RankedService) []Resolved {
	result := make([]Resolved, 0, len(endpoints))
	added := make(map[contract.ServiceID]struct{}, len(endpoints))
	for _, candidate := range endpoints {
		if _, duplicate := added[candidate.ID]; duplicate {
			continue
		}
		resolved, skip := planService(candidate, request, subscriptionBaseURL, runtime)
		if skip != "" {
			MarkSkipped(ranking, candidate.ID, skip)
			continue
		}
		added[candidate.ID] = struct{}{}
		result = append(result, resolved)
	}
	return result
}

// planService plans request on the first capability mode of service that can
// serve it, or reports the furthest check every mode failed.
func planService(candidate contract.Service, request ResolveRequest, subscriptionBaseURL string, runtime contract.RuntimeProfile) (Resolved, contract.RoutingSkipReason) {
	if !request.Protocol.IsModelDiscovery() && !containsModel(candidate.Models, request.Model) {
		return Resolved{}, contract.RoutingSkipModelNotListed
	}
	skip := contract.RoutingSkipProtocolUnsupported
	narrow := func(reason contract.RoutingSkipReason) {
		if reason == contract.RoutingSkipConversionUnavailable || skip == contract.RoutingSkipProtocolUnsupported {
			skip = reason
		}
	}
	for _, mode := range []contract.CapabilityMode{contract.CapabilityModeNative, contract.CapabilityModeDelegated} {
		if !supportsRequest(candidate, request, mode) {
			narrow(capabilitySkip(candidate, request.Protocol, request, mode))
			continue
		}
		upstreamProtocol, planType := request.Protocol, contract.PlanType("")
		if native := candidate.Kind.ModelNativeProtocol(request.Model); native != "" && !request.Protocol.IsModelDiscovery() {
			if mode != contract.CapabilityModeNative {
				continue
			}
			if !supportsProtocol(candidate, native, request.Model, request.Streaming, contract.CapabilityModeNative) {
				narrow(capabilitySkip(candidate, native, request, contract.CapabilityModeNative))
				continue
			}
			upstreamProtocol = native
			if native != request.Protocol {
				if !supportsModelConversion(runtime, request.Protocol, native, request.Streaming) {
					narrow(contract.RoutingSkipConversionUnavailable)
					continue
				}
				planType = contract.PlanTypeRelayKit
			}
		}
		legacy, _ := candidate.EndpointView()
		return Resolved{
			Service: candidate, Endpoint: legacy,
			BaseURL: baseURLForService(candidate, subscriptionBaseURL), Mode: mode,
			UpstreamModel: request.Model, UpstreamProtocol: upstreamProtocol, PlanType: planType,
		}, ""
	}
	return Resolved{}, skip
}

// capabilitySkip separates a provider that lacks protocol in mode from one that
// only cannot stream it.
func capabilitySkip(candidate contract.Service, protocol contract.ProtocolID, request ResolveRequest, mode contract.CapabilityMode) contract.RoutingSkipReason {
	if request.Streaming && supportsProtocol(candidate, protocol, request.Model, false, mode) {
		return contract.RoutingSkipStreamingUnsupported
	}
	return contract.RoutingSkipProtocolUnsupported
}

func supportsModelConversion(runtime contract.RuntimeProfile, from, to contract.ProtocolID, streaming bool) bool {
	if !runtime.RelayKitAvailable {
		return false
	}
	for _, edge := range runtime.Edges {
		if edge.From == from && edge.To == to && (!streaming || edge.Streaming) {
			return true
		}
	}
	return false
}

func baseURLForService(service contract.Service, subscriptionBaseURL string) string {
	if service.Kind == contract.ServiceKindClaudeSubscription {
		return accountauth.DefaultClaudeAPIBaseURL
	}
	if service.Kind == contract.ServiceKindGrokSubscription {
		return accountauth.DefaultGrokAPIBaseURL
	}
	if service.Kind.IsSubscription() {
		return subscriptionBaseURL
	}
	if service.HTTP == nil {
		return ""
	}
	return service.HTTP.BaseURL
}

func supportsRequest(endpoint contract.Service, request ResolveRequest, mode contract.CapabilityMode) bool {
	return supportsProtocol(endpoint, request.Protocol, request.Model, request.Streaming, mode)
}

func supportsProtocol(
	endpoint contract.Service,
	protocol contract.ProtocolID,
	model string,
	streaming bool,
	mode contract.CapabilityMode,
) bool {
	if !protocol.IsModelDiscovery() && !containsModel(endpoint.Models, model) {
		return false
	}
	for _, capability := range endpoint.Capabilities {
		if capability.Protocol != protocol || capability.Mode != mode {
			continue
		}
		if streaming && !capability.Streaming {
			continue
		}
		return true
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

var (
	_ Resolver          = (*StoreResolver)(nil)
	_ CandidateResolver = (*StoreResolver)(nil)
	_ RankingResolver   = (*StoreResolver)(nil)
	_ AttemptController = (*StoreResolver)(nil)
)
