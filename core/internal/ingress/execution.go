package ingress

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/planner"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

const maxUpstreamAttempts = 3

type executionFailureKind uint8

const (
	executionFailureNone executionFailureKind = iota
	executionFailureCapability
	executionFailureCredential
	executionFailureConfiguration
	executionFailureUpstream
	executionFailureConversionUnsupported
	executionFailureConversionFailed
)

type executionFailure struct {
	kind       executionFailureKind
	err        error
	endpointID contract.ServiceID
	capability *planner.CapabilityUnavailableError
}

func (handler *Handler) resolveCandidates(
	ctx context.Context,
	request endpoint.ResolveRequest,
) ([]endpoint.Resolved, error) {
	if resolver, ok := handler.resolver.(endpoint.CandidateResolver); ok {
		candidates, err := resolver.ResolveCandidates(ctx, request)
		if err != nil {
			return nil, err
		}
		if len(candidates) == 0 {
			return nil, endpoint.ErrNoEndpoint
		}
		return candidates, nil
	}
	resolved, err := handler.resolver.Resolve(ctx, request)
	if err != nil {
		return nil, err
	}
	return []endpoint.Resolved{resolved}, nil
}

func (handler *Handler) executeCandidates(
	writer http.ResponseWriter,
	request *http.Request,
	classified Request,
	candidates []endpoint.Resolved,
) {
	if request.Context().Err() != nil {
		return
	}
	body, err := captureRequestBody(
		request,
		requiresInspectedJSON(classified.Protocol),
	)
	if err != nil {
		writeInferenceError(
			writer,
			http.StatusBadRequest,
			"invalid_request",
			"request body could not be prepared safely",
			false,
			nil,
		)
		if session := recordSessionFromContext(request.Context()); session != nil {
			session.noteFailed(errorSummaryFromInference(
				"invalid_request",
				"request body could not be prepared safely",
				false,
			))
		}
		return
	}
	defer body.Close()

	downstream := newCommitTrackingWriter(writer)
	initialHeaders := downstream.Header().Clone()
	controller, healthAware := handler.resolver.(endpoint.AttemptController)
	attemptLimit := maxUpstreamAttempts
	if !body.Replayable() {
		attemptLimit = 1
	}

	attempts := 0
	var last executionFailure
	for _, candidate := range candidates {
		candidate.Service = candidate.CanonicalService()
		candidate.BaseURL = candidate.EffectiveBaseURL()
		if attempts >= attemptLimit {
			break
		}
		mode := candidate.Mode
		if !mode.Valid() {
			// Preserve the original M1 seam: an omitted mode is native.
			mode = contract.CapabilityModeNative
		}
		planType := candidate.PlanType
		if planType == "" {
			if mode == contract.CapabilityModeDelegated {
				planType = contract.PlanTypeDelegated
			} else {
				planType = contract.PlanTypeNative
			}
		}
		var plan contract.ExecutionPlan
		var planErr error
		convertTo := declaredConvertTo(candidate.Service, classified.Protocol, mode)
		if planType == contract.PlanTypeRelayKit || convertTo != "" {
			if handler.conversionEngine == nil {
				last = executionFailure{kind: executionFailureCapability, endpointID: candidate.Service.ID}
				continue
			}
			upstreamProtocol := candidate.UpstreamProtocol
			if convertTo != "" {
				upstreamProtocol = convertTo
			}
			plan, planErr = planner.BuildRelayKit(planner.RelayKitInput{
				Service: candidate.Service, InputProtocol: classified.Protocol,
				UpstreamProtocol: upstreamProtocol, Streaming: classified.Streaming,
				Edges: handler.conversionEngine.Edges(),
			})
		} else {
			plan, planErr = planner.BuildAlpha(planner.AlphaInput{
				Service: candidate.Service, Protocol: classified.Protocol, Mode: mode, Streaming: classified.Streaming,
			})
		}
		if planErr != nil {
			var capabilityErr *planner.CapabilityUnavailableError
			if errors.As(planErr, &capabilityErr) ||
				errors.Is(planErr, planner.ErrEndpointDisabled) {
				if capabilityErr == nil {
					capabilityErr = &planner.CapabilityUnavailableError{
						Protocol:  classified.Protocol,
						Mode:      mode,
						Streaming: classified.Streaming,
					}
				}
				last = executionFailure{
					kind:       executionFailureCapability,
					err:        planErr,
					endpointID: candidate.Service.ID,
					capability: capabilityErr,
				}
				continue
			}
			last = executionFailure{
				kind:       executionFailureConfiguration,
				err:        planErr,
				endpointID: candidate.Service.ID,
			}
			continue
		}

		attemptRequest, ok, bodyErr := body.Next(request.Context())
		if bodyErr != nil || !ok {
			if bodyErr == nil && last.kind != executionFailureNone {
				break
			}
			writeInferenceError(
				downstream,
				http.StatusBadRequest,
				"invalid_request",
				"request body could not be replayed safely",
				false,
				nil,
			)
			return
		}

		if candidate.UpstreamModel != "" && plan.Type != contract.PlanTypeRelayKit {
			rewritten, rewriteErr := rewriteRequestModel(
				attemptRequest,
				classified,
				candidate.UpstreamModel,
				body.Replayable(),
			)
			if rewriteErr != nil {
				last = executionFailure{
					kind:       executionFailureConfiguration,
					err:        rewriteErr,
					endpointID: candidate.Service.ID,
				}
				_ = attemptRequest.Body.Close()
				continue
			}
			attemptRequest = rewritten
		}

		resetResponseHeaders(downstream.Header(), initialHeaders)
		finishPrivacy, privacyResult, privacyErr := handler.applyPrivacy(
			downstream,
			attemptRequest,
			classified,
			candidate.Service.ID,
		)
		redactions := privacyResult.redactions
		if request.Context().Err() != nil {
			finishPrivacy()
			_ = attemptRequest.Body.Close()
			return
		}
		if privacyErr != nil {
			finishPrivacy()
			_ = attemptRequest.Body.Close()
			handler.writePrivacyError(downstream, request, privacyErr)
			return
		}
		if plan.Type == contract.PlanTypeRelayKit {
			convertedInput, readErr := io.ReadAll(attemptRequest.Body)
			if readErr != nil {
				finishPrivacy()
				_ = attemptRequest.Body.Close()
				last = executionFailure{kind: executionFailureConversionUnsupported, err: readErr, endpointID: candidate.Service.ID}
				continue
			}
			_ = attemptRequest.Body.Close()
			upstreamModel := candidate.UpstreamModel
			if upstreamModel == "" {
				upstreamModel = classified.Model
			}
			converted, convertErr := handler.conversionEngine.ConvertRequest(request.Context(), relaykitbridge.ConvertRequestInput{
				From: plan.InputProtocol, To: plan.UpstreamProtocol, ContentType: attemptRequest.Header.Get("Content-Type"),
				Body: convertedInput, PublicModel: classified.Model, UpstreamModel: upstreamModel, Streaming: classified.Streaming,
			})
			if convertErr != nil || adaptRelayKitRequest(attemptRequest, plan.UpstreamProtocol, classified.Streaming, upstreamModel, converted.Body) != nil {
				finishPrivacy()
				last = executionFailure{kind: executionFailureConversionUnsupported, err: convertErr, endpointID: candidate.Service.ID}
				continue
			}
		}

		var headers http.Header
		authorizationEndpoint, authorizeErr := candidate.AuthorizationEndpoint()
		if authorizeErr == nil {
			var headersErr error
			headers, headersErr = handler.authorizer.Headers(request.Context(), authorizationEndpoint)
			authorizeErr = headersErr
		}
		if authorizeErr != nil {
			finishPrivacy()
			_ = attemptRequest.Body.Close()
			if request.Context().Err() != nil {
				return
			}
			last = executionFailure{
				kind:       executionFailureCredential,
				err:        authorizeErr,
				endpointID: candidate.Service.ID,
			}
			if !body.Replayable() {
				break
			}
			continue
		}
		baseURL, parseErr := url.Parse(candidate.BaseURL)
		if parseErr != nil {
			finishPrivacy()
			_ = attemptRequest.Body.Close()
			last = executionFailure{
				kind:       executionFailureConfiguration,
				err:        parseErr,
				endpointID: candidate.Service.ID,
			}
			if !body.Replayable() {
				break
			}
			continue
		}
		if candidate.Service.Kind.IsSubscription() {
			attemptRequest.URL.Path = strings.TrimPrefix(attemptRequest.URL.Path, "/v1")
			if attemptRequest.URL.RawPath != "" {
				attemptRequest.URL.RawPath = strings.TrimPrefix(attemptRequest.URL.RawPath, "/v1")
			}
		}

		if healthAware && !controller.BeginAttempt(candidate) {
			finishPrivacy()
			_ = attemptRequest.Body.Close()
			if !body.Replayable() {
				break
			}
			continue
		}
		attempts++
		health := newAttemptHealthOutcome(controller, candidate, healthAware)
		recordSession := recordSessionFromContext(request.Context())

		outWriter := http.ResponseWriter(downstream)
		var aliasWriter *aliasRestoringWriter
		var restoring *restoringResponseWriter
		var relayWriter *relayKitResponseWriter
		if recordSession != nil &&
			(recordSession.responseCaptureEnabled() ||
				recordSession.upstreamResponseCapture.enabled) {
			attemptRequest.Header.Del("Accept-Encoding")
		}
		// Writer onion (outermost receives upstream bytes first):
		// Native/Delegated: upstream -> privacy restore -> alias restore -> client
		// RelayKit: upstream -> convert -> privacy restore -> client
		if plan.Type != contract.PlanTypeRelayKit && candidate.UpstreamModel != "" && classified.Model != "" {
			attemptRequest.Header.Del("Accept-Encoding")
			aliasWriter = newAliasRestoringWriter(
				outWriter,
				classified.Model,
				candidate.UpstreamModel,
				classified.Streaming,
				aliasMemberNames(classified.Protocol),
			)
			outWriter = aliasWriter
		}
		if len(redactions) > 0 {
			attemptRequest.Header.Del("Accept-Encoding")
			restoring = newRestoringResponseWriter(
				outWriter,
				redactions,
				classified.Streaming,
				classified.Protocol,
				privacyResult.toolArguments,
			)
			outWriter = restoring
		}
		if plan.Type == contract.PlanTypeRelayKit {
			attemptRequest.Header.Del("Accept-Encoding")
			upstreamModel := candidate.UpstreamModel
			if upstreamModel == "" {
				upstreamModel = classified.Model
			}
			relayWriter, planErr = newRelayKitResponseWriter(
				outWriter, handler.conversionEngine, plan, classified.Model, upstreamModel,
			)
			if planErr != nil {
				finishPrivacy()
				_ = attemptRequest.Body.Close()
				last = executionFailure{kind: executionFailureConversionUnsupported, err: planErr, endpointID: candidate.Service.ID}
				continue
			}
			outWriter = relayWriter
		}
		deferHealthStatus := (restoring != nil || aliasWriter != nil || relayWriter != nil) && !classified.Streaming
		var upstreamStatus atomic.Int32

		attemptContext := newResponseStartContext(
			request.Context(),
			handler.responseStartTimeout,
		)
		attemptRequest = attemptRequest.WithContext(attemptContext.Context())
		startWriter := newResponseStartWriter(outWriter, func(status int) {
			if attemptContext.ResponseStarted() {
				upstreamStatus.Store(int32(status))
				if !deferHealthStatus {
					health.RecordStatus(status)
				}
			}
		})
		forwardTarget := transport.Target{
			BaseURL:        baseURL,
			RequestHeaders: headers,
		}
		if recordSession != nil {
			forwardTarget.ObserveOutbound = func(outbound *http.Request) {
				recordSession.beginNetworkAttempt(
					request.Context(),
					candidate,
					plan,
					handler.requestRecords,
					handler.recordLogger,
				)
				recordSession.observeOutboundCapture(outbound)
			}
			forwardTarget.WrapResponseBody = recordSession.wrapUpstreamResponseBody
		}
		forwardErr := handler.forwarder.Forward(startWriter, attemptRequest, forwardTarget)
		attemptContext.Stop()
		timedOut := attemptContext.TimedOut()
		relayConversionFailed := false
		if relayWriter != nil && forwardErr == nil {
			if finishErr := relayWriter.Finish(); finishErr != nil {
				forwardErr = transport.NewResponseError(finishErr)
				relayConversionFailed = !downstream.Committed()
			}
		}
		if restoring != nil && (forwardErr == nil || classified.Streaming) {
			if finishErr := restoring.Finish(); finishErr != nil && forwardErr == nil {
				forwardErr = transport.NewResponseError(finishErr)
			}
		}
		if restoring != nil {
			if session := recordSessionFromContext(request.Context()); session != nil {
				session.notePrivacyRestore(restoring.privacyRestoreSummary())
			}
		}
		if aliasWriter != nil && forwardErr == nil {
			if finishErr := aliasWriter.Finish(); finishErr != nil {
				forwardErr = transport.NewResponseError(finishErr)
			}
		}
		if relayWriter != nil && forwardErr != nil && !downstream.Committed() {
			_ = relayWriter.streamClose()
		}
		finishPrivacy()
		_ = attemptRequest.Body.Close()

		if timedOut {
			if forwardErr == nil {
				forwardErr = context.DeadlineExceeded
			} else if !errors.Is(forwardErr, context.DeadlineExceeded) {
				forwardErr = fmt.Errorf("%w: upstream response start", context.DeadlineExceeded)
			}
		}
		if forwardErr == nil {
			if deferHealthStatus && upstreamStatus.Load() != 0 {
				health.RecordStatus(int(upstreamStatus.Load()))
			} else {
				health.Success()
			}
			if session := recordSessionFromContext(request.Context()); session != nil {
				session.noteServed(candidate, plan)
				session.noteSucceeded()
			}
			return
		}
		if request.Context().Err() != nil {
			health.Abandon()
			return
		}

		var upstreamErr *transport.UpstreamError
		preResponseFailure := timedOut || errors.As(forwardErr, &upstreamErr)
		var responseErr *transport.ResponseError
		bufferedResponseFailure := errors.As(forwardErr, &responseErr) &&
			!downstream.Committed()
		safeRetryFailure := preResponseFailure || bufferedResponseFailure
		if safeRetryFailure {
			health.Failure()
		} else {
			health.Abandon()
		}

		// This check is deliberately independent of transport error typing.
		// Once headers, a flush, or body bytes reached the client, another
		// upstream attempt could only corrupt the response.
		if downstream.Committed() {
			if session := recordSessionFromContext(request.Context()); session != nil {
				session.noteServed(candidate, plan)
				session.noteFailed(errorSummaryFromInference(
					"upstream_stream_interrupted",
					interruptedStreamMessage(forwardErr),
					true,
				))
			}
			return
		}
		if responseErr != nil && !bufferedResponseFailure {
			if session := recordSessionFromContext(request.Context()); session != nil {
				session.noteServed(candidate, plan)
				session.noteFailed(errorSummaryFromInference(
					"upstream_stream_interrupted",
					interruptedStreamMessage(forwardErr),
					true,
				))
			}
			return
		}
		var targetErr *transport.TargetError
		if errors.As(forwardErr, &targetErr) {
			last = executionFailure{
				kind:       executionFailureConfiguration,
				err:        forwardErr,
				endpointID: candidate.Service.ID,
			}
			continue
		}
		if relayConversionFailed {
			last = executionFailure{
				kind:       executionFailureConversionFailed,
				err:        forwardErr,
				endpointID: candidate.Service.ID,
			}
			if !body.Replayable() {
				break
			}
			demoteFailedAttemptForRetry(
				request.Context(),
				recordSession,
				handler.requestRecords,
				handler.auditBlobs,
				errorSummaryFromInference(
					"relaykit_conversion_failed",
					"upstream response could not be converted",
					true,
				),
				handler.recordLogger,
			)
			continue
		}

		last = executionFailure{
			kind:       executionFailureUpstream,
			err:        forwardErr,
			endpointID: candidate.Service.ID,
		}
		if !safeRetryFailure || !body.Replayable() {
			break
		}
		code := "upstream_unavailable"
		fallback := upstreamUnavailableFallback
		if errors.Is(forwardErr, context.DeadlineExceeded) {
			code = "upstream_timeout"
			fallback = upstreamTimeoutFallback
		}
		message := operatorTransportMessage(forwardErr, fallback)
		demoteFailedAttemptForRetry(
			request.Context(),
			recordSession,
			handler.requestRecords,
			handler.auditBlobs,
			errorSummaryFromInference(code, message, true),
			handler.recordLogger,
		)
	}

	if downstream.Committed() || request.Context().Err() != nil {
		return
	}
	resetResponseHeaders(downstream.Header(), initialHeaders)
	if last.kind == executionFailureNone {
		handler.writeResolveError(
			downstream,
			request,
			classified,
			endpoint.ErrNoHealthyEndpoint,
		)
		return
	}
	handler.writeExecutionFailure(downstream, request, classified, last)
}

