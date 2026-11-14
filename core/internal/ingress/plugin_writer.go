package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/convo"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
)

// maxPluginRewriteBytes bounds a whole response the plugins rewrite; a
// larger one goes to the client as it came.
const maxPluginRewriteBytes = 8 << 20

// pluginResponseWriter runs the response hooks of plugins over one
// attempt's response, in the client's protocol and before the privacy
// restore, so a plugin sees what the upstream saw: rewriteResponse over a
// whole JSON response, onStreamEvent over each event of a stream. It also
// keeps the reply's visible text for onResponse. Error responses, and
// responses of another type, pass as they are.
type pluginResponseWriter struct {
	http.ResponseWriter
	ctx         context.Context
	extensions  *pluginhost.Extensions
	streaming   bool
	rewrite     bool
	stream      *pluginhost.StreamRewrite
	status      int
	wroteHeader bool
	passthrough bool
	buffer      bytes.Buffer
	carry       []byte
	// text collects the reply's visible text for onResponse; nil when no
	// plugin reads it.
	text   *replyText
	parser convo.ResponseParser
}

func newPluginResponseWriter(ctx context.Context, writer http.ResponseWriter, extensions *pluginhost.Extensions, protocol contract.ProtocolID, streaming bool) *pluginResponseWriter {
	plugins := &pluginResponseWriter{ResponseWriter: writer, ctx: ctx, extensions: extensions, streaming: streaming, rewrite: extensions.RewritesResponses(streaming)}
	if extensions.Observes() {
		if convoProto, ok := convoProtocol(protocol); ok {
			if adapter, ok := convo.DefaultRegistry().Lookup(convoProto); ok {
				plugins.text = &replyText{}
				plugins.parser = adapter.NewResponseParser(streaming)
			}
		}
	}
	return plugins
}

