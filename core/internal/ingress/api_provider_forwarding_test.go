package ingress

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
)

func TestAPIProviderForwarding(t *testing.T) {
	for _, tt := range []struct {
		kind       contract.ServiceKind
		base, want string
		protocol   contract.ProtocolID
		apiKey     bool
	}{
		{contract.ServiceKindDeepSeek, "/v1", "/v1/chat/completions", contract.ProtocolOpenAIChat, false},
		{contract.ServiceKindQwen, "/compatible-mode/v1", "/compatible-mode/v1/chat/completions", contract.ProtocolOpenAIChat, false},
		{contract.ServiceKindMoonshot, "/v1", "/v1/chat/completions", contract.ProtocolOpenAIChat, false},
		{contract.ServiceKindGLM, "/api/paas/v4", "/api/paas/v4/chat/completions", contract.ProtocolOpenAIChat, false},
		{contract.ServiceKindMiniMax, "/v1", "/v1/chat/completions", contract.ProtocolOpenAIChat, false},
		{contract.ServiceKindDoubao, "/api/v3", "/api/v3/chat/completions", contract.ProtocolOpenAIChat, false},
		{contract.ServiceKindXAI, "/v1", "/v1/chat/completions", contract.ProtocolOpenAIChat, false},

		{contract.ServiceKindDeepSeek, "/v1", "/anthropic/v1/messages", contract.ProtocolAnthropicMessages, true},
		{contract.ServiceKindQwen, "/compatible-mode/v1", "/apps/anthropic/v1/messages", contract.ProtocolAnthropicMessages, false},
		{contract.ServiceKindMoonshot, "/v1", "/anthropic/v1/messages", contract.ProtocolAnthropicMessages, false},
		{contract.ServiceKindGLM, "/api/paas/v4", "/api/anthropic/v1/messages", contract.ProtocolAnthropicMessages, true},
		{contract.ServiceKindMiniMax, "/v1", "/anthropic/v1/messages", contract.ProtocolAnthropicMessages, false},
		{contract.ServiceKindDoubao, "/api/v3", "/api/compatible/v1/messages", contract.ProtocolAnthropicMessages, true},
		{contract.ServiceKindXAI, "/v1", "/v1/messages", contract.ProtocolAnthropicMessages, false},
		{contract.ServiceKindDeepSeek, "/v1", "/responses", contract.ProtocolOpenAIResponses, false},
		{contract.ServiceKindQwen, "/compatible-mode/v1", "/compatible-mode/v1/responses", contract.ProtocolOpenAIResponses, false},
		{contract.ServiceKindMoonshot, "/v1", "/v1/responses", contract.ProtocolOpenAIResponses, false},
		{contract.ServiceKindMiniMax, "/v1", "/v1/responses", contract.ProtocolOpenAIResponses, false},
		{contract.ServiceKindDoubao, "/api/v3", "/api/v3/responses", contract.ProtocolOpenAIResponses, false},
		{contract.ServiceKindXAI, "/v1", "/v1/responses", contract.ProtocolOpenAIResponses, false},
		{contract.ServiceKindXAI, "/v1", "/v1/responses/compact", contract.ProtocolOpenAIResponsesCompact, false},
		{contract.ServiceKindXAI, "/v1", "/v1/completions", contract.ProtocolOpenAICompletions, false},
		// SDK roots and proxy prefixes must not duplicate /v1 or /anthropic.
		{contract.ServiceKindDeepSeek, "/proxy%2Ftenant/v1/", "/proxy%2Ftenant/anthropic/v1/messages", contract.ProtocolAnthropicMessages, true},
		{contract.ServiceKindDeepSeek, "/anthropic/v1", "/anthropic/v1/messages", contract.ProtocolAnthropicMessages, true},
		{contract.ServiceKindDeepSeek, "/proxy/anthropic", "/proxy/responses", contract.ProtocolOpenAIResponses, false},
		{contract.ServiceKindMoonshot, "/proxy/anthropic/", "/proxy/v1/chat/completions", contract.ProtocolOpenAIChat, false},
		{contract.ServiceKindQwen, "/proxy/apps/anthropic", "/proxy/compatible-mode/v1/responses", contract.ProtocolOpenAIResponses, false},
		{contract.ServiceKindGLM, "/proxy/api/anthropic", "/proxy/api/paas/v4/chat/completions", contract.ProtocolOpenAIChat, false},
		{contract.ServiceKindDoubao, "/proxy/api/compatible/v1/", "/proxy/api/v3/responses", contract.ProtocolOpenAIResponses, false},
		{contract.ServiceKindNewAPI, "/proxy/v1", "/proxy/v1/messages", contract.ProtocolAnthropicMessages, false},
		{contract.ServiceKindCustom, "/proxy", "/proxy/v1/messages", contract.ProtocolAnthropicMessages, false},
	} {
		for _, streaming := range []bool{false, true} {
			if streaming && tt.protocol == contract.ProtocolOpenAIResponsesCompact {
				continue
			}
			t.Run(fmt.Sprintf("%s/%s/%s/stream=%t", tt.kind, tt.protocol, tt.base, streaming), func(t *testing.T) {
				path, requestBody, responseBody := nativeProviderExchange(tt.protocol, streaming)
				var called atomic.Bool
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called.Store(true)
					if r.URL.EscapedPath() != tt.want || r.URL.Query().Get("trace") != "test" {
						t.Errorf("upstream URL = %s", r.URL)
					}
					wantBearer, wantAPIKey := "Bearer plan-key", ""
					if tt.apiKey {
						wantBearer, wantAPIKey = "", "plan-key"
					}
					if r.Header.Get("Authorization") != wantBearer || r.Header.Get("X-Api-Key") != wantAPIKey || r.Header.Get("X-Goog-Api-Key") != "" {
						t.Error("wrong upstream credentials")
					}
					body, err := io.ReadAll(r.Body)
					if err != nil || string(body) != requestBody {
						t.Errorf("request changed: %s (err=%v)", body, err)
					}
					if tt.protocol == contract.ProtocolAnthropicMessages && r.Header.Get("Anthropic-Version") != "2023-06-01" {
						t.Error("missing Anthropic version")
					}
					w.Header().Set("Content-Type", "application/json")
					if streaming {
						w.Header().Set("Content-Type", "text/event-stream")
					}
					_, _ = io.WriteString(w, responseBody)
				}))
				defer upstream.Close()
				service := contract.Service{
					ID: "service_api", Name: "API", Kind: tt.kind, Enabled: true,
					Models:       []string{"model-test"},
					Capabilities: []contract.Capability{{Protocol: tt.protocol, Mode: contract.CapabilityModeNative, Streaming: tt.protocol != contract.ProtocolOpenAIResponsesCompact}},
					HTTP:         &contract.HTTPConnection{BaseURL: upstream.URL + tt.base, Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}, CredentialRef: "local://service/service_api"},
				}
				if err := service.Validate(); err != nil {
					t.Fatal(err)
				}
				handler := NewWithDependencies(Dependencies{
					Resolver:   candidateResolver{candidates: []endpoint.Resolved{{Service: service, BaseURL: service.HTTP.BaseURL, UpstreamProtocol: tt.protocol}}},
					Authorizer: endpoint.NewServiceAuthorizer(codingPlanCredentials{}, nil),
				})
				request := httptest.NewRequest(http.MethodPost, path+"?trace=test", strings.NewReader(requestBody))
				request.Header.Set("Anthropic-Version", "2023-06-01")
				request.Header.Set("X-Goog-Api-Key", "local-secret")
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", "Bearer local-secret")
				request.Header.Set("X-Api-Key", "local-secret")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if !called.Load() || response.Code != http.StatusOK || response.Body.String() != responseBody {
					t.Fatalf("called=%t status=%d body=%s", called.Load(), response.Code, response.Body.String())
				}
			})
		}
	}
}

