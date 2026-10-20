package ingress

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestRelayKitStreamFlushesConvertedOutput(t *testing.T) {
	destination := httptest.NewRecorder()
	writer, err := newRelayKitResponseWriter(destination, relaykitbridge.NewEngine(), contract.ExecutionPlan{
		Type: contract.PlanTypeRelayKit, Streaming: true,
		InputProtocol: contract.ProtocolOpenAIResponses, UpstreamProtocol: contract.ProtocolOpenAIChat,
	}, "public-model", "upstream-model", relaykitbridge.ConversionState{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.streamClose() })
	frame := `data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"upstream-model","choices":[{"index":0,"delta":{"content":"o"},"finish_reason":null}]}` + "\n\n"
	response := &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(frame)),
	}
	if err := transport.WriteResponse(newResponseStartWriter(writer, func(int) {}), response); err != nil {
		t.Fatal(err)
	}
	// Finish has not run: a client must already be able to receive the delta.
	if destination.Body.Len() == 0 || !destination.Flushed {
		t.Fatalf("output bytes=%d flushed=%t", destination.Body.Len(), destination.Flushed)
	}
}

func TestRelayKitFlushWaitsForConvertibleContent(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", streaming), func(t *testing.T) {
			destination := newCommitTrackingWriter(httptest.NewRecorder())
			writer := &relayKitResponseWriter{ResponseWriter: destination, streaming: streaming}
			writer.WriteHeader(http.StatusOK)
			if err := writer.FlushError(); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write([]byte("data: {")); err != nil {
				t.Fatal(err)
			}
			if err := writer.FlushError(); err != nil {
				t.Fatal(err)
			}
			if destination.Committed() {
				t.Fatal("headers or incomplete content committed the downstream response")
			}
		})
	}

	destination := httptest.NewRecorder()
	writer := &relayKitResponseWriter{ResponseWriter: destination, streaming: true}
	writer.WriteHeader(http.StatusTooManyRequests)
	if err := writer.FlushError(); err != nil {
		t.Fatal(err)
	}
	if !destination.Flushed || destination.Code != http.StatusTooManyRequests {
		t.Fatalf("passthrough status=%d flushed=%t", destination.Code, destination.Flushed)
	}
}

func TestRelayKitStreamFramesAcrossChunkBoundaries(t *testing.T) {
	input := ": keepalive\r\n\r\nevent: first\r\ndata: one\r\ndata: two\r\n\r\n" +
		"data: three\n\n" + "event: final\ndata: four"
	want := []relaykitbridge.ResponseEvent{
		{Type: "first", Data: []byte("one\ntwo")},
		{Type: "data", Data: []byte("three")},
		{Type: "final", Data: []byte("four")},
	}
	for chunkSize := 1; chunkSize <= len(input); chunkSize++ {
		t.Run(fmt.Sprintf("chunk=%d", chunkSize), func(t *testing.T) {
			stream := &frameRecordingStream{}
			writer := &relayKitResponseWriter{
				ResponseWriter: httptest.NewRecorder(), streaming: true, stream: stream,
			}
			for start := 0; start < len(input); start += chunkSize {
				if _, err := writer.Write([]byte(input[start:min(start+chunkSize, len(input))])); err != nil {
					t.Fatal(err)
				}
			}
			if len(stream.events) != 2 {
				t.Fatalf("events before EOF=%d, want 2", len(stream.events))
			}
			if err := writer.Finish(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(stream.events, want) || !stream.finalized || !stream.closed {
				t.Fatalf("events=%v finalized=%t closed=%t", stream.events, stream.finalized, stream.closed)
			}
		})
	}
}

func TestRelayKitConversionFailureDoesNotCommit(t *testing.T) {
	want := errors.New("invalid event")
	destination := newCommitTrackingWriter(httptest.NewRecorder())
	writer := &relayKitResponseWriter{
		ResponseWriter: destination, streaming: true, stream: &frameRecordingStream{convertErr: want},
	}
	if _, err := writer.Write([]byte("data: invalid\n\n")); !errors.Is(err, want) {
		t.Fatalf("Write error=%v", err)
	}
	if err := writer.FlushError(); err != nil {
		t.Fatal(err)
	}
	if destination.Committed() {
		t.Fatal("failed conversion committed downstream")
	}
}

func TestRelayKitFinalizeFlushesAndPropagatesFlushErrors(t *testing.T) {
	for _, flushErr := range []error{nil, errors.New("client disconnected")} {
		t.Run(fmt.Sprint(flushErr), func(t *testing.T) {
			destination := &relayFlushWriter{ResponseRecorder: httptest.NewRecorder(), err: flushErr}
			writer := &relayKitResponseWriter{
				ResponseWriter: destination, streaming: true,
				stream: &frameRecordingStream{finalEvents: []relaykitbridge.ResponseEvent{{Type: "done"}}},
			}
			if err := writer.Finish(); !errors.Is(err, flushErr) {
				t.Fatalf("Finish error=%v, want %v", err, flushErr)
			}
			if !destination.Flushed || destination.Body.String() != "data: [DONE]\n\n" {
				t.Fatalf("final output=%q flushed=%t", destination.Body.String(), destination.Flushed)
			}
		})
	}
}

type relayFlushWriter struct {
	*httptest.ResponseRecorder
	err error
}

func (writer *relayFlushWriter) FlushError() error {
	writer.Flush()
	return writer.err
}

func TestRelayKitLargeFramePreservesPayload(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 4<<20)
	stream := &frameRecordingStream{}
	writer := &relayKitResponseWriter{
		ResponseWriter: httptest.NewRecorder(), streaming: true, stream: stream,
	}
	if _, err := writer.Write([]byte("data: ")); err != nil {
		t.Fatal(err)
	}
	for start := 0; start < len(payload); start += 32 << 10 {
		if _, err := writer.Write(payload[start:min(start+32<<10, len(payload))]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Write([]byte("\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	if len(stream.events) != 1 || !bytes.Equal(stream.events[0].Data, payload) {
		t.Fatal("large event was truncated or changed")
	}
}

type frameRecordingStream struct {
	events      []relaykitbridge.ResponseEvent
	finalEvents []relaykitbridge.ResponseEvent
	convertErr  error
	finalized   bool
	closed      bool
}

func (stream *frameRecordingStream) Convert(_ context.Context, event relaykitbridge.ResponseEvent) ([]relaykitbridge.ResponseEvent, error) {
	stream.events = append(stream.events, event)
	return nil, stream.convertErr
}

func (stream *frameRecordingStream) Finalize(context.Context) ([]relaykitbridge.ResponseEvent, error) {
	stream.finalized = true
	return stream.finalEvents, nil
}

func (*frameRecordingStream) Diagnostics() []relaykitbridge.ConversionDiagnostic { return nil }
func (stream *frameRecordingStream) Close() error {
	stream.closed = true
	return nil
}

func BenchmarkRelayKitPartialFrame(b *testing.B) {
	for _, size := range []int{256 << 10, 1 << 20, 4 << 20} {
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			chunk := bytes.Repeat([]byte("x"), 32<<10)
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for range b.N {
				writer := &relayKitResponseWriter{streaming: true, status: http.StatusOK}
				for offset := 0; offset < size; offset += len(chunk) {
					if _, err := writer.Write(chunk); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
