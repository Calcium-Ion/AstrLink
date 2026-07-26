package ingress

import (
	"bytes"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

var errRestoreAborted = errors.New("response restore aborted")

const maxRestoreBufferBytes = maxMetadataBytes

type restoringResponseWriter struct {
	http.ResponseWriter
	redactions  []privacy.Redaction
	streaming   bool
	status      int
	wroteHeader bool
	passthrough bool
	buffering   bool
	failed      bool
	buffer      bytes.Buffer
	carry       *privacy.CarryRestorer
}

func newRestoringResponseWriter(
	writer http.ResponseWriter,
	redactions []privacy.Redaction,
	streaming bool,
) *restoringResponseWriter {
	return &restoringResponseWriter{
		ResponseWriter: writer,
		redactions:     append([]privacy.Redaction(nil), redactions...),
		streaming:      streaming,
	}
}

func (writer *restoringResponseWriter) WriteHeader(status int) {
	if writer.wroteHeader || writer.failed {
		return
	}
	encoding := strings.ToLower(strings.TrimSpace(writer.Header().Get("Content-Encoding")))
	if encoding != "" && encoding != "identity" {
		writer.writeRestoreFailure(http.StatusBadGateway, "upstream_content_encoding", "encoded upstream responses cannot be restored safely")
		return
	}
	contentType := writer.Header().Get("Content-Type")
	if !privacy.ShouldRestoreContentType(contentType) {
		writer.passthrough = true
		writer.wroteHeader = true
		writer.ResponseWriter.WriteHeader(status)
		return
	}
	if writer.streaming {
		writer.Header().Del("Content-Length")
		writer.wroteHeader = true
		writer.ResponseWriter.WriteHeader(status)
		writer.carry = privacy.NewCarryRestorer(writer.redactions)
		return
	}
	writer.status = status
	writer.buffering = true
	writer.wroteHeader = true
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
		restored := writer.carry.Push(chunk)
		if len(restored) == 0 {
			return len(chunk), nil
		}
		if _, err := writer.ResponseWriter.Write(restored); err != nil {
			return 0, err
		}
		if err := http.NewResponseController(writer.ResponseWriter).Flush(); err != nil &&
			!errors.Is(err, http.ErrNotSupported) {
			return 0, err
		}
		return len(chunk), nil
	}
	if writer.buffer.Len()+len(chunk) > maxRestoreBufferBytes {
		writer.writeRestoreFailure(http.StatusBadGateway, "upstream_response_too_large", "upstream response exceeds the restore buffer")
		return 0, errRestoreAborted
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
		// A non-streaming restored response is intentionally atomic. The
		// transport flush after upstream headers must not commit a synthetic
		// 200 before Finish can publish the real status and content length.
		return nil
	}
	// CarryRestorer may be holding a trailing placeholder prefix split across
	// upstream read chunks. Routine transport flushes must not drain it; Finish
	// emits any terminal remainder after the upstream body reaches EOF.
	return http.NewResponseController(writer.ResponseWriter).Flush()
}

func (writer *restoringResponseWriter) Finish() error {
	if writer.failed || writer.passthrough || !writer.buffering {
		if writer.streaming && writer.carry != nil {
			if pending := writer.carry.Flush(); len(pending) > 0 {
				if _, err := writer.ResponseWriter.Write(pending); err != nil {
					return err
				}
			}
		}
		return nil
	}
	restored := privacy.RestorePlaceholders(writer.buffer.Bytes(), writer.redactions)
	writer.Header().Set("Content-Length", strconv.Itoa(len(restored)))
	writer.ResponseWriter.WriteHeader(writer.status)
	if len(restored) == 0 {
		return nil
	}
	_, err := writer.ResponseWriter.Write(restored)
	return err
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
