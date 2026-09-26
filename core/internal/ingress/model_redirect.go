package ingress

import (
	"context"

	"github.com/QuantumNous/astrlink/core/contract"
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
