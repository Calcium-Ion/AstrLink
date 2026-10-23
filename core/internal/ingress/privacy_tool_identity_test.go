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
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"github.com/tidwall/gjson"
)

func TestPrivacyPreservesToolNamespaceOnUpstreamRequest(t *testing.T) {
	const namespace = "mcp__cua_repl"
	const body = `{"model":"gpt-test","input":[
		{"type":"function_call","namespace" : "mcp\u005f_cua_repl","name":"js","call_id":"call_fixture","arguments":"{\"namespace\":\"mcp__cua_repl\"}"},
		{"type":"function_call_output","call_id":"call_fixture","output":"alice@example.com"},
		{"role":"user","content":"continue"}
	],"tools":[{"type":"namespace","name":"mcp__cua_repl","tools":[{"type":"function","name":"js","parameters":{}}]}]}`
	for _, mode := range []privacy.Mode{privacy.ModeRegex, privacy.ModeLocalModel} {
		t.Run(string(mode), func(t *testing.T) {
			rules := []contract.PolicyRegexRule{
				{Kind: string(privacy.KindCommonSecret), Pattern: `mcp__cua_repl|call_fixture|\bjs\b`},
				{Kind: string(privacy.KindEmail), Pattern: `alice@example\.com`},
			}
			// Make the detector flag the tool identity whenever extraction
			// exposes it, without depending on a particular local model.
			model, err := privacy.NewCustomRegexDetector(rules)
			if err != nil {
				t.Fatal(err)
			}
			filter := testPrivacyEngine(t, privacy.Policy{
				Enabled: true, Mode: mode, Action: privacy.ActionRedact,
				LocalModelID: "model_00000000000000000000000000000001",
				RegexSource:  contract.PolicyRegexSourceCustom, CustomRegexRules: rules,
				InspectToolDeclarations: true,
			}, model)
			upstream := validEndpoint(contract.ProtocolOpenAIResponses, false)
			forwarded := 0
			handler := NewWithDependencies(Dependencies{
				Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
					return endpoint.Resolved{Endpoint: upstream}, nil
				}),
				PrivacyFilter: filter,
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					forwarded++
					outbound, err := io.ReadAll(request.Body)
					if err != nil {
						t.Fatal(err)
					}
					for path, want := range map[string]string{
						"input.0.namespace": namespace, "input.0.name": "js",
						"input.0.call_id": "call_fixture", "input.1.call_id": "call_fixture",
						"tools.0.name": namespace, "tools.0.tools.0.name": "js",
					} {
						if got := gjson.GetBytes(outbound, path).Str; got != want {
							t.Errorf("upstream %s = %q, want %q", path, got, want)
						}
					}
					if !strings.Contains(string(outbound), `"namespace" : "mcp\u005f_cua_repl"`) {
						t.Error("rewriting content changed the raw namespace bytes")
					}
					arguments := gjson.GetBytes(outbound, "input.0.arguments").Str
					if !gjson.Valid(arguments) || strings.Contains(arguments, namespace) || strings.Contains(string(outbound), "alice@example.com") {
						t.Error("tool arguments or results escaped valid redaction")
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"id":"resp_fixture","output":[]}`)),
					}, nil
				})),
			})
			// Replayed history must keep working on subsequent requests too.
			for range 2 {
				request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusOK {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
			}
			if forwarded != 2 {
				t.Fatalf("upstream attempts=%d, want one per request", forwarded)
			}
		})
	}
}
