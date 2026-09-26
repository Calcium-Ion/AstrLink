package ingress

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/convo"
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
	InsertRequestRecord(context.Context, contract.RequestRecord) error
	// FindSessionLink implements the convo.Lookup contract over stored
	// cursors; see storage.RequestRecordStore for the matching rules.
	FindSessionLink(context.Context, contract.SessionCursorKind, []string, storage.SessionCursorScope) (storage.SessionLinkMatch, bool, error)
}

// AuditSettingsProvider supplies the capture switches for one request.
// Nil disables body capture (headless / tests without audit wiring).
type AuditSettingsProvider interface {
	GetAuditSettings(context.Context) (contract.AuditSettings, error)
}

// AuditBlobPersister encrypts and stores opt-in audit blobs. Request-side
// blobs may be written while the call is still in flight; response-side
// blobs wait until the stream ends (ADR 0007).
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
	complete  bool
}

func (buffer *captureBuffer) observe(chunk []byte) {
	if buffer == nil || !buffer.enabled || buffer.stopped {
		return
	}
	if buffer.maxBytes <= 0 {
		buffer.truncated = true
		buffer.stopped = true
		buffer.complete = true
		return
	}
	remaining := buffer.maxBytes - len(buffer.bytes)
	if remaining <= 0 {
		buffer.truncated = true
		buffer.stopped = true
		buffer.complete = true
		return
	}
	if len(chunk) > remaining {
		buffer.bytes = append(buffer.bytes, chunk[:remaining]...)
		buffer.truncated = true
		buffer.stopped = true
		buffer.complete = true
		return
	}
	buffer.bytes = append(buffer.bytes, chunk...)
}

func (buffer *captureBuffer) markComplete() {
	if buffer == nil {
		return
	}
	buffer.complete = true
}

func (buffer *captureBuffer) readyToPersist() bool {
	return buffer != nil && buffer.enabled && buffer.complete && len(buffer.bytes) > 0
}

func (buffer *captureBuffer) reset(enabled bool, maxBytes int) {
	*buffer = captureBuffer{enabled: enabled, maxBytes: maxBytes}
}

type pendingAttemptRecord struct {
	record     contract.RequestRecord
	blobs      []storage.AuditBlob
	eventCount int
}

type recordSession struct {
	clientType     contract.ClientType
	channelBinding *channelBindingAttempt
	// routingSettings is the request's single settings read; nil when the
	// store has none or the read failed.
	routingSettings          *contract.RoutingSettings
	pendingAttempt           *pendingAttemptRecord
	recovery                 *contract.RequestRecovery
	modelRedirect            *contract.RequestModelRedirect
	id                       contract.RequestID
	startedAt                time.Time
	classified               Request
	accessTokenID            *contract.AccessTokenID
	scanner                  *usageScanner
	upstreamScanner          *usageScanner
	status                   contract.RequestStatus
	httpStatus               int
	hasHTTPStatus            bool
	upstreamHTTPStatus       int
	hasUpstreamHTTPStatus    bool
	endpointID               *contract.ServiceID
	plan                     *contract.ExecutionPlan
	errorSummary             *contract.ErrorSummary
	privacyRestore           *contract.PrivacyRestoreSummary
	privacyBatch             string
	attemptIndex             int
	childCount               int
	networkAttemptOpen       bool
	responseWriter           *recordStatusWriter
	requestCapture           captureBuffer
	responseCapture          captureBuffer
	upstreamRequestCapture   captureBuffer
	upstreamResponseCapture  captureBuffer
	httpMetaEnabled          bool
	httpMetaCaptured         bool
	httpMetaResponseDone     bool
	httpMeta                 contract.AuditHTTPMeta
	upstreamHTTPMetaEnabled  bool
	upstreamHTTPMetaCaptured bool
	upstreamHTTPMeta         contract.AuditHTTPMeta
	settings                 contract.AuditSettings
	sessionID                contract.SessionID
	previousResponseID       string
	outputResponseID         string
	inputPreview             string
	// fingerprinter is nil when audit storage is unavailable; text-only
	// linking is then skipped for this request.
	fingerprinter *convo.Fingerprinter
	// turn is the user turn convo placed this request in, with the state the
	// next linked request compares against; nil when the protocol has none.
	turn        *convo.TurnState
	sessionLink *contract.SessionLink
	// routing explains the provider choice. All attempts share it, so
	// resetAttemptLocal keeps it; nil when the resolver cannot rank providers.
	routing *routingTrace
	// inboundCursors are the explicit cursors the request named; they are
	// stored so sibling requests naming the same conversation can link.
	inboundCursors []contract.SessionCursor
	// outputCursors are what the client-facing response produced.
	outputCursors  []contract.SessionCursor
	events         []contract.RequestEvent
	persistStore   RequestRecordStore
	persistBlobs   AuditBlobPersister
	persistLogf    func(string, ...any)
	persistedAudit map[storage.AuditDirection]bool
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
		// No upstream RoundTrip yet — blocked/local failures stay at 0.
		attemptIndex: 0,
		requestCapture: captureBuffer{
			enabled:  settings.RequestBodyEnabled,
			maxBytes: settings.RequestBodyMaxBytes,
		},
		responseCapture: captureBuffer{
			enabled:  settings.ResponseContentEnabled,
			maxBytes: settings.ResponseContentMaxBytes,
		},
		upstreamRequestCapture: captureBuffer{
			enabled:  settings.RequestBodyEnabled,
			maxBytes: settings.RequestBodyMaxBytes,
		},
		upstreamResponseCapture: captureBuffer{
			enabled:  settings.ResponseContentEnabled,
			maxBytes: settings.ResponseContentMaxBytes,
		},
		httpMetaEnabled:         settings.HTTPMetaEnabled,
		upstreamHTTPMetaEnabled: settings.HTTPMetaEnabled,
		settings:                settings,
		persistedAudit:          make(map[storage.AuditDirection]bool),
	}
	if accessTokenID != "" {
		session.accessTokenID = &accessTokenID
	}
	return session
}

