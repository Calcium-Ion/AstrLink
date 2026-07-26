package ingress

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestAlphaNonStreamingProtocolFixturesPassThroughWithoutConversion(t *testing.T) {
	tests := []struct {
		name         string
		method       string
		path         string
		body         string
		protocol     contract.ProtocolID
		model        string
		responseBody string
	}{
		{name: "Responses", method: http.MethodPost, path: "/v1/responses?trace=1", body: `{"model":"gpt-5","input":"hello"}`, protocol: contract.ProtocolOpenAIResponses, model: "gpt-5", responseBody: `{"id":"resp_fixture","status":"completed"}`},
		{name: "Responses Compact", method: http.MethodPost, path: "/v1/responses/compact", body: `{"model":"gpt-5","input":[{"role":"user","content":"hello"}]}`, protocol: contract.ProtocolOpenAIResponsesCompact, model: "gpt-5", responseBody: `{"id":"cmp_fixture","output":[]}`},
		{name: "Anthropic Messages", method: http.MethodPost, path: "/v1/messages", body: `{"model":"claude-fixture","messages":[]}`, protocol: contract.ProtocolAnthropicMessages, model: "claude-fixture", responseBody: `{"id":"msg_fixture","type":"message"}`},
		{name: "Gemini GenerateContent", method: http.MethodPost, path: "/v1beta/models/gemini-fixture:generateContent", body: `{"contents":[{"parts":[{"text":"hello"}]}]}`, protocol: contract.ProtocolGoogleGenerateContent, model: "gemini-fixture", responseBody: `{"candidates":[{"content":{"parts":[{"text":"hello"}]}}]}`},
		{name: "Chat Completions", method: http.MethodPost, path: "/v1/chat/completions", body: `{"model":"chat-fixture","messages":[]}`, protocol: contract.ProtocolOpenAIChat, model: "chat-fixture", responseBody: `{"id":"chatcmpl_fixture","choices":[]}`},
		{name: "Completions", method: http.MethodPost, path: "/v1/completions", body: `{"model":"completion-fixture","prompt":"hello"}`, protocol: contract.ProtocolOpenAICompletions, model: "completion-fixture", responseBody: `{"id":"cmpl_fixture","choices":[]}`},
		// Model discovery routes are deliberately absent: since the M2 third
		// slice they aggregate across all capable endpoints instead of relaying
		// one upstream response byte-for-byte. See discovery_test.go.
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseURL := "https://upstream.example/sdk/v1"
			if strings.HasPrefix(test.path, "/v1beta/") {
				baseURL = "https://upstream.example/sdk/v1beta"
			}
			upstreamEndpoint := fixtureEndpoint(test.protocol, false, baseURL)
			handler := NewWithDependencies(Dependencies{
				Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
					if request.Protocol != test.protocol || request.Model != test.model || request.Streaming {
						t.Errorf("resolve request = %#v", request)
					}
					return endpoint.Resolved{Endpoint: upstreamEndpoint}, nil
				}),
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					wantURL := "https://upstream.example/sdk" + test.path
					if request.URL.String() != wantURL {
						t.Errorf("upstream URL = %q, want %q", request.URL.String(), wantURL)
					}
					body, err := io.ReadAll(request.Body)
					if err != nil {
						t.Fatal(err)
					}
					if string(body) != test.body {
						t.Errorf("upstream body = %q, want exact %q", body, test.body)
					}
					if request.Header.Get("X-Client-Marker") != "preserved" {
						t.Errorf("end-to-end request header was not preserved")
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header: http.Header{
							"Content-Type":       {"application/json"},
							"X-Fixture-Protocol": {string(test.protocol)},
						},
						Body: io.NopCloser(strings.NewReader(test.responseBody)),
					}, nil
				})),
			})
			var body io.Reader
			if test.body != "" {
				body = strings.NewReader(test.body)
			}
			request := httptest.NewRequest(test.method, test.path, body)
			request.Header.Set("X-Client-Marker", "preserved")
			if test.method == http.MethodPost {
				request.Header.Set("Content-Type", "application/json; charset=utf-8")
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK || response.Body.String() != test.responseBody {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
			if response.Header().Get("X-Fixture-Protocol") != string(test.protocol) {
				t.Fatalf("upstream response header was not preserved")
			}
		})
	}
}

