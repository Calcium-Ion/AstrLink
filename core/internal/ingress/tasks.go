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
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/taskplugin"
)

// The inference port serves the tasks of task plugins, written for
// new-api's task plugin API: OpenAI's video, image and Responses APIs for
// the models a task plugin claims, the plugins' own native routes, and
// /v1/tasks. A submission is recorded as any request; reading a task back
// is not.

// Bounds of a task request.
const (
	// maxTaskBody bounds a submission, uploads included.
	maxTaskBody = 160 << 20
	// taskProtocolTimeout bounds how long a request waits on its task:
	// an image, or a Responses request that does not stream.
	taskProtocolTimeout = 600 * time.Second
	// awaitInterval is how often a waiting request asks the vendor.
	awaitInterval = 3 * time.Second
)

// taskProtocols are the gateway protocols of task plugins, by the
// protocol IDs routing knows them by.
var taskProtocols = map[string]contract.ProtocolID{
	plugin.TaskProtocolVideo:     contract.ProtocolOpenAIVideos,
	plugin.TaskProtocolImage:     contract.ProtocolOpenAIImages,
	plugin.TaskProtocolResponses: contract.ProtocolOpenAIResponses,
}

// ownerOf is the access token a task belongs to, "" without tokens.
func ownerOf(request *http.Request) string {
	id, _ := AccessTokenIDFromContext(request.Context())
	return string(id)
}

// serveTaskArtifact serves an artifact link the Responses protocol handed
// out, which opens without an access token: the link is the capability.
func (handler *Handler) serveTaskArtifact(writer http.ResponseWriter, request *http.Request) bool {
	if handler.tasks == nil || !request.URL.Query().Has("access") {
		return false
	}
	segments := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(segments) != 6 || segments[0] != "v1" || segments[1] != "tasks" || segments[3] != "artifacts" || segments[5] != "content" {
		return false
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		writeTaskError(writer, &taskplugin.Error{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Message: "use GET or HEAD"})
		return true
	}
	id, key := segments[2], segments[4]
	if !handler.tasks.VerifyAccess(id, key, request.URL.Query().Get("access")) {
		writeTaskError(writer, &taskplugin.Error{Status: http.StatusNotFound, Code: "artifact_not_found", Message: "no such artifact"})
		return true
	}
	task, err := handler.taskByID(request.Context(), id)
	if err == nil {
		err = handler.tasks.ServeContent(request.Context(), writer, request, task, key)
	}
	if err != nil {
		writeTaskError(writer, err)
	}
	return true
}

// taskByID reads a task whatever its owner: its artifact link was checked.
func (handler *Handler) taskByID(ctx context.Context, id string) (storage.PluginTask, error) {
	task, err := handler.tasks.Store().GetPluginTask(ctx, id)
	if err != nil || !task.Retain {
		return storage.PluginTask{}, &taskplugin.Error{Status: http.StatusNotFound, Code: "task_not_found", Message: "no task " + id}
	}
	return task, nil
}

// serveTasks serves the task routes of the inference port, after the
// access token was accepted. It reports false for a request that is not a
// task's, whose body it left as it was.
func (handler *Handler) serveTasks(writer http.ResponseWriter, request *http.Request) bool {
	if handler.tasks == nil {
		return false
	}
	path := request.URL.Path
	// A plugin's native route comes first, unless the gateway answers the
	// vendor's path itself; the route is then served under /{key} alone.
	if route, params, ok := handler.tasks.MatchRoute(request.Method, path, gatewayAnswers); ok {
		handler.serveNativeRoute(writer, request, route, params)
		return true
	}
	switch {
	case path == "/v1/videos":
		if request.Method != http.MethodPost {
			return false
		}
		handler.submitTaskProtocol(writer, request, plugin.TaskProtocolVideo, "create", "")
		return true
	case strings.HasPrefix(path, "/v1/videos/"):
		handler.serveVideo(writer, request, strings.Split(strings.TrimPrefix(path, "/v1/videos/"), "/"))
		return true
	case path == "/v1/images/generations" || path == "/v1/images/edits":
		if request.Method != http.MethodPost || !handler.tasks.Claims(plugin.TaskProtocolImage) {
			return false
		}
		operation := "generate"
		if path == "/v1/images/edits" {
			operation = "edit"
		}
		return handler.peekTaskModel(writer, request, plugin.TaskProtocolImage, operation)
	case path == "/v1/responses":
		if request.Method != http.MethodPost || !handler.tasks.Claims(plugin.TaskProtocolResponses) {
			return false
		}
		return handler.peekTaskModel(writer, request, plugin.TaskProtocolResponses, "create")
	case strings.HasPrefix(path, "/v1/responses/"):
		id := strings.TrimPrefix(path, "/v1/responses/")
		if request.Method != http.MethodGet || !taskplugin.ValidTaskID(id) {
			return false
		}
		handler.retrieveResponse(writer, request, id)
		return true
	case strings.HasPrefix(path, "/v1/tasks/"):
		handler.serveTaskAPI(writer, request, strings.Split(strings.TrimPrefix(path, "/v1/tasks/"), "/"))
		return true
	}
	return false
}

// taskSurfaceNative is the surface of a task a native route or /v1/tasks
// took.
const taskSurfaceNative = "native"

// gatewayAnswers reports whether a route of the gateway's own answers
// method and path, ahead of a task plugin's route on the vendor's path. A
// task id segment takes only a gateway task id, as new-api's do, so
// GET /v1/videos/text2video/:id stays a plugin's beside the gateway's
// GET /v1/videos/:id/content.
func gatewayAnswers(method, path string) bool {
	if _, _, ok := matchProtocolRoute(path); ok {
		return true
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) < 2 || segments[0] != "v1" {
		return false
	}
	switch {
	case segments[1] == "videos" && len(segments) == 2:
		return method == http.MethodPost
	case segments[1] == "videos" && len(segments) >= 3 && taskplugin.ValidTaskID(segments[2]):
		switch {
		case len(segments) == 3:
			return method == http.MethodGet
		case len(segments) == 4 && segments[3] == "content":
			return method == http.MethodGet || method == http.MethodHead
		case len(segments) == 4 && segments[3] == "remix":
			return method == http.MethodPost
		}
	case segments[1] == "responses" && len(segments) == 3:
		return method == http.MethodGet && taskplugin.ValidTaskID(segments[2])
	}
	return false
}