func demoteFailedAttemptForRetry(
	ctx context.Context,
	session *recordSession,
	store RequestRecordStore,
	blobs AuditBlobPersister,
	summary contract.ErrorSummary,
	logf func(string, ...any),
) {
	if session == nil {
		return
	}
	session.demoteCurrentAttemptToChild(ctx, store, blobs, summary, logf)
}

func (handler *Handler) writeExecutionFailure(
	writer http.ResponseWriter,
	request *http.Request,
	classified Request,
	failure executionFailure,
) {
	session := recordSessionFromContext(request.Context())
	switch failure.kind {
	case executionFailureCapability:
		writePlannerCapability(writer, failure.capability)
		session.noteFailed(errorSummaryFromInference(
			"missing_protocol_capability",
			"no endpoint provides the requested protocol capability",
			false,
		))
	case executionFailureCredential:
		writeInferenceError(
			writer,
			http.StatusServiceUnavailable,
			"credential_unavailable",
			"selected endpoint credential is unavailable",
			true,
			[]errorDetail{{
				Protocol:  string(classified.Protocol),
				ServiceID: string(failure.endpointID),
			}},
		)
		session.noteFailed(errorSummaryFromInference(
			"credential_unavailable",
			"selected endpoint credential is unavailable",
			true,
		))
	case executionFailureConfiguration:
		writeInferenceError(
			writer,
			http.StatusInternalServerError,
			"invalid_endpoint_configuration",
			"selected endpoint configuration is invalid",
			false,
			nil,
		)
		session.noteFailed(errorSummaryFromInference(
			"invalid_endpoint_configuration",
			"selected endpoint configuration is invalid",
			false,
		))
	case executionFailureConversionUnsupported:
		writeInferenceError(writer, http.StatusUnprocessableEntity, "relaykit_conversion_unsupported",
			"selected protocol conversion is unsupported", false, nil)
		session.noteFailed(errorSummaryFromInference("relaykit_conversion_unsupported", "selected protocol conversion is unsupported", false))
	case executionFailureConversionFailed:
		writeInferenceError(writer, http.StatusBadGateway, "relaykit_conversion_failed",
			"upstream response could not be converted", true, nil)
		session.noteFailed(errorSummaryFromInference("relaykit_conversion_failed", "upstream response could not be converted", true))
	default:
		status := http.StatusBadGateway
		code := "upstream_unavailable"
		fallback := upstreamUnavailableFallback
		if errors.Is(failure.err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
			code = "upstream_timeout"
			fallback = upstreamTimeoutFallback
		}
		message := operatorTransportMessage(failure.err, fallback)
		writeInferenceError(writer, status, code, message, true, []errorDetail{{
			Protocol:  string(classified.Protocol),
			ServiceID: string(failure.endpointID),
		}})
		session.noteFailed(errorSummaryFromInference(code, message, true))
	}
}

