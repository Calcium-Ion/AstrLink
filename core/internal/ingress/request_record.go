package ingress

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type recordSessionContextKey struct{}

func withRecordSession(ctx context.Context, session *recordSession) context.Context {
	return context.WithValue(ctx, recordSessionContextKey{}, session)
}

func recordSessionFromContext(ctx context.Context) *recordSession {
	session, _ := ctx.Value(recordSessionContextKey{}).(*recordSession)
	return session
}

// RequestRecordStore is the ingress-facing persistence seam for always-on
// metadata records (ADR 0007). Nil disables recording.
type RequestRecordStore interface {
	UpsertRequestRecord(context.Context, contract.RequestRecord) error
}

// AuditSettingsProvider supplies the capture switches for one request.
// Nil disables body capture (headless / tests without audit wiring).
type AuditSettingsProvider interface {
	GetAuditSettings(context.Context) (contract.AuditSettings, error)
}

// AuditBlobPersister encrypts and stores opt-in audit blobs after the response.
type AuditBlobPersister interface {
	GetOrCreateAuditKey(context.Context) ([]byte, error)
	InsertAuditBlob(context.Context, storage.AuditBlob) error
}

type captureBuffer struct {
	enabled   bool
	maxBytes  int
	mediaType string
	bytes     []byte
	truncated bool
	stopped   bool
}

func (buffer *captureBuffer) observe(chunk []byte) {
	if buffer == nil || !buffer.enabled || buffer.stopped {
		return
	}
	if buffer.maxBytes <= 0 {
		buffer.truncated = true
		buffer.stopped = true
		return
	}
	remaining := buffer.maxBytes - len(buffer.bytes)
	if remaining <= 0 {
		buffer.truncated = true
		buffer.stopped = true
		return
	}
	if len(chunk) > remaining {
		buffer.bytes = append(buffer.bytes, chunk[:remaining]...)
		buffer.truncated = true
		buffer.stopped = true
		return
	}
	buffer.bytes = append(buffer.bytes, chunk...)
}

type recordSession struct {
	id                   contract.RequestID
	startedAt            time.Time
	classified           Request
	accessTokenID        *contract.AccessTokenID
	scanner              *usageScanner
	status               contract.RequestStatus
	httpStatus           int
	hasHTTPStatus        bool
	endpointID           *contract.ServiceID
	routeID              *contract.RouteID
	plan                 *contract.ExecutionPlan
	errorSummary         *contract.ErrorSummary
	privacyRestore       *contract.PrivacyRestoreSummary
	responseWriter       *recordStatusWriter
	requestCapture       captureBuffer
	responseCapture      captureBuffer
	httpMetaEnabled      bool
	httpMetaCaptured     bool
	httpMetaResponseDone bool
	httpMeta             contract.AuditHTTPMeta
}

func newRecordSession(
	classified Request,
	accessTokenID contract.AccessTokenID,
	settings contract.AuditSettings,
) *recordSession {
	session := &recordSession{
		id:         newRequestRecordID(),
		startedAt:  time.Now().UTC(),
		classified: classified,
		scanner:    newUsageScanner(classified.Protocol, classified.Streaming),
		status:     contract.RequestStatusPending,
		requestCapture: captureBuffer{
			enabled:  settings.RequestBodyEnabled,
			maxBytes: settings.RequestBodyMaxBytes,
		},
		responseCapture: captureBuffer{
			enabled:  settings.ResponseContentEnabled,
			maxBytes: settings.ResponseContentMaxBytes,
		},
		httpMetaEnabled: settings.HTTPMetaEnabled,
	}
	if accessTokenID != "" {
		session.accessTokenID = &accessTokenID
	}
	return session
}

func (session *recordSession) persistPending(
	ctx context.Context,
	store RequestRecordStore,
	logf func(string, ...any),
) {
	if session == nil || store == nil {
		return
	}
	persistCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if err := store.UpsertRequestRecord(persistCtx, session.recordSnapshot(nil, nil)); err != nil {
		logRequestRecordFailure(logf, "pending_upsert", err)
	}
}