// readTaskBody reads a task request's body up to maxTaskBody, and leaves
// a copy in its place.
func readTaskBody(request *http.Request) ([]byte, error) {
	if request.Body == nil || request.Body == http.NoBody {
		return nil, nil
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, maxTaskBody+1))
	if err != nil {
		return nil, &taskplugin.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "the request body could not be read"}
	}
	if len(raw) > maxTaskBody {
		return nil, &taskplugin.Error{Status: http.StatusRequestEntityTooLarge, Code: "request_too_large", Message: fmt.Sprintf("the request body is larger than %d MiB", maxTaskBody>>20)}
	}
	request.Body = io.NopCloser(bytes.NewReader(raw))
	return raw, nil
}

// peekTaskModel reads the model an image or Responses request names, and
// serves it as a task when a task plugin claims that model; any other
// request goes on with its body as it was.
func (handler *Handler) peekTaskModel(writer http.ResponseWriter, request *http.Request, protocol, operation string) bool {
	raw, err := readTaskBody(request)
	if err != nil {
		writeTaskError(writer, err)
		return true
	}
	peek := request.Clone(request.Context())
	peek.Body = io.NopCloser(bytes.NewReader(raw))
	body, err := taskplugin.ReadBody(peek, maxTaskBody)
	request.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	model, ok := body.TopLevelModel()
	if !ok || len(handler.tasks.Claimants(protocol, model)) == 0 {
		return false
	}
	handler.submitTaskProtocol(writer, request, protocol, operation, model)
	return true
}

// taskRequest is a submission as its handlers share it: the decoded body,
// the record, and where its answer goes.
type taskRequest struct {
	handler  *Handler
	request  *http.Request
	writer   http.ResponseWriter
	session  *recordSession
	body     *taskplugin.Body
	protocol contract.ProtocolID
	// finished hears a task that succeeded, before the answer ends.
	finished func(storage.PluginTask)
}

// startTaskRecord starts the request record of a submission and reads its
// body through the record's capture.
func (handler *Handler) startTaskRecord(writer http.ResponseWriter, request *http.Request, protocol contract.ProtocolID, model string, stream bool) (*taskRequest, func()) {
	classified := Request{Protocol: protocol, Model: model, Streaming: stream}
	session := handler.startRecordSession(request, classified)
	session.persistPending(request.Context(), handler.requestRecords, handler.recordLogger)
	request = request.WithContext(withRecordSession(request.Context(), session))
	current := &taskRequest{handler: handler, request: request, writer: session.wrap(writer), session: session, protocol: protocol}
	finish := func() {
		if request.Context().Err() != nil {
			session.noteCancelled()
		}
		session.notePlugins(session.requestPlugins.Records(), nil)
		session.finish(context.Background(), handler.requestRecords, handler.auditBlobs, handler.recordLogger)
		session.reportRequestPlugins()
	}
	return current, finish
}

// ingressPlugins runs the request processing instances' ingress over a
// submission's JSON body, before the task plugin decodes it: the changed
// body is what the plugin decodes and sends. A form body goes unchecked.
// It reports true when an instance stopped the submission, which then has
// its answer. A task submission is not under the privacy policy, so the
// plugins read the body the vendor gets.
func (current *taskRequest) ingressPlugins(model string) bool {
	run := current.handler.plugins.RequestPlugins(model, current.request.URL.Path)
	if run == nil || current.body == nil {
		return false
	}
	current.session.requestPlugins = run
	if current.body.Kind != taskplugin.BodyJSON {
		fields, refusal := run.IngressForm(current.request.Context(), current.protocol, current.body.FieldNames(), current.body.Fields)
		if refusal != nil {
			writeInferenceError(current.writer, refusal.Status, refusal.Code, refusal.Message, false, nil)
			current.session.noteFailed(errorSummaryFromInference(refusal.Code, refusal.Message, false))
			return true
		}
		current.body.Fields = fields
		return false
	}
	body, refusal := run.Ingress(current.request.Context(), current.protocol, current.body.JSON, nil)
	if refusal != nil {
		writeInferenceError(current.writer, refusal.Status, refusal.Code, refusal.Message, false, nil)
		current.session.noteFailed(errorSummaryFromInference(refusal.Code, refusal.Message, false))
		return true
	}
	current.body.JSON = body
	return false
}

// egressPlugins is the check of the body a task plugin built for the
// vendor, before it is sent: nil without request processing.
func (current *taskRequest) egressPlugins() func(ctx context.Context, body json.RawMessage) error {
	run := current.session.requestPluginRun()
	if !run.Serves(contract.RequestStageEgress) {
		return nil
	}
	return func(ctx context.Context, body json.RawMessage) error {
		if refusal := run.Egress(ctx, contract.ProtocolPluginNative, body); refusal != nil {
			return &taskplugin.Error{Status: refusal.Status, Code: refusal.Code, Message: refusal.Message}
		}
		return nil
	}
}

// fail answers the submission with err and records it.
func (current *taskRequest) fail(err error) {
	var known *taskplugin.Error
	if !errors.As(err, &known) {
		known = &taskplugin.Error{Status: http.StatusBadGateway, Code: "task_failed", Message: err.Error()}
	}
	writeTaskError(current.writer, known)
	current.session.noteFailed(errorSummaryFromInference(known.Code, known.Message, known.Retryable))
}

