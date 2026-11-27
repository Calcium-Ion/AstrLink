package taskplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/new-api/pluginkit"
)

// hookTimeout bounds one hook call, its utils.fetch requests included.
const hookTimeout = 45 * time.Second

// fetchHooks are the driver hooks that may read upstream data with
// utils.fetch: those whose failure is harmless or retried.
var fetchHooks = []string{"buildSubmitRequest", "buildQueryRequest", "parseTaskResult", "buildBatchQueryRequest", "parseBatchResult", "buildContentRequest"}

// Bounds of what a submission and a poll read of the vendor's answer. A
// poll may carry the result itself, such as a video in base64.
const (
	maxSubmitAnswer = 64 << 20
	maxPollAnswer   = 96 << 20
	maxErrorAnswer  = 1 << 20
)

// Intent is what a decoder returns: a submission, or a query of tasks.
type Intent struct {
	Kind          string          `json:"kind"`
	Model         string          `json:"model,omitempty"`
	Action        string          `json:"action,omitempty"`
	RequestBody   json.RawMessage `json:"requestBody,omitempty"`
	OriginTaskIDs []string        `json:"originTaskIds,omitempty"`
	TaskIDs       []string        `json:"taskIds,omitempty"`
}

// Submission is a generation a client asks for, as a decoder gave it.
type Submission struct {
	PluginKey string
	// Surface is the protocol or native route the request came by, and
	// Operation what it asked of it.
	Surface   string
	Operation string
	Intent    Intent
	// Body holds the files the client uploaded.
	Body           *Body
	RequestHeaders http.Header
	Owner          string
	// Retain is false for a route that keeps no result once the vendor
	// answers at once.
	Retain bool
	// Request is what the protocol's answers echo of the request.
	Request   json.RawMessage
	RequestID string
	// Egress checks the JSON body buildSubmitRequest built before it is
	// sent; an error stops the submission on every provider.
	Egress func(ctx context.Context, body json.RawMessage) error
}

// Candidate is a provider a submission may run on: the service and the
// model it serves the request's model as.
type Candidate struct {
	Service       contract.Service
	UpstreamModel string
}

// Attempt is one try of a submission on a provider.
type Attempt struct {
	Service contract.ServiceID
	Started time.Time
	Ended   time.Time
	// Status is the vendor's HTTP status, 0 when none came.
	Status int
	Err    error
}

// Submitted is a submission the vendor accepted.
type Submitted struct {
	Task    storage.PluginTask
	Service contract.Service
	// Data is the vendor's answer as the task keeps it, which a route
	// that keeps no result does not store.
	Data     json.RawMessage
	Attempts []Attempt
}

// taskResult is the NormalizedTaskResult the parse hooks return.
type taskResult = pluginkit.TaskResult

// statuses are the statuses a parse hook may report.
var statuses = []string{
	contract.PluginTaskNotStart, contract.PluginTaskSubmitted, contract.PluginTaskQueued, contract.PluginTaskInProgress,
	contract.PluginTaskSuccess, contract.PluginTaskFailure,
}

// text reads a JSON string or number as text, as a task id or a progress
// may be either.
func text(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var value string
	if json.Unmarshal(trimmed, &value) == nil {
		return strings.TrimSpace(value)
	}
	var number json.Number
	if json.Unmarshal(trimmed, &number) == nil {
		return number.String()
	}
	return ""
}

// progressText reads a progress: "50%", 50 or 0.5 all read 50%.
func progressText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.HasSuffix(value, "%") {
		var number float64
		if _, err := fmt.Sscan(value, &number); err == nil {
			if number > 0 && number <= 1 && strings.Contains(value, ".") {
				number *= 100
			}
			return fmt.Sprintf("%g%%", number)
		}
	}
	return value
}

func (call *exchange) hasHook(name string) bool { return call.target.HasTaskHook(name) }

