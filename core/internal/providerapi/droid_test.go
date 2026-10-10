package providerapi_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/providerapi"
)

func droidRequest(t *testing.T, path, body string, header http.Header, endpoint string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "https://api.factory.ai"+path, strings.NewReader(body))
	for name, values := range header {
		request.Header[name] = values
	}
	if err := providerapi.DroidRequest(request, endpoint); err != nil {
		t.Fatalf("DroidRequest(%s) = %v", path, err)
	}
	return request
}

const droidLine = providerapi.DroidSystemPromptLine

func TestDroidRequestMapsPathsAndKeepsOnlyDroidHeaders(t *testing.T) {
	client := http.Header{
		"Authorization":         {"Bearer factory_secret"},
		"User-Agent":            {"factory-cli/0.237.0"},
		"X-Factory-Client":      {"cli"},
		"X-Client-Version":      {"0.237.0"},
		"X-Factory-Org-Id":      {"factory-org-7"},
		"Accept":                {"text/event-stream"},
		"Content-Type":          {"application/json; charset=utf-8"},
		"Anthropic-Beta":        {"claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14"},
		"X-Stainless-Lang":      {"js"},
		"Originator":            {"codex_cli_rs"},
		"X-Claude-Code-Session": {"claude-session"},
	}
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for _, tt := range []struct{ path, want, body, provider string }{
		{"/v1/chat/completions", "/api/llm/o/v1/chat/completions", `{"model":"glm-5.3","messages":[{"role":"user","content":"hi"}]}`, "fireworks"},
		{"/v1/responses", "/api/llm/o/v1/responses", `{"model":"gpt-5.4","input":"hi"}`, "openai"},
		{"/v1/messages", "/api/llm/a/v1/messages", `{"model":"claude-opus-4-6-fast","messages":[{"role":"user","content":"hi"}]}`, "anthropic"},
	} {
		request := droidRequest(t, tt.path, tt.body, client, "")
		if request.URL.Path != tt.want || request.URL.Host != "api.factory.ai" {
			t.Fatalf("%s -> %s", tt.path, request.URL)
		}
		allowed := map[string]bool{
			"Authorization": true, "User-Agent": true, "X-Factory-Client": true, "X-Client-Version": true, "X-Factory-Org-Id": true,
			"Accept": true, "Content-Type": true, "X-Api-Provider": true, "X-Session-Id": true, "X-Assistant-Message-Id": true,
		}
		switch tt.want {
		case "/api/llm/a/v1/messages":
			allowed["Anthropic-Beta"], allowed["Anthropic-Version"], allowed["X-Api-Key"] = true, true, true
		case "/api/llm/o/v1/responses":
			allowed["Openai-Platform"] = true
		}
		for name := range request.Header {
			if !allowed[name] {
				t.Fatalf("%s kept client header %s", tt.path, name)
			}
		}
		if request.Header.Get("Authorization") != "Bearer factory_secret" || request.Header.Get("User-Agent") != "factory-cli/0.237.0" ||
			request.Header.Get("X-Api-Provider") != tt.provider || request.Header.Get("Content-Type") != "application/json" ||
			!uuid.MatchString(request.Header.Get("X-Session-Id")) || !uuid.MatchString(request.Header.Get("X-Assistant-Message-Id")) {
			t.Fatalf("%s headers = %v", tt.path, request.Header)
		}
		switch tt.want {
		case "/api/llm/a/v1/messages":
			if request.Header.Get("Anthropic-Version") != "2023-06-01" || request.Header.Get("X-Api-Key") != "placeholder" ||
				request.Header.Get("Anthropic-Beta") != "interleaved-thinking-2025-05-14,fast-mode-2026-02-01" {
				t.Fatalf("messages headers = %v", request.Header)
			}
		case "/api/llm/o/v1/responses":
			if request.Header.Get("Openai-Platform") != "org-bHuLtG1fGmYk5YaOihAAXFBw" {
				t.Fatalf("responses headers = %v", request.Header)
			}
		}
		body, _ := io.ReadAll(request.Body)
		if !strings.Contains(string(body), droidLine) || request.ContentLength != int64(len(body)) {
			t.Fatalf("%s body = %s", tt.path, body)
		}
	}
	eu := droidRequest(t, "/v1/responses", `{"model":"grok-4.7","input":"hi"}`, client, "https://api.eu.factory.ai")
	if eu.URL.Host != "api.eu.factory.ai" || eu.URL.Scheme != "https" || eu.Header.Get("X-Api-Provider") != "xai" || eu.Header.Get("Openai-Platform") != "" {
		t.Fatalf("eu request = %s %v", eu.URL, eu.Header)
	}
	for _, rejected := range []struct{ method, path string }{
		{http.MethodGet, "/v1/models"},
		{http.MethodPost, "/v1/responses/compact"},
		{http.MethodPost, "/v1/embeddings"},
		{http.MethodGet, "/v1/chat/completions"},
	} {
		request := httptest.NewRequest(rejected.method, "https://api.factory.ai"+rejected.path, strings.NewReader("{}"))
		if err := providerapi.DroidRequest(request, ""); err == nil {
			t.Fatalf("accepted %s %s", rejected.method, rejected.path)
		}
	}
	if err := providerapi.DroidRequest(httptest.NewRequest(http.MethodPost, "https://api.factory.ai/v1/responses", strings.NewReader("{}")), "http://evil.example"); err == nil {
		t.Fatal("accepted a plain-http endpoint")
	}
}

