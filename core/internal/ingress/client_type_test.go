package ingress

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestDetectClientType(t *testing.T) {
	for _, test := range []struct {
		name, ua, originator, title string
		extraHeader, extraValue     string
		want                        contract.ClientType
	}{
		{name: "Codex CLI", ua: "codex_cli_rs/0.144.0 (Mac OS; arm64)", want: contract.ClientCodex},
		{name: "Codex desktop", ua: "codex_app/2026.925", want: contract.ClientCodex},
		{name: "Codex originator", ua: "openai-python/2.0", originator: "codex_vscode", want: contract.ClientCodex},
		{name: "Claude Code", ua: "claude-cli/2.1.2 (external, cli)", want: contract.ClientClaudeCode},
		{name: "Cursor", ua: "Mozilla/5.0 Cursor/1.0.0", want: contract.ClientCursor},
		{name: "Grok", ua: "xai-grok-workspace/1.0.0", want: contract.ClientGrokCLI},
		{name: "Gemini", ua: "GeminiCLI/0.34.0/gemini-pro (linux; x64)", want: contract.ClientGeminiCLI},
		{name: "Gemini server", ua: "GeminiCLI-a2a-server/0.34.0", want: contract.ClientGeminiCLI},
		{name: "OpenCode", ua: "opencode/1.0.0", want: contract.ClientOpenCode},
		{name: "OpenClaw", ua: "OpenClaw/1.0.0", want: contract.ClientOpenClaw},
		{name: "Cline SDK", ua: "OpenAI/JS 5.0.0", title: "Cline", want: contract.ClientCline},
		{name: "Pi native", ua: "pi (darwin 25.0.0; arm64)", want: contract.ClientPi},
		{name: "Pi browser", ua: "pi (browser)", want: contract.ClientPi},
		{name: "Pi versioned", ua: "pi/0.74.0 (linux; x64)", want: contract.ClientPi},
		{name: "Pi Cloudflare", ua: "pi-coding-agent", want: contract.ClientPi},
		{name: "Pi originator", ua: "OpenAI/JS 5.0.0", originator: "pi", want: contract.ClientPi},
		{name: "Pi title", ua: "OpenAI/JS 5.0.0", title: " Pi ", want: contract.ClientPi},
		{name: "Pi OpenRouter", ua: "OpenAI/JS 5.0.0", extraHeader: "X-OpenRouter-Title", extraValue: "pi", want: contract.ClientPi},
		{name: "Pi OpenCode", ua: "OpenAI/JS 5.0.0", extraHeader: "X-OpenCode-Client", extraValue: "pi", want: contract.ClientPi},
		{name: "Pi NVIDIA", ua: "OpenAI/JS 5.0.0", extraHeader: "X-Billing-Invoke-Origin", extraValue: "Pi", want: contract.ClientPi},
		{name: "Pi application beats provider identity", ua: "codex_cli_rs/0.144.0", originator: "codex_cli_rs", title: "pi", want: contract.ClientPi},
		{name: "application beats provider identity", ua: "claude-cli/2.1.2", originator: "codex_cli_rs", title: "OpenCode", want: contract.ClientOpenCode},
		{name: "case and whitespace", ua: "  CODEX-TUI/0.1.0  ", want: contract.ClientCodex},
		{name: "generic SDK", ua: "openai-python/2.0", want: contract.ClientUnknown},
		{name: "generic CLI", ua: "node", title: "cli", want: contract.ClientUnknown},
		{name: "missing", want: contract.ClientUnknown},
		{name: "substring is not a product", ua: "not-codex_cli_rs/1.0", want: contract.ClientUnknown},
		{name: "Pi substring is not a product", ua: "raspberrypi/1.0", want: contract.ClientUnknown},
		{name: "Pi prefix is not a product", ua: "pi-hole/1.0", want: contract.ClientUnknown},
		{name: "Pi URL is not a product", ua: "https://pi.dev", want: contract.ClientUnknown},
		{name: "URL is not a product", ua: "https://example.com/codex", want: contract.ClientUnknown},
		{name: "invalid header", ua: "codex\n", want: contract.ClientUnknown},
		{name: "bounded header", ua: "codex/" + strings.Repeat("x", 1024), want: contract.ClientUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			headers := make(http.Header)
			headers.Set("User-Agent", test.ua)
			headers.Set("originator", test.originator)
			headers.Set("X-Title", test.title)
			if test.extraHeader != "" {
				headers.Set(test.extraHeader, test.extraValue)
			}
			before := headers.Clone()
			if got := detectClientType(headers); got != test.want {
				t.Fatalf("client = %q, want %q", got, test.want)
			}
			if !reflect.DeepEqual(headers, before) {
				t.Fatal("detection changed inbound headers")
			}
		})
	}
}

func TestClientTypeSurvivesUpstreamIdentityAndRetry(t *testing.T) {
	for _, test := range []struct {
		ua   string
		want contract.ClientType
	}{
		{ua: "Cursor/1.0.0", want: contract.ClientCursor},
		{ua: "pi (darwin 25.0.0; arm64)", want: contract.ClientPi},
	} {
		t.Run(string(test.want), func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			request.Header.Set("User-Agent", test.ua)
			handler := NewWithDependencies(Dependencies{})
			session := handler.startRecordSession(request, Request{Protocol: contract.ProtocolOpenAIResponses})
			if session.settings.RequestBodyEnabled || session.settings.ResponseContentEnabled {
				t.Fatal("test must not depend on body capture")
			}
			request.Header.Set("User-Agent", "codex_cli_rs/0.144.0")
			request.Header.Set("originator", "codex_cli_rs")
			if got := session.recordSnapshot(nil, nil).ClientType; got != test.want {
				t.Fatalf("client after upstream rewrite = %q", got)
			}
			session.resetAttemptLocal()
			if got := session.recordSnapshot(nil, nil).ClientType; got != test.want {
				t.Fatalf("client after retry = %q", got)
			}
		})
	}
}

func TestClientTypeRecordedOnBoundaryFailure(t *testing.T) {
	harness := newSessionHarness(t, contract.ProtocolOpenAIResponses, "/v1/responses", sessionHarnessOptions{})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "text/plain")
	request.Header.Set("User-Agent", "claude-cli/2.1.2")
	response := httptest.NewRecorder()
	harness.handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d", response.Code)
	}
	if got := latestRoot(t, harness.store).ClientType; got != contract.ClientClaudeCode {
		t.Fatalf("boundary failure client = %q", got)
	}
}
