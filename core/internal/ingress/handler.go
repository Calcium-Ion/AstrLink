// Package ingress owns protocol recognition and execution at the public
// inference-plane HTTP boundary. Alpha only creates protocol-preserving native
// plans; it never calls RelayKit or silently changes an ingress protocol.
package ingress

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
	"github.com/QuantumNous/astrlink/core/internal/autotext"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/planner"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

type Forwarder interface {
	Forward(http.ResponseWriter, *http.Request, transport.Target) error
}

// AccessTokenAuthenticator resolves a presented local inference credential to
// its stable persistent identifier. Implementations must fail closed when the
// token is missing, deleted, or the persisted token state cannot be trusted.
type AccessTokenAuthenticator interface {
	AuthenticateAccessToken(context.Context, string) (contract.AccessTokenID, error)
}

type AccessTokenAuthenticatorFunc func(context.Context, string) (contract.AccessTokenID, error)

func (function AccessTokenAuthenticatorFunc) AuthenticateAccessToken(ctx context.Context, token string) (contract.AccessTokenID, error) {
	return function(ctx, token)
}

type Dependencies struct {
	ProxyCredentials         secretstore.SecretStore
	Resolver                 endpoint.Resolver
	Authorizer               endpoint.Authorizer
	Forwarder                Forwarder
	AccessTokenAuthenticator AccessTokenAuthenticator
	PrivacyFilter            privacy.Filter
	PolicyWarningReporter    PolicyWarningReporter
	RequestRecords           RequestRecordStore
	AuditSettings            AuditSettingsProvider
	AuditBlobs               AuditBlobPersister
	RecordLogger             func(string, ...any)
	AllowedHost              string
	// ResponseStartTimeout bounds how long one candidate may wait for
	// upstream response headers. Zero waits indefinitely so slow non-stream
	// generations are not cut off before the first byte. Discovery and
	// health probes keep their own bounds.
	ResponseStartTimeout time.Duration
	ConversionEngine     relaykitbridge.ConversionEngine
	// Classifier is optional. Missing, timeout, or empty text fail-open to
	// an empty category so auto routes flatten every configured target.
	Classifier Classifier
	// MaxConcurrentInspections limits how many requests may parse and
	// classify at once. Zero selects DefaultMaxConcurrentInspections.
	MaxConcurrentInspections int
	// MaxRequestBodyMiB limits incoming request bodies; zero means unlimited.
	MaxRequestBodyMiB uint32
}

type Classifier interface {
	Classify(context.Context, string) autoclassifier.Outcome
}

type ClassifierFunc func(context.Context, string) autoclassifier.Outcome

func (function ClassifierFunc) Classify(ctx context.Context, text string) autoclassifier.Outcome {
	return function(ctx, text)
}

type PolicyWarningReporter interface {
	ReportPolicyWarning(contract.ProtocolID, contract.ServiceID, string)
}

type PolicyWarningReporterFunc func(contract.ProtocolID, contract.ServiceID, string)

func (function PolicyWarningReporterFunc) ReportPolicyWarning(
	protocol contract.ProtocolID,
	endpointID contract.ServiceID,
	summary string,
) {
	function(protocol, endpointID, summary)
}

type Handler struct {
	proxyCredentials         secretstore.SecretStore
	affinities               responseAffinities
	resolver                 endpoint.Resolver
	authorizer               endpoint.Authorizer
	forwarder                Forwarder
	accessTokenAuthenticator AccessTokenAuthenticator
	privacyFilter            privacy.Filter
	policyWarningReporter    PolicyWarningReporter
	requestRecords           RequestRecordStore
	auditSettings            AuditSettingsProvider
	auditBlobs               AuditBlobPersister
	recordLogger             func(string, ...any)
	allowedHost              string
	responseStartTimeout     time.Duration
	metadataSlots            chan struct{}
	maxRequestBodyBytes      int64
	conversionEngine         relaykitbridge.ConversionEngine
	classifier               Classifier
	sessionFingerprints      sessionFingerprints
}

const (
	DefaultMaxConcurrentInspections    = 16
	MinMaxConcurrentInspections        = 4
	MaxMaxConcurrentInspections        = 128
	DefaultResponseStartTimeoutSeconds = 0
	MaxResponseStartTimeoutSeconds     = 86400
)

func metadataInspectionLimit(requested int) int {
	if requested <= 0 {
		return DefaultMaxConcurrentInspections
	}
	return requested
}

func ValidateMaxConcurrentInspections(n int) error {
	if n < MinMaxConcurrentInspections || n > MaxMaxConcurrentInspections {
		return fmt.Errorf(
			"max concurrent inspections must be between %d and %d",
			MinMaxConcurrentInspections,
			MaxMaxConcurrentInspections,
		)
	}
	return nil
}

