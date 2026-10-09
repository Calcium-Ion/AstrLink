// Package endpoint owns the boundary between protocol routing and configured
// upstream Endpoints. Persistent desktop composition uses StoreResolver;
// incomplete/headless composition retains a fail-closed fallback.
package endpoint

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

var (
	ErrNoEndpoint = errors.New("no endpoint provides the requested capability")
	// ErrNoHealthyEndpoint means every candidate is still cooling down after
	// its upstream answered HTTP 429.
	ErrNoHealthyEndpoint = errors.New("every candidate is rate limited by its upstream")
	ErrUnavailable       = errors.New("endpoint resolver is unavailable")
)

// CapabilityUnavailableError identifies the protocol, accepted Alpha modes,
// and streaming requirement that no enabled candidate can satisfy. It unwraps
// to ErrNoEndpoint for compatibility with the original resolver seam.
type CapabilityUnavailableError struct {
	Protocol  contract.ProtocolID
	Model     string
	Modes     []contract.CapabilityMode
	Streaming bool
}

func (err *CapabilityUnavailableError) Error() string {
	modes := make([]string, 0, len(err.Modes))
	for _, mode := range err.Modes {
		modes = append(modes, string(mode))
	}
	return fmt.Sprintf(
		"%s: protocol=%q model=%q modes=%q streaming=%t",
		ErrNoEndpoint,
		err.Protocol,
		err.Model,
		strings.Join(modes, ","),
		err.Streaming,
	)
}

func (err *CapabilityUnavailableError) Unwrap() error {
	return ErrNoEndpoint
}

// RateLimitedCandidatesError lists the cooldowns that left no candidate to
// attempt. It unwraps to ErrNoHealthyEndpoint.
type RateLimitedCandidatesError struct {
	Limits []RateLimitedCandidate
}

// RateLimitedCandidate is one route its upstream asked to wait for.
type RateLimitedCandidate struct {
	Service     contract.ServiceID
	ServiceName string
	Model       string
	Until       time.Time
}

func (err *RateLimitedCandidatesError) Error() string {
	return ErrNoHealthyEndpoint.Error()
}

func (err *RateLimitedCandidatesError) Unwrap() error {
	return ErrNoHealthyEndpoint
}

// RetryAt is when the earliest cooldown ends, or the zero time when none is known.
func (err *RateLimitedCandidatesError) RetryAt() time.Time {
	var earliest time.Time
	for _, limit := range err.Limits {
		if earliest.IsZero() || limit.Until.Before(earliest) {
			earliest = limit.Until
		}
	}
	return earliest
}

type ResolveRequest struct {
	// AllCandidates defers the failover limit until ingress applies session and transport constraints.
	AllCandidates bool
	Protocol      contract.ProtocolID
	Model         string
	Streaming     bool
	// Continuation keeps eligible targets available for exact affinity binding.
	// Ingress must bind the response ID before attempting any target.
	Continuation bool
}

type Resolved struct {
	FailurePolicy *contract.FailurePolicy
	Failover      *contract.FailoverPolicy
	Service       contract.Service
	Endpoint      contract.Endpoint // compatibility view for legacy callers
	BaseURL       string
	Mode          contract.CapabilityMode
	// PlanType is explicit for converted candidates. An empty value retains the
	// Mode-derived native/delegated behavior.
	PlanType contract.PlanType
	// UpstreamProtocol is the protocol the selected endpoint receives. Empty
	// retains the ingress protocol.
	UpstreamProtocol contract.ProtocolID
	// UpstreamModel is the model id sent upstream. Ingress rewrites the
	// request's model only when it differs from the client's; empty keeps the
	// routing model.
	UpstreamModel  string
	RequestedModel string
}

func (resolved Resolved) CanonicalService() contract.Service {
	if resolved.Service.ID != "" {
		return resolved.Service
	}
	if resolved.Endpoint.ID != "" {
		return contract.ServiceFromEndpoint(resolved.Endpoint)
	}
	return contract.Service{}
}

func (resolved Resolved) EffectiveBaseURL() string {
	if resolved.BaseURL != "" {
		return resolved.BaseURL
	}
	if resolved.Endpoint.BaseURL != "" {
		return resolved.Endpoint.BaseURL
	}
	service := resolved.CanonicalService()
	if service.HTTP != nil {
		return service.HTTP.BaseURL
	}
	return ""
}

func (resolved Resolved) AuthorizationEndpoint() (contract.Endpoint, error) {
	service := resolved.CanonicalService()
	if service.ID == "" {
		return contract.Endpoint{}, fmt.Errorf("resolved service is empty")
	}
	if service.Kind.IsHTTP() {
		return service.EndpointView()
	}
	if service.Subscription == nil {
		return contract.Endpoint{}, fmt.Errorf("subscription service %q has no connection", service.ID)
	}
	return contract.Endpoint{
		ID: service.ID, Name: service.Name, Kind: service.Kind, BaseURL: resolved.BaseURL,
		Auth:          contract.EndpointAuth{Scheme: contract.AuthSchemeBearer},
		CredentialRef: service.Subscription.CredentialRef, Enabled: service.Enabled,
		Capabilities: append([]contract.Capability(nil), service.Capabilities...),
	}, nil
}

type Resolver interface {
	Resolve(context.Context, ResolveRequest) (Resolved, error)
}

// CandidateResolver exposes the complete deterministic fallback sequence.
// Resolver remains the compatibility seam for single-attempt callers.
type CandidateResolver interface {
	ResolveCandidates(context.Context, ResolveRequest) ([]Resolved, error)
}

// ServiceResolver looks up one schedulable service by ID for gateway-owned
// requests outside protocol routing, such as built-in tool Images API calls
// and Codex's own tool requests for a turn a subscription served. A disabled,
// disconnected or risk-paused service is not found. It never selects a
// different provider.
type ServiceResolver interface {
	ResolveService(context.Context, contract.ServiceID) (Resolved, error)
}

// RankedService is one configured provider in routing priority order. Skip is
// empty when the provider was eligible for the request.
type RankedService struct {
	ServiceID contract.ServiceID
	Skip      contract.RoutingSkipReason
}

// RankingResolver also reports every configured provider in priority order and
// why routing excluded it, so request records can explain the choice. The
// ranking accompanies resolution errors too.
type RankingResolver interface {
	ResolveRankedCandidates(context.Context, ResolveRequest) ([]Resolved, []RankedService, error)
}

// UnavailableResolver is the fail-closed fallback for composition without a
// persistent Endpoint reader.
type UnavailableResolver struct{}

func (UnavailableResolver) Resolve(context.Context, ResolveRequest) (Resolved, error) {
	return Resolved{}, ErrUnavailable
}

// RateLimitController applies the cooldown an upstream requested with HTTP
// 429. RateLimitedUntil is checked immediately before each attempt, when the
// candidate carries its final upstream protocol and model.
type RateLimitController interface {
	RecordRateLimit(Resolved, time.Duration)
	RateLimitedUntil(Resolved) time.Time
}
