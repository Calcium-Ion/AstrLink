package relaykitbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestEngineEdgesAndDescriptor(t *testing.T) {
	engine := NewEngine()
	edges := engine.Edges()
	if len(edges) != 12 {
		t.Fatalf("edges = %d, want 12", len(edges))
	}
	edges[0].From = "mutated"
	if engine.Edges()[0].From == "mutated" {
		t.Fatal("Edges returned its backing slice")
	}
	descriptor := Descriptor(engine)
	if !descriptor.Available || descriptor.Version == nil || *descriptor.Version == "" || len(descriptor.Edges) != 12 {
		t.Fatalf("unexpected descriptor: %#v", descriptor)
	}
	noop := Descriptor(NoopEngine{})
	if noop.Available || noop.Version != nil || len(noop.Edges) != 0 {
		t.Fatalf("unexpected noop descriptor: %#v", noop)
	}
}

func TestEngineConvertsChatRequestWithUpstreamModel(t *testing.T) {
	engine := NewEngine()
	output, err := engine.ConvertRequest(context.Background(), ConvertRequestInput{
		From: contract.ProtocolOpenAIChat, To: contract.ProtocolOpenAIResponses,
		Body:        []byte(`{"model":"public-model","messages":[{"role":"user","content":"hello"}]}`),
		PublicModel: "public-model", UpstreamModel: "upstream-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(output.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "upstream-model" {
		t.Fatalf("model = %#v, want upstream-model", body["model"])
	}
}

func TestConvertRequestSplitsReasoningSuffixForClaude(t *testing.T) {
	engine := NewEngine()
	output, err := engine.ConvertRequest(context.Background(), ConvertRequestInput{
		From: contract.ProtocolOpenAIChat, To: contract.ProtocolAnthropicMessages,
		Body:        []byte(`{"model":"public-model","messages":[{"role":"user","content":"hello"}]}`),
		PublicModel: "public-model", UpstreamModel: "claude-opus-4-7-high",
	})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Model        string          `json:"model"`
		Thinking     json.RawMessage `json:"thinking"`
		OutputConfig struct {
			Effort string `json:"effort"`
		} `json:"output_config"`
	}
	if err := json.Unmarshal(output.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Model != "claude-opus-4-7" {
		t.Fatalf("model = %q, want suffix trimmed: %s", body.Model, output.Body)
	}
	if body.OutputConfig.Effort != "high" || len(body.Thinking) == 0 {
		t.Fatalf("reasoning intent from suffix was not rendered: %s", output.Body)
	}
}

func TestConvertRequestSplitsReasoningSuffixForGemini(t *testing.T) {
	engine := NewEngine()
	output, err := engine.ConvertRequest(context.Background(), ConvertRequestInput{
		From: contract.ProtocolOpenAIChat, To: contract.ProtocolGoogleGenerateContent,
		Body:        []byte(`{"model":"public-model","messages":[{"role":"user","content":"hello"}]}`),
		PublicModel: "public-model", UpstreamModel: "gemini-2.5-flash-nothinking",
	})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		GenerationConfig struct {
			ThinkingConfig *struct {
				ThinkingBudget *int `json:"thinkingBudget"`
			} `json:"thinkingConfig"`
		} `json:"generationConfig"`
	}
	if err := json.Unmarshal(output.Body, &body); err != nil {
		t.Fatal(err)
	}
	config := body.GenerationConfig.ThinkingConfig
	if config == nil || config.ThinkingBudget == nil || *config.ThinkingBudget != 0 {
		t.Fatalf("-nothinking suffix was not rendered as thinkingBudget 0: %s", output.Body)
	}
}