func ValidateResponseStartTimeoutSeconds(n int) error {
	if n < DefaultResponseStartTimeoutSeconds || n > MaxResponseStartTimeoutSeconds {
		return fmt.Errorf(
			"response start timeout must be between %d and %d seconds",
			DefaultResponseStartTimeoutSeconds,
			MaxResponseStartTimeoutSeconds,
		)
	}
	return nil
}

const PolicyWarningHeader = "X-AstrLink-Policy-Warning"

const PrivacyWarningHeader = PolicyWarningHeader

type accessTokenIDContextKey struct{}

// AccessTokenIDFromContext returns the stable identifier of the persistent
// local access token that authenticated the inference request.
func AccessTokenIDFromContext(ctx context.Context) (contract.AccessTokenID, bool) {
	id, ok := ctx.Value(accessTokenIDContextKey{}).(contract.AccessTokenID)
	return id, ok && id != ""
}

func New() *Handler {
	return NewWithDependencies(Dependencies{})
}

// NewProduction requires the complete minimum loopback gate before a resolver
// backed by stored upstream credentials can be installed.
func NewProduction(dependencies Dependencies) (*Handler, error) {
	if dependencies.Resolver == nil || dependencies.Authorizer == nil {
		return nil, fmt.Errorf("production resolver and authorizer are required")
	}
	if dependencies.AccessTokenAuthenticator == nil {
		return nil, fmt.Errorf("production access token authenticator is required")
	}
	host, port, err := net.SplitHostPort(dependencies.AllowedHost)
	portNumber, portErr := strconv.ParseUint(port, 10, 16)
	if err != nil || portErr != nil || host != "127.0.0.1" || portNumber == 0 {
		return nil, fmt.Errorf("production allowed host must be a fixed 127.0.0.1 authority")
	}
	return NewWithDependencies(dependencies), nil
}