func declaredConvertTo(
	service contract.Service,
	protocol contract.ProtocolID,
	mode contract.CapabilityMode,
) contract.ProtocolID {
	for _, capability := range service.Capabilities {
		if capability.Protocol == protocol && capability.Mode == mode && capability.ConvertTo != "" {
			return capability.ConvertTo
		}
	}
	return ""
}

func requiresInspectedJSON(protocol contract.ProtocolID) bool {
	switch protocol {
	case contract.ProtocolOpenAIResponses,
		contract.ProtocolOpenAIResponsesCompact,
		contract.ProtocolAnthropicMessages,
		contract.ProtocolOpenAIChat,
		contract.ProtocolOpenAICompletions:
		return true
	default:
		return false
	}
}

type requestBodySource struct {
	request    *http.Request
	factory    func() (io.ReadCloser, error)
	first      io.ReadCloser
	replayable bool
	used       bool
	release    func()
}

func captureRequestBody(request *http.Request, forceBuffer bool) (*requestBodySource, error) {
	source := &requestBodySource{request: request}
	if existing, ok := request.Body.(*metadataPermitBody); ok {
		source.release = existing.transferPermit()
	}
	if request.Body == nil || request.Body == http.NoBody {
		source.replayable = true
		source.factory = func() (io.ReadCloser, error) { return http.NoBody, nil }
		return source, nil
	}
	if request.GetBody != nil {
		source.replayable = true
		source.factory = request.GetBody
		if err := request.Body.Close(); err != nil {
			source.Close()
			return nil, err
		}
		source.releasePermit()
		return source, nil
	}

	if !forceBuffer {
		source.first = request.Body
		return source, nil
	}

	original := request.Body
	buffered, err := io.ReadAll(io.LimitReader(original, maxMetadataBytes+1))
	if err != nil {
		_ = original.Close()
		source.Close()
		return nil, err
	}
	if len(buffered) > maxMetadataBytes {
		source.first = &joinedReadCloser{
			Reader: io.MultiReader(bytes.NewReader(buffered), original),
			closer: original,
		}
		return source, nil
	}
	if err := original.Close(); err != nil {
		source.Close()
		return nil, err
	}
	contents := buffered
	source.replayable = true
	source.factory = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(contents)), nil
	}
	source.releasePermit()
	return source, nil
}