func TestConvertRequestKeepsModelSuffixForOpenAITargets(t *testing.T) {
	engine := NewEngine()
	output, err := engine.ConvertRequest(context.Background(), ConvertRequestInput{
		From: contract.ProtocolAnthropicMessages, To: contract.ProtocolOpenAIChat,
		Body:        []byte(`{"model":"public-model","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
		PublicModel: "public-model", UpstreamModel: "claude-opus-4-7-high",
	})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(output.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "claude-opus-4-7-high" {
		t.Fatalf("model = %#v, want configured name kept for OpenAI-compatible upstream", body["model"])
	}
}

func TestConvertRequestRejectsMalformedThinkingBudgetSuffix(t *testing.T) {
	_, err := NewEngine().ConvertRequest(context.Background(), ConvertRequestInput{
		From: contract.ProtocolOpenAIChat, To: contract.ProtocolAnthropicMessages,
		Body:        []byte(`{"model":"public-model","messages":[{"role":"user","content":"hello"}]}`),
		PublicModel: "public-model", UpstreamModel: "claude-opus-4-7-thinking-abc",
	})
	if err == nil {
		t.Fatal("malformed -thinking-<budget> suffix was accepted")
	}
}

func TestStreamToResponsesEmitsSequenceNumber(t *testing.T) {
	stream, err := NewEngine().NewResponseStream(context.Background(), StreamOptions{
		From: contract.ProtocolOpenAIChat, To: contract.ProtocolOpenAIResponses,
		PublicModel: "public-model", UpstreamModel: "upstream-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	events, err := stream.Convert(context.Background(), ResponseEvent{Type: "data", Data: []byte(
		`{"id":"chatcmpl_1","object":"chat.completion.chunk","model":"upstream-model","choices":[{"index":0,"delta":{"content":"o"},"finish_reason":null}]}`,
	)})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("no Responses events emitted")
	}
	for index, event := range events {
		var payload struct {
			SequenceNumber *int `json:"sequence_number"`
		}
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.SequenceNumber == nil || *payload.SequenceNumber != index {
			t.Fatalf("event %d (%s) sequence_number = %v, want %d: %s", index, event.Type, payload.SequenceNumber, index, event.Data)
		}
	}
}

func TestStreamIgnoresChatDoneMarkerUntilFinalize(t *testing.T) {
	stream, err := NewEngine().NewResponseStream(context.Background(), StreamOptions{
		From: contract.ProtocolOpenAIChat, To: contract.ProtocolAnthropicMessages,
		PublicModel: "public-model", UpstreamModel: "upstream-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for _, event := range []ResponseEvent{
		{Type: "done", Data: []byte("[DONE]")},
		{Type: "data", Data: []byte("[DONE]")}, // adapter that did not classify the terminator
	} {
		events, err := stream.Convert(context.Background(), event)
		if err != nil || len(events) != 0 {
			t.Fatalf("Convert(%q %q) = %v, %v; want no events and no error", event.Type, event.Data, events, err)
		}
	}
	events, err := stream.Finalize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("Finalize emitted no terminal Claude events")
	}
}

func TestStreamFinalizeDoesNotCompleteOnClose(t *testing.T) {
	stream, err := NewEngine().NewResponseStream(context.Background(), StreamOptions{
		From: contract.ProtocolOpenAIChat, To: contract.ProtocolOpenAIResponses,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Finalize(context.Background()); err == nil {
		t.Fatal("Finalize after interrupted Close succeeded")
	}
}

func TestMediaAddressPolicy(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "::1", "10.0.0.1", "169.254.169.254", "192.168.1.1"} {
		if _, err := checkedMediaURL(context.Background(), "https://"+address+"/x"); err == nil {
			t.Fatalf("allowed forbidden media address %s", address)
		}
	}
	if _, err := checkedMediaURL(context.Background(), "http://example.com/x"); err == nil {
		t.Fatal("allowed non-HTTPS media URL")
	}
}

const customToolPatch = "*** Begin Patch\n*** Add File: hello.txt\n+say \"hi\" \\ bye\n*** End Patch\n"

// customToolRequest is a Codex-shaped Responses request that defines the
// freeform apply_patch tool and a plain function tool.
const customToolRequest = `{"model":"public-model","input":"add hello.txt","tools":[` +
	`{"type":"custom","name":"apply_patch","description":"Apply a patch","format":{"type":"grammar","syntax":"lark","definition":"start: /.+/"}},` +
	`{"type":"function","name":"read_file","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}]}`

func customToolUpstreamResponse(t *testing.T, protocol contract.ProtocolID) []byte {
	t.Helper()
	arguments, err := json.Marshal(map[string]string{"input": customToolPatch})
	if err != nil {
		t.Fatal(err)
	}
	var body any
	switch protocol {
	case contract.ProtocolOpenAIChat:
		body = map[string]any{
			"id": "chatcmpl_1", "object": "chat.completion", "model": "upstream-model",
			"choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls", "message": map[string]any{
				"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
					"id": "call_patch", "type": "function",
					"function": map[string]any{"name": "apply_patch", "arguments": string(arguments)},
				}},
			}}},
		}
	case contract.ProtocolAnthropicMessages:
		body = map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": "upstream-model", "stop_reason": "tool_use",
			"content": []any{map[string]any{
				"type": "tool_use", "id": "toolu_patch", "name": "apply_patch", "input": map[string]any{"input": customToolPatch},
			}},
			"usage": map[string]any{"input_tokens": 4, "output_tokens": 2},
		}
	case contract.ProtocolGoogleGenerateContent:
		body = map[string]any{
			"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"role": "model", "parts": []any{
				map[string]any{"functionCall": map[string]any{"name": "apply_patch", "args": map[string]any{"input": customToolPatch}}},
			}}}},
			"usageMetadata": map[string]any{"promptTokenCount": 4, "candidatesTokenCount": 2, "totalTokenCount": 6},
		}
	default:
		t.Fatalf("unsupported protocol %s", protocol)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func customToolUpstreamStream(t *testing.T, protocol contract.ProtocolID) []ResponseEvent {
	t.Helper()
	arguments, err := json.Marshal(map[string]string{"input": customToolPatch})
	if err != nil {
		t.Fatal(err)
	}
	half := len(arguments) / 2
	jsonString := func(value string) string {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	switch protocol {
	case contract.ProtocolOpenAIChat:
		return []ResponseEvent{
			{Type: "data", Data: []byte(`{"id":"chatcmpl_1","object":"chat.completion.chunk","model":"upstream-model","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_patch","type":"function","function":{"name":"apply_patch","arguments":` + jsonString(string(arguments[:half])) + `}}]},"finish_reason":null}]}`)},
			{Type: "data", Data: []byte(`{"id":"chatcmpl_1","object":"chat.completion.chunk","model":"upstream-model","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":` + jsonString(string(arguments[half:])) + `}}]},"finish_reason":"tool_calls"}]}`)},
			{Type: "done", Data: []byte("[DONE]")},
		}
	case contract.ProtocolAnthropicMessages:
		return []ResponseEvent{
			{Type: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"upstream-model","content":[],"usage":{"input_tokens":4,"output_tokens":0}}}`)},
			{Type: "content_block_start", Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_patch","name":"apply_patch","input":{}}}`)},
			{Type: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + jsonString(string(arguments[:half])) + `}}`)},
			{Type: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + jsonString(string(arguments[half:])) + `}}`)},
			{Type: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
			{Type: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":2}}`)},
			{Type: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
		}
	case contract.ProtocolGoogleGenerateContent:
		return []ResponseEvent{{Type: "data", Data: customToolUpstreamResponse(t, protocol)}}
	default:
		t.Fatalf("unsupported protocol %s", protocol)
		return nil
	}
}