func TestDroidRequestOpensEverySystemPromptAsDroid(t *testing.T) {
	for _, tt := range []struct{ name, path, body, want string }{
		{"responses without instructions", "/v1/responses", `{"model":"gpt-5.4","input":"hi"}`,
			`{"model":"gpt-5.4","input":"hi","instructions":"` + droidLine + `"}`},
		{"responses with instructions", "/v1/responses", `{"model":"gpt-5.4","instructions":"Be terse.","input":"hi"}`,
			`{"model":"gpt-5.4","instructions":"` + droidLine + `\nBe terse.","input":"hi"}`},
		{"responses already droid", "/v1/responses", `{"model":"gpt-5.4","instructions":"` + droidLine + `\nMore","input":"hi"}`,
			`{"model":"gpt-5.4","instructions":"` + droidLine + `\nMore","input":"hi"}`},
		{"chat without system", "/v1/chat/completions", `{"model":"glm-5.3","messages":[{"role":"user","content":"hi"}]}`,
			`{"model":"glm-5.3","messages":[{"content":"` + droidLine + `","role":"system"},{"role":"user","content":"hi"}]}`},
		{"chat with system string", "/v1/chat/completions", `{"model":"glm-5.3","messages":[{"role":"system","content":"Be terse."},{"role":"user","content":"hi"}]}`,
			`{"model":"glm-5.3","messages":[{"role":"system","content":"` + droidLine + `\nBe terse."},{"role":"user","content":"hi"}]}`},
		{"chat with system parts", "/v1/chat/completions", `{"model":"glm-5.3","messages":[{"role":"system","content":[{"type":"text","text":"Be terse."}]}]}`,
			`{"model":"glm-5.3","messages":[{"role":"system","content":[{"text":"` + droidLine + `","type":"text"},{"type":"text","text":"Be terse."}]}]}`},
		{"messages without system", "/v1/messages", `{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}]}`,
			`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}],"system":[{"text":"` + droidLine + `","type":"text"}]}`},
		{"messages with system string", "/v1/messages", `{"model":"claude-opus-4-6","system":"Be terse.","messages":[]}`,
			`{"model":"claude-opus-4-6","system":"` + droidLine + `\nBe terse.","messages":[]}`},
		{"messages with system blocks", "/v1/messages", `{"model":"claude-opus-4-6","system":[{"type":"text","text":"Be terse.","cache_control":{"type":"ephemeral"}}],"messages":[]}`,
			`{"model":"claude-opus-4-6","system":[{"text":"` + droidLine + `","type":"text"},{"type":"text","text":"Be terse.","cache_control":{"type":"ephemeral"}}],"messages":[]}`},
		{"messages already droid", "/v1/messages", `{"model":"claude-opus-4-6","system":[{"type":"text","text":"` + droidLine + `"},{"type":"text","text":"Mine"}],"messages":[]}`,
			`{"model":"claude-opus-4-6","system":[{"type":"text","text":"` + droidLine + `"},{"type":"text","text":"Mine"}],"messages":[]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := droidRequest(t, tt.path, tt.body, nil, "")
			body, _ := io.ReadAll(request.Body)
			if string(body) != tt.want {
				t.Fatalf("body = %s\nwant %s", body, tt.want)
			}
		})
	}
}

func TestDroidSessionIDFollowsTheConversationPerAccount(t *testing.T) {
	id := func(token, body string) string {
		request := droidRequest(t, "/v1/messages", body, http.Header{"Authorization": {token}}, "")
		return request.Header.Get("X-Session-Id")
	}
	first := `{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hello"}]}`
	followUp := `{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"more"}]}`
	if id("Bearer a", first) != id("Bearer a", followUp) {
		t.Fatal("a follow-up request changed the session id")
	}
	if id("Bearer a", first) == id("Bearer b", first) {
		t.Fatal("two accounts share a session id")
	}
	if id("Bearer a", first) == id("Bearer a", `{"model":"claude-opus-4-6","messages":[{"role":"user","content":"other"}]}`) {
		t.Fatal("two conversations share a session id")
	}
	if got := providerapi.DroidAPIProvider("Nemotron-3-Ultra"); got != "baseten" {
		t.Fatalf("DroidAPIProvider(nemotron) = %q", got)
	}
}