func (source *requestBodySource) Replayable() bool {
	return source != nil && source.replayable
}

func (source *requestBodySource) Next(ctx context.Context) (*http.Request, bool, error) {
	if source == nil || source.request == nil {
		return nil, false, nil
	}
	cloned := source.request.Clone(ctx)
	if source.replayable {
		body, err := source.factory()
		if err != nil {
			return nil, false, err
		}
		if source.release != nil {
			// A leftover inspection permit is owned by the source, not by
			// each replayed attempt. Mark the attempt body as already
			// permitted so privacy inspection does not take a second slot.
			body = &metadataPermitBody{
				ReadCloser: body,
				release:    func() {},
			}
		}
		cloned.Body = body
		cloned.GetBody = source.factory
		return cloned, true, nil
	}
	if source.used || source.first == nil {
		return nil, false, nil
	}
	source.used = true
	cloned.Body = source.first
	cloned.GetBody = nil
	source.first = nil
	return cloned, true, nil
}

func (source *requestBodySource) releasePermit() {
	if source == nil || source.release == nil {
		return
	}
	source.release()
	source.release = nil
}

func (source *requestBodySource) Close() {
	if source == nil {
		return
	}
	if source.first != nil {
		_ = source.first.Close()
		source.first = nil
	}
	source.releasePermit()
}

type joinedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (body *joinedReadCloser) Close() error {
	return body.closer.Close()
}

type commitTrackingWriter struct {
	http.ResponseWriter
	committed atomic.Bool
	status    atomic.Int32
}

func newCommitTrackingWriter(writer http.ResponseWriter) *commitTrackingWriter {
	return &commitTrackingWriter{ResponseWriter: writer}
}

func (writer *commitTrackingWriter) WriteHeader(status int) {
	if writer.status.CompareAndSwap(0, int32(status)) {
		writer.committed.Store(true)
		writer.ResponseWriter.WriteHeader(status)
	}
}

func (writer *commitTrackingWriter) Write(chunk []byte) (int, error) {
	if writer.status.Load() == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	writer.committed.Store(true)
	return writer.ResponseWriter.Write(chunk)
}

func (writer *commitTrackingWriter) Flush() {
	_ = writer.FlushError()
}

func (writer *commitTrackingWriter) FlushError() error {
	if writer.status.Load() == 0 {
		writer.WriteHeader(http.StatusOK)
	} else {
		writer.committed.Store(true)
	}
	return http.NewResponseController(writer.ResponseWriter).Flush()
}

func (writer *commitTrackingWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *commitTrackingWriter) Committed() bool {
	return writer.committed.Load()
}

