package ingress

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

// beginExtensions starts the extension points of apiVersion 2 plugins for a
// request a client sent, and runs the entry hooks. It reports true when a
// plugin refused the request, which then has its answer.
func (handler *Handler) beginExtensions(writer http.ResponseWriter, request *http.Request, classified Request, session *recordSession) bool {
	if handler.plugins == nil || session == nil || len(classified.body) == 0 {
		return false
	}
	ctx := request.Context()
	if builtinInternalFrom(ctx) != nil {
		return false
	}
	switch classified.Protocol {
	case contract.ProtocolAnthropicMessages, contract.ProtocolOpenAIChat,
		contract.ProtocolOpenAIResponses, contract.ProtocolGoogleGenerateContent:
	default:
		return false
	}
	turn := 0
	if session.turn != nil {
		turn = session.turn.Index
	}
	accessTokenID, _ := AccessTokenIDFromContext(ctx)
	meta := map[string]plugin.MetaValue{}
	for key, value := range session.metadata.Values {
		meta[key] = plugin.MetaValue{Value: value.Value, Source: value.Source}
	}
	extensions := handler.plugins.Extend(pluginhost.ExtensionInput{
		RequestID: session.id, Protocol: classified.Protocol, Model: classified.Model,
		Client: string(session.clientType), Stream: classified.Streaming,
		Session: string(session.sessionID), Turn: turn, Meta: meta, BodyBytes: len(classified.body),
		View: handler.pluginBodyView(session, classified, accessTokenID),
	})
	if extensions == nil {
		return false
	}
	session.extensions = extensions
	decision := extensions.OnRequest(ctx, classified.body)
	session.notePlugins(extensions.Records(), nil)
	if !decision.Deny {
		return false
	}
	session.captureUnreadRequestBody(request)
	writeInferenceError(writer, decision.Status, decision.Code, decision.Message, false, nil)
	session.noteFailed(errorSummaryFromInference(decision.Code, decision.Message, false))
	return true
}

// pluginBodyView shows a plugin the user does not trust a body as the
// privacy check of an upstream nobody trusts leaves it, under the policy of
// the request and with its conversation's placeholders. nil when no policy
// can apply.
func (handler *Handler) pluginBodyView(session *recordSession, classified Request, accessTokenID contract.AccessTokenID) func(context.Context, contract.ProtocolID, []byte) (pluginhost.BodyView, error) {
	filter := handler.privacyFilter
	if filter == nil {
		return nil
	}
	scope := privacy.Scope{Protocol: classified.Protocol, Model: classified.Model, AccessTokenID: accessTokenID}
	conversation := string(session.sessionID)
	return func(ctx context.Context, protocol contract.ProtocolID, body []byte) (pluginhost.BodyView, error) {
		policy, err := filter.ResolvePolicy(ctx, scope)
		if err != nil {
			return pluginhost.BodyView{}, err
		}
		if !policy.Enabled {
			return pluginhost.BodyView{Body: body}, nil
		}
		result, err := filter.Inspect(privacy.WithConversation(ctx, conversation), policy, protocol, body)
		if err != nil {
			return pluginhost.BodyView{}, err
		}
		switch result.Decision {
		case privacy.DecisionAllow, privacy.DecisionWarn:
			return pluginhost.BodyView{Body: body}, nil
		case privacy.DecisionRedact:
			if len(result.Redactions) == 0 {
				return pluginhost.BodyView{Body: body}, nil
			}
			return pluginhost.BodyView{Body: result.Body, Restore: placeholderRestorer(result.Redactions)}, nil
		default:
			return pluginhost.BodyView{Blocked: true}, nil
		}
	}
}

// placeholderRestorer puts the originals back for the placeholders of
// redactions in a body a plugin made from the redacted one. The plugin's
// JSON may have escaped the < and > of a token placeholder.
func placeholderRestorer(redactions []privacy.Redaction) func([]byte) []byte {
	escaper := strings.NewReplacer("<", `<`, ">", `>`)
	var escaped []privacy.Redaction
	for _, redaction := range redactions {
		if alternative := escaper.Replace(redaction.Placeholder); alternative != redaction.Placeholder {
			redaction.Placeholder = alternative
			escaped = append(escaped, redaction)
		}
	}
	copied := append([]privacy.Redaction(nil), redactions...)
	return func(body []byte) []byte {
		return privacy.RestorePlaceholders(privacy.RestorePlaceholders(body, copied), escaped)
	}
}

// routeExtensions lets the route hooks reorder and exclude the request's
// candidates, or change its model. A new model resolves the candidates once
// more.
func (handler *Handler) routeExtensions(ctx context.Context, session *recordSession, classified Request, resolve endpoint.ResolveRequest, candidates []endpoint.Resolved) (Request, []endpoint.Resolved) {
	extensions := session.extensions
	if !extensions.Serves(plugin.HookRoute, plugin.CapabilityRouting) || len(candidates) == 0 {
		return classified, candidates
	}
	decision := extensions.Route(ctx, routeCandidates(classified, candidates), classified.body)
	session.notePlugins(extensions.Records(), nil)
	if decision.Model != "" {
		resolve.Model = decision.Model
		if resolved, err := handler.resolveCandidates(ctx, resolve); err == nil && len(resolved) > 0 {
			classified.RedirectedModel = decision.Model
			extensions.Routed(decision.Model)
			return classified, resolved
		}
	}
	return classified, reorderCandidates(candidates, decision.Order)
}

