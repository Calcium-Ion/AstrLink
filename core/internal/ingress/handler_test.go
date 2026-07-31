package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

type resolverFunc func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error)

func (function resolverFunc) Resolve(ctx context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
	return function(ctx, request)
}

type candidateResolver struct {
	candidates []endpoint.Resolved
	err        error
}

func (resolver candidateResolver) Resolve(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
	if resolver.err != nil {
		return endpoint.Resolved{}, resolver.err
	}
	if len(resolver.candidates) == 0 {
		return endpoint.Resolved{}, endpoint.ErrNoEndpoint
	}
	return resolver.candidates[0], nil
}

func (resolver candidateResolver) ResolveCandidates(context.Context, endpoint.ResolveRequest) ([]endpoint.Resolved, error) {
	if resolver.err != nil {
		return nil, resolver.err
	}
	return append([]endpoint.Resolved(nil), resolver.candidates...), nil
}

type healthRecordingResolver struct {
	candidate endpoint.Resolved
	begins    chan struct{}
	successes chan struct{}
	failures  chan struct{}
	abandons  chan struct{}
}

func (resolver *healthRecordingResolver) Resolve(
	context.Context,
	endpoint.ResolveRequest,
) (endpoint.Resolved, error) {
	return resolver.candidate, nil
}

func (resolver *healthRecordingResolver) ResolveCandidates(
	context.Context,
	endpoint.ResolveRequest,
) ([]endpoint.Resolved, error) {
	return []endpoint.Resolved{resolver.candidate}, nil
}

func (resolver *healthRecordingResolver) BeginAttempt(endpoint.Resolved) bool {
	resolver.begins <- struct{}{}
	return true
}

func (resolver *healthRecordingResolver) RecordSuccess(endpoint.Resolved) {
	resolver.successes <- struct{}{}
}

func (resolver *healthRecordingResolver) RecordFailure(endpoint.Resolved) {
	resolver.failures <- struct{}{}
}

func (resolver *healthRecordingResolver) AbandonAttempt(endpoint.Resolved) {
	resolver.abandons <- struct{}{}
}

type healthTrackingCandidateResolver struct {
	candidateResolver
	mu        sync.Mutex
	successes []contract.ServiceID
	failures  []contract.ServiceID
	abandons  []contract.ServiceID
}

func (resolver *healthTrackingCandidateResolver) BeginAttempt(endpoint.Resolved) bool {
	return true
}

func (resolver *healthTrackingCandidateResolver) RecordSuccess(candidate endpoint.Resolved) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	resolver.successes = append(resolver.successes, candidate.Endpoint.ID)
}

func (resolver *healthTrackingCandidateResolver) RecordFailure(candidate endpoint.Resolved) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	resolver.failures = append(resolver.failures, candidate.Endpoint.ID)
}

func (resolver *healthTrackingCandidateResolver) AbandonAttempt(candidate endpoint.Resolved) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	resolver.abandons = append(resolver.abandons, candidate.Endpoint.ID)
}

type endpointPageReader struct {
	items []storage.EndpointRecord
}

func (reader endpointPageReader) ListEndpoints(
	context.Context,
	storage.EndpointListOptions,
) (storage.EndpointPage, error) {
	return storage.EndpointPage{Items: append([]storage.EndpointRecord(nil), reader.items...)}, nil
}

type authorizerFunc func(context.Context, contract.Endpoint) (http.Header, error)

func (function authorizerFunc) Headers(ctx context.Context, endpoint contract.Endpoint) (http.Header, error) {
	return function(ctx, endpoint)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type forwarderFunc func(http.ResponseWriter, *http.Request, transport.Target) error

func (function forwarderFunc) Forward(writer http.ResponseWriter, request *http.Request, target transport.Target) error {
	// Mirror production transport.Forward so attempt bookkeeping runs under
	// test doubles. Response-body tees are covered by transport.New tests.
	if target.ObserveOutbound != nil {
		target.ObserveOutbound(request)
	}
	return function(writer, request, target)
}

func TestDefaultInferencePlaneRecognizesRoutesButFailsClosedWithoutEndpointConfiguration(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5"}`))
	response := httptest.NewRecorder()

	New().ServeHTTP(response, request)

	assertInferenceError(t, response, http.StatusServiceUnavailable, "endpoint_resolver_unavailable")
}

func TestInferencePlaneReturnsStructuredBoundaryErrors(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		status     int
		code       string
		wantAllow  string
		resolveErr error
		body       string
	}{
		{name: "unknown path", method: http.MethodPost, path: "/v1/embeddings", status: http.StatusNotFound, code: "not_found"},
		{name: "wrong method", method: http.MethodGet, path: "/v1/responses", status: http.StatusMethodNotAllowed, code: "method_not_allowed", wantAllow: http.MethodPost},
		{name: "missing capability", method: http.MethodPost, path: "/v1/messages", status: http.StatusUnprocessableEntity, code: "missing_protocol_capability", resolveErr: endpoint.ErrNoEndpoint},
		{name: "resolver failure", method: http.MethodPost, path: "/v1/chat/completions", status: http.StatusServiceUnavailable, code: "endpoint_resolver_unavailable", resolveErr: errors.New("database detail must stay private")},
		{name: "invalid metadata", method: http.MethodPost, path: "/v1/responses", status: http.StatusBadRequest, code: "invalid_request", body: `{"stream":`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewWithDependencies(Dependencies{
				Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
					return endpoint.Resolved{}, test.resolveErr
				}),
			})
			body := test.body
			if body == "" {
				body = `{}`
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, strings.NewReader(body)))

			assertInferenceError(t, response, test.status, test.code)
			if got := response.Header().Get("Allow"); got != test.wantAllow {
				t.Fatalf("Allow = %q, want %q", got, test.wantAllow)
			}
			if strings.Contains(response.Body.String(), "database detail") {
				t.Fatalf("private resolver error leaked: %s", response.Body.String())
			}
		})
	}
}

func TestInferencePlaneRejectsBrowserOriginsAndSimpleRequestMediaTypes(t *testing.T) {
	tests := []struct {
		name        string
		origin      string
		contentType string
		status      int
		code        string
	}{
		{name: "browser origin", origin: "https://attacker.example", contentType: "application/json", status: http.StatusForbidden, code: "origin_forbidden"},
		{name: "simple text request", contentType: "text/plain;charset=UTF-8", status: http.StatusUnsupportedMediaType, code: "unsupported_media_type"},
		{name: "HTML form request", contentType: "application/x-www-form-urlencoded", status: http.StatusUnsupportedMediaType, code: "unsupported_media_type"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
			request.Header.Set("Content-Type", test.contentType)
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			response := httptest.NewRecorder()
			New().ServeHTTP(response, request)
			assertInferenceError(t, response, test.status, test.code)
		})
	}
}