func (writer *commitTrackingWriter) Status() int {
	return int(writer.status.Load())
}

type attemptHealthOutcome struct {
	controller endpoint.AttemptController
	candidate  endpoint.Resolved
	enabled    bool
	once       sync.Once
}

func newAttemptHealthOutcome(
	controller endpoint.AttemptController,
	candidate endpoint.Resolved,
	enabled bool,
) *attemptHealthOutcome {
	return &attemptHealthOutcome{
		controller: controller,
		candidate:  candidate,
		enabled:    enabled,
	}
}

func (outcome *attemptHealthOutcome) RecordStatus(status int) {
	if status >= http.StatusInternalServerError {
		outcome.Failure()
		return
	}
	outcome.Success()
}

func (outcome *attemptHealthOutcome) Success() {
	if outcome == nil || !outcome.enabled {
		return
	}
	outcome.once.Do(func() {
		outcome.controller.RecordSuccess(outcome.candidate)
	})
}

func (outcome *attemptHealthOutcome) Failure() {
	if outcome == nil || !outcome.enabled {
		return
	}
	outcome.once.Do(func() {
		outcome.controller.RecordFailure(outcome.candidate)
	})
}

func (outcome *attemptHealthOutcome) Abandon() {
	if outcome == nil || !outcome.enabled {
		return
	}
	outcome.once.Do(func() {
		outcome.controller.AbandonAttempt(outcome.candidate)
	})
}

