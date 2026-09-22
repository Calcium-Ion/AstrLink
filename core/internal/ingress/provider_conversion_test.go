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