func TestProductionInferenceGateRequiresCanonicalHostOriginBoundaryAndLocalToken(t *testing.T) {
	const token = "astr_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	resolveCalls := 0
	handler, err := NewProduction(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			resolveCalls++
			return endpoint.Resolved{}, endpoint.ErrNoEndpoint
		}),
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
			return nil, errors.New("authorizer must not run without an endpoint")
		}),
		AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(_ context.Context, presented string) (contract.AccessTokenID, error) {
			if presented == token {
				return "token_primary", nil
			}
			return "", errors.New("not found")
		}),
		AllowedHost: "127.0.0.1:8317",
	})
	if err != nil {
		t.Fatalf("NewProduction: %v", err)
	}
	tests := []struct {
		name       string
		host       string
		origin     string
		origins    []string
		path       string
		headerName string
		header     string
		status     int
		code       string
		resolves   bool
	}{
		{name: "wrong Host", host: "localhost:8317", headerName: "Authorization", header: "Bearer " + token, status: http.StatusMisdirectedRequest, code: "host_forbidden"},
		{name: "browser Origin", host: "127.0.0.1:8317", origin: "https://attacker.example", headerName: "Authorization", header: "Bearer " + token, status: http.StatusForbidden, code: "origin_forbidden"},
		{name: "browser Origin after empty value", host: "127.0.0.1:8317", origins: []string{"", "https://attacker.example"}, headerName: "Authorization", header: "Bearer " + token, status: http.StatusForbidden, code: "origin_forbidden"},
		{name: "missing token", host: "127.0.0.1:8317", status: http.StatusUnauthorized, code: "invalid_access_token"},
		{name: "wrong token", host: "127.0.0.1:8317", headerName: "Authorization", header: "Bearer wrong_token_0123456789abcdef", status: http.StatusUnauthorized, code: "invalid_access_token"},
		{name: "ambiguous token", host: "127.0.0.1:8317", headerName: "Authorization", header: "Bearer " + token, status: http.StatusUnauthorized, code: "invalid_access_token"},
		{name: "query token", host: "127.0.0.1:8317", path: "?key=" + token, headerName: "X-Goog-Api-Key", header: token, status: http.StatusUnauthorized, code: "token_query_forbidden"},
		{name: "Bearer token", host: "127.0.0.1:8317", headerName: "Authorization", header: "Bearer " + token, status: http.StatusUnprocessableEntity, code: "missing_protocol_capability", resolves: true},
		{name: "Anthropic token", host: "127.0.0.1:8317", headerName: "X-Api-Key", header: token, status: http.StatusUnprocessableEntity, code: "missing_protocol_capability", resolves: true},
		{name: "Google token", host: "127.0.0.1:8317", headerName: "X-Goog-Api-Key", header: token, status: http.StatusUnprocessableEntity, code: "missing_protocol_capability", resolves: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := resolveCalls
			request := httptest.NewRequest(http.MethodPost, "/v1/responses"+test.path, strings.NewReader(`{}`))
			request.Host = test.host
			request.Header.Set("Content-Type", "application/json")
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.origins != nil {
				request.Header["Origin"] = test.origins
			}
			if test.headerName != "" {
				request.Header.Set(test.headerName, test.header)
			}
			if test.name == "ambiguous token" {
				request.Header.Set("X-Api-Key", token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assertInferenceError(t, response, test.status, test.code)
			if got := resolveCalls > before; got != test.resolves {
				t.Fatalf("resolver called=%t, want %t", got, test.resolves)
			}
			for name := range response.Header() {
				if strings.HasPrefix(http.CanonicalHeaderKey(name), "Access-Control-") {
					t.Fatalf("CORS header leaked: %s", name)
				}
			}
		})
	}
}

func TestNewProductionRejectsMissingSecurityDependencies(t *testing.T) {
	valid := Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{}, endpoint.ErrNoEndpoint
		}),
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) { return nil, nil }),
		AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(context.Context, string) (contract.AccessTokenID, error) {
			return "token_primary", nil
		}),
		AllowedHost: "127.0.0.1:8317",
	}
	tests := []Dependencies{
		{Authorizer: valid.Authorizer, AccessTokenAuthenticator: valid.AccessTokenAuthenticator, AllowedHost: valid.AllowedHost},
		{Resolver: valid.Resolver, AccessTokenAuthenticator: valid.AccessTokenAuthenticator, AllowedHost: valid.AllowedHost},
		{Resolver: valid.Resolver, Authorizer: valid.Authorizer, AllowedHost: valid.AllowedHost},
		{Resolver: valid.Resolver, Authorizer: valid.Authorizer, AccessTokenAuthenticator: valid.AccessTokenAuthenticator, AllowedHost: "localhost:8317"},
		{Resolver: valid.Resolver, Authorizer: valid.Authorizer, AccessTokenAuthenticator: valid.AccessTokenAuthenticator, AllowedHost: "127.0.0.1:0"},
		{Resolver: valid.Resolver, Authorizer: valid.Authorizer, AccessTokenAuthenticator: valid.AccessTokenAuthenticator, AllowedHost: "127.0.0.1:not-a-port"},
		{Resolver: valid.Resolver, Authorizer: valid.Authorizer, AccessTokenAuthenticator: valid.AccessTokenAuthenticator, AllowedHost: "127.0.0.1:65536"},
	}
	for index, dependencies := range tests {
		if _, err := NewProduction(dependencies); err == nil {
			t.Errorf("case %d accepted incomplete production gate", index)
		}
	}
}

func TestProductionInferenceGateAuthenticatesMultipleTokensAndAttachesStablePrincipal(t *testing.T) {
	active := map[string]contract.AccessTokenID{
		"astr_1111111111111111111111111111111111111111111": "token_one",
		"astr_2222222222222222222222222222222222222222222": "token_two",
	}
	var principal contract.AccessTokenID
	handler, err := NewProduction(Dependencies{
		Resolver: resolverFunc(func(ctx context.Context, _ endpoint.ResolveRequest) (endpoint.Resolved, error) {
			var ok bool
			principal, ok = AccessTokenIDFromContext(ctx)
			if !ok {
				t.Fatal("resolver context is missing access token principal")
			}
			return endpoint.Resolved{}, endpoint.ErrNoEndpoint
		}),
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
			return nil, errors.New("unexpected authorizer call")
		}),
		AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(_ context.Context, token string) (contract.AccessTokenID, error) {
			id, ok := active[token]
			if !ok {
				return "", errors.New("invalid token")
			}
			return id, nil
		}),
		AllowedHost: "127.0.0.1:8317",
	})
	if err != nil {
		t.Fatalf("NewProduction: %v", err)
	}

	for token, wantID := range active {
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8317/v1/responses", strings.NewReader(`{}`))
		request.Host = "127.0.0.1:8317"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		assertInferenceError(t, response, http.StatusUnprocessableEntity, "missing_protocol_capability")
		if principal != wantID {
			t.Fatalf("principal = %q, want %q", principal, wantID)
		}
	}

	deleted := "astr_1111111111111111111111111111111111111111111"
	delete(active, deleted)
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8317/v1/responses", strings.NewReader(`{}`))
	request.Host = "127.0.0.1:8317"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+deleted)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertInferenceError(t, response, http.StatusUnauthorized, "invalid_access_token")
}