type responseStartWriter struct {
	http.ResponseWriter
	once    sync.Once
	started func(int)
}

func newResponseStartWriter(
	writer http.ResponseWriter,
	started func(int),
) *responseStartWriter {
	return &responseStartWriter{ResponseWriter: writer, started: started}
}

func (writer *responseStartWriter) markStarted(status int) {
	writer.once.Do(func() {
		writer.started(status)
	})
}

func (writer *responseStartWriter) WriteHeader(status int) {
	writer.markStarted(status)
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *responseStartWriter) Write(chunk []byte) (int, error) {
	writer.markStarted(http.StatusOK)
	return writer.ResponseWriter.Write(chunk)
}

func (writer *responseStartWriter) Flush() {
	_ = writer.FlushError()
}

func (writer *responseStartWriter) FlushError() error {
	writer.markStarted(http.StatusOK)
	return http.NewResponseController(writer.ResponseWriter).Flush()
}

func (writer *responseStartWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

type responseStartContext struct {
	ctx     context.Context
	cancel  context.CancelCauseFunc
	timer   *time.Timer
	outcome atomic.Uint32
}

const (
	responseStartPending uint32 = iota
	responseStartObserved
	responseStartTimedOut
)

func newResponseStartContext(parent context.Context, timeout time.Duration) *responseStartContext {
	ctx, cancel := context.WithCancelCause(parent)
	result := &responseStartContext{ctx: ctx, cancel: cancel}
	if timeout > 0 {
		result.timer = time.AfterFunc(timeout, func() {
			if result.outcome.CompareAndSwap(responseStartPending, responseStartTimedOut) {
				cancel(context.DeadlineExceeded)
			}
		})
	}
	return result
}

func (attempt *responseStartContext) Context() context.Context {
	return attempt.ctx
}

func (attempt *responseStartContext) ResponseStarted() bool {
	if attempt.outcome.CompareAndSwap(responseStartPending, responseStartObserved) {
		if attempt.timer != nil {
			attempt.timer.Stop()
		}
		return true
	}
	return attempt.outcome.Load() == responseStartObserved
}

func (attempt *responseStartContext) Stop() {
	_ = attempt.ResponseStarted()
	attempt.cancel(context.Canceled)
}

func (attempt *responseStartContext) TimedOut() bool {
	return attempt.outcome.Load() == responseStartTimedOut
}

func resetResponseHeaders(destination, source http.Header) {
	for name := range destination {
		delete(destination, name)
	}
	for name, values := range source {
		destination[name] = append([]string(nil), values...)
	}
}