// NewWithDependencies is the composition seam used by deterministic tests and
// the fail-closed headless handler. Persistent production wiring must use
// NewProduction so stored credentials cannot bypass the minimum local gate.
func NewWithDependencies(dependencies Dependencies) *Handler {
	if dependencies.Resolver == nil {
		dependencies.Resolver = endpoint.UnavailableResolver{}
	}
	if dependencies.Authorizer == nil {
		dependencies.Authorizer = endpoint.NewSecretAuthorizer(nil)
	}
	if dependencies.Forwarder == nil {
		dependencies.Forwarder = transport.New(nil)
	}
	return &Handler{
		proxyCredentials: dependencies.ProxyCredentials,
		resolver:         dependencies.Resolver, authorizer: dependencies.Authorizer, forwarder: dependencies.Forwarder,
		accessTokenAuthenticator: dependencies.AccessTokenAuthenticator, privacyFilter: dependencies.PrivacyFilter,
		policyWarningReporter: dependencies.PolicyWarningReporter,
		requestRecords:        dependencies.RequestRecords,
		auditSettings:         dependencies.AuditSettings,
		auditBlobs:            dependencies.AuditBlobs,
		recordLogger:          dependencies.RecordLogger,
		allowedHost:           dependencies.AllowedHost,
		responseStartTimeout:  dependencies.ResponseStartTimeout,
		metadataSlots:         make(chan struct{}, metadataInspectionLimit(dependencies.MaxConcurrentInspections)),
		maxRequestBodyBytes:   int64(dependencies.MaxRequestBodyMiB) << 20,
		conversionEngine:      dependencies.ConversionEngine,
		classifier:            dependencies.Classifier,
	}
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	// The policy warning header is owned by this local response boundary.
	// Strip any client-supplied value so it cannot be spoofed upstream.
	request.Header.Del(PolicyWarningHeader)
	if !handler.allowInferenceBoundary(writer, request) {
		return
	}
	if isResponsesWebSocket(request) {
		handler.serveResponsesWebSocket(writer, request)
		return
	}
	classified, finishMetadata, err := handler.classify(request)
	defer finishMetadata()
	if request.Context().Err() != nil {
		return
	}
	if errors.Is(err, errProtocolPathNotFound) {
		handler.writeRecordedInferenceError(writer, request, http.StatusNotFound, "not_found", "inference endpoint not found", false, nil)
		return
	}
	var methodErr methodNotAllowedError
	if errors.As(err, &methodErr) {
		writer.Header().Set("Allow", methodErr.allow)
		handler.writeRecordedInferenceError(writer, request, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed for this inference endpoint", false, nil)
		return
	}
	if errors.Is(err, errMetadataTooLarge) {
		handler.writeRecordedInferenceError(writer, request, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the configured size limit", false, nil)
		return
	}
	if errors.Is(err, errUnsupportedContentEncoding) {
		handler.writeRecordedInferenceError(writer, request, http.StatusUnsupportedMediaType, "unsupported_content_encoding", "encoded inference request bodies are not supported", false, nil)
		return
	}
	if err != nil {
		handler.writeRecordedInferenceError(writer, request, http.StatusBadRequest, "invalid_request", "request metadata could not be identified", false, nil)
		return
	}

	session := handler.startRecordSession(request, classified)
	session.persistPending(request.Context(), handler.requestRecords, handler.recordLogger)
	outWriter := session.wrap(writer)
	request = request.WithContext(withRecordSession(request.Context(), session))
	defer func() {
		if request.Context().Err() != nil {
			session.noteCancelled()
		}
		// Never delay or fail the client response on persistence errors.
		session.finish(context.Background(), handler.requestRecords, handler.auditBlobs, handler.recordLogger)
	}()

	if classified.Model == contract.AstrLinkAutoModelID {
		session.captureUnreadRequestBody(request)
		writeInferenceError(outWriter, http.StatusGone, "routing_feature_retired", "astrlink/auto is retired; request an explicit model", false, nil)
		session.noteFailed(errorSummaryFromInference("routing_feature_retired", "automatic routing is retired", false))
		return
	}
	category := ""
	session.channelBinding = handler.prepareChannelBinding(request.Context(), session)
	candidates, err := handler.resolveCandidates(request.Context(), endpoint.ResolveRequest{
		Protocol:      classified.Protocol,
		Model:         classified.Model,
		Streaming:     classified.Streaming,
		Category:      category,
		Continuation:  classified.PreviousResponseID != "",
		AllCandidates: session.channelBinding != nil || responsesWSTurnFromContext(request.Context()) != nil,
	})
	if err != nil {
		// No attempt will read the body, so capture it for the audit now.
		session.captureUnreadRequestBody(request)
		var unhealthy *endpoint.UnhealthyCandidatesError
		if errors.As(err, &unhealthy) {
			for _, id := range unhealthy.Services {
				session.noteCandidateRejected(id, "circuit_open")
			}
		}
		handler.writeResolveError(outWriter, request, classified, err)
		return
	}
	if turn := responsesWSTurnFromContext(request.Context()); turn != nil {
		candidates = turn.session.filterCandidates(classified.Model, candidates)
		if len(candidates) == 0 {
			writeInferenceError(outWriter, http.StatusUnprocessableEntity, "responses_websocket_unavailable", "no enabled API provider supports native Responses WebSocket for this model and connection", false, nil)
			session.noteFailed(errorSummaryFromInference("responses_websocket_unavailable", "no enabled API provider supports Responses WebSocket for this connection", false))
			return
		}
	}
	candidates, err = handler.bindResponseAffinity(request.Context(), classified, candidates)
	if err != nil {
		writeInferenceError(outWriter, http.StatusConflict, "response_affinity_unavailable", err.Error(), false, nil)
		session.noteFailed(errorSummaryFromInference("response_affinity_unavailable", err.Error(), false))
		return
	}
	if classified.Protocol.IsModelDiscovery() {
		handler.aggregateModelDiscovery(outWriter, request, classified, candidates)
		return
	}
	candidates = handler.preferChannelBinding(request, session, candidates)
	if len(candidates) > 1 && candidates[0].Failover != nil && !candidates[0].Failover.Enabled {
		candidates = candidates[:1]
	}
	handler.executeCandidates(outWriter, request, classified, candidates)
}

var errPrivacyBlocked = errors.New("privacy policy blocked request")

// privacyOutcome carries what the response path needs from the request-side
// decision: the mapping to reverse and how far restoration may reach.
type privacyOutcome struct {
	redactions    []privacy.Redaction
	toolArguments bool
}

func (handler *Handler) applyPrivacy(
	writer http.ResponseWriter,
	request *http.Request,
	classified Request,
	endpointID contract.ServiceID,
) (func(), privacyOutcome, error) {
	session := recordSessionFromContext(request.Context())
	session.beginPrivacyAttempt()
	if handler.privacyFilter == nil {
		session.notePrivacyDecision("allow", contract.RequestStatusSucceeded)
		return func() {}, privacyOutcome{}, nil
	}
	accessTokenID, _ := AccessTokenIDFromContext(request.Context())
	policy, err := handler.privacyFilter.ResolvePolicy(request.Context(), privacy.Scope{
		Protocol:      classified.Protocol,
		Model:         classified.Model,
		ServiceID:     endpointID,
		AccessTokenID: accessTokenID,
	})
	if err != nil {
		return func() {}, privacyOutcome{}, err
	}
	if !policy.Enabled {
		// This branch deliberately does not read, replace, or otherwise touch
		// request.Body. Disabled policy preserves the original byte path.
		session.notePrivacyDecision("allow", contract.RequestStatusSucceeded)
		return func() {}, privacyOutcome{}, nil
	}
	encoding := strings.ToLower(strings.TrimSpace(request.Header.Get("Content-Encoding")))
	if encoding != "" && encoding != "identity" {
		return func() {}, privacyOutcome{}, errUnsupportedContentEncoding
	}

	body, buffered, err := handler.bufferPrivacyBody(request)
	if err != nil {
		return func() {}, privacyOutcome{}, err
	}
	finish := func() {}
	if buffered != nil {
		finish = buffered.Close
	}
	session.beginPrivacyInspection(request.Context(), privacyInspectionSummary(policy.Mode, len(body)))
	result, err := handler.privacyFilter.Inspect(
		request.Context(),
		policy,
		classified.Protocol,
		body,
	)
	if err != nil {
		return finish, privacyOutcome{}, err
	}
	switch result.Decision {
	case privacy.DecisionAllow:
		session.notePrivacyDecision("allow", contract.RequestStatusSucceeded)
		return finish, privacyOutcome{}, nil
	case privacy.DecisionWarn:
		// This is a response-only signal. Never add it to request.Header, where
		// it could cross the upstream boundary.
		summary := privacy.WarningSummary(result.Findings)
		writer.Header().Set(PolicyWarningHeader, summary)
		if handler.policyWarningReporter != nil {
			handler.policyWarningReporter.ReportPolicyWarning(
				classified.Protocol,
				endpointID,
				summary,
			)
		}
		session.notePrivacyDecision("warn", contract.RequestStatusSucceeded)
		return finish, privacyOutcome{}, nil
	case privacy.DecisionBlock:
		session.notePrivacyDecision("block", contract.RequestStatusBlocked)
		return finish, privacyOutcome{}, errPrivacyBlocked
	case privacy.DecisionRedact:
		if buffered == nil {
			return finish, privacyOutcome{}, privacy.ErrUnsafeRewrite
		}
		if handler.maxRequestBodyBytes > 0 && int64(len(result.Body)) > handler.maxRequestBodyBytes {
			return finish, privacyOutcome{}, errMetadataTooLarge
		}
		buffered.Replace(result.Body)
		mappingCount := uniqueRedactionMappingCount(result.Redactions)
		session.notePrivacyMapping(
			policy.ResponseRestore,
			mappingCount,
			privacyHitCounts(result.Redactions),
		)
		session.notePrivacyDecision(
			privacyDecisionSummary(mappingCount, result.NoticeInjected),
			contract.RequestStatusSucceeded,
		)
		if !policy.ResponseRestore || len(result.Redactions) == 0 {
			return finish, privacyOutcome{}, nil
		}
		return finish, privacyOutcome{
			redactions:    result.Redactions,
			toolArguments: policy.RestoreToolArguments,
		}, nil
	default:
		return finish, privacyOutcome{}, privacy.ErrPolicyUnavailable
	}
}

// privacyInspectionSummary names the detector and input size, never content.
func privacyInspectionSummary(mode privacy.Mode, size int) string {
	summary := "inspecting · " + formatBodySize(size)
	if mode != "" {
		summary = string(mode) + " · " + summary
	}
	return summary
}

func formatBodySize(size int) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(size)/(1024*1024))
}