// callHook runs call, a call of hook into the plugin, with the time a hook
// has; the driver hooks that may fetch reach the provider's hosts.
func callHook[T any](ctx context.Context, exchange *exchange, hook string, call func(context.Context, *plugin.NewAPIPlugin) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	options := plugin.CallOptions{}
	if slices.Contains(fetchHooks, hook) {
		options.Fetch = &plugin.Fetch{
			Allowed: func(target *url.URL) bool { return allowedHost(target, exchange.credentials.baseURL, exchange.meta) },
			Client:  exchange.engine.fetchClient(exchange.service),
		}
	}
	result, _, err := plugin.CallNewAPI(ctx, exchange.target, hook, options, call)
	if err != nil {
		var zero T
		return zero, hookFailure(hook, err)
	}
	return result, nil
}

// call runs a hook pluginkit has no typed call for, a renderer or a
// presenter, with args, and returns its result as JSON text.
func (call *exchange) call(ctx context.Context, hook string, args ...any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	raw, _, err := call.target.CallTask(ctx, hook, args, plugin.CallOptions{})
	if err != nil {
		return nil, hookFailure(hook, err)
	}
	return raw, nil
}

// usage runs a usage hook and adds the facts it returned.
func (call *exchange) usage(ctx context.Context, facts map[string]any, hook string, args ...any) error {
	values := make([]any, len(args))
	for index, arg := range args {
		values[index] = plugin.NewAPIValue(arg)
	}
	returned, err := callHook(ctx, call, hook, func(ctx context.Context, loaded *plugin.NewAPIPlugin) (map[string]any, error) {
		return loaded.CallUsage(ctx, hook, values...)
	})
	if err != nil {
		return err
	}
	addFacts(facts, returned)
	return nil
}

// fetchClient is the client utils.fetch sends through: the provider's
// proxy, no redirects followed.
func (engine *Engine) fetchClient(service contract.Service) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		headers := request.Header.Clone()
		target := *request.URL
		return engine.roundTrip(request.Context(), service, request.Method, &target, headers, nil)
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

// clientHeaders are the client's headers a driver reads: none that carry
// its credentials or the gateway's.
func clientHeaders(header http.Header) map[string]string {
	headers := pluginkit.ClientRequestHeaders(header)
	for name := range headers {
		if lower := strings.ToLower(name); strings.HasPrefix(lower, "x-astrlink-") {
			delete(headers, name)
		}
	}
	return headers
}

// Submit sends a submission to the first candidate that accepts it,
// moving on while one fails in a way another may not, and keeps the task.
func (engine *Engine) Submit(ctx context.Context, submission Submission, candidates []Candidate) (*Submitted, error) {
	target, meta, err := engine.plugin(submission.PluginKey)
	if err != nil {
		return nil, err
	}
	if !meta.Has("json-order@1") {
		submission.Intent.RequestBody = sortedJSON(submission.Intent.RequestBody)
	}
	origins, pinned, err := engine.originTasks(ctx, submission, meta)
	if err != nil {
		return nil, err
	}
	if pinned != "" {
		candidates = slices.DeleteFunc(slices.Clone(candidates), func(candidate Candidate) bool { return candidate.Service.ID != pinned })
		if len(candidates) == 0 {
			return nil, failure(http.StatusBadRequest, "origin_task_unavailable", "the provider the earlier task ran on cannot serve this request")
		}
	}
	if len(candidates) == 0 {
		return nil, failure(http.StatusServiceUnavailable, "provider_unavailable", "no enabled provider of the task plugin %s serves the model %s", submission.PluginKey, submission.Intent.Model)
	}
	var attempts []Attempt
	var last error
	for _, candidate := range candidates {
		started := engine.now()
		submitted, status, err := engine.submitOn(ctx, target, meta, submission, candidate, origins)
		attempts = append(attempts, Attempt{Service: candidate.Service.ID, Started: started, Ended: engine.now(), Status: status, Err: err})
		if err == nil {
			submitted.Attempts = attempts
			engine.notify()
			return submitted, nil
		}
		last = err
		var known *Error
		if ctx.Err() != nil || !errors.As(err, &known) || !known.Retryable {
			break
		}
	}
	var known *Error
	if errors.As(last, &known) {
		failed := *known
		failed.attempts = attempts
		return nil, &failed
	}
	return nil, last
}

