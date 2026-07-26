package relaykitbridge

import (
	"context"
	"errors"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestNoopEngineAdvertisesNoEdges(t *testing.T) {
	engine := NoopEngine{}
	if engine.Version() != "" {
		t.Fatalf("version = %q, want empty", engine.Version())
	}
	edges := engine.Edges()
	if edges == nil || len(edges) != 0 {
		t.Fatalf("edges = %#v, want non-nil empty slice", edges)
	}
}

func TestNoopEngineNeverConverts(t *testing.T) {
	engine := NoopEngine{}
	request := ConvertRequestInput{
		From: contract.ProtocolOpenAIResponses, To: contract.ProtocolOpenAIChat,
		ContentType: "application/json", Body: []byte(`{"model":"example"}`),
	}
	if output, err := engine.ConvertRequest(context.Background(), request); !errors.Is(err, ErrConversionUnavailable) || output.Body != nil {
		t.Fatalf("ConvertRequest output=%#v error=%v", output, err)
	}
	response := ConvertResponseInput{
		From: contract.ProtocolOpenAIChat, To: contract.ProtocolOpenAIResponses,
		StatusCode: 200, ContentType: "application/json", Body: []byte(`{}`),
	}
	if output, err := engine.ConvertResponse(context.Background(), response); !errors.Is(err, ErrConversionUnavailable) || output.Body != nil {
		t.Fatalf("ConvertResponse output=%#v error=%v", output, err)
	}
	if stream, err := engine.NewResponseStream(context.Background(), StreamOptions{
		From: contract.ProtocolOpenAIChat, To: contract.ProtocolOpenAIResponses,
	}); !errors.Is(err, ErrConversionUnavailable) || stream != nil {
		t.Fatalf("NewResponseStream stream=%#v error=%v", stream, err)
	}
}
