package ingress

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
)

// relayKitResponseWriter gates a converted response until it is safe to commit.
// It intentionally treats a non-2xx upstream response as protocol-native
// passthrough so existing endpoint health and retry behavior is retained.
type relayKitResponseWriter struct {
	http.ResponseWriter
	engine                     relaykitbridge.ConversionEngine
	plan                       contract.ExecutionPlan
	publicModel, upstreamModel string
	// state is what this attempt's request conversion recorded; the response
	// conversion needs it to restore client-only tool call shapes.
	state       relaykitbridge.ConversionState
	diagnostics []relaykitbridge.ConversionDiagnostic
	streaming   bool
	status      int
	body        bytes.Buffer
	stream      relaykitbridge.ResponseStream
	carry       []byte
	carryScan   int
	lineStart   int
	committed   bool
	passthrough bool
}

func newRelayKitResponseWriter(
	writer http.ResponseWriter, engine relaykitbridge.ConversionEngine, plan contract.ExecutionPlan,
	publicModel, upstreamModel string, state relaykitbridge.ConversionState,
) (*relayKitResponseWriter, error) {
	result := &relayKitResponseWriter{
		ResponseWriter: writer, engine: engine, plan: plan, publicModel: publicModel,
		upstreamModel: upstreamModel, state: state, streaming: plan.Streaming,
	}
	if plan.Streaming {
		stream, err := engine.NewResponseStream(context.Background(), relaykitbridge.StreamOptions{
			From: plan.UpstreamProtocol, To: plan.InputProtocol, PublicModel: publicModel,
			UpstreamModel: upstreamModel, State: state,
		})
		if err != nil {
			return nil, err
		}
		result.stream = stream
	}
	return result, nil
}

func (writer *relayKitResponseWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.passthrough = status < 200 || status >= 300
	if writer.passthrough {
		writer.ResponseWriter.WriteHeader(status)
	}
}

func (writer *relayKitResponseWriter) Write(value []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	if writer.passthrough {
		return writer.ResponseWriter.Write(value)
	}
	if !writer.streaming {
		return writer.body.Write(value)
	}
	writer.carry = append(writer.carry, value...)
	if err := writer.convertStreamFrames(false); err != nil {
		return 0, err
	}
	return len(value), nil
}

func (writer *relayKitResponseWriter) Flush() { _ = writer.FlushError() }

func (writer *relayKitResponseWriter) FlushError() error {
	// Upstream headers and incomplete events must not commit a response that
	// can still fail conversion and be retried on another route.
	if !writer.passthrough && !writer.committed {
		return nil
	}
	return http.NewResponseController(writer.ResponseWriter).Flush()
}

