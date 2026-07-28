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
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/planner"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
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
	ResponseStartTimeout     time.Duration
	ConversionEngine         relaykitbridge.ConversionEngine
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
	conversionEngine         relaykitbridge.ConversionEngine
}

const maxConcurrentMetadataInspections = 4
const defaultResponseStartTimeout = 60 * time.Second

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
	if dependencies.ResponseStartTimeout <= 0 {
		dependencies.ResponseStartTimeout = defaultResponseStartTimeout
	}
	return &Handler{
		resolver: dependencies.Resolver, authorizer: dependencies.Authorizer, forwarder: dependencies.Forwarder,
		accessTokenAuthenticator: dependencies.AccessTokenAuthenticator, privacyFilter: dependencies.PrivacyFilter,
		policyWarningReporter: dependencies.PolicyWarningReporter,
		requestRecords:        dependencies.RequestRecords,
		auditSettings:         dependencies.AuditSettings,
		auditBlobs:            dependencies.AuditBlobs,
		recordLogger:          dependencies.RecordLogger,
		allowedHost:           dependencies.AllowedHost,
		responseStartTimeout:  dependencies.ResponseStartTimeout,
		metadataSlots:         make(chan struct{}, maxConcurrentMetadataInspections),
		conversionEngine:      dependencies.ConversionEngine,
	}
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	// The policy warning header is owned by this local response boundary.
	// Strip any client-supplied value so it cannot be spoofed upstream.
	request.Header.Del(PolicyWarningHeader)
	if !handler.allowInferenceBoundary(writer, request) {
		return
	}
	classified, finishMetadata, err := handler.classify(request)
	defer finishMetadata()
	if request.Context().Err() != nil {
		return
	}
	if errors.Is(err, errProtocolPathNotFound) {
		writeInferenceError(writer, http.StatusNotFound, "not_found", "inference endpoint not found", false, nil)
		return
	}
	var methodErr methodNotAllowedError
	if errors.As(err, &methodErr) {
		writer.Header().Set("Allow", methodErr.allow)
		writeInferenceError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed for this inference endpoint", false, nil)
		return
	}
	if errors.Is(err, errMetadataTooLarge) {
		writeInferenceError(writer, http.StatusRequestEntityTooLarge, "request_too_large", "request metadata exceeds the inspection limit", false, nil)
		return
	}
	if errors.Is(err, errUnsupportedContentEncoding) {
		writeInferenceError(writer, http.StatusUnsupportedMediaType, "unsupported_content_encoding", "encoded inference request bodies are not supported", false, nil)
		return
	}
	if err != nil {
		writeInferenceError(writer, http.StatusBadRequest, "invalid_request", "request metadata could not be identified", false, nil)
		return
	}

	accessTokenID, _ := AccessTokenIDFromContext(request.Context())
	auditSettings := contract.DefaultAuditSettings()
	if handler.auditSettings != nil {
		if loaded, err := handler.auditSettings.GetAuditSettings(request.Context()); err != nil {
			if handler.recordLogger != nil {
				handler.recordLogger("audit settings load failed: %v", err)
			}
		} else {
			auditSettings = loaded
		}
	}
	session := newRecordSession(classified, accessTokenID, auditSettings)
	// Snapshot the redacted HTTP envelope before any privacy or routing
	// rewrite mutates the request (ADR 0008).
	session.captureHTTPRequestMeta(request)
	session.persistPending(request.Context(), handler.requestRecords, handler.recordLogger)
	session.attachRequestCapture(request)
	outWriter := session.wrap(writer)
	request = request.WithContext(withRecordSession(request.Context(), session))
	defer func() {
		if request.Context().Err() != nil {
			session.noteCancelled()
		}
		// Never delay or fail the client response on persistence errors.
		session.finish(context.Background(), handler.requestRecords, handler.auditBlobs, handler.recordLogger)
	}()

	candidates, err := handler.resolveCandidates(request.Context(), endpoint.ResolveRequest{
		Protocol: classified.Protocol, Model: classified.Model, Streaming: classified.Streaming,
	})
	if err != nil {
		handler.writeResolveError(outWriter, request, classified, err)
		return
	}
	if classified.Protocol.IsModelDiscovery() {
		handler.aggregateModelDiscovery(outWriter, request, classified, candidates)
		return
	}
	handler.executeCandidates(outWriter, request, classified, candidates)
}