// originTasks reads the earlier tasks a submission continues: the
// client's own, of the same plugin, on one provider, which the submission
// then runs on.
func (engine *Engine) originTasks(ctx context.Context, submission Submission, meta *plugin.NewAPIPlugin) ([]originTask, contract.ServiceID, error) {
	var origins []originTask
	var pinned contract.ServiceID
	for _, id := range submission.Intent.OriginTaskIDs {
		task, err := engine.store.GetPluginTask(ctx, id)
		if err != nil || task.Owner != submission.Owner || task.PluginKey != meta.Meta.Key {
			return nil, "", failure(http.StatusBadRequest, "origin_task_not_found", "the task %s is not one of yours", id)
		}
		if pinned != "" && task.ServiceID != pinned {
			return nil, "", failure(http.StatusBadRequest, "origin_task_conflict", "the earlier tasks ran on different providers")
		}
		pinned = task.ServiceID
		data := task.Data
		if !task.Retain {
			data = nil
		}
		origins = append(origins, originTask{TaskID: task.ID, UpstreamTaskID: upstreamTaskID(task), Action: task.Action, Status: task.Status, Data: jsonOrNull(data)})
	}
	return origins, pinned, nil
}

// submitOn tries a submission on one provider. The status is the vendor's,
// 0 when it did not answer.
func (engine *Engine) submitOn(ctx context.Context, target *plugin.Plugin, meta *plugin.NewAPIPlugin, submission Submission, candidate Candidate, origins []originTask) (*Submitted, int, error) {
	found, err := engine.credentialsFor(ctx, candidate.Service, meta)
	if err != nil {
		return nil, 0, err
	}
	call := &exchange{engine: engine, target: target, service: candidate.Service, meta: meta, credentials: found, body: submission.Body}
	intent := submission.Intent
	upstreamModel := candidate.UpstreamModel
	if upstreamModel == "" {
		upstreamModel = intent.Model
	}
	id := newTaskID(submission.Surface)
	driver := driverContext{
		RequestBody: jsonOrNull(intent.RequestBody), RequestHeaders: clientHeaders(submission.RequestHeaders), Action: intent.Action,
		Model: intent.Model, UpstreamModel: upstreamModel, BaseURL: found.baseURL, APIKey: found.apiKey, AuthHeader: found.authHeader,
		Auth: found.auth, AuthError: found.authError, Upstream: found.upstream, Files: submission.Body.references(), PublicTaskID: id,
		OriginTasks: origins, UserSetting: settingsOrEmpty(found.settings),
	}
	if len(origins) > 0 {
		driver.OriginTaskID = origins[0].UpstreamTaskID
	}
	facts := map[string]any{}
	if call.hasHook("extractUsage") {
		usage := driver
		usage.UsagePurpose = "facts"
		if err := call.usage(ctx, facts, "extractUsage", usage); err != nil {
			return nil, 0, err
		}
	}
	desc, err := call.buildRequest(ctx, "buildSubmitRequest", plugin.NewAPIObject(driver))
	if err != nil {
		return nil, 0, err
	}
	if desc.ResponseType == "sse" && !meta.SubmitsSSE() {
		return nil, 0, failure(http.StatusBadGateway, "plugin_error", "buildSubmitRequest asked for an SSE answer, which the plugin does not declare")
	}
	if desc.Action != "" {
		driver.Action = desc.Action
	}
	if _, text := desc.Body.(string); submission.Egress != nil && desc.BodyType != "multipart" && desc.hasBody() && !text {
		body, err := desc.jsonBody()
		if err != nil {
			return nil, 0, failure(http.StatusBadGateway, "plugin_error", "buildSubmitRequest returned a body that is not JSON")
		}
		if err := submission.Egress(ctx, body); err != nil {
			return nil, 0, err
		}
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	response, err := call.send(streamCtx, "buildSubmitRequest", desc, nil)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		body, _, _ := readAnswer(response, maxErrorAnswer)
		return nil, response.StatusCode, upstreamRefusal(response, body)
	}
	var body json.RawMessage
	if desc.ResponseType == "sse" {
		body, err = call.streamSubmission(streamCtx, cancel, response, driver)
	} else {
		body, _, err = readAnswer(response, maxSubmitAnswer)
	}
	if err != nil {
		// The vendor accepted the submission: starting it again elsewhere
		// could pay for the work twice.
		return nil, response.StatusCode, notRetryable(err)
	}
	answer, err := callHook(ctx, call, "parseSubmitResponse", func(ctx context.Context, loaded *plugin.NewAPIPlugin) (pluginkit.SubmitResult, error) {
		return loaded.CallParseSubmitResponse(ctx, plugin.NewAPIObject(driver), pluginkit.SubmitResponse{StatusCode: response.StatusCode, Headers: allHeaders(response.Header), Body: pluginkit.RawJSON(body)})
	})
	if err != nil {
		return nil, response.StatusCode, notRetryable(err)
	}
	upstreamID := strings.TrimSpace(answer.TaskID)
	data := body
	if answer.TaskData != nil {
		if data, err = marshalOrdered(answer.TaskData); err != nil {
			return nil, response.StatusCode, notRetryable(failure(http.StatusBadGateway, "plugin_error", "parseSubmitResponse returned taskData that is not JSON"))
		}
	}
	if call.hasHook("extractUsageOnSubmit") {
		if err := call.usage(ctx, facts, "extractUsageOnSubmit", driver, data); err != nil {
			engine.logf("task plugin %s: extractUsageOnSubmit: %v", meta.Meta.Key, err)
		}
	}
	now := engine.now()
	task := storage.PluginTask{
		ID: id, PluginKey: meta.Meta.Key, PluginVersion: meta.Meta.Version, ServiceID: candidate.Service.ID, Owner: submission.Owner,
		Surface: submission.Surface, Operation: submission.Operation, Action: driver.Action, Model: intent.Model, UpstreamModel: upstreamModel,
		UpstreamTaskID: upstreamID, Status: contract.PluginTaskSubmitted, Retain: true, State: nullable(answer.State),
		Request: submission.Request, RequestID: submission.RequestID, CreatedAt: now, UpdatedAt: now,
	}
	if len(task.Request) == 0 && len(intent.RequestBody) > 0 {
		task.Request = intent.RequestBody
	}
	full := data
	if answer.Immediate != nil {
		result := *answer.Immediate
		if !slices.Contains(statuses, result.Status) {
			return nil, response.StatusCode, failure(http.StatusBadGateway, "plugin_error", "parseSubmitResponse returned the status %q", result.Status)
		}
		engine.applyResult(&task, result, data)
		if task.Status == contract.PluginTaskSuccess && call.hasHook("extractUsageOnComplete") {
			_ = call.usage(ctx, facts, "extractUsageOnComplete", found.query(task), resultValue(result), data)
		}
		if task.Terminal() && !submission.Retain {
			// The route answers with the whole result now and keeps none.
			task.Retain, task.Data = false, nil
		}
	} else {
		task.Data = engine.compactData(id, data)
	}
	if len(facts) > 0 {
		task.Usage, _ = json.Marshal(facts)
	}
	if !task.Terminal() {
		task.NextPollAt = now.Add(3 * time.Second)
	}
	if err := engine.store.CreatePluginTask(ctx, task); err != nil {
		return nil, response.StatusCode, notRetryable(fmt.Errorf("keep the task: %w", err))
	}
	return &Submitted{Task: task, Service: candidate.Service, Data: full}, response.StatusCode, nil
}

