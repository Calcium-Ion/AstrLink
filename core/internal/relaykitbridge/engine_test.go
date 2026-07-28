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