func TestProductionInferenceGateDoesNotReauthenticateInFlightRequestAfterDeletion(t *testing.T) {
	const raw = "astr_3333333333333333333333333333333333333333333"
	active := map[string]contract.AccessTokenID{raw: "token_inflight"}
	resolverEntered := make(chan struct{})
	releaseResolver := make(chan struct{})
	handler, err := NewProduction(Dependencies{
		Resolver: resolverFunc(func(ctx context.Context, _ endpoint.ResolveRequest) (endpoint.Resolved, error) {
			if id, ok := AccessTokenIDFromContext(ctx); !ok || id != "token_inflight" {
				t.Errorf("principal=(%q,%t), want token_inflight", id, ok)
			}
			close(resolverEntered)
			<-releaseResolver
			return endpoint.Resolved{}, endpoint.ErrNoEndpoint
		}),
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
			return nil, errors.New("unexpected authorizer call")
		}),
		AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(_ context.Context, token string) (contract.AccessTokenID, error) {
			id, ok := active[token]
			if !ok {
				return "", errors.New("invalid token")
			}
			return id, nil
		}),
		AllowedHost: "127.0.0.1:8317",
	})
	if err != nil {
		t.Fatalf("NewProduction: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8317/v1/responses", strings.NewReader(`{}`))
	request.Host = "127.0.0.1:8317"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+raw)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(response, request)
	}()
	<-resolverEntered
	delete(active, raw)
	close(releaseResolver)
	<-done
	assertInferenceError(t, response, http.StatusUnprocessableEntity, "missing_protocol_capability")
}

func TestInferencePlaneRejectsEncodedJSONBeforeEndpointResolution(t *testing.T) {
	called := false
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			called = true
			return endpoint.Resolved{}, nil
		}),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("compressed"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Content-Encoding", "gzip")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assertInferenceError(t, response, http.StatusUnsupportedMediaType, "unsupported_content_encoding")
	if called {
		t.Fatal("resolver ran for an opaque encoded request")
	}
}

func TestInferencePlaneBuildsNativePlanAuthorizesAndForwardsOriginalProtocol(t *testing.T) {
	const originalBody = " {\n  \"model\": \"gpt-5\", \"stream\": true, \"input\": \"hello\"\n} "
	var resolvedRequest endpoint.ResolveRequest
	upstreamEndpoint := validEndpoint(contract.ProtocolOpenAIResponses, true)
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
			resolvedRequest = request
			return endpoint.Resolved{Endpoint: upstreamEndpoint}, nil
		}),
		Authorizer: authorizerFunc(func(_ context.Context, got contract.Endpoint) (http.Header, error) {
			if got.ID != upstreamEndpoint.ID {
				t.Fatalf("authorizer endpoint = %q", got.ID)
			}
			return http.Header{"Authorization": {"Bearer resolved-secret"}}, nil
		}),
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.String() != "https://upstream.example/prefix/v1/responses?trace=1" {
				t.Errorf("upstream URL = %q", request.URL.String())
			}
			if request.Header.Get("Authorization") != "Bearer resolved-secret" {
				t.Errorf("upstream Authorization = %q", request.Header.Get("Authorization"))
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != originalBody {
				t.Errorf("upstream body = %q, want exact %q", body, originalBody)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}, "X-Upstream": {"native"}},
				Body:       io.NopCloser(strings.NewReader(`{"id":"resp_01"}`)),
			}, nil
		})),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses?trace=1", strings.NewReader(originalBody))
	request.Header.Set("Authorization", "Bearer local-client-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != `{"id":"resp_01"}` {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Upstream") != "native" {
		t.Fatalf("upstream response header not preserved")
	}
	if resolvedRequest.Protocol != contract.ProtocolOpenAIResponses || resolvedRequest.Model != "gpt-5" || !resolvedRequest.Streaming {
		t.Fatalf("resolve request = %#v", resolvedRequest)
	}
}

func TestInferencePlaneExecutesExplicitDelegatedPlanWithoutLocalConversion(t *testing.T) {
	upstreamEndpoint := validEndpoint(contract.ProtocolOpenAIResponses, true)
	upstreamEndpoint.Kind = contract.EndpointKindNewAPI
	upstreamEndpoint.Capabilities[0].Mode = contract.CapabilityModeDelegated
	forwarded := false
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: upstreamEndpoint, Mode: contract.CapabilityModeDelegated}, nil
		}),
		Forwarder: forwarderFunc(func(_ http.ResponseWriter, request *http.Request, _ transport.Target) error {
			forwarded = true
			body, err := io.ReadAll(request.Body)
			if err != nil || string(body) != `{"model":"gpt-5"}` {
				t.Fatalf("delegated request body = %q, %v", body, err)
			}
			return nil
		}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5"}`)))
	if !forwarded || response.Code != http.StatusOK {
		t.Fatalf("delegated forwarded=%t response=%d %s", forwarded, response.Code, response.Body.String())
	}
}

func TestInferencePlaneRejectsMissingStreamingCapabilityBeforeRoundTrip(t *testing.T) {
	called := false
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
		}),
		Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
			called = true
			return nil
		}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":true}`)))

	assertInferenceError(t, response, http.StatusUnprocessableEntity, "missing_protocol_capability")
	if called {
		t.Fatal("transport ran without a streaming capability")
	}
}

func TestInferencePlaneCapabilityErrorsNameProtocolModeAndStreaming(t *testing.T) {
	tests := []struct {
		name            string
		resolver        endpoint.Resolver
		body            string
		wantModeReason  string
		wantPlanTypes   []string
		wantMessagePart string
	}{
		{
			name: "resolver has no native or delegated protocol path",
			resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
				return endpoint.Resolved{}, endpoint.ErrNoEndpoint
			}),
			body:            `{"model":"gpt-5","stream":true}`,
			wantModeReason:  "required mode=native or delegated; streaming=true",
			wantPlanTypes:   []string{"native", "delegated"},
			wantMessagePart: `protocol "openai.responses" in native or delegated mode with streaming=true`,
		},
		{
			name: "selected delegated mode lacks streaming",
			resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
				candidate := validEndpoint(contract.ProtocolOpenAIResponses, false)
				candidate.Capabilities[0].Mode = contract.CapabilityModeDelegated
				return endpoint.Resolved{
					Endpoint: candidate,
					Mode:     contract.CapabilityModeDelegated,
				}, nil
			}),
			body:            `{"model":"gpt-5","stream":true}`,
			wantModeReason:  "required mode=delegated; streaming=true",
			wantPlanTypes:   []string{"delegated"},
			wantMessagePart: `protocol "openai.responses" in delegated mode with streaming=true`,
		},
		{
			name: "route requires native mode",
			resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
				return endpoint.Resolved{}, &endpoint.CapabilityUnavailableError{
					Protocol: contract.ProtocolOpenAIResponses,
					Modes: []contract.CapabilityMode{
						contract.CapabilityModeNative,
					},
					Streaming: true,
				}
			}),
			body:            `{"model":"gpt-5","stream":true}`,
			wantModeReason:  "required mode=native; streaming=true",
			wantPlanTypes:   []string{"native"},
			wantMessagePart: `protocol "openai.responses" in native mode with streaming=true`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewWithDependencies(Dependencies{Resolver: test.resolver})
			response := httptest.NewRecorder()
			handler.ServeHTTP(
				response,
				httptest.NewRequest(
					http.MethodPost,
					"/v1/responses",
					strings.NewReader(test.body),
				),
			)

			envelope := assertInferenceError(
				t,
				response,
				http.StatusUnprocessableEntity,
				"missing_protocol_capability",
			)
			if !strings.Contains(envelope.Error.Message, test.wantMessagePart) {
				t.Fatalf("message = %q, want %q", envelope.Error.Message, test.wantMessagePart)
			}
			if len(envelope.Error.Details) != 1 {
				t.Fatalf("details = %#v", envelope.Error.Details)
			}
			detail := envelope.Error.Details[0]
			if detail.Protocol != string(contract.ProtocolOpenAIResponses) ||
				detail.Reason != test.wantModeReason ||
				strings.Join(detail.RequiredPlanTypes, ",") != strings.Join(test.wantPlanTypes, ",") {
				t.Fatalf("capability detail = %#v", detail)
			}
		})
	}
}

