package ingress

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestStreamOutputFollowsThePartAndNamesToolCalls(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol contract.ProtocolID
		events   []string
		want     *contract.OutputActivity
	}{
		{"anthropic", contract.ProtocolAnthropicMessages, []string{
			`{"type":"message_start","message":{"id":"m"}}`,
			`{"type":"content_block_start","content_block":{"type":"thinking","thinking":""}}`,
			`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"Plan"}}`,
			`{"type":"content_block_start","content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","delta":{"type":"text_delta","text":"Reading."}}`,
			`{"type":"content_block_start","content_block":{"type":"tool_use","name":"Read"}}`,
			`{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{"}}`,
			`{"type":"content_block_start","content_block":{"type":"tool_use","name":"Bash"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}`,
		}, &contract.OutputActivity{Kind: contract.OutputKindTool, Tools: []string{"Read", "Bash"}}},
		{"responses", contract.ProtocolOpenAIResponses, []string{
			`{"type":"response.created","response":{"id":"r"}}`,
			`{"type":"response.output_item.added","item":{"type":"reasoning"}}`,
			`{"type":"response.reasoning_summary_text.delta","delta":"Plan"}`,
			`{"type":"response.output_item.added","item":{"type":"web_search_call"}}`,
			`{"type":"response.output_item.added","item":{"type":"message"}}`,
			`{"type":"response.output_text.delta","delta":"Done"}`,
		}, &contract.OutputActivity{Kind: contract.OutputKindText, Tools: []string{"web_search"}}},
		{"chat", contract.ProtocolOpenAIChat, []string{
			`{"choices":[{"delta":{"role":"assistant","content":""}}]}`,
			`{"choices":[{"delta":{"reasoning_content":"Plan"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"exec_command","arguments":""}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\":\"ls\"}"}}]}}]}`,
		}, &contract.OutputActivity{Kind: contract.OutputKindTool, Tools: []string{"exec_command"}}},
		{"gemini", contract.ProtocolGoogleGenerateContent, []string{
			`{"candidates":[{"content":{"parts":[{"text":"Plan","thought":true}]}}]}`,
			`{"candidates":[{"content":{"parts":[{"text":"Sure."},{"functionCall":{"name":"search"}}]}}]}`,
		}, &contract.OutputActivity{Kind: contract.OutputKindTool, Tools: []string{"search"}}},
		{"metadata only", contract.ProtocolAnthropicMessages, []string{
			`{"type":"message_start","message":{"id":"m"}}`,
			`{"type":"ping"}`,
		}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			scanner := newUsageScanner(test.protocol, true)
			for _, event := range test.events {
				scanner.observe([]byte("data: " + event + "\n\n"))
			}
			if got := scanner.outputActivity(); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("output = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestStreamOutputBoundsToolNames(t *testing.T) {
	scanner := newUsageScanner(contract.ProtocolAnthropicMessages, true)
	for index := 0; index < contract.MaxOutputTools+2; index++ {
		scanner.observe([]byte(`data: {"type":"content_block_start","content_block":{"type":"tool_use","name":"tool\n` +
			strings.Repeat("x", contract.MaxOutputToolNameRunes) + `"}}` + "\n\n"))
	}
	output := scanner.outputActivity()
	if output == nil || len(output.Tools) != contract.MaxOutputTools {
		t.Fatalf("output = %+v", output)
	}
	if err := output.Validate(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.Tools[0], "tool ") {
		t.Fatalf("tool name = %q", output.Tools[0])
	}
}

func TestNonStreamingOutputReadsTheWholeResponse(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol contract.ProtocolID
		body     string
		want     *contract.OutputActivity
	}{
		{"anthropic", contract.ProtocolAnthropicMessages,
			`{"content":[{"type":"thinking","thinking":"x"},{"type":"text","text":"Let me look."},{"type":"tool_use","name":"Grep","input":{}}],"usage":{"input_tokens":1,"output_tokens":2}}`,
			&contract.OutputActivity{Kind: contract.OutputKindTool, Tools: []string{"Grep"}}},
		{"chat answer", contract.ProtocolOpenAIChat,
			`{"choices":[{"message":{"role":"assistant","content":"Done."}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			&contract.OutputActivity{Kind: contract.OutputKindText}},
		{"chat tools", contract.ProtocolOpenAIChat,
			`{"choices":[{"message":{"content":null,"tool_calls":[{"function":{"name":"read_file","arguments":"{}"}},{"function":{"name":"exec_command","arguments":"{}"}}]}}]}`,
			&contract.OutputActivity{Kind: contract.OutputKindTool, Tools: []string{"read_file", "exec_command"}}},
		{"responses", contract.ProtocolOpenAIResponses,
			`{"output":[{"type":"reasoning"},{"type":"function_call","name":"shell"}]}`,
			&contract.OutputActivity{Kind: contract.OutputKindTool, Tools: []string{"shell"}}},
		{"gemini", contract.ProtocolGoogleGenerateContent,
			`{"candidates":[{"content":{"parts":[{"text":"Hello"}]}}]}`,
			&contract.OutputActivity{Kind: contract.OutputKindText}},
	} {
		t.Run(test.name, func(t *testing.T) {
			scanner := newUsageScanner(test.protocol, false)
			scanner.observe([]byte(test.body))
			scanner.Usage()
			if got := scanner.outputActivity(); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("output = %+v, want %+v", got, test.want)
			}
		})
	}
}

// outputRecordingStore keeps the outputs a stream stored on its own.
type outputRecordingStore struct {
	memoryRequestRecordStore
	updates []contract.OutputActivity
}

func (store *outputRecordingStore) UpdateRequestOutput(
	_ context.Context,
	id contract.RequestID,
	output contract.OutputActivity,
) error {
	store.updates = append(store.updates, output)
	return nil
}

func TestStreamStoresItsOutputAtMostEveryInterval(t *testing.T) {
	store := &outputRecordingStore{}
	session := newRecordSession(Request{Protocol: contract.ProtocolAnthropicMessages, Streaming: true}, "", contract.AuditSettings{})
	session.persistStore = store
	session.upstreamScanner = newUsageScanner(contract.ProtocolAnthropicMessages, true)
	events := strings.Join([]string{
		`data: {"type":"content_block_start","content_block":{"type":"thinking","thinking":""}}`,
		`data: {"type":"content_block_start","content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_start","content_block":{"type":"tool_use","name":"Bash"}}`,
	}, "\n\n") + "\n\n"
	upstream := io.NopCloser(strings.NewReader(events))
	body := session.wrapUpstreamResponseBody(200, map[string][]string{"Content-Type": {"text/event-stream"}}, upstream)
	read := func(size int) {
		t.Helper()
		buffer := make([]byte, size)
		if _, err := io.ReadFull(body, buffer); err != nil {
			t.Fatal(err)
		}
	}
	first := len(strings.SplitAfter(events, "\n\n")[0])
	read(first)
	// The next part arrives within the interval and waits for a later read.
	second := len(strings.SplitAfter(events, "\n\n")[1])
	read(second)
	if want := []contract.OutputActivity{{Kind: contract.OutputKindReasoning}}; !reflect.DeepEqual(store.updates, want) {
		t.Fatalf("updates = %+v, want %+v", store.updates, want)
	}
	session.outputFlushedAt = session.outputFlushedAt.Add(-liveOutputInterval)
	read(len(events) - first - second)
	want := []contract.OutputActivity{
		{Kind: contract.OutputKindReasoning},
		{Kind: contract.OutputKindTool, Tools: []string{"Bash"}},
	}
	if !reflect.DeepEqual(store.updates, want) {
		t.Fatalf("updates = %+v, want %+v", store.updates, want)
	}
	if record := session.recordSnapshot(nil, nil); !reflect.DeepEqual(record.Output, &want[1]) {
		t.Fatalf("snapshot output = %+v", record.Output)
	}
}
