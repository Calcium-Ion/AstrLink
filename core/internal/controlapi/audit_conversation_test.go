package controlapi

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func decodeConversationItems(t *testing.T, body, key string) []any {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	if len(root) != 1 {
		t.Fatalf("root keeps %v, want only %q", root, key)
	}
	items, _ := root[key].([]any)
	return items
}

func mustDecodeJSON(t *testing.T, text string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestConversationRequestBodyKeepsWordsAndTrailingResults(t *testing.T) {
	body := `{
		"model": "m",
		"system": "long rules",
		"tools": [{"name": "Read"}],
		"messages": [
			{"role": "user", "content": "first question"},
			{"role": "assistant", "content": [{"type": "text", "text": "long answer"}, {"type": "tool_use", "id": "t0", "name": "Read", "input": {}}]},
			{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "t0", "content": "huge file"}, {"type": "text", "text": "<system-reminder>x</system-reminder>"}]},
			{"role": "assistant", "content": [{"type": "text", "text": "done"}]},
			{"role": "user", "content": [{"type": "text", "text": "second question"}]},
			{"role": "assistant", "content": [{"type": "tool_use", "id": "t1", "name": "Bash", "input": {}}]},
			{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "t1", "content": "exit 0"}]}
		]
	}`
	got := decodeConversationItems(t, conversationRequestBody("anthropic.messages", body), "messages")
	want := mustDecodeJSON(t, `[
		{"role": "user", "content": "first question"},
		{"role": "assistant"},
		{"role": "user", "content": [{"type": "text", "text": "<system-reminder>x</system-reminder>"}, {"type": "tool_result"}]},
		{"role": "assistant"},
		{"role": "user", "content": [{"type": "text", "text": "second question"}]},
		{"role": "assistant"},
		{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "t1", "content": "exit 0"}]}
	]`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("messages = %v\nwant %v", got, want)
	}
}

func TestConversationRequestBodyResponses(t *testing.T) {
	body := `{
		"instructions": "rules",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "ask"}]},
			{"type": "reasoning", "summary": []},
			{"type": "function_call", "call_id": "c1", "name": "exec", "arguments": "{}"},
			{"type": "function_call_output", "call_id": "c1", "output": "big"},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "narration"}]},
			{"type": "function_call", "call_id": "c2", "name": "exec", "arguments": "{}"},
			{"type": "custom_tool_call_output", "call_id": "c2", "output": "last"}
		]
	}`
	got := decodeConversationItems(t, conversationRequestBody("openai.responses", body), "input")
	want := mustDecodeJSON(t, `[
		{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "ask"}]},
		{"type": "function_call_output"},
		{"type": "message", "role": "assistant"},
		{"type": "custom_tool_call_output", "call_id": "c2", "output": "last"}
	]`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("input = %v\nwant %v", got, want)
	}
}

func TestConversationRequestBodyGeminiAndChat(t *testing.T) {
	gemini := `{"contents": [
		{"role": "user", "parts": [{"text": "hi"}]},
		{"role": "model", "parts": [{"functionCall": {"name": "f"}}]},
		{"role": "user", "parts": [{"functionResponse": {"name": "f", "response": {"x": 1}}}]},
		{"role": "model", "parts": [{"functionCall": {"name": "g"}}]},
		{"role": "user", "parts": [{"functionResponse": {"name": "g", "response": {"y": 2}}}]}
	]}`
	got := decodeConversationItems(t, conversationRequestBody("google.generate_content", gemini), "contents")
	want := mustDecodeJSON(t, `[
		{"role": "user", "parts": [{"text": "hi"}]},
		{"role": "model"},
		{"role": "user", "parts": [{"functionResponse": {}}]},
		{"role": "model"},
		{"role": "user", "parts": [{"functionResponse": {"name": "g", "response": {"y": 2}}}]}
	]`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("contents = %v\nwant %v", got, want)
	}

	chat := `{"messages": [
		{"role": "system", "content": "rules"},
		{"role": "user", "content": "q"},
		{"role": "assistant", "tool_calls": [{"id": "a"}]},
		{"role": "tool", "tool_call_id": "a", "content": "old"},
		{"role": "assistant", "tool_calls": [{"id": "b"}]},
		{"role": "tool", "tool_call_id": "b", "content": "new"}
	]}`
	got = decodeConversationItems(t, conversationRequestBody("openai.chat", chat), "messages")
	want = mustDecodeJSON(t, `[
		{"role": "user", "content": "q"},
		{"role": "assistant"},
		{"role": "tool"},
		{"role": "assistant"},
		{"role": "tool", "tool_call_id": "b", "content": "new"}
	]`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chat = %v\nwant %v", got, want)
	}
}

func TestConversationRequestBodyUnreadable(t *testing.T) {
	for _, test := range []struct{ protocol, body, want string }{
		{"anthropic.messages", `{"messages": [`, ""},
		{"unknown.protocol", `{"messages": []}`, ""},
		{"openai.responses", `{"input": "just text", "model": "m"}`, `{"input":"just text"}`},
	} {
		if got := conversationRequestBody(test.protocol, test.body); got != test.want {
			t.Errorf("%s %q = %q, want %q", test.protocol, test.body, got, test.want)
		}
	}
}

func TestConversationContentDropsPartsTheViewNeverReads(t *testing.T) {
	part := func(text string) *contract.AuditContentPart {
		return &contract.AuditContentPart{Content: text}
	}
	content := conversationContent("openai.chat", contract.AuditContent{
		RequestBody:             part(`{"messages":[{"role":"user","content":"q"}],"model":"m"}`),
		ResponseContent:         part("reply"),
		UpstreamRequestBody:     part("upstream request"),
		UpstreamResponseContent: part("upstream reply"),
	})
	if content.UpstreamRequestBody != nil || content.UpstreamResponseContent != nil {
		t.Fatalf("upstream parts kept: %+v", content)
	}
	if content.RequestBody.Content != `{"messages":[{"role":"user","content":"q"}]}` {
		t.Fatalf("request body = %q", content.RequestBody.Content)
	}
	fallback := conversationContent("openai.chat", contract.AuditContent{
		UpstreamResponseContent: part("upstream reply"),
	})
	if fallback.UpstreamResponseContent == nil {
		t.Fatal("the provider response must stand in when the client one is missing")
	}
}