var errPrivacyBlocked = errors.New("privacy policy blocked request")

func (handler *Handler) applyPrivacy(
	writer http.ResponseWriter,
	request *http.Request,
	classified Request,
	endpointID contract.ServiceID,
) (func(), []privacy.Redaction, error) {
	if handler.privacyFilter == nil {
		return func() {}, nil, nil
	}
	accessTokenID, _ := AccessTokenIDFromContext(request.Context())
	policy, err := handler.privacyFilter.ResolvePolicy(request.Context(), privacy.Scope{
		Protocol:      classified.Protocol,
		Model:         classified.Model,
		ServiceID:     endpointID,
		AccessTokenID: accessTokenID,
	})
	if err != nil {
		return func() {}, nil, err
	}
	if !policy.Enabled {
		// This branch deliberately does not read, replace, or otherwise touch
		// request.Body. Disabled policy preserves the original byte path.
		return func() {}, nil, nil
	}
	encoding := strings.ToLower(strings.TrimSpace(request.Header.Get("Content-Encoding")))
	if encoding != "" && encoding != "identity" {
		return func() {}, nil, errUnsupportedContentEncoding
	}

	body, buffered, err := handler.bufferPrivacyBody(request)
	if err != nil {
		return func() {}, nil, err
	}
	finish := func() {}
	if buffered != nil {
		finish = buffered.Close
	}
	result, err := handler.privacyFilter.Inspect(
		request.Context(),
		policy,
		classified.Protocol,
		body,
	)
	if err != nil {
		return finish, nil, err
	}
	switch result.Decision {
	case privacy.DecisionAllow:
		return finish, nil, nil
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
		return finish, nil, nil
	case privacy.DecisionBlock:
		return finish, nil, errPrivacyBlocked
	case privacy.DecisionRedact:
		if buffered == nil || len(result.Body) > maxMetadataBytes {
			return finish, nil, privacy.ErrUnsafeRewrite
		}
		buffered.Replace(result.Body)
		if !policy.ResponseRestore || len(result.Redactions) == 0 {
			return finish, nil, nil
		}
		return finish, result.Redactions, nil
	default:
		return finish, nil, privacy.ErrPolicyUnavailable
	}
}

