package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// streamThrough runs events through a plugin's stream hooks as the host
// does, the state of each call passed to the next, and returns the events
// sent.
func streamThrough(t *testing.T, plugin *Plugin, protocol string, events []map[string]any) []map[string]any {
	t.Helper()
	ctx := map[string]any{"protocol": protocol}
	var state any
	var sent []map[string]any
	for _, event := range events {
		result, _, err := plugin.CallArgs(context.Background(), HookOnStreamEvent, []any{ctx, event, state}, CallOptions{})
		if err != nil {
			t.Fatalf("onStreamEvent: %v", err)
		}
		var decided struct {
			Events []map[string]any `json:"events"`
			State  json.RawMessage  `json:"state"`
		}
		if err := json.Unmarshal(result, &decided); err != nil {
			t.Fatal(err)
		}
		state = decided.State
		sent = append(sent, decided.Events...)
	}
	result, _, err := plugin.CallArgs(context.Background(), HookOnStreamEnd, []any{ctx, state}, CallOptions{})
	if err != nil {
		t.Fatalf("onStreamEnd: %v", err)
	}
	var ended struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(result, &ended); err != nil {
		t.Fatal(err)
	}
	return append(sent, ended.Events...)
}

func textPlugin(t *testing.T) *Plugin {
	t.Helper()
	manager := newTestManager(t, map[string]string{
		"mask": pluginSourceV2("mask", nil, `
export const { onStreamEvent, onStreamEnd } = astrlink.textStream({
  replace: [["secret", "[hidden]"], [/\d{4}/, "####"]],
  maxMatch: 4,
  map: (text) => text.replaceAll("ok", "OK"),
});
`),
	}, settingsMap{"mask": enabled()}, Services{}, Limits{})
	return loadedPlugin(t, manager, "mask")
}

func TestTextStreamReplacesAcrossChatPieces(t *testing.T) {
	plugin := textPlugin(t)
	chunk := func(text string, finish any) map[string]any {
		return map[string]any{"data": map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}, "finish_reason": finish}}}}
	}
	sent := streamThrough(t, plugin, "openai.chat", []map[string]any{
		chunk("my sec", nil), chunk("ret is 12", nil), chunk("34 ok", nil), chunk("", "stop"),
		{"data": "[DONE]"},
	})
	var text strings.Builder
	for _, event := range sent {
		data, ok := event["data"].(map[string]any)
		if !ok {
			continue
		}
		choice := data["choices"].([]any)[0].(map[string]any)
		text.WriteString(choice["delta"].(map[string]any)["content"].(string))
	}
	if text.String() != "my [hidden] is #### OK" {
		t.Fatalf("text = %q", text.String())
	}
	last := sent[len(sent)-1]
	if last["data"] != "[DONE]" {
		t.Fatalf("the end marker = %v", last)
	}
	finish := sent[len(sent)-2]["data"].(map[string]any)["choices"].([]any)[0].(map[string]any)
	if finish["finish_reason"] != "stop" {
		t.Fatalf("the finish event = %v", finish)
	}
}

func TestTextStreamKeepsAnthropicBlocksWhole(t *testing.T) {
	plugin := textPlugin(t)
	delta := func(text string) map[string]any {
		return map[string]any{"event": "content_block_delta", "data": map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": text}}}
	}
	sent := streamThrough(t, plugin, "anthropic.messages", []map[string]any{
		{"event": "content_block_start", "data": map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}},
		delta("the secr"), delta("et code 9"), delta("876"),
		{"event": "content_block_stop", "data": map[string]any{"type": "content_block_stop", "index": 0}},
		{"event": "message_stop", "data": map[string]any{"type": "message_stop"}},
	})
	var text strings.Builder
	var kinds []string
	for _, event := range sent {
		kinds = append(kinds, event["event"].(string))
		data := event["data"].(map[string]any)
		if data["type"] == "content_block_delta" {
			text.WriteString(data["delta"].(map[string]any)["text"].(string))
		}
	}
	if text.String() != "the [hidden] code ####" {
		t.Fatalf("text = %q", text.String())
	}
	if kinds[0] != "content_block_start" || kinds[len(kinds)-2] != "content_block_stop" || kinds[len(kinds)-1] != "message_stop" {
		t.Fatalf("events = %v", kinds)
	}
}

func TestTextStreamRepeatsTheSentTextInResponsesDoneEvents(t *testing.T) {
	plugin := textPlugin(t)
	delta := func(text string) map[string]any {
		return map[string]any{"event": "response.output_text.delta", "data": map[string]any{"type": "response.output_text.delta", "item_id": "msg_1", "output_index": 0, "content_index": 0, "delta": text}}
	}
	sent := streamThrough(t, plugin, "openai.responses", []map[string]any{
		delta("a secret "), delta("1234"),
		{"event": "response.output_text.done", "data": map[string]any{"type": "response.output_text.done", "item_id": "msg_1", "output_index": 0, "content_index": 0, "text": "a secret 1234"}},
		{"event": "response.content_part.done", "data": map[string]any{"type": "response.content_part.done", "item_id": "msg_1", "content_index": 0, "part": map[string]any{"type": "output_text", "text": "a secret 1234"}}},
		{"event": "response.completed", "data": map[string]any{"type": "response.completed", "response": map[string]any{"output": []any{map[string]any{"id": "msg_1", "type": "message", "content": []any{map[string]any{"type": "output_text", "text": "a secret 1234"}}}}}}},
	})
	var deltas strings.Builder
	var done, part, completed string
	for _, event := range sent {
		data := event["data"].(map[string]any)
		switch data["type"] {
		case "response.output_text.delta":
			deltas.WriteString(data["delta"].(string))
		case "response.output_text.done":
			done = data["text"].(string)
		case "response.content_part.done":
			part = data["part"].(map[string]any)["text"].(string)
		case "response.completed":
			completed = data["response"].(map[string]any)["output"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
		}
	}
	for name, got := range map[string]string{"deltas": deltas.String(), "done": done, "part": part, "completed": completed} {
		if got != "a [hidden] ####" {
			t.Errorf("%s = %q", name, got)
		}
	}
}

func TestTextStreamFlushesGeminiOnItsLastChunk(t *testing.T) {
	plugin := textPlugin(t)
	chunk := func(text string, finish any) map[string]any {
		candidate := map[string]any{"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}}}
		if finish != nil {
			candidate["finishReason"] = finish
		}
		return map[string]any{"data": map[string]any{"candidates": []any{candidate}}}
	}
	sent := streamThrough(t, plugin, "google.generate_content", []map[string]any{chunk("keep it se", nil), chunk("cret ok", "STOP")})
	var text strings.Builder
	for _, event := range sent {
		parts := event["data"].(map[string]any)["candidates"].([]any)[0].(map[string]any)["content"].(map[string]any)["parts"].([]any)
		for _, part := range parts {
			text.WriteString(part.(map[string]any)["text"].(string))
		}
	}
	if text.String() != "keep it [hidden] OK" {
		t.Fatalf("text = %q", text.String())
	}
}