// detectorFailure names how the detector failed for the local record only;
// the client reply stays generic. A timeout on a long agent transcript and a
// worker that never started need different fixes.
func detectorFailure(err error) (detail, message string) {
	switch {
	case errors.Is(err, privacy.ErrDetectorTimeout):
		return "detector_timeout", "local privacy detector timed out"
	case errors.Is(err, privacy.ErrDetectorLimit):
		return "detector_limit", "local privacy detector input limit exceeded"
	default:
		return "detector_unavailable", "local privacy detector is unavailable"
	}
}

func privacyDecisionSummary(mappingCount int, noticeInjected bool) string {
	if noticeInjected {
		return fmt.Sprintf("redact · %d · notice", mappingCount)
	}
	return fmt.Sprintf("redact · %d", mappingCount)
}

func (handler *Handler) writePrivacyError(writer http.ResponseWriter, request *http.Request, err error) {
	session := recordSessionFromContext(request.Context())
	switch {
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, errMetadataTooLarge):
		writeInferenceError(writer, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the configured size limit", false, nil)
		session.notePrivacyDecision("request_too_large", contract.RequestStatusFailed)
		session.noteFailed(errorSummaryFromInference("request_too_large", "request body exceeds the configured size limit", false))
	case errors.Is(err, errUnsupportedContentEncoding):
		writeInferenceError(writer, http.StatusUnsupportedMediaType, "unsupported_content_encoding", "encoded inference request bodies cannot be inspected safely", false, nil)
		session.notePrivacyDecision("unsupported_content_encoding", contract.RequestStatusFailed)
		session.noteFailed(errorSummaryFromInference("unsupported_content_encoding", "encoded inference request bodies cannot be inspected safely", false))
	case errors.Is(err, privacy.ErrPolicyUnavailable):
		writeInferenceError(writer, http.StatusServiceUnavailable, "privacy_policy_unavailable", "privacy policy could not be resolved", true, nil)
		session.notePrivacyDecision("privacy_policy_unavailable", contract.RequestStatusFailed)
		session.noteFailed(errorSummaryFromInference("privacy_policy_unavailable", "privacy policy could not be resolved", true))
	case errors.Is(err, privacy.ErrDetectorUnavailable),
		errors.Is(err, privacy.ErrDetectorLimit),
		errors.Is(err, privacy.ErrDetectorTimeout):
		detail, message := detectorFailure(err)
		writeInferenceError(writer, http.StatusServiceUnavailable, "safety_engine_unavailable", "local safety engine is unavailable", true, nil)
		session.notePrivacyDecision("safety_engine_unavailable · "+detail, contract.RequestStatusFailed)
		session.noteFailed(errorSummaryFromInference("safety_engine_unavailable", message, true))
	case errors.Is(err, privacy.ErrUnsafeInput):
		// These content-dependent processing failures are non-retryable, but
		// are not policy decisions. Only an explicit block is reported as 403.
		writeInferenceError(writer, http.StatusUnprocessableEntity, "privacy_inspection_failed", "local privacy inspection failed: request body could not be inspected safely", false, nil)
		session.notePrivacyDecision("privacy_inspection_failed", contract.RequestStatusFailed)
		session.noteFailed(errorSummaryFromInference("privacy_inspection_failed", "local privacy inspection failed: request body could not be inspected safely", false))
	case errors.Is(err, privacy.ErrUnsafeRewrite):
		writeInferenceError(writer, http.StatusUnprocessableEntity, "privacy_redaction_failed", "local privacy redaction failed: request body could not be rewritten safely", false, nil)
		session.notePrivacyDecision("privacy_redaction_failed", contract.RequestStatusFailed)
		session.noteFailed(errorSummaryFromInference("privacy_redaction_failed", "local privacy redaction failed: request body could not be rewritten safely", false))
	case errors.Is(err, errPrivacyBlocked):
		writeInferenceError(writer, http.StatusForbidden, "policy_blocked", "request was blocked by local privacy policy", false, nil)
		session.noteBlocked(errorSummaryFromInference("policy_blocked", "request was blocked by local privacy policy", false))
	default:
		writeInferenceError(writer, http.StatusServiceUnavailable, "safety_engine_unavailable", "local safety engine is unavailable", true, nil)
		session.notePrivacyDecision("safety_engine_unavailable", contract.RequestStatusFailed)
		session.noteFailed(errorSummaryFromInference("safety_engine_unavailable", "local safety engine is unavailable", true))
	}
}

