package transport

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/tidwall/gjson"
)

func TestVertexFinalOutboundRequestsAndNativeResponses(t *testing.T) {
	for _, location := range []string{"global", "us-central1", "us", "eu"} {
		for _, stream := range []bool{false, true} {
			for _, surface := range []string{"gemini", "claude", "chat"} {
				t.Run(fmt.Sprintf("%s/%s/stream=%v", location, surface, stream), func(t *testing.T) {
					vertex := &contract.VertexConnection{ProjectID: "vertex-test", Location: location}
					connection := &contract.HTTPConnection{BaseURL: vertex.BaseURL(), Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeGoogleServiceAccount}, Vertex: vertex}
					path, model, action := "/v1/chat/completions", "meta/llama-test-maas", "/endpoints/openapi/chat/completions"
					body := fmt.Sprintf(`{"model":%q,"stream":%v,"messages":[{"role":"user","content":"AstrLink is caller content"},{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"weather","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call-1","content":"20"}],"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object"}}}],"unknown":{"large":9007199254740993}}`, model, stream)
					if surface == "gemini" {
						actionName := "generateContent"
						if stream {
							actionName = "streamGenerateContent"
						}
						path = "/v1beta/models/gemini-test:" + actionName
						action = "/publishers/google/models/gemini-test:" + actionName
						body = `{"contents":[{"role":"model","parts":[{"functionCall":{"name":"weather","id":"call-1","args":{}},"thoughtSignature":"signed"}]},{"role":"user","parts":[{"functionResponse":{"id":"call-1","name":"weather","response":{"temperature":20}}}]}],"unknown":{"large":9007199254740993}}`
					} else if surface == "claude" {
						path = "/v1/messages"
						body = fmt.Sprintf(`{"model":"claude-test@20250101","stream":%v,"max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"caller thought","signature":"signed"},{"type":"tool_use","id":"call-1","name":"weather","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":"20"}]}],"tools":[{"name":"weather","input_schema":{"type":"object"}}],"unknown":{"large":9007199254740993}}`, stream)
						action = "/publishers/anthropic/models/claude-test@20250101:rawPredict"
						if stream {
							action = "/publishers/anthropic/models/claude-test@20250101:streamRawPredict"
						}
					}
					responseBody := `{"choices":[{"message":{"tool_calls":[{"id":"call-2","type":"function","function":{"name":"weather","arguments":"{}"}}]}}]}`
					if surface == "gemini" {
						responseBody = `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call-2","name":"weather","args":{}},"thoughtSignature":"signed"}]}}]}`
					} else if surface == "claude" {
						responseBody = `{"type":"message","content":[{"type":"tool_use","id":"call-2","name":"weather","input":{}}]}`
					}
					if stream {
						responseBody = "data: " + responseBody + "\n\n"
					}
					forwarder := New(roundTripFunc(func(r *http.Request) (*http.Response, error) {
						if r.URL.String() != vertex.BaseURL()+"/v1/projects/vertex-test/locations/"+location+action+func() string {
							if surface == "gemini" && stream {
								return "?alt=sse"
							}
							return ""
						}() {
							t.Errorf("URL=%s", r.URL)
						}
						if r.Header.Get("Authorization") != "Bearer google-token" || r.Header.Get("X-Goog-User-Project") != "vertex-test" {
							t.Error("incorrect Vertex authentication")
						}
						for _, header := range []string{"X-Api-Key", "X-Goog-Api-Key", "Cookie", "X-AstrLink-Test", "Via"} {
							if r.Header.Get(header) != "" {
								t.Errorf("leaked %s", header)
							}
						}
						out, _ := io.ReadAll(r.Body)
						if gjson.GetBytes(out, "unknown.large").Raw != "9007199254740993" {
							t.Error("changed unknown field")
						}
						if surface == "claude" {
							if gjson.GetBytes(out, "model").Exists() || gjson.GetBytes(out, "anthropic_version").String() != "vertex-2023-10-16" {
								t.Error("incorrect Claude envelope")
							}
							for _, field := range []string{"messages", "tools", "stream"} {
								if gjson.GetBytes(out, field).Raw != gjson.Get(body, field).Raw {
									t.Errorf("changed %s", field)
								}
							}
						} else if string(out) != body {
							t.Error("changed native body")
						}
						return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(responseBody))}, nil
					}))
					request := httptest.NewRequest(http.MethodPost, path+"?key=client-secret&access_token=client-token", strings.NewReader(body))
					for _, header := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "Cookie", "X-AstrLink-Test", "Via"} {
						request.Header.Set(header, "client-secret")
					}
					response := httptest.NewRecorder()
					err := forwarder.Forward(response, request, Target{Service: contract.Service{ID: "service_vertex", Kind: contract.ServiceKindVertex, HTTP: connection}, BaseURL: mustParseURL(t, connection.BaseURL), RequestHeaders: http.Header{"Authorization": {"Bearer google-token"}, "X-AstrLink-Test": {"overlay"}}})
					if err != nil || response.Body.String() != responseBody {
						t.Fatalf("native response=%s error=%v", response.Body.String(), err)
					}
				})
			}
		}
	}
}