func (writer *relayKitResponseWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

func (writer *relayKitResponseWriter) Finish() error {
	if writer.passthrough {
		return nil
	}
	defer func() { _ = writer.streamClose() }()
	if !writer.streaming {
		output, err := writer.engine.ConvertResponse(context.Background(), relaykitbridge.ConvertResponseInput{
			From: writer.plan.UpstreamProtocol, To: writer.plan.InputProtocol, StatusCode: writer.status,
			ContentType: writer.Header().Get("Content-Type"), Body: writer.body.Bytes(), PublicModel: writer.publicModel,
			UpstreamModel: writer.upstreamModel, State: writer.state,
		})
		if err != nil {
			return err
		}
		writer.diagnostics = output.Diagnostics
		writer.Header().Set("Content-Type", output.ContentType)
		writer.ResponseWriter.WriteHeader(output.StatusCode)
		writer.committed = true
		_, err = writer.ResponseWriter.Write(output.Body)
		return err
	}
	if err := writer.convertStreamFrames(true); err != nil {
		return err
	}
	events, err := writer.stream.Finalize(context.Background())
	if err != nil {
		return err
	}
	if err := writer.writeEvents(events); err != nil {
		return err
	}
	if err := writer.FlushError(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

// responseDiagnostics reports what the response conversion dropped or
// rewrote so far; a passthrough error response is never converted.
func (writer *relayKitResponseWriter) responseDiagnostics() []relaykitbridge.ConversionDiagnostic {
	if writer.passthrough {
		return nil
	}
	if writer.stream != nil {
		return writer.stream.Diagnostics()
	}
	return writer.diagnostics
}

func (writer *relayKitResponseWriter) streamClose() error {
	if writer.stream != nil {
		return writer.stream.Close()
	}
	return nil
}

func (writer *relayKitResponseWriter) convertStreamFrames(final bool) error {
	// Scan each incoming byte once. Retain the unfinished line's start so a
	// CRLF split between reads still identifies a blank line correctly.
	consumed := 0
	for writer.carryScan < len(writer.carry) {
		next := bytes.IndexByte(writer.carry[writer.carryScan:], '\n')
		if next < 0 {
			writer.carryScan = len(writer.carry)
			break
		}
		newline := writer.carryScan + next
		blank := newline == writer.lineStart ||
			(newline == writer.lineStart+1 && writer.carry[writer.lineStart] == '\r')
		if blank {
			if err := writer.convertStreamFrame(writer.carry[consumed:writer.lineStart]); err != nil {
				return err
			}
			consumed = newline + 1
		}
		writer.lineStart = newline + 1
		writer.carryScan = newline + 1
	}
	if consumed > 0 {
		if consumed == len(writer.carry) {
			writer.carry = writer.carry[:0]
		} else {
			writer.carry = writer.carry[consumed:]
		}
		writer.carryScan -= consumed
		writer.lineStart -= consumed
	}
	if final {
		if err := writer.convertStreamFrame(writer.carry); err != nil {
			return err
		}
		writer.carry = nil
		writer.carryScan = 0
		writer.lineStart = 0
	}
	return nil
}

func (writer *relayKitResponseWriter) convertStreamFrame(frame []byte) error {
	if len(bytes.TrimSpace(frame)) == 0 {
		return nil
	}
	eventType, data := parseSSEFrame(frame)
	if data == nil {
		return nil
	}
	events, err := writer.stream.Convert(context.Background(), relaykitbridge.ResponseEvent{Type: eventType, Data: data})
	if err != nil {
		return err
	}
	return writer.writeEvents(events)
}

// parseSSEFrame maps one upstream SSE frame onto the engine-neutral event
// boundary. OpenAI Chat's "data: [DONE]" terminator has no DTO; it is reported
// as the "done" event so the bridge can hand terminal conversion to Finalize.
func parseSSEFrame(frame []byte) (string, []byte) {
	eventType := "data"
	var data [][]byte
	for _, line := range bytes.Split(frame, []byte("\n")) {
		switch {
		case bytes.HasPrefix(line, []byte("event:")):
			eventType = strings.TrimSpace(string(line[len("event:"):]))
		case bytes.HasPrefix(line, []byte("data:")):
			data = append(data, bytes.TrimSpace(line[len("data:"):]))
		}
	}
	if len(data) == 0 {
		return eventType, nil
	}
	payload := bytes.Join(data, []byte("\n"))
	if bytes.Equal(payload, []byte("[DONE]")) {
		return "done", payload
	}
	return eventType, payload
}

func (writer *relayKitResponseWriter) writeEvents(events []relaykitbridge.ResponseEvent) error {
	for _, event := range events {
		wire := formatRelayKitEvent(writer.plan.InputProtocol, event)
		if wire == nil {
			continue
		}
		if !writer.committed {
			if writer.status == 0 {
				writer.WriteHeader(http.StatusOK)
			}
			writer.ResponseWriter.WriteHeader(writer.status)
			writer.committed = true
		}
		if _, err := writer.ResponseWriter.Write(wire); err != nil {
			return err
		}
	}
	return nil
}

func formatRelayKitEvent(protocol contract.ProtocolID, event relaykitbridge.ResponseEvent) []byte {
	if event.Type == "done" {
		return []byte("data: [DONE]\n\n")
	}
	switch protocol {
	case contract.ProtocolAnthropicMessages, contract.ProtocolOpenAIResponses:
		if event.Type == "" {
			return []byte("data: " + string(event.Data) + "\n\n")
		}
		return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event.Type, event.Data))
	default:
		return []byte("data: " + string(event.Data) + "\n\n")
	}
}
