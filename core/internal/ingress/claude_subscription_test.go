package ingress

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClaudeSubscriptionPreservesSystemAndTools(t *testing.T) {
	for _, system := range []string{`"Keep my instructions"`, `[{"type":"text","text":"Keep my instructions","cache_control":{"type":"ephemeral"}}]`} {
		raw := `{"model":"claude-sonnet-4-5","max_tokens":32,"system":` + system + `,"tools":[{"name":"Read","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"hello"}]}`
		request := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(raw))
		if err := prepareClaudeSubscriptionRequest(request, claudeRequestOptions{normalize: true}); err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(request.Body)
		var got struct {
			System []struct {
				Text string `json:"text"`
			} `json:"system"`
			Tools json.RawMessage `json:"tools"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.System) != 2 || got.System[0].Text != claudeCodeBanner || got.System[1].Text != "Keep my instructions" || !strings.Contains(string(got.Tools), `"name":"Read"`) {
			t.Fatalf("request content changed: %s", body)
		}
		if request.ContentLength != int64(len(body)) {
			t.Fatal("stale content length")
		}
		request.Body = io.NopCloser(strings.NewReader(string(body)))
		if err := prepareClaudeSubscriptionRequest(request, claudeRequestOptions{normalize: true}); err != nil {
			t.Fatal(err)
		}
		second, _ := io.ReadAll(request.Body)
		if string(body) != string(second) {
			t.Fatal("native Claude request rewritten twice")
		}
	}
}

func prepareClaudeBody(t *testing.T, body string, betas ...string) (map[string]any, []byte) {
	t.Helper()
	request := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	for _, beta := range betas {
		request.Header.Add("Anthropic-Beta", beta)
	}
	if err := prepareClaudeSubscriptionRequest(request, claudeRequestOptions{normalize: true}); err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(request.Body)
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	// Every repair is idempotent: a second pass leaves the bytes unchanged.
	again := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(string(raw)))
	again.Header = request.Header.Clone()
	if err := prepareClaudeSubscriptionRequest(again, claudeRequestOptions{normalize: true}); err != nil {
		t.Fatal(err)
	}
	if second, _ := io.ReadAll(again.Body); string(second) != string(raw) {
		t.Fatalf("second pass changed the body:\n%s\n%s", raw, second)
	}
	return fields, raw
}

func claudeCacheControls(fields map[string]any) []string {
	var controls []string
	visit := func(blocks any) {
		items, _ := blocks.([]any)
		for _, item := range items {
			block, _ := item.(map[string]any)
			if control, ok := block["cache_control"].(map[string]any); ok {
				ttl, _ := control["ttl"].(string)
				text, _ := block["text"].(string)
				if name, ok := block["name"].(string); ok {
					text = name
				}
				controls = append(controls, text+"@"+ttl)
			}
			if nested, ok := block["content"].([]any); ok {
				for _, child := range nested {
					if child, ok := child.(map[string]any); ok && child["cache_control"] != nil {
						text, _ := child["text"].(string)
						controls = append(controls, text+"@")
					}
				}
			}
		}
	}
	visit(fields["tools"])
	visit(fields["system"])
	messages, _ := fields["messages"].([]any)
	for _, message := range messages {
		visit(message.(map[string]any)["content"])
	}
	return controls
}

func TestClaudeSubscriptionLimitsCacheBreakpoints(t *testing.T) {
	const ephemeral = `"cache_control":{"type":"ephemeral"}`
	fields, _ := prepareClaudeBody(t, `{"model":"claude-sonnet-4-5","max_tokens":64,
		"tools":[{"name":"Read","input_schema":{"type":"object"},`+ephemeral+`}],
		"system":[{"type":"text","text":"sys",`+ephemeral+`}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"m1",`+ephemeral+`}]},
			{"role":"assistant","content":[{"type":"text","text":"m2",`+ephemeral+`}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":[{"type":"text","text":"m3",`+ephemeral+`}]}]},
			{"role":"user","content":[{"type":"text","text":"m4",`+ephemeral+`}]}]}`)
	if got := strings.Join(claudeCacheControls(fields), ","); got != "Read@,sys@,m3@,m4@" {
		t.Fatalf("breakpoints = %s, want the earliest message breakpoints dropped", got)
	}
}

func TestClaudeSubscriptionOrdersCacheTTL(t *testing.T) {
	fields, _ := prepareClaudeBody(t, `{"model":"claude-sonnet-4-5","max_tokens":64,
		"system":[{"type":"text","text":"long","cache_control":{"type":"ephemeral","ttl":"1h"}},
			{"type":"text","text":"short","cache_control":{"type":"ephemeral"}}],
		"messages":[{"role":"user","content":[{"type":"text","text":"late","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)
	if got := strings.Join(claudeCacheControls(fields), ","); got != "long@1h,short@,late@" {
		t.Fatalf("ttl order = %s, want 1h kept only before the first 5m breakpoint", got)
	}
}

func TestClaudeSubscriptionResolvesThinkingConflicts(t *testing.T) {
	for _, test := range []struct {
		name, body string
		betas      []string
		want       map[string]any
		absent     []string
	}{
		{
			name:   "sampling parameters",
			body:   `{"max_tokens":4096,"thinking":{"type":"enabled","budget_tokens":2048},"temperature":0.5,"top_k":5,"top_p":0.9}`,
			want:   map[string]any{"thinking": map[string]any{"type": "enabled", "budget_tokens": 2048.0}},
			absent: []string{"temperature", "top_k", "top_p"},
		},
		{
			name: "compatible sampling parameters",
			body: `{"max_tokens":4096,"thinking":{"type":"enabled","budget_tokens":2048},"temperature":1,"top_p":0.99}`,
			want: map[string]any{"temperature": 1.0, "top_p": 0.99},
		},
		{
			name: "budget clamped below max_tokens",
			body: `{"max_tokens":2048,"thinking":{"type":"enabled","budget_tokens":4096}}`,
			want: map[string]any{"thinking": map[string]any{"type": "enabled", "budget_tokens": 2047.0}},
		},
		{
			name:  "interleaved budget may exceed max_tokens",
			body:  `{"max_tokens":2048,"thinking":{"type":"enabled","budget_tokens":4096}}`,
			betas: []string{"oauth-2025-04-20, interleaved-thinking-2025-05-14"},
			want:  map[string]any{"thinking": map[string]any{"type": "enabled", "budget_tokens": 4096.0}},
		},
		{
			name:   "budget too small after clamp",
			body:   `{"max_tokens":1000,"thinking":{"type":"enabled","budget_tokens":2000},"temperature":0.5}`,
			want:   map[string]any{"temperature": 0.5, "max_tokens": 1000.0},
			absent: []string{"thinking"},
		},
		{
			name:   "forced tool use",
			body:   `{"max_tokens":4096,"thinking":{"type":"enabled","budget_tokens":2048},"tool_choice":{"type":"tool","name":"Read"},"temperature":0.2}`,
			want:   map[string]any{"temperature": 0.2},
			absent: []string{"thinking"},
		},
		{
			name: "missing max_tokens covers the budget",
			body: `{"thinking":{"type":"enabled","budget_tokens":10000}}`,
			want: map[string]any{"max_tokens": 11024.0},
		},
		{
			name: "missing max_tokens",
			body: `{"max_tokens":null}`,
			want: map[string]any{"max_tokens": 8192.0},
		},
		{
			name: "adaptive thinking is left alone",
			body: `{"max_tokens":64,"thinking":{"type":"adaptive"},"temperature":0.5,"top_k":3}`,
			want: map[string]any{"thinking": map[string]any{"type": "adaptive"}, "temperature": 0.5, "top_k": 3.0},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := strings.Replace(test.body, "{", `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}],`, 1)
			fields, raw := prepareClaudeBody(t, body, test.betas...)
			for name, want := range test.want {
				if got, _ := json.Marshal(fields[name]); string(got) != mustJSON(t, want) {
					t.Errorf("%s = %s, want %s", name, got, mustJSON(t, want))
				}
			}
			for _, name := range test.absent {
				if _, ok := fields[name]; ok {
					t.Errorf("%s was forwarded: %s", name, raw)
				}
			}
		})
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestClaudeSubscriptionDropsEmptyBlocksWithoutSynthesizing(t *testing.T) {
	fields, _ := prepareClaudeBody(t, `{"model":"claude-sonnet-4-5","max_tokens":64,
		"system":[{"type":"text","text":" "},{"type":"text","text":"keep system"}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":""},{"type":"text","text":"hello"}]},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"unsigned","signature":""},
				{"type":"redacted_thinking","data":""},
				{"type":"thinking","thinking":"signed","signature":"sig-1"},
				{"type":"tool_use","id":"t1","name":"Read","input":{}}]},
			{"role":"user","content":[{"type":"text","text":"\n"}]},
			{"role":"assistant","content":[{"type":"thinking","thinking":"only","signature":" "}]}]}`)
	got, _ := json.Marshal(fields["messages"])
	want := `[{"content":[{"text":"hello","type":"text"}],"role":"user"},` +
		`{"content":[{"signature":"sig-1","thinking":"signed","type":"thinking"},{"id":"t1","input":{},"name":"Read","type":"tool_use"}],"role":"assistant"},` +
		`{"content":[{"text":"\n","type":"text"}],"role":"user"},` +
		`{"content":[{"signature":" ","thinking":"only","type":"thinking"}],"role":"assistant"}]`
	if string(got) != want {
		t.Fatalf("messages =\n%s\nwant\n%s", got, want)
	}
	system, _ := json.Marshal(fields["system"])
	if string(system) != `[{"text":"`+claudeCodeBanner+`","type":"text"},{"text":"keep system","type":"text"}]` {
		t.Fatalf("system = %s", system)
	}
}

func TestClaudeSubscriptionKeepsCompliantBytes(t *testing.T) {
	compliant := "{\"model\": \"claude-sonnet-4-5\", \"max_tokens\": 32000,\n" +
		"  \"thinking\": {\"type\": \"enabled\", \"budget_tokens\": 31999}, \"temperature\": 1,\n" +
		"  \"system\": [{\"type\": \"text\", \"text\": \"" + claudeCodeBanner + "\", \"cache_control\": {\"type\": \"ephemeral\"}}],\n" +
		"  \"messages\": [{\"role\": \"user\", \"content\": [{\"type\": \"text\", \"text\": \"hi\", \"cache_control\": {\"type\": \"ephemeral\"}}]}]}"
	request := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(compliant))
	if err := prepareClaudeSubscriptionRequest(request, claudeRequestOptions{normalize: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(request.Body); string(got) != compliant {
		t.Fatalf("compliant Claude Code body rewritten: %s", got)
	}
}

func TestFilterClaudeRelayKitHeaders(t *testing.T) {
	header := http.Header{}
	header.Set("X-Stainless-Lang", "python")
	header.Set("x-stainless-package-version", "1.2.3")
	header.Add("Anthropic-Beta", "interleaved-thinking-2025-05-14, files-api-2025-04-14")
	header.Add("Anthropic-Beta", "context-1m-2025-08-07")
	header.Set("X-Client-Feature", "kept")
	header.Set("OpenAI-Organization", "org-client")
	filterClaudeRelayKitHeaders(header)
	if header.Get("X-Stainless-Lang") != "" || len(header["x-stainless-package-version"]) != 0 ||
		header.Get("OpenAI-Organization") != "" ||
		header.Get("Anthropic-Beta") != "interleaved-thinking-2025-05-14,context-1m-2025-08-07" ||
		header.Get("X-Client-Feature") != "kept" {
		t.Fatalf("filtered headers = %v", header)
	}
	header = http.Header{"Anthropic-Beta": {"files-api-2025-04-14"}}
	filterClaudeRelayKitHeaders(header)
	if _, ok := header["Anthropic-Beta"]; ok {
		t.Fatalf("unsupported betas kept: %v", header)
	}
}

func TestClaudeSubscriptionDropsBlankStringSystem(t *testing.T) {
	fields, _ := prepareClaudeBody(t, `{"model":"claude-sonnet-4-5","max_tokens":64,"system":" \n","messages":[{"role":"user","content":"hi"}]}`)
	if system, _ := json.Marshal(fields["system"]); string(system) != `[{"text":"`+claudeCodeBanner+`","type":"text"}]` {
		t.Fatalf("system = %s, want only the banner", system)
	}
}
