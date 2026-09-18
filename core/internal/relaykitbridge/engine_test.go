package relaykitbridge

import (
	"context"
	"encoding/json"
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
