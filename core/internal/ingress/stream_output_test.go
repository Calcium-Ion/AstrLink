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
		reasoning        bool
	}{
		{"responses text", contract.ProtocolOpenAIResponses, `{"type":"response.created","response":{"id":"r"}}`, `{"type":"response.output_text.delta","delta":"Hi"}`, false},
		{"responses reasoning", contract.ProtocolOpenAIResponses, `{"type":"response.output_item.added","item":{"type":"reasoning"}}`, `{"type":"response.reasoning_summary_text.delta","delta":"Thinking"}`, true},
		{"responses reasoning text", contract.ProtocolOpenAIResponses, `{"type":"response.reasoning_text.delta","delta":""}`, `{"type":"response.reasoning_text.delta","delta":"Thinking"}`, true},
		{"responses gateway reasoning", contract.ProtocolOpenAIResponses, `{"type":"response.reasoning.done","text":"x"}`, `{"type":"response.reasoning.delta","delta":"Thinking"}`, true},
		{"responses refusal", contract.ProtocolOpenAIResponses, `{"type":"response.refusal.delta","delta":""}`, `{"type":"response.refusal.delta","delta":"No"}`, false},
		{"responses tools", contract.ProtocolOpenAIResponses, `{"type":"response.function_call_arguments.delta","delta":""}`, `{"type":"response.output_item.added","item":{"type":"function_call","name":"read_file"}}`, false},
		{"chat", contract.ProtocolOpenAIChat, `{"choices":[{"delta":{"role":"assistant","content":""}}]}`, `{"choices":[{"delta":{"content":"Hi"}}]}`, false},
		{"chat reasoning", contract.ProtocolOpenAIChat, `{"choices":[{"delta":{"reasoning_content":""}}]}`, `{"choices":[{"delta":{"reasoning_content":"Thinking"}}]}`, true},
		{"chat tools", contract.ProtocolOpenAIChat, `{"choices":[],"usage":{"completion_tokens":5}}`, `{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{}"}}]}}]}`, false},
		{"completions", contract.ProtocolOpenAICompletions, `{"choices":[{"text":""}]}`, `{"choices":[{"text":"Hi"}]}`, false},
		{"anthropic", contract.ProtocolAnthropicMessages, `{"type":"message_start","message":{"id":"m"}}`, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hi"}}`, false},
		{"anthropic reasoning", contract.ProtocolAnthropicMessages, `{"type":"content_block_start","content_block":{"type":"thinking","thinking":""}}`, `{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"Thinking"}}`, true},
		{"anthropic tools", contract.ProtocolAnthropicMessages, `{"type":"ping"}`, `{"type":"content_block_start","content_block":{"type":"tool_use","name":"read_file"}}`, false},
		{"gemini", contract.ProtocolGoogleGenerateContent, `{"usageMetadata":{"promptTokenCount":5}}`, `{"candidates":[{"content":{"parts":[{"text":"Hi"}]}}]}`, false},
		{"gemini reasoning", contract.ProtocolGoogleGenerateContent, `{"candidates":[{"content":{"parts":[{"text":"","thought":true}]}}]}`, `{"candidates":[{"content":{"parts":[{"text":"Thinking","thought":true}]}}]}`, true},
		{"gemini tools", contract.ProtocolGoogleGenerateContent, `{"candidates":[{"content":{"parts":[]}}]}`, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"read_file"}}]}}]}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			scanner := newUsageScanner(test.protocol, true)
			scanner.observe([]byte(": heartbeat\ndata: " + test.metadata + "\n\ndata: invalid\n\n"))
			if !scanner.firstOutputAt.IsZero() || !scanner.firstAnswerAt.IsZero() {
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
			// Reasoning starts the clock but is not yet an answer.
			if test.reasoning != scanner.firstAnswerAt.IsZero() {
				t.Fatalf("first answer = %v for reasoning=%v", scanner.firstAnswerAt, test.reasoning)
			}
			if !test.reasoning && scanner.firstAnswerAt != first {
				t.Fatal("an answer as the first token must share its time")
			}
			scanner.observe(wire)
			if scanner.firstOutputAt != first {
				t.Fatal("later output replaced first token time")
			}
			scanner.reset(test.protocol, true)
			if !scanner.firstOutputAt.IsZero() || !scanner.firstAnswerAt.IsZero() {
				t.Fatal("retry inherited first token timing")
			}
			scanner.reset(test.protocol, false)
			scanner.observe([]byte(test.output))
			scanner.Usage()
			if !scanner.firstOutputAt.IsZero() || !scanner.firstAnswerAt.IsZero() {
				t.Fatal("non-streaming body invented a first token time")
			}
		})
	}
}

func TestFirstAnswerFollowsReasoningAndStaysPut(t *testing.T) {
	scanner := newUsageScanner(contract.ProtocolOpenAIResponses, true)
	scanner.observe([]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"Thinking\"}\n\n"))
	thought := scanner.firstOutputAt
	if thought.IsZero() || !scanner.firstAnswerAt.IsZero() {
		t.Fatalf("after reasoning: first=%v answer=%v", thought, scanner.firstAnswerAt)
	}
	scanner.observe([]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"More\"}\n\n"))
	if !scanner.firstAnswerAt.IsZero() {
		t.Fatal("more reasoning was counted as an answer")
	}
	scanner.observe([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hi\"}\n\n"))
	answer := scanner.firstAnswerAt
	if answer.IsZero() || answer.Before(thought) || scanner.firstOutputAt != thought {
		t.Fatalf("after text: first=%v answer=%v", scanner.firstOutputAt, answer)
	}
	scanner.observe([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"there\"}\n\n"))
	if scanner.firstAnswerAt != answer {
		t.Fatal("later text replaced the first answer time")
	}
}

func TestRecordSnapshotKeepsUpstreamFirstTokenTimingPerAttempt(t *testing.T) {
	session := newRecordSession(Request{Protocol: contract.ProtocolOpenAIResponses, Streaming: true}, "", contract.AuditSettings{})
	session.upstreamScanner = newUsageScanner(contract.ProtocolOpenAIChat, true)
	session.upstreamScanner.firstOutputAt = session.startedAt.Add(2200 * time.Millisecond)
	session.upstreamScanner.firstAnswerAt = session.startedAt.Add(3500 * time.Millisecond)
	// Downstream conversion can buffer: use the upstream timing with upstream usage.
	session.scanner.firstOutputAt = session.startedAt.Add(3 * time.Second)
	session.scanner.firstAnswerAt = session.startedAt.Add(3900 * time.Millisecond)
	latency := 4000
	completed := session.startedAt.Add(4 * time.Second)
	record := session.recordSnapshot(&completed, &latency)
	if record.FirstTokenMs == nil || *record.FirstTokenMs != 2200 {
		t.Fatalf("first token = %v", record.FirstTokenMs)
	}
	if record.FirstAnswerMs == nil || *record.FirstAnswerMs != 3500 {
		t.Fatalf("first answer = %v", record.FirstAnswerMs)
	}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	// A call that only reasoned has no answer time.
	session.upstreamScanner.firstAnswerAt = time.Time{}
	if session.recordSnapshot(&completed, &latency).FirstAnswerMs != nil {
		t.Fatal("reasoning-only call invented an answer time")
	}
	session.resetAttemptLocal()
	if snapshot := session.recordSnapshot(nil, nil); snapshot.FirstTokenMs != nil || snapshot.FirstAnswerMs != nil {
		t.Fatal("new retry retained timing")
	}
}