type privacyBufferedBody struct {
	request *http.Request
	current *metadataPermitBody
}

func (handler *Handler) bufferPrivacyBody(request *http.Request) ([]byte, *privacyBufferedBody, error) {
	if request.Body == nil || request.Body == http.NoBody {
		return nil, nil, nil
	}

	var release func()
	if existing, ok := request.Body.(*metadataPermitBody); ok {
		release = existing.transferPermit()
	} else {
		var err error
		release, err = handler.acquireMetadataPermit(request.Context())
		if err != nil {
			return nil, nil, err
		}
	}
	original := request.Body
	body, err := readRequestBody(original, handler.maxRequestBodyBytes)
	if errors.Is(err, errMetadataTooLarge) {
		release()
		_ = original.Close()
		return nil, nil, errMetadataTooLarge
	}
	if err != nil {
		release()
		_ = original.Close()
		return nil, nil, privacy.ErrUnsafeInput
	}
	if err := original.Close(); err != nil {
		release()
		return nil, nil, privacy.ErrUnsafeInput
	}
	buffered := &privacyBufferedBody{request: request}
	buffered.current = &metadataPermitBody{
		ReadCloser: io.NopCloser(bytes.NewReader(body)),
	}
	request.Body = buffered.current
	// The body is already in memory. Free the inspection slot before
	// Detect/forward so concurrent streams are not limited by the inspect cap.
	release()
	return body, buffered, nil
}

func (handler *Handler) acquireMetadataPermit(ctx context.Context) (func(), error) {
	select {
	case handler.metadataSlots <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() { <-handler.metadataSlots })
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (body *privacyBufferedBody) Replace(replacement []byte) {
	release := body.current.transferPermit()
	_ = body.current.Close()
	copied := replacement
	body.current = &metadataPermitBody{
		ReadCloser: io.NopCloser(bytes.NewReader(copied)),
		release:    release,
	}
	body.request.Body = body.current
	body.request.ContentLength = int64(len(copied))
	body.request.Header.Set("Content-Length", strconv.Itoa(len(copied)))
	body.request.TransferEncoding = nil
	body.request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(copied)), nil
	}
}