func (session *recordSession) recordSnapshot(
	completedAt *time.Time,
	latencyMs *int,
) contract.RequestRecord {
	var requestedModel *string
	if session.classified.Model != "" {
		model := session.classified.Model
		requestedModel = &model
	}
	var httpStatus *int
	if session.hasHTTPStatus {
		status := session.httpStatus
		httpStatus = &status
	}
	return contract.RequestRecord{
		ID:                 session.id,
		StartedAt:          session.startedAt,
		CompletedAt:        completedAt,
		Status:             session.status,
		InputProtocol:      session.classified.Protocol,
		RequestedModel:     requestedModel,
		Streaming:          session.classified.Streaming,
		RouteID:            session.routeID,
		ServiceID:          session.endpointID,
		LocalAccessTokenID: session.accessTokenID,
		Plan:               session.plan,
		HTTPStatus:         httpStatus,
		LatencyMs:          latencyMs,
		Usage:              session.scanner.Usage(),
		Error:              session.errorSummary,
		Audit:              contract.NotCapturedAuditSummary(),
		PrivacyRestore:     session.privacyRestore,
	}
}

func (session *recordSession) responseCaptureEnabled() bool {
	return session != nil && session.responseCapture.enabled
}

// captureHTTPRequestMeta snapshots the redacted request envelope (ADR 0008).
// It must run before privacy rewrites, alias rewriting, and header mutation
// so the capture reflects the bytes the client actually sent. It never sees
// transport.Target.RequestHeaders, which carry the upstream credential.
func (session *recordSession) captureHTTPRequestMeta(request *http.Request) {
	if session == nil || !session.httpMetaEnabled || request == nil {
		return
	}
	session.httpMeta = RedactRequestMeta(request)
	session.httpMetaCaptured = true
}

// noteHTTPResponseMeta records the redacted local response status and headers
// once, at first WriteHeader/Write. The forwarder has already stripped
// hop-by-hop headers and never copies upstream credentials here.
func (session *recordSession) noteHTTPResponseMeta(status int, headers http.Header) {
	if session == nil || !session.httpMetaEnabled || session.httpMetaResponseDone {
		return
	}
	statusCopy := status
	session.httpMeta.ResponseStatus = &statusCopy
	session.httpMeta.ResponseHeaders = RedactResponseHeaders(headers)
	session.httpMetaCaptured = true
	session.httpMetaResponseDone = true
}

func (session *recordSession) attachRequestCapture(request *http.Request) {
	if session == nil || !session.requestCapture.enabled || request == nil {
		return
	}
	mediaType := strings.TrimSpace(request.Header.Get("Content-Type"))
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	session.requestCapture.mediaType = mediaType
	if request.Body == nil || request.Body == http.NoBody {
		return
	}
	request.Body = &requestCaptureBody{ReadCloser: request.Body, session: session}
}

type requestCaptureBody struct {
	io.ReadCloser
	session *recordSession
}

func (body *requestCaptureBody) Read(p []byte) (int, error) {
	n, err := body.ReadCloser.Read(p)
	if n > 0 && body.session != nil {
		body.session.requestCapture.observe(p[:n])
	}
	return n, err
}

func (session *recordSession) wrap(writer http.ResponseWriter) http.ResponseWriter {
	if session == nil {
		return writer
	}
	session.responseWriter = &recordStatusWriter{ResponseWriter: writer, session: session}
	return session.scanner.wrap(session.responseWriter)
}

func (session *recordSession) noteServed(candidate endpoint.Resolved, plan contract.ExecutionPlan) {
	if session == nil {
		return
	}
	session.noteSelected(candidate, plan)
}

func (session *recordSession) noteSelected(candidate endpoint.Resolved, plan contract.ExecutionPlan) {
	endpointID := candidate.Service.ID
	session.endpointID = &endpointID
	if candidate.RouteID != "" {
		routeID := candidate.RouteID
		session.routeID = &routeID
	}
	planCopy := plan
	session.plan = &planCopy
}

func (session *recordSession) noteAttempt(
	ctx context.Context,
	candidate endpoint.Resolved,
	plan contract.ExecutionPlan,
	store RequestRecordStore,
	logf func(string, ...any),
) {
	if session == nil {
		return
	}
	session.noteSelected(candidate, plan)
	if store == nil {
		return
	}
	persistCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if err := store.UpsertRequestRecord(persistCtx, session.recordSnapshot(nil, nil)); err != nil {
		logRequestRecordFailure(logf, "pending_route_upsert", err)
	}
}

func (session *recordSession) beginPrivacyAttempt() {
	if session == nil {
		return
	}
	session.privacyRestore = nil
}

func (session *recordSession) notePrivacyMapping(enabled bool, mappingCount int) {
	if session == nil || mappingCount <= 0 {
		return
	}
	session.privacyRestore = &contract.PrivacyRestoreSummary{
		Enabled:      enabled,
		MappingCount: mappingCount,
	}
}

