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

// submitAnswer is what parseSubmitResponse returns.
type submitAnswer struct {
	TaskID    json.RawMessage `json:"taskId"`
	TaskData  json.RawMessage `json:"taskData"`
	Immediate *taskResult     `json:"immediate"`
	State     json.RawMessage `json:"state"`
}

// taskResult is the NormalizedTaskResult the parse hooks return.
type taskResult struct {
	TaskID           json.RawMessage `json:"taskId"`
	Status           string          `json:"status"`
	Progress         json.RawMessage `json:"progress"`
	Reason           string          `json:"reason"`
	URL              string          `json:"url"`
	RemoteURL        string          `json:"remoteUrl"`
	CompletionTokens json.Number     `json:"completionTokens"`
	TotalTokens      json.Number     `json:"totalTokens"`
	// Data and State come with an item of parseBatchResult.
	Data  json.RawMessage `json:"data"`
	State json.RawMessage `json:"state"`
}

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
func progressText(raw json.RawMessage) string {
	value := text(raw)
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

// call runs a hook of the plugin with args; the driver hooks that may
// fetch reach the provider's hosts.
func (call *exchange) call(ctx context.Context, hook string, args ...any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	options := plugin.CallOptions{}
	if slices.Contains(fetchHooks, hook) {
		options.TaskFetch = &plugin.TaskFetch{
			Allowed: func(target *url.URL) bool { return allowedHost(target, call.credentials.baseURL, call.meta) },
			Client:  call.engine.fetchClient(call.service),
		}
	}
	raw, _, err := call.target.CallTask(ctx, hook, args, options)
	if err != nil {
		return nil, hookFailure(hook, err)
	}
	return raw, nil
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
	headers := map[string]string{}
	for name, values := range header {
		canonical := http.CanonicalHeaderKey(name)
		lower := strings.ToLower(canonical)
		switch {
		case len(values) == 0,
			lower == "authorization", lower == "cookie", lower == "x-api-key", lower == "x-goog-api-key",
			lower == "proxy-authorization", lower == "host", lower == "forwarded", lower == "via",
			strings.HasPrefix(lower, "x-astrlink-"), strings.HasPrefix(lower, "x-forwarded-"), strings.HasPrefix(lower, "sec-"):
			continue
		}
		headers[canonical] = values[0]
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
func (engine *Engine) originTasks(ctx context.Context, submission Submission, meta *plugin.TaskMeta) ([]originTask, contract.ServiceID, error) {
	var origins []originTask
	var pinned contract.ServiceID
	for _, id := range submission.Intent.OriginTaskIDs {
		task, err := engine.store.GetPluginTask(ctx, id)
		if err != nil || task.Owner != submission.Owner || task.PluginKey != meta.Key {
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
func (engine *Engine) submitOn(ctx context.Context, target *plugin.Plugin, meta *plugin.TaskMeta, submission Submission, candidate Candidate, origins []originTask) (*Submitted, int, error) {
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
		raw, err := call.call(ctx, "extractUsage", usage)
		if err != nil {
			return nil, 0, err
		}
		mergeFacts(facts, raw)
	}
	raw, err := call.call(ctx, "buildSubmitRequest", driver)
	if err != nil {
		return nil, 0, err
	}
	desc, err := parseDescriptor("buildSubmitRequest", raw)
	if err != nil {
		return nil, 0, err
	}
	if desc.ResponseType == "sse" && !meta.SubmitsSSE() {
		return nil, 0, failure(http.StatusBadGateway, "plugin_error", "buildSubmitRequest asked for an SSE answer, which the plugin does not declare")
	}
	if desc.Action != "" {
		driver.Action = desc.Action
	}
	if submission.Egress != nil && desc.BodyType != "multipart" && json.Valid(desc.Body) {
		if err := submission.Egress(ctx, desc.Body); err != nil {
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
	answerRaw, err := call.call(ctx, "parseSubmitResponse", driver, upstreamResponse{StatusCode: response.StatusCode, Headers: allHeaders(response.Header), Body: body})
	if err != nil {
		return nil, response.StatusCode, notRetryable(err)
	}
	var answer submitAnswer
	if err := strictJSON(answerRaw, &answer); err != nil {
		return nil, response.StatusCode, failure(http.StatusBadGateway, "plugin_error", "parseSubmitResponse returned something other than {taskId, taskData, immediate, state}")
	}
	upstreamID := text(answer.TaskID)
	if upstreamID == "" && answer.Immediate == nil {
		return nil, response.StatusCode, failure(http.StatusBadGateway, "plugin_error", "parseSubmitResponse returned no taskId")
	}
	data := answer.TaskData
	if len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		data = body
	}
	if call.hasHook("extractUsageOnSubmit") {
		if raw, err := call.call(ctx, "extractUsageOnSubmit", driver, data); err == nil {
			mergeFacts(facts, raw)
		} else {
			engine.logf("task plugin %s: extractUsageOnSubmit: %v", meta.Key, err)
		}
	}
	now := engine.now()
	task := storage.PluginTask{
		ID: id, PluginKey: meta.Key, PluginVersion: meta.Version, ServiceID: candidate.Service.ID, Owner: submission.Owner,
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
			if raw, err := call.call(ctx, "extractUsageOnComplete", found.query(task), result, data); err == nil {
				mergeFacts(facts, raw)
			}
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

// mergeFacts adds the usage facts a hook returned: a plain object of
// strings, numbers and booleans.
func mergeFacts(facts map[string]any, raw json.RawMessage) {
	var returned map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&returned) != nil {
		return
	}
	for name, value := range returned {
		switch value.(type) {
		case string, json.Number, bool:
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
