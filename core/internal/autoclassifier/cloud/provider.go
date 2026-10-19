// Package cloud adapts external classification providers (Jev Decisions and
// OpenAI Decisions) to the autoclassifier Outcome contract.
//
// Fail-open is inherited from the parent package: every transport, status, or
// parsing failure maps to an Outcome fallback reason, never to an error that
// could block the caller's request. Outbound requests carry only the
// provider's own authentication; no AstrLink identity is injected.
package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
)

// Protocol selects the wire adapter for one classification call.
type Protocol string

const (
	ProtocolJev             Protocol = contract.ClassifierProtocolJev
	ProtocolOpenAIDecisions Protocol = contract.ClassifierProtocolOpenAIDecisions
)

// maxResponseBytes bounds one provider reply. Classification answers are
// small documents; anything larger is a misrouted endpoint.
const maxResponseBytes = 1 << 20

// ErrInvalidProtocol is reported through the Outcome fallback when the caller
// requests an adapter this build does not know.
var ErrInvalidProtocol = errors.New("unknown classifier protocol")

// Input is one fully resolved classification request: the caller has already
// merged group overrides into the effective prompt, timeout, and labels.
type Input struct {
	Protocol Protocol
	Endpoint string
	// APIKey is unsealed by the caller and zeroed here after the request is
	// built; it never appears in an Outcome or an error.
	APIKey    []byte
	Model     string
	Prompt    string
	Labels    []string
	Text      string
	TimeoutMS int
	// Dimensions are the custom dimensions this call asks about, in addition
	// to the built-in task_type. Empty is the single-question request every
	// existing deployment sends, and it produces byte-identical wire traffic.
	Dimensions []contract.ClassifierDimension
}

// Client is the package-level HTTP client. It honors the environment proxy so
// users behind one can still classify, and shares connections across calls.
// Compression stays off so the default outbound header set stays minimal.
var Client = &http.Client{
	Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DisableCompression: true},
}

// Classify performs one provider call and always returns an Outcome. A nil
// client falls back to the shared package client.
func Classify(ctx context.Context, client *http.Client, in Input) autoclassifier.Outcome {
	protocol := in.Protocol
	classifierID := contract.AutoClassifierID("cloud:" + string(protocol))
	outcome := autoclassifier.Outcome{ClassifierID: classifierID}
	if len(bytes.TrimSpace(in.APIKey)) == 0 {
		outcome.FallbackReason = autoclassifier.FallbackUnavailable
		return outcome
	}
	timeout := time.Duration(in.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = time.Duration(contract.DefaultClassifierTimeoutMS) * time.Millisecond
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var requestBuilder func(Input) (string, []byte, error)
	var responseParser func([]byte, []string) (string, float64, []autoclassifier.Candidate, error)
	var dimensionParser func([]byte, Input) (map[string]autoclassifier.DimensionAnswer, error)
	switch protocol {
	case ProtocolJev:
		requestBuilder, responseParser = buildJevRequest, parseJevResponse
		dimensionParser = parseJevDimensions
	case ProtocolOpenAIDecisions:
		requestBuilder, responseParser = buildOpenAIDecisionsRequest, parseOpenAIDecisionsResponse
		dimensionParser = parseOpenAIDecisionsDimensions
	default:
		outcome.FallbackReason = autoclassifier.FallbackUnavailable
		return outcome
	}
	endpoint, body, err := requestBuilder(in)
	if err != nil {
		outcome.FallbackReason = autoclassifier.FallbackUnavailable
		return outcome
	}
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		outcome.FallbackReason = autoclassifier.FallbackUnavailable
		return outcome
	}
	// The outbound header set is a fixed whitelist: the provider's own
	// authentication plus JSON content negotiation. No gateway identity,
	// branding, or diagnostic header ever leaves through this client.
	request.Header.Set("Authorization", "Bearer "+string(in.APIKey))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "")
	clear(in.APIKey)

	if client == nil {
		client = Client
	}
	response, err := client.Do(request)
	if err != nil {
		outcome.FallbackReason = fallbackFromTransport(callCtx, err)
		return outcome
	}
	defer func() {
		_ = response.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
	}()
	if response.StatusCode == http.StatusTooManyRequests ||
		response.StatusCode == http.StatusServiceUnavailable ||
		response.StatusCode == http.StatusGatewayTimeout ||
		response.StatusCode == http.StatusBadGateway ||
		// 529 is the shared-capacity overload signal of some fronts.
		response.StatusCode == 529 {
		outcome.FallbackReason = autoclassifier.FallbackBusy
		return outcome
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		outcome.FallbackReason = autoclassifier.FallbackUnavailable
		return outcome
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(payload) > maxResponseBytes {
		outcome.FallbackReason = autoclassifier.FallbackUnavailable
		return outcome
	}
	category, confidence, candidates, err := responseParser(payload, in.Labels)
	if err != nil {
		switch {
		case errors.Is(err, errRefusal):
			outcome.FallbackReason = FallbackRefusal
		case errors.Is(err, errInvalidOutput):
			outcome.FallbackReason = FallbackInvalidOutput
		default:
			outcome.FallbackReason = autoclassifier.FallbackUnavailable
		}
		return outcome
	}
	// A multi-dimension reply is validated as one document: a malformed
	// dimension fails the whole attempt through the same mapping a malformed
	// task_type uses, rather than keeping the readable half. With no dimensions
	// requested this stays out of the way entirely and the request is
	// byte-identical to the single-dimension call.
	if len(in.Dimensions) > 0 {
		dimensions, err := dimensionParser(payload, in)
		if err != nil {
			switch {
			case errors.Is(err, errRefusal):
				outcome.FallbackReason = FallbackRefusal
			case errors.Is(err, errInvalidOutput):
				outcome.FallbackReason = FallbackInvalidOutput
			default:
				outcome.FallbackReason = autoclassifier.FallbackUnavailable
			}
			return outcome
		}
		outcome.Dimensions = dimensions
	}
	outcome.Category = category
	outcome.Confidence = confidence
	outcome.Candidates = candidates
	return outcome
}

// FallbackReason values this package re-exports from the shared
// autoclassifier reason domain so existing callers keep their names while
// every source reports one domain. Callers map them to stable record codes;
// neither ever blocks the caller's request.
const (
	FallbackRefusal       = autoclassifier.FallbackRefusal
	FallbackInvalidOutput = autoclassifier.FallbackInvalidOutput
)

var errRefusal = errors.New("provider refused to answer")

func fallbackFromTransport(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return autoclassifier.FallbackTimeout
	}
	return autoclassifier.FallbackUnavailable
}

// labelDescriptions maps each option key to the description sent to the
// provider. Labels carry no per-label description in V1, so the label itself
// is the description.
func labelDescriptions(labels []string) map[string]string {
	criteria := make(map[string]string, len(labels))
	for _, label := range labels {
		criteria[label] = label
	}
	return criteria
}

func marshalRequestBody(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode classifier request: %w", err)
	}
	return encoded, nil
}