func (session *recordSession) bindPersistence(
	store RequestRecordStore,
	blobs AuditBlobPersister,
	logf func(string, ...any),
) {
	if session == nil {
		return
	}
	session.persistStore = store
	session.persistBlobs = blobs
	session.persistLogf = logf
}

func (session *recordSession) persistPending(
	ctx context.Context,
	store RequestRecordStore,
	logf func(string, ...any),
) {
	if session == nil {
		return
	}
	if store != nil {
		session.persistStore = store
	}
	if logf != nil {
		session.persistLogf = logf
	}
	session.persistAvailableAudit(ctx)
}

func (session *recordSession) liveAuditSummary() contract.AuditRecordSummary {
	if session == nil {
		return contract.NotCapturedAuditSummary()
	}
	return contract.AuditRecordSummary{
		RequestBodyCaptured:              session.auditPersisted(storage.AuditDirectionRequest),
		ResponseContentCaptured:          session.auditPersisted(storage.AuditDirectionResponse),
		RequestBodyTruncated:             session.auditPersisted(storage.AuditDirectionRequest) && session.requestCapture.truncated,
		ResponseContentTruncated:         session.auditPersisted(storage.AuditDirectionResponse) && session.responseCapture.truncated,
		UpstreamRequestBodyCaptured:      session.auditPersisted(storage.AuditDirectionUpstreamRequest),
		UpstreamResponseContentCaptured:  session.auditPersisted(storage.AuditDirectionUpstreamResponse),
		UpstreamRequestBodyTruncated:     session.auditPersisted(storage.AuditDirectionUpstreamRequest) && session.upstreamRequestCapture.truncated,
		UpstreamResponseContentTruncated: session.auditPersisted(storage.AuditDirectionUpstreamResponse) && session.upstreamResponseCapture.truncated,
	}
}

func (session *recordSession) auditPersisted(direction storage.AuditDirection) bool {
	return session != nil && session.persistedAudit[direction]
}

func (session *recordSession) markAuditPersisted(direction storage.AuditDirection) {
	if session == nil {
		return
	}
	if session.persistedAudit == nil {
		session.persistedAudit = make(map[storage.AuditDirection]bool)
	}
	session.persistedAudit[direction] = true
}

func (session *recordSession) clearPersistedAudit(direction storage.AuditDirection) {
	if session == nil || session.persistedAudit == nil {
		return
	}
	delete(session.persistedAudit, direction)
}

// persistAvailableAudit writes request-side blobs that are already complete
// and refreshes the pending metadata row. The request row is upserted before
// any blob: audit_blobs.request_id references request_records(id), so a live
// body that is ready on the first persist must not race the parent insert.
// A second upsert follows successful blob writes so the pending row's audit
// flags match what was actually stored. It must never delay inference: the
// 500ms bound matches persistPending / pending_route_upsert.
func (session *recordSession) persistAvailableAudit(ctx context.Context) {
	if session == nil {
		return
	}
	persistCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if session.persistStore != nil {
		if err := session.persistStore.UpsertRequestRecord(persistCtx, session.recordSnapshot(nil, nil)); err != nil {
			logRequestRecordFailure(session.persistLogf, "live_audit_upsert", err)
			return
		}
	}
	if session.persistBlobs != nil {
		session.persistReadyAuditBlobs(persistCtx, session.persistBlobs, session.persistLogf)
	}
	if session.persistStore == nil {
		return
	}
	if err := session.persistStore.UpsertRequestRecord(persistCtx, session.recordSnapshot(nil, nil)); err != nil {
		logRequestRecordFailure(session.persistLogf, "live_audit_upsert", err)
	}
}

func (session *recordSession) persistReadyAuditBlobs(
	ctx context.Context,
	blobs AuditBlobPersister,
	logf func(string, ...any),
) {
	key, keyErr := session.prepareAuditKey(ctx, blobs, logf)
	if keyErr != nil || key == nil {
		return
	}
	now := time.Now().UTC()
	session.persistOneAuditBlob(ctx, blobs, logf, storage.AuditDirectionRequest, func() (storage.AuditBlob, bool) {
		if !session.requestCapture.readyToPersist() {
			return storage.AuditBlob{}, false
		}
		return session.sealCapture(storage.AuditDirectionRequest, &session.requestCapture, key, now, logf)
	})
	session.persistOneAuditBlob(ctx, blobs, logf, storage.AuditDirectionHTTPMeta, func() (storage.AuditBlob, bool) {
		if !session.httpMetaCaptured {
			return storage.AuditBlob{}, false
		}
		return session.sealHTTPMeta(key, now, logf)
	})
	session.persistOneAuditBlob(ctx, blobs, logf, storage.AuditDirectionUpstreamRequest, func() (storage.AuditBlob, bool) {
		if !session.upstreamRequestCapture.readyToPersist() {
			return storage.AuditBlob{}, false
		}
		return session.sealCapture(storage.AuditDirectionUpstreamRequest, &session.upstreamRequestCapture, key, now, logf)
	})
	session.persistOneAuditBlob(ctx, blobs, logf, storage.AuditDirectionUpstreamHTTPMeta, func() (storage.AuditBlob, bool) {
		if !session.upstreamHTTPMetaCaptured {
			return storage.AuditBlob{}, false
		}
		return session.sealUpstreamHTTPMeta(key, now, logf)
	})
}

func (session *recordSession) persistOneAuditBlob(
	ctx context.Context,
	blobs AuditBlobPersister,
	logf func(string, ...any),
	direction storage.AuditDirection,
	seal func() (storage.AuditBlob, bool),
) {
	if session.auditPersisted(direction) {
		return
	}
	blob, ok := seal()
	if !ok {
		return
	}
	blob.RequestID = session.id
	if err := blobs.InsertAuditBlob(ctx, blob); err != nil {
		logRequestRecordFailure(logf, "live_audit_blob_insert", err)
		return
	}
	session.markAuditPersisted(direction)
}