func (body *privacyBufferedBody) Close() {
	if body != nil && body.current != nil {
		_ = body.current.Close()
	}
}

func (handler *Handler) allowInferenceBoundary(writer http.ResponseWriter, request *http.Request) bool {
	if hasBrowserOrigin(request.Header) {
		handler.writeRecordedInferenceError(writer, request, http.StatusForbidden, "origin_forbidden", "browser origins cannot call the inference plane", false, nil)
		return false
	}
	if handler.allowedHost != "" && request.Host != handler.allowedHost {
		handler.writeRecordedInferenceError(writer, request, http.StatusMisdirectedRequest, "host_forbidden", "request Host does not match the inference listener", false, nil)
		return false
	}
	if handler.accessTokenAuthenticator != nil {
		if request.URL.Query().Has("key") || request.URL.Query().Has("api_key") || request.URL.Query().Has("access_token") {
			handler.writeRecordedInferenceError(writer, request, http.StatusUnauthorized, "token_query_forbidden", "local access tokens are not accepted in query parameters", false, nil)
			return false
		}
		token, ok := localClientCredential(request.Header)
		if !ok {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="astrlink-inference"`)
			handler.writeRecordedInferenceError(writer, request, http.StatusUnauthorized, "invalid_access_token", "a valid local access token is required", false, nil)
			return false
		}
		tokenID, err := handler.accessTokenAuthenticator.AuthenticateAccessToken(request.Context(), token)
		if err != nil || tokenID == "" {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="astrlink-inference"`)
			handler.writeRecordedInferenceError(writer, request, http.StatusUnauthorized, "invalid_access_token", "a valid local access token is required", false, nil)
			return false
		}
		*request = *request.WithContext(context.WithValue(request.Context(), accessTokenIDContextKey{}, tokenID))
	}
	route, _, known := matchProtocolRoute(request.URL.Path)
	if !known || route.method != http.MethodPost || request.Method != http.MethodPost {
		return true
	}
	contentType := request.Header.Get("Content-Type")
	if contentType == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		handler.writeRecordedInferenceError(writer, request, http.StatusUnsupportedMediaType, "unsupported_media_type", "inference request bodies must use application/json", false, nil)
		return false
	}
	return true
}

func (handler *Handler) loadAuditSettings(ctx context.Context) contract.AuditSettings {
	settings := contract.DefaultAuditSettings()
	if handler.auditSettings == nil {
		return settings
	}
	loaded, err := handler.auditSettings.GetAuditSettings(ctx)
	if err != nil {
		if handler.recordLogger != nil {
			handler.recordLogger("audit settings load failed: %v", err)
		}
		return settings
	}
	return loaded
}

func (handler *Handler) startRecordSession(request *http.Request, classified Request) *recordSession {
	accessTokenID, _ := AccessTokenIDFromContext(request.Context())
	session := newRecordSession(classified, accessTokenID, handler.loadAuditSettings(request.Context()))
	session.bindPersistence(handler.requestRecords, handler.auditBlobs, handler.recordLogger)
	if handler.requestRecords != nil {
		session.fingerprinter = handler.sessionFingerprints.get(request.Context(), handler.auditBlobs, handler.recordLogger)
	}
	session.resolveSession(request.Context(), handler.requestRecords, handler.recordLogger)
	// Snapshot the redacted HTTP envelope before any privacy or routing
	// rewrite mutates the request (ADR 0008).
	session.captureHTTPRequestMeta(request)
	session.attachRequestCapture(request)
	return session
}

// writeRecordedInferenceError writes a local inference-plane error and, when
// the path maps to a known protocol, persists a failed request record so
// boundary failures such as invalid_access_token appear in the operator list.
func (handler *Handler) writeRecordedInferenceError(
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	code, message string,
	retryable bool,
	details []errorDetail,
) {
	classified, ok := classifyFromPath(request)
	if !ok {
		writeInferenceError(writer, status, code, message, retryable, details)
		return
	}
	session := handler.startRecordSession(request, classified)
	writeInferenceError(session.wrap(writer), status, code, message, retryable, details)
	if request.Context().Err() != nil {
		session.noteCancelled()
	} else {
		session.noteFailed(errorSummaryFromInference(code, message, retryable))
	}
	session.finish(context.Background(), handler.requestRecords, handler.auditBlobs, handler.recordLogger)
}

func hasBrowserOrigin(header http.Header) bool {
	for _, value := range header.Values("Origin") {
		if value != "" {
			return true
		}
	}
	return false
}

