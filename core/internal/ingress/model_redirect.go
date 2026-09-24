package ingress

import (
	"context"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
)

// applyModelRedirect chooses the routing model for one request. Model keeps
// the client's id; only routing and the upstream request use the target.
// A bound Responses WebSocket reuses its routing model for turns naming the
// same client model, so a rule edit never moves a live socket.
func (handler *Handler) applyModelRedirect(
	ctx context.Context,
	session *recordSession,
	classified Request,
	settings contract.RoutingSettings,
) Request {
	routingModel := classified.Model
	turn := responsesWSTurnFromContext(ctx)
	if turn != nil && turn.session.serviceID != "" && turn.session.model == classified.Model && turn.session.routingModel != "" {
		routingModel = turn.session.routingModel
	} else if redirect, ok := contract.ResolveModelRedirect(settings.ModelRedirects, classified.Model); ok && redirect.To != "" {
		routingModel = redirect.To
	}
	if turn != nil {
		turn.routingModel = routingModel
	}
	if routingModel == classified.Model {
		return classified
	}
	classified.RedirectedModel = routingModel
	session.noteModelRedirect(ctx, classified.Model, routingModel)
	return classified
}

// redirectCandidates makes the redirect target explicit on candidates that
// did not name an upstream model, so alias rewriting sends the target and
// restores the client's model in the response.
func redirectCandidates(classified Request, candidates []endpoint.Resolved) []endpoint.Resolved {
	if classified.RedirectedModel == "" {
		return candidates
	}
	result := append([]endpoint.Resolved(nil), candidates...)
	for index := range result {
		if result[index].UpstreamModel == "" {
			result[index].UpstreamModel = classified.RedirectedModel
		}
	}
	return result
}
