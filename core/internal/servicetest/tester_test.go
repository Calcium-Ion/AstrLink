package servicetest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
)

type tokens struct{}

func (tokens) AccessToken(context.Context, contract.ServiceID) (accountauth.AccountTokens, error) {
	return accountauth.AccountTokens{AccessToken: "private-token"}, nil
}

func TestProviderRequests(t *testing.T) {
	for _, test := range []struct {
		name                         string
		kind                         contract.ServiceKind
		protocol                     contract.ProtocolID
		base, path, response, header string
		stream                       bool
	}{
		{"chat", contract.ServiceKindOpenAI, contract.ProtocolOpenAIChat, "/proxy/v1", "/proxy/v1/chat/completions", `{"choices":[{"message":{"content":"OK"}}]}`, "", false},
		{"responses", contract.ServiceKindOpenAI, contract.ProtocolOpenAIResponses, "/v1", "/v1/responses", `{"status":"completed","output":[{"content":[{"type":"output_text","text":"OK"}]}]}`, "", false},
		{"completions", contract.ServiceKindOpenAICompatible, contract.ProtocolOpenAICompletions, "", "/v1/completions", `{"choices":[{"text":"OK"}]}`, "", false},
		{"anthropic", contract.ServiceKindAnthropic, contract.ProtocolAnthropicMessages, "/v1", "/v1/messages", `{"content":[{"type":"text","text":"OK"}]}`, "Anthropic-Version", false},
		{"deepseek messages", contract.ServiceKindDeepSeek, contract.ProtocolAnthropicMessages, "/proxy/v1", "/proxy/anthropic/v1/messages", `{"content":[{"type":"text","text":"OK"}]}`, "Anthropic-Version", false},
		{"glm chat", contract.ServiceKindGLM, contract.ProtocolOpenAIChat, "/api/paas/v4", "/api/paas/v4/chat/completions", `{"choices":[{"message":{"content":"OK"}}]}`, "", false},
		{"google", contract.ServiceKindGemini, contract.ProtocolGoogleGenerateContent, "/v1beta", "/v1beta/models/test-model:generateContent", `{"candidates":[{"content":{"parts":[{"text":"OK"}]}}]}`, "", false},
		{"google stream", contract.ServiceKindGemini, contract.ProtocolGoogleGenerateContent, "", "/v1beta/models/test-model:streamGenerateContent?alt=sse", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"OK\"}]},\"finishReason\":\"STOP\"}]}\n\n", "", true},
		{"codex", contract.ServiceKindCodexSubscription, contract.ProtocolOpenAIResponses, "/backend-api/codex", "/backend-api/codex/responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", "Authorization", true},
		{"claude subscription", contract.ServiceKindClaudeSubscription, contract.ProtocolAnthropicMessages, "", "/v1/messages", `{"content":[{"type":"text","text":"OK"}]}`, "Authorization", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.RequestURI() != test.path {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.RequestURI())
				}
				if test.header != "" && r.Header.Get(test.header) == "" {
					t.Errorf("missing %s", test.header)
				}
				wantAccept := "application/json"
				if test.stream {
					wantAccept = "text/event-stream"
				}
				if got := r.Header.Get("Accept"); got != wantAccept {
					t.Errorf("Accept = %q, want %q", got, wantAccept)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if test.protocol != contract.ProtocolGoogleGenerateContent && body["model"] != "test-model" {
					t.Errorf("model = %v", body["model"])
				}
				if test.kind == contract.ServiceKindCodexSubscription && (body["store"] != false || body["stream"] != true || body["instructions"] == nil || body["max_output_tokens"] != nil) {
					t.Errorf("Codex payload = %v", body)
				}
				if test.kind == contract.ServiceKindClaudeSubscription && body["system"] == nil {
					t.Error("missing subscription identity")
				}
				if test.kind == contract.ServiceKindClaudeSubscription && !strings.HasPrefix(r.Header.Get("User-Agent"), "claude-cli/") {
					t.Error("missing subscription user agent")
				}
				// Reproduce providers that label the response using Accept even
				// when stream=true still makes the body contain SSE events.
				w.Header().Set("Content-Type", r.Header.Get("Accept"))
				_, _ = io.WriteString(w, test.response)
			}))
			defer server.Close()
			service := contract.Service{ID: "service_test", Name: "Test provider", CreatedAt: time.Now(), UpdatedAt: time.Now(), Kind: test.kind, Enabled: false, Capabilities: []contract.Capability{{Protocol: test.protocol, Mode: contract.CapabilityModeNative, Streaming: true}}, HTTP: &contract.HTTPConnection{BaseURL: server.URL + test.base, Auth: contract.EndpointAuth{Scheme: contract.AuthSchemeNone}}}
			if test.kind.IsSubscription() {
				service.HTTP = nil
				service.Subscription = &contract.SubscriptionConnection{Provider: test.kind.SubscriptionProvider(), Status: contract.SubscriptionStatusConnected}
			}
			tester := New(endpoint.NewServiceAuthorizer(nil, tokens{}), nil, func(contract.SubscriptionProvider) string { return server.URL + test.base })
			result := tester.Test(context.Background(), service, contract.ServiceTestRequest{Protocol: test.protocol, Model: "test-model", Stream: test.stream})
			if !result.OK || result.Output != "OK" || result.StatusCode != 200 || calls != 1 {
				t.Fatalf("result = %+v; calls = %d", result, calls)
			}
			if test.stream && result.FirstTokenMS == nil {
				t.Fatal("streaming text was not timed")
			}
			if result.RawResponse != test.response || result.RawResponseTruncated {
				t.Fatalf("original upstream body was not preserved: %q", result.RawResponse)
			}
		})
	}
}