func (handler *Handler) writePrivacyError(writer http.ResponseWriter, request *http.Request, err error) {
	session := recordSessionFromContext(request.Context())
	switch {
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, errMetadataTooLarge):
		writeInferenceError(writer, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the inspection limit", false, nil)
		session.noteFailed(errorSummaryFromInference("request_too_large", "request body exceeds the inspection limit", false))
	case errors.Is(err, errUnsupportedContentEncoding):
		writeInferenceError(writer, http.StatusUnsupportedMediaType, "unsupported_content_encoding", "encoded inference request bodies cannot be inspected safely", false, nil)
		session.noteFailed(errorSummaryFromInference("unsupported_content_encoding", "encoded inference request bodies cannot be inspected safely", false))
	case errors.Is(err, privacy.ErrPolicyUnavailable):
		writeInferenceError(writer, http.StatusServiceUnavailable, "privacy_policy_unavailable", "privacy policy could not be resolved", true, nil)
		session.noteFailed(errorSummaryFromInference("privacy_policy_unavailable", "privacy policy could not be resolved", true))
	case errors.Is(err, privacy.ErrDetectorUnavailable),
		errors.Is(err, privacy.ErrDetectorLimit),
		errors.Is(err, privacy.ErrDetectorTimeout):
		writeInferenceError(writer, http.StatusServiceUnavailable, "safety_engine_unavailable", "local safety engine is unavailable", true, nil)
		session.noteFailed(errorSummaryFromInference("safety_engine_unavailable", "local safety engine is unavailable", true))
	case errors.Is(err, errPrivacyBlocked),
		errors.Is(err, privacy.ErrUnsafeInput),
		errors.Is(err, privacy.ErrUnsafeRewrite):
		writeInferenceError(writer, http.StatusForbidden, "policy_blocked", "request was blocked by local privacy policy", false, nil)
		session.noteBlocked(errorSummaryFromInference("policy_blocked", "request was blocked by local privacy policy", false))
	default:
		writeInferenceError(writer, http.StatusServiceUnavailable, "safety_engine_unavailable", "local safety engine is unavailable", true, nil)
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
	body, err := io.ReadAll(io.LimitReader(original, maxMetadataBytes+1))
	if err != nil {
		release()
		_ = original.Close()
		return nil, nil, privacy.ErrUnsafeInput
	}
	if len(body) > maxMetadataBytes {
		release()
		_ = original.Close()
		return nil, nil, errMetadataTooLarge
	}
	if err := original.Close(); err != nil {
		release()
		return nil, nil, privacy.ErrUnsafeInput
	}
	buffered := &privacyBufferedBody{request: request}
	buffered.current = &metadataPermitBody{
		ReadCloser: io.NopCloser(bytes.NewReader(body)),
		release:    release,
	}
	request.Body = buffered.current
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
		writeInferenceError(writer, http.StatusForbidden, "origin_forbidden", "browser origins cannot call the inference plane", false, nil)
		return false
	}
	if handler.allowedHost != "" && request.Host != handler.allowedHost {
		writeInferenceError(writer, http.StatusMisdirectedRequest, "host_forbidden", "request Host does not match the inference listener", false, nil)
		return false
	}
	if handler.accessTokenAuthenticator != nil {
		if request.URL.Query().Has("key") || request.URL.Query().Has("api_key") || request.URL.Query().Has("access_token") {
			writeInferenceError(writer, http.StatusUnauthorized, "token_query_forbidden", "local access tokens are not accepted in query parameters", false, nil)
			return false
		}
		token, ok := localClientCredential(request.Header)
		if !ok {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="astrlink-inference"`)
			writeInferenceError(writer, http.StatusUnauthorized, "invalid_access_token", "a valid local access token is required", false, nil)
			return false
		}
		tokenID, err := handler.accessTokenAuthenticator.AuthenticateAccessToken(request.Context(), token)
		if err != nil || tokenID == "" {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="astrlink-inference"`)
			writeInferenceError(writer, http.StatusUnauthorized, "invalid_access_token", "a valid local access token is required", false, nil)
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
		writeInferenceError(writer, http.StatusUnsupportedMediaType, "unsupported_media_type", "inference request bodies must use application/json", false, nil)
		return false
	}
	return true
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
	if len(credentials) != 1 || credentials[0] == "" {
		return "", false
	}
	return credentials[0], true
}

func (handler *Handler) classify(request *http.Request) (Request, func(), error) {
	select {
	case handler.metadataSlots <- struct{}{}:
		var once sync.Once
		release := func() {
			once.Do(func() { <-handler.metadataSlots })
		}
		classified, err := classify(request)
		if err != nil {
			if replay, buffered := request.Body.(*replayReadCloser); buffered {
				_ = replay.Close()
			}
			release()
			return Request{}, func() {}, err
		}
		if _, buffered := request.Body.(*replayReadCloser); !buffered {
			release()
			return classified, func() {}, nil
		}
		body := &metadataPermitBody{ReadCloser: request.Body, release: release}
		request.Body = body
		return classified, func() {
			_ = body.Close()
		}, nil
	case <-request.Context().Done():
		return Request{}, func() {}, request.Context().Err()
	}
}

// metadataPermitBody keeps the inspection permit until the replay buffer is
// consumed or closed. This bounds resident replay memory across slow endpoint
// resolution and upstream work, rather than only bounding concurrent parsing.
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
	modes []contract.CapabilityMode,
	streaming bool,
) {
	modeDescription, planTypes := capabilityModeDescription(modes)
	writeInferenceError(
		writer,
		http.StatusUnprocessableEntity,
		"missing_protocol_capability",
		fmt.Sprintf(
			"no enabled endpoint provides protocol %q in %s mode with streaming=%t",
			protocol,
			modeDescription,
			streaming,
		),
		false,
		[]errorDetail{{
			Protocol:          string(protocol),
			Reason:            fmt.Sprintf("required mode=%s; streaming=%t", modeDescription, streaming),
			RequiredPlanTypes: planTypes,
		}},
	)
}

func writePlannerCapability(writer http.ResponseWriter, capability *planner.CapabilityUnavailableError) {
	if capability == nil {
		writeMissingCapability(
			writer,
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
