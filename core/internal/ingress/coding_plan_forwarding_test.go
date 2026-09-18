package ingress

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
)

type codingPlanCredentials struct{}

func (codingPlanCredentials) Get(context.Context, secretstore.Ref) ([]byte, error) {
	return []byte("plan-key"), nil
}
func (codingPlanCredentials) Put(context.Context, secretstore.Ref, []byte) error { return nil }
func (codingPlanCredentials) Delete(context.Context, secretstore.Ref) error      { return nil }
func (codingPlanCredentials) AccessToken(context.Context, contract.ServiceID) (accountauth.AccountTokens, error) {
	return accountauth.AccountTokens{AccessToken: "subscription-token"}, nil
}

func TestCodingPlanForwardingPathsHeadersAndStreams(t *testing.T) {
	for _, test := range []struct {
		kind                contract.ServiceKind
		model, prefix, path string
		protocol            contract.ProtocolID
		auth                contract.AuthScheme
	}{
		{contract.ServiceKindClaudeSubscription, "claude-sonnet-4-5", "", "/v1/messages", contract.ProtocolAnthropicMessages, contract.AuthSchemeBearer},
		{contract.ServiceKindKimiCoding, "kimi-for-coding", "/coding", "/v1/messages", contract.ProtocolAnthropicMessages, contract.AuthSchemeAnthropicAPIKey},
		{contract.ServiceKindGLMCoding, "glm-5.3", "/api/anthropic", "/v1/messages", contract.ProtocolAnthropicMessages, contract.AuthSchemeBearer},
		{contract.ServiceKindMiniMaxCoding, "MiniMax-M3", "/anthropic", "/v1/messages", contract.ProtocolAnthropicMessages, contract.AuthSchemeBearer},
		{contract.ServiceKindOpenCodeGo, "minimax-m3", "/zen/go/v1", "/v1/messages", contract.ProtocolAnthropicMessages, contract.AuthSchemeBearer},
		{contract.ServiceKindOpenCodeGo, "gpt-5.6-luna", "/zen/go/v1", "/v1/responses", contract.ProtocolOpenAIResponses, contract.AuthSchemeBearer},
		{contract.ServiceKindOpenCodeZen, "minimax-m3", "/zen/v1", "/v1/chat/completions", contract.ProtocolOpenAIChat, contract.AuthSchemeBearer},
	} {
		t.Run(string(test.kind)+"/"+test.model, func(t *testing.T) {
			for _, streaming := range []bool{false, true} {
				t.Run(fmt.Sprintf("stream=%t", streaming), func(t *testing.T) {
					responseBody := `{"id":"message_test","model":"` + test.model + `","content":[{"type":"text","text":"ok"}]}`
					if streaming {
						responseBody = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
					}
					var called atomic.Bool
					upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
						called.Store(true)
						wantPath := strings.TrimSuffix(test.prefix, "/v1") + test.path
						if request.URL.Path != wantPath {
							t.Errorf("path = %q, want %q", request.URL.Path, wantPath)
						}
						authorization, apiKey := "Bearer plan-key", ""
						if test.kind.IsSubscription() {
							authorization = "Bearer subscription-token"
						}
						if test.auth == contract.AuthSchemeAnthropicAPIKey {
							authorization, apiKey = "", "plan-key"
						}
						if request.Header.Get("Authorization") != authorization || request.Header.Get("X-Api-Key") != apiKey {
							t.Error("upstream authentication did not replace local credentials")
						}
						if request.Header.Get("Chatgpt-Account-Id") != "" {
							t.Error("Codex account header reached another provider")
						}
						body, err := io.ReadAll(request.Body)
						if err != nil {
							t.Error(err)
						}
						if !strings.Contains(string(body), "Keep my instructions") {
							t.Error("lost client system prompt")
						}
						if test.kind == contract.ServiceKindClaudeSubscription {
							if !strings.Contains(string(body), claudeCodeBanner) {
								t.Error("missing Claude compatibility banner")
							}
							for _, beta := range []string{"client-feature", "oauth-2025-04-20", "claude-code-20250219"} {
								if !strings.Contains(request.Header.Get("Anthropic-Beta"), beta) {
									t.Errorf("missing beta %s", beta)
								}
							}
							if !strings.HasPrefix(request.UserAgent(), "claude-cli/") {
								t.Error("missing Claude user agent")
							}
						}
						if test.kind == contract.ServiceKindOpenCodeGo || test.kind == contract.ServiceKindOpenCodeZen {
							if request.Header.Get("X-Opencode-Session") != "client-session" || request.UserAgent() != "astrlink/0.1" {
								t.Error("missing OpenCode session or user agent")
							}
						}
						writer.Header().Set("Content-Type", "application/json")
						if streaming {
							writer.Header().Set("Content-Type", "text/event-stream")
						}
						_, _ = io.WriteString(writer, responseBody)
					}))
					defer upstream.Close()
					service := contract.Service{
						ID: "service_plan", Name: "Coding plan", Kind: test.kind, Enabled: true,
						Models:       []string{test.model},
						Capabilities: []contract.Capability{{Protocol: test.protocol, Mode: contract.CapabilityModeNative, Streaming: true}},
						HTTP:         &contract.HTTPConnection{BaseURL: upstream.URL + test.prefix, Auth: contract.ServiceAuth{Scheme: test.auth}, CredentialRef: "local://service/service_plan"},
					}
					if test.kind.IsSubscription() {
						service.HTTP = nil
						service.Capabilities = test.kind.SubscriptionProvider().Capabilities()
						service.Subscription = &contract.SubscriptionConnection{Provider: test.kind.SubscriptionProvider(), Status: contract.SubscriptionStatusConnected, CredentialRef: "keyring://subscription/service_plan"}
					}
					if err := service.Validate(); err != nil {
						t.Fatal(err)
					}
					handler := NewWithDependencies(Dependencies{
						Resolver:   candidateResolver{candidates: []endpoint.Resolved{{Service: service, BaseURL: upstream.URL + test.prefix, UpstreamProtocol: test.protocol}}},
						Authorizer: endpoint.NewServiceAuthorizer(codingPlanCredentials{}, codingPlanCredentials{}),
					})
					body := fmt.Sprintf(`{"model":%q,"stream":%t,"max_tokens":32,"system":"Keep my instructions","messages":[{"role":"user","content":"hello"}]}`, test.model, streaming)
					request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(body))
					request.Header.Set("Content-Type", "application/json")
					request.Header.Set("Authorization", "Bearer local-secret")
					request.Header.Set("X-Api-Key", "local-secret")
					request.Header.Set("Anthropic-Version", "2023-06-01")
					request.Header.Set("Anthropic-Beta", "client-feature")
					request.Header.Set("X-Opencode-Session", "client-session")
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					if !called.Load() || response.Code != http.StatusOK || response.Body.String() != responseBody {
						t.Fatalf("forwarding failed: called=%t status=%d body=%s", called.Load(), response.Code, response.Body.String())
					}
				})
			}
		})
	}
}