func TestResponseValidation(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol contract.ProtocolID
		stream   bool
		raw      string
		ok       bool
	}{
		{"chat SSE", contract.ProtocolOpenAIChat, true, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\r\n\r\ndata: [DONE]\r\n\r\n", true},
		{"anthropic SSE", contract.ProtocolAnthropicMessages, true, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n", true},
		{"HTML 200", contract.ProtocolOpenAIChat, false, "<html>login</html>", false},
		{"error 200", contract.ProtocolOpenAIChat, false, `{"error":{"message":"invalid key"}}`, false},
		{"wrong protocol", contract.ProtocolOpenAIChat, false, `{"content":[{"type":"text","text":"OK"}]}`, false},
		{"empty JSON", contract.ProtocolOpenAIResponses, false, `{}`, false},
		{"truncated stream", contract.ProtocolOpenAIChat, true, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n", false},
		{"stream error", contract.ProtocolAnthropicMessages, true, "data: {\"type\":\"error\",\"error\":{\"message\":\"overloaded\"}}\n\n", false},
		{"responses failed", contract.ProtocolOpenAIResponses, true, "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"quota\"}}}\n\n", false},
		{"empty stream", contract.ProtocolOpenAIChat, true, "data: [DONE]\n\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeResponse(strings.NewReader(test.raw), test.protocol, test.stream, "text/event-stream; charset=utf-8", nil)
			if (err == nil) != test.ok {
				t.Fatalf("error = %v, want ok=%t", err, test.ok)
			}
		})
	}
}

func TestCompatibleReasoningModelsUseCompletionTokenLimit(t *testing.T) {
	for _, model := range []string{"gpt-5", "gpt-5.4", "o1", "o3-mini", "o4-mini"} {
		_, body := testPayload(contract.ServiceKindOpenAICompatible, contract.ServiceTestRequest{Protocol: contract.ProtocolOpenAIChat, Model: model})
		if body["max_tokens"] != nil || body["max_completion_tokens"] != 1024 {
			t.Errorf("%s: token limit = %v", model, body)
		}
	}
}

type authorizerFunc func(context.Context, contract.Endpoint) (http.Header, error)

func (f authorizerFunc) Headers(ctx context.Context, e contract.Endpoint, _ http.Header) (http.Header, error) {
	return f(ctx, e)
}

func TestFailuresAreBoundedAndRedacted(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		raw, code string
	}{
		{"auth", 401, `{"error":{"message":"invalid private-token / Bearer private-token"}}`, "upstream_error"},
		{"limit", 429, `{"error":{"message":"quota exceeded"}}`, "upstream_error"},
		{"large", 200, strings.Repeat("x", maxResponseBytes+1), "response_too_large"},
		{"echo", 200, `{"choices":[{"message":{"content":"private-token"}}]}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.raw)
			}))
			defer server.Close()
			service := contract.Service{ID: "service_test", Name: "Test provider", CreatedAt: time.Now(), UpdatedAt: time.Now(), Kind: contract.ServiceKindOpenAI, HTTP: &contract.HTTPConnection{BaseURL: server.URL, Auth: contract.EndpointAuth{Scheme: contract.AuthSchemeNone}}, Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative}}}
			auth := authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
				return http.Header{"Authorization": {"Bearer private-token"}}, nil
			})
			result := New(auth, nil, nil).Test(context.Background(), service, contract.ServiceTestRequest{Protocol: contract.ProtocolOpenAIChat, Model: "gpt-test"})
			raw, _ := json.Marshal(result)
			if result.ErrorCode != test.code || strings.Contains(string(raw), "private-token") {
				t.Fatalf("result = %s", raw)
			}
			if result.RawResponse == "" {
				t.Fatal("missing upstream response body")
			}
			if test.name == "auth" && result.RawResponse != `{"error":{"message":"invalid [redacted] / [redacted]"}}` {
				t.Fatalf("unexpected redacted raw response: %s", result.RawResponse)
			}
			if test.name == "large" && (!result.RawResponseTruncated || len([]rune(result.RawResponse)) != maxRawResponseCharacters+1) {
				t.Fatal("large raw response was not bounded")
			}
		})
	}
}

func TestRawResponsePreservesMalformedAndInterruptedBodies(t *testing.T) {
	for _, test := range []struct {
		name, contentType, raw         string
		stream, interrupted, truncated bool
	}{
		{name: "malformed JSON", contentType: "application/json", raw: "{broken\n"},
		{name: "HTML", contentType: "text/html", raw: "<html>login</html>"},
		{name: "early SSE error", contentType: "text/event-stream", stream: true, raw: "data: malformed\r\n\r\ndata: retained after parser failure\r\n\r\n"},
		{name: "wrong stream type", contentType: "text/html", stream: true, raw: "<html>upstream proxy error</html>"},
		{name: "interrupted", contentType: "application/json", raw: `{"choices":[`, interrupted: true, truncated: true},
		{name: "character limit", contentType: "application/json", raw: `{"choices":[{"message":{"content":"` + strings.Repeat("文", maxRawResponseCharacters+5) + `"}}]}`, truncated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				if test.interrupted {
					w.Header().Set("Content-Length", "1024")
				}
				_, _ = io.WriteString(w, test.raw)
			}))
			defer server.Close()
			service := contract.Service{ID: "service_test", Name: "Test provider", CreatedAt: time.Now(), UpdatedAt: time.Now(), Kind: contract.ServiceKindOpenAI,
				HTTP:         &contract.HTTPConnection{BaseURL: server.URL, Auth: contract.EndpointAuth{Scheme: contract.AuthSchemeNone}},
				Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true}}}
			result := New(endpoint.NewServiceAuthorizer(nil, nil), nil, nil).Test(context.Background(), service,
				contract.ServiceTestRequest{Protocol: contract.ProtocolOpenAIChat, Model: "test", Stream: test.stream})
			if result.RawResponse != redact(test.raw, nil, maxRawResponseCharacters) || result.RawResponseTruncated != test.truncated || result.ResponseContentType != test.contentType {
				t.Fatalf("raw response mismatch: length=%d truncated=%t type=%s", len(result.RawResponse), result.RawResponseTruncated, result.ResponseContentType)
			}
			if test.interrupted && result.ErrorCode != "interrupted" {
				t.Fatalf("error = %s", result.ErrorCode)
			}
		})
	}
}

func TestCancellationAndNoRedirects(t *testing.T) {
	redirectCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			redirectCalls++
			return
		}
		if r.URL.Query().Get("wait") != "" {
			<-r.Context().Done()
			return
		}
		http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	service := contract.Service{ID: "service_test", Name: "Test provider", CreatedAt: time.Now(), UpdatedAt: time.Now(), Kind: contract.ServiceKindOpenAI, HTTP: &contract.HTTPConnection{BaseURL: server.URL, Auth: contract.EndpointAuth{Scheme: contract.AuthSchemeNone}}, Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative}}}
	tester := New(endpoint.NewServiceAuthorizer(nil, nil), nil, nil)
	input := contract.ServiceTestRequest{Protocol: contract.ProtocolOpenAIChat, Model: "test"}
	result := tester.Test(context.Background(), service, input)
	if result.OK || result.StatusCode != 307 || redirectCalls != 0 {
		t.Fatalf("redirect result = %+v", result)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	result = tester.Test(ctx, service, input)
	if result.ErrorCode != "timeout" {
		t.Fatalf("cancel result = %+v", result)
	}
}

func TestAnthropicStreamLifecycle(t *testing.T) {
	// message_start carries a message object, unlike the string in error bodies.
	// Keep a complete lifecycle: the old minimal fixture omitted that object and
	// allowed successful Claude responses to be rejected before the first delta.
	start := "event: message_start\r\ndata: " + `{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"output_tokens":1}}}` + "\r\n\r\n"
	text := "event: content_block_start\r\ndata: " + `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\r\n\r\n" +
		"event: content_block_delta\r\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"OK"}}` + "\r\n\r\n" +
		"event: content_block_stop\r\ndata: " + `{"type":"content_block_stop","index":0}` + "\r\n\r\n"
	for _, test := range []struct {
		name, tail, code string
		ok               bool
	}{
		{"completed", "event: message_delta\r\ndata: " + `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}` + "\r\n\r\nevent: message_stop\r\ndata: {\"type\":\"message_stop\"}\r\n\r\n", "", true},
		{"error after text", "event: error\r\ndata: " + `{"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}` + "\r\n\r\n", "upstream_error", false},
		{"missing stop", "", "invalid_response", false},
		{"malformed event", "event: content_block_delta\r\ndata: {broken\r\n\r\n", "invalid_response", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := start + text + test.tail
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			service := contract.Service{ID: "service_test", Name: "new-api", Kind: contract.ServiceKindNewAPI,
				HTTP:         &contract.HTTPConnection{BaseURL: server.URL, Auth: contract.EndpointAuth{Scheme: contract.AuthSchemeNone}},
				Capabilities: []contract.Capability{{Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeNative, Streaming: true}}}
			result := New(endpoint.NewServiceAuthorizer(nil, nil), nil, nil).Test(context.Background(), service,
				contract.ServiceTestRequest{Protocol: contract.ProtocolAnthropicMessages, Model: "claude-sonnet-5", Stream: true})
			if result.OK != test.ok || result.StatusCode != http.StatusOK || result.ErrorCode != test.code || result.Output != "OK" || result.FirstTokenMS == nil {
				t.Fatalf("result=%+v", result)
			}
			if result.RawResponse != body || result.RawResponseTruncated {
				t.Fatal("original upstream stream was not preserved")
			}
			if test.code == "upstream_error" && result.Message != "overloaded" {
				t.Fatalf("lost upstream failure message: %q", result.Message)
			}
		})
	}
}

func TestUpstreamErrorMessageShapes(t *testing.T) {
	for _, test := range []struct{ raw, message string }{
		{`{"message":"request failed"}`, "request failed"},
		{`{"error":{"message":"overloaded"},"message":{"id":"msg_test"}}`, "overloaded"},
		{`{"error":"invalid key"}`, "invalid key"},
		{`{"type":"message_start","message":{"id":"msg_test"}}`, ""},
	} {
		if got := upstreamError([]byte(test.raw)); got != test.message {
			t.Errorf("upstreamError(%s) = %q, want %q", test.raw, got, test.message)
		}
	}
}
