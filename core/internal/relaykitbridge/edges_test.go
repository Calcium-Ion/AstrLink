package relaykitbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestTwelveEdgesConvertRequestResponseAndStream(t *testing.T) {
	engine := NewEngine()
	edges := engine.Edges()
	if len(edges) != 12 {
		t.Fatalf("edges = %d, want 12", len(edges))
	}

	for _, edge := range edges {
		edge := edge
		t.Run(string(edge.From)+"->"+string(edge.To), func(t *testing.T) {
			requestBody := sampleRequest(t, edge.From)
			convertedRequest, err := engine.ConvertRequest(context.Background(), ConvertRequestInput{
				From: edge.From, To: edge.To, Body: requestBody,
				PublicModel: "public-model", UpstreamModel: "upstream-model",
			})
			if err != nil {
				t.Fatalf("ConvertRequest: %v", err)
			}
			assertJSONModel(t, convertedRequest.Body, "upstream-model", edge.To)

			upstreamResponse := sampleResponse(t, edge.To)
			convertedResponse, err := engine.ConvertResponse(context.Background(), ConvertResponseInput{
				From: edge.To, To: edge.From, StatusCode: 200, Body: upstreamResponse, PublicModel: "public-model",
			})
			if err != nil {
				t.Fatalf("ConvertResponse: %v", err)
			}
			if edge.From != contract.ProtocolGoogleGenerateContent {
				assertJSONContains(t, convertedResponse.Body, "public-model")
			}
			assertTerminalMarker(t, edge.From, convertedResponse.Body)
			assertBillingUsageSurvives(t, convertedResponse.Body)

			stream, err := engine.NewResponseStream(context.Background(), StreamOptions{
				From: edge.To, To: edge.From, PublicModel: "public-model", UpstreamModel: "upstream-model",
			})
			if err != nil {
				t.Fatalf("NewResponseStream: %v", err)
			}
			chunk := sampleStreamChunk(t, edge.To)
			chunkEvents, err := stream.Convert(context.Background(), ResponseEvent{Type: streamEventType(edge.To), Data: chunk})
			if err != nil {
				t.Fatalf("Convert stream chunk: %v", err)
			}
			events, err := stream.Finalize(context.Background())
			if err != nil {
				t.Fatalf("Finalize: %v", err)
			}
			if err := stream.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if len(events) == 0 && edge.From != contract.ProtocolGoogleGenerateContent {
				// Some Gemini finals may be empty after a full chunk; other
				// protocols must emit a terminal/completion event.
				t.Fatalf("Finalize returned no events")
			}
			for _, event := range append(chunkEvents, events...) {
				assertStreamEventShape(t, edge.From, event)
			}
		})
	}
}

// assertStreamEventShape checks that a converted stream event is a bare
// protocol payload (never a RelayKit wrapper such as {"Type","Payload"}), that
// event-typed protocols carry the SSE event name, and that any model field
// visible to the client has been restored to the public model.
func assertStreamEventShape(t *testing.T, protocol contract.ProtocolID, event ResponseEvent) {
	t.Helper()
	if event.Type == "done" {
		if protocol != contract.ProtocolOpenAIChat || string(event.Data) != "[DONE]" {
			t.Fatalf("unexpected done marker for %s: %q", protocol, event.Data)
		}
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		t.Fatalf("stream event is not a JSON object: %v data=%s", err, event.Data)
	}
	if _, wrapped := payload["Payload"]; wrapped {
		t.Fatalf("RelayKit wrapper leaked onto the wire: %s", event.Data)
	}
	switch protocol {
	case contract.ProtocolOpenAIChat, contract.ProtocolGoogleGenerateContent:
		if event.Type != "data" {
			t.Fatalf("event type = %q, want data: %s", event.Type, event.Data)
		}
	case contract.ProtocolOpenAIResponses, contract.ProtocolAnthropicMessages:
		if event.Type == "" || payload["type"] != event.Type {
			t.Fatalf("event type %q does not match payload type %#v: %s", event.Type, payload["type"], event.Data)
		}
	}
	if protocol == contract.ProtocolOpenAIResponses {
		if _, ok := payload["sequence_number"]; !ok {
			t.Fatalf("responses event missing sequence_number: %s", event.Data)
		}
		if response, ok := payload["response"].(map[string]any); ok {
			if model, ok := response["model"].(string); ok && model != "" && model != "public-model" {
				t.Fatalf("responses model = %q, want public-model: %s", model, event.Data)
			}
		}
	}
	if model, ok := payload["model"].(string); ok && model != "" && model != "public-model" {
		t.Fatalf("model = %q, want public-model: %s", model, event.Data)
	}
}