func (session *recordSession) noteInboundBodyReady() {
	if session == nil {
		return
	}
	if len(session.requestCapture.bytes) > 0 || session.requestCapture.complete {
		session.requestCapture.markComplete()
	}
	session.persistAvailableAudit(context.Background())
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
	} else if session.hasUpstreamHTTPStatus {
		// Retry children never reach the client writer; surface upstream status.
		status := session.upstreamHTTPStatus
		httpStatus = &status
	}
	record := contract.RequestRecord{
		ClientType:         session.clientType,
		ID:                 session.id,
		ParentRequestID:    nil,
		AttemptIndex:       session.attemptIndex,
		ChildCount:         session.childCount,
		StartedAt:          session.startedAt,
		CompletedAt:        completedAt,
		Status:             session.status,
		InputProtocol:      session.classified.Protocol,
		RequestedModel:     requestedModel,
		ReasoningEffort:    session.classified.ReasoningEffort,
		Streaming:          session.classified.Streaming,
		ServiceID:          session.endpointID,
		LocalAccessTokenID: session.accessTokenID,
		Plan:               session.plan,
		HTTPStatus:         httpStatus,
		Recovery:           session.recovery,
		LatencyMs:          latencyMs,
		Usage:              session.attemptUsage(),
		Error:              session.errorSummary,
		Audit:              session.liveAuditSummary(),
		PrivacyRestore:     session.privacyRestore,
		Events:             append([]contract.RequestEvent(nil), session.events...),
	}
	if session.upstreamScanner != nil && !session.upstreamScanner.firstOutputAt.IsZero() {
		firstTokenMs := int(max(0, session.upstreamScanner.firstOutputAt.Sub(session.startedAt).Milliseconds()))
		record.FirstTokenMs = &firstTokenMs
	}
	if session.sessionID != "" {
		id := session.sessionID
		record.SessionID = &id
	}
	if session.previousResponseID != "" {
		value := session.previousResponseID
		record.PreviousResponseID = &value
	}
	if session.outputResponseID != "" {
		value := session.outputResponseID
		record.OutputResponseID = &value
	}
	if session.inputPreview != "" {
		value := session.inputPreview
		record.InputPreview = &value
	}
	if session.turn != nil && session.turn.Index >= 1 {
		index, users := session.turn.Index, session.turn.UserMessages
		record.TurnIndex = &index
		record.TurnUserMessages = &users
		if session.turn.LastUserFingerprint != "" {
			fingerprint := session.turn.LastUserFingerprint
			record.TurnUserFingerprint = &fingerprint
		}
	}
	if session.sessionLink != nil {
		link := *session.sessionLink
		record.SessionLink = &link
	}
	if session.modelRedirect != nil {
		// Copy so a retry child never aliases the root's redirect.
		redirect := *session.modelRedirect
		record.ModelRedirect = &redirect
	}
	record.RoutingDecision = session.routingDecision()
	record.Cursors = mergeSessionCursors(session.inboundCursors, session.outputCursors)
	return record
}

func (session *recordSession) attemptUsage() *contract.Usage {
	if session == nil {
		return nil
	}
	if session.upstreamScanner != nil {
		if usage := session.upstreamScanner.Usage(); usage != nil {
			copy := *usage
			copy.ThinkingEnabled = session.classified.ThinkingEnabled
			copy.BillingIncomplete = session.upstreamScanner.streaming && !session.upstreamScanner.complete
			return &copy
		}
	}
	usage := session.scanner.Usage()
	if usage != nil {
		copy := *usage
		copy.ThinkingEnabled = session.classified.ThinkingEnabled
		copy.BillingIncomplete = session.scanner.streaming && !session.scanner.complete
		return &copy
	}
	return nil
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
		session.requestCapture.markComplete()
		return
	}
	request.Body = &requestCaptureBody{ReadCloser: request.Body, session: session}
}