func (session *recordSession) notePrivacyRestore(summary contract.PrivacyRestoreSummary) {
	if session == nil {
		return
	}
	copy := summary
	session.privacyRestore = &copy
}

func (session *recordSession) noteSucceeded() {
	if session == nil {
		return
	}
	session.status = contract.RequestStatusSucceeded
}

func (session *recordSession) noteBlocked(summary contract.ErrorSummary) {
	if session == nil {
		return
	}
	session.status = contract.RequestStatusBlocked
	session.errorSummary = &summary
}

func (session *recordSession) noteFailed(summary contract.ErrorSummary) {
	if session == nil {
		return
	}
	if session.status == contract.RequestStatusSucceeded ||
		session.status == contract.RequestStatusBlocked {
		return
	}
	session.status = contract.RequestStatusFailed
	session.errorSummary = &summary
}

func (session *recordSession) noteCancelled() {
	if session == nil {
		return
	}
	if session.status == contract.RequestStatusSucceeded {
		return
	}
	session.status = contract.RequestStatusCancelled
}

func (session *recordSession) finish(
	ctx context.Context,
	store RequestRecordStore,
	blobs AuditBlobPersister,
	logf func(string, ...any),
) {
	if session == nil || store == nil {
		return
	}
	if session.status == contract.RequestStatusPending {
		session.status = contract.RequestStatusFailed
		session.errorSummary = &contract.ErrorSummary{
			Category:  "runtime",
			Code:      "request_incomplete",
			Message:   "request execution ended without a terminal status",
			Retryable: true,
		}
	}
	completed := time.Now().UTC()
	latency := int(completed.Sub(session.startedAt).Milliseconds())
	if latency < 0 {
		latency = 0
	}
	audit := contract.NotCapturedAuditSummary()
	pendingBlobs := make([]storage.AuditBlob, 0, 3)
	if blobs != nil {
		key, keyErr := session.prepareAuditKey(ctx, blobs, logf)
		if keyErr == nil && key != nil {
			if blob, ok := session.sealCapture(storage.AuditDirectionRequest, &session.requestCapture, key, completed, logf); ok {
				pendingBlobs = append(pendingBlobs, blob)
				audit.RequestBodyCaptured = true
				audit.RequestBodyTruncated = session.requestCapture.truncated
			}
			if blob, ok := session.sealCapture(storage.AuditDirectionResponse, &session.responseCapture, key, completed, logf); ok {
				pendingBlobs = append(pendingBlobs, blob)
				audit.ResponseContentCaptured = true
				audit.ResponseContentTruncated = session.responseCapture.truncated
			}
			if blob, ok := session.sealHTTPMeta(key, completed, logf); ok {
				pendingBlobs = append(pendingBlobs, blob)
			}
		}
	}

	record := session.recordSnapshot(&completed, &latency)
	record.Audit = audit
	if err := record.Validate(); err != nil {
		logRequestRecordFailure(logf, "validate", err)
		return
	}
	if err := store.UpsertRequestRecord(ctx, record); err != nil {
		logRequestRecordFailure(logf, "terminal_upsert", err)
		return
	}
	if blobs == nil {
		return
	}
	for _, blob := range pendingBlobs {
		blob.RequestID = record.ID
		if err := blobs.InsertAuditBlob(ctx, blob); err != nil {
			logRequestRecordFailure(logf, "audit_blob_insert", err)
		}
	}
}

func (session *recordSession) prepareAuditKey(
	ctx context.Context,
	blobs AuditBlobPersister,
	logf func(string, ...any),
) ([]byte, error) {
	requestHasBytes := session.requestCapture.enabled && len(session.requestCapture.bytes) > 0
	responseHasBytes := session.responseCapture.enabled && len(session.responseCapture.bytes) > 0
	// http meta needs the key too: it must persist even when both body
	// captures are disabled (the common default configuration).
	if !requestHasBytes && !responseHasBytes && !session.httpMetaCaptured {
		return nil, nil
	}
	key, err := blobs.GetOrCreateAuditKey(ctx)
	if err != nil {
		logRequestRecordFailure(logf, "audit_key", err)
		return nil, err
	}
	return key, nil
}

