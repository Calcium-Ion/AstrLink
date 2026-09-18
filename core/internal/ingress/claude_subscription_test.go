package ingress

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClaudeSubscriptionPreservesSystemAndTools(t *testing.T) {
	for _, system := range []string{`"Keep my instructions"`, `[{"type":"text","text":"Keep my instructions","cache_control":{"type":"ephemeral"}}]`} {
		raw := `{"model":"claude-sonnet-4-5","max_tokens":32,"system":` + system + `,"tools":[{"name":"Read","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"hello"}]}`
		request := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(raw))
		if err := prepareClaudeSubscriptionRequest(request); err != nil {
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
		if err := prepareClaudeSubscriptionRequest(request); err != nil {
			t.Fatal(err)
		}
		second, _ := io.ReadAll(request.Body)
		if string(body) != string(second) {
			t.Fatal("native Claude request rewritten twice")
		}
	}
}