// routeCandidates are the candidates as route sees them, by the provider
// they go to.
func routeCandidates(classified Request, candidates []endpoint.Resolved) []pluginhost.RouteCandidate {
	routed := make([]pluginhost.RouteCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		service := candidate.CanonicalService()
		model := candidate.UpstreamModel
		if model == "" {
			model = classified.routingModel()
		}
		routed = append(routed, pluginhost.RouteCandidate{ID: string(service.ID), Name: service.Name, Kind: string(service.Kind), Model: model})
	}
	return routed
}

// reorderCandidates puts the candidates in the order of their providers'
// IDs and leaves out those order does not name.
func reorderCandidates(candidates []endpoint.Resolved, order []string) []endpoint.Resolved {
	if len(order) == 0 {
		return candidates
	}
	reordered := make([]endpoint.Resolved, 0, len(candidates))
	for _, id := range order {
		for _, candidate := range candidates {
			if string(candidate.CanonicalService().ID) == id {
				reordered = append(reordered, candidate)
			}
		}
	}
	if len(reordered) == 0 {
		return candidates
	}
	return reordered
}

// forwardExtensions runs the beforeForward hooks over the body every attempt
// sends.
func (session *recordSession) forwardExtensions(ctx context.Context, body *requestBodySource) {
	if session == nil || !session.extensions.Serves(plugin.HookBeforeForward) {
		return
	}
	defer func() { session.notePlugins(session.extensions.Records(), nil) }()
	if !body.Replayable() {
		return
	}
	data, err := body.bytes()
	if err != nil {
		return
	}
	if edited := session.extensions.BeforeForward(ctx, data); string(edited) != string(data) {
		body.rewrite(edited)
	}
}

// upstreamExtensions runs the beforeUpstream hooks over the body one
// attempt sends, in its provider's protocol, and adds the headers they set.
func (session *recordSession) upstreamExtensions(ctx context.Context, attempt *http.Request, candidate endpoint.Resolved, protocol contract.ProtocolID, model string, index int) {
	if session == nil || !session.extensions.Serves(plugin.HookBeforeUpstream, plugin.CapabilityRequestWriteRaw) {
		return
	}
	defer func() { session.notePlugins(session.extensions.Records(), nil) }()
	data, err := io.ReadAll(attempt.Body)
	if err != nil {
		return
	}
	service := candidate.CanonicalService()
	edited, headers := session.extensions.BeforeUpstream(ctx, pluginhost.UpstreamAttempt{
		Provider: service.Name, Kind: string(service.Kind), Protocol: string(protocol), Model: model, Index: index,
	}, data)
	replaceRecoveryRequestBody(attempt, edited)
	for name, value := range headers {
		attempt.Header.Set(name, value)
	}
}

// PluginHTTPPath is where plugins granted http.serve answer on the
// inference listener: /plugins/<key>/….
const PluginHTTPPath = "/plugins/"

// servePluginHTTP answers /plugins/<key>/… with the plugin's onHttp hook.
// It comes after the inference listener's access check, so it takes the
// same credentials as inference.
func (handler *Handler) servePluginHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	notFound := func() {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(writer, "no such plugin endpoint\n")
	}
	if handler.plugins == nil {
		notFound()
		return
	}
	key, rest, _ := strings.Cut(strings.TrimPrefix(request.URL.Path, PluginHTTPPath), "/")
	if !contract.ValidPluginKey(key) {
		notFound()
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, pluginhost.MaxPluginHTTPBytes+1))
	if err != nil {
		http.Error(writer, "the request could not be read", http.StatusBadRequest)
		return
	}
	if len(body) > pluginhost.MaxPluginHTTPBytes {
		http.Error(writer, "the request is too large", http.StatusRequestEntityTooLarge)
		return
	}
	call := pluginhost.PluginHTTPRequest{Method: request.Method, Path: "/" + rest, Query: map[string]string{}, Headers: map[string]string{}}
	for name, values := range request.URL.Query() {
		if len(values) > 0 {
			call.Query[name] = values[0]
		}
	}
	for name, values := range request.Header {
		lower := strings.ToLower(name)
		switch lower {
		case "authorization", "cookie", "x-api-key", "x-goog-api-key", "proxy-authorization":
			continue
		}
		if len(values) > 0 {
			call.Headers[lower] = values[0]
		}
	}
	if len(body) > 0 {
		if json.Valid(body) {
			call.JSON = body
		} else {
			call.Body = strings.ToValidUTF8(string(body), "�")
		}
	}
	response, found := handler.plugins.ServePluginHTTP(request.Context(), key, call)
	if !found {
		notFound()
		return
	}
	for name, value := range response.Headers {
		writer.Header().Set(name, value)
	}
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(response.Status)
	_, _ = writer.Write(response.Body)
}
