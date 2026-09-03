package convo

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func observeBytes(t *testing.T, policy Policy, protocol Protocol, streaming bool, data []byte, chunk int) ResponseSummary {
	t.Helper()
	observer := policy.NewResponseObserver(protocol, streaming)
	if observer == nil {
		t.Fatalf("no observer for %s", protocol)
	}
	for len(data) > 0 {
		size := min(chunk, len(data))
		if _, err := observer.Write(data[:size]); err != nil {
			t.Fatal(err)
		}
		data = data[size:]
	}
	return observer.Summary()
}

func TestObserveOpenAIChatStreamTextRoundTrip(t *testing.T) {
	summary := observeBytes(t, DefaultPolicy(), OpenAIChat, true, readFixture(t, "openai_chat", "stream_text.sse"), 7)
	if summary.OutputID != "chatcmpl-9f2b7c1e" {
		t.Fatalf("OutputID = %q", summary.OutputID)
	}
	if summary.EchoIDs != nil {
		t.Fatalf("EchoIDs = %v", summary.EchoIDs)
	}
	replay := inspectFixture(t, OpenAIChat, "openai_chat", "text_turn2.json")
	if !bytes.Equal(summary.AssistantDigest, replay.AssistantDigest) || summary.AssistantDigest == nil {
		t.Fatal("streamed answer (with <think>) and replayed answer must fingerprint identically")
	}
}

func TestObserveOpenAIChatStreamToolCall(t *testing.T) {
	summary := observeBytes(t, DefaultPolicy(), OpenAIChat, true, readFixture(t, "openai_chat", "stream_tool_call.sse"), 64)
	if want := []string{"call_7f3a9c2e1b4d4e8fa1c2"}; !reflect.DeepEqual(summary.EchoIDs, want) {
		t.Fatalf("EchoIDs = %v, want %v", summary.EchoIDs, want)
	}
	if summary.AssistantDigest != nil {
		t.Fatal("tool-only response must not fingerprint")
	}
	replay := inspectFixture(t, OpenAIChat, "openai_chat", "tool_loop_step2.json")
	if !reflect.DeepEqual(replay.EchoIDs, summary.EchoIDs) {
		t.Fatal("echo id produced by the response must equal the id the next request replays")
	}
}

func TestObserveOpenAIChatNonStreamRejectsSequentialIDs(t *testing.T) {
	summary := observeBytes(t, DefaultPolicy(), OpenAIChat, false, readFixture(t, "openai_chat", "nonstream_tool_call.json"), 1000)
	if summary.OutputID != "chatcmpl-b91e2f4a" {
		t.Fatalf("OutputID = %q", summary.OutputID)
	}
	if want := []string{"call_5e6d7c8b9a0f1e2d3c4b"}; !reflect.DeepEqual(summary.EchoIDs, want) {
		t.Fatalf("EchoIDs = %v, want %v (call_0 rejected)", summary.EchoIDs, want)
	}
}

func TestObserveOpenAIResponsesStreamRoundTrip(t *testing.T) {
	summary := observeBytes(t, DefaultPolicy(), OpenAIResponses, true, readFixture(t, "openai_responses", "stream.sse"), 33)
	if summary.OutputID != "resp_68b7d2a0f0e1d2c3b4a59687" {
		t.Fatalf("OutputID = %q", summary.OutputID)
	}
	want := []string{"rs_68b7d2a0f0e1d2c3b4a59688", "msg_68b7d2a0b1c2d3e4f5a6b7c8"}
	if !reflect.DeepEqual(summary.EchoIDs, want) {
		t.Fatalf("EchoIDs = %v, want %v", summary.EchoIDs, want)
	}
	replay := inspectFixture(t, OpenAIResponses, "openai_responses", "text_turn2.json")
	if !bytes.Equal(summary.AssistantDigest, replay.AssistantDigest) || summary.AssistantDigest == nil {
		t.Fatal("streamed output_text and replayed message item must fingerprint identically")
	}
	if replay.EchoIDs[0] != summary.EchoIDs[1] {
		t.Fatal("replayed message id must be among the response's echo ids")
	}
}

