package ingress

import (
	"bytes"
	"context"
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
	streaming                  bool
	status                     int
	body                       bytes.Buffer
	stream                     relaykitbridge.ResponseStream
	carry                      []byte
	passthrough                bool
}

func newRelayKitResponseWriter(
	writer http.ResponseWriter, engine relaykitbridge.ConversionEngine, plan contract.ExecutionPlan,
	publicModel, upstreamModel string,
) (*relayKitResponseWriter, error) {
	result := &relayKitResponseWriter{
		ResponseWriter: writer, engine: engine, plan: plan, publicModel: publicModel,
		upstreamModel: upstreamModel, streaming: plan.Streaming,
	}
	if plan.Streaming {
		stream, err := engine.NewResponseStream(context.Background(), relaykitbridge.StreamOptions{
			From: plan.UpstreamProtocol, To: plan.InputProtocol, PublicModel: publicModel,
			UpstreamModel: upstreamModel,
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

func (writer *relayKitResponseWriter) Flush()                      {}
func (writer *relayKitResponseWriter) FlushError() error           { return nil }
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
		})
		if err != nil {
			return err
		}
		writer.Header().Set("Content-Type", output.ContentType)
		writer.ResponseWriter.WriteHeader(output.StatusCode)
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
	return writer.writeEvents(events)
}

func (writer *relayKitResponseWriter) streamClose() error {
	if writer.stream != nil {
		return writer.stream.Close()
	}
	return nil
}

func (writer *relayKitResponseWriter) convertStreamFrames(final bool) error {
	normalized := bytes.ReplaceAll(writer.carry, []byte("\r\n"), []byte("\n"))
	frames := bytes.Split(normalized, []byte("\n\n"))
	if !final {
		writer.carry = append(writer.carry[:0], frames[len(frames)-1]...)
		frames = frames[:len(frames)-1]
	} else {
		writer.carry = nil
	}
	for _, frame := range frames {
		if len(bytes.TrimSpace(frame)) == 0 {
			continue
		}
		eventType, data := parseSSEFrame(frame)
		if data == nil {
			continue
		}
		events, err := writer.stream.Convert(context.Background(), relaykitbridge.ResponseEvent{Type: eventType, Data: data})
		if err != nil {
			return err
		}
		if err := writer.writeEvents(events); err != nil {
			return err
		}
	}
	return nil
}

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
	return eventType, bytes.Join(data, []byte("\n"))
}

func (writer *relayKitResponseWriter) writeEvents(events []relaykitbridge.ResponseEvent) error {
	for _, event := range events {
		wire := formatRelayKitEvent(writer.plan.InputProtocol, event)
		if wire == nil {
			continue
		}
		if writer.status != 0 {
			writer.ResponseWriter.WriteHeader(writer.status)
			writer.status = 0 // destination has committed; prevent a second header.
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
