package ingress

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestRelayKitUsesProviderSurfaceAfterConversion(t *testing.T) {
	for _, kind := range []contract.ServiceKind{contract.ServiceKindDeepSeek, contract.ServiceKindGemini} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", kind, streaming), func(t *testing.T) {
				protocol, base, auth := contract.ProtocolAnthropicMessages, "https://provider.example/v1", contract.AuthSchemeBearer
				if kind == contract.ServiceKindGemini {
					protocol, base, auth = contract.ProtocolGoogleGenerateContent, "https://provider.example", contract.AuthSchemeGoogleAPIKey
				}
				service := contract.Service{
					ID: "service_api", Name: "API", Kind: kind, Enabled: true, Models: []string{"model-test"},
					Capabilities: []contract.Capability{
						{Protocol: protocol, Mode: contract.CapabilityModeNative, Streaming: true},
						{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true, ConvertTo: protocol},
					},
					HTTP: &contract.HTTPConnection{BaseURL: base, Auth: contract.ServiceAuth{Scheme: auth}, CredentialRef: "local://service/service_api"},
				}
				called := false
				handler := NewWithDependencies(Dependencies{
					Resolver:         candidateResolver{candidates: []endpoint.Resolved{{Service: service, BaseURL: base, PlanType: contract.PlanTypeRelayKit, UpstreamProtocol: protocol}}},
					Authorizer:       endpoint.NewServiceAuthorizer(codingPlanCredentials{}, nil),
					ConversionEngine: relaykitbridge.NewEngine(),
					Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
						called = true
						wantPath, keyHeader := "/anthropic/v1/messages", "X-Api-Key"
						body, _ := io.ReadAll(request.Body)
						var payload map[string]any
						if err := json.Unmarshal(body, &payload); err != nil {
							t.Fatal(err)
						}
						_, _, responseBody := nativeProviderExchange(protocol, streaming)
						if kind == contract.ServiceKindGemini {
							keyHeader = "X-Goog-Api-Key"
							wantPath = "/v1beta/models/model-test:generateContent"
							if payload["contents"] == nil || payload["messages"] != nil {
								t.Fatalf("not converted to Gemini: %s", body)
							}
							responseBody = `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`
							if streaming {
								wantPath = "/v1beta/models/model-test:streamGenerateContent"
								if request.URL.Query().Get("alt") != "sse" {
									t.Fatal("missing Gemini SSE query")
								}
								responseBody = "data: " + responseBody + "\n\n"
							}
						}
						if request.URL.Path != wantPath || request.Header.Get(keyHeader) != "plan-key" || request.Header.Get("Authorization") != "" {
							t.Fatalf("wrong converted target/auth: %s", request.URL)
						}
						response := jsonResponse(http.StatusOK, responseBody)
						if streaming {
							response.Header.Set("Content-Type", "text/event-stream")
						}
						return response, nil
					})),
				})
				path, body, _ := nativeProviderExchange(contract.ProtocolOpenAIChat, streaming)
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", "Bearer local-secret")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if !called || response.Code != http.StatusOK {
					t.Fatalf("called=%t status=%d body=%s", called, response.Code, response.Body.String())
				}
				if kind == contract.ServiceKindGemini && (!strings.Contains(response.Body.String(), `"content":"ok"`) || strings.Contains(response.Body.String(), `"candidates"`)) {
					t.Fatalf("not converted back to Chat: %s", response.Body.String())
				}
			})
		}
	}
}