// captureUnreadRequestBody feeds the client body to the audit capture when the
// request fails before any attempt reads it, e.g. every provider's circuit is
// open. Reading stops one byte past the capture limit so truncation is marked.
func (session *recordSession) captureUnreadRequestBody(request *http.Request) {
	if session == nil || request == nil || session.requestCapture.complete {
		return
	}
	body, ok := request.Body.(*requestCaptureBody)
	if !ok {
		return
	}
	limit := int64(session.requestCapture.maxBytes) + 1
	_, _ = io.Copy(io.Discard, io.LimitReader(body, limit))
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
	if errors.Is(err, io.EOF) && body.session != nil {
		body.session.requestCapture.markComplete()
		body.session.persistAvailableAudit(context.Background())
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
	model := candidate.UpstreamModel
	if model == "" {
		model = session.classified.routingModel()
	}
	if session.recovery == nil {
		session.recovery = &contract.RequestRecovery{}
	}
	session.recovery.UpstreamModel = model
	endpointID := candidate.Service.ID
	session.endpointID = &endpointID
	planCopy := plan
	session.plan = &planCopy
}

// noteAttemptedService attributes a failure that never reached a RoundTrip to
// the provider it was prepared for. A real network attempt already recorded on
// the root wins, matching the failure reported to the client.
func (session *recordSession) noteAttemptedService(id contract.ServiceID) {
	if session == nil || id == "" || session.endpointID != nil {
		return
	}
	session.endpointID = &id
}

// noteModelRedirect records that routing uses to instead of the client's
// requested model from. It is a closed point event right after accepted; the
// requested model stays from, and retry children inherit the redirect.
func (session *recordSession) noteModelRedirect(ctx context.Context, from, to string) {
	if session == nil || from == "" || to == "" || from == to {
		return
	}
	session.modelRedirect = &contract.RequestModelRedirect{From: from, To: to}
	session.classified.RedirectedModel = to
	session.addEvent(contract.RequestEventModelRedirect, contract.RequestStatusSucceeded, from+" → "+to)
	event := &session.events[len(session.events)-1]
	ended := event.StartedAt
	event.EndedAt = &ended
	// The pending row was written before routing settings were read.
	session.persistLiveMetadata(ctx)
}

// noteCandidateRejected keeps a provider that was chosen but never called
// visible on the root, e.g. a missing credential or an open circuit.
func (session *recordSession) noteCandidateRejected(id contract.ServiceID, reason string) {
	if session == nil || id == "" {
		return
	}
	session.addEvent(contract.RequestEventRouted, contract.RequestStatusFailed, string(id)+" · "+reason)
	event := &session.events[len(session.events)-1]
	ended := event.StartedAt
	event.EndedAt = &ended
}

// beginNetworkAttempt marks the start of a real RoundTrip. Candidate selection
// that never reaches ObserveOutbound must not call this.
func (session *recordSession) beginNetworkAttempt(
	ctx context.Context,
	candidate endpoint.Resolved,
	plan contract.ExecutionPlan,
	store RequestRecordStore,
	logf func(string, ...any),
) {
	if session == nil {
		return
	}
	session.commitPendingAttempt(ctx, store, logf)
	session.attemptIndex++
	session.startedAt = time.Now().UTC()
	session.networkAttemptOpen = true
	session.upstreamScanner = newUsageScanner(plan.UpstreamProtocol, session.classified.Streaming)
	session.noteSelected(candidate, plan)
	serviceName := string(candidate.Service.ID)
	session.addEvent(contract.RequestEventRouted, contract.RequestStatusSucceeded, string(plan.Type)+" · "+serviceName)
	session.addEvent(contract.RequestEventUpstream, contract.RequestStatusPending, string(plan.UpstreamProtocol))
	if store != nil {
		session.persistStore = store
	}
	if logf != nil {
		session.persistLogf = logf
	}
	session.persistAvailableAudit(ctx)
}

// observeOutboundCapture records the exact upstream request after transport
// normalization and attaches a body tee. Credentials are redacted first.
func (session *recordSession) observeOutboundCapture(outbound *http.Request) {
	if session == nil || outbound == nil {
		return
	}
	if session.upstreamHTTPMetaEnabled {
		session.upstreamHTTPMeta = RedactUpstreamRequestMeta(outbound)
		session.upstreamHTTPMetaCaptured = true
	}
	if !session.upstreamRequestCapture.enabled {
		return
	}
	mediaType := strings.TrimSpace(outbound.Header.Get("Content-Type"))
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	session.upstreamRequestCapture.mediaType = mediaType
	if outbound.Body == nil || outbound.Body == http.NoBody {
		session.upstreamRequestCapture.markComplete()
		session.persistAvailableAudit(context.Background())
		return
	}
	outbound.Body = &upstreamRequestCaptureBody{ReadCloser: outbound.Body, session: session}
}

type upstreamRequestCaptureBody struct {
	io.ReadCloser
	session *recordSession
}

func (body *upstreamRequestCaptureBody) Read(p []byte) (int, error) {
	n, err := body.ReadCloser.Read(p)
	if n > 0 && body.session != nil {
		body.session.upstreamRequestCapture.observe(p[:n])
	}
	if errors.Is(err, io.EOF) && body.session != nil {
		body.session.upstreamRequestCapture.markComplete()
		body.session.persistAvailableAudit(context.Background())
	}
	return n, err
}

// wrapUpstreamResponseBody tees raw upstream response bytes before RelayKit /
// privacy / alias restoration. Partial bytes on interruption are kept.
func (session *recordSession) wrapUpstreamResponseBody(
	status int,
	headers http.Header,
	body io.ReadCloser,
) io.ReadCloser {
	if session == nil {
		return body
	}
	session.upstreamHTTPStatus = status
	session.hasUpstreamHTTPStatus = true
	if session.upstreamHTTPMetaEnabled {
		if !session.upstreamHTTPMetaCaptured {
			session.upstreamHTTPMeta = contract.AuditHTTPMeta{
				RequestHeaders:  []contract.AuditHeader{},
				ResponseHeaders: []contract.AuditHeader{},
			}
		}
		statusCopy := status
		session.upstreamHTTPMeta.ResponseStatus = &statusCopy
		session.upstreamHTTPMeta.ResponseHeaders = RedactResponseHeaders(headers)
		session.upstreamHTTPMetaCaptured = true
	}
	if session.upstreamScanner != nil {
		// A non-streaming request can be answered with SSE when streaming was
		// forced upstream (Codex subscriptions); parse usage from the events.
		if !session.upstreamScanner.streaming &&
			strings.HasPrefix(strings.ToLower(strings.TrimSpace(headers.Get("Content-Type"))), "text/event-stream") {
			session.upstreamScanner.reset(session.upstreamScanner.protocol, true)
		}
		session.upstreamScanner.setContentEncoding(headers.Get("Content-Encoding"))
	}
	if body == nil {
		return body
	}
	if session.upstreamResponseCapture.enabled {
		mediaType := strings.TrimSpace(headers.Get("Content-Type"))
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		session.upstreamResponseCapture.mediaType = mediaType
	}
	return &upstreamResponseCaptureBody{ReadCloser: body, session: session}
}

type upstreamResponseCaptureBody struct {
	io.ReadCloser
	session *recordSession
}

func (body *upstreamResponseCaptureBody) Read(p []byte) (int, error) {
	n, err := body.ReadCloser.Read(p)
	if n > 0 && body.session != nil {
		body.session.upstreamResponseCapture.observe(p[:n])
		if body.session.upstreamScanner != nil {
			body.session.upstreamScanner.observe(p[:n])
		}
	}
	return n, err
}

func (session *recordSession) beginPrivacyAttempt() {
	if session == nil {
		return
	}
	session.privacyRestore = nil
	session.privacyBatch = ""
}

// beginPrivacyInspection opens the privacy phase before the detector runs. A
// local model can spend minutes on a long agent transcript while the pending
// row is otherwise not rewritten until the first RoundTrip, so without this a
// live or exported record cannot show where the request is waiting.
func (session *recordSession) beginPrivacyInspection(ctx context.Context, summary string) {
	if session == nil {
		return
	}
	session.addEvent(contract.RequestEventPrivacy, contract.RequestStatusPending, summary)
	session.persistLiveMetadata(ctx)
}

// updatePrivacyInspection rewrites the open privacy phase as the detector
// works through its batches. batch is kept so a detector failure can record
// where the inspection stopped.
func (session *recordSession) updatePrivacyInspection(ctx context.Context, summary, batch string) {
	if session == nil {
		return
	}
	session.privacyBatch = batch
	for index := len(session.events) - 1; index >= 0; index-- {
		event := &session.events[index]
		if event.Kind != contract.RequestEventPrivacy || event.EndedAt != nil {
			continue
		}
		event.Summary = sanitizeSummary(summary)
		session.persistLiveMetadata(ctx)
		return
	}
}

func (session *recordSession) privacyInspectionBatch() string {
	if session == nil {
		return ""
	}
	return session.privacyBatch
}

// persistLiveMetadata refreshes the pending row without touching audit blobs.
// A retry that is still staging its failed attempt holds a terminal status in
// memory, so only a pending root is written; the next RoundTrip persists the
// rest. It shares persistAvailableAudit's 500ms bound.
func (session *recordSession) persistLiveMetadata(ctx context.Context) {
	if session == nil || session.persistStore == nil || session.status != contract.RequestStatusPending {
		return
	}
	persistCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if err := session.persistStore.UpsertRequestRecord(persistCtx, session.recordSnapshot(nil, nil)); err != nil {
		logRequestRecordFailure(session.persistLogf, "live_metadata_upsert", err)
	}
}

func (session *recordSession) notePrivacyMapping(
	enabled bool,
	mappingCount int,
	hits []contract.PrivacyHitCount,
) {
	if session == nil || mappingCount <= 0 {
		return
	}
	session.privacyRestore = &contract.PrivacyRestoreSummary{
		Enabled:      enabled,
		MappingCount: mappingCount,
		Hits:         hits,
	}
}

func (session *recordSession) notePrivacyRestore(summary contract.PrivacyRestoreSummary) {
	if session == nil {
		return
	}
	copy := summary
	session.privacyRestore = &copy
}

func (session *recordSession) recordedHTTPStatus() (int, bool) {
	if session == nil {
		return 0, false
	}
	if session.hasHTTPStatus {
		return session.httpStatus, true
	}
	if session.hasUpstreamHTTPStatus {
		return session.upstreamHTTPStatus, true
	}
	return 0, false
}

func (session *recordSession) failFromHTTPError() bool {
	if session == nil {
		return false
	}
	status, ok := session.recordedHTTPStatus()
	if !ok || status < http.StatusBadRequest {
		return false
	}
	session.status = contract.RequestStatusFailed
	if session.errorSummary == nil {
		summary := errorSummaryFromHTTPStatus(status)
		session.errorSummary = &summary
	}
	return true
}

func (session *recordSession) noteSucceeded() {
	if session == nil {
		return
	}
	if session.failFromHTTPError() {
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

// demoteCurrentAttemptToChild stages a failed attempt. It becomes an independent
// child only when another actual network attempt begins.
func (session *recordSession) demoteCurrentAttemptToChild(
	ctx context.Context,
	store RequestRecordStore,
	blobs AuditBlobPersister,
	summary contract.ErrorSummary,
	logf func(string, ...any),
) {
	if session == nil || !session.networkAttemptOpen || session.attemptIndex < 1 {
		return
	}
	session.noteFailed(summary)
	session.closeEventKind(contract.RequestEventUpstream, contract.RequestStatusFailed, summary.Code)
	session.captureOutputID()
	completed := time.Now().UTC()
	latency := int(completed.Sub(session.startedAt).Milliseconds())
	if latency < 0 {
		latency = 0
	}

	childID := newRequestRecordID()
	parentID := session.id
	child := session.recordSnapshot(&completed, &latency)
	child.ID = childID
	child.ParentRequestID = &parentID
	child.ChildCount = 0
	child.Events = eventsForAttempt(session.events, session.attemptIndex)
	child.Audit = session.upstreamAuditSummary()
	// A failed attempt produced nothing the client will replay; only the
	// root anchors the session.
	child.Cursors = nil
	child.SessionLink = nil

	pendingBlobs := make([]storage.AuditBlob, 0, 3)
	if blobs != nil {
		key, keyErr := session.prepareUpstreamAuditKey(ctx, blobs, logf)
		if keyErr == nil && key != nil {
			if blob, ok := session.sealCapture(storage.AuditDirectionUpstreamRequest, &session.upstreamRequestCapture, key, completed, logf); ok {
				blob.RequestID = childID
				pendingBlobs = append(pendingBlobs, blob)
			}
			if blob, ok := session.sealCapture(storage.AuditDirectionUpstreamResponse, &session.upstreamResponseCapture, key, completed, logf); ok {
				blob.RequestID = childID
				pendingBlobs = append(pendingBlobs, blob)
			}
			if blob, ok := session.sealUpstreamHTTPMeta(key, completed, logf); ok {
				blob.RequestID = childID
				pendingBlobs = append(pendingBlobs, blob)
			}
		}
	}

	if child.Recovery != nil {
		copy := *child.Recovery
		child.Recovery = &copy
	}
	// Keep the last real attempt on the root until another RoundTrip starts.
	// Cancellation during backoff or locally rejected candidates create no child.
	session.pendingAttempt = &pendingAttemptRecord{record: child, blobs: pendingBlobs, eventCount: len(session.events)}
}

func (session *recordSession) commitPendingAttempt(ctx context.Context, store RequestRecordStore, logf func(string, ...any)) {
	pending := session.pendingAttempt
	if pending == nil {
		return
	}
	session.pendingAttempt = nil
	child := pending.record
	persisted := true
	if err := child.Validate(); err != nil {
		logRequestRecordFailure(logf, "child_validate", err)
		persisted = false
	} else if store != nil {
		if err := store.InsertRequestRecord(ctx, child); err != nil {
			logRequestRecordFailure(logf, "child_insert", err)
			persisted = false
		}
	}
	if persisted {
		session.childCount++
		if session.persistBlobs != nil {
			for _, blob := range pending.blobs {
				if err := session.persistBlobs.InsertAuditBlob(ctx, blob); err != nil {
					logRequestRecordFailure(logf, "child_audit_blob_insert", err)
				}
			}
		}
	}
	if resetter, ok := session.persistBlobs.(interface {
		DeleteUpstreamAuditBlobs(context.Context, contract.RequestID) error
	}); ok {
		if err := resetter.DeleteUpstreamAuditBlobs(ctx, session.id); err != nil {
			logRequestRecordFailure(logf, "root_upstream_audit_reset", err)
		}
	}
	// Preparation of the next target has already evaluated privacy. Preserve
	// that decision while discarding the previous target's captures and status.
	privacy := session.privacyRestore
	events := eventsWithoutAttempt(session.events[:pending.eventCount], session.attemptIndex)
	for _, event := range session.events[pending.eventCount:] {
		event.AttemptIndex = session.attemptIndex + 1
		events = append(events, event)
	}
	session.events = events
	session.resetAttemptLocal()
	session.privacyRestore = privacy
}

func (session *recordSession) resetAttemptLocal() {
	// Keep the scanner pointer held by the client writer valid across retries.
	session.scanner.reset(session.classified.Protocol, session.classified.Streaming)
	session.upstreamScanner = nil
	session.status = contract.RequestStatusPending
	session.httpStatus = 0
	session.recovery = nil
	session.outputResponseID = ""
	session.outputCursors = nil
	session.hasHTTPStatus = false
	session.upstreamHTTPStatus = 0
	session.hasUpstreamHTTPStatus = false
	session.endpointID = nil
	session.plan = nil
	session.errorSummary = nil
	session.privacyRestore = nil
	session.networkAttemptOpen = false
	session.upstreamRequestCapture.reset(session.settings.RequestBodyEnabled, session.settings.RequestBodyMaxBytes)
	session.upstreamResponseCapture.reset(session.settings.ResponseContentEnabled, session.settings.ResponseContentMaxBytes)
	session.upstreamHTTPMeta = contract.AuditHTTPMeta{}
	session.upstreamHTTPMetaCaptured = false
	session.clearPersistedAudit(storage.AuditDirectionUpstreamRequest)
	session.clearPersistedAudit(storage.AuditDirectionUpstreamResponse)
	session.clearPersistedAudit(storage.AuditDirectionUpstreamHTTPMeta)
	// Client-side captures and attemptIndex stay on the root until the next
	// beginNetworkAttempt advances the index.
}

func (session *recordSession) upstreamAuditSummary() contract.AuditRecordSummary {
	return contract.AuditRecordSummary{
		UpstreamRequestBodyCaptured:      session.upstreamRequestCapture.enabled && len(session.upstreamRequestCapture.bytes) > 0,
		UpstreamResponseContentCaptured:  session.upstreamResponseCapture.enabled && len(session.upstreamResponseCapture.bytes) > 0,
		UpstreamRequestBodyTruncated:     session.upstreamRequestCapture.truncated,
		UpstreamResponseContentTruncated: session.upstreamResponseCapture.truncated,
	}
}

func (session *recordSession) finish(
	ctx context.Context,
	store RequestRecordStore,
	blobs AuditBlobPersister,
	logf func(string, ...any),
) {
	if session == nil {
		return
	}
	if store == nil {
		logIngressAccess(logf, session)
		return
	}
	defer logIngressAccess(logf, session)
	if session.status == contract.RequestStatusPending {
		session.status = contract.RequestStatusFailed
		session.errorSummary = &contract.ErrorSummary{
			Category:  "runtime",
			Code:      "request_incomplete",
			Message:   "request execution ended without a terminal status",
			Retryable: true,
		}
	}
	if session.status == contract.RequestStatusSucceeded {
		session.failFromHTTPError()
	}
	session.captureOutputID()
	session.captureOutputCursors()
	if session.networkAttemptOpen {
		session.closeEventKind(contract.RequestEventUpstream, session.status, session.completedSummary())
	}
	if session.privacyRestore != nil && session.privacyRestore.Enabled {
		session.addEvent(
			contract.RequestEventRestore,
			session.status,
			privacyRestoreSummaryText(*session.privacyRestore),
		)
	}
	session.settlePrivacyEvent()
	session.settleAcceptedEvent()
	session.addEvent(contract.RequestEventCompleted, session.status, session.completedSummary())
	session.closeOpenEvents(time.Now().UTC())
	completed := time.Now().UTC()
	latency := int(completed.Sub(session.startedAt).Milliseconds())
	if latency < 0 {
		latency = 0
	}
	audit := session.liveAuditSummary()
	pendingBlobs := make([]storage.AuditBlob, 0, 6)
	if blobs != nil {
		key, keyErr := session.prepareAuditKey(ctx, blobs, logf)
		if keyErr == nil && key != nil {
			if blob, ok := session.sealUnpersistedCapture(storage.AuditDirectionRequest, &session.requestCapture, key, completed, logf); ok {
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
			if blob, ok := session.sealUnpersistedCapture(storage.AuditDirectionUpstreamRequest, &session.upstreamRequestCapture, key, completed, logf); ok {
				pendingBlobs = append(pendingBlobs, blob)
				audit.UpstreamRequestBodyCaptured = true
				audit.UpstreamRequestBodyTruncated = session.upstreamRequestCapture.truncated
			}
			if blob, ok := session.sealCapture(storage.AuditDirectionUpstreamResponse, &session.upstreamResponseCapture, key, completed, logf); ok {
				pendingBlobs = append(pendingBlobs, blob)
				audit.UpstreamResponseContentCaptured = true
				audit.UpstreamResponseContentTruncated = session.upstreamResponseCapture.truncated
			}
			if blob, ok := session.sealUpstreamHTTPMeta(key, completed, logf); ok {
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
			continue
		}
		session.markAuditPersisted(blob.Direction)
	}
}

func (session *recordSession) prepareAuditKey(
	ctx context.Context,
	blobs AuditBlobPersister,
	logf func(string, ...any),
) ([]byte, error) {
	requestHasBytes := session.requestCapture.enabled && len(session.requestCapture.bytes) > 0
	responseHasBytes := session.responseCapture.enabled && len(session.responseCapture.bytes) > 0
	upstreamRequestHasBytes := session.upstreamRequestCapture.enabled && len(session.upstreamRequestCapture.bytes) > 0
	upstreamResponseHasBytes := session.upstreamResponseCapture.enabled && len(session.upstreamResponseCapture.bytes) > 0
	if !requestHasBytes && !responseHasBytes && !upstreamRequestHasBytes &&
		!upstreamResponseHasBytes && !session.httpMetaCaptured && !session.upstreamHTTPMetaCaptured {
		return nil, nil
	}
	key, err := blobs.GetOrCreateAuditKey(ctx)
	if err != nil {
		logRequestRecordFailure(logf, "audit_key", err)
		return nil, err
	}
	return key, nil
}

func (session *recordSession) prepareUpstreamAuditKey(
	ctx context.Context,
	blobs AuditBlobPersister,
	logf func(string, ...any),
) ([]byte, error) {
	upstreamRequestHasBytes := session.upstreamRequestCapture.enabled && len(session.upstreamRequestCapture.bytes) > 0
	upstreamResponseHasBytes := session.upstreamResponseCapture.enabled && len(session.upstreamResponseCapture.bytes) > 0
	if !upstreamRequestHasBytes && !upstreamResponseHasBytes && !session.upstreamHTTPMetaCaptured {
		return nil, nil
	}
	key, err := blobs.GetOrCreateAuditKey(ctx)
	if err != nil {
		logRequestRecordFailure(logf, "audit_key", err)
		return nil, err
	}
	return key, nil
}

func (session *recordSession) sealUnpersistedCapture(
	direction storage.AuditDirection,
	buffer *captureBuffer,
	key []byte,
	createdAt time.Time,
	logf func(string, ...any),
) (storage.AuditBlob, bool) {
	if session.auditPersisted(direction) {
		return storage.AuditBlob{}, false
	}
	return session.sealCapture(direction, buffer, key, createdAt, logf)
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

func (session *recordSession) sealUpstreamHTTPMeta(
	key []byte,
	createdAt time.Time,
	logf func(string, ...any),
) (storage.AuditBlob, bool) {
	if !session.upstreamHTTPMetaCaptured {
		return storage.AuditBlob{}, false
	}
	payload, err := json.Marshal(session.upstreamHTTPMeta)
	if err != nil {
		logRequestRecordFailure(logf, "upstream_http_meta_encode", err)
		return storage.AuditBlob{}, false
	}
	nonce, ciphertext, err := storage.SealAuditBlob(key, payload)
	if err != nil {
		logRequestRecordFailure(logf, "audit_encrypt", err)
		return storage.AuditBlob{}, false
	}
	return storage.AuditBlob{
		Direction:     storage.AuditDirectionUpstreamHTTPMeta,
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

func logIngressAccess(logf func(string, ...any), session *recordSession) {
	if session == nil {
		return
	}
	if logf == nil {
		logf = log.Printf
	}
	durationMs := time.Since(session.startedAt).Milliseconds()
	if durationMs < 0 {
		durationMs = 0
	}
	status := session.status
	if status == "" {
		status = contract.RequestStatusPending
	}
	protocol := session.classified.Protocol
	if protocol == "" {
		protocol = "unknown"
	}
	sessionID := session.sessionID
	if sessionID == "" {
		sessionID = contract.SessionID(session.id)
	}
	logf(
		"ingress %s %s %dms request=%s session=%s",
		protocol,
		status,
		durationMs,
		session.id,
		sessionID,
	)
}

// resolveSession attaches the request to an earlier session through the convo
// policy (explicit cursor, then echoed ids, then the assistant-text
// fingerprint) and derives its user turn. Lookup failures degrade to a fresh
// session so recording never blocks or fails the request.
func (session *recordSession) resolveSession(ctx context.Context, store RequestRecordStore, logf func(string, ...any)) {
	if session == nil {
		return
	}
	cursor := session.classified.PreviousResponseID
	if cursor == "" {
		cursor = session.classified.ConversationID
	}
	if cursor != "" {
		session.previousResponseID = cursor
	}
	summary := session.classified.Conversation
	if len(summary.ExplicitCursors) == 0 && cursor != "" {
		// Protocols without a convo adapter still honour the legacy cursor.
		summary.ExplicitCursors = []string{cursor}
	}

	lookupCtx, cancel := context.WithTimeout(ctx, sessionLookupTimeout)
	defer cancel()
	decision, err := conversationPolicy.Resolve(
		lookupCtx, summary, session.fingerprinter, sessionLookup(store, session.accessTokenID), time.Now(),
	)
	if err != nil {
		logRequestRecordFailure(logf, "session_lookup", err)
		// Resolve aborts on the first lookup error, so the partial decision
		// may lack Inbound and Turn; recompute both without storage.
		decision, _ = conversationPolicy.Resolve(ctx, summary, session.fingerprinter, nil, time.Now())
	}
	if decision.Matched {
		session.sessionID = contract.SessionID(decision.Match.SessionID)
		session.sessionLink = &contract.SessionLink{
			Kind:  contract.SessionCursorKind(decision.Match.Kind),
			Value: decision.Match.Value,
		}
	}
	session.turn = decision.Turn
	session.inboundCursors = contractCursors(decision.PersistentInbound())
	if session.sessionID == "" {
		session.sessionID = newSessionID()
	}
	session.inputPreview = session.classified.InputPreview
	session.addEvent(contract.RequestEventAccepted, contract.RequestStatusPending, session.acceptedSummary())
}

func (session *recordSession) completedSummary() string {
	if session == nil {
		return ""
	}
	if session.errorSummary != nil {
		if session.errorSummary.Message == "" {
			return session.errorSummary.Code
		}
		return session.errorSummary.Code + " · " + session.errorSummary.Message
	}
	parts := make([]string, 0, 3)
	if session.hasHTTPStatus {
		parts = append(parts, fmt.Sprintf("HTTP %d", session.httpStatus))
	} else if session.hasUpstreamHTTPStatus {
		parts = append(parts, fmt.Sprintf("HTTP %d", session.upstreamHTTPStatus))
	}
	if usage := session.attemptUsage(); usage != nil {
		parts = append(parts, fmt.Sprintf("%d → %d", usage.InputTokens, usage.OutputTokens))
	}
	if len(parts) == 0 {
		return string(session.status)
	}
	return strings.Join(parts, " · ")
}

func (session *recordSession) acceptedSummary() string {
	model := session.classified.Model
	if model == "" {
		model = "未指定模型"
	}
	summary := model + " · " + string(session.classified.Protocol)
	if session.inputPreview != "" {
		summary += " · " + session.inputPreview
	}
	return summary
}

func (session *recordSession) addEvent(kind contract.RequestEventKind, status contract.RequestStatus, summary string) {
	if session == nil {
		return
	}
	now := time.Now().UTC()
	session.closeOpenEvents(now)
	session.events = append(session.events, contract.RequestEvent{
		Kind:         kind,
		StartedAt:    now,
		Status:       status,
		Summary:      sanitizeSummary(summary),
		AttemptIndex: session.attemptIndex,
	})
}

func (session *recordSession) closeOpenEvents(ended time.Time) {
	if session == nil {
		return
	}
	for index := range session.events {
		if session.events[index].EndedAt == nil {
			end := ended
			session.events[index].EndedAt = &end
		}
	}
}

// settleAcceptedEvent gives the accepted phase a terminal status once the
// request is over. It is written pending while the call is in flight, and
// closeOpenEvents only stamps ended_at, so without this the phase stays
// pending forever and the desktop paints every finished call's client row as
// still running. Acceptance itself succeeded whatever the outcome was; the
// privacy, upstream and completed phases carry the failure.
func (session *recordSession) settleAcceptedEvent() {
	if session == nil {
		return
	}
	for index := range session.events {
		if session.events[index].Kind != contract.RequestEventAccepted {
			continue
		}
		if session.events[index].Status == contract.RequestStatusPending {
			session.events[index].Status = contract.RequestStatusSucceeded
		}
	}
}

func (session *recordSession) closeEventKind(kind contract.RequestEventKind, status contract.RequestStatus, summary string) {
	if session == nil {
		return
	}
	now := time.Now().UTC()
	for index := len(session.events) - 1; index >= 0; index-- {
		if session.events[index].Kind != kind || session.events[index].EndedAt != nil {
			continue
		}
		session.events[index].EndedAt = &now
		session.events[index].Status = status
		if summary != "" {
			session.events[index].Summary = sanitizeSummary(summary)
		}
		return
	}
	session.addEvent(kind, status, summary)
}

// notePrivacyDecision closes the phase beginPrivacyInspection opened, so its
// duration is the inspection itself. Paths that never inspect add a point.
func (session *recordSession) notePrivacyDecision(summary string, status contract.RequestStatus) {
	session.closeEventKind(contract.RequestEventPrivacy, status, summary)
}

// settlePrivacyEvent ends an inspection the request left before a decision,
// such as a client disconnect while the detector ran, with the final status.
func (session *recordSession) settlePrivacyEvent() {
	if session == nil {
		return
	}
	for index := range session.events {
		if session.events[index].Kind == contract.RequestEventPrivacy &&
			session.events[index].Status == contract.RequestStatusPending {
			session.events[index].Status = session.status
		}
	}
}

func (session *recordSession) captureOutputID() {
	if session == nil {
		return
	}
	if session.scanner != nil {
		_ = session.scanner.Usage()
		if id := session.scanner.OutputID(); id != "" {
			session.outputResponseID = id
		}
	}
	if session.outputResponseID == "" && session.upstreamScanner != nil {
		_ = session.upstreamScanner.Usage()
		if id := session.upstreamScanner.OutputID(); id != "" {
			session.outputResponseID = id
		}
	}
}

// captureOutputCursors turns what the client-facing response produced (its
// id, echoed ids, assistant-text fingerprint) into stored cursors. Only the
// client-facing scanner counts: after protocol conversion or privacy restore
// those are the bytes the client will replay.
func (session *recordSession) captureOutputCursors() {
	if session == nil || session.scanner == nil {
		return
	}
	summary := session.scanner.conversation()
	if summary.OutputID == "" {
		summary.OutputID = session.outputResponseID
	}
	session.outputCursors = contractCursors(conversationPolicy.OutputCursors(summary, session.fingerprinter))
}

func eventsForAttempt(events []contract.RequestEvent, attempt int) []contract.RequestEvent {
	filtered := make([]contract.RequestEvent, 0, 2)
	for _, event := range events {
		if event.AttemptIndex == attempt {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func eventsWithoutAttempt(events []contract.RequestEvent, attempt int) []contract.RequestEvent {
	filtered := make([]contract.RequestEvent, 0, len(events))
	for _, event := range events {
		if event.AttemptIndex != attempt {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func newSessionID() contract.SessionID {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return contract.SessionID("session_unavailable")
	}
	return contract.SessionID("session_" + hex.EncodeToString(value[:]))
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

func errorSummaryFromHTTPStatus(status int) contract.ErrorSummary {
	retryable := status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
	return errorSummaryFromInference(
		"upstream_http_error",
		fmt.Sprintf("upstream returned HTTP %d", status),
		retryable,
	)
}

func errorSummaryFromInference(code, message string, retryable bool) contract.ErrorSummary {
	category := "gateway"
	switch code {
	case "policy_blocked", "privacy_inspection_failed", "privacy_redaction_failed",
		"privacy_policy_unavailable", "safety_engine_unavailable":
		category = "privacy"
	case "invalid_access_token", "token_query_forbidden":
		category = "auth"
	case "upstream_unavailable", "upstream_timeout", "credential_unavailable",
		"upstream_stream_interrupted", "upstream_http_error":
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
