package ingress

import (
	"context"
	"encoding/json"
	"errors"
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

func vertexServiceForTest() contract.Service {
	vertex := &contract.VertexConnection{ProjectID: "vertex-test", Location: "global"}
	service := contract.Service{
		ID: "service_vertex", Name: "Vertex", Kind: contract.ServiceKindVertex, Enabled: true,
		Models: []string{"gemini-test", "claude-test@20250101", "meta/llama-test-maas"},
		ModelProtocols: []contract.ModelProtocol{
			{Model: "gemini-test", Protocol: contract.ProtocolGoogleGenerateContent},
			{Model: "claude-test@20250101", Protocol: contract.ProtocolAnthropicMessages},
			{Model: "meta/llama-test-maas", Protocol: contract.ProtocolOpenAIChat},
		},
		HTTP: &contract.HTTPConnection{BaseURL: vertex.BaseURL(), Vertex: vertex, Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeGoogleServiceAccount}, CredentialRef: "local://service/service_vertex"},
	}
	for _, protocol := range []contract.ProtocolID{contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIChat, contract.ProtocolAnthropicMessages, contract.ProtocolGoogleGenerateContent, contract.ProtocolOpenAIModels, contract.ProtocolGoogleModels} {
		capability := contract.Capability{Protocol: protocol, Mode: contract.CapabilityModeNative, Streaming: protocol.IsModelFormat()}
		if protocol == contract.ProtocolOpenAIResponses {
			capability.ConvertTo = contract.ProtocolGoogleGenerateContent
		}
		service.Capabilities = append(service.Capabilities, capability)
	}
	return service
}

func vertexHandlerForTest(t *testing.T, upstream roundTripFunc, services ...contract.Service) http.Handler {
	t.Helper()
	for _, service := range services {
		if err := service.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	resolver, err := endpoint.NewStoreResolver(redirectServices(services))
	if err != nil {
		t.Fatal(err)
	}
	engine := relaykitbridge.NewEngine()
	resolver.WithRuntimeProfile(contract.RuntimeProfile{RelayKitAvailable: true, Edges: engine.Edges()})
	return NewWithDependencies(Dependencies{
		Resolver: resolver, ConversionEngine: engine,
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
			return http.Header{"Authorization": {"Bearer google-token"}}, nil
		}),
		Forwarder: transport.New(upstream),
	})
}

func TestVertexConvertsClientProtocolsAfterModelRedirect(t *testing.T) {
	for _, setting := range []string{"model", "entry"} {
		for _, model := range vertexServiceForTest().Models {
			for _, entry := range []contract.ProtocolID{contract.ProtocolOpenAIChat, contract.ProtocolOpenAIResponses} {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/stream=%t", setting, model, entry, stream), func(t *testing.T) {
						service := vertexServiceForTest()
						target := service.ModelProtocol(model)
						if setting == "entry" {
							service.ModelProtocols = nil
						} else if target == contract.ProtocolGoogleGenerateContent {
							target = contract.ProtocolAnthropicMessages
						} else {
							target = contract.ProtocolGoogleGenerateContent
						}
						for i := range service.Capabilities {
							if service.Capabilities[i].Protocol == entry {
								service.Capabilities[i].ConvertTo = target
								if target == entry {
									service.Capabilities[i].ConvertTo = ""
								}
							}
						}
						service.ModelRedirects = []contract.ModelRedirect{enabledRedirect("public-alias", model)}
						calls := 0
						handler := vertexHandlerForTest(t, func(r *http.Request) (*http.Response, error) {
							calls++
							if r.Header.Get("Authorization") != "Bearer google-token" || r.Header.Get("X-Api-Key") != "" || r.Header.Get("X-AstrLink-Test") != "" {
								t.Error("client credentials or identity escaped")
							}
							var payload map[string]any
							if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
								t.Fatal(err)
							}
							const root = "/v1/projects/vertex-test/locations/global"
							switch {
							case strings.HasPrefix(model, "gemini-"):
								checkGeminiCall(t, r, payload, root+"/publishers/google", model, stream)
								return geminiReply(stream), nil
							case strings.HasPrefix(model, "claude-"):
								action := "rawPredict"
								if stream {
									action = "streamRawPredict"
								}
								if r.URL.Path != root+"/publishers/anthropic/models/"+model+":"+action || payload["model"] != nil || payload["messages"] == nil || payload["anthropic_version"] != "vertex-2023-10-16" {
									t.Errorf("Claude request: %s %v", r.URL, payload)
								}
								_, _, body := nativeProviderExchange(contract.ProtocolAnthropicMessages, false)
								response := jsonResponse(200, body)
								if stream {
									response = jsonResponse(200, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-test\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
									response.Header.Set("Content-Type", "text/event-stream")
								}
								return response, nil
							default:
								if r.URL.Path != root+"/endpoints/openapi/chat/completions" || payload["model"] != model || payload["messages"] == nil {
									t.Errorf("Chat request: %s %v", r.URL, payload)
								}
								_, _, body := nativeProviderExchange(contract.ProtocolOpenAIChat, stream)
								response := jsonResponse(200, body)
								if stream {
									response.Header.Set("Content-Type", "text/event-stream")
								}
								return response, nil
							}
						}, service)
						path, body, _ := nativeProviderExchange(entry, stream)
						body = strings.Replace(body, "model-test", "public-alias", 1)
						request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
						request.Header.Set("Content-Type", "application/json")
						request.Header.Set("Authorization", "Bearer client-key")
						request.Header.Set("X-Api-Key", "client-key")
						request.Header.Set("X-AstrLink-Test", "local-only")
						response := httptest.NewRecorder()
						handler.ServeHTTP(response, request)
						want := `"content":"ok"`
						if entry == contract.ProtocolOpenAIResponses {
							want = `"output_text"`
							if stream {
								want = "response.completed"
							}
						}
						if calls != 1 {
							t.Fatalf("calls=%d response=%s", calls, response.Body)
						}
						checkConvertedBack(t, response, want)
					})
				}
			}
		}
	}

}