func TestObserveOpenAIResponsesNonStreamFunctionCall(t *testing.T) {
	summary := observeBytes(t, DefaultPolicy(), OpenAIResponses, false, readFixture(t, "openai_responses", "nonstream_function_call.json"), 50)
	if summary.OutputID != "resp_68b7d1e2f3a4b5c6d7e8f9ff" {
		t.Fatalf("OutputID = %q", summary.OutputID)
	}
	want := []string{"rs_68b7d1e2f3a4b5c6d7e8f9a0", "fc_68b7d1e2f3a4b5c6d7e8f9a1", "call_Ab12Cd34Ef56Gh78Ij90"}
	if !reflect.DeepEqual(summary.EchoIDs, want) {
		t.Fatalf("EchoIDs = %v, want %v", summary.EchoIDs, want)
	}
	replay := inspectFixture(t, OpenAIResponses, "openai_responses", "replay_items.json")
	for _, id := range replay.EchoIDs {
		found := false
		for _, out := range summary.EchoIDs {
			found = found || out == id
		}
		if !found {
			t.Fatalf("replayed id %q missing from response echo ids %v", id, summary.EchoIDs)
		}
	}
}

func TestObserveAnthropicStream(t *testing.T) {
	policy := DefaultPolicy()
	policy.MinFingerprintRunes = 1
	summary := observeBytes(t, policy, AnthropicMessages, true, readFixture(t, "anthropic", "stream.sse"), 11)
	if summary.OutputID != "msg_01Hj7kLmNoPqRsTuVwXyZ012" {
		t.Fatalf("OutputID = %q", summary.OutputID)
	}
	if want := []string{"toolu_01XyZabc123DEF456ghi789JK"}; !reflect.DeepEqual(summary.EchoIDs, want) {
		t.Fatalf("EchoIDs = %v, want %v", summary.EchoIDs, want)
	}
	if !bytes.Equal(summary.AssistantDigest, DigestText("我先看看 main.go 的内容。", true)) {
		t.Fatal("text deltas must be accumulated only for text blocks")
	}
	replay := inspectFixture(t, AnthropicMessages, "anthropic", "tool_loop.json")
	if !reflect.DeepEqual(replay.EchoIDs, summary.EchoIDs) {
		t.Fatal("tool_use id must round-trip through tool_result")
	}
}

func TestObserveAnthropicNonStreamSkipsThinking(t *testing.T) {
	summary := observeBytes(t, DefaultPolicy(), AnthropicMessages, false, readFixture(t, "anthropic", "nonstream.json"), 4096)
	if summary.OutputID != "msg_01AbCdEfGhIjKlMnOpQrStUv" {
		t.Fatalf("OutputID = %q", summary.OutputID)
	}
	replay := inspectFixture(t, AnthropicMessages, "anthropic", "text_turn2.json")
	if !bytes.Equal(summary.AssistantDigest, replay.AssistantDigest) || summary.AssistantDigest == nil {
		t.Fatal("thinking block must not affect the digest")
	}
}

func TestObserveGeminiStreamRoundTrip(t *testing.T) {
	summary := observeBytes(t, DefaultPolicy(), GeminiGenerateContent, true, readFixture(t, "gemini", "stream.sse"), 100)
	if summary.OutputID != "Qm3YaP2jN5Wx0PEP9pXg4Q0" {
		t.Fatalf("OutputID = %q", summary.OutputID)
	}
	replay := inspectFixture(t, GeminiGenerateContent, "gemini", "two_turns.json")
	if !bytes.Equal(summary.AssistantDigest, replay.AssistantDigest) || summary.AssistantDigest == nil {
		t.Fatal("thought parts must be excluded and text parts concatenated")
	}
	loop := inspectFixture(t, GeminiGenerateContent, "gemini", "function_loop.json")
	if !reflect.DeepEqual(summary.EchoIDs, loop.EchoIDs) {
		t.Fatalf("thoughtSignature hash mismatch: %v vs %v", summary.EchoIDs, loop.EchoIDs)
	}
}

func TestObserveGeminiNonStreamFunctionCall(t *testing.T) {
	summary := observeBytes(t, DefaultPolicy(), GeminiGenerateContent, false, readFixture(t, "gemini", "nonstream_function_call.json"), 4096)
	if summary.OutputID != "Rn4ZaP7kO6Xy1QFQ0qYh5R1" || len(summary.EchoIDs) != 1 {
		t.Fatalf("summary = %+v", summary)
	}
}