func TestInferencePlaneFailsClosedWhenCredentialCannotBeLoaded(t *testing.T) {
	upstreamEndpoint := validEndpoint(contract.ProtocolAnthropicMessages, true)
	upstreamEndpoint.Auth = contract.EndpointAuth{Scheme: contract.AuthSchemeAnthropicAPIKey}
	upstreamEndpoint.CredentialRef = "local://endpoint/endpoint_test"
	called := false
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: upstreamEndpoint}, nil
		}),
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
			return nil, errors.New("secret value must stay private")
		}),
		Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
			called = true
			return nil
		}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`)))

	assertInferenceError(t, response, http.StatusServiceUnavailable, "credential_unavailable")
	if called || strings.Contains(response.Body.String(), "secret value") {
		t.Fatalf("unsafe credential failure response: called=%t body=%s", called, response.Body.String())
	}
}

func TestInferencePlaneMapsUpstreamFailuresBeforeResponseStart(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "dial", err: errors.New("dial detail"), status: http.StatusBadGateway, code: "upstream_unavailable"},
		{name: "timeout", err: context.DeadlineExceeded, status: http.StatusGatewayTimeout, code: "upstream_timeout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewWithDependencies(Dependencies{
				Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
					return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIModels, false)}, nil
				}),
				Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, test.err
				})),
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
			assertInferenceError(t, response, test.status, test.code)
			if strings.Contains(response.Body.String(), "dial detail") {
				t.Fatalf("upstream detail leaked: %s", response.Body.String())
			}
		})
	}
}

func TestInferencePlaneFailsOverInDeterministicOrderWithExactBodyAndAuthorization(t *testing.T) {
	const originalBody = " {\n \"model\":\"gpt-5\", \"input\":\"preserve me\"\n} "
	tests := []struct {
		name         string
		endpointIDs  []contract.ServiceID
		succeedAt    int
		wantAttempts []contract.ServiceID
		wantStatus   int
		wantCode     string
	}{
		{
			name:         "first failure switches to second",
			endpointIDs:  []contract.ServiceID{"endpoint_a", "endpoint_b"},
			succeedAt:    1,
			wantAttempts: []contract.ServiceID{"endpoint_a", "endpoint_b"},
			wantStatus:   http.StatusOK,
		},
		{
			name:         "attempts are bounded at three",
			endpointIDs:  []contract.ServiceID{"endpoint_a", "endpoint_b", "endpoint_c", "endpoint_d"},
			succeedAt:    -1,
			wantAttempts: []contract.ServiceID{"endpoint_a", "endpoint_b", "endpoint_c"},
			wantStatus:   http.StatusBadGateway,
			wantCode:     "upstream_unavailable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidates := make([]endpoint.Resolved, 0, len(test.endpointIDs))
			hostToID := make(map[string]contract.ServiceID, len(test.endpointIDs))
			for _, id := range test.endpointIDs {
				candidate := validEndpoint(contract.ProtocolOpenAIResponses, false)
				candidate.ID = id
				candidate.Name = string(id)
				candidate.BaseURL = "https://" + string(id) + ".example"
				candidates = append(candidates, endpoint.Resolved{Endpoint: candidate})
				hostToID[string(id)+".example"] = id
			}

			var attempts []contract.ServiceID
			var bodies []string
			var authorizations []string
			handler := NewWithDependencies(Dependencies{
				Resolver: candidateResolver{candidates: candidates},
				Authorizer: authorizerFunc(func(_ context.Context, candidate contract.Endpoint) (http.Header, error) {
					return http.Header{
						"Authorization": {"Bearer secret-for-" + string(candidate.ID)},
					}, nil
				}),
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					id := hostToID[request.URL.Host]
					attempts = append(attempts, id)
					body, err := io.ReadAll(request.Body)
					if err != nil {
						t.Fatalf("read attempt body: %v", err)
					}
					bodies = append(bodies, string(body))
					authorizations = append(authorizations, request.Header.Get("Authorization"))
					if len(attempts)-1 != test.succeedAt {
						return nil, errors.New("dial failed")
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
					}, nil
				})),
			})
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(originalBody),
			)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer local-client-token")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if len(attempts) != len(test.wantAttempts) {
				t.Fatalf("attempts = %v, want %v", attempts, test.wantAttempts)
			}
			for index, wantID := range test.wantAttempts {
				if attempts[index] != wantID {
					t.Fatalf("attempts = %v, want %v", attempts, test.wantAttempts)
				}
				if bodies[index] != originalBody {
					t.Fatalf("attempt %d body = %q, want exact %q", index+1, bodies[index], originalBody)
				}
				wantAuthorization := "Bearer secret-for-" + string(wantID)
				if authorizations[index] != wantAuthorization {
					t.Fatalf(
						"attempt %d Authorization = %q, want %q",
						index+1,
						authorizations[index],
						wantAuthorization,
					)
				}
			}
			if test.wantCode != "" {
				assertInferenceError(t, response, test.wantStatus, test.wantCode)
			} else if response.Body.String() != `{"ok":true}` {
				t.Fatalf("success body = %q", response.Body.String())
			}
		})
	}
}

func TestInferencePlaneRecordsMetadataWithoutChangingClientBytes(t *testing.T) {
	const responseBody = `{"id":"resp","model":"public-alias","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`
	upstream := validEndpoint(contract.ProtocolOpenAIResponses, false)
	store := &memoryRequestRecordStore{}
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint:      upstream,
			UpstreamModel: "provider/secret-upstream",
			RouteID:       "route_alias",
		}}},
		RequestRecords: store,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), `"model":"provider/secret-upstream"`) {
				t.Fatalf("upstream did not receive rewrite: %s", body)
			}
			if strings.Contains(string(body), "public-alias") {
				t.Fatalf("public alias leaked upstream: %s", body)
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, err = writer.Write([]byte(responseBody))
			return err
		}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodPost,
			"/v1/responses",
			strings.NewReader(`{"model":"public-alias","input":"hi"}`),
		),
	)
	if response.Code != http.StatusOK || response.Body.String() != responseBody {
		t.Fatalf("client response altered: %d %q", response.Code, response.Body.String())
	}
	if len(store.records) != 1 {
		t.Fatalf("records=%d", len(store.records))
	}
	record := store.records[0]
	if record.Status != contract.RequestStatusSucceeded {
		t.Fatalf("status=%q", record.Status)
	}
	if record.RequestedModel == nil || *record.RequestedModel != "public-alias" {
		t.Fatalf("requested model=%v", record.RequestedModel)
	}
	if record.Usage == nil || record.Usage.TotalTokens != 5 {
		t.Fatalf("usage=%#v", record.Usage)
	}
	encoded, _ := json.Marshal(record)
	if strings.Contains(string(encoded), "provider/secret-upstream") {
		t.Fatalf("upstream model leaked into record: %s", encoded)
	}
	if record.Audit.RequestBodyCaptured || record.Audit.ResponseContentCaptured {
		t.Fatalf("5a audit flags should report not captured: %#v", record.Audit)
	}
}

