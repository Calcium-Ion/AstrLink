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
		{contract.ServiceKindGrokSubscription, "grok-4.5", "", "/v1/responses", contract.ProtocolOpenAIResponses, contract.AuthSchemeBearer},
		{contract.ServiceKindGrokSubscription, "grok-composer-2.5-fast", "", "/v1/chat/completions", contract.ProtocolOpenAIChat, contract.AuthSchemeBearer},
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
						if test.kind == contract.ServiceKindGrokSubscription {
							if request.Header.Get("X-XAI-Token-Auth") != "xai-grok-cli" || request.Header.Get("X-Grok-Client-Version") == "" ||
								!strings.HasPrefix(request.UserAgent(), "xai-grok-workspace/") {
								t.Errorf("missing Grok CLI identity: %v", request.Header)
							}
						} else if request.Header.Get("X-XAI-Token-Auth") != "" {
							t.Error("Grok CLI header reached another provider")
						}
						if test.kind == contract.ServiceKindOpenCodeGo || test.kind == contract.ServiceKindOpenCodeZen {
							if request.Header.Get("X-Opencode-Session") != "client-session" || request.UserAgent() != "opencode/1.0.0" {
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
					request.Header.Set("User-Agent", "opencode/1.0.0")
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

type subscriptionIdentitySettings struct{ settings contract.RoutingSettings }

func (store subscriptionIdentitySettings) GetRoutingSettings(context.Context) (contract.RoutingSettings, error) {
	return store.settings, nil
}
func (store subscriptionIdentitySettings) UpdateRoutingSettings(context.Context, contract.RoutingSettings) error {
	return nil
}

func TestSubscriptionIdentityOptOutReachesUpstream(t *testing.T) {
	for _, test := range []struct {
		kind                  contract.ServiceKind
		protocol              contract.ProtocolID
		model, path, clientUA string
	}{
		{contract.ServiceKindClaudeSubscription, contract.ProtocolAnthropicMessages, "claude-sonnet-4-5", "/v1/messages", "claude-cli/2.2.0 (external, cli)"},
		{contract.ServiceKindGrokSubscription, contract.ProtocolOpenAIResponses, "grok-4.5", "/v1/responses", "xai-grok-workspace/0.2.102"},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			var called atomic.Bool
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				called.Store(true)
				if request.UserAgent() != test.clientUA {
					t.Errorf("opt-out UA was overwritten: %q", request.UserAgent())
				}
				if request.Header.Get("Authorization") != "Bearer subscription-token" || request.Header.Get("X-Api-Key") != "" {
					t.Error("credentials not replaced")
				}
				if request.Header.Get("X-AstrLink-Debug") != "" || request.Header.Get("X-Client-Feature") != "keep" {
					t.Error("unexpected forwarding headers")
				}
				if test.kind == contract.ServiceKindGrokSubscription && request.Header.Get("X-Grok-Client-Version") != "0.2.102" {
					t.Error("Grok version did not follow UA")
				}
				if test.kind == contract.ServiceKindClaudeSubscription && request.Header.Get("Anthropic-Beta") != "claude-code-20250219,oauth-2025-04-20,client-feature" {
					t.Errorf("invalid beta headers: %s", request.Header.Get("Anthropic-Beta"))
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, `{"id":"test","content":[]}`)
			}))
			defer upstream.Close()
			settings := contract.DefaultRoutingSettings()
			settings.ClaudeIdentityEnforcement = false
			settings.GrokIdentityEnforcement = false
			service := contract.Service{ID: "service_identity", Name: "Subscription", Kind: test.kind, Enabled: true, Models: []string{test.model}, Capabilities: test.kind.SubscriptionProvider().Capabilities(), Subscription: &contract.SubscriptionConnection{Provider: test.kind.SubscriptionProvider(), Status: contract.SubscriptionStatusConnected, CredentialRef: "keyring://subscription/service_identity"}}
			handler := NewWithDependencies(Dependencies{
				Resolver:   candidateResolver{candidates: []endpoint.Resolved{{Service: service, BaseURL: upstream.URL, UpstreamProtocol: test.protocol}}},
				Authorizer: endpoint.NewServiceAuthorizer(codingPlanCredentials{}, codingPlanCredentials{}).WithRoutingSettings(subscriptionIdentitySettings{settings}),
			})
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(fmt.Sprintf(`{"model":%q,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`, test.model)))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("User-Agent", test.clientUA)
			request.Header.Set("Authorization", "Bearer local-token")
			request.Header.Set("X-Api-Key", "local-key")
			request.Header.Set("X-Grok-Client-Version", "0.0.1")
			request.Header.Set("X-AstrLink-Debug", "local-only")
			request.Header.Set("X-Client-Feature", "keep")
			request.Header.Set("Anthropic-Beta", "client-feature,oauth-2025-04-20")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || !called.Load() {
				t.Fatalf("forwarding failed: %d %s", response.Code, response.Body.String())
			}
		})
	}
}
