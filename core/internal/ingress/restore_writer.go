package ingress

import (
	"bytes"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

var errRestoreAborted = errors.New("response restore aborted")

const maxRestoreBufferBytes = maxMetadataBytes

type responseRestoreMode uint8

const (
	restoreModeBuffered responseRestoreMode = iota
	restoreModeSSE
	restoreModeJSONStream
	restoreModeText
)

type jsonRestoreStreamKind uint8

const (
	jsonRestoreUnknown jsonRestoreStreamKind = iota
	jsonRestoreArray
	jsonRestoreSequence
	jsonRestoreDone
)

type restoringResponseWriter struct {
	http.ResponseWriter
	streaming     bool
	status        int
	wroteHeader   bool
	passthrough   bool
	buffering     bool
	failed        bool
	mode          responseRestoreMode
	buffer        bytes.Buffer
	streamCarry   []byte
	streamRaw     bool
	jsonKind      jsonRestoreStreamKind
	textPending   string
	engine        *visibleRestoreEngine
	textRestored  int
	textFallbacks int
}

func newRestoringResponseWriter(
	writer http.ResponseWriter,
	redactions []privacy.Redaction,
	streaming bool,
	protocol contract.ProtocolID,
) *restoringResponseWriter {
	copied := append([]privacy.Redaction(nil), redactions...)
	return &restoringResponseWriter{
		ResponseWriter: writer,
		streaming:      streaming,
		engine:         newVisibleRestoreEngine(protocol, copied),
	}
}

func (writer *restoringResponseWriter) WriteHeader(status int) {
	if writer.wroteHeader || writer.failed {
		return
	}
	encoding := strings.ToLower(strings.TrimSpace(writer.Header().Get("Content-Encoding")))
	if encoding != "" && encoding != "identity" {
		writer.writeRestoreFailure(
			http.StatusBadGateway,
			"upstream_content_encoding",
			"encoded upstream responses cannot be restored safely",
		)
		return
	}
	contentType := writer.Header().Get("Content-Type")
	if !privacy.ShouldRestoreContentType(contentType) {
		writer.passthrough = true
		writer.wroteHeader = true
		writer.ResponseWriter.WriteHeader(status)
		return
	}

	writer.status = status
	writer.wroteHeader = true
	if !writer.streaming {
		writer.mode = restoreModeBuffered
		writer.buffering = true
		return
	}

	writer.Header().Del("Content-Length")
	writer.mode = streamingRestoreMode(contentType)
	writer.ResponseWriter.WriteHeader(status)
}

func streamingRestoreMode(contentType string) responseRestoreMode {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch mediaType {
	case "text/event-stream":
		return restoreModeSSE
	case "text/plain":
		return restoreModeText
	default:
		return restoreModeJSONStream
	}
}

func (writer *restoringResponseWriter) Write(chunk []byte) (int, error) {
	if writer.failed {
		return 0, errRestoreAborted
	}
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
		if writer.failed {
			return 0, errRestoreAborted
		}
	}
	if writer.passthrough {
		return writer.ResponseWriter.Write(chunk)
	}
	if writer.streaming {
		var output []byte
		switch writer.mode {
		case restoreModeSSE:
			output = writer.pushSSE(chunk)
		case restoreModeText:
			output = writer.pushText(chunk)
		default:
			output = writer.pushJSONStream(chunk, false)
		}
		if err := writer.writeStreamingOutput(output); err != nil {
			return 0, err
		}
		return len(chunk), nil
	}

	if writer.buffer.Len()+len(chunk) > maxRestoreBufferBytes {
		writer.engine.fallbacks++
		writer.buffering = false
		writer.passthrough = true
		writer.ResponseWriter.WriteHeader(writer.status)
		if writer.buffer.Len() > 0 {
			if _, err := writer.ResponseWriter.Write(writer.buffer.Bytes()); err != nil {
				return 0, err
			}
		}
		writer.buffer.Reset()
		if _, err := writer.ResponseWriter.Write(chunk); err != nil {
			return 0, err
		}
		return len(chunk), nil
	}
	_, _ = writer.buffer.Write(chunk)
	return len(chunk), nil
}

func (writer *restoringResponseWriter) Flush() {
	_ = writer.FlushError()
}

