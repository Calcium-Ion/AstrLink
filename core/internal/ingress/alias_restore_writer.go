package ingress

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

type aliasRestoringWriter struct {
	http.ResponseWriter
	publicModel   string
	upstreamModel string
	memberNames   map[string]bool
	aliasJSON     []byte
	streaming     bool
	status        int
	wroteHeader   bool
	passthrough   bool
	buffering     bool
	failed        bool
	buffer        bytes.Buffer
	carry         []byte
}

func newAliasRestoringWriter(
	writer http.ResponseWriter,
	publicModel, upstreamModel string,
	streaming bool,
	memberNames map[string]bool,
) *aliasRestoringWriter {
	aliasJSON, err := json.Marshal(publicModel)
	if err != nil {
		// publicModel is a plain string; Marshal only fails on impossible values.
		aliasJSON = []byte(`""`)
	}
	names := make(map[string]bool, len(memberNames))
	for name, ok := range memberNames {
		if ok {
			names[name] = true
		}
	}
	return &aliasRestoringWriter{
		ResponseWriter: writer,
		publicModel:    publicModel,
		upstreamModel:  upstreamModel,
		memberNames:    names,
		aliasJSON:      aliasJSON,
		streaming:      streaming,
	}
}

func (writer *aliasRestoringWriter) WriteHeader(status int) {
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
		return
	}
	writer.status = status
	writer.buffering = true
	writer.wroteHeader = true
}

func (writer *aliasRestoringWriter) Write(chunk []byte) (int, error) {
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
		return writer.writeStreaming(chunk)
	}
	if writer.buffer.Len()+len(chunk) > maxMetadataBytes {
		writer.writeRestoreFailure(http.StatusBadGateway, "upstream_response_too_large", "upstream response exceeds the restore buffer")
		return 0, errRestoreAborted
	}
	_, _ = writer.buffer.Write(chunk)
	return len(chunk), nil
}

func (writer *aliasRestoringWriter) writeStreaming(chunk []byte) (int, error) {
	writer.carry = append(writer.carry, chunk...)
	if len(writer.carry) > maxMetadataBytes {
		writer.writeRestoreFailure(http.StatusBadGateway, "upstream_response_too_large", "upstream response exceeds the restore buffer")
		return 0, errRestoreAborted
	}
	for {
		newline := bytes.IndexByte(writer.carry, '\n')
		if newline < 0 {
			break
		}
		line := writer.carry[:newline]
		writer.carry = append([]byte(nil), writer.carry[newline+1:]...)
		restored := writer.restoreSSELine(line)
		if _, err := writer.ResponseWriter.Write(append(restored, '\n')); err != nil {
			return 0, err
		}
	}
	if err := http.NewResponseController(writer.ResponseWriter).Flush(); err != nil &&
		!errors.Is(err, http.ErrNotSupported) {
		return 0, err
	}
	return len(chunk), nil
}

func (writer *aliasRestoringWriter) restoreSSELine(line []byte) []byte {
	if !bytes.HasPrefix(line, []byte("data:")) {
		return line
	}
	payload := line[len("data:"):]
	if len(payload) > 0 && payload[0] == ' ' {
		payload = payload[1:]
	}
	spans, err := modelMemberSpans(payload, writer.memberNames, writer.upstreamModel)
	if err != nil || len(spans) == 0 {
		return line
	}
	rewritten := spliceSpans(payload, spans, writer.aliasJSON)
	out := make([]byte, 0, len("data: ")+len(rewritten))
	out = append(out, "data: "...)
	out = append(out, rewritten...)
	return out
}

func (writer *aliasRestoringWriter) Flush() {
	_ = writer.FlushError()
}

func (writer *aliasRestoringWriter) FlushError() error {
	if writer.failed || writer.passthrough {
		return http.NewResponseController(writer.ResponseWriter).Flush()
	}
	if writer.buffering && !writer.streaming {
		// A non-streaming restored response is intentionally atomic. The
		// transport flush after upstream headers must not commit a synthetic
		// 200 before Finish can publish the real status and content length.
		return nil
	}
	// Streaming carry may hold an incomplete SSE line split across upstream
	// read chunks. Routine transport flushes must not drain it; Finish emits
	// any terminal remainder after the upstream body reaches EOF.
	return http.NewResponseController(writer.ResponseWriter).Flush()
}

func (writer *aliasRestoringWriter) Finish() error {
	if writer.failed || writer.passthrough {
		return nil
	}
	if writer.streaming {
		if len(writer.carry) == 0 {
			return nil
		}
		restored := writer.restoreSSELine(writer.carry)
		writer.carry = nil
		_, err := writer.ResponseWriter.Write(restored)
		return err
	}
	if !writer.buffering {
		return nil
	}
	body := writer.buffer.Bytes()
	spans, err := modelMemberSpans(body, writer.memberNames, writer.upstreamModel)
	restored := body
	if err == nil && len(spans) > 0 {
		restored = spliceSpans(body, spans, writer.aliasJSON)
	}
	writer.Header().Set("Content-Length", strconv.Itoa(len(restored)))
	writer.ResponseWriter.WriteHeader(writer.status)
	if len(restored) == 0 {
		return nil
	}
	_, writeErr := writer.ResponseWriter.Write(restored)
	return writeErr
}

func (writer *aliasRestoringWriter) writeRestoreFailure(status int, code, message string) {
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

func aliasMemberNames(protocol contract.ProtocolID) map[string]bool {
	if protocol == contract.ProtocolGoogleGenerateContent {
		return map[string]bool{"modelVersion": true, "model": true}
	}
	return map[string]bool{"model": true}
}