func TestBillingUsageSurvivesCompositeClaudeToGeminiToResponses(t *testing.T) {
	engine := NewEngine()
	first, err := engine.ConvertResponse(context.Background(), ConvertResponseInput{
		From: contract.ProtocolAnthropicMessages, To: contract.ProtocolGoogleGenerateContent,
		StatusCode: 200, PublicModel: "public-model",
		Body: []byte(`{
			"id":"msg_1","type":"message","role":"assistant","model":"claude",
			"content":[{"type":"text","text":"hi"}],
			"stop_reason":"end_turn",
			"usage":{
				"input_tokens":11,"output_tokens":7,
				"cache_read_input_tokens":3,"cache_creation_input_tokens":2
			}
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.ConvertResponse(context.Background(), ConvertResponseInput{
		From: contract.ProtocolGoogleGenerateContent, To: contract.ProtocolOpenAIResponses,
		StatusCode: 200, PublicModel: "public-model", Body: first.Body,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertBillingUsageSurvives(t, second.Body)
	if !strings.Contains(string(second.Body), `"source"`) {
		t.Fatalf("missing billing usage source: %s", second.Body)
	}
}

func sampleRequest(t *testing.T, protocol contract.ProtocolID) []byte {
	t.Helper()
	switch protocol {
	case contract.ProtocolOpenAIChat:
		return []byte(`{"model":"public-model","messages":[{"role":"user","content":"hi"}]}`)
	case contract.ProtocolOpenAIResponses:
		return []byte(`{"model":"public-model","input":"hi"}`)
	case contract.ProtocolAnthropicMessages:
		return []byte(`{"model":"public-model","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`)
	case contract.ProtocolGoogleGenerateContent:
		return []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	default:
		t.Fatalf("unsupported protocol %s", protocol)
		return nil
	}
}

func sampleResponse(t *testing.T, protocol contract.ProtocolID) []byte {
	t.Helper()
	switch protocol {
	case contract.ProtocolOpenAIChat:
		return []byte(`{
			"id":"chatcmpl_1","object":"chat.completion","model":"upstream-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7,
				"prompt_tokens_details":{"cached_tokens":1},
				"completion_tokens_details":{"reasoning_tokens":1}}
		}`)
	case contract.ProtocolOpenAIResponses:
		return []byte(`{
			"id":"resp_1","object":"response","status":"completed","model":"upstream-model",
			"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],
			"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7,
				"input_tokens_details":{"cached_tokens":1},
				"output_tokens_details":{"reasoning_tokens":1}}
		}`)
	case contract.ProtocolAnthropicMessages:
		return []byte(`{
			"id":"msg_1","type":"message","role":"assistant","model":"upstream-model",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":5,"output_tokens":2,"cache_read_input_tokens":1,"cache_creation_input_tokens":1}
		}`)
	case contract.ProtocolGoogleGenerateContent:
		return []byte(`{
			"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],
			"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":7,"thoughtsTokenCount":1}
		}`)
	default:
		t.Fatalf("unsupported protocol %s", protocol)
		return nil
	}
}

func sampleStreamChunk(t *testing.T, protocol contract.ProtocolID) []byte {
	t.Helper()
	switch protocol {
	case contract.ProtocolOpenAIChat:
		return []byte(`{"id":"chatcmpl_1","object":"chat.completion.chunk","model":"upstream-model","choices":[{"index":0,"delta":{"content":"o"},"finish_reason":null}]}`)
	case contract.ProtocolOpenAIResponses:
		return []byte(`{"type":"response.output_text.delta","delta":"o"}`)
	case contract.ProtocolAnthropicMessages:
		return []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"o"}}`)
	case contract.ProtocolGoogleGenerateContent:
		return []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"o"}]}}]}`)
	default:
		t.Fatalf("unsupported protocol %s", protocol)
		return nil
	}
}

func streamEventType(protocol contract.ProtocolID) string {
	switch protocol {
	case contract.ProtocolOpenAIResponses:
		return "response.output_text.delta"
	case contract.ProtocolAnthropicMessages:
		return "content_block_delta"
	default:
		return "data"
	}
}

func assertJSONModel(t *testing.T, body []byte, want string, protocol contract.ProtocolID) {
	t.Helper()
	if protocol == contract.ProtocolGoogleGenerateContent {
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if payload["model"] != want {
		t.Fatalf("model = %#v, want %q body=%s", payload["model"], want, body)
	}
}

func assertJSONContains(t *testing.T, body []byte, want string) {
	t.Helper()
	if !strings.Contains(string(body), want) {
		t.Fatalf("body missing %q: %s", want, body)
	}
}

func assertTerminalMarker(t *testing.T, protocol contract.ProtocolID, body []byte) {
	t.Helper()
	text := string(body)
	switch protocol {
	case contract.ProtocolOpenAIChat:
		if !strings.Contains(text, `"finish_reason"`) {
			t.Fatalf("missing chat finish_reason: %s", text)
		}
	case contract.ProtocolOpenAIResponses:
		if !strings.Contains(text, `"completed"`) && !strings.Contains(text, `"status"`) {
			t.Fatalf("missing responses completion marker: %s", text)
		}
	case contract.ProtocolAnthropicMessages:
		if !strings.Contains(text, `"stop_reason"`) && !strings.Contains(text, `"end_turn"`) {
			t.Fatalf("missing claude terminal marker: %s", text)
		}
	case contract.ProtocolGoogleGenerateContent:
		if !strings.Contains(text, `"finishReason"`) && !strings.Contains(text, "STOP") {
			t.Fatalf("missing gemini finishReason: %s", text)
		}
	}
}

func assertBillingUsageSurvives(t *testing.T, body []byte) {
	t.Helper()
	if !strings.Contains(string(body), "billing_usage") &&
		!strings.Contains(string(body), "usageMetadata") &&
		!strings.Contains(string(body), "usage") {
		t.Fatalf("usage metadata missing after conversion: %s", body)
	}
}
