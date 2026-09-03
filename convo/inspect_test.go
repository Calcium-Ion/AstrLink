package convo

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const goroutineAnswerZH = "Goroutine 是 Go 运行时调度的轻量级线程，用 go 关键字即可并发执行函数，成本远低于操作系统线程。"
const goroutineAnswerEN = "A goroutine is a lightweight thread managed by the Go runtime, started with the go keyword and far cheaper than an OS thread."

func readFixture(t *testing.T, parts ...string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{"testdata"}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func inspectFixture(t *testing.T, protocol Protocol, parts ...string) RequestSummary {
	t.Helper()
	summary, err := InspectBody(protocol, readFixture(t, parts...))
	if err != nil {
		t.Fatalf("InspectBody: %v", err)
	}
	return summary
}

func TestInspectOpenAIChatToolLoop(t *testing.T) {
	summary := inspectFixture(t, OpenAIChat, "openai_chat", "tool_loop_step2.json")
	if summary.UserTurnCount != 1 || !summary.HasUserMessage {
		t.Fatalf("UserTurnCount = %d, want 1", summary.UserTurnCount)
	}
	if want := []string{"call_7f3a9c2e1b4d4e8fa1c2"}; !reflect.DeepEqual(summary.EchoIDs, want) {
		t.Fatalf("EchoIDs = %v, want %v", summary.EchoIDs, want)
	}
	if len(summary.ExplicitCursors) != 0 || summary.Stateful {
		t.Fatalf("unexpected explicit cursors %v / stateful %v", summary.ExplicitCursors, summary.Stateful)
	}
	if summary.AssistantDigest != nil {
		t.Fatal("empty assistant content must not fingerprint")
	}
	if summary.LastUserText != "帮我看看 repo 里有哪些 TODO" || summary.FirstUserText != summary.LastUserText {
		t.Fatalf("user text = %q / %q", summary.FirstUserText, summary.LastUserText)
	}
}

func TestInspectOpenAIChatTextTurns(t *testing.T) {
	summary := inspectFixture(t, OpenAIChat, "openai_chat", "text_turn2.json")
	if summary.UserTurnCount != 2 {
		t.Fatalf("UserTurnCount = %d, want 2", summary.UserTurnCount)
	}
	if !bytes.Equal(summary.AssistantDigest, DigestText(goroutineAnswerZH, true)) {
		t.Fatal("assistant digest does not match the replayed answer")
	}
	if summary.FirstUserText != "用一句话介绍一下 Go 语言的 goroutine。" || summary.LastUserText != "那 channel 呢？" {
		t.Fatalf("user text = %q / %q", summary.FirstUserText, summary.LastUserText)
	}
	if summary.EchoIDs != nil {
		t.Fatalf("EchoIDs = %v, want none", summary.EchoIDs)
	}
}

// A coding-agent harness replays a compaction summary and skill injections as
// role=user messages. Only the message a person typed is a turn, and the
// preview must be that text, not the wrapper.
func TestInspectOpenAIChatHarnessMessagesAreNotTurns(t *testing.T) {
	summary := inspectFixture(t, OpenAIChat, "openai_chat", "harness_compaction_skill.json")
	if summary.UserTurnCount != 1 || !summary.HasUserMessage {
		t.Fatalf("UserTurnCount = %d, want 1 (compaction summary and <skill> blocks are harness)", summary.UserTurnCount)
	}
	if summary.LastUserText != "审核完先别改，给我结论" || summary.FirstUserText != summary.LastUserText {
		t.Fatalf("user text = %q / %q", summary.FirstUserText, summary.LastUserText)
	}
	if want := []string{"call_3a3d1f9e8c7b6a5d4e3f2a1b"}; !reflect.DeepEqual(summary.EchoIDs, want) {
		t.Fatalf("EchoIDs = %v, want %v", summary.EchoIDs, want)
	}
	if summary.AssistantDigest == nil {
		t.Fatal("the last assistant reply is long enough to fingerprint")
	}
}

func TestInspectOpenAIResponsesReplay(t *testing.T) {
	summary := inspectFixture(t, OpenAIResponses, "openai_responses", "replay_items.json")
	if summary.UserTurnCount != 1 {
		t.Fatalf("UserTurnCount = %d, want 1 (function_call_output is not a turn)", summary.UserTurnCount)
	}
	if want := []string{"codex-sess-3f9a1c2b-7d4e-4f6a-9b8c-0d1e2f3a4b5c"}; !reflect.DeepEqual(summary.ExplicitCursors, want) {
		t.Fatalf("ExplicitCursors = %v, want %v", summary.ExplicitCursors, want)
	}
	if summary.Stateful {
		t.Fatal("full replay must not be stateful")
	}
	want := []string{"call_Ab12Cd34Ef56Gh78Ij90", "fc_68b7d1e2f3a4b5c6d7e8f9a1", "rs_68b7d1e2f3a4b5c6d7e8f9a0"}
	if !reflect.DeepEqual(summary.EchoIDs, want) {
		t.Fatalf("EchoIDs = %v, want %v", summary.EchoIDs, want)
	}
	if summary.LastUserText != "List the Go files in src and tell me which one has a TODO" {
		t.Fatalf("LastUserText = %q", summary.LastUserText)
	}
}

func TestInspectOpenAIResponsesTextTurns(t *testing.T) {
	summary := inspectFixture(t, OpenAIResponses, "openai_responses", "text_turn2.json")
	if summary.UserTurnCount != 2 {
		t.Fatalf("UserTurnCount = %d, want 2", summary.UserTurnCount)
	}
	if want := []string{"msg_68b7d2a0b1c2d3e4f5a6b7c8"}; !reflect.DeepEqual(summary.EchoIDs, want) {
		t.Fatalf("EchoIDs = %v, want %v", summary.EchoIDs, want)
	}
	if !bytes.Equal(summary.AssistantDigest, DigestText(goroutineAnswerEN, true)) {
		t.Fatal("assistant digest does not match the replayed message item")
	}
}

func TestInspectOpenAIResponsesStateful(t *testing.T) {
	summary := inspectFixture(t, OpenAIResponses, "openai_responses", "stateful.json")
	if !summary.Stateful {
		t.Fatal("previous_response_id must mark the request stateful")
	}
	if want := []string{"resp_68b7d3f0a1b2c3d4e5f6a7b8"}; !reflect.DeepEqual(summary.ExplicitCursors, want) {
		t.Fatalf("ExplicitCursors = %v, want %v", summary.ExplicitCursors, want)
	}
	if summary.UserTurnCount != 1 {
		t.Fatalf("UserTurnCount = %d, want 1", summary.UserTurnCount)
	}
}

func TestInspectOpenAIResponsesStringInput(t *testing.T) {
	summary, err := InspectBody(OpenAIResponses, []byte(`{"model":"gpt-5","input":"  hello there  "}`))
	if err != nil {
		t.Fatal(err)
	}
	if summary.UserTurnCount != 1 || summary.LastUserText != "hello there" {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestInspectAnthropicToolLoop(t *testing.T) {
	summary := inspectFixture(t, AnthropicMessages, "anthropic", "tool_loop.json")
	if summary.UserTurnCount != 1 {
		t.Fatalf("UserTurnCount = %d, want 1 (tool_result + system-reminder is not a turn)", summary.UserTurnCount)
	}
	if want := []string{"66666666-7777-8888-9999-000000000000"}; !reflect.DeepEqual(summary.ExplicitCursors, want) {
		t.Fatalf("ExplicitCursors = %v, want %v", summary.ExplicitCursors, want)
	}
	if want := []string{"toolu_01XyZabc123DEF456ghi789JK"}; !reflect.DeepEqual(summary.EchoIDs, want) {
		t.Fatalf("EchoIDs = %v, want %v", summary.EchoIDs, want)
	}
	if summary.AssistantDigest != nil {
		t.Fatal("short assistant text must not fingerprint")
	}
	if summary.LastUserText != "修复 main.go 的编译错误" {
		t.Fatalf("LastUserText = %q", summary.LastUserText)
	}
}

func TestInspectAnthropicTextTurns(t *testing.T) {
	summary := inspectFixture(t, AnthropicMessages, "anthropic", "text_turn2.json")
	if summary.UserTurnCount != 2 {
		t.Fatalf("UserTurnCount = %d, want 2", summary.UserTurnCount)
	}
	if !bytes.Equal(summary.AssistantDigest, DigestText(goroutineAnswerZH, true)) {
		t.Fatal("assistant digest does not match")
	}
}

func TestInspectGeminiTextTurns(t *testing.T) {
	summary := inspectFixture(t, GeminiGenerateContent, "gemini", "two_turns.json")
	if summary.UserTurnCount != 2 {
		t.Fatalf("UserTurnCount = %d, want 2", summary.UserTurnCount)
	}
	if !bytes.Equal(summary.AssistantDigest, DigestText(goroutineAnswerZH, true)) {
		t.Fatal("assistant digest does not match")
	}
	if summary.LastUserText != "那 channel 呢？" {
		t.Fatalf("LastUserText = %q", summary.LastUserText)
	}
}

func TestInspectGeminiFunctionLoop(t *testing.T) {
	summary := inspectFixture(t, GeminiGenerateContent, "gemini", "function_loop.json")
	if summary.UserTurnCount != 1 {
		t.Fatalf("UserTurnCount = %d, want 1 (functionResponse is not a turn)", summary.UserTurnCount)
	}
	if len(summary.EchoIDs) != 1 || len(summary.EchoIDs[0]) != len("ts_")+32 || summary.EchoIDs[0][:3] != "ts_" {
		t.Fatalf("EchoIDs = %v, want one hashed thought signature", summary.EchoIDs)
	}
}

func TestInspectExplicitCursorPriority(t *testing.T) {
	body := []byte(`{
		"previous_response_id": "resp_prev",
		"conversation": {"id": "conv_123"},
		"metadata": {"session_id": "sess_meta", "user_id": "user_x_session_abc"},
		"prompt_cache_key": "pck_1",
		"input": "hi"
	}`)
	summary, err := InspectBody(OpenAIResponses, body)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"resp_prev", "conv_123", "sess_meta", "abc", "pck_1"}
	if !reflect.DeepEqual(summary.ExplicitCursors, want) {
		t.Fatalf("ExplicitCursors = %v, want %v", summary.ExplicitCursors, want)
	}
}

func TestInspectAnthropicContainerAndGeminiCache(t *testing.T) {
	summary, err := InspectBody(AnthropicMessages, []byte(`{"container":{"id":"container_01Abc"},"messages":[]}`))
	if err != nil || !reflect.DeepEqual(summary.ExplicitCursors, []string{"container_01Abc"}) {
		t.Fatalf("anthropic container: %v %v", summary.ExplicitCursors, err)
	}
	summary, err = InspectBody(GeminiGenerateContent, []byte(`{"cachedContent":"cachedContents/abc123","contents":[]}`))
	if err != nil || !reflect.DeepEqual(summary.ExplicitCursors, []string{"cachedContents/abc123"}) {
		t.Fatalf("gemini cachedContent: %v %v", summary.ExplicitCursors, err)
	}
}

func TestInspectRejectsLowEntropyEchoIDs(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"do it"},
		{"role":"assistant","tool_calls":[{"id":"call_0","type":"function","function":{"name":"f","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_0","content":"ok"}
	]}`)
	summary, err := InspectBody(OpenAIChat, body)
	if err != nil {
		t.Fatal(err)
	}
	if summary.EchoIDs != nil {
		t.Fatalf("EchoIDs = %v, want none", summary.EchoIDs)
	}
}

func TestInspectEchoIDLimitTakesNewest(t *testing.T) {
	var messages []json.RawMessage
	messages = append(messages, json.RawMessage(`{"role":"user","content":"go"}`))
	for i := 0; i < 40; i++ {
		id := "call_" + string(rune('a'+i%26)) + "bcdefgh" + string(rune('0'+i%10)) + "xyz" + string(rune('A'+i%26))
		messages = append(messages, json.RawMessage(`{"role":"assistant","tool_calls":[{"id":"`+id+`","type":"function","function":{"name":"f","arguments":"{}"}}]}`))
		messages = append(messages, json.RawMessage(`{"role":"tool","tool_call_id":"`+id+`","content":"ok"}`))
	}
	body, _ := json.Marshal(map[string]any{"messages": messages})
	policy := DefaultPolicy()
	policy.MaxInboundEchoIDs = 5
	summary, err := policy.InspectBody(OpenAIChat, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.EchoIDs) != 5 {
		t.Fatalf("got %d echo ids, want 5", len(summary.EchoIDs))
	}
	if summary.EchoIDs[0] != "call_nbcdefgh9xyzN" {
		t.Fatalf("first echo id %q is not the newest", summary.EchoIDs[0])
	}
}

func TestInspectUnsupportedProtocolAndBadJSON(t *testing.T) {
	if _, err := InspectBody(Protocol("openai.completions"), []byte(`{}`)); err != ErrUnsupportedProtocol {
		t.Fatalf("err = %v, want ErrUnsupportedProtocol", err)
	}
	if _, err := InspectBody(OpenAIChat, []byte(`[1,2,3]`)); err != ErrInvalidJSON {
		t.Fatalf("err = %v, want ErrInvalidJSON", err)
	}
	summary, err := InspectFields(OpenAIChat, nil)
	if err != nil || summary.UserTurnCount != 0 {
		t.Fatalf("nil fields: %+v %v", summary, err)
	}
}

func TestInspectStripsLeadingThinkFromReplayedAssistant(t *testing.T) {
	withThink := []byte(`{"messages":[{"role":"user","content":"q"},{"role":"assistant","content":"<think>\nplan\n</think>\n` + goroutineAnswerZH + `"},{"role":"user","content":"next"}]}`)
	without := []byte(`{"messages":[{"role":"user","content":"q"},{"role":"assistant","content":"` + goroutineAnswerZH + `"},{"role":"user","content":"next"}]}`)
	a, _ := InspectBody(OpenAIChat, withThink)
	b, _ := InspectBody(OpenAIChat, without)
	if !bytes.Equal(a.AssistantDigest, b.AssistantDigest) || a.AssistantDigest == nil {
		t.Fatal("leading <think> must not change the digest")
	}
}

func TestInspectUserTextBound(t *testing.T) {
	policy := DefaultPolicy()
	policy.MaxUserTextBytes = 10
	summary, err := policy.InspectBody(OpenAIChat, []byte(`{"messages":[{"role":"user","content":"日本語のテキストです"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if summary.LastUserText != "日本語" {
		t.Fatalf("LastUserText = %q, want rune-safe cut", summary.LastUserText)
	}
}

func TestRegistryExtension(t *testing.T) {
	registry := DefaultRegistry()
	if got := registry.Protocols(); len(got) != 4 {
		t.Fatalf("Protocols = %v", got)
	}
	custom := Protocol("vendor.chat")
	registry.Register(custom, openAIChatAdapter{})
	policy := DefaultPolicy()
	policy.Registry = registry
	summary, err := policy.InspectBody(custom, []byte(`{"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil || summary.UserTurnCount != 1 {
		t.Fatalf("custom protocol: %+v %v", summary, err)
	}
	if _, err := InspectBody(custom, []byte(`{}`)); err != ErrUnsupportedProtocol {
		t.Fatal("registering on a copy must not leak into the default registry")
	}
	registry.Register(custom, nil)
	if _, ok := registry.Lookup(custom); ok {
		t.Fatal("nil adapter must unregister")
	}
}