// writeTaskError answers with an OpenAI error envelope.
func writeTaskError(writer http.ResponseWriter, err error) {
	var known *taskplugin.Error
	if !errors.As(err, &known) {
		known = &taskplugin.Error{Status: http.StatusBadGateway, Code: "task_failed", Message: err.Error()}
	}
	status := known.Status
	if status < 400 || status > 599 {
		status = http.StatusBadGateway
	}
	writeInferenceError(writer, status, known.Code, known.Message, known.Retryable, nil)
}

// taskCandidates are the providers a submission may run on: those routing
// chooses for the model, bound to a task plugin that serves it on surface,
// optionally only the plugins pluginKeys names.
func (current *taskRequest) taskCandidates(protocol contract.ProtocolID, model string, stream bool, serves func(*plugin.TaskMeta) bool) ([]endpoint.Resolved, error) {
	pinned, ok := requestedService(current.request)
	if !ok {
		return nil, &taskplugin.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: ServiceHeader + " must hold one API provider ID"}
	}
	resolved, err := current.handler.resolveCandidates(current.request.Context(), endpoint.ResolveRequest{
		Protocol: protocol, Model: model, Streaming: stream, AllCandidates: true, Service: pinned,
	})
	if err != nil && !errors.Is(err, endpoint.ErrNoEndpoint) {
		return nil, &taskplugin.Error{Status: http.StatusServiceUnavailable, Code: "provider_unavailable", Message: "no enabled provider serves the model " + model}
	}
	var candidates []endpoint.Resolved
	for _, candidate := range resolved {
		service := candidate.CanonicalService()
		if service.Kind != contract.ServiceKindPlugin || service.Plugin == nil || service.Plugin.Provider != contract.PluginProviderTask {
			continue
		}
		meta := current.handler.tasks.TaskPlugin(service.Plugin.Key)
		if meta == nil || !serves(meta) {
			current.session.noteRoutingSkip(service.ID, contract.RoutingSkipProtocolUnsupported)
			continue
		}
		candidates = append(candidates, candidate)
	}
	if len(candidates) == 0 {
		return nil, &taskplugin.Error{Status: http.StatusServiceUnavailable, Code: "provider_unavailable", Message: "no enabled provider of a task plugin serves the model " + model}
	}
	return candidates, nil
}

// fixedCandidates keeps the given providers whose task plugin serves the
// request, whether routing would choose them or not.
func (current *taskRequest) fixedCandidates(fixed []endpoint.Resolved, model string, serves func(*plugin.TaskMeta) bool) ([]endpoint.Resolved, error) {
	var candidates []endpoint.Resolved
	for _, candidate := range fixed {
		service := candidate.CanonicalService()
		if service.Plugin == nil {
			continue
		}
		if meta := current.handler.tasks.TaskPlugin(service.Plugin.Key); meta != nil && serves(meta) {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		return nil, &taskplugin.Error{Status: http.StatusBadRequest, Code: "unsupported_model", Message: "the provider's task plugin does not serve the model " + model + " this way"}
	}
	return candidates, nil
}

// submit decodes and submits on the candidates in order: each plugin
// decodes the request once, and a candidate whose plugin cannot gives way
// to the next.
func (current *taskRequest) submit(candidates []endpoint.Resolved, surface, operation string, decode func(pluginKey string) (taskplugin.Intent, error), retain bool, echo json.RawMessage) (*taskplugin.Submitted, error) {
	intents := map[string]taskplugin.Intent{}
	failures := map[string]error{}
	var last error
	for _, candidate := range candidates {
		service := candidate.CanonicalService()
		key := service.Plugin.Key
		intent, ok := intents[key]
		if !ok {
			if failed, seen := failures[key]; seen {
				last = failed
				continue
			}
			decoded, err := decode(key)
			if err != nil {
				failures[key], last = err, err
				continue
			}
			intents[key], intent = decoded, decoded
		}
		current.session.noteRoutingTried(service.ID)
		submitted, err := current.handler.tasks.Submit(current.request.Context(), taskplugin.Submission{
			PluginKey: key, Surface: surface, Operation: operation, Intent: intent, Body: current.body,
			RequestHeaders: current.request.Header, Owner: ownerOf(current.request), Retain: retain, Request: echo,
			RequestID: string(current.session.requestID()), Egress: current.egressPlugins(),
		}, []taskplugin.Candidate{{Service: service, UpstreamModel: candidate.UpstreamModel}})
		if err == nil {
			// The record holds a plan as the planner makes one: a native plan
			// converts nothing, and one without its path fails validation, so
			// the record would stay waiting for a provider.
			current.session.noteServed(candidate, contract.ExecutionPlan{
				Type: contract.PlanTypeNative, ServiceID: service.ID, InputProtocol: current.protocol, UpstreamProtocol: current.protocol,
				ConversionPath: []contract.ConversionEdge{}, Streaming: current.session.classified.Streaming,
			})
			return submitted, nil
		}
		last = err
		var known *taskplugin.Error
		if current.request.Context().Err() != nil || !errors.As(err, &known) || !known.Retryable {
			current.session.noteAttemptedService(service.ID)
			break
		}
	}
	return nil, last
}

// submitTaskProtocol serves a submission on a gateway protocol: a video,
// an image or a Responses request for a model a task plugin claims.
func (handler *Handler) submitTaskProtocol(writer http.ResponseWriter, request *http.Request, protocol, operation, model string) {
	handler.runTaskProtocol(writer, request, protocol, operation, model, nil, nil)
}

// runTaskProtocol serves a submission on a gateway protocol on the
// providers routing chooses, or on fixed ones, as a provider's test asks;
// finished, when set, hears a Responses task that succeeded before its
// answer ends.
func (handler *Handler) runTaskProtocol(writer http.ResponseWriter, request *http.Request, protocol, operation, model string, fixed []endpoint.Resolved, finished func(storage.PluginTask)) {
	if _, err := readTaskBody(request); err != nil {
		writeTaskError(writer, err)
		return
	}
	peek := request.Clone(request.Context())
	raw, _ := io.ReadAll(request.Body)
	peek.Body = io.NopCloser(bytes.NewReader(raw))
	probe, err := taskplugin.ReadBody(peek, maxTaskBody)
	request.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		writeTaskError(writer, err)
		return
	}
	if model == "" {
		model, _ = probe.TopLevelModel()
	}
	stream := protocol == plugin.TaskProtocolResponses && probe.Stream()
	background := protocol == plugin.TaskProtocolResponses && probe.Background()
	current, finish := handler.startTaskRecord(writer, request, taskProtocols[protocol], model, stream)
	defer finish()
	current.finished = finished
	request = current.request
	if current.body, err = taskplugin.ReadBody(request, maxTaskBody); err != nil {
		current.fail(err)
		return
	}
	if model == "" {
		current.fail(&taskplugin.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "the request names no model"})
		return
	}
	if current.ingressPlugins(model) {
		return
	}
	mode := ""
	if protocol == plugin.TaskProtocolResponses {
		switch {
		case stream && background:
			current.fail(&taskplugin.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "stream and background cannot both be set"})
			return
		case stream:
			mode = plugin.TaskModeStream
		case background:
			mode = plugin.TaskModeBackground
		default:
			mode = plugin.TaskModeSync
		}
	}
	serves := func(meta *plugin.TaskMeta) bool {
		if !meta.ServesProtocol(protocol, model) {
			return false
		}
		return mode == "" || slices.Contains(meta.Claim(protocol).Supports, mode)
	}
	var candidates []endpoint.Resolved
	if fixed != nil {
		candidates, err = current.fixedCandidates(fixed, model, serves)
	} else {
		candidates, err = current.taskCandidates(taskProtocols[protocol], model, false, serves)
	}
	if err != nil {
		if mode != "" {
			if forms := handler.tasks.ResponseModes(model); len(forms) > 0 && !slices.Contains(forms, mode) {
				err = &taskplugin.Error{Status: http.StatusBadRequest, Code: "unsupported_mode", Message: fmt.Sprintf("the model %s takes %s requests", model, strings.Join(forms, ", "))}
			}
		}
		current.fail(err)
		return
	}
	decodeRequest := taskplugin.Request{
		Method: request.Method, Path: request.URL.Path, Query: request.URL.Query(), Headers: request.Header, Body: current.body,
		Protocol: protocol, Operation: operation, Model: model, Stream: stream,
	}
	decode := func(pluginKey string) (taskplugin.Intent, error) {
		return handler.tasks.Decode(request.Context(), pluginKey, "protocols."+protocol+".decodeRequest", decodeRequest)
	}
	// An image answers inline, so a result the vendor gave at once is not
	// kept; a video and a response are read back later.
	retain := protocol != plugin.TaskProtocolImage
	submitted, err := current.submit(candidates, protocol, operation, decode, retain, nil)
	if err != nil {
		current.fail(err)
		return
	}
	switch protocol {
	case plugin.TaskProtocolVideo:
		current.answerVideo(submitted.Task, decodeRequest)
	case plugin.TaskProtocolImage:
		current.answerImages(submitted, decodeRequest, probe)
	case plugin.TaskProtocolResponses:
		current.answerResponse(submitted.Task, decodeRequest, mode)
	}
}