func nullable(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	return trimmed
}

// notRetryable marks err as one no other provider may take over.
func notRetryable(err error) error {
	var known *Error
	if errors.As(err, &known) {
		copied := *known
		copied.Retryable = false
		return &copied
	}
	return &Error{Status: http.StatusBadGateway, Code: "upstream_error", Message: err.Error(), err: err}
}

// upstreamRefusal is a submission the vendor refused: its status and the
// message it gave. A refused key reads as the gateway's failure, not the
// client's.
func upstreamRefusal(response *http.Response, body json.RawMessage) *Error {
	status := response.StatusCode
	message := upstreamMessage(body)
	if message == "" {
		message = http.StatusText(status)
	}
	refusal := &Error{Status: status, Code: "upstream_error", Message: message, Upstream: &UpstreamAnswer{Status: status, Header: response.Header.Clone(), Body: body}}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		refusal.Status, refusal.Code = http.StatusBadGateway, "upstream_auth_failed"
		refusal.Message = "the provider refused its key: " + message
		refusal.Retryable = true
	case status == http.StatusTooManyRequests:
		refusal.Retryable = true
	case status >= 500:
		refusal.Status, refusal.Retryable = http.StatusBadGateway, true
	case status < 400:
		refusal.Status = http.StatusBadGateway
	}
	return refusal
}