func TestVertexNativeEntriesAfterClearingModelFormats(t *testing.T) {
	for _, format := range vertexServiceForTest().ModelProtocols {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", format.Protocol, stream), func(t *testing.T) {
				service := vertexServiceForTest()
				service.ModelProtocols = nil
				path, body, reply := nativeProviderExchange(format.Protocol, stream)
				body = strings.Replace(body, "model-test", format.Model, 1)
				upstreamPath := "/endpoints/openapi/chat/completions"
				if format.Protocol == contract.ProtocolGoogleGenerateContent {
					action := "generateContent"
					if stream {
						action = "streamGenerateContent"
					}
					path = "/v1beta/models/" + format.Model + ":" + action
					upstreamPath = "/publishers/google/models/" + format.Model + ":" + action
					body = `{"contents":[{"role":"user","parts":[{"text":"AstrLink caller content","thoughtSignature":"signed"}]}]}`
					reply = geminiTestReply
					if stream {
						reply = "data: " + reply + "\n\n"
					}
				} else if format.Protocol == contract.ProtocolAnthropicMessages {
					upstreamPath = "/publishers/anthropic/models/" + format.Model + ":rawPredict"
					if stream {
						upstreamPath = "/publishers/anthropic/models/" + format.Model + ":streamRawPredict"
					}
				}
				body = strings.TrimSuffix(body, "}") + `,"vendor_field":{"large":9007199254740993}}`
				calls := 0
				handler := vertexHandlerForTest(t, func(request *http.Request) (*http.Response, error) {
					calls++
					if request.URL.Path != "/v1/projects/vertex-test/locations/global"+upstreamPath || request.Header.Get("Authorization") != "Bearer google-token" || request.Header.Get("X-AstrLink-Test") != "" {
						t.Errorf("outbound request=%s headers=%v", request.URL, request.Header)
					}
					out, err := io.ReadAll(request.Body)
					if err != nil || !strings.Contains(string(out), `"vendor_field":{"large":9007199254740993}`) {
						t.Errorf("lost native fields: %s error=%v", out, err)
					}
					response := jsonResponse(200, reply)
					if stream {
						response.Header.Set("Content-Type", "text/event-stream")
					}
					return response, nil
				}, service)
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("X-AstrLink-Test", "local-only")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if calls != 1 || response.Code != 200 || response.Body.String() != reply {
					t.Fatalf("native response=%d %s calls=%d", response.Code, response.Body, calls)
				}
			})
		}
	}
}

func TestVertexDoesNotGuessAnUpstreamProtocol(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(fmt.Sprintf("conversion_unavailable=%t", unavailable), func(t *testing.T) {
			service := vertexServiceForTest()
			service.ModelProtocols = nil
			upstream := roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("invalid routing reached upstream")
				return nil, errors.New("unexpected upstream")
			})
			handler := vertexHandlerForTest(t, upstream, service)
			path, body, _ := nativeProviderExchange(contract.ProtocolOpenAIChat, false)
			body = strings.Replace(body, "model-test", "gemini-test", 1)
			if unavailable {
				resolver, err := endpoint.NewStoreResolver(redirectServices{service})
				if err != nil {
					t.Fatal(err)
				}
				handler = NewWithDependencies(Dependencies{Resolver: resolver, Forwarder: transport.New(upstream)})
				path, body, _ = nativeProviderExchange(contract.ProtocolOpenAIResponses, false)
				body = strings.Replace(body, "model-test", "gemini-test", 1)
			}
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code < 400 {
				t.Fatalf("accepted unsupported route: %d %s", response.Code, response.Body)
			}
		})
	}
}

