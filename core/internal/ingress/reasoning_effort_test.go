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
			got, err := classify(request)
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