func TestRelayKitConvertsIntoSubscriptionEgress(t *testing.T) {
	for _, test := range []struct {
		kind              contract.ServiceKind
		ingress, upstream contract.ProtocolID
		wantPath          string
		check             func(t *testing.T, payload map[string]any)
	}{
		{
			kind: contract.ServiceKindCodexSubscription, ingress: contract.ProtocolAnthropicMessages,
			upstream: contract.ProtocolOpenAIResponses, wantPath: "/backend-api/codex/responses",
			check: func(t *testing.T, payload map[string]any) {
				if payload["store"] != false || payload["instructions"] != "" || payload["input"] == nil {
					t.Fatalf("Codex body not prepared: %#v", payload)
				}
				if _, ok := payload["max_output_tokens"]; ok {
					t.Fatalf("Codex body kept max_output_tokens: %#v", payload)
				}
			},
		},
		{
			kind: contract.ServiceKindClaudeSubscription, ingress: contract.ProtocolOpenAIChat,
			upstream: contract.ProtocolAnthropicMessages, wantPath: "/v1/messages",
			check: func(t *testing.T, payload map[string]any) {
				system, _ := payload["system"].([]any)
				if len(system) == 0 || !strings.Contains(fmt.Sprint(system[0]), claudeCodeBanner) || payload["messages"] == nil {
					t.Fatalf("Claude body not prepared: %#v", payload)
				}
			},
		},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			provider := test.kind.SubscriptionProvider()
			service := contract.Service{
				ID: "service_subscription", Name: "Subscription", Kind: test.kind, Enabled: true, Models: []string{"model-test"},
				Capabilities: append(provider.Capabilities(), contract.Capability{
					Protocol: test.ingress, Mode: contract.CapabilityModeNative, Streaming: true, ConvertTo: test.upstream,
				}),
				Subscription: &contract.SubscriptionConnection{
					Provider: provider, Status: contract.SubscriptionStatusConnected,
					CredentialRef: "keyring://subscription/service_subscription",
				},
			}
			if err := service.Validate(); err != nil {
				t.Fatal(err)
			}
			base := "https://chatgpt.example/backend-api/codex"
			if test.kind == contract.ServiceKindClaudeSubscription {
				base = "https://claude.example"
			}
			called := false
			handler := NewWithDependencies(Dependencies{
				Resolver:         candidateResolver{candidates: []endpoint.Resolved{{Service: service, BaseURL: base, UpstreamProtocol: test.upstream}}},
				Authorizer:       endpoint.NewServiceAuthorizer(codingPlanCredentials{}, codingPlanCredentials{}),
				ConversionEngine: relaykitbridge.NewEngine(),
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					called = true
					if request.URL.Path != test.wantPath || request.Header.Get("Authorization") != "Bearer subscription-token" {
						t.Fatalf("wrong converted target/auth: %s", request.URL)
					}
					// The source SDK's fingerprint must not be mixed into the
					// subscription client's identity: Codex sends none, and
					// Claude sends only the Claude Code release's own set.
					for name := range request.Header {
						lower := strings.ToLower(name)
						foreign := "anthropic-"
						if test.kind == contract.ServiceKindClaudeSubscription {
							foreign = "openai-"
						}
						stainless := strings.HasPrefix(lower, "x-stainless-") &&
							(test.kind != contract.ServiceKindClaudeSubscription || lower == "x-stainless-helper-method")
						if stainless || strings.HasPrefix(lower, foreign) {
							t.Fatalf("converted request forwarded source header %s: %v", name, request.Header)
						}
					}
					if test.kind == contract.ServiceKindClaudeSubscription &&
						(request.Header.Get("X-Stainless-Runtime") != "node" || request.Header.Get("X-App") != "cli") {
						t.Fatalf("converted Claude request lacks the Claude Code SDK headers: %v", request.Header)
					}
					for name, values := range request.Header {
						if strings.Contains(strings.ToLower(name+strings.Join(values, ",")), "astrlink") {
							t.Fatalf("forwarded header carries gateway branding: %s: %v", name, values)
						}
					}
					if beta := request.Header.Get("Anthropic-Beta"); test.kind == contract.ServiceKindClaudeSubscription &&
						(strings.Contains(beta, "files-api") || !strings.Contains(beta, "interleaved-thinking-2025-05-14")) {
						t.Fatalf("converted Anthropic-Beta = %q", beta)
					}
					body, _ := io.ReadAll(request.Body)
					var payload map[string]any
					if err := json.Unmarshal(body, &payload); err != nil {
						t.Fatal(err)
					}
					if strings.Contains(strings.ToLower(string(body)), "astrlink") {
						t.Fatalf("forwarded body carries gateway branding: %s", body)
					}
					test.check(t, payload)
					_, _, responseBody := nativeProviderExchange(test.upstream, false)
					return jsonResponse(http.StatusOK, responseBody), nil
				})),
			})
			path, body, _ := nativeProviderExchange(test.ingress, false)
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer local-secret")
			request.Header.Set("X-Stainless-Lang", "js")
			request.Header.Set("X-Stainless-Helper-Method", "stream")
			request.Header.Set("OpenAI-Organization", "org-client")
			request.Header.Set("Anthropic-Beta", "files-api-2025-04-14,interleaved-thinking-2025-05-14")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if !called || response.Code != http.StatusOK {
				t.Fatalf("called=%t status=%d body=%s", called, response.Code, response.Body.String())
			}
		})
	}
}
