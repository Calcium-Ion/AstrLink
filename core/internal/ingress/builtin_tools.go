package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/builtintools"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

type builtinInternalKey struct{}
type builtinTestSessionKey struct{}
type builtinInternal struct {
	SharedSession *recordSession
	OnEvent       func(builtintools.Object) error
	Model         string
	Target        contract.ServiceID
	Native        bool
}

func builtinInternalFrom(ctx context.Context) *builtinInternal {
	value, _ := ctx.Value(builtinInternalKey{}).(*builtinInternal)
	return value
}

func (handler *Handler) tryBuiltinTools(writer http.ResponseWriter, request *http.Request, classified Request, settings contract.RoutingSettings) bool {
	if builtinInternalFrom(request.Context()) != nil || classified.Protocol != contract.ProtocolOpenAIResponses {
		return false
	}
	if settings.BuiltinTools == nil && !strings.HasPrefix(classified.PreviousResponseID, "resp_tool_") {
		return false
	}
	limit := handler.maxRequestBodyBytes
	if limit == 0 {
		limit = 128 << 20
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	_ = request.Body.Close()
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.GetBody = nil
	var input builtintools.Object
	if err != nil || int64(len(body)) > limit || json.Unmarshal(body, &input) != nil {
		writeInferenceError(writer, 400, "invalid_request", "tool request could not be decoded", false, nil)
		return true
	}
	if !builtintools.Needed(input, settings.BuiltinTools) {
		return false
	}
	session := recordSessionFromContext(request.Context())
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	if turn := responsesWSTurnFromContext(ctx); turn != nil {
		go func() {
			select {
			case <-turn.controls:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	principal, _ := AccessTokenIDFromContext(ctx)
	mainMode := &builtinInternal{Model: classified.routingModel()}
	runner := builtintools.Runner{StreamModel: func(ctx context.Context, body builtintools.Object, onEvent func(builtintools.Object) error) (builtintools.Object, error) {
		mainMode.OnEvent = onEvent
		return handler.builtinModel(ctx, request, body, mainMode)
	}, Resume: func(state builtintools.State) error {
		if state.ServiceID != "" {
			mainMode.Target = contract.ServiceID(state.ServiceID)
			mainMode.Model = state.RoutingModel
		}
		return nil
	}, Binding: func() (string, string) { return string(mainMode.Target), mainMode.Model }, Store: &handler.builtinStates, Executor: handler.builtinExecutor(request, classified), Model: func(ctx context.Context, body builtintools.Object) (builtintools.Object, error) {
		return handler.builtinModel(ctx, request, body, mainMode)
	}, Observe: func(kind string, config contract.BuiltinTool, started time.Time, result builtintools.Result, err error) {
		status := contract.RequestStatusSucceeded
		if err != nil {
			status = contract.RequestStatusFailed
		}
		summary := fmt.Sprintf("%s · %s %s · %d ms", kind, config.Backend, config.ServiceID, time.Since(started).Milliseconds())
		if result.Usage != nil {
			summary += " · usage " + builtintools.Text(result.Usage)
		}
		if err != nil {
			summary += " · " + err.Error()
		}
		ended := time.Now().UTC()
		session.events = append(session.events, contract.RequestEvent{Kind: contract.RequestEventUpstream, StartedAt: started.UTC(), EndedAt: &ended, Status: status, Summary: sanitizeSummary(summary), AttemptIndex: session.attemptIndex})
		if err != nil {
			session.noteFailed(errorSummaryFromInference("builtin_tool_failed", err.Error(), false))
		}
	}}
	err = runner.Run(ctx, writer, string(principal), input, settings.BuiltinTools)
	if err != nil {
		var streamed *builtintools.StreamFailure
		if !errors.As(err, &streamed) {
			writeInferenceError(writer, http.StatusBadGateway, "builtin_tool_failed", err.Error(), false, nil)
		}
		session.noteFailed(errorSummaryFromInference("builtin_tool_failed", err.Error(), false))
		if ctx.Err() != nil {
			session.noteCancelled()
		}
	} else if session.status == contract.RequestStatusPending {
		session.noteSucceeded()
	}
	return true
}

func (handler *Handler) builtinExecutor(request *http.Request, classified Request) builtintools.Executor {
	return builtintools.Executor{Secrets: handler.proxyCredentials, Native: func(ctx context.Context, config contract.BuiltinTool, body builtintools.Object) (builtintools.Object, error) {
		shared, _ := ctx.Value(builtinTestSessionKey{}).(*recordSession)
		return handler.builtinModel(ctx, request, body, &builtinInternal{Target: config.ServiceID, Native: true, SharedSession: shared})
	}, Images: handler.builtinServiceImages, Inspect: func(ctx context.Context, args builtintools.Object) (builtintools.Object, error) {
		body := builtintools.Text(builtintools.Object{"input": builtintools.Text(args), "model": classified.Model})
		inspect := request.Clone(ctx)
		inspect.Body = io.NopCloser(strings.NewReader(body))
		inspect.GetBody = nil
		inspect.ContentLength = int64(len(body))
		capture := &builtinCapture{header: make(http.Header)}
		finish, _, err := handler.applyPrivacy(capture, inspect, classified, "")
		defer finish()
		if err != nil {
			return nil, fmt.Errorf("tool arguments blocked by privacy policy")
		}
		data, err := io.ReadAll(inspect.Body)
		_ = inspect.Body.Close()
		if err != nil {
			return nil, err
		}
		var wrapped builtintools.Object
		var result builtintools.Object
		if json.Unmarshal(data, &wrapped) != nil || json.Unmarshal([]byte(builtintools.String(wrapped["input"])), &result) != nil {
			return nil, fmt.Errorf("tool arguments could not be safely inspected")
		}
		return result, nil
	}}
}

func (handler *Handler) builtinModel(ctx context.Context, original *http.Request, body builtintools.Object, mode *builtinInternal) (builtintools.Object, error) {
	if mode == nil {
		mode = &builtinInternal{}
	}
	ctx = context.WithValue(ctx, responsesWSTurnKey{}, (*responsesWSTurn)(nil))
	ctx = context.WithValue(ctx, builtinInternalKey{}, mode)
	request := original.Clone(ctx)
	urlCopy := *original.URL
	request.URL = &urlCopy
	request.URL.Path = "/v1/responses"
	request.URL.RawPath = ""
	body = builtintools.Clone(body)
	if mode.Model != "" {
		body["model"] = mode.Model
	}
	data := []byte(builtintools.Text(body))
	request.Method = http.MethodPost
	request.Body = io.NopCloser(bytes.NewReader(data))
	request.GetBody = nil
	request.ContentLength = int64(len(data))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Del("Content-Length")
	request.Header.Del("Content-Encoding")
	capture := &builtinCapture{header: make(http.Header), onEvent: mode.OnEvent}
	handler.ServeHTTP(capture, request)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if capture.err != nil {
		return nil, capture.err
	}
	if capture.status >= 400 {
		return nil, fmt.Errorf("model request failed with HTTP %d", capture.status)
	}
	if strings.Contains(capture.header.Get("Content-Type"), "text/event-stream") {
		if capture.response == nil {
			return nil, fmt.Errorf("model stream ended without a terminal response")
		}
		return capture.response, nil
	}
	var response builtintools.Object
	if json.Unmarshal(capture.buffer, &response) != nil {
		return nil, fmt.Errorf("invalid model response")
	}
	return response, nil
}

// builtinServiceImages sends one Images API request to the configured
// provider. It shares the provider's credential, proxy and gateway-header
// stripping with ordinary forwarding, but never retries or fails over: a lost
// response must not cause a second billed image.
func (handler *Handler) builtinServiceImages(ctx context.Context, config contract.BuiltinTool, path, contentType string, body io.Reader) (builtintools.Object, error) {
	resolver, ok := handler.resolver.(endpoint.ServiceResolver)
	if !ok {
		return nil, fmt.Errorf("provider lookup is unavailable")
	}
	candidate, err := resolver.ResolveService(ctx, config.ServiceID)
	if err != nil {
		return nil, fmt.Errorf("image provider is unavailable or disabled")
	}
	if !contract.BuiltinImagesServiceKind(candidate.Service.Kind) {
		return nil, fmt.Errorf("image provider does not offer an OpenAI Images API")
	}
	authorization, err := candidate.AuthorizationEndpoint()
	if err != nil {
		return nil, fmt.Errorf("image provider configuration is invalid")
	}
	baseURL, err := url.Parse(candidate.EffectiveBaseURL())
	if err != nil {
		return nil, fmt.Errorf("image provider configuration is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/v1"+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", contentType)
	// An empty value suppresses Go's default client identity.
	request.Header.Set("User-Agent", "")
	headers, err := handler.authorizer.Headers(ctx, authorization, request.Header)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("image provider credential is unavailable")
	}
	capture := &builtinCapture{header: make(http.Header)}
	err = handler.forwarder.Forward(capture, request, transport.Target{
		Service: candidate.Service, ProxyCredentials: handler.proxyCredentials,
		BaseURL: baseURL, RequestHeaders: headers,
	})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if capture.err != nil {
		return nil, capture.err
	}
	if err != nil {
		return nil, fmt.Errorf("image provider connection failed")
	}
	if capture.status < 200 || capture.status >= 300 {
		return nil, fmt.Errorf("image provider returned HTTP %d", capture.status)
	}
	var response builtintools.Object
	if json.Unmarshal(capture.buffer, &response) != nil || response == nil {
		return nil, fmt.Errorf("invalid image provider response")
	}
	return response, nil
}

// builtinCapture parses bounded individual SSE frames. Large image payloads
// never pass through the ordinary eight-MiB text inspection buffer here.
type builtinCapture struct {
	onEvent  func(builtintools.Object) error
	header   http.Header
	status   int
	buffer   []byte
	response builtintools.Object
	err      error
}

func (c *builtinCapture) Header() http.Header { return c.header }
func (c *builtinCapture) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}
func (c *builtinCapture) Flush() {}
func (c *builtinCapture) Write(data []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	if c.status == 0 {
		c.status = 200
	}
	c.buffer = append(c.buffer, data...)
	if len(c.buffer) > builtintools.MaxResultBytes {
		c.err = fmt.Errorf("model response exceeds tool buffer limit")
		return 0, c.err
	}
	if !strings.Contains(c.header.Get("Content-Type"), "text/event-stream") {
		return len(data), nil
	}
	c.buffer = bytes.ReplaceAll(c.buffer, []byte("\r\n"), []byte("\n"))
	for {
		end := bytes.Index(c.buffer, []byte("\n\n"))
		if end < 0 {
			break
		}
		_, payload := parseSSEFrame(c.buffer[:end])
		c.buffer = c.buffer[end+2:]
		if len(payload) == 0 {
			continue
		}
		var event builtintools.Object
		if json.Unmarshal(payload, &event) != nil {
			c.err = fmt.Errorf("invalid model stream event")
			return 0, c.err
		}
		if c.onEvent != nil {
			if err := c.onEvent(event); err != nil {
				c.err = err
				return 0, err
			}
		}
		switch builtintools.String(event["type"]) {
		case "response.completed", "response.incomplete":
			c.response = builtintools.Map(event["response"])
		case "response.failed", "error":
			c.err = builtinStreamError(event)
			return 0, c.err
		}
	}
	return len(data), nil
}

func builtinStreamError(event builtintools.Object) error {
	problem := builtintools.Map(event["error"])
	if len(problem) == 0 {
		problem = builtintools.Map(builtintools.Map(event["response"])["error"])
	}
	if len(problem) == 0 {
		problem = event
	}
	// Error codes are useful for diagnosis without copying arbitrary provider
	// messages (which may echo prompts or credentials) into ordinary logs.
	code := builtintools.String(problem["code"])
	if code == "" {
		code = builtintools.String(problem["type"])
	}
	switch code {
	case "rate_limit_exceeded", "too_many_requests", "insufficient_quota",
		"server_error", "invalid_request_error", "invalid_api_key",
		"authentication_error", "permission_denied", "unsupported_parameter",
		"model_not_found", "content_policy_violation":
		return fmt.Errorf("model stream reported failure: %s", code)
	default:
		return fmt.Errorf("model stream reported failure")
	}
}

func filterBuiltinCandidates(ctx context.Context, candidates []endpoint.Resolved) []endpoint.Resolved {
	mode := builtinInternalFrom(ctx)
	if mode == nil || mode.Target == "" {
		return candidates
	}
	for _, candidate := range candidates {
		if candidate.CanonicalService().ID != mode.Target || (mode.Native && candidate.PlanType == contract.PlanTypeRelayKit) {
			continue
		}
		if !mode.Native {
			candidate.Failover = &contract.FailoverPolicy{Enabled: false, Strategy: contract.FailoverOnly, MaxAttempts: 1}
			return []endpoint.Resolved{candidate}
		}
		// Tool execution has no recovery: a lost image response must never
		// cause another image generation attempt or a different provider.
		disabled := false
		candidate.Failover = &contract.FailoverPolicy{Enabled: false, Strategy: contract.FailoverOnly, MaxAttempts: 1}
		candidate.FailurePolicy = &contract.FailurePolicy{NetworkError: contract.FailureStop, ResponseTimeout: contract.FailureStop, HTTPStatus: map[string]contract.FailureAction{}, ThinkingSignatureRecovery: &disabled, OpenAIReasoningRecovery: &disabled, OpenAIFunctionOutputRecovery: &disabled}
		return []endpoint.Resolved{candidate}
	}
	return nil
}

// TestBuiltinTool is called only by the authenticated control API. No model
// request is issued when editing or saving tool configuration.
func (handler *Handler) TestBuiltinTool(ctx context.Context, kind string, config contract.BuiltinTool) (map[string]any, error) {
	if err := config.Validate(kind); err != nil {
		return nil, err
	}
	args := builtintools.Object{"action": "search", "query": "OpenAI official website"}
	if kind == "image_generation" {
		args = builtintools.Object{"prompt": "A small solid blue circle on a white background."}
	}
	payload := builtintools.Text(builtintools.Object{"model": config.Model, "input": builtintools.Text(args)})
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/v1/responses", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	classified := Request{Protocol: contract.ProtocolOpenAIResponses, Model: config.Model, InputPreview: "Builtin tool test · " + kind}
	session := handler.startRecordSession(request, classified)
	session.addEvent(contract.RequestEventAccepted, contract.RequestStatusPending, "builtin tool test · "+kind)
	session.persistPending(ctx, handler.requestRecords, handler.recordLogger)
	defer func() {
		session.finish(context.Background(), handler.requestRecords, handler.auditBlobs, handler.recordLogger)
	}()
	testCtx := context.WithValue(withRecordSession(ctx, session), builtinTestSessionKey{}, session)
	request = request.WithContext(testCtx)
	started := time.Now()
	result, err := handler.builtinExecutor(request, classified).Execute(request.Context(), config, builtintools.Invocation{Kind: kind, Arguments: args, Options: builtintools.Object{"type": kind}})
	if config.Backend != "upstream" {
		ended := time.Now().UTC()
		status := contract.RequestStatusSucceeded
		summary := kind + " · " + config.Backend + " · " + fmt.Sprint(ended.Sub(started).Milliseconds()) + " ms"
		if result.Usage != nil {
			summary += " · usage " + builtintools.Text(result.Usage)
		}
		if err != nil {
			status = contract.RequestStatusFailed
			summary += " · " + err.Error()
		}
		session.events = append(session.events, contract.RequestEvent{Kind: contract.RequestEventUpstream, StartedAt: started.UTC(), EndedAt: &ended, Status: status, Summary: sanitizeSummary(summary)})
	}
	if err != nil {
		if session.status == contract.RequestStatusSucceeded {
			session.status = contract.RequestStatusPending
		}
		session.noteFailed(errorSummaryFromInference("builtin_tool_test_failed", err.Error(), false))
		if ctx.Err() != nil {
			session.noteCancelled()
		}
		return nil, err
	}
	session.noteSucceeded()
	return map[string]any{"ok": true, "duration_ms": time.Since(started).Milliseconds(), "usage": result.Usage, "result_count": len(result.Items)}, nil
}