type memoryRequestRecordStore struct {
	records []contract.RequestRecord
}

func (store *memoryRequestRecordStore) InsertRequestRecord(_ context.Context, record contract.RequestRecord) error {
	store.records = append(store.records, record)
	return nil
}

func (store *memoryRequestRecordStore) UpsertRequestRecord(_ context.Context, record contract.RequestRecord) error {
	for index := range store.records {
		if store.records[index].ID != record.ID {
			continue
		}
		if store.records[index].Status != contract.RequestStatusPending &&
			record.Status == contract.RequestStatusPending {
			return nil
		}
		store.records[index] = record
		return nil
	}
	store.records = append(store.records, record)
	return nil
}

func TestInferencePlanePublishesPendingThenUpdatesSameRecord(t *testing.T) {
	store := &memoryRequestRecordStore{}
	started := make(chan struct{})
	release := make(chan struct{})
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false),
		}}},
		RequestRecords: store,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, _ *http.Request, _ transport.Target) error {
			close(started)
			<-release
			writer.WriteHeader(http.StatusOK)
			_, err := writer.Write([]byte(`{"id":"resp_live","status":"completed"}`))
			return err
		}),
	})
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"m","input":"hi"}`)),
		)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("forwarder did not start")
	}
	if len(store.records) != 1 || store.records[0].Status != contract.RequestStatusPending {
		t.Fatalf("pending records=%#v", store.records)
	}
	requestID := store.records[0].ID
	if store.records[0].ServiceID == nil {
		t.Fatalf("pending record should expose selected endpoint: %#v", store.records[0])
	}
	if store.records[0].CompletedAt != nil {
		t.Fatalf("pending record completed_at=%v", store.records[0].CompletedAt)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not finish")
	}
	if len(store.records) != 1 {
		t.Fatalf("records=%#v", store.records)
	}
	if store.records[0].ID != requestID || store.records[0].Status != contract.RequestStatusSucceeded {
		t.Fatalf("terminal record=%#v", store.records[0])
	}
}

func TestInferencePlaneRecordsFailedCancelledBlockedAndStreaming(t *testing.T) {
	t.Run("failed", func(t *testing.T) {
		store := &memoryRequestRecordStore{}
		handler := NewWithDependencies(Dependencies{
			Resolver: candidateResolver{candidates: []endpoint.Resolved{{
				Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false),
			}}},
			RequestRecords: store,
			Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
				return errors.New("upstream boom")
			}),
		})
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"m","input":"hi"}`)),
		)
		if response.Code == http.StatusOK {
			t.Fatalf("expected failure status, got %d", response.Code)
		}
		if len(store.records) != 1 || store.records[0].Status != contract.RequestStatusFailed {
			t.Fatalf("records=%#v", store.records)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		store := &memoryRequestRecordStore{}
		started := make(chan struct{})
		handler := NewWithDependencies(Dependencies{
			Resolver: candidateResolver{candidates: []endpoint.Resolved{{
				Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false),
			}}},
			RequestRecords: store,
			Forwarder: forwarderFunc(func(_ http.ResponseWriter, request *http.Request, _ transport.Target) error {
				close(started)
				<-request.Context().Done()
				return request.Context().Err()
			}),
		})
		ctx, cancel := context.WithCancel(context.Background())
		request := httptest.NewRequest(
			http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"m","input":"hi"}`),
		).WithContext(ctx)
		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			handler.ServeHTTP(response, request)
			close(done)
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("forwarder did not start")
		}
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("handler did not return after cancel")
		}
		if len(store.records) != 1 || store.records[0].Status != contract.RequestStatusCancelled {
			t.Fatalf("records=%#v", store.records)
		}
	})

	t.Run("blocked", func(t *testing.T) {
		store := &memoryRequestRecordStore{}
		filter := testPrivacyEngine(t, privacy.Policy{
			Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionBlock,
		}, nil)
		handler := NewWithDependencies(Dependencies{
			Resolver: candidateResolver{candidates: []endpoint.Resolved{{
				Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false),
			}}},
			RequestRecords: store,
			PrivacyFilter:  filter,
			Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
				t.Fatal("blocked request must not forward")
				return nil
			}),
		})
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(`{"model":"m","input":"alice@example.com"}`),
			),
		)
		assertInferenceError(t, response, http.StatusForbidden, "policy_blocked")
		if len(store.records) != 1 || store.records[0].Status != contract.RequestStatusBlocked {
			t.Fatalf("records=%#v", store.records)
		}
	})

	t.Run("streaming", func(t *testing.T) {
		const sse = "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"total_tokens\":3}}}\n\n"
		store := &memoryRequestRecordStore{}
		handler := NewWithDependencies(Dependencies{
			Resolver: candidateResolver{candidates: []endpoint.Resolved{{
				Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, true),
			}}},
			RequestRecords: store,
			Forwarder: forwarderFunc(func(writer http.ResponseWriter, _ *http.Request, _ transport.Target) error {
				writer.Header().Set("Content-Type", "text/event-stream")
				writer.WriteHeader(http.StatusOK)
				_, err := writer.Write([]byte(sse))
				return err
			}),
		})
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(`{"model":"m","input":"hi","stream":true}`),
			),
		)
		if response.Code != http.StatusOK || response.Body.String() != sse {
			t.Fatalf("client response altered: %d %q", response.Code, response.Body.String())
		}
		if len(store.records) != 1 {
			t.Fatalf("records=%d", len(store.records))
		}
		record := store.records[0]
		if !record.Streaming || record.Status != contract.RequestStatusSucceeded {
			t.Fatalf("record=%#v", record)
		}
		if record.Usage == nil || record.Usage.TotalTokens != 3 {
			t.Fatalf("usage=%#v", record.Usage)
		}
	})
}

func TestInferencePlaneAliasRestoreStreamingSSEHidesUpstreamModel(t *testing.T) {
	upstream := validEndpoint(contract.ProtocolOpenAIResponses, true)
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint:      upstream,
			UpstreamModel: "upstream-secret-model",
		}}},
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, _ *http.Request, _ transport.Target) error {
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.WriteHeader(http.StatusOK)
			_, err := writer.Write([]byte(
				"event: response.created\n" +
					"data: {\"type\":\"response.created\",\"response\":{\"model\":\"upstream-secret-model\"}}\n\n" +
					"data: [DONE]\n",
			))
			return err
		}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodPost,
			"/v1/responses",
			strings.NewReader(`{"model":"public-alias","stream":true}`),
		),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "upstream-secret-model") {
		t.Fatalf("upstream model leaked to client: %q", body)
	}
	if !strings.Contains(body, `"model":"public-alias"`) {
		t.Fatalf("alias missing from SSE body: %q", body)
	}
	if !strings.Contains(body, "data: [DONE]\n") {
		t.Fatalf("DONE line missing: %q", body)
	}
}

func TestInferencePlaneAliasRestoreRewritesUpstreamErrorBody(t *testing.T) {
	upstream := validEndpoint(contract.ProtocolOpenAIResponses, false)
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint:      upstream,
			UpstreamModel: "upstream-secret-model",
		}}},
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, _ *http.Request, _ transport.Target) error {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusTooManyRequests)
			_, err := writer.Write([]byte(
				`{"error":{"message":"rate limited","type":"rate_limit"},"model":"upstream-secret-model"}`,
			))
			return err
		}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodPost,
			"/v1/responses",
			strings.NewReader(`{"model":"public-alias","input":"hi"}`),
		),
	)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "upstream-secret-model") {
		t.Fatalf("upstream model leaked in error body: %q", body)
	}
	if !strings.Contains(body, `"model":"public-alias"`) {
		t.Fatalf("alias missing from error body: %q", body)
	}
}

func TestInferencePlaneAliasRewriteUsesOriginalModelPerFallbackTarget(t *testing.T) {
	const originalBody = `{"model":"public-alias","input":"preserve me"}`
	first := validEndpoint(contract.ProtocolOpenAIResponses, false)
	first.ID = "endpoint_first"
	first.BaseURL = "https://first.example"
	second := validEndpoint(contract.ProtocolOpenAIResponses, false)
	second.ID = "endpoint_second"
	second.BaseURL = "https://second.example"

	var bodies []string
	var hosts []string
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{
			{Endpoint: first, UpstreamModel: "upstream-one"},
			{Endpoint: second, UpstreamModel: "upstream-two"},
		}},
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			hosts = append(hosts, request.URL.Host)
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("read attempt body: %v", err)
			}
			bodies = append(bodies, string(body))
			if request.URL.Host == "first.example" {
				return nil, errors.New("dial failed before response start")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			}, nil
		})),
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(originalBody),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Join(hosts, ",") != "first.example,second.example" {
		t.Fatalf("hosts = %v", hosts)
	}
	if len(bodies) != 2 {
		t.Fatalf("bodies = %v", bodies)
	}
	wantFirst := `{"model":"upstream-one","input":"preserve me"}`
	wantSecond := `{"model":"upstream-two","input":"preserve me"}`
	if bodies[0] != wantFirst {
		t.Fatalf("first body = %q, want %q", bodies[0], wantFirst)
	}
	if bodies[1] != wantSecond {
		t.Fatalf("second body = %q, want %q (must rewrite original, not first rewrite)", bodies[1], wantSecond)
	}
	if strings.Contains(bodies[1], "upstream-one") {
		t.Fatalf("second attempt reused first rewrite: %q", bodies[1])
	}
}

func TestInferencePlaneNeverFailsOverAfterDownstreamResponseStarts(t *testing.T) {
	streamErr := errors.New("stream interrupted")
	first := validEndpoint(contract.ProtocolOpenAIResponses, true)
	first.ID = "endpoint_first"
	first.BaseURL = "https://first.example"
	second := validEndpoint(contract.ProtocolOpenAIResponses, true)
	second.ID = "endpoint_second"
	second.BaseURL = "https://second.example"
	var attempts []string
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{
			{Endpoint: first},
			{Endpoint: second},
		}},
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts = append(attempts, request.URL.Host)
			if request.URL.Host != "first.example" {
				t.Fatal("second upstream was attempted after downstream bytes")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body: io.NopCloser(io.MultiReader(
					strings.NewReader("event: response.created\ndata: first\n\n"),
					failingReader{err: streamErr},
				)),
			}, nil
		})),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodPost,
			"/v1/responses",
			strings.NewReader(`{"stream":true}`),
		),
	)

	if len(attempts) != 1 || attempts[0] != "first.example" {
		t.Fatalf("attempts = %v, want first only", attempts)
	}
	if response.Code != http.StatusOK ||
		response.Body.String() != "event: response.created\ndata: first\n\n" {
		t.Fatalf("partial response = %d %q", response.Code, response.Body.String())
	}
}

func TestInferencePlaneDoesNotRetryOpaqueNonReplayableBody(t *testing.T) {
	const originalBody = `{"contents":[{"parts":[{"text":"once"}]}]}`
	tracked := &trackingRequestBody{reader: strings.NewReader(originalBody)}
	first := validEndpoint(contract.ProtocolGoogleGenerateContent, false)
	first.ID = "endpoint_first"
	first.BaseURL = "https://first.example"
	second := first
	second.ID = "endpoint_second"
	second.BaseURL = "https://second.example"
	attempts := 0
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{
			{Endpoint: first},
			{Endpoint: second},
		}},
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			body, err := io.ReadAll(request.Body)
			if err != nil || string(body) != originalBody {
				t.Fatalf("opaque body = %q, %v", body, err)
			}
			return nil, errors.New("dial failed")
		})),
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/gemini:generateContent",
		nil,
	)
	request.Body = tracked
	request.ContentLength = -1
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assertInferenceError(t, response, http.StatusBadGateway, "upstream_unavailable")
	if attempts != 1 || tracked.reads == 0 {
		t.Fatalf("attempts=%d reads=%d, want one consumed attempt", attempts, tracked.reads)
	}
}

func TestInferencePlaneResponseStartTimeoutCanFailOverWithoutCuttingLongSSE(t *testing.T) {
	t.Run("timeout before headers switches candidate", func(t *testing.T) {
		first := validEndpoint(contract.ProtocolOpenAIResponses, false)
		first.ID = "endpoint_first"
		first.BaseURL = "https://first.example"
		second := first
		second.ID = "endpoint_second"
		second.BaseURL = "https://second.example"
		var attempts []string
		handler := NewWithDependencies(Dependencies{
			Resolver: candidateResolver{candidates: []endpoint.Resolved{
				{Endpoint: first},
				{Endpoint: second},
			}},
			ResponseStartTimeout: 10 * time.Millisecond,
			Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, target transport.Target) error {
				attempts = append(attempts, target.BaseURL.Host)
				if target.BaseURL.Host == "first.example" {
					<-request.Context().Done()
					return request.Context().Err()
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusOK)
				_, err := writer.Write([]byte(`{"ok":true}`))
				return err
			}),
		})
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(`{"model":"gpt-5"}`),
			),
		)

		if response.Code != http.StatusOK || response.Body.String() != `{"ok":true}` {
			t.Fatalf("response = %d %q", response.Code, response.Body.String())
		}
		if strings.Join(attempts, ",") != "first.example,second.example" {
			t.Fatalf("attempts = %v", attempts)
		}
	})

	t.Run("SSE continues after response headers", func(t *testing.T) {
		candidate := validEndpoint(contract.ProtocolOpenAIResponses, true)
		handler := NewWithDependencies(Dependencies{
			Resolver:             candidateResolver{candidates: []endpoint.Resolved{{Endpoint: candidate}}},
			ResponseStartTimeout: 5 * time.Millisecond,
			Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
				writer.Header().Set("Content-Type", "text/event-stream")
				writer.WriteHeader(http.StatusOK)
				if err := http.NewResponseController(writer).Flush(); err != nil {
					return err
				}
				time.Sleep(20 * time.Millisecond)
				if request.Context().Err() != nil {
					t.Fatalf("SSE context was canceled after response start: %v", request.Context().Err())
				}
				_, err := writer.Write([]byte("data: done\n\n"))
				return err
			}),
		})
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(`{"stream":true}`),
			),
		)
		if response.Code != http.StatusOK || response.Body.String() != "data: done\n\n" {
			t.Fatalf("SSE response = %d %q", response.Code, response.Body.String())
		}
	})
}

func TestResponseStartTimeoutHasOneDeterministicWinner(t *testing.T) {
	t.Run("response start disarms timeout", func(t *testing.T) {
		attempt := newResponseStartContext(context.Background(), 5*time.Millisecond)
		attempt.ResponseStarted()
		time.Sleep(15 * time.Millisecond)
		if attempt.TimedOut() || attempt.Context().Err() != nil {
			t.Fatalf(
				"disarmed timeout state: timed_out=%t err=%v",
				attempt.TimedOut(),
				attempt.Context().Err(),
			)
		}
		attempt.Stop()
	})

	t.Run("elapsed timeout cannot be overwritten by late headers", func(t *testing.T) {
		attempt := newResponseStartContext(context.Background(), time.Millisecond)
		select {
		case <-attempt.Context().Done():
		case <-time.After(time.Second):
			t.Fatal("response-start timeout did not fire")
		}
		attempt.ResponseStarted()
		if !attempt.TimedOut() ||
			!errors.Is(context.Cause(attempt.Context()), context.DeadlineExceeded) {
			t.Fatalf(
				"timeout state: timed_out=%t cause=%v",
				attempt.TimedOut(),
				context.Cause(attempt.Context()),
			)
		}
		attempt.Stop()
	})
}

func TestInferencePlaneCompletesHealthProbeAtResponseStart(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		wantSignal func(*healthRecordingResolver) <-chan struct{}
	}{
		{
			name:   "successful headers close probe",
			status: http.StatusOK,
			wantSignal: func(resolver *healthRecordingResolver) <-chan struct{} {
				return resolver.successes
			},
		},
		{
			name:   "server error headers reopen probe",
			status: http.StatusServiceUnavailable,
			wantSignal: func(resolver *healthRecordingResolver) <-chan struct{} {
				return resolver.failures
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := validEndpoint(contract.ProtocolOpenAIResponses, true)
			resolver := &healthRecordingResolver{
				candidate: endpoint.Resolved{Endpoint: candidate},
				begins:    make(chan struct{}, 1),
				successes: make(chan struct{}, 1),
				failures:  make(chan struct{}, 1),
				abandons:  make(chan struct{}, 1),
			}
			releaseStream := make(chan struct{})
			handler := NewWithDependencies(Dependencies{
				Resolver: resolver,
				Forwarder: forwarderFunc(func(
					writer http.ResponseWriter,
					_ *http.Request,
					_ transport.Target,
				) error {
					writer.Header().Set("Content-Type", "text/event-stream")
					writer.WriteHeader(test.status)
					if err := http.NewResponseController(writer).Flush(); err != nil {
						return err
					}
					<-releaseStream
					_, err := writer.Write([]byte("data: done\n\n"))
					return err
				}),
			})
			done := make(chan struct{})
			go func() {
				defer close(done)
				handler.ServeHTTP(
					httptest.NewRecorder(),
					httptest.NewRequest(
						http.MethodPost,
						"/v1/responses",
						strings.NewReader(`{"stream":true}`),
					),
				)
			}()

			select {
			case <-resolver.begins:
			case <-time.After(time.Second):
				t.Fatal("upstream attempt did not begin")
			}
			select {
			case <-test.wantSignal(resolver):
				// Health is settled while the SSE remains open.
			case <-time.After(time.Second):
				t.Fatal("response headers did not settle endpoint health")
			}
			select {
			case <-done:
				t.Fatal("stream completed before the test released it")
			default:
			}
			close(releaseStream)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("stream did not finish")
			}
			select {
			case <-resolver.abandons:
				t.Fatal("settled health probe was abandoned a second time")
			default:
			}
		})
	}
}

func TestInferencePlaneAcquiresHealthProbeImmediatelyBeforeUpstreamIO(t *testing.T) {
	candidate := validEndpoint(contract.ProtocolOpenAIResponses, false)
	resolver := &healthRecordingResolver{
		candidate: endpoint.Resolved{Endpoint: candidate},
		begins:    make(chan struct{}, 1),
		successes: make(chan struct{}, 1),
		failures:  make(chan struct{}, 1),
		abandons:  make(chan struct{}, 1),
	}
	authorizerEntered := make(chan struct{})
	releaseAuthorizer := make(chan struct{})
	handler := NewWithDependencies(Dependencies{
		Resolver: resolver,
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
			close(authorizerEntered)
			<-releaseAuthorizer
			return nil, nil
		}),
		Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
			return nil
		}),
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(`{"model":"gpt-5"}`),
			),
		)
	}()

	select {
	case <-authorizerEntered:
	case <-time.After(time.Second):
		t.Fatal("authorizer did not run")
	}
	select {
	case <-resolver.begins:
		t.Fatal("half-open probe was reserved during local credential preparation")
	default:
	}
	close(releaseAuthorizer)
	select {
	case <-resolver.begins:
	case <-time.After(time.Second):
		t.Fatal("upstream I/O did not acquire the health probe")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("request did not finish")
	}
}

func TestInferencePlaneCircuitExcludesUnhealthyAutomaticCandidate(t *testing.T) {
	first := validEndpoint(contract.ProtocolOpenAIModels, false)
	first.ID = "endpoint_a"
	first.Name = "endpoint_a"
	first.BaseURL = "https://endpoint-a.example"
	second := first
	second.ID = "endpoint_b"
	second.Name = "endpoint_b"
	second.BaseURL = "https://endpoint-b.example"
	resolver, err := endpoint.NewStoreResolver(endpointPageReader{items: []storage.EndpointRecord{
		{Endpoint: second},
		{Endpoint: first},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	attempts := map[string]int{}
	handler := NewWithDependencies(Dependencies{
		Resolver: resolver,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			mu.Lock()
			attempts[request.URL.Host]++
			mu.Unlock()
			if request.URL.Host == "endpoint-a.example" {
				return nil, errors.New("dial failed")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"model-b"}]}`)),
			}, nil
		})),
	})

	// Model discovery fans out to every capable endpoint, so each of the first
	// three requests fails endpoint_a once while endpoint_b keeps serving the
	// partial aggregate. The third consecutive failure opens endpoint_a's
	// circuit and the fourth request must exclude it entirely.
	const wantBody = `{"object":"list","data":[{"id":"model-b"}],"first_id":"model-b","has_more":false,"last_id":"model-b"}`
	for requestIndex := 0; requestIndex < 4; requestIndex++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, "/v1/models", nil),
		)
		if response.Code != http.StatusOK || response.Body.String() != wantBody {
			t.Fatalf("request %d response = %d %q", requestIndex+1, response.Code, response.Body.String())
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if attempts["endpoint-a.example"] != 3 || attempts["endpoint-b.example"] != 4 {
		t.Fatalf("attempts = %v, want endpoint-a.example=3 endpoint-b.example=4", attempts)
	}
}

