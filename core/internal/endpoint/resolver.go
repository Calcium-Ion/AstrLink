// Package endpoint owns the boundary between protocol routing and configured
// upstream Endpoints. Persistent desktop composition uses StoreResolver;
// incomplete/headless composition retains a fail-closed fallback.
package endpoint

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	Modes     []contract.CapabilityMode
	Streaming bool
}

func (err *CapabilityUnavailableError) Error() string {
	modes := make([]string, 0, len(err.Modes))
	for _, mode := range err.Modes {
		modes = append(modes, string(mode))
	}
	return fmt.Sprintf(
		"%s: protocol=%q modes=%q streaming=%t",
		ErrNoEndpoint,
		err.Protocol,
		strings.Join(modes, ","),
		err.Streaming,
	)
}

func (err *CapabilityUnavailableError) Unwrap() error {
	return ErrNoEndpoint
}

type ResolveRequest struct {
	Protocol  contract.ProtocolID
	Model     string
	Streaming bool
}

type Resolved struct {
	Endpoint contract.Endpoint
	Mode     contract.CapabilityMode
	// RouteID is set when an explicit persisted Route produced this candidate.
	RouteID contract.RouteID
	// Pinned marks a Route that names exactly one distinct Endpoint. A pinned
	// Endpoint still must be enabled and capable, but may bypass circuit-open
	// exclusion when the caller explicitly chose it.
	Pinned bool
	// UpstreamModel is the per-target model rewrite from an explicit Route
	// (ADR 0006). Empty means no rewrite.
	UpstreamModel string
}

type Resolver interface {
	Resolve(context.Context, ResolveRequest) (Resolved, error)
}

// CandidateResolver exposes the complete deterministic fallback sequence.
// Resolver remains the compatibility seam for single-attempt callers.
type CandidateResolver interface {
	ResolveCandidates(context.Context, ResolveRequest) ([]Resolved, error)
}

// AliasLister names the public alias models that explicit Routes define
// for a protocol family (ADR 0006). Implementations must not disclose
// target endpoints or upstream models.
type AliasLister interface {
	ListAliasModels(ctx context.Context, discovery contract.ProtocolID) ([]string, error)
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
