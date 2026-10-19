package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/classifieroutput"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
)

// classifierInternalKey marks one in-process model-classification sub-call.
// It is deliberately a separate flag from builtinInternalKey: the builtin-tool
// mode carries tool semantics (native target pinning, event streaming) that
// do not apply here, and reusing it would make the two exemption sets drift
// apart. The flag buys the same per-point exemptions in ServeHTTP: inference
// boundary, record-session reuse, no second record, no recursive model
// routing, and no privacy check of its own — model routing already passed the
// text this sub-call carries through the parent request's policy.
type classifierInternalKey struct{}

type classifierInternal struct {
	SharedSession *recordSession
	Target        contract.ServiceID
	Model         string
}

func classifierInternalFrom(ctx context.Context) *classifierInternal {
	value, _ := ctx.Value(classifierInternalKey{}).(*classifierInternal)
	return value
}

// classificationSlots bounds concurrent existing-model classification calls.
// The TryAcquire semantic keeps a busy gateway on its deterministic fallback
// instead of queueing classification behind inference.
var classificationSlots = make(chan struct{}, 4)

// Errors reported by ClassifyModel. The routing seam maps every one of them
// onto a fail-open fallback code; none of them may fail the caller's request.
var (
	// ErrClassificationVirtualModel marks a target naming a gateway virtual
	// model (astrlink/...). Virtual models route through model routing
	// itself; classifying through one would recurse.
	ErrClassificationVirtualModel = errors.New("classification target is a virtual model")
	// ErrClassificationInvalidTarget marks an empty service or model.
	ErrClassificationInvalidTarget = errors.New("classification target is incomplete")
	// ErrClassificationInvalidText marks empty classification input.
	ErrClassificationInvalidText = errors.New("classification text is empty")
	// ErrClassificationInvalidInstruction marks an empty instruction. The
	// caller compiles it from the label snapshot; this seam cannot invent
	// one, and the legacy bare-label default could never satisfy the strict
	// output parser.
	ErrClassificationInvalidInstruction = errors.New("classification instruction is empty")
	// ErrClassificationBusy marks the concurrency slots being exhausted.
	ErrClassificationBusy = errors.New("classification slots are busy")
	// ErrClassificationTimeout marks the caller's deadline running out.
	ErrClassificationTimeout = errors.New("classification call timed out")
	// ErrClassificationUnavailable marks transport and non-2xx failures.
	ErrClassificationUnavailable = errors.New("classification call failed")
)

// ModelClassificationCaller performs one bounded, non-streaming generation
// call against an existing gateway model and returns the assistant-visible
// text. Production routing and the control-plane dry-run share this seam so
// both classify through the same execution chain, authorization, proxy, and
// outbound header scrubbing. The signature stays on shared contract and
// builtin types so consumers declare their own interface (the ingress
// handler satisfies it structurally) without importing this package.
type ModelClassificationCaller interface {
	ClassifyModel(ctx context.Context, serviceID contract.ServiceID, model, instruction, sanitizedText string, timeoutMS int) (string, error)
}

// ClassifyModel is the ingress implementation of the classification caller:
// it re-enters the shared pipeline in process with the classifierInternal
// flag set, never through a network hop to the listening port. instruction
// travels as the request's top-level instructions field so it reaches the
// target protocol as its system message; the untrusted user text always
// travels separately as the input, never spliced into the instruction.
func (handler *Handler) ClassifyModel(ctx context.Context, serviceID contract.ServiceID, model, instruction, sanitizedText string, timeoutMS int) (string, error) {
	if model == "" || serviceID == "" {
		return "", ErrClassificationInvalidTarget
	}
	// D1: a virtual model name would re-enter model routing (the sub-call is
	// exempt from it, so the pair would misresolve) and could recurse once
	// routing stops being skipped. Reject before any billing can happen.
	if strings.HasPrefix(model, contract.ModelRoutingModelPrefix) || model == contract.AstrLinkAutoModelID {
		return "", fmt.Errorf("%w: %q", ErrClassificationVirtualModel, model)
	}
	if strings.TrimSpace(sanitizedText) == "" {
		return "", ErrClassificationInvalidText
	}
	if strings.TrimSpace(instruction) == "" {
		return "", ErrClassificationInvalidInstruction
	}
	select {
	case classificationSlots <- struct{}{}:
		defer func() { <-classificationSlots }()
	default:
		return "", ErrClassificationBusy
	}
	budget := time.Duration(timeoutMS) * time.Millisecond
	if budget <= 0 {
		budget = time.Duration(contract.DefaultClassifierExistingModelTimeoutMS) * time.Millisecond
	}
	// The parent deadline is a hard ceiling: classification may not outlive
	// the request it serves. An exhausted parent skips classification and
	// fails open rather than stretching the request.
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < budget {
			budget = remaining
		}
	}
	if budget <= 0 {
		return "", ErrClassificationTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	body, err := json.Marshal(map[string]any{
		"model":        model,
		"instructions": instruction,
		"input":        sanitizedText,
		"stream":       false,
	})
	if err != nil {
		return "", ErrClassificationUnavailable
	}
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, "http://localhost/v1/responses", bytes.NewReader(body))
	if err != nil {
		return "", ErrClassificationUnavailable
	}
	request.Header.Set("Content-Type", "application/json")
	// The URL above is a placeholder never dialed: resolution picks the real
	// target from the service registry, same as the builtin tool seam.
	request = request.WithContext(context.WithValue(callCtx, classifierInternalKey{}, &classifierInternal{
		SharedSession: recordSessionFromContext(callCtx),
		Target:        serviceID,
		Model:         model,
	}))
	capture := &builtinCapture{header: make(http.Header)}
	handler.ServeHTTP(capture, request)
	if callCtx.Err() != nil {
		return "", ErrClassificationTimeout
	}
	if capture.err != nil {
		return "", ErrClassificationUnavailable
	}
	if capture.status >= 400 {
		return "", fmt.Errorf("%w: model returned HTTP %d", ErrClassificationUnavailable, capture.status)
	}
	if strings.Contains(capture.header.Get("Content-Type"), "text/event-stream") {
		return "", fmt.Errorf("%w: streaming classification responses are unsupported", ErrClassificationUnavailable)
	}
	return classifieroutput.AssistantText(capture.buffer)
}

// filterClassifierCandidates narrows a resolved candidate list to the one
// exact service+model pair the classification call pinned. There is no
// recovery: a lost response must never trigger a second billed attempt, so
// failover is disabled and failure handling stops at the first error.
func filterClassifierCandidates(ctx context.Context, candidates []endpoint.Resolved) []endpoint.Resolved {
	mode := classifierInternalFrom(ctx)
	if mode == nil {
		return candidates
	}
	for index, candidate := range candidates {
		if candidate.CanonicalService().ID != mode.Target {
			continue
		}
		if candidate.UpstreamModel != "" && candidate.UpstreamModel != mode.Model {
			continue
		}
		candidates[index].UpstreamModel = mode.Model
		disabled := false
		candidates[index].Failover = &contract.FailoverPolicy{Enabled: false, Strategy: contract.FailoverOnly, MaxAttempts: 1}
		candidates[index].FailurePolicy = &contract.FailurePolicy{
			NetworkError: contract.FailureStop, ResponseTimeout: contract.FailureStop,
			HTTPStatus:                map[string]contract.FailureAction{},
			ThinkingSignatureRecovery: &disabled, OpenAIReasoningRecovery: &disabled,
			OpenAIFunctionOutputRecovery: &disabled,
		}
		return []endpoint.Resolved{candidates[index]}
	}
	return nil
}
