package contract

import "fmt"

// MaxRoutingSkips bounds the passed-over providers kept on one record.
const MaxRoutingSkips = 64

// RoutingSelection says why an attempt used its API provider.
type RoutingSelection string

const (
	// RoutingSelectionPriority is the first eligible provider in the
	// configured order.
	RoutingSelectionPriority RoutingSelection = "priority"
	// RoutingSelectionSessionBinding is the provider channel stickiness
	// preferred because it last served this session.
	RoutingSelectionSessionBinding RoutingSelection = "session_binding"
	// RoutingSelectionResponseAffinity is the provider that produced the
	// previous_response_id the request continues.
	RoutingSelectionResponseAffinity RoutingSelection = "response_affinity"
	// RoutingSelectionWebSocketConnection is the provider an established
	// Responses WebSocket connection is bound to.
	RoutingSelectionWebSocketConnection RoutingSelection = "websocket_connection"
	// RoutingSelectionFailover follows an earlier provider of the same
	// request that was rejected or failed.
	RoutingSelectionFailover RoutingSelection = "failover"
)

func (selection RoutingSelection) Valid() bool {
	switch selection {
	case RoutingSelectionPriority, RoutingSelectionSessionBinding, RoutingSelectionResponseAffinity,
		RoutingSelectionWebSocketConnection, RoutingSelectionFailover:
		return true
	default:
		return false
	}
}

// RoutingSkipReason says why routing excluded a provider before any attempt.
type RoutingSkipReason string

const (
	RoutingSkipDisabled     RoutingSkipReason = "disabled"
	RoutingSkipNotConnected RoutingSkipReason = "not_connected"
	// RoutingSkipRiskPaused: an upstream risk signal paused the subscription
	// account.
	RoutingSkipRiskPaused           RoutingSkipReason = "risk_paused"
	RoutingSkipModelNotListed       RoutingSkipReason = "model_not_listed"
	RoutingSkipProtocolUnsupported  RoutingSkipReason = "protocol_unsupported"
	RoutingSkipStreamingUnsupported RoutingSkipReason = "streaming_unsupported"
	// RoutingSkipConversionUnavailable: the model only speaks another protocol
	// and the local converter cannot bridge the request.
	RoutingSkipConversionUnavailable RoutingSkipReason = "conversion_unavailable"
	RoutingSkipCircuitOpen           RoutingSkipReason = "circuit_open"
	RoutingSkipRateLimited           RoutingSkipReason = "rate_limited"
	RoutingSkipWebSocketDisabled     RoutingSkipReason = "websocket_disabled"
	// RoutingSkipWebSocketUnsupported: the provider cannot serve this model
	// over a native Responses WebSocket.
	RoutingSkipWebSocketUnsupported RoutingSkipReason = "websocket_unsupported"
)

func (reason RoutingSkipReason) Valid() bool {
	switch reason {
	case RoutingSkipDisabled, RoutingSkipNotConnected, RoutingSkipRiskPaused, RoutingSkipModelNotListed,
		RoutingSkipProtocolUnsupported, RoutingSkipStreamingUnsupported,
		RoutingSkipConversionUnavailable, RoutingSkipCircuitOpen, RoutingSkipRateLimited,
		RoutingSkipWebSocketDisabled, RoutingSkipWebSocketUnsupported:
		return true
	default:
		return false
	}
}

type RoutingSkip struct {
	ServiceID ServiceID         `json:"service_id"`
	Reason    RoutingSkipReason `json:"reason"`
}

// RequestRoutingDecision explains the provider choice of one attempt.
// Selected is empty when no provider could be selected. Skipped lists, in
// priority order, the providers ranked ahead of the selected one (every
// provider when none was selected) that routing excluded; providers that were
// tried and rejected appear as routed events instead.
type RequestRoutingDecision struct {
	Selected RoutingSelection `json:"selected,omitempty"`
	Skipped  []RoutingSkip    `json:"skipped"`
}

func (decision RequestRoutingDecision) Validate() error {
	if decision.Selected != "" && !decision.Selected.Valid() {
		return fmt.Errorf("unknown routing_decision.selected %q", decision.Selected)
	}
	if len(decision.Skipped) > MaxRoutingSkips {
		return fmt.Errorf("routing_decision.skipped must contain at most %d providers", MaxRoutingSkips)
	}
	for _, skip := range decision.Skipped {
		if err := skip.ServiceID.Validate(); err != nil {
			return fmt.Errorf("routing_decision.skipped: %w", err)
		}
		if !skip.Reason.Valid() {
			return fmt.Errorf("unknown routing_decision skip reason %q", skip.Reason)
		}
	}
	return nil
}