func localClientCredential(header http.Header) (string, bool) {
	credentials := make([]string, 0, 3)
	if values := header.Values("Authorization"); len(values) > 0 {
		if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
			return "", false
		}
		credentials = append(credentials, strings.TrimPrefix(values[0], "Bearer "))
	}
	for _, name := range []string{"X-Api-Key", "X-Goog-Api-Key"} {
		if values := header.Values(name); len(values) > 0 {
			if len(values) != 1 {
				return "", false
			}
			credentials = append(credentials, values[0])
		}
	}
	if len(credentials) == 0 || credentials[0] == "" {
		return "", false
	}
	// Anthropic-compatible clients (Cherry Studio, official SDK) commonly send
	// the same token as both Authorization and X-Api-Key. That is one
	// credential, not an ambiguous pair. Differing values stay rejected.
	for _, credential := range credentials[1:] {
		if credential != credentials[0] {
			return "", false
		}
	}
	return credentials[0], true
}

func (handler *Handler) autoCategory(ctx context.Context, classified Request) string {
	if classified.Model != contract.AstrLinkAutoModelID {
		return ""
	}
	if handler.classifier == nil || autotext.IsBlank(classified.lastUserText) {
		return ""
	}
	outcome := handler.classifier.Classify(ctx, classified.lastUserText)
	if !outcome.OK() {
		return ""
	}
	return outcome.Category
}

func (handler *Handler) classify(request *http.Request) (Request, func(), error) {
	release, err := handler.acquireMetadataPermit(request.Context())
	if err != nil {
		return Request{}, func() {}, err
	}
	classified, classifyErr := classify(request, handler.maxRequestBodyBytes)
	if classifyErr != nil {
		if replay, buffered := request.Body.(*replayReadCloser); buffered {
			_ = replay.Close()
		}
		release()
		return Request{}, func() {}, classifyErr
	}
	// Inspection is done. Holding the slot through resolve + upstream
	// streaming caps the whole gateway at four in-flight AI requests.
	release()
	return classified, func() {}, nil
}

// metadataPermitBody keeps an inspection permit until the body is consumed,
// closed, or the caller transfers/releases it. The permit only bounds
// concurrent parse/inspect work, not in-flight upstream streams.
type metadataPermitBody struct {
	io.ReadCloser
	releaseMu sync.Mutex
	release   func()
	closeOnce sync.Once
	closeErr  error
}

func (body *metadataPermitBody) Read(buffer []byte) (int, error) {
	read, err := body.ReadCloser.Read(buffer)
	if errors.Is(err, io.EOF) {
		body.releasePermit()
	}
	return read, err
}

func (body *metadataPermitBody) Close() error {
	body.closeOnce.Do(func() {
		body.closeErr = body.ReadCloser.Close()
		body.releasePermit()
	})
	return body.closeErr
}

func (body *metadataPermitBody) releasePermit() {
	body.releaseMu.Lock()
	release := body.release
	body.release = nil
	body.releaseMu.Unlock()
	if release != nil {
		release()
	}
}

func (body *metadataPermitBody) transferPermit() func() {
	body.releaseMu.Lock()
	release := body.release
	body.release = nil
	body.releaseMu.Unlock()
	if release == nil {
		return func() {}
	}
	return release
}