// answerVideo answers a video's creation with the video object.
func (current *taskRequest) answerVideo(task storage.PluginTask, request taskplugin.Request) {
	rendered, err := current.handler.tasks.RenderVideo(current.request.Context(), task, request)
	if err != nil {
		current.fail(err)
		return
	}
	writeTaskJSON(current.writer, http.StatusOK, rendered)
	current.session.noteSucceeded()
}

// answerImages waits for an image task to end and answers with the
// images: a result the vendor gave at once without waiting.
func (current *taskRequest) answerImages(submitted *taskplugin.Submitted, request taskplugin.Request, probe *taskplugin.Body) {
	ctx := current.request.Context()
	task := submitted.Task
	full := submitted.Data
	if !task.Terminal() {
		waitCtx, cancel := context.WithTimeout(ctx, taskProtocolTimeout)
		defer cancel()
		var err error
		task, err = current.handler.tasks.Await(waitCtx, task.ID, awaitInterval, nil)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			current.fail(&taskplugin.Error{Status: http.StatusGatewayTimeout, Code: "task_timeout", Message: "the image is not ready after 600 seconds; it finishes in the background"})
			return
		}
		full = nil
	}
	if task.Status == contract.PluginTaskFailure {
		current.fail(&taskplugin.Error{Status: http.StatusBadRequest, Code: "task_failed", Message: task.FailReason})
		return
	}
	format := responseFormat(probe)
	rendered, err := current.handler.tasks.RenderImages(ctx, task, request, full, format)
	if err != nil {
		current.fail(err)
		return
	}
	writeTaskJSON(current.writer, http.StatusOK, rendered)
	current.session.noteSucceeded()
}