func TestInferencePlaneDoesNotWriteAnErrorAfterUpstreamResponseStarts(t *testing.T) {
	streamErr := errors.New("stream broke")
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, true)}, nil
		}),
		Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body:       io.NopCloser(io.MultiReader(strings.NewReader("event: response.created\n\n"), failingReader{err: streamErr})),
			}, nil
		})),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":true}`)))

	if response.Code != http.StatusOK || response.Body.String() != "event: response.created\n\n" {
		t.Fatalf("partial response = %d %q", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "upstream_unavailable") {
		t.Fatal("handler appended an error after the stream started")
	}
}

func TestInferencePlaneRecordsInterruptedStreamAsFailed(t *testing.T) {
	streamErr := errors.New("stream broke")
	upstream := validEndpoint(contract.ProtocolOpenAIResponses, true)
	store := &memoryRequestRecordStore{}
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint: upstream,
			RouteID:  "route_stream",
		}}},
		RequestRecords: store,
		Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body: io.NopCloser(io.MultiReader(
					strings.NewReader("event: response.created\ndata: {\"type\":\"response.created\"}\n\n"),
					failingReader{err: streamErr},
				)),
			}, nil
		})),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodPost,
			"/v1/responses",
			strings.NewReader(`{"model":"m","input":"hi","stream":true}`),
		),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("client status=%d body=%q", response.Code, response.Body.String())
	}
	if len(store.records) != 1 {
		t.Fatalf("records=%d", len(store.records))
	}
	record := store.records[0]
	if record.Status != contract.RequestStatusFailed {
		t.Fatalf("status=%q", record.Status)
	}
	if record.HTTPStatus == nil || *record.HTTPStatus != http.StatusOK {
		t.Fatalf("http_status=%v", record.HTTPStatus)
	}
	if !record.Streaming {
		t.Fatal("expected streaming=true")
	}
	if record.Error == nil ||
		record.Error.Code != "upstream_stream_interrupted" ||
		record.Error.Category != "upstream" ||
		!record.Error.Retryable {
		t.Fatalf("error=%#v", record.Error)
	}
	if record.ServiceID == nil || *record.ServiceID != upstream.ID {
		t.Fatalf("service_id=%v", record.ServiceID)
	}
	if record.RouteID == nil || *record.RouteID != "route_stream" {
		t.Fatalf("route_id=%v", record.RouteID)
	}
	if record.Plan == nil {
		t.Fatal("expected plan attribution")
	}
}

