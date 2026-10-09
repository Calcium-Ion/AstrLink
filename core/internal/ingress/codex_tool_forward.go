package ingress

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// codexToolProvider returns the provider that served the Codex turn ref
// names, while it can still be scheduled and offers the tool itself. A Codex
// subscription serves both tools, as it does for the official client signed
// in with ChatGPT. A provider's own image API serves images only: other
// providers have no /alpha/search, and a failed search ends the whole turn.
func (handler *Handler) codexToolProvider(ctx context.Context, ref codexTurnRef, kind string) (endpoint.Resolved, codexTurnBinding, bool) {
	principal, _ := AccessTokenIDFromContext(ctx)
	binding, found := handler.codexTurns.lookup(string(principal), ref, time.Now())
	resolver, ok := handler.resolver.(endpoint.ServiceResolver)
	if !found || !ok {
		return endpoint.Resolved{}, codexTurnBinding{}, false
	}
	candidate, err := resolver.ResolveService(ctx, binding.service)
	if err != nil {
		return endpoint.Resolved{}, codexTurnBinding{}, false
	}
	switch service := candidate.CanonicalService(); {
	case service.Kind == contract.ServiceKindCodexSubscription:
	case kind == "image_generation" && contract.BuiltinImagesServiceKind(service.Kind):
	default:
		return endpoint.Resolved{}, codexTurnBinding{}, false
	}
	return candidate, binding, true
}

// codexToolModel applies the routing redirect rules, then the provider's own
// model mapping, to the model a Codex tool request names.
func codexToolModel(settings contract.RoutingSettings, service contract.Service, model string) string {
	if redirect, ok := contract.ResolveModelRedirect(settings.ModelRedirects, model); ok && redirect.To != "" {
		model = redirect.To
	}
	if served, ok := service.UpstreamModelFor(model); ok {
		model = served
	}
	return model
}

var (
	errCodexToolProxy       = errors.New("the provider's proxy is unavailable")
	errCodexToolConfig      = errors.New("the provider's configuration is invalid")
	errCodexToolCredential  = errors.New("the provider's credential is unavailable")
	errCodexToolUnreachable = errors.New("the provider could not be reached")
)

// forwardCodexTool sends one of Codex's own image or search requests to the
// Codex subscription that served the turn, at the path the official client
// uses. It carries the identity the turn was forwarded with, and it is sent
// once: never retried, never to another account. handle reads the response.
func (handler *Handler) forwardCodexTool(
	writer http.ResponseWriter,
	request *http.Request,
	candidate endpoint.Resolved,
	binding codexTurnBinding,
	settings contract.RoutingSettings,
	body []byte,
	handle func(*http.Response) error,
) error {
	class := accountauth.ClientClassThirdParty
	if binding.official && settings.OfficialClientPassthrough && accountauth.RecognizedCodexToolClient(request.Header) {
		class = accountauth.ClientClassOfficial
	}
	ctx, err := networkproxy.Bind(accountauth.WithClientClass(request.Context(), class), candidate.Service, handler.proxyCredentials)
	if err != nil {
		return errCodexToolProxy
	}
	authorization, err := candidate.AuthorizationEndpoint()
	if err != nil {
		return errCodexToolConfig
	}
	baseURL, err := url.Parse(candidate.EffectiveBaseURL())
	if err != nil {
		return errCodexToolConfig
	}
	outbound := request.Clone(ctx)
	replaceRecoveryRequestBody(outbound, body)
	outbound.Header.Del("Content-Encoding")
	// The subscription's API root already ends where /v1 would.
	path := *outbound.URL
	path.Path = strings.TrimPrefix(path.Path, "/v1")
	path.RawPath = ""
	outbound.URL = &path
	headers, err := handler.authorizer.Headers(ctx, authorization, outbound.Header)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errCodexToolCredential
	}
	responded := false
	err = handler.forwarder.Forward(writer, outbound, transport.Target{
		Service: candidate.Service, ProxyCredentials: handler.proxyCredentials,
		BaseURL: baseURL, RequestHeaders: headers,
		HandleResponse: func(response *http.Response) error {
			responded = true
			return handle(response)
		},
	})
	if err != nil && !responded && ctx.Err() == nil {
		return errCodexToolUnreachable
	}
	return err
}

// readCodexToolResponse reads at most limit bytes of a response body.
func readCodexToolResponse(response *http.Response, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("the provider's response exceeds the size limit or was interrupted")
	}
	return data, nil
}