func TestVertexClientModelsUseConfiguredIDsAndAliasesWithoutCatalog(t *testing.T) {
	service := vertexServiceForTest()
	resolver, err := endpoint.NewStoreResolver(redirectServices{service})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewWithDependencies(Dependencies{
		Resolver:       resolver,
		RequestRecords: newRedirectSettingsStore(enabledRedirect("public-alias", "gemini-test")),
		Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Error("client models fetched upstream catalog")
			return nil, errors.New("unexpected upstream")
		})),
	})
	for _, path := range []string{"/v1/models", "/v1beta/models"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != 200 {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body)
		}
		for _, id := range append(service.Models, "public-alias") {
			if !strings.Contains(response.Body.String(), id) {
				t.Errorf("missing %s: %s", id, response.Body)
			}
		}
	}
}

func TestVertexErrorsAndInterruptedStreamsDoNotReplay(t *testing.T) {
	for _, protocol := range []contract.ProtocolID{contract.ProtocolGoogleGenerateContent, contract.ProtocolAnthropicMessages, contract.ProtocolOpenAIChat} {
		for _, interrupt := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/interrupted=%t", protocol, interrupt), func(t *testing.T) {
				service := vertexServiceForTest()
				second := vertexServiceForTest()
				second.ID = "service_vertex_second"
				second.HTTP.CredentialRef = "local://service/service_vertex_second"
				calls := 0
				path, body, reply := nativeProviderExchange(protocol, interrupt)
				model := "meta/llama-test-maas"
				if protocol == contract.ProtocolAnthropicMessages {
					model = "claude-test@20250101"
				}
				body = strings.Replace(body, "model-test", model, 1)
				if protocol == contract.ProtocolGoogleGenerateContent {
					action := "generateContent"
					if interrupt {
						action = "streamGenerateContent"
					}
					path = "/v1beta/models/gemini-test:" + action
					body = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`
					reply = "data: " + geminiTestReply + "\n\n"
				}
				handler := vertexHandlerForTest(t, func(*http.Request) (*http.Response, error) {
					calls++
					if !interrupt {
						return jsonResponse(400, `{"error":{"message":"model unavailable"}}`), nil
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(io.MultiReader(strings.NewReader(reply), failingReader{err: errors.New("interrupted")}))}, nil
				}, service, second)
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				handler.ServeHTTP(response, request)
				if calls != 1 {
					t.Fatalf("replayed request %d times: %s", calls, response.Body)
				}
				if interrupt {
					if response.Code != 200 || !strings.Contains(response.Body.String(), reply) {
						t.Fatalf("partial stream: %d %s", response.Code, response.Body)
					}
				} else if response.Code != 400 || !strings.Contains(response.Body.String(), "model unavailable") {
					t.Fatalf("error: %d %s", response.Code, response.Body)
				}
			})
		}
	}
}

func TestVertexToolHistoryThroughChatConversion(t *testing.T) {
	for _, model := range vertexServiceForTest().Models {
		t.Run(model, func(t *testing.T) {
			calls := 0
			handler := vertexHandlerForTest(t, func(r *http.Request) (*http.Response, error) {
				calls++
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatal(err)
				}
				for _, want := range []string{"weather", "call-1", "20"} {
					if !strings.Contains(string(body), want) {
						t.Errorf("lost tool history %s: %s", want, body)
					}
				}
				switch {
				case strings.HasPrefix(model, "gemini-"):
					if payload["contents"] == nil || !strings.Contains(string(body), "functionResponse") {
						t.Errorf("missing Gemini tool result: %s", body)
					}
					return jsonResponse(200, `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call-2","name":"weather","args":{}}}]},"finishReason":"STOP"}]}`), nil
				case strings.HasPrefix(model, "claude-"):
					if payload["model"] != nil || !strings.Contains(string(body), "tool_use_id") {
						t.Errorf("missing Claude tool result: %s", body)
					}
					return jsonResponse(200, `{"id":"msg_test","type":"message","role":"assistant","model":"claude-test","content":[{"type":"tool_use","id":"call-2","name":"weather","input":{}}],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`), nil
				default:
					return jsonResponse(200, `{"id":"chat_test","model":"meta/llama-test-maas","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call-2","type":"function","function":{"name":"weather","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`), nil
				}
			}, vertexServiceForTest())
			body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"weather?"},{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"weather","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call-1","content":"20"}],"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object"}}}]}`, model)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(response, request)
			if calls != 1 || response.Code != 200 || !strings.Contains(response.Body.String(), `"tool_calls"`) || !strings.Contains(response.Body.String(), "call-2") {
				t.Fatalf("tool response: %d %s", response.Code, response.Body)
			}
		})
	}
}