func TestInferencePlaneDoesNotWriteAfterClientCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{}, context.Canceled
		}),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`)).WithContext(ctx)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Body.Len() != 0 {
		t.Fatalf("canceled response body = %q", response.Body.String())
	}
}

func TestInferencePlaneMetadataConcurrencyWaitHonorsCancellation(t *testing.T) {
	handler := New()
	for range maxConcurrentMetadataInspections {
		handler.metadataSlots <- struct{}{}
	}
	defer func() {
		for range maxConcurrentMetadataInspections {
			<-handler.metadataSlots
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`)).WithContext(ctx)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("metadata concurrency wait ignored request cancellation")
	}
	if response.Body.Len() != 0 {
		t.Fatalf("canceled response body = %q", response.Body.String())
	}
}

func TestInferencePlaneHoldsMetadataPermitAcrossSafeFallbackWindow(t *testing.T) {
	resolverEntered := make(chan struct{}, maxConcurrentMetadataInspections+1)
	allowResolve := make(chan struct{})
	allowForward := make(chan struct{})
	done := make(chan struct{}, maxConcurrentMetadataInspections+1)
	var resolveOnce sync.Once
	var forwardOnce sync.Once
	defer func() {
		resolveOnce.Do(func() { close(allowResolve) })
		forwardOnce.Do(func() { close(allowForward) })
	}()

	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			resolverEntered <- struct{}{}
			<-allowResolve
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}, nil
		}),
		Forwarder: forwarderFunc(func(_ http.ResponseWriter, request *http.Request, _ transport.Target) error {
			if _, err := io.Copy(io.Discard, request.Body); err != nil {
				return err
			}
			<-allowForward
			return nil
		}),
	})
	start := func() {
		go func() {
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`)))
			done <- struct{}{}
		}()
	}

	for range maxConcurrentMetadataInspections {
		start()
	}
	for range maxConcurrentMetadataInspections {
		select {
		case <-resolverEntered:
		case <-time.After(time.Second):
			t.Fatal("initial request did not reach resolver")
		}
	}

	start()
	select {
	case <-resolverEntered:
		t.Fatal("fifth buffered request passed the metadata memory bound")
	case <-time.After(50 * time.Millisecond):
	}

	resolveOnce.Do(func() { close(allowResolve) })
	select {
	case <-resolverEntered:
		t.Fatal("retry buffer escaped the four-request residency bound")
	case <-time.After(50 * time.Millisecond):
	}
	forwardOnce.Do(func() { close(allowForward) })
	select {
	case <-resolverEntered:
		// The exact replay copy is no longer needed once one of the bounded
		// attempts completes, so the next request may acquire the permit.
	case <-time.After(time.Second):
		t.Fatal("completed fallback window did not release a metadata permit")
	}
	for range maxConcurrentMetadataInspections + 1 {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("inference request did not finish")
		}
	}
}

func validEndpoint(protocol contract.ProtocolID, streaming bool) contract.Endpoint {
	return contract.Endpoint{
		ID: "endpoint_test", Name: "test upstream", Kind: contract.EndpointKindOpenAICompatible,
		BaseURL: "https://upstream.example/prefix", Auth: contract.EndpointAuth{Scheme: contract.AuthSchemeNone},
		Enabled: true,
		Capabilities: []contract.Capability{{
			Protocol: protocol, Mode: contract.CapabilityModeNative, Streaming: streaming,
		}},
	}
}

type failingReader struct {
	err error
}

func (reader failingReader) Read([]byte) (int, error) {
	return 0, reader.err
}

func assertInferenceError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) errorEnvelope {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, status, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("error headers = %#v", response.Header())
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if envelope.Error.Code != code || envelope.RequestID == "" {
		t.Fatalf("error response = %#v", envelope)
	}
	return envelope
}
