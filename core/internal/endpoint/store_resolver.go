package endpoint

import (
	"context"
	"fmt"
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

// StoreResolver orders eligible services by persisted priority. Legacy routes
// are inactive. AttemptController owns transient circuit admission.
type StoreResolver struct {
	paths               storage.RecoveryPathStore
	routingSettings     storage.RoutingSettingsStore
	reader              serviceReader
	routes              storage.RouteReader
	breaker             *circuitBreaker
	runtime             contract.RuntimeProfile
	subscriptionBaseURL string
}

// WithRuntimeProfile enables candidates backed by optional local runtimes.
// Persisted RelayKit documents remain readable when disabled so mixed routes
// can continue serving their native/delegated targets.
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
	paths, _ := source.(storage.RecoveryPathStore)
	routeReader, _ := source.(storage.RouteReader)
	routingSettings, _ := source.(storage.RoutingSettingsStore)
	return &StoreResolver{
		reader:              reader,
		paths:               paths,
		routingSettings:     routingSettings,
		routes:              routeReader,
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
	if resolver == nil || resolver.reader == nil {
		return nil, ErrUnavailable
	}
	if err := request.Protocol.Validate(); err != nil {
		return nil, fmt.Errorf("resolve protocol: %w", err)
	}

	endpoints, err := resolver.readEnabledServices(ctx)
	if err != nil {
		return nil, err
	}
	settings := contract.DefaultRoutingSettings()
	if resolver.routingSettings != nil && !request.Protocol.IsModelDiscovery() {
		settings, err = resolver.routingSettings.GetRoutingSettings(ctx)
		if err != nil {
			return nil, fmt.Errorf("read routing settings: %w", err)
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
	candidates := defaultCandidates(endpoints, request, resolver.subscriptionBaseURL, resolver.runtime)
	if !request.Protocol.IsModelDiscovery() {
		policy := settings.FailoverPolicy()
		for index := range candidates {
			candidates[index].Failover = &policy
		}
	}
	if len(candidates) == 0 {
		return nil, &CapabilityUnavailableError{
			Protocol: request.Protocol,
			Model:    request.Model,
			Modes: []contract.CapabilityMode{
				contract.CapabilityModeNative,
				contract.CapabilityModeDelegated,
			},
			Streaming: request.Streaming,
		}
	}
	candidates, err = resolver.availableCandidates(applyDefaults(candidates))
	if err != nil {
		return nil, err
	}
	if !request.Protocol.IsModelDiscovery() && !request.Continuation && !request.AllCandidates && !settings.AllowUnmatchedFailover {
		candidates = candidates[:1]
	}
	return candidates, nil
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

func (resolver *StoreResolver) readEnabledServices(ctx context.Context) ([]contract.Service, error) {
	enabled := true
	options := storage.ServiceListOptions{Limit: 200, Enabled: &enabled}
	byID := make(map[contract.ServiceID]contract.Service)
	seenCursors := make(map[string]struct{})
	for {
		page, err := resolver.reader.ListServices(ctx, options)
		if err != nil {
			return nil, fmt.Errorf("read persisted endpoints: %w", err)
		}
		for _, record := range page.Items {
			candidate := record.Service
			if err := candidate.Validate(); err != nil {
				return nil, fmt.Errorf("persisted endpoint failed validation: %w", err)
			}
			if !candidate.Enabled {
				continue
			}
			if candidate.Kind.IsSubscription() &&
				(candidate.Subscription == nil || candidate.Subscription.Status != contract.SubscriptionStatusConnected) {
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

	endpoints := make([]contract.Service, 0, len(byID))
	for _, candidate := range byID {
		endpoints = append(endpoints, candidate)
	}
	positions := map[contract.ServiceID]int{}
	if ordered, ok := resolver.reader.(storage.ServiceOrderStore); ok {
		record, err := ordered.GetServiceOrder(ctx)
		if err != nil {
			return nil, fmt.Errorf("read service order: %w", err)
		}
		for index, id := range record.Order.ServiceIDs {
			positions[id] = index + 1
		}
	}
	sort.Slice(endpoints, func(left, right int) bool {
		a, b := positions[endpoints[left].ID], positions[endpoints[right].ID]
		if a == 0 {
			a = len(positions) + 1
		}
		if b == 0 {
			b = len(positions) + 1
		}
		if a != b {
			return a < b
		}
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
		if err := validateRouteDocument(route); err != nil {
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

func routeCandidates(
	route contract.Route,
	endpoints []contract.Service,
	request ResolveRequest,
	runtime contract.RuntimeProfile,
	subscriptionBaseURL string,
) []Resolved {
	targets := route.TargetsForCategory(request.Category)
	sort.Slice(targets, func(left, right int) bool {
		a, b := targets[left], targets[right]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if rankA, rankB := planTypeRank(a.PlanType), planTypeRank(b.PlanType); rankA != rankB {
			return rankA < rankB
		}
		if a.ServiceID != b.ServiceID {
			return a.ServiceID < b.ServiceID
		}
		return a.UpstreamProtocol < b.UpstreamProtocol
	})

	if route.Failover != nil && !route.Failover.Enabled && !request.Continuation && len(targets) > 1 {
		targets = targets[:1]
	}

	byID := make(map[contract.ServiceID]contract.Service, len(endpoints))
	for _, candidate := range endpoints {
		byID[candidate.ID] = candidate
	}
	distinctTargets := make(map[contract.ServiceID]struct{}, len(targets))
	for _, target := range targets {
		distinctTargets[target.ServiceID] = struct{}{}
	}
	pinned := len(distinctTargets) == 1

	result := make([]Resolved, 0, len(targets))
	type candidateKey struct {
		endpoint contract.ServiceID
		plan     contract.PlanType
		protocol contract.ProtocolID
		model    string
	}
	added := make(map[candidateKey]struct{}, len(targets))
	for _, target := range targets {
		candidate, exists := byID[target.ServiceID]
		if !exists {
			continue
		}
		mode, ok := capabilityMode(target.PlanType)
		upstreamProtocol := target.UpstreamProtocol
		effectiveRequest := request
		if target.UpstreamModel != "" {
			effectiveRequest.Model = target.UpstreamModel
		}
		if native := candidate.Kind.ModelNativeProtocol(effectiveRequest.Model); native != "" && !request.Protocol.IsModelDiscovery() {
			upstreamProtocol = native
			target.PlanType = contract.PlanTypeNative
			if native != request.Protocol {
				if !supportsModelConversion(runtime, request.Protocol, native, request.Streaming) {
					continue
				}
				target.PlanType = contract.PlanTypeRelayKit
			}
			mode, ok = contract.CapabilityModeNative, true
		}
		if target.PlanType == contract.PlanTypeRelayKit {
			if !runtime.RelayKitAvailable ||
				!supportsProtocol(candidate, upstreamProtocol, effectiveRequest.Model, request.Streaming, contract.CapabilityModeNative) {
				continue
			}
			mode = contract.CapabilityModeNative
		} else if !ok || !supportsRequest(candidate, effectiveRequest, mode) {
			continue
		}
		key := candidateKey{endpoint: candidate.ID, plan: target.PlanType, protocol: upstreamProtocol, model: effectiveRequest.Model}
		if _, duplicate := added[key]; duplicate {
			// Only an identical execution target is redundant.
			continue
		}
		added[key] = struct{}{}
		legacy, _ := candidate.EndpointView()
		result = append(result, Resolved{
			Service:       candidate,
			FailurePolicy: route.FailurePolicy, Failover: route.Failover,
			Endpoint:          legacy,
			BaseURL:           baseURLForService(candidate, subscriptionBaseURL),
			Mode:              mode,
			PlanType:          target.PlanType,
			UpstreamProtocol:  upstreamProtocol,
			RouteID:           route.ID,
			SingleTargetRoute: len(route.ExecutableTargets()) == 1,
			Pinned:            pinned,
			UpstreamModel:     target.UpstreamModel,
			RequestedModel:    request.Model,
		})
	}
	return result
}

func defaultCandidates(endpoints []contract.Service, request ResolveRequest, subscriptionBaseURL string, runtimes ...contract.RuntimeProfile) []Resolved {
	var runtime contract.RuntimeProfile
	if len(runtimes) > 0 {
		runtime = runtimes[0]
	}
	result := make([]Resolved, 0, len(endpoints))
	added := make(map[contract.ServiceID]struct{}, len(endpoints))
	for _, candidate := range endpoints {
		for _, mode := range []contract.CapabilityMode{contract.CapabilityModeNative, contract.CapabilityModeDelegated} {
			if _, duplicate := added[candidate.ID]; duplicate {
				continue
			}
			if !supportsRequest(candidate, request, mode) {
				continue
			}
			upstreamProtocol, planType := request.Protocol, contract.PlanType("")
			if native := candidate.Kind.ModelNativeProtocol(request.Model); native != "" && !request.Protocol.IsModelDiscovery() {
				if mode != contract.CapabilityModeNative || !supportsProtocol(candidate, native, request.Model, request.Streaming, contract.CapabilityModeNative) {
					continue
				}
				upstreamProtocol = native
				if native != request.Protocol {
					if !supportsModelConversion(runtime, request.Protocol, native, request.Streaming) {
						continue
					}
					planType = contract.PlanTypeRelayKit
				}
			}
			added[candidate.ID] = struct{}{}
			legacy, _ := candidate.EndpointView()
			result = append(result, Resolved{
				Service: candidate, Endpoint: legacy,
				BaseURL: baseURLForService(candidate, subscriptionBaseURL), Mode: mode,
				UpstreamModel: request.Model, UpstreamProtocol: upstreamProtocol, PlanType: planType,
			})
		}
	}
	return result
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

func routeCapabilityModes(route contract.Route) []contract.CapabilityMode {
	present := make(map[contract.CapabilityMode]struct{}, 2)
	for _, target := range route.ExecutableTargets() {
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

func validateRouteDocument(route contract.Route) error {
	if err := route.Validate(); err != nil {
		return err
	}
	if !route.Match.Protocol.AvailableInAlpha() {
		return fmt.Errorf("match protocol %q is not available in Alpha", route.Match.Protocol)
	}
	for index, target := range route.ExecutableTargets() {
		if !target.UpstreamProtocol.AvailableInAlpha() {
			return fmt.Errorf("targets[%d]: upstream protocol %q is not available in Alpha", index, target.UpstreamProtocol)
		}
	}
	return nil
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
	mappings, err := resolver.ListAliasModelMappings(ctx, discovery)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(mappings))
	for _, mapping := range mappings {
		seen[mapping.PublicModel] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// ListAliasModelMappings returns enabled, model-eligible rewrites for internal
// discovery filtering. Callers must expose PublicModel only.
func (resolver *StoreResolver) ListAliasModelMappings(
	ctx context.Context,
	discovery contract.ProtocolID,
) ([]AliasModelMapping, error) {
	return []AliasModelMapping{}, nil
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