func (session *recordSession) sealCapture(
	direction storage.AuditDirection,
	buffer *captureBuffer,
	key []byte,
	createdAt time.Time,
	logf func(string, ...any),
) (storage.AuditBlob, bool) {
	if buffer == nil || !buffer.enabled || len(buffer.bytes) == 0 {
		return storage.AuditBlob{}, false
	}
	nonce, ciphertext, err := storage.SealAuditBlob(key, buffer.bytes)
	if err != nil {
		logRequestRecordFailure(logf, "audit_encrypt", err)
		return storage.AuditBlob{}, false
	}
	mediaType := buffer.mediaType
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return storage.AuditBlob{
		Direction:     direction,
		MediaType:     mediaType,
		Nonce:         nonce,
		Ciphertext:    ciphertext,
		Truncated:     buffer.truncated,
		CapturedBytes: len(buffer.bytes),
		CreatedAt:     createdAt,
	}, true
}

// sealHTTPMeta encrypts the redacted HTTP envelope as a third blob direction.
// The payload is already redacted at capture time; encryption at rest matches
// the body blobs so all audit data shares one lifecycle (ADR 0008).
func (session *recordSession) sealHTTPMeta(
	key []byte,
	createdAt time.Time,
	logf func(string, ...any),
) (storage.AuditBlob, bool) {
	if !session.httpMetaCaptured {
		return storage.AuditBlob{}, false
	}
	payload, err := json.Marshal(session.httpMeta)
	if err != nil {
		logRequestRecordFailure(logf, "http_meta_encode", err)
		return storage.AuditBlob{}, false
	}
	nonce, ciphertext, err := storage.SealAuditBlob(key, payload)
	if err != nil {
		logRequestRecordFailure(logf, "audit_encrypt", err)
		return storage.AuditBlob{}, false
	}
	return storage.AuditBlob{
		Direction:     storage.AuditDirectionHTTPMeta,
		MediaType:     "application/json",
		Nonce:         nonce,
		Ciphertext:    ciphertext,
		Truncated:     false,
		CapturedBytes: len(payload),
		CreatedAt:     createdAt,
	}, true
}

func logRequestRecordFailure(logf func(string, ...any), op string, err error) {
	if logf == nil {
		logf = log.Printf
	}
	// Sanitized: no bodies, headers, credentials, or upstream model names.
	logf("request record %s failed: %v", op, err)
}

func newRequestRecordID() contract.RequestID {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return contract.RequestID("request_unavailable")
	}
	return contract.RequestID("request_" + hex.EncodeToString(value[:]))
}

type recordStatusWriter struct {
	http.ResponseWriter
	session *recordSession
}

func (writer *recordStatusWriter) WriteHeader(status int) {
	if writer.session != nil {
		if !writer.session.hasHTTPStatus {
			writer.session.httpStatus = status
			writer.session.hasHTTPStatus = true
		}
		writer.session.noteHTTPResponseMeta(status, writer.Header())
		if writer.session.responseCapture.enabled && writer.session.responseCapture.mediaType == "" {
			mediaType := strings.TrimSpace(writer.Header().Get("Content-Type"))
			if mediaType == "" {
				mediaType = "application/octet-stream"
			}
			writer.session.responseCapture.mediaType = mediaType
		}
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *recordStatusWriter) Write(chunk []byte) (int, error) {
	if writer.session != nil {
		if !writer.session.hasHTTPStatus {
			writer.session.httpStatus = http.StatusOK
			writer.session.hasHTTPStatus = true
		}
		writer.session.noteHTTPResponseMeta(writer.session.httpStatus, writer.Header())
		if writer.session.responseCapture.enabled {
			if writer.session.responseCapture.mediaType == "" {
				mediaType := strings.TrimSpace(writer.Header().Get("Content-Type"))
				if mediaType == "" {
					mediaType = "application/octet-stream"
				}
				writer.session.responseCapture.mediaType = mediaType
			}
			writer.session.responseCapture.observe(chunk)
		}
	}
	return writer.ResponseWriter.Write(chunk)
}

func (writer *recordStatusWriter) Flush() {
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (writer *recordStatusWriter) FlushError() error {
	if controller, ok := writer.ResponseWriter.(interface{ FlushError() error }); ok {
		return controller.FlushError()
	}
	writer.Flush()
	return nil
}

func (writer *recordStatusWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func errorSummaryFromInference(code, message string, retryable bool) contract.ErrorSummary {
	category := "gateway"
	switch code {
	case "policy_blocked":
		category = "privacy"
	case "upstream_unavailable", "upstream_timeout", "credential_unavailable",
		"upstream_stream_interrupted":
		category = "upstream"
	case "missing_protocol_capability", "endpoint_resolver_unavailable":
		category = "routing"
	}
	return contract.ErrorSummary{
		Category:  category,
		Code:      code,
		Message:   message,
		Retryable: retryable,
	}
}
