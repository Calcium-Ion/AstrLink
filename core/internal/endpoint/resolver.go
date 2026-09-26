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
	ErrNoEndpoint        = errors.New("no endpoint provides the requested capability")
	ErrNoHealthyEndpoint = errors.New("no healthy endpoint is available for the requested capability")
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

// UnhealthyCandidatesError names the services skipped because their circuits
// are open. It unwraps to ErrNoHealthyEndpoint.
type UnhealthyCandidatesError struct {
	Services []contract.ServiceID
}

func (err *UnhealthyCandidatesError) Error() string {
	return ErrNoHealthyEndpoint.Error()
}

func (err *UnhealthyCandidatesError) Unwrap() error {
	return ErrNoHealthyEndpoint
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

// ServiceResolver looks up one schedulable HTTP service by ID for
// gateway-owned requests outside protocol routing, such as built-in tool
// Images API calls. It never selects a different provider.
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

// AttemptController owns transient endpoint health admission and feedback.
// BeginAttempt must be called immediately before an upstream attempt. Exactly
// one of RecordSuccess, RecordFailure, or AbandonAttempt should follow a
// successful admission.
type AttemptController interface {
	BeginAttempt(Resolved) bool
	RecordSuccess(Resolved)
	RecordFailure(Resolved)
	AbandonAttempt(Resolved)
}

// UnavailableResolver is the fail-closed fallback for composition without a
// persistent Endpoint reader.
type UnavailableResolver struct{}

func (UnavailableResolver) Resolve(context.Context, ResolveRequest) (Resolved, error) {
	return Resolved{}, ErrUnavailable
}

// RateLimitController applies upstream-requested cooldown without treating 429 as success.
type RateLimitController interface{ RecordRateLimit(Resolved, time.Duration) }