// upstreamMessage finds the message of a vendor's error answer.
func upstreamMessage(body json.RawMessage) string {
	var answer struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Msg     string          `json:"msg"`
		Detail  string          `json:"detail"`
	}
	if json.Unmarshal(body, &answer) == nil {
		var nested struct {
			Message string `json:"message"`
		}
		switch {
		case json.Unmarshal(answer.Error, &nested) == nil && nested.Message != "":
			return clip(nested.Message)
		case text(answer.Error) != "":
			return clip(text(answer.Error))
		case answer.Message != "":
			return clip(answer.Message)
		case answer.Msg != "":
			return clip(answer.Msg)
		case answer.Detail != "":
			return clip(answer.Detail)
		}
	}
	var plain string
	if json.Unmarshal(body, &plain) == nil {
		return clip(plain)
	}
	return clip(string(body))
}

func clip(message string) string {
	message = strings.TrimSpace(strings.ToValidUTF8(message, ""))
	if runes := []rune(message); len(runes) > 512 {
		message = string(runes[:512]) + "…"
	}
	return message
}

// mergeFacts adds the usage facts a task kept: a plain object of strings,
// numbers and booleans.
func mergeFacts(facts map[string]any, raw json.RawMessage) {
	var returned map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&returned) != nil {
		return
	}
	addFacts(facts, returned)
}

// addFacts adds the usage facts a hook returned: its strings, numbers and
// booleans.
func addFacts(facts map[string]any, returned map[string]any) {
	for name, value := range returned {
		switch value.(type) {
		case string, json.Number, bool, float64, int64, int:
			if len(name) <= 64 && len(facts) < 64 {
				facts[name] = value
			}
		}
	}
}

// applyResult records what a parse hook says of a task.
func (engine *Engine) applyResult(task *storage.PluginTask, result taskResult, data json.RawMessage) {
	now := engine.now()
	task.Status = result.Status
	if progress := progressText(result.Progress); progress != "" {
		task.Progress = progress
	}
	if result.Reason != "" || task.Status != contract.PluginTaskFailure {
		task.FailReason = clip(result.Reason)
	}
	if task.Status == contract.PluginTaskFailure && task.FailReason == "" {
		task.FailReason = "the task failed"
	}
	if task.Status == contract.PluginTaskSuccess {
		task.Progress = "100%"
		task.FailReason = ""
	}
	task.Data = engine.compactData(task.ID, data)
	if url := result.URL; url != "" || result.RemoteURL != "" {
		if url == "" {
			url = result.RemoteURL
		}
		task.ResultURL = engine.keepResult(task.ID, url)
	}
	if task.Terminal() {
		task.FinishedAt, task.NextPollAt = now, time.Time{}
	}
	task.UpdatedAt = now
}

// resultValue is a task result as the usage hooks read it: what the parse
// hook returned, its state as JSON.
func resultValue(result taskResult) map[string]any {
	value := plugin.NewAPIObject(result)
	if value == nil {
		return nil
	}
	if result.State != nil {
		value["state"] = pluginkit.RawJSON(result.State)
	}
	return value
}
