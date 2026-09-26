package ingress

import (
	"slices"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
)

// routingTrace keeps what routing learned about every configured provider so
// each attempt's record can explain why it used its provider. All attempts of
// a request share it.
type routingTrace struct {
	// ranking is every provider in priority order with the reason routing
	// excluded it, if any.
	ranking []endpoint.RankedService
	// pinned was chosen ahead of the priority order for the reason pin.
	pin    contract.RoutingSelection
	pinned contract.ServiceID
	// tried lists providers in the order execution first picked them.
	tried []contract.ServiceID
}

// noteRoutingRanking starts explaining the provider choice. Requests whose
// resolver cannot rank providers get no routing decision.
func (session *recordSession) noteRoutingRanking(ranking []endpoint.RankedService) {
	if session == nil || ranking == nil {
		return
	}
	session.routing = &routingTrace{ranking: slices.Clone(ranking)}
}

// noteRoutingSkip records why a later routing stage excluded a provider.
func (session *recordSession) noteRoutingSkip(id contract.ServiceID, reason contract.RoutingSkipReason) {
	if session == nil || session.routing == nil {
		return
	}
	endpoint.MarkSkipped(session.routing.ranking, id, reason)
}

// noteRoutingPin records a provider chosen ahead of the priority order.
func (session *recordSession) noteRoutingPin(selection contract.RoutingSelection, id contract.ServiceID) {
	if session == nil || session.routing == nil || id == "" {
		return
	}
	session.routing.pin, session.routing.pinned = selection, id
}

// noteRoutingTried records that execution picked a provider.
func (session *recordSession) noteRoutingTried(id contract.ServiceID) {
	if session == nil || session.routing == nil || id == "" || slices.Contains(session.routing.tried, id) {
		return
	}
	session.routing.tried = append(session.routing.tried, id)
}

// routingDecision explains the current attempt's provider. Until a provider
// is chosen only a finished request has one, listing every exclusion.
func (session *recordSession) routingDecision() *contract.RequestRoutingDecision {
	trace := session.routing
	if trace == nil {
		return nil
	}
	decision := contract.RequestRoutingDecision{Skipped: []contract.RoutingSkip{}}
	var selected contract.ServiceID
	if session.endpointID != nil {
		selected = *session.endpointID
		switch {
		case slices.Index(trace.tried, selected) > 0:
			decision.Selected = contract.RoutingSelectionFailover
		case selected == trace.pinned:
			decision.Selected = trace.pin
		default:
			decision.Selected = contract.RoutingSelectionPriority
		}
	} else if session.status == contract.RequestStatusPending {
		return nil
	}
	for _, ranked := range trace.ranking {
		if ranked.ServiceID == selected || len(decision.Skipped) == contract.MaxRoutingSkips {
			break
		}
		if ranked.Skip != "" {
			decision.Skipped = append(decision.Skipped, contract.RoutingSkip{ServiceID: ranked.ServiceID, Reason: ranked.Skip})
		}
	}
	return &decision
}
