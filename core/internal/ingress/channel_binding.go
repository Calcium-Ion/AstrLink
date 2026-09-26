package ingress

import (
	"context"
	"net/http"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type channelBindingAttempt struct {
	store  storage.ChannelBindingStore
	scope  contract.ChannelBindingScope
	ttl    time.Duration
	source string
}

// Consume the SAME session resolution as request records (including echo ids
// and keyed fingerprints). Binding isolation is stricter than record grouping:
// two principals sharing an explicit conversation id never share preferences.
func (handler *Handler) prepareChannelBinding(session *recordSession, settings contract.RoutingSettings, loaded bool) *channelBindingAttempt {
	if session == nil || session.classified.Protocol.IsModelDiscovery() {
		return nil
	}
	if _, ok := convoProtocol(session.classified.Protocol); !ok {
		return nil
	}
	store, ok := handler.requestRecords.(storage.ChannelBindingStore)
	if !ok {
		return nil
	}
	if !loaded || settings.ChannelStickiness == nil || !settings.ChannelStickiness.Enabled {
		return nil
	}
	protocol := session.classified.Protocol
	if protocol == contract.ProtocolOpenAIResponsesCompact {
		protocol = contract.ProtocolOpenAIResponses
	}
	principal := ""
	if session.accessTokenID != nil {
		principal = string(*session.accessTokenID)
	}
	source := "new"
	if session.sessionLink != nil {
		source = string(session.sessionLink.Kind)
	}
	return &channelBindingAttempt{store: store, scope: contract.ChannelBindingScope{SessionID: session.sessionID, Principal: principal, Protocol: protocol, Model: session.classified.routingModel()}, ttl: time.Duration(settings.ChannelStickiness.TTLSeconds) * time.Second, source: source}
}

// loadRoutingSettings reads the persisted routing settings for one request.
// A store without routing settings means defaults (loaded=false). A failed
// read is returned, like the resolver's own read, so callers can fail closed
// instead of silently skipping model redirects.
func (handler *Handler) loadRoutingSettings(ctx context.Context) (contract.RoutingSettings, bool, error) {
	settingsStore, ok := handler.requestRecords.(storage.RoutingSettingsStore)
	if !ok {
		return contract.RoutingSettings{}, false, nil
	}
	settings, err := settingsStore.GetRoutingSettings(ctx)
	if err != nil {
		return contract.RoutingSettings{}, false, err
	}
	return settings, true, nil
}

func (handler *Handler) preferChannelBinding(request *http.Request, session *recordSession, candidates []endpoint.Resolved) []endpoint.Resolved {
	attempt := session.channelBinding
	if attempt == nil || len(candidates) == 0 {
		return candidates
	}
	ctx, cancel := context.WithTimeout(request.Context(), 100*time.Millisecond)
	defer cancel()
	binding, found, err := attempt.store.GetChannelBinding(ctx, attempt.scope)
	if err != nil {
		logRequestRecordFailure(handler.recordLogger, "channel_binding_lookup", err)
		return candidates
	}
	event := contract.ChannelBindingEvent{ChannelBindingScope: attempt.scope, Action: "miss", Reason: "no_binding", Source: attempt.source, RequestID: session.id}
	strict := session.classified.PreviousResponseID != ""
	if turn := responsesWSTurnFromContext(request.Context()); turn != nil && turn.session.serviceID != "" {
		strict = true
	}
	switch {
	case strict:
		event.Action, event.Reason, event.ServiceID = "strict", "protocol_binding", candidates[0].CanonicalService().ID
	case found && !binding.ExpiresAt.After(time.Now()):
		event.Reason, event.PreviousServiceID = "expired", binding.ServiceID
	case found:
		event.Reason, event.PreviousServiceID = "unavailable", binding.ServiceID
		for i, candidate := range candidates {
			if candidate.CanonicalService().ID != binding.ServiceID {
				continue
			}
			// Stable promotion preserves the configured order of all other services.
			candidates = append([]endpoint.Resolved(nil), candidates...)
			copy(candidates[1:i+1], candidates[:i])
			candidates[0] = candidate
			event.Action, event.Reason, event.ServiceID = "hit", "session_match", binding.ServiceID
			if i > 0 {
				// Only a promotion overrides the priority order.
				session.noteRoutingPin(contract.RoutingSelectionSessionBinding, binding.ServiceID)
			}
			break
		}
	}
	if err := attempt.store.RecordChannelBindingEvent(ctx, event); err != nil {
		logRequestRecordFailure(handler.recordLogger, "channel_binding_audit", err)
	}
	return candidates
}

func (handler *Handler) rememberChannelBinding(ctx context.Context, session *recordSession, candidate endpoint.Resolved) {
	if session == nil || session.channelBinding == nil || ctx.Err() != nil {
		return
	}
	attempt := session.channelBinding
	now := time.Now().UTC()
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Millisecond)
	defer cancel()
	if err := attempt.store.RememberChannelBinding(persist, contract.ChannelBinding{ChannelBindingScope: attempt.scope, ServiceID: candidate.CanonicalService().ID, Source: attempt.source, RequestID: session.id, UpdatedAt: now, ExpiresAt: now.Add(attempt.ttl)}, session.startedAt); err != nil {
		logRequestRecordFailure(handler.recordLogger, "channel_binding_write", err)
	}
}
