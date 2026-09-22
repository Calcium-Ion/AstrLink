package ingress

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestPrivacyPreservesEncryptedReasoningAcrossResponsesTurns(t *testing.T) {
	for _, mode := range []privacy.Mode{privacy.ModeRegex, privacy.ModeLocalModel} {
		t.Run(string(mode), func(t *testing.T) {
			cipher := "gAAAAA" + strings.Repeat("A", 2012) + "=="
			rules := []contract.PolicyRegexRule{
				{Kind: string(privacy.KindCommonSecret), Pattern: `gAAAAA[A-Za-z0-9_=-]+`},
				{Kind: string(privacy.KindEmail), Pattern: `alice@example\.com`},
			}
			// Simulate a model classifying ciphertext as a secret if extraction
			// incorrectly exposes it, independently of the built-in regex catalog.
			model, err := privacy.NewCustomRegexDetector(rules)
			if err != nil {
				t.Fatal(err)
			}
			filter := testPrivacyEngine(t, privacy.Policy{
				Enabled: true, Mode: mode, Action: privacy.ActionRedact,
				LocalModelID: "model_00000000000000000000000000000001",
				RegexSource:  contract.PolicyRegexSourceCustom, CustomRegexRules: rules,
				ResponseRestore: true, PlaceholderNotice: true,
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
					body, err := io.ReadAll(request.Body)
					if err != nil {
						t.Fatal(err)
					}
					text := "hello"
					if forwarded == 2 {
						items := reasoningInput(t, body)
						if len(items) != 3 || reasoningString(items[0]["encrypted_content"]) != cipher ||
							reasoningString(items[0]["id"]) != "rs_1" || string(items[0]["summary"]) != "[]" {
							t.Fatal("forwarding changed replayed reasoning")
						}
						if strings.Contains(string(body), "alice@example.com") {
							t.Fatal("message text was not redacted")
						}
						text = emailPlaceholderFromBody(t, body)
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(`{"id":"resp_1","output":[` +
							`{"type":"reasoning","id":"rs_1","encrypted_content":"` + cipher + `","summary":[]},` +
							`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + text + `"}]}]}`)),
					}, nil
				})),
			})
			body := `{"model":"gpt-5","store":false,"input":[{"role":"user","content":"hello"}]}`
			for turn := 0; turn < 2; turn++ {
				request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusOK {
					t.Fatalf("turn %d status=%d", turn+1, response.Code)
				}
				var result struct {
					Output []json.RawMessage `json:"output"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(response.Body.String(), cipher) {
					t.Fatal("response restoration changed ciphertext")
				}
				if turn == 1 && !strings.Contains(response.Body.String(), "alice@example.com") {
					t.Fatal("response text was not restored")
				}
				input, err := json.Marshal(append(result.Output, json.RawMessage(`{"role":"user","content":"alice@example.com"}`)))
				if err != nil {
					t.Fatal(err)
				}
				body = `{"model":"gpt-5","store":false,"input":` + string(input) + `}`
			}
			if forwarded != 2 {
				t.Fatalf("upstream calls=%d, want one per turn", forwarded)
			}
		})
	}
}
