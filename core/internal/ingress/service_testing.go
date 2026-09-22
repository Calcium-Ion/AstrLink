package ingress

import (
	"context"
	"io"
	"net/http"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
)

// ServiceTestObserver observes one in-process test. Callbacks run synchronously
// on the execution goroutine, before the corresponding client response bytes.
// Authorization contains secrets and must never be logged or persisted.
type ServiceTestObserver struct {
	Authorization func(http.Header)
	Outbound      func()
	Response      func(int)
	// WrapResponseBody bounds upstream consumption before restoration buffers it.
	WrapResponseBody func(io.ReadCloser) io.ReadCloser
}

type serviceTestExecution struct {
	plan          contract.ExecutionPlan
	observer      ServiceTestObserver
	responseError error
}

// ServeServiceTest is an in-process entry point for the authenticated control
// API, never an HTTP route or a client-controlled execution mode. It shares the
// inference pipeline while fixing the target and allowing exactly one attempt.
// A disabled service may be tested without changing its saved configuration.
func (handler *Handler) ServeServiceTest(
	writer http.ResponseWriter,
	request *http.Request,
	service contract.Service,
	input contract.ServiceTestRequest,
	baseURL string,
	observer ServiceTestObserver,
) error {
	if err := input.Validate(service); err != nil {
		writeInferenceError(writer, http.StatusUnprocessableEntity, "invalid_test", err.Error(), false, nil)
		return nil
	}
	// http.NewRequest supplies GetBody for an in-memory payload, unlike an
	// inbound server request. Use the inspected body so audit capture cannot be
	// bypassed by reopening the original reader during execution.
	request.GetBody = nil
	classified, finishMetadata, err := handler.classify(request)
	defer finishMetadata()
	// The control input is authoritative, including model IDs with a models/
	// prefix which Google's URL classifier normalizes away.
	classified.Protocol, classified.Model, classified.Streaming = input.Protocol, input.Model, input.Stream
	session := handler.startRecordSession(request, classified)
	serviceID := service.ID
	session.endpointID = &serviceID
	session.persistPending(request.Context(), handler.requestRecords, handler.recordLogger)
	outWriter := session.wrap(writer)
	request = request.WithContext(withRecordSession(request.Context(), session))
	defer func() {
		if request.Context().Err() != nil {
			session.noteCancelled()
		}
		session.finish(context.Background(), handler.requestRecords, handler.auditBlobs, handler.recordLogger)
	}()
	if request.Context().Err() != nil {
		return request.Context().Err()
	}
	if err != nil {
		writeInferenceError(outWriter, http.StatusBadRequest, "invalid_request", "test request metadata could not be identified", false, nil)
		session.noteFailed(errorSummaryFromInference("invalid_request", "test request metadata could not be identified", false))
		return nil
	}
	if service.Kind.IsSubscription() && (service.Subscription == nil || service.Subscription.Status != contract.SubscriptionStatusConnected || baseURL == "") {
		writeInferenceError(outWriter, http.StatusServiceUnavailable, "not_connected", "Subscription is not connected. Sign in before testing.", false, nil)
		session.noteFailed(errorSummaryFromInference("not_connected", "subscription is not connected", false))
		return nil
	}
	mode := contract.CapabilityModeNative
	for _, capability := range service.Capabilities {
		if capability.Protocol == input.Protocol && capability.ConvertTo == "" && (!input.Stream || capability.Streaming) {
			mode = capability.Mode
			break
		}
	}
	planType := contract.PlanTypeNative
	if mode == contract.CapabilityModeDelegated {
		planType = contract.PlanTypeDelegated
	}
	execution := &serviceTestExecution{
		observer: observer,
		plan: contract.ExecutionPlan{
			ServiceID: service.ID, Type: planType, InputProtocol: input.Protocol,
			UpstreamProtocol: input.Protocol, Streaming: input.Stream,
			ConversionPath: []contract.ConversionEdge{},
		},
	}
	disabled := false
	policy := contract.FailurePolicy{
		NetworkError: contract.FailureStop, ResponseTimeout: contract.FailureStop,
		HTTPStatus:                map[string]contract.FailureAction{},
		ThinkingSignatureRecovery: &disabled, OpenAIReasoningRecovery: &disabled,
		OpenAIFunctionOutputRecovery: &disabled,
	}
	candidate := endpoint.Resolved{
		Service: service, BaseURL: baseURL, Mode: mode, PlanType: planType,
		FailurePolicy: &policy,
		Failover:      &contract.FailoverPolicy{Enabled: false, Strategy: contract.RetryFirst, MaxAttempts: 1},
	}
	session.noteSelected(candidate, execution.plan)
	handler.executeCandidatesWithTest(outWriter, request, classified, []endpoint.Resolved{candidate}, execution)
	if request.Context().Err() != nil {
		return request.Context().Err()
	}
	// Pre-response transport failures already have a gateway error envelope.
	// Propagate only interrupted bodies, so the parser cannot mistake EOF for a
	// complete response after headers have been committed.
	return execution.responseError
}