// responseFormat is the response_format an image request asks for.
func responseFormat(body *taskplugin.Body) string {
	if body == nil {
		return ""
	}
	switch body.Kind {
	case taskplugin.BodyJSON:
		var request struct {
			ResponseFormat string `json:"response_format"`
		}
		_ = json.Unmarshal(body.JSON, &request)
		return request.ResponseFormat
	case taskplugin.BodyMultipart, taskplugin.BodyForm:
		if values := body.Fields["response_format"]; len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

// publicBase is the address the client reached the gateway at, for the
// artifact links an answer carries.
func publicBase(request *http.Request) string {
	scheme := "http"
	if request.TLS != nil || strings.EqualFold(request.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := request.Host
	if forwarded := strings.TrimSpace(strings.Split(request.Header.Get("X-Forwarded-Host"), ",")[0]); forwarded != "" {
		host = forwarded
	}
	// Without a host, as in a request made inside the gateway, the links
	// stay relative.
	if host == "" {
		return ""
	}
	return (&url.URL{Scheme: scheme, Host: host}).String()
}

// artifactLinks makes the links of a task's artifacts under the address
// the client reached the gateway at.
func (handler *Handler) artifactLinks(request *http.Request, task storage.PluginTask) func(key string) string {
	base := publicBase(request)
	return func(key string) string { return handler.tasks.ArtifactURL(base, task.ID, key) }
}

// answerResponse answers a Responses request: at once with the pending
// response in the background, as a stream of events while the task runs,
// or with the finished response.
func (current *taskRequest) answerResponse(task storage.PluginTask, request taskplugin.Request, mode string) {
	ctx := current.request.Context()
	switch mode {
	case plugin.TaskModeBackground:
		writeTaskJSON(current.writer, http.StatusOK, taskplugin.ResponseObject(task, nil, nil))
		current.session.noteSucceeded()
		return
	case plugin.TaskModeStream:
		current.streamResponse(task, request)
		return
	}
	waitCtx, cancel := context.WithTimeout(ctx, taskProtocolTimeout)
	defer cancel()
	task, err := current.handler.tasks.Await(waitCtx, task.ID, awaitInterval, nil)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		current.fail(&taskplugin.Error{Status: http.StatusGatewayTimeout, Code: "task_timeout", Message: "the response is not ready after 600 seconds; retrieve it later"})
		return
	}
	rendered, err := current.handler.tasks.RenderResponse(ctx, task, request, current.handler.artifactLinks(current.request, task))
	if err != nil {
		current.fail(err)
		return
	}
	if task.Status == contract.PluginTaskSuccess && current.finished != nil {
		current.finished(task)
	}
	writeTaskJSON(current.writer, http.StatusOK, rendered)
	if task.Status == contract.PluginTaskFailure {
		current.session.noteFailed(errorSummaryFromInference("task_failed", task.FailReason, false))
		return
	}
	current.session.noteSucceeded()
}

// streamResponse streams a task as Responses events: the response created,
// its progress, its output as text, and the response completed or failed.
func (current *taskRequest) streamResponse(task storage.PluginTask, request taskplugin.Request) {
	ctx := current.request.Context()
	writer := current.writer
	header := writer.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache")
	header.Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	flusher, _ := writer.(http.Flusher)
	sequence := 0
	send := func(kind string, payload map[string]any) {
		payload["type"] = kind
		payload["sequence_number"] = sequence
		sequence++
		encoded, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", kind, encoded)
		if flusher != nil {
			flusher.Flush()
		}
	}
	response := func(current storage.PluginTask) json.RawMessage { return taskplugin.ResponseObject(current, nil, nil) }
	// A stream opens in progress, also on a task its vendor finished at
	// once: response.completed or response.failed ends it.
	opening := task
	if opening.Terminal() {
		opening.Status = contract.PluginTaskInProgress
	}
	send("response.created", map[string]any{"response": response(opening)})
	itemID := "msg_" + strings.TrimPrefix(task.ID, "resp_")
	var text strings.Builder
	opened := false
	open := func() {
		if opened {
			return
		}
		opened = true
		send("response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"type": "message", "id": itemID, "status": "in_progress", "role": "assistant", "content": []any{}}})
		send("response.content_part.added", map[string]any{"item_id": itemID, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
	}
	var state json.RawMessage
	links := current.handler.artifactLinks(current.request, task)
	waitCtx, cancel := context.WithTimeout(ctx, 24*time.Hour)
	defer cancel()
	var lastStatus, lastProgress string
	var failed *taskplugin.ResponseEvent
	done := false
	observe := func(observed storage.PluginTask) error {
		if observed.Status == lastStatus && observed.Progress == lastProgress && !observed.Terminal() {
			// Keep the connection alive while nothing changes.
			_, _ = io.WriteString(writer, ": waiting\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			return nil
		}
		lastStatus, lastProgress = observed.Status, observed.Progress
		events, next, finished, err := current.handler.tasks.RenderEvents(ctx, observed, request, state, links)
		if err != nil {
			return err
		}
		state = next
		for _, event := range events {
			switch event.Type {
			case "progress":
				send("response.in_progress", map[string]any{"response": response(observed)})
			case "output":
				open()
				delta := event.Text()
				text.WriteString(delta)
				send("response.output_text.delta", map[string]any{"item_id": itemID, "output_index": 0, "content_index": 0, "delta": delta})
			case "error":
				copied := event
				failed = &copied
			}
		}
		if finished || observed.Terminal() {
			done = true
			return errStreamDone
		}
		return nil
	}
	final, err := current.handler.tasks.Await(waitCtx, task.ID, awaitInterval, observe)
	if err != nil && !errors.Is(err, errStreamDone) {
		if ctx.Err() != nil {
			return
		}
		final.Status, final.FailReason = contract.PluginTaskFailure, err.Error()
		failed = &taskplugin.ResponseEvent{Type: "error", Code: "task_failed", Message: err.Error()}
	}
	_ = done
	if opened {
		send("response.output_text.done", map[string]any{"item_id": itemID, "output_index": 0, "content_index": 0, "text": text.String()})
		send("response.content_part.done", map[string]any{"item_id": itemID, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": text.String(), "annotations": []any{}}})
		send("response.output_item.done", map[string]any{"output_index": 0, "item": map[string]any{"type": "message", "id": itemID, "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text.String(), "annotations": []any{}}}}})
	}
	if failed != nil || final.Status == contract.PluginTaskFailure {
		message := final.FailReason
		code := "task_failed"
		if failed != nil {
			if failed.Message != "" {
				message = failed.Message
			}
			if failed.Code != "" {
				code = failed.Code
			}
		}
		final.Status, final.FailReason = contract.PluginTaskFailure, message
		object := taskplugin.ResponseObject(final, nil, nil)
		send("response.failed", map[string]any{"response": object})
		current.session.noteFailed(errorSummaryFromInference(code, message, false))
		return
	}
	if current.finished != nil {
		current.finished(final)
	}
	send("response.completed", map[string]any{"response": taskplugin.ResponseObject(final, taskplugin.OutputMessage(final.ID, text.String()), nil)})
	current.session.noteSucceeded()
}

var errStreamDone = errors.New("the stream is done")

func writeTaskJSON(writer http.ResponseWriter, status int, body json.RawMessage) {
	header := writer.Header()
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}

// serveVideo serves /v1/videos/:id, its content and its remix.
func (handler *Handler) serveVideo(writer http.ResponseWriter, request *http.Request, segments []string) {
	id := segments[0]
	switch {
	case len(segments) == 1:
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", "GET")
			writeTaskError(writer, &taskplugin.Error{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Message: "use GET"})
			return
		}
		task, err := handler.tasks.Task(request.Context(), id, ownerOf(request))
		// A task its plugin's native route took is answered as that
		// plugin's query route at this path answers, as xAI's
		// GET /v1/videos/:request_id does.
		if err == nil && task.Surface == taskSurfaceNative && !strings.HasPrefix(task.Operation, "POST /v1/tasks/") {
			if route, params, ok := handler.tasks.QueryRoute(task.PluginKey, request.Method, request.URL.Path); ok {
				handler.serveNativeRoute(writer, request, route, params)
				return
			}
		}
		if err != nil || task.Surface != plugin.TaskProtocolVideo {
			writeTaskError(writer, &taskplugin.Error{Status: http.StatusNotFound, Code: "video_not_found", Message: "no video " + id})
			return
		}
		rendered, err := handler.tasks.RenderVideo(request.Context(), task, taskplugin.Request{
			Method: request.Method, Path: request.URL.Path, Params: map[string]string{"task_id": id}, Query: request.URL.Query(),
			Headers: request.Header, Protocol: plugin.TaskProtocolVideo, Operation: "retrieve", Model: task.Model,
		})
		if err != nil {
			writeTaskError(writer, err)
			return
		}
		writeTaskJSON(writer, http.StatusOK, rendered)
	case len(segments) == 2 && segments[1] == "content":
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writer.Header().Set("Allow", "GET, HEAD")
			writeTaskError(writer, &taskplugin.Error{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Message: "use GET or HEAD"})
			return
		}
		task, err := handler.tasks.Task(request.Context(), id, ownerOf(request))
		if err != nil || task.Surface != plugin.TaskProtocolVideo {
			writeTaskError(writer, &taskplugin.Error{Status: http.StatusNotFound, Code: "video_not_found", Message: "no video " + id})
			return
		}
		key, err := handler.tasks.VideoArtifact(request.Context(), task, request.URL.Query().Get("variant"))
		if err == nil {
			err = handler.tasks.ServeContent(request.Context(), writer, request, task, key)
		}
		if err != nil {
			writeTaskError(writer, err)
		}
	case len(segments) == 2 && segments[1] == "remix" && request.Method == http.MethodPost:
		handler.remixVideo(writer, request, id)
	default:
		writeTaskError(writer, &taskplugin.Error{Status: http.StatusNotFound, Code: "not_found", Message: "no such video endpoint"})
	}
}

// remixVideo submits a remix of a finished video: the client's body, the
// earlier video its origin.
func (handler *Handler) remixVideo(writer http.ResponseWriter, request *http.Request, id string) {
	origin, err := handler.tasks.Task(request.Context(), id, ownerOf(request))
	if err != nil || origin.Surface != plugin.TaskProtocolVideo {
		writeTaskError(writer, &taskplugin.Error{Status: http.StatusNotFound, Code: "video_not_found", Message: "no video " + id})
		return
	}
	current, finish := handler.startTaskRecord(writer, request, contract.ProtocolOpenAIVideos, origin.Model, false)
	defer finish()
	request = current.request
	if current.body, err = taskplugin.ReadBody(request, maxTaskBody); err != nil {
		current.fail(err)
		return
	}
	if current.ingressPlugins(origin.Model) {
		return
	}
	if current.body.Kind != taskplugin.BodyJSON {
		current.fail(&taskplugin.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "a remix takes a JSON body"})
		return
	}
	candidates, err := current.taskCandidates(contract.ProtocolOpenAIVideos, origin.Model, false, func(meta *plugin.TaskMeta) bool { return meta.Key == origin.PluginKey })
	if err != nil {
		current.fail(err)
		return
	}
	decode := func(string) (taskplugin.Intent, error) {
		body := map[string]any{}
		_ = json.Unmarshal(current.body.JSON, &body)
		body["model"] = origin.Model
		encoded, _ := json.Marshal(body)
		return taskplugin.Intent{Kind: "submit", Model: origin.Model, Action: "remix", RequestBody: encoded, OriginTaskIDs: []string{origin.ID}}, nil
	}
	submitted, err := current.submit(candidates, plugin.TaskProtocolVideo, "remix", decode, true, nil)
	if err != nil {
		current.fail(err)
		return
	}
	current.answerVideo(submitted.Task, taskplugin.Request{Method: request.Method, Path: request.URL.Path, Params: map[string]string{"video_id": id}, Headers: request.Header, Body: current.body, Protocol: plugin.TaskProtocolVideo, Operation: "remix", Model: origin.Model})
}

// retrieveResponse serves GET /v1/responses/:id for a response a task
// plugin produced.
func (handler *Handler) retrieveResponse(writer http.ResponseWriter, request *http.Request, id string) {
	task, err := handler.tasks.Task(request.Context(), id, ownerOf(request))
	if err != nil || task.Surface != plugin.TaskProtocolResponses {
		writeTaskError(writer, &taskplugin.Error{Status: http.StatusNotFound, Code: "response_not_found", Message: "no response " + id})
		return
	}
	rendered, err := handler.tasks.RenderResponse(request.Context(), task, taskplugin.Request{
		Method: request.Method, Path: request.URL.Path, Params: map[string]string{"response_id": id}, Query: request.URL.Query(),
		Headers: request.Header, Protocol: plugin.TaskProtocolResponses, Operation: "retrieve", Model: task.Model,
	}, handler.artifactLinks(request, task))
	if err != nil {
		writeTaskError(writer, err)
		return
	}
	writeTaskJSON(writer, http.StatusOK, rendered)
}

// serveTaskAPI serves new-api's generic task surface: POST /v1/tasks/:plugin
// submits, GET /v1/tasks/:id reads a task, its artifacts and their content.
func (handler *Handler) serveTaskAPI(writer http.ResponseWriter, request *http.Request, segments []string) {
	ctx := request.Context()
	first := segments[0]
	if request.Method == http.MethodPost && len(segments) == 1 && contract.ValidPluginKey(first) && !taskplugin.ValidTaskID(first) {
		handler.submitTaskIntent(writer, request, first)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		writeTaskError(writer, &taskplugin.Error{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Message: "use GET"})
		return
	}
	task, err := handler.tasks.Task(ctx, first, ownerOf(request))
	if err != nil {
		writeTaskError(writer, err)
		return
	}
	switch {
	case len(segments) == 1:
		writeTaskJSON(writer, http.StatusOK, taskplugin.TaskView(task))
	case len(segments) == 2 && segments[1] == "artifacts":
		artifacts, err := handler.tasks.Artifacts(ctx, task)
		if err != nil {
			writeTaskError(writer, err)
			return
		}
		base := publicBase(request)
		listed := make([]map[string]any, 0, len(artifacts))
		for _, artifact := range artifacts {
			listed = append(listed, map[string]any{"key": artifact.Key, "type": artifact.Type, "mimeType": artifact.MimeType, "content_url": handler.tasks.ArtifactURL(base, task.ID, artifact.Key)})
		}
		encoded, _ := json.Marshal(map[string]any{"task_id": task.ID, "artifacts": listed})
		writeTaskJSON(writer, http.StatusOK, encoded)
	case len(segments) == 4 && segments[1] == "artifacts" && segments[3] == "content":
		if err := handler.tasks.ServeContent(ctx, writer, request, task, segments[2]); err != nil {
			writeTaskError(writer, err)
		}
	default:
		writeTaskError(writer, &taskplugin.Error{Status: http.StatusNotFound, Code: "not_found", Message: "no such task endpoint"})
	}
}

// submitTaskIntent serves POST /v1/tasks/:plugin: the body is the intent
// a decoder would return.
func (handler *Handler) submitTaskIntent(writer http.ResponseWriter, request *http.Request, pluginKey string) {
	meta := handler.tasks.TaskPlugin(pluginKey)
	if meta == nil {
		writeTaskError(writer, &taskplugin.Error{Status: http.StatusNotFound, Code: "plugin_not_found", Message: "no enabled task plugin " + pluginKey})
		return
	}
	current, finish := handler.startTaskRecord(writer, request, contract.ProtocolPluginNative, "", false)
	defer finish()
	request = current.request
	var err error
	if current.body, err = taskplugin.ReadBody(request, maxTaskBody); err != nil {
		current.fail(err)
		return
	}
	if current.body.Kind == taskplugin.BodyJSON {
		var named struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(current.body.JSON, &named)
		if current.ingressPlugins(named.Model) {
			return
		}
	}
	var intent struct {
		Model         string          `json:"model"`
		Action        string          `json:"action"`
		RequestBody   json.RawMessage `json:"requestBody"`
		RequestBodyV2 json.RawMessage `json:"request_body"`
		OriginTaskIDs []string        `json:"originTaskIds"`
	}
	if current.body.Kind != taskplugin.BodyJSON || json.Unmarshal(current.body.JSON, &intent) != nil || intent.Model == "" {
		current.fail(&taskplugin.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "the body is {model, action, requestBody, originTaskIds}"})
		return
	}
	if intent.RequestBody == nil {
		intent.RequestBody = intent.RequestBodyV2
	}
	current.session.classified.Model = intent.Model
	candidates, err := current.taskCandidates(contract.ProtocolPluginNative, intent.Model, false, func(found *plugin.TaskMeta) bool {
		return found.Key == pluginKey && slices.Contains(found.Models, intent.Model)
	})
	if err != nil {
		current.fail(err)
		return
	}
	decode := func(string) (taskplugin.Intent, error) {
		return taskplugin.Intent{Kind: "submit", Model: intent.Model, Action: intent.Action, RequestBody: intent.RequestBody, OriginTaskIDs: intent.OriginTaskIDs}, nil
	}
	submitted, err := current.submit(candidates, taskSurfaceNative, "POST /v1/tasks/"+pluginKey, decode, true, nil)
	if err != nil {
		current.fail(err)
		return
	}
	writeTaskJSON(current.writer, http.StatusOK, taskplugin.TaskView(submitted.Task))
	current.session.noteSucceeded()
}

// serveNativeRoute serves a route a task plugin declares: a submission,
// a query of tasks, or a dynamic route that decodes into either.
func (handler *Handler) serveNativeRoute(writer http.ResponseWriter, request *http.Request, route taskplugin.Route, params map[string]string) {
	ctx := request.Context()
	native := taskplugin.Request{Method: request.Method, Path: request.URL.Path, Params: params, Query: request.URL.Query(), Headers: request.Header}
	answerError := func(writer http.ResponseWriter, err error) {
		var known *taskplugin.Error
		if !errors.As(err, &known) {
			known = &taskplugin.Error{Status: http.StatusBadGateway, Code: "task_failed", Message: err.Error()}
		}
		if rendered, ok := handler.tasks.NativeError(ctx, route.Plugin, native, known); ok && writeNativeReply(writer, known.Status, rendered) == nil {
			return
		}
		writeTaskError(writer, known)
	}
	// answer writes a presenter's reply; a reply wrapper that breaks the
	// rules fails the presenter.
	answer := func(writer http.ResponseWriter, rendered json.RawMessage) error {
		if err := writeNativeReply(writer, http.StatusOK, rendered); err != nil {
			answerError(writer, &taskplugin.Error{Status: http.StatusInternalServerError, Code: "plugin_error", Message: err.Error()})
			return err
		}
		return nil
	}
	if route.Type == "query" {
		// The task id is the path parameter or, when the path has none,
		// the query-string parameter, as MiniMax's
		// GET /v1/query/video_generation?task_id= reads it.
		id, inPath := params[route.TaskIDParam]
		if !inPath {
			id = request.URL.Query().Get(route.TaskIDParam)
		}
		task, err := handler.tasks.Task(ctx, id, ownerOf(request))
		if err == nil && task.PluginKey != route.Plugin {
			err = &taskplugin.Error{Status: http.StatusNotFound, Code: "task_not_found", Message: "no task " + id}
		}
		if err != nil {
			answerError(writer, err)
			return
		}
		rendered, err := handler.tasks.Present(ctx, route.Plugin, route.Render, native, []storage.PluginTask{task}, false, nil, handler.artifactLinks(request, task))
		if err != nil {
			answerError(writer, err)
			return
		}
		_ = answer(writer, rendered)
		return
	}
	if _, err := readTaskBody(request); err != nil {
		answerError(writer, err)
		return
	}
	raw, _ := io.ReadAll(request.Body)
	request.Body = io.NopCloser(bytes.NewReader(raw))
	probe := request.Clone(ctx)
	probe.Body = io.NopCloser(bytes.NewReader(raw))
	body, err := taskplugin.ReadBody(probe, maxTaskBody)
	if err != nil {
		answerError(writer, err)
		return
	}
	model, _ := body.TopLevelModel()
	if len(route.Models) > 0 && !slices.Contains(route.Models, model) {
		answerError(writer, &taskplugin.Error{Status: http.StatusBadRequest, Code: "model_not_supported", Message: fmt.Sprintf("this route serves %s", strings.Join(route.Models, ", "))})
		return
	}
	native.Body = body
	intent, err := handler.tasks.Decode(ctx, route.Plugin, "native."+route.Decode, native)
	if err != nil {
		answerError(writer, err)
		return
	}
	if intent.Kind == "query" {
		var tasks []storage.PluginTask
		for _, id := range intent.TaskIDs {
			task, err := handler.tasks.Task(ctx, id, ownerOf(request))
			if err == nil && task.PluginKey == route.Plugin {
				tasks = append(tasks, task)
			}
		}
		rendered, err := handler.tasks.Present(ctx, route.Plugin, route.Render, native, tasks, true, nil, handler.artifactLinks(request, storage.PluginTask{}))
		if err != nil {
			answerError(writer, err)
			return
		}
		_ = answer(writer, rendered)
		return
	}
	current, finish := handler.startTaskRecord(writer, request, contract.ProtocolPluginNative, intent.Model, false)
	defer finish()
	request = current.request
	if current.body, err = taskplugin.ReadBody(request, maxTaskBody); err != nil {
		current.fail(err)
		return
	}
	if current.ingressPlugins(intent.Model) {
		return
	}
	native.Body = current.body
	candidates, err := current.taskCandidates(contract.ProtocolPluginNative, intent.Model, false, func(meta *plugin.TaskMeta) bool {
		return meta.Key == route.Plugin && slices.Contains(meta.Models, intent.Model)
	})
	if err != nil {
		answerError(current.writer, err)
		current.session.noteFailed(errorSummaryFromInference("provider_unavailable", err.Error(), false))
		return
	}
	if intent.Action == "" {
		intent.Action = route.Action
	}
	decode := func(string) (taskplugin.Intent, error) { return intent, nil }
	submitted, err := current.submit(candidates, taskSurfaceNative, route.Method+" "+route.Path, decode, route.Retain, nil)
	if err != nil {
		var known *taskplugin.Error
		if !errors.As(err, &known) {
			known = &taskplugin.Error{Status: http.StatusBadGateway, Code: "task_failed", Message: err.Error()}
		}
		answerError(current.writer, known)
		current.session.noteFailed(errorSummaryFromInference(known.Code, known.Message, known.Retryable))
		return
	}
	rendered, err := handler.tasks.Present(ctx, route.Plugin, route.Render, native, []storage.PluginTask{submitted.Task}, false, submitted.Data, nil)
	if err != nil {
		answerError(current.writer, err)
		current.session.noteFailed(errorSummaryFromInference("plugin_error", err.Error(), false))
		return
	}
	if err := answer(current.writer, rendered); err != nil {
		current.session.noteFailed(errorSummaryFromInference("plugin_error", err.Error(), false))
		return
	}
	current.session.noteSucceeded()
}

// writeNativeReply writes what a native presenter returned: its JSON as the
// body of a reply of status, or a reply wrapper's own status, Location and
// Retry-After and body, as new-api reads {__status, __headers, __body}. A
// wrapper without __body has no body. It writes nothing for a wrapper that
// breaks the rules.
func writeNativeReply(writer http.ResponseWriter, status int, rendered json.RawMessage) error {
	var members map[string]json.RawMessage
	if json.Unmarshal(rendered, &members) != nil || members["__status"] == nil {
		writeTaskJSON(writer, status, rendered)
		return nil
	}
	var reply struct {
		Status  int               `json:"__status"`
		Headers map[string]string `json:"__headers"`
	}
	for name := range members {
		if name != "__status" && name != "__headers" && name != "__body" {
			return fmt.Errorf("a reply wrapper has no member %s", name)
		}
	}
	if err := json.Unmarshal(rendered, &reply); err != nil || reply.Status < 200 || reply.Status > 599 {
		return errors.New("a reply wrapper's __status is a status from 200 to 599")
	}
	for name := range reply.Headers {
		if name != "Location" && name != "Retry-After" {
			return fmt.Errorf("a reply wrapper sets Location and Retry-After, not %s", name)
		}
	}
	header := writer.Header()
	for name, value := range reply.Headers {
		header.Set(name, value)
	}
	body, ok := members["__body"]
	if !ok {
		header.Set("Cache-Control", "no-store")
		writer.WriteHeader(reply.Status)
		return nil
	}
	writeTaskJSON(writer, reply.Status, body)
	return nil
}