func (handler *Handler) writeResolveError(writer http.ResponseWriter, request *http.Request, classified Request, err error) {
	if request.Context().Err() != nil {
		return
	}
	session := recordSessionFromContext(request.Context())
	var capabilityErr *endpoint.CapabilityUnavailableError
	switch {
	case errors.As(err, &capabilityErr):
		writeMissingCapability(
			writer,
			capabilityErr.Protocol,
			capabilityErr.Model,
			capabilityErr.Modes,
			capabilityErr.Streaming,
		)
		session.noteFailed(errorSummaryFromInference(
			"missing_protocol_capability",
			"no endpoint provides the requested protocol capability",
			false,
		))
	case errors.Is(err, endpoint.ErrNoEndpoint):
		writeMissingCapability(
			writer,
			classified.Protocol,
			classified.Model,
			[]contract.CapabilityMode{
				contract.CapabilityModeNative,
				contract.CapabilityModeDelegated,
			},
			classified.Streaming,
		)
		session.noteFailed(errorSummaryFromInference(
			"missing_protocol_capability",
			"no endpoint provides the requested protocol capability",
			false,
		))
	case errors.Is(err, endpoint.ErrNoHealthyEndpoint):
		writeInferenceError(writer, http.StatusServiceUnavailable, "upstream_unavailable", fmt.Sprintf(
			"all endpoints providing protocol %q in native or delegated mode with streaming=%t are temporarily unhealthy",
			classified.Protocol,
			classified.Streaming,
		), true, []errorDetail{{
			Protocol:          string(classified.Protocol),
			Reason:            fmt.Sprintf("required mode=native or delegated; streaming=%t", classified.Streaming),
			RequiredPlanTypes: []string{string(contract.PlanTypeNative), string(contract.PlanTypeDelegated)},
		}})
		session.noteFailed(errorSummaryFromInference("upstream_unavailable", "all capable endpoints are temporarily unhealthy", true))
	case errors.Is(err, endpoint.ErrUnavailable):
		writeInferenceError(writer, http.StatusServiceUnavailable, "endpoint_resolver_unavailable", "upstream endpoint configuration is not available yet", true, []errorDetail{{
			Protocol: string(classified.Protocol), RequiredPlanTypes: []string{string(contract.PlanTypeNative), string(contract.PlanTypeDelegated)},
		}})
		session.noteFailed(errorSummaryFromInference("endpoint_resolver_unavailable", "upstream endpoint configuration is not available yet", true))
	default:
		writeInferenceError(writer, http.StatusServiceUnavailable, "endpoint_resolver_unavailable", "upstream endpoint resolution failed", true, []errorDetail{{
			Protocol: string(classified.Protocol), RequiredPlanTypes: []string{string(contract.PlanTypeNative), string(contract.PlanTypeDelegated)},
		}})
		session.noteFailed(errorSummaryFromInference("endpoint_resolver_unavailable", "upstream endpoint resolution failed", true))
	}
}

func writeMissingCapability(
	writer http.ResponseWriter,
	protocol contract.ProtocolID,
	model string,
	modes []contract.CapabilityMode,
	streaming bool,
) {
	modeDescription, planTypes := capabilityModeDescription(modes)
	message := fmt.Sprintf(
		"no enabled endpoint provides protocol %q in %s mode with streaming=%t",
		protocol,
		modeDescription,
		streaming,
	)
	reason := fmt.Sprintf("required mode=%s; streaming=%t", modeDescription, streaming)
	if model != "" {
		message += fmt.Sprintf(" for model %q", model)
		reason += fmt.Sprintf("; model=%q", model)
	}
	writeInferenceError(
		writer,
		http.StatusUnprocessableEntity,
		"missing_protocol_capability",
		message,
		false,
		[]errorDetail{{
			Protocol:          string(protocol),
			Reason:            reason,
			RequiredPlanTypes: planTypes,
		}},
	)
}

func writePlannerCapability(writer http.ResponseWriter, capability *planner.CapabilityUnavailableError) {
	if capability == nil {
		writeMissingCapability(
			writer,
			"",
			"",
			[]contract.CapabilityMode{
				contract.CapabilityModeNative,
				contract.CapabilityModeDelegated,
			},
			false,
		)
		return
	}
	writeMissingCapability(
		writer,
		capability.Protocol,
		"",
		[]contract.CapabilityMode{capability.Mode},
		capability.Streaming,
	)
}

func capabilityModeDescription(modes []contract.CapabilityMode) (string, []string) {
	description := make([]string, 0, len(modes))
	planTypes := make([]string, 0, len(modes))
	seen := make(map[contract.CapabilityMode]struct{}, len(modes))
	for _, mode := range modes {
		if _, duplicate := seen[mode]; duplicate || !mode.Valid() {
			continue
		}
		seen[mode] = struct{}{}
		description = append(description, string(mode))
		planTypes = append(planTypes, string(mode))
	}
	if len(description) == 0 {
		return "native or delegated", []string{
			string(contract.PlanTypeNative),
			string(contract.PlanTypeDelegated),
		}
	}
	return strings.Join(description, " or "), planTypes
}

type errorEnvelope struct {
	Error     inferenceError `json:"error"`
	RequestID string         `json:"request_id"`
}

type inferenceError struct {
	Code      string        `json:"code"`
	Message   string        `json:"message"`
	Retryable bool          `json:"retryable"`
	Details   []errorDetail `json:"details"`
}

type errorDetail struct {
	Protocol          string   `json:"protocol,omitempty"`
	ServiceID         string   `json:"service_id,omitempty"`
	Reason            string   `json:"reason,omitempty"`
	RequiredPlanTypes []string `json:"required_plan_types,omitempty"`
}

func writeInferenceError(writer http.ResponseWriter, status int, code, message string, retryable bool, details []errorDetail) {
	if details == nil {
		details = []errorDetail{}
	}
	header := writer.Header()
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Type", "application/json")
	header.Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(errorEnvelope{
		Error:     inferenceError{Code: code, Message: message, Retryable: retryable, Details: details},
		RequestID: newRequestID(),
	})
}

func newRequestID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "req_unavailable"
	}
	return "req_" + hex.EncodeToString(value[:])
}