func nativeProviderExchange(protocol contract.ProtocolID, streaming bool) (path, request, response string) {
	switch protocol {
	case contract.ProtocolAnthropicMessages:
		path = "/v1/messages"
		request = `{"model":"model-test","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"stream":%t}`
		response = `{"id":"msg_test","type":"message","role":"assistant","model":"model-test","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
		if streaming {
			response = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
		}
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		path = "/v1/responses"
		request = `{"model":"model-test","input":"hello","stream":%t}`
		response = `{"id":"resp_test","object":"response","model":"model-test","output":[]}`
		if protocol == contract.ProtocolOpenAIResponsesCompact {
			path += "/compact"
		}
		if streaming {
			response = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"output\":[]}}\n\n"
		}
	case contract.ProtocolOpenAICompletions:
		path = "/v1/completions"
		request = `{"model":"model-test","prompt":"hello","stream":%t}`
		response = `{"id":"cmpl_test","choices":[{"text":"ok","index":0,"finish_reason":"stop"}]}`
		if streaming {
			response = "data: {\"choices\":[{\"text\":\"ok\",\"index\":0}]}\n\ndata: [DONE]\n\n"
		}
	default:
		path = "/v1/chat/completions"
		request = `{"model":"model-test","messages":[{"role":"user","content":"hello"}],"stream":%t}`
		response = `{"id":"chat_test","model":"model-test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
		if streaming {
			response = "data: {\"id\":\"chat_test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"
		}
	}
	return path, fmt.Sprintf(request, streaming), response
}