func TestAlphaStreamingProtocolFixturesPreserveEventBytesAndOrder(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		body     string
		protocol contract.ProtocolID
		model    string
		stream   string
	}{
		{name: "Responses SSE", path: "/v1/responses", body: `{"model":"gpt-5","stream":true,"input":"hello"}`, protocol: contract.ProtocolOpenAIResponses, model: "gpt-5", stream: "event: response.created\ndata: {\"type\":\"response.created\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"},
		{name: "Anthropic SSE", path: "/v1/messages", body: `{"model":"claude-fixture","stream":true,"messages":[]}`, protocol: contract.ProtocolAnthropicMessages, model: "claude-fixture", stream: "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
		{name: "Gemini SSE", path: "/v1beta/models/gemini-fixture:streamGenerateContent?alt=sse", body: `{"contents":[]}`, protocol: contract.ProtocolGoogleGenerateContent, model: "gemini-fixture", stream: "data: {\"candidates\":[{\"index\":0}]}\n\ndata: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"},
		{name: "Chat SSE", path: "/v1/chat/completions", body: `{"model":"chat-fixture","stream":true,"messages":[]}`, protocol: contract.ProtocolOpenAIChat, model: "chat-fixture", stream: "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"},
		{name: "Completions SSE", path: "/v1/completions", body: `{"model":"completion-fixture","stream":true,"prompt":"hello"}`, protocol: contract.ProtocolOpenAICompletions, model: "completion-fixture", stream: "data: {\"choices\":[{\"text\":\"hi\"}]}\n\ndata: [DONE]\n\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseURL := "https://upstream.example"
			upstreamEndpoint := fixtureEndpoint(test.protocol, true, baseURL)
			handler := NewWithDependencies(Dependencies{
				Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
					if request.Protocol != test.protocol || request.Model != test.model || !request.Streaming {
						t.Errorf("resolve request = %#v", request)
					}
					return endpoint.Resolved{Endpoint: upstreamEndpoint}, nil
				}),
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					body, err := io.ReadAll(request.Body)
					if err != nil {
						t.Fatal(err)
					}
					if string(body) != test.body {
						t.Errorf("upstream body = %q, want exact %q", body, test.body)
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"text/event-stream"}},
						Body:       io.NopCloser(strings.NewReader(test.stream)),
					}, nil
				})),
			})
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK || response.Body.String() != test.stream {
				t.Fatalf("stream response = %d %q, want exact %q", response.Code, response.Body.String(), test.stream)
			}
		})
	}
}

func TestAlphaUpstreamErrorsRemainProtocolNative(t *testing.T) {
	const upstreamBody = `{"type":"error","error":{"type":"rate_limit_error","message":"fixture"}}`
	upstreamEndpoint := fixtureEndpoint(contract.ProtocolAnthropicMessages, true, "https://upstream.example")
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: upstreamEndpoint}, nil
		}),
		Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Content-Type": {"application/json"}, "Retry-After": {"2"}},
				Body:       io.NopCloser(strings.NewReader(upstreamBody)),
			}, nil
		})),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-fixture","messages":[]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusTooManyRequests || response.Body.String() != upstreamBody || response.Header().Get("Retry-After") != "2" {
		t.Fatalf("upstream error was rewritten: status=%d headers=%#v body=%q", response.Code, response.Header(), response.Body.String())
	}
}

func fixtureEndpoint(protocol contract.ProtocolID, streaming bool, baseURL string) contract.Endpoint {
	return contract.Endpoint{
		ID: "endpoint_fixture", Name: "fixture upstream", Kind: contract.EndpointKindOpenAICompatible,
		BaseURL: baseURL, Auth: contract.EndpointAuth{Scheme: contract.AuthSchemeNone}, Enabled: true,
		Capabilities: []contract.Capability{{
			Protocol: protocol, Mode: contract.CapabilityModeNative, Streaming: streaming,
		}},
	}
}