func (writer *pluginResponseWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

func (writer *pluginResponseWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.wroteHeader = true
	writer.status = status
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(writer.Header().Get("Content-Type"), ";")[0]))
	encoding := strings.ToLower(strings.TrimSpace(writer.Header().Get("Content-Encoding")))
	readable := status < http.StatusBadRequest && (encoding == "" || encoding == "identity")
	switch {
	case !readable:
		writer.passthrough = true
	case writer.streaming && mediaType == "text/event-stream":
		if writer.rewrite {
			writer.stream = writer.extensions.Stream()
		}
		writer.Header().Del("Content-Length")
	case !writer.streaming && (mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")):
		if writer.rewrite {
			// The body may change: the header waits for it.
			return
		}
	default:
		writer.passthrough = true
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *pluginResponseWriter) Write(chunk []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	if writer.passthrough {
		return writer.ResponseWriter.Write(chunk)
	}
	if writer.streaming {
		writer.carry = append(writer.carry, chunk...)
		for {
			event, rest, ok := cutSSEEvent(writer.carry)
			if !ok {
				break
			}
			writer.carry = rest
			if err := writer.event(event); err != nil {
				return 0, err
			}
		}
		return len(chunk), nil
	}
	if writer.buffer.Len()+len(chunk) > maxPluginRewriteBytes {
		// Too large to rewrite: what came so far goes on, then the rest.
		writer.passthrough = true
		if writer.rewrite {
			writer.ResponseWriter.WriteHeader(writer.status)
		}
		if _, err := writer.ResponseWriter.Write(writer.buffer.Bytes()); err != nil {
			return 0, err
		}
		writer.buffer.Reset()
		return writer.ResponseWriter.Write(chunk)
	}
	writer.buffer.Write(chunk)
	if !writer.rewrite {
		// Kept for onResponse only; the client gets it as it comes.
		return writer.ResponseWriter.Write(chunk)
	}
	return len(chunk), nil
}

// event runs one raw event of the stream through the stream hooks and
// writes what they return.
func (writer *pluginResponseWriter) event(raw []byte) error {
	event, comment := parseSSEEvent(raw)
	if comment {
		_, err := writer.ResponseWriter.Write(raw)
		return err
	}
	if writer.stream == nil {
		// Read for onResponse only: the event goes on byte for byte.
		writer.observe(event.Data)
		_, err := writer.ResponseWriter.Write(raw)
		return err
	}
	for _, sent := range writer.stream.Event(writer.ctx, event) {
		writer.observe(sent.Data)
		if _, err := writer.ResponseWriter.Write(formatSSEEvent(sent)); err != nil {
			return err
		}
	}
	return nil
}

// observe feeds an event's data, or a whole body, to the reply's text.
func (writer *pluginResponseWriter) observe(data []byte) {
	if writer.text == nil || len(data) == 0 || data[0] != '{' {
		return
	}
	document := map[string]json.RawMessage{}
	if json.Unmarshal(data, &document) == nil {
		writer.parser.Observe(document, writer.text)
	}
}

func (writer *pluginResponseWriter) Flush() { _ = writer.FlushError() }

func (writer *pluginResponseWriter) FlushError() error {
	if !writer.passthrough && !writer.streaming && writer.rewrite {
		return nil
	}
	return http.NewResponseController(writer.ResponseWriter).Flush()
}

// Finish ends the response: a whole one goes out rewritten, a stream gets
// what its last incomplete event and the end hooks leave.
func (writer *pluginResponseWriter) Finish() error {
	if writer.passthrough || !writer.wroteHeader {
		return nil
	}
	if writer.streaming {
		if len(bytes.TrimSpace(writer.carry)) > 0 {
			if err := writer.event(writer.carry); err != nil {
				return err
			}
		}
		writer.carry = nil
		if writer.stream != nil {
			for _, sent := range writer.stream.End(writer.ctx) {
				writer.observe(sent.Data)
				if _, err := writer.ResponseWriter.Write(formatSSEEvent(sent)); err != nil {
					return err
				}
			}
			writer.stream.Close()
			writer.stream = nil
		}
		return http.NewResponseController(writer.ResponseWriter).Flush()
	}
	body := writer.buffer.Bytes()
	if writer.rewrite {
		body = writer.extensions.RewriteResponse(writer.ctx, writer.status, body)
		writer.Header().Set("Content-Length", strconv.Itoa(len(body)))
		writer.ResponseWriter.WriteHeader(writer.status)
		if _, err := writer.ResponseWriter.Write(body); err != nil {
			return err
		}
	}
	writer.observe(body)
	return nil
}

// Text returns the reply's visible text so far.
func (writer *pluginResponseWriter) Text() string {
	if writer == nil || writer.text == nil {
		return ""
	}
	return writer.text.String()
}

// cutSSEEvent cuts the first complete event, up to its blank line, off
// data.
func cutSSEEvent(data []byte) (event, rest []byte, ok bool) {
	for index := 0; index < len(data); index++ {
		if data[index] != '\n' {
			continue
		}
		next := index + 1
		if next < len(data) && data[next] == '\r' {
			next++
		}
		if next < len(data) && data[next] == '\n' {
			return data[:next+1], data[next+1:], true
		}
	}
	return nil, data, false
}

// parseSSEEvent reads one event: its name, id and data lines. comment is
// set for a block of comments alone, which goes on untouched.
func parseSSEEvent(raw []byte) (event pluginhost.StreamEvent, comment bool) {
	var data [][]byte
	fields := 0
	for _, line := range bytes.Split(bytes.TrimRight(raw, "\r\n"), []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) == 0 || line[0] == ':' {
			continue
		}
		name, value, _ := bytes.Cut(line, []byte(":"))
		value = bytes.TrimPrefix(value, []byte(" "))
		fields++
		switch string(name) {
		case "event":
			event.Event = string(value)
		case "id":
			event.ID = string(value)
		case "data":
			data = append(data, value)
		}
	}
	event.Data = bytes.Join(data, []byte("\n"))
	return event, fields == 0
}

// formatSSEEvent writes an event back in the stream's form.
func formatSSEEvent(event pluginhost.StreamEvent) []byte {
	var out bytes.Buffer
	if event.Event != "" {
		out.WriteString("event: " + event.Event + "\n")
	}
	if event.ID != "" {
		out.WriteString("id: " + event.ID + "\n")
	}
	for _, line := range bytes.Split(event.Data, []byte("\n")) {
		out.WriteString("data: ")
		out.Write(line)
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	return out.Bytes()
}

// replyText keeps the visible text of a reply, as far as maxReplyText.
type replyText struct {
	builder strings.Builder
}

const maxReplyText = 64 << 10

func (text *replyText) OutputID(string) {}

func (text *replyText) EchoID(string) {}

func (text *replyText) AssistantText(fragment string) {
	if text.builder.Len()+len(fragment) <= maxReplyText {
		text.builder.WriteString(fragment)
	}
}

func (text *replyText) ResetAssistantText() {
	if text.builder.Len() > 0 && text.builder.Len() < maxReplyText {
		text.builder.WriteString("\n")
	}
}

func (text *replyText) String() string { return text.builder.String() }