func (writer *restoringResponseWriter) FlushError() error {
	if writer.failed || writer.passthrough {
		return http.NewResponseController(writer.ResponseWriter).Flush()
	}
	if writer.buffering && !writer.streaming {
		return nil
	}
	return http.NewResponseController(writer.ResponseWriter).Flush()
}

func (writer *restoringResponseWriter) Finish() error {
	if writer.failed {
		return nil
	}
	if writer.passthrough {
		return nil
	}
	if writer.streaming {
		var output []byte
		switch writer.mode {
		case restoreModeSSE:
			output = writer.finishSSE()
		case restoreModeText:
			output = writer.finishText()
		default:
			output = writer.pushJSONStream(nil, true)
		}
		return writer.writeStreamingOutput(output)
	}
	if !writer.buffering {
		return nil
	}

	original := append([]byte(nil), writer.buffer.Bytes()...)
	contentType := strings.ToLower(strings.TrimSpace(
		strings.Split(writer.Header().Get("Content-Type"), ";")[0],
	))
	var restored []byte
	switch contentType {
	case "text/plain":
		value, count, pending := restoreVisibleText(string(original), writer.engine.replacements)
		restored = []byte(value)
		writer.textRestored += count
		if pending {
			writer.textFallbacks++
		}
	case "text/event-stream":
		// A non-streaming request receiving SSE is an upstream shape mismatch.
		// Preserve it rather than applying raw substitutions to unknown fields.
		restored = original
		writer.engine.fallbacks++
	default:
		restored = writer.engine.push(newJSONRestoreFrame(nil, original))
		if pending := writer.engine.finish(); len(pending) > 0 {
			restored = append(restored, pending...)
		}
	}
	writer.Header().Set("Content-Length", strconv.Itoa(len(restored)))
	writer.ResponseWriter.WriteHeader(writer.status)
	if len(restored) == 0 {
		return nil
	}
	_, err := writer.ResponseWriter.Write(restored)
	return err
}

func (writer *restoringResponseWriter) pushSSE(chunk []byte) []byte {
	if writer.streamRaw {
		return chunk
	}
	writer.streamCarry = append(writer.streamCarry, chunk...)
	var out bytes.Buffer
	for {
		end := nextSSEFrameEnd(writer.streamCarry)
		if end == 0 {
			break
		}
		frame := append([]byte(nil), writer.streamCarry[:end]...)
		writer.streamCarry = writer.streamCarry[end:]
		data := parseSSEData(frame)
		switch {
		case data == nil:
			_, _ = out.Write(writer.engine.push(newRawRestoreFrame(frame)))
		case bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")):
			_, _ = out.Write(writer.engine.terminal(frame))
		default:
			_, _ = out.Write(writer.engine.push(newSSERestoreFrame(frame, data)))
		}
	}
	if len(writer.streamCarry) > maxQueuedRestoreBytes {
		_, _ = out.Write(writer.engine.abort())
		_, _ = out.Write(writer.streamCarry)
		writer.streamCarry = nil
		writer.engine.fallbacks++
		writer.streamRaw = true
	}
	return out.Bytes()
}

func (writer *restoringResponseWriter) finishSSE() []byte {
	var out bytes.Buffer
	_, _ = out.Write(writer.engine.finish())
	if len(writer.streamCarry) > 0 {
		_, _ = out.Write(writer.streamCarry)
		writer.streamCarry = nil
		writer.engine.fallbacks++
	}
	return out.Bytes()
}

func (writer *restoringResponseWriter) pushText(chunk []byte) []byte {
	combined := writer.textPending + string(chunk)
	writer.textPending = ""
	hold := trailingRestorePrefix(combined, writer.engine.replacements)
	stable := combined
	if hold > 0 {
		stable = combined[:len(combined)-hold]
		writer.textPending = combined[len(combined)-hold:]
	}
	restored, count, _ := restoreVisibleText(stable, writer.engine.replacements)
	writer.textRestored += count
	return []byte(restored)
}

func (writer *restoringResponseWriter) finishText() []byte {
	if writer.textPending == "" {
		return nil
	}
	pending := []byte(writer.textPending)
	writer.textPending = ""
	writer.textFallbacks++
	return pending
}