func convertCustomToolRequest(t *testing.T, engine *Engine, target contract.ProtocolID, streaming bool) ConvertRequestOutput {
	t.Helper()
	output, err := engine.ConvertRequest(context.Background(), ConvertRequestInput{
		From: contract.ProtocolOpenAIResponses, To: target, Body: []byte(customToolRequest),
		PublicModel: "public-model", UpstreamModel: "upstream-model", Streaming: streaming,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Body, []byte(`apply_patch`)) {
		t.Fatalf("converted %s request lost apply_patch: %s", target, output.Body)
	}
	return output
}

var customToolTargets = []contract.ProtocolID{
	contract.ProtocolOpenAIChat, contract.ProtocolAnthropicMessages, contract.ProtocolGoogleGenerateContent,
}

func TestConvertResponseRestoresCustomToolCallFromRequestState(t *testing.T) {
	engine := NewEngine()
	for _, target := range customToolTargets {
		t.Run(string(target), func(t *testing.T) {
			request := convertCustomToolRequest(t, engine, target, false)
			convert := func(state ConversionState) map[string]any {
				output, err := engine.ConvertResponse(context.Background(), ConvertResponseInput{
					From: target, To: contract.ProtocolOpenAIResponses, StatusCode: 200,
					Body: customToolUpstreamResponse(t, target), PublicModel: "public-model", UpstreamModel: "upstream-model",
					State: state,
				})
				if err != nil {
					t.Fatal(err)
				}
				var body struct {
					Output []map[string]any `json:"output"`
				}
				if err := json.Unmarshal(output.Body, &body); err != nil {
					t.Fatal(err)
				}
				for _, item := range body.Output {
					if item["name"] == "apply_patch" {
						return item
					}
				}
				t.Fatalf("no apply_patch output item: %s", output.Body)
				return nil
			}

			item := convert(request.State)
			if item["type"] != "custom_tool_call" || item["input"] != customToolPatch {
				t.Fatalf("restored item = %#v, want custom_tool_call with the original input", item)
			}
			// Without the request's state the call has no way back to its custom shape.
			if item := convert(ConversionState{}); item["type"] != "function_call" {
				t.Fatalf("stateless item type = %v, want function_call", item["type"])
			}
		})
	}
}

func TestResponseStreamRestoresCustomToolCallFromRequestState(t *testing.T) {
	engine := NewEngine()
	for _, target := range customToolTargets {
		t.Run(string(target), func(t *testing.T) {
			request := convertCustomToolRequest(t, engine, target, true)
			stream, err := engine.NewResponseStream(context.Background(), StreamOptions{
				From: target, To: contract.ProtocolOpenAIResponses,
				PublicModel: "public-model", UpstreamModel: "upstream-model", State: request.State,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			var events []ResponseEvent
			for _, event := range customToolUpstreamStream(t, target) {
				converted, err := stream.Convert(context.Background(), event)
				if err != nil {
					t.Fatal(err)
				}
				events = append(events, converted...)
			}
			final, err := stream.Finalize(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			events = append(events, final...)

			var input strings.Builder
			var done, item map[string]any
			for _, event := range events {
				var payload map[string]any
				if err := json.Unmarshal(event.Data, &payload); err != nil {
					t.Fatal(err)
				}
				switch event.Type {
				case "response.function_call_arguments.delta", "response.function_call_arguments.done":
					t.Fatalf("custom tool streamed as a function call: %s", event.Data)
				case "response.custom_tool_call_input.delta":
					delta, _ := payload["delta"].(string)
					input.WriteString(delta)
				case "response.custom_tool_call_input.done":
					done = payload
				case "response.output_item.done":
					if output, _ := payload["item"].(map[string]any); output["name"] == "apply_patch" {
						item = output
					}
				}
			}
			if input.String() != customToolPatch {
				t.Fatalf("custom tool input deltas = %q, want %q", input.String(), customToolPatch)
			}
			if done == nil || done["input"] != customToolPatch {
				t.Fatalf("custom_tool_call_input.done = %#v", done)
			}
			if item == nil || item["type"] != "custom_tool_call" || item["input"] != customToolPatch {
				t.Fatalf("output item = %#v, want custom_tool_call with the original input", item)
			}
		})
	}
}

func TestConvertRequestReportsDroppedToolsWithoutFailing(t *testing.T) {
	output, err := NewEngine().ConvertRequest(context.Background(), ConvertRequestInput{
		From: contract.ProtocolOpenAIResponses, To: contract.ProtocolOpenAIChat,
		Body: []byte(`{"model":"public-model","input":"list files","tools":[{"type":"local_shell"},` +
			`{"type":"function","name":"read_file","parameters":{"type":"object"}}]}`),
		PublicModel: "public-model",
	})
	if err != nil {
		t.Fatalf("lossy conversion failed: %v", err)
	}
	if len(output.Diagnostics) == 0 {
		t.Fatal("dropped local_shell tool produced no diagnostic")
	}
	diagnostic := output.Diagnostics[0]
	if diagnostic.Path != "tools[0]" || diagnostic.Code == "" || diagnostic.Message == "" ||
		(diagnostic.Severity != "warning" && diagnostic.Severity != "error") {
		t.Fatalf("unexpected diagnostic: %#v", diagnostic)
	}
	if output.State != (ConversionState{}) {
		t.Fatalf("request without custom tools recorded state: %#v", output.State)
	}
}

// Codex retries a response that failed mid-stream with the reasoning and text
// it had already received at the end of input (issue #56). Claude and Gemini
// would continue that tail as a prefill, which Claude Opus 4.6 rejects, so the
// converted request must end with the tool result.
func TestConvertRequestDropsCodexRetryTail(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-6-thinking","stream":true,"input":[` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"list files"}]},` +
		`{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"ls\"}","call_id":"call_1"},` +
		`{"type":"function_call_output","call_id":"call_1","output":"a.txt"},` +
		`{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"next"}],"encrypted_content":null},` +
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Reading a.txt."}]}],` +
		`"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}}}}]}`)
	for _, to := range []contract.ProtocolID{contract.ProtocolGoogleGenerateContent, contract.ProtocolAnthropicMessages} {
		output, err := NewEngine().ConvertRequest(context.Background(), ConvertRequestInput{
			From: contract.ProtocolOpenAIResponses, To: to, Body: body, PublicModel: "claude-opus-4-6-thinking",
		})
		if err != nil {
			t.Fatalf("%s: %v", to, err)
		}
		var request struct {
			Contents []struct {
				Role  string `json:"role"`
				Parts []struct {
					FunctionResponse json.RawMessage `json:"functionResponse"`
				} `json:"parts"`
			} `json:"contents"`
			Messages []struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(output.Body, &request); err != nil {
			t.Fatal(err)
		}
		endsWithResult := false
		if contents := request.Contents; len(contents) > 0 {
			last := contents[len(contents)-1]
			endsWithResult = last.Role == "user" && len(last.Parts) == 1 && last.Parts[0].FunctionResponse != nil
		} else if messages := request.Messages; len(messages) > 0 {
			last := messages[len(messages)-1]
			endsWithResult = last.Role == "user" && len(last.Content) == 1 && last.Content[0].Type == "tool_result"
		}
		if !endsWithResult {
			t.Fatalf("%s request does not end with the tool result: %s", to, output.Body)
		}
		if !slices.ContainsFunc(output.Diagnostics, func(diagnostic ConversionDiagnostic) bool {
			return diagnostic.Code == "trailing_assistant_omitted"
		}) {
			t.Fatalf("%s diagnostics = %+v, want trailing_assistant_omitted", to, output.Diagnostics)
		}
	}
}
