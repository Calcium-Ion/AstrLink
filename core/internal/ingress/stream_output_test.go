package ingress

import (
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestFirstTokenIgnoresMetadataAndTracksGeneratedContent(t *testing.T) {
	for _, test := range []struct {
		name             string
		protocol         contract.ProtocolID
		metadata, output string
	}{
		{"responses text", contract.ProtocolOpenAIResponses, `{"type":"response.created","response":{"id":"r"}}`, `{"type":"response.output_text.delta","delta":"Hi"}`},
		{"responses reasoning", contract.ProtocolOpenAIResponses, `{"type":"response.output_item.added","item":{"type":"reasoning"}}`, `{"type":"response.reasoning_summary_text.delta","delta":"Thinking"}`},
		{"responses tools", contract.ProtocolOpenAIResponses, `{"type":"response.function_call_arguments.delta","delta":""}`, `{"type":"response.output_item.added","item":{"type":"function_call","name":"read_file"}}`},
		{"chat", contract.ProtocolOpenAIChat, `{"choices":[{"delta":{"role":"assistant","content":""}}]}`, `{"choices":[{"delta":{"content":"Hi"}}]}`},
		{"chat tools", contract.ProtocolOpenAIChat, `{"choices":[],"usage":{"completion_tokens":5}}`, `{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{}"}}]}}]}`},
		{"completions", contract.ProtocolOpenAICompletions, `{"choices":[{"text":""}]}`, `{"choices":[{"text":"Hi"}]}`},
		{"anthropic", contract.ProtocolAnthropicMessages, `{"type":"message_start","message":{"id":"m"}}`, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hi"}}`},
		{"anthropic reasoning", contract.ProtocolAnthropicMessages, `{"type":"content_block_start","content_block":{"type":"thinking","thinking":""}}`, `{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"Thinking"}}`},
		{"anthropic tools", contract.ProtocolAnthropicMessages, `{"type":"ping"}`, `{"type":"content_block_start","content_block":{"type":"tool_use","name":"read_file"}}`},
		{"gemini", contract.ProtocolGoogleGenerateContent, `{"usageMetadata":{"promptTokenCount":5}}`, `{"candidates":[{"content":{"parts":[{"text":"Hi"}]}}]}`},
		{"gemini tools", contract.ProtocolGoogleGenerateContent, `{"candidates":[{"content":{"parts":[]}}]}`, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"read_file"}}]}}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			scanner := newUsageScanner(test.protocol, true)
			scanner.observe([]byte(": heartbeat\ndata: " + test.metadata + "\n\ndata: invalid\n\n"))
			if !scanner.firstOutputAt.IsZero() {
				t.Fatal("metadata was counted as a token")
			}
			wire := []byte("data: " + test.output + "\n\n")
			for _, b := range wire {
				scanner.observe([]byte{b})
			}
			first := scanner.firstOutputAt
			if first.IsZero() {
				t.Fatal("generated content was not timed")
			}
			scanner.observe(wire)
			if scanner.firstOutputAt != first {
				t.Fatal("later output replaced first token time")
			}
			scanner.reset(test.protocol, true)
			if !scanner.firstOutputAt.IsZero() {
				t.Fatal("retry inherited first token timing")
			}
			scanner.reset(test.protocol, false)
			scanner.observe([]byte(test.output))
			scanner.Usage()
			if !scanner.firstOutputAt.IsZero() {
				t.Fatal("non-streaming body invented a first token time")
			}
		})
	}
}

func TestRecordSnapshotKeepsUpstreamFirstTokenTimingPerAttempt(t *testing.T) {
	session := newRecordSession(Request{Protocol: contract.ProtocolOpenAIResponses, Streaming: true}, "", contract.AuditSettings{})
	session.upstreamScanner = newUsageScanner(contract.ProtocolOpenAIChat, true)
	session.upstreamScanner.firstOutputAt = session.startedAt.Add(2200 * time.Millisecond)
	// Downstream conversion can buffer: use the upstream timing with upstream usage.
	session.scanner.firstOutputAt = session.startedAt.Add(3 * time.Second)
	latency := 4000
	completed := session.startedAt.Add(4 * time.Second)
	record := session.recordSnapshot(&completed, &latency)
	if record.FirstTokenMs == nil || *record.FirstTokenMs != 2200 {
		t.Fatalf("first token = %v", record.FirstTokenMs)
	}
	session.resetAttemptLocal()
	if session.recordSnapshot(nil, nil).FirstTokenMs != nil {
		t.Fatal("new retry retained timing")
	}
}