func (writer *restoringResponseWriter) pushJSONStream(chunk []byte, final bool) []byte {
	if writer.streamRaw {
		return chunk
	}
	writer.streamCarry = append(writer.streamCarry, chunk...)
	var out bytes.Buffer

	for {
		if writer.jsonKind == jsonRestoreDone {
			_, _ = out.Write(writer.streamCarry)
			writer.streamCarry = nil
			break
		}
		if writer.jsonKind == jsonRestoreUnknown {
			start := firstNonSpace(writer.streamCarry)
			if start < 0 {
				break
			}
			switch writer.streamCarry[start] {
			case '[':
				_, _ = out.Write(writer.streamCarry[:start+1])
				writer.streamCarry = writer.streamCarry[start+1:]
				writer.jsonKind = jsonRestoreArray
				continue
			case '{':
				writer.jsonKind = jsonRestoreSequence
			default:
				return writer.fallbackJSONStream(out.Bytes())
			}
		}

		start := firstNonSpace(writer.streamCarry)
		if start < 0 {
			break
		}
		if writer.jsonKind == jsonRestoreArray {
			if writer.streamCarry[start] == ']' {
				end := start + 1
				_, _ = out.Write(writer.engine.terminal(writer.streamCarry[:end]))
				writer.streamCarry = writer.streamCarry[end:]
				writer.jsonKind = jsonRestoreDone
				continue
			}
			if writer.streamCarry[start] == ',' {
				next := firstNonSpace(writer.streamCarry[start+1:])
				if next < 0 {
					break
				}
				start += 1 + next
				if writer.streamCarry[start] == ']' {
					return writer.fallbackJSONStream(out.Bytes())
				}
			}
		}

		end, complete, invalid := completeJSONValueEnd(writer.streamCarry, start)
		if invalid {
			return writer.fallbackJSONStream(out.Bytes())
		}
		if !complete {
			break
		}
		frame := newJSONRestoreFrame(
			writer.streamCarry[:start],
			writer.streamCarry[start:end],
		)
		_, _ = out.Write(writer.engine.push(frame))
		writer.streamCarry = writer.streamCarry[end:]
	}

	if len(writer.streamCarry)+writer.engine.queueBytes > maxQueuedRestoreBytes {
		return writer.fallbackJSONStream(out.Bytes())
	}
	if final {
		_, _ = out.Write(writer.engine.finish())
		if len(writer.streamCarry) > 0 {
			if firstNonSpace(writer.streamCarry) >= 0 {
				writer.engine.fallbacks++
			}
			_, _ = out.Write(writer.streamCarry)
			writer.streamCarry = nil
		}
	}
	return out.Bytes()
}

func (writer *restoringResponseWriter) fallbackJSONStream(prefix []byte) []byte {
	out := append([]byte(nil), prefix...)
	out = append(out, writer.engine.abort()...)
	out = append(out, writer.streamCarry...)
	writer.streamCarry = nil
	writer.engine.fallbacks++
	writer.streamRaw = true
	return out
}

func (writer *restoringResponseWriter) writeStreamingOutput(output []byte) error {
	if len(output) == 0 {
		return nil
	}
	if _, err := writer.ResponseWriter.Write(output); err != nil {
		return err
	}
	if err := http.NewResponseController(writer.ResponseWriter).Flush(); err != nil &&
		!errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

func (writer *restoringResponseWriter) mappingCount() int {
	if writer == nil || writer.engine == nil {
		return 0
	}
	return len(writer.engine.replacements)
}

func (writer *restoringResponseWriter) restoredCount() int {
	if writer == nil || writer.engine == nil {
		return 0
	}
	return writer.engine.restored + writer.textRestored
}

func (writer *restoringResponseWriter) fallbackCount() int {
	if writer == nil || writer.engine == nil {
		return 0
	}
	return writer.engine.fallbacks + writer.textFallbacks
}

func (writer *restoringResponseWriter) privacyRestoreSummary() contract.PrivacyRestoreSummary {
	return contract.PrivacyRestoreSummary{
		Enabled:       true,
		MappingCount:  writer.mappingCount(),
		RestoredCount: writer.restoredCount(),
		FallbackCount: writer.fallbackCount(),
	}
}

func (writer *restoringResponseWriter) writeRestoreFailure(status int, code, message string) {
	if writer.failed {
		return
	}
	writer.failed = true
	writer.wroteHeader = true
	for name := range writer.Header() {
		delete(writer.Header(), name)
	}
	writeInferenceError(writer.ResponseWriter, status, code, message, true, nil)
}
