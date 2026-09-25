package ingress

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/planner"
	"github.com/QuantumNous/astrlink/core/internal/providerapi"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

const maxUpstreamAttempts = 6

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

// code names a failure for the request record, matching writeExecutionFailure.
func (failure executionFailure) code() string {
	switch failure.kind {
	case executionFailureCapability:
		return "missing_protocol_capability"
	case executionFailureCredential:
		return "credential_unavailable"
	case executionFailureConfiguration:
		return "invalid_endpoint_configuration"
	case executionFailureConversionUnsupported:
		return "relaykit_conversion_unsupported"
	case executionFailureConversionFailed:
		return "relaykit_conversion_failed"
	default:
		if isUpstreamTimeout(failure.err) {
			return "upstream_timeout"
		}
		return "upstream_unavailable"
	}
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
	handler.executeCandidatesWithTest(writer, request, classified, candidates, nil)
}

func (handler *Handler) executeCandidatesWithTest(
	writer http.ResponseWriter,
	request *http.Request,
	classified Request,
	candidates []endpoint.Resolved,
	test *serviceTestExecution,
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
	if session := recordSessionFromContext(request.Context()); session != nil {
		if body.Replayable() || session.requestCapture.complete {
			session.noteInboundBodyReady()
		}
	}

	downstream := newCommitTrackingWriter(writer)
	initialHeaders := downstream.Header().Clone()
	controller, healthAware := handler.resolver.(endpoint.AttemptController)
	if test != nil {
		controller, healthAware = nil, false
	}
	schedule := newRecoverySchedule(candidates, body.Replayable())
	if plan, ok := request.Context().Value(graphPlanKey{}).(*endpoint.RoutingGraphPlan); ok {
		var factsBody []byte
		if body.Replayable() {
			if reader, err := body.factory(); err == nil {
				factsBody, _ = io.ReadAll(io.LimitReader(reader, 4*1024*1024+1))
				_ = reader.Close()
				if len(factsBody) > 4*1024*1024 {
					factsBody = nil
				}
			}
		}
		schedule.attachGraph(plan, factsBody, recordSessionFromContext(request.Context()))
	}
	repairedTargets := map[string][]byte{}
	var last executionFailure
	var lastNetworkFailure executionFailure
	var replayLastHTTP func()
	records := recordSessionFromContext(request.Context())
	for {
		candidateIndex, hasNext := schedule.next(request.Context())
		if !hasNext {
			break
		}
		candidate := candidates[candidateIndex]
		if candidate.Unavailable != "" {
			schedule.graphSkip(candidateIndex, candidate.Unavailable)
			records.noteCandidateRejected(candidate.CanonicalService().ID, candidate.Unavailable)
			continue
		}
		candidate.Service = candidate.CanonicalService()
		candidate.BaseURL = candidate.EffectiveBaseURL()
		policy := failurePolicy(candidate)
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
		protocolModel := candidate.UpstreamModel
		if protocolModel == "" {
			protocolModel = classified.routingModel()
		}
		if native := candidate.Service.Kind.ModelNativeProtocol(protocolModel); native != "" {
			convertTo = ""
			if native != classified.Protocol {
				convertTo = native
			}
		}
		if test != nil {
			// Control-plane tests validate the directly declared capability and
			// deliberately bypass enabled/model-list gates and conversion.
			plan = test.plan
		} else if planType == contract.PlanTypeRelayKit || convertTo != "" {
			if handler.conversionEngine == nil {
				last = executionFailure{kind: executionFailureCapability, endpointID: candidate.Service.ID}
				schedule.graphSkip(candidateIndex, last.code())
				records.noteCandidateRejected(last.endpointID, last.code())
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
				schedule.graphSkip(candidateIndex, last.code())
				records.noteCandidateRejected(last.endpointID, last.code())
				continue
			}
			last = executionFailure{
				kind:       executionFailureConfiguration,
				err:        planErr,
				endpointID: candidate.Service.ID,
			}
			schedule.graphSkip(candidateIndex, last.code())
			records.noteCandidateRejected(last.endpointID, last.code())
			continue
		}

		candidate.RequestedModel = classified.Model
		candidate.UpstreamProtocol = plan.UpstreamProtocol
		repairTarget := recoveryTargetKey(candidate)
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

		if repaired := repairedTargets[repairTarget]; repaired != nil {
			replaceRecoveryRequestBody(attemptRequest, repaired)
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
				schedule.graphSkip(candidateIndex, last.code())
				records.noteCandidateRejected(last.endpointID, last.code())
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
			records.noteAttemptedService(candidate.Service.ID)
			handler.writePrivacyError(downstream, request, privacyErr)
			return
		}
		if plan.Type == contract.PlanTypeRelayKit {
			convertedInput, readErr := io.ReadAll(attemptRequest.Body)
			if readErr != nil {
				finishPrivacy()
				_ = attemptRequest.Body.Close()
				last = executionFailure{kind: executionFailureConversionUnsupported, err: readErr, endpointID: candidate.Service.ID}
				schedule.graphSkip(candidateIndex, last.code())
				records.noteCandidateRejected(last.endpointID, last.code())
				continue
			}
			_ = attemptRequest.Body.Close()
			upstreamModel := candidate.UpstreamModel
			if upstreamModel == "" {
				upstreamModel = classified.routingModel()
			}
			converted, convertErr := handler.conversionEngine.ConvertRequest(request.Context(), relaykitbridge.ConvertRequestInput{
				From: plan.InputProtocol, To: plan.UpstreamProtocol, ContentType: attemptRequest.Header.Get("Content-Type"),
				Body: convertedInput, PublicModel: classified.Model, UpstreamModel: upstreamModel, Streaming: classified.Streaming,
			})
			if convertErr != nil || adaptRelayKitRequest(attemptRequest, plan.UpstreamProtocol, classified.Streaming, upstreamModel, converted.Body) != nil {
				finishPrivacy()
				last = executionFailure{kind: executionFailureConversionUnsupported, err: convertErr, endpointID: candidate.Service.ID}
				schedule.graphSkip(candidateIndex, last.code())
				records.noteCandidateRejected(last.endpointID, last.code())
				continue
			}
			if candidate.Service.Kind == contract.ServiceKindCodexSubscription && plan.UpstreamProtocol == contract.ProtocolOpenAIResponses {
				if err := prepareCodexConvertedRequest(attemptRequest); err != nil {
					finishPrivacy()
					last = executionFailure{kind: executionFailureConversionUnsupported, err: err, endpointID: candidate.Service.ID}
					schedule.graphSkip(candidateIndex, last.code())
					records.noteCandidateRejected(last.endpointID, last.code())
					continue
				}
			}
		}

		if candidate.Service.Kind == contract.ServiceKindClaudeSubscription && plan.UpstreamProtocol == contract.ProtocolAnthropicMessages {
			if err := prepareClaudeSubscriptionRequest(attemptRequest); err != nil {
				finishPrivacy()
				last = executionFailure{kind: executionFailureConfiguration, endpointID: candidate.Service.ID, err: err}
				schedule.graphSkip(candidateIndex, last.code())
				records.noteCandidateRejected(last.endpointID, last.code())
				continue
			}
		}
		var headers http.Header
		authorizationEndpoint, authorizeErr := candidate.AuthorizationEndpoint()
		proxyContext := request.Context()
		if authorizeErr == nil {
			proxyContext, authorizeErr = networkproxy.Bind(proxyContext, candidate.Service, handler.proxyCredentials)
			attemptRequest = attemptRequest.WithContext(proxyContext)
		}
		if authorizeErr == nil {
			authorizationEndpoint.Auth = providerapi.Auth(candidate.Service.Kind, plan.UpstreamProtocol, authorizationEndpoint.Auth)
			var headersErr error
			headers, headersErr = handler.authorizer.Headers(proxyContext, authorizationEndpoint, attemptRequest.Header)
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
			schedule.graphSkip(candidateIndex, last.code())
			records.noteCandidateRejected(last.endpointID, last.code())
			if !body.Replayable() {
				break
			}
			continue
		}
		if test != nil && test.observer.Authorization != nil {
			test.observer.Authorization(headers.Clone())
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
			schedule.graphSkip(candidateIndex, last.code())
			records.noteCandidateRejected(last.endpointID, last.code())
			if !body.Replayable() {
				break
			}
			continue
		}
		baseURL = providerapi.BaseURL(candidate.Service.Kind, plan.UpstreamProtocol, baseURL)
		attemptRequest.URL = providerapi.RequestURL(candidate.Service.Kind, plan.UpstreamProtocol, attemptRequest.URL)
		if candidate.Service.Kind == contract.ServiceKindCodexSubscription {
			attemptRequest.URL.Path = strings.TrimPrefix(attemptRequest.URL.Path, "/v1")
			if attemptRequest.URL.RawPath != "" {
				attemptRequest.URL.RawPath = strings.TrimPrefix(attemptRequest.URL.RawPath, "/v1")
			}
		}

		if healthAware && !controller.BeginAttempt(candidate) {
			schedule.graphSkip(candidateIndex, "circuit_open")
			records.noteCandidateRejected(candidate.Service.ID, "circuit_open")
			finishPrivacy()
			_ = attemptRequest.Body.Close()
			if !body.Replayable() {
				break
			}
			continue
		}
		health := newAttemptHealthOutcome(controller, candidate, healthAware)
		recordSession := recordSessionFromContext(request.Context())
		if candidate.Service.Kind == contract.ServiceKindOpenCodeGo || candidate.Service.Kind == contract.ServiceKindOpenCodeZen {
			if headers == nil {
				headers = make(http.Header)
			}
			if attemptRequest.Header.Get("X-Opencode-Session") == "" && recordSession != nil {
				sessionID := recordSession.sessionID
				if sessionID != "" {
					headers.Set("X-Opencode-Session", string(sessionID))
				}
			}
		}

		outWriter := http.ResponseWriter(downstream)
		var aliasWriter *aliasRestoringWriter
		var restoring *restoringResponseWriter
		var relayWriter *relayKitResponseWriter
		if recordSession != nil &&
			(recordSession.responseCaptureEnabled() ||
				recordSession.upstreamResponseCapture.enabled) {
			attemptRequest.Header.Del("Accept-Encoding")
		}
		upstreamModel := candidate.UpstreamModel
		if upstreamModel == "" {
			upstreamModel = classified.routingModel()
		}
		canRepairThinking := body.Replayable() && policy.AllowsThinkingSignatureRecovery() &&
			supportsThinkingSignatureRecovery(plan, upstreamModel)
		canRepairOpenAIReasoning := body.Replayable() && policy.AllowsOpenAIReasoningRecovery() &&
			supportsOpenAIReasoningRecovery(plan, upstreamModel)
		canRepairOpenAIFunction := body.Replayable() && policy.AllowsOpenAIFunctionOutputRecovery() &&
			supportsOpenAIReasoningRecovery(plan, upstreamModel)
		canRepairOpenAI := canRepairOpenAIReasoning || canRepairOpenAIFunction
		if canRepairThinking || canRepairOpenAI {
			// Inspect structured error messages without negotiating a compressed body.
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
				upstreamModel = classified.routingModel()
			}
			relayWriter, planErr = newRelayKitResponseWriter(
				outWriter, handler.conversionEngine, plan, classified.Model, upstreamModel,
			)
			if planErr != nil {
				health.Abandon()
				finishPrivacy()
				_ = attemptRequest.Body.Close()
				last = executionFailure{kind: executionFailureConversionUnsupported, err: planErr, endpointID: candidate.Service.ID}
				continue
			}
			outWriter = relayWriter
		}
		deferHealthStatus := (restoring != nil || aliasWriter != nil || relayWriter != nil) && !classified.Streaming
		var upstreamStatus atomic.Int32

		responseTimeout := handler.responseStartTimeout
		if policy.ResponseStartTimeoutSeconds != nil {
			responseTimeout = time.Duration(*policy.ResponseStartTimeoutSeconds) * time.Second
		}
		if test != nil {
			// The test owns its total deadline, independently of routing policy.
			responseTimeout = 0
		}
		attemptContext := newResponseStartContext(attemptRequest.Context(), responseTimeout)
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
			Service: candidate.Service, ProxyCredentials: handler.proxyCredentials,
			BaseURL:        baseURL,
			RequestHeaders: headers,
		}
		attemptStarted := false
		forwardTarget.ObserveOutbound = func(outbound *http.Request) {
			attemptStarted = true
			schedule.started(candidateIndex)
			replayLastHTTP = nil
			if recordSession != nil {
				recordSession.beginNetworkAttempt(
					request.Context(),
					candidate,
					plan,
					handler.requestRecords,
					handler.recordLogger,
				)
				recordSession.observeOutboundCapture(outbound)
			}
			if test != nil && test.observer.Outbound != nil {
				test.observer.Outbound()
			}
		}
		forwardTarget.WrapResponseBody = func(status int, header http.Header, body io.ReadCloser) io.ReadCloser {
			if test != nil && test.observer.WrapResponseBody != nil {
				body = test.observer.WrapResponseBody(body)
			}
			if recordSession != nil {
				body = recordSession.wrapUpstreamResponseBody(status, header, body)
			}
			return body
		}
		forwardTarget.HandleResponse = func(response *http.Response) error {
			if test != nil && test.observer.Response != nil {
				test.observer.Response(response.StatusCode)
			}
			startWriter.markStarted(response.StatusCode)
			if response.StatusCode >= 400 {
				action := policy.ActionForStatus(response.StatusCode)
				reason := "http_" + fmt.Sprint(response.StatusCode)
				var repaired []byte
				if response.StatusCode == http.StatusBadRequest && !downstream.Committed() &&
					(canRepairThinking || canRepairOpenAI) && repairedTargets[repairTarget] == nil &&
					schedule.total < schedule.policy.MaxAttempts {
					data, complete := inspectRecoveryError(response)
					if complete && canRepairThinking && isThinkingSignatureError(data) {
						repaired = prepareReasoningRecovery(body, rectifyThinkingSignature)
						if repaired != nil {
							reason = thinkingSignatureRecoveryReason
						}
					} else if complete && canRepairOpenAI {
						repairReason := ""
						repaired = prepareReasoningRecovery(body, func(original []byte) ([]byte, bool) {
							next, name, ok := rectifyOpenAIRequest(original, data, openAIRepairScope{
								reasoning:      canRepairOpenAIReasoning,
								functionOutput: canRepairOpenAIFunction,
							})
							if ok {
								repairReason = name
							}
							return next, ok
						})
						if repaired != nil {
							reason = repairReason
						}
					}
				}
				retryAfter := parseRetryAfter(response.Header.Get("Retry-After"), time.Now())
				if test == nil && response.StatusCode == http.StatusTooManyRequests {
					if cooldown, ok := handler.resolver.(endpoint.RateLimitController); ok {
						cooldown.RecordRateLimit(candidate, max(retryAfter, time.Duration(policy.InitialDelayMS)*time.Millisecond))
					}
				}
				schedule.graphOutcome(candidateIndex, "http_"+fmt.Sprint(response.StatusCode))
				recovering := false
				if repaired != nil && schedule.repair(candidateIndex) {
					repairedTargets[repairTarget] = repaired
					recovering = true
				} else {
					recovering = schedule.recover(candidateIndex, action, retryAfter)
				}
				if recovering {
					// Keep only a small complete error for the rare case where every
					// remaining candidate fails local preparation or health admission.
					stopRead := time.AfterFunc(time.Second, func() { attemptContext.cancel(context.DeadlineExceeded) })
					data, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
					stopRead.Stop()
					_ = response.Body.Close()
					complete := readErr == nil && len(data) <= 64*1024
					savedHeaders := downstream.Header().Clone()
					replayLastHTTP = func() {
						resetResponseHeaders(downstream.Header(), savedHeaders)
						if complete {
							response.Body = io.NopCloser(bytes.NewReader(data))
							_ = transport.WriteResponse(outWriter, response)
							if relayWriter != nil {
								_ = relayWriter.Finish()
							}
							if restoring != nil {
								_ = restoring.Finish()
							}
							if aliasWriter != nil {
								_ = aliasWriter.Finish()
							}
						} else {
							writeInferenceError(downstream, response.StatusCode, "upstream_error", "upstream returned an error and no recovery target was available", false, nil)
						}
						if recordSession != nil {
							recordSession.noteServed(candidate, plan)
							recordSession.noteFailed(errorSummaryFromHTTPStatus(response.StatusCode))
							recordSession.noteRecoveryStop("targets_exhausted")
						}
					}
					return &retryHTTPError{status: response.StatusCode, reason: reason}
				}
				if recordSession != nil {
					recordSession.noteRecoveryStop(schedule.stopReason)
				}
			}
			return transport.WriteResponse(startWriter, response)
		}
		var forwardErr error
		if turn := responsesWSTurnFromContext(request.Context()); turn != nil {
			forwardErr = turn.forward(startWriter, attemptRequest, forwardTarget, candidate, upstreamModel)
		} else {
			forwardErr = handler.forwarder.Forward(startWriter, attemptRequest, forwardTarget)
		}
		if test != nil {
			var responseErr *transport.ResponseError
			if errors.As(forwardErr, &responseErr) {
				test.responseError = forwardErr
			}
		}
		// Compatibility forwarders may not expose ObserveOutbound. The built-in
		// transport always calls it immediately before I/O.
		var preparationError *transport.TargetError
		if !attemptStarted && !errors.As(forwardErr, &preparationError) {
			schedule.started(candidateIndex)
			replayLastHTTP = nil
		}
		var retryHTTP *retryHTTPError
		_ = errors.As(forwardErr, &retryHTTP)
		attemptContext.Stop()
		timedOut := attemptContext.TimedOut()
		relayConversionFailed := false
		if relayWriter != nil && forwardErr == nil {
			if finishErr := relayWriter.Finish(); finishErr != nil {
				forwardErr = transport.NewResponseError(finishErr)
				relayConversionFailed = !downstream.Committed()
			}
		}
		if retryHTTP == nil && restoring != nil && (forwardErr == nil || classified.Streaming) {
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
		if retryHTTP == nil && relayWriter != nil && forwardErr != nil && !downstream.Committed() {
			_ = relayWriter.streamClose()
		}
		finishPrivacy()
		_ = attemptRequest.Body.Close()

		if retryHTTP != nil {
			health.RecordStatus(retryHTTP.status)
			last = executionFailure{kind: executionFailureUpstream, err: retryHTTP, endpointID: candidate.Service.ID}
			lastNetworkFailure = last
			if recordSession != nil {
				recordSession.noteRecoveryDecision(candidateIndex, schedule, retryHTTP.reason)
			}
			demoteFailedAttemptForRetry(request.Context(), recordSession, handler.requestRecords, handler.auditBlobs, errorSummaryFromHTTPStatus(retryHTTP.status), handler.recordLogger)
			if relayWriter != nil {
				_ = relayWriter.streamClose()
			}
			continue
		}
		if timedOut {
			if forwardErr == nil {
				forwardErr = context.DeadlineExceeded
			} else if !isUpstreamTimeout(forwardErr) {
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
				if session.status == contract.RequestStatusSucceeded {
					session.noteRecoveryStop("succeeded")
					schedule.graphResult(candidateIndex, "succeeded", "")
					if test == nil {
						handler.rememberResponseAffinity(request.Context(), session, candidate, plan)
						handler.rememberChannelBinding(request.Context(), session, candidate)
					}
				}
			}
			return
		}
		if request.Context().Err() != nil {
			recordSession.noteRecoveryStop("cancelled")
			health.Abandon()
			return
		}

		var upstreamErr *transport.UpstreamError
		preResponseFailure := timedOut || errors.As(forwardErr, &upstreamErr)
		var responseErr *transport.ResponseError
		bufferedResponseFailure := errors.As(forwardErr, &responseErr) &&
			!downstream.Committed()
		if turn := responsesWSTurnFromContext(request.Context()); turn != nil && turn.session.upstream.Connected() {
			// A sent WebSocket turn cannot be replayed, even when restoration
			// buffered all output or the response-start deadline expired.
			preResponseFailure = false
			bufferedResponseFailure = false
		}
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
			recordSession.noteRecoveryStop("response_committed")
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
			schedule.graphSkip(candidateIndex, last.code())
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
			schedule.graphOutcome(candidateIndex, "conversion_failed")
			if !schedule.recover(candidateIndex, contract.FailureFailover, 0) {
				recordSession.noteRecoveryStop(schedule.stopReason)
				break
			}
			recordSession.noteRecoveryDecision(candidateIndex, schedule, "conversion_failed")
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
		lastNetworkFailure = last
		if !safeRetryFailure || !body.Replayable() {
			recordSession.noteRecoveryStop("body_not_replayable")
			break
		}
		code := "upstream_unavailable"
		fallback := upstreamUnavailableFallback
		if isUpstreamTimeout(forwardErr) {
			code = "upstream_timeout"
			fallback = upstreamTimeoutFallback
		}
		message := operatorTransportMessage(forwardErr, fallback)
		action := policy.NetworkError
		if isUpstreamTimeout(forwardErr) {
			action = policy.ResponseTimeout
		}
		schedule.graphOutcome(candidateIndex, code)
		if !schedule.recover(candidateIndex, action, 0) {
			recordSession.noteRecoveryStop(schedule.stopReason)
			break
		}
		recordSession.noteRecoveryDecision(candidateIndex, schedule, code)
		demoteFailedAttemptForRetry(
			request.Context(),
			recordSession,
			handler.requestRecords,
			handler.auditBlobs,
			errorSummaryFromInference(code, message, true),
			handler.recordLogger,
		)
	}

	if session := recordSessionFromContext(request.Context()); session != nil && session.pendingAttempt != nil {
		reason := schedule.stopReason
		if reason == "" {
			reason = "targets_exhausted"
		}
		session.noteRecoveryStop(reason)
	}
	if downstream.Committed() || request.Context().Err() != nil {
		if request.Context().Err() != nil {
			recordSessionFromContext(request.Context()).noteRecoveryStop("cancelled")
		}
		return
	}
	if replayLastHTTP != nil {
		replayLastHTTP()
		return
	}
	resetResponseHeaders(downstream.Header(), initialHeaders)
	if last.kind == executionFailureNone && schedule.graph != nil {
		status := http.StatusServiceUnavailable
		if schedule.stopReason == "protocol_binding" {
			status = http.StatusConflict
		}
		message := graphStopMessage(schedule.stopReason)
		writeInferenceError(downstream, status, "routing_graph_stopped", message, false, nil)
		records.noteRecoveryStop(schedule.stopReason)
		records.noteFailed(errorSummaryFromInference("routing_graph_stopped", message, false))
		return
	}
	if last.kind == executionFailureNone {
		handler.writeResolveError(
			downstream,
			request,
			classified,
			endpoint.ErrNoHealthyEndpoint,
		)
		return
	}
	if lastNetworkFailure.kind != executionFailureNone {
		last = lastNetworkFailure
	}
	records.noteAttemptedService(last.endpointID)
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
		if isUpstreamTimeout(failure.err) {
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
	buffered, err := io.ReadAll(original)
	if err != nil {
		_ = original.Close()
		source.Close()
		return nil, err
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
	if status == http.StatusTooManyRequests {
		outcome.Abandon()
		return
	}
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

func isUpstreamTimeout(err error) bool {
	var timeout net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout())
}