// TestObserveWriteChunkingInvariance: SSE framing must be independent of how
// the bytes arrive.
func TestObserveWriteChunkingInvariance(t *testing.T) {
	cases := []struct {
		protocol Protocol
		fixture  []string
	}{
		{OpenAIChat, []string{"openai_chat", "stream_text.sse"}},
		{OpenAIResponses, []string{"openai_responses", "stream.sse"}},
		{AnthropicMessages, []string{"anthropic", "stream.sse"}},
		{GeminiGenerateContent, []string{"gemini", "stream.sse"}},
	}
	policy := DefaultPolicy()
	policy.MinFingerprintRunes = 1
	for _, tc := range cases {
		data := readFixture(t, tc.fixture...)
		want := observeBytes(t, policy, tc.protocol, true, data, len(data))
		for _, chunk := range []int{1, 2, 3, 5, 17, 64, 1000} {
			got := observeBytes(t, policy, tc.protocol, true, data, chunk)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s chunk=%d: %+v != %+v", tc.protocol, chunk, got, want)
			}
		}
		// CRLF line endings and a missing trailing newline must also work.
		crlf := bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
		if got := observeBytes(t, policy, tc.protocol, true, bytes.TrimRight(crlf, "\r\n"), 9); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s crlf: %+v != %+v", tc.protocol, got, want)
		}
	}
}

// TestObserveEventMatchesWrite: hosts that already parse SSE can feed decoded
// documents and get the same result.
func TestObserveEventMatchesWrite(t *testing.T) {
	data := readFixture(t, "openai_responses", "stream.sse")
	want := observeBytes(t, DefaultPolicy(), OpenAIResponses, true, data, len(data))
	observer := NewResponseObserver(OpenAIResponses, true)
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal([]byte(strings.TrimSpace(line[5:])), &doc); err != nil {
			t.Fatal(err)
		}
		observer.ObserveEvent(doc)
	}
	if got := observer.Summary(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ObserveEvent %+v != Write %+v", got, want)
	}
}

func TestObserveTruncation(t *testing.T) {
	policy := DefaultPolicy()
	policy.MaxResponseBytes = 64
	observer := policy.NewResponseObserver(OpenAIChat, false)
	_, _ = observer.Write(bytes.Repeat([]byte("x"), 100))
	if summary := observer.Summary(); !summary.Truncated {
		t.Fatal("oversized non-streaming body must be reported as truncated")
	}
	observer = policy.NewResponseObserver(OpenAIChat, true)
	_, _ = observer.Write(bytes.Repeat([]byte("y"), 100))
	if summary := observer.Summary(); !summary.Truncated {
		t.Fatal("oversized streaming line must be reported as truncated")
	}
}

func TestObserveNilAndUnsupported(t *testing.T) {
	if NewResponseObserver(Protocol("openai.completions"), true) != nil {
		t.Fatal("unsupported protocol must return nil")
	}
	var observer *ResponseObserver
	n, err := observer.Write([]byte("data: {}\n"))
	if n != 9 || err != nil {
		t.Fatal("nil observer Write must be a no-op that reports full length")
	}
	observer.ObserveEvent(map[string]json.RawMessage{})
	if got := observer.Summary(); !reflect.DeepEqual(got, ResponseSummary{}) {
		t.Fatalf("nil observer summary = %+v", got)
	}
}

func TestObserveOutputEchoLimit(t *testing.T) {
	policy := DefaultPolicy()
	policy.MaxOutputEchoIDs = 2
	observer := policy.NewResponseObserver(OpenAIChat, false)
	observer.EchoID("call_aaaabbbbccccdddd1")
	observer.EchoID("call_aaaabbbbccccdddd2")
	observer.EchoID("call_aaaabbbbccccdddd2")
	observer.EchoID("call_aaaabbbbccccdddd3")
	if got := observer.Summary().EchoIDs; len(got) != 2 {
		t.Fatalf("EchoIDs = %v, want 2 entries", got)
	}
}
