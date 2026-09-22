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

type ResolveRequest struct {
	// AllCandidates defers the failover limit until ingress applies session and transport constraints.
	AllCandidates bool
	Protocol      contract.ProtocolID
	Model         string
	Streaming     bool
	// Continuation keeps eligible targets available for exact affinity binding.
	// Ingress must bind the response ID before attempting any target.
	Continuation bool
	// Category is the classifier label for auto routes. Empty means fail-open
	// across every category's targets in document order.
	Category string
}

type Resolved struct {
	Path          *RecoveryPathSnapshot
	Unavailable   string
	FailurePolicy *contract.FailurePolicy
	Failover      *contract.FailoverPolicy
	Service       contract.Service
	Endpoint      contract.Endpoint // compatibility view for legacy callers
	BaseURL       string
	Mode          contract.CapabilityMode
	// PlanType is explicit for routed candidates. An empty value retains the
	// historical Mode-derived native/delegated behavior.
	PlanType contract.PlanType
	// UpstreamProtocol is the protocol the selected endpoint receives. Empty
	// retains the ingress protocol.
	UpstreamProtocol contract.ProtocolID
	// RouteID is set when an explicit persisted Route produced this candidate.
	RouteID contract.RouteID
	// SingleTargetRoute reflects the document before capability/health filtering.
	SingleTargetRoute bool
	// Pinned marks a Route that names exactly one distinct Endpoint. A pinned
	// Endpoint still must be enabled and capable, but may bypass circuit-open
	// exclusion when the caller explicitly chose it.
	Pinned bool
	// UpstreamModel is the per-target model rewrite from an explicit Route
	// (ADR 0006). Empty means no rewrite.
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

// AliasModelMapping is internal discovery metadata. It lets the aggregate
// listing replace a route's private upstream model with its public alias.
type AliasModelMapping struct {
	ServiceID     contract.ServiceID
	PublicModel   string
	UpstreamModel string
}

// AliasLister names the public alias models that explicit Routes define for a
// protocol family (ADR 0006). Mappings stay inside the process and must never
// be serialized into a discovery response.
type AliasLister interface {
	ListAliasModels(ctx context.Context, discovery contract.ProtocolID) ([]string, error)
	ListAliasModelMappings(ctx context.Context, discovery contract.ProtocolID) ([]AliasModelMapping, error)
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
