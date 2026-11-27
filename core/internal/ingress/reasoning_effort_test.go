package ingress

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestClassifyExplicitReasoningEffortPreservesBody(t *testing.T) {
	for _, test := range []struct{ name, path, body, want string }{
		{"chat", "/v1/chat/completions", `{"model":"deepseek-v4.1-flash","reasoning_effort":"high"}`, "high"},
		{"responses", "/v1/responses", `{"model":"gpt-5","reasoning":{"effort":"xhigh"}}`, "xhigh"},
		{"compact", "/v1/responses/compact", `{"model":"gpt-5","reasoning":{"effort":"low"}}`, "low"},
		{"anthropic", "/v1/messages", `{"model":"claude-opus","thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`, "max"},
		{"gemini", "/v1beta/models/gemini-3-pro:generateContent", `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"HIGH"}}}`, "high"},
		{"disabled", "/v1/chat/completions", `{"model":"test","reasoning_effort":"none"}`, "none"},
		{"absent", "/v1/responses", `{"model":"gpt-5-high"}`, ""},
		{"budget only", "/v1/messages", `{"model":"claude-opus","thinking":{"type":"enabled","budget_tokens":8192}}`, ""},
		{"unknown level", "/v1/chat/completions", `{"model":"test","reasoning_effort":"private prompt text"}`, ""},
		{"malformed level", "/v1/responses", `{"model":"test","reasoning":{"effort":42}}`, ""},
		{"malformed object", "/v1/responses", `{"model":"test","reasoning":true}`, ""},
		{"null", "/v1/responses", `{"model":"test","reasoning":null}`, ""},
		{"unrelated field", "/v1/messages", `{"model":"test","metadata":{"reasoning_effort":"high"}}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			got, err := classify(request, 0)
			if err != nil {
				t.Fatal(err)
			}
			if test.want == "" {
				if got.ReasoningEffort != nil {
					t.Fatalf("unexpected effort %q", *got.ReasoningEffort)
				}
			} else if got.ReasoningEffort == nil || *got.ReasoningEffort != test.want {
				t.Fatalf("effort = %v, want %q", got.ReasoningEffort, test.want)
			}
			body, err := io.ReadAll(request.Body)
			if err != nil || string(body) != test.body {
				t.Fatalf("forwarded body changed: %q, %v", body, err)
			}
		})
	}
}

func TestReasoningEffortRecordedWithoutBodyCapture(t *testing.T) {
	harness := newSessionHarness(t, contract.ProtocolOpenAIChat, "/v1/chat/completions", sessionHarnessOptions{})
	record := harness.serve(`{"model":"test","reasoning_effort":"high","messages":[{"role":"user","content":"hello"}]}`, `{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`)
	if record.ReasoningEffort == nil || *record.ReasoningEffort != "high" {
		t.Fatalf("effort missing from recorded metadata: %#v", record)
	}
	if record.Audit.RequestBodyCaptured || record.Audit.ResponseContentCaptured {
		t.Fatal("recording reasoning effort must not enable body capture")
	}
}

// Pi changes the level mid conversation with a configuration_update input
// item and leaves the top-level reasoning as it was (#140).
func TestClassifyReasoningUpdateFromConfigurationUpdate(t *testing.T) {
	for _, test := range []struct{ name, path, body, effort, update string }{
		{"pi", "/v1/responses", `{"model":"gpt-6.2-sol","reasoning":{"effort":"medium"},"input":[{"role":"user","content":"请回复 OK"},{"type":"configuration_update","reasoning":{"effort":"high"}}]}`, "medium", "high"},
		{"latest wins", "/v1/responses", `{"model":"m","input":[{"type":"configuration_update","reasoning":{"effort":"low"}},{"role":"user","content":"x"},{"type":"configuration_update","reasoning":{"effort":"XHIGH"}}]}`, "", "xhigh"},
		{"latest without level", "/v1/responses", `{"model":"m","input":[{"type":"configuration_update","reasoning":{"effort":"low"}},{"type":"configuration_update","model":"other"}]}`, "", "low"},
		{"compact", "/v1/responses/compact", `{"model":"m","input":[{"type":"configuration_update","reasoning":{"effort":"max"}}]}`, "", "max"},
		{"none", "/v1/responses", `{"model":"m","reasoning":{"effort":"medium"},"input":[{"role":"user","content":"x"}]}`, "medium", ""},
		{"string input", "/v1/responses", `{"model":"m","input":"hi"}`, "", ""},
		{"unknown level", "/v1/responses", `{"model":"m","input":[{"type":"configuration_update","reasoning":{"effort":"private text"}}]}`, "", ""},
		{"malformed", "/v1/responses", `{"model":"m","input":[{"type":"configuration_update","reasoning":"high"}]}`, "", ""},
		{"other protocol", "/v1/chat/completions", `{"model":"m","input":[{"type":"configuration_update","reasoning":{"effort":"high"}}]}`, "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := classify(httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body)), 0)
			if err != nil {
				t.Fatal(err)
			}
			if value(got.ReasoningEffort) != test.effort || value(got.ReasoningUpdate) != test.update {
				t.Fatalf("effort = %q, update = %q", value(got.ReasoningEffort), value(got.ReasoningUpdate))
			}
		})
	}
}

func value(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
