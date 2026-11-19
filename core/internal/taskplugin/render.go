package taskplugin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// Request is what the decoders, presenters and renderers read of a
// client's request: its method, path, parameters, query and body, and for
// a protocol the model the gateway pinned.
type Request struct {
	Method string
	Path   string
	Params map[string]string
	Query  url.Values
	// Headers are the client's request headers; hooks read them without
	// the ones that carry credentials.
	Headers http.Header
	Body    *Body
	// Protocol and Operation are set for a protocol: openai_video and
	// create, retrieve or content.
	Protocol      string
	Operation     string
	Model         string
	UpstreamModel string
	Stream        bool
}

// view is the request as a hook reads it; requestBody and artifacts are
// set for a renderer.
func (request Request) view(clientOrder bool, requestBody json.RawMessage, artifacts map[string]artifactLink) map[string]any {
	params := request.Params
	if params == nil {
		params = map[string]string{}
	}
	query := map[string][]string(request.Query)
	if query == nil {
		query = map[string][]string{}
	}
	headers := clientHeaders(request.Headers)
	for _, name := range []string{"Content-Type", "Accept"} {
		if _, ok := headers[name]; !ok {
			headers[name] = ""
		}
	}
	value := map[string]any{
		"method": request.Method, "path": request.Path, "params": params, "query": query,
		"requestHeaders": headers, "body": request.Body.decoded(clientOrder),
	}
	if request.Protocol != "" {
		value["protocol"] = request.Protocol
		value["operation"] = request.Operation
		value["model"] = request.Model
		value["stream"] = request.Stream
		upstream := request.UpstreamModel
		if upstream == "" {
			upstream = request.Model
		}
		value["upstreamModel"] = upstream
	}
	if requestBody != nil {
		value["requestBody"] = jsonOrNull(requestBody)
	}
	if artifacts != nil {
		value["artifacts"] = artifacts
	}
	return value
}

// renderCall is a hook of a plugin that does not reach the vendor.
func (engine *Engine) renderCall(ctx context.Context, target *plugin.Plugin, hook string, args ...any) (json.RawMessage, error) {
	call := &exchange{engine: engine, target: target, meta: target.Task()}
	return call.call(ctx, hook, args...)
}

// Decode runs a decoder, protocols.<protocol>.decodeRequest or a native
// route's, on request and returns the intent it produced.
func (engine *Engine) Decode(ctx context.Context, pluginKey, hook string, request Request) (Intent, error) {
	target, meta, err := engine.plugin(pluginKey)
	if err != nil {
		return Intent{}, err
	}
	raw, err := engine.renderCall(ctx, target, hook, request.view(meta.Has("json-order@1"), nil, nil))
	if err != nil {
		return Intent{}, err
	}
	var intent Intent
	if err := json.Unmarshal(raw, &intent); err != nil {
		return Intent{}, failure(http.StatusBadGateway, "plugin_error", "%s returned something other than an intent", hook)
	}
	switch intent.Kind {
	case "submit":
		if request.Protocol != "" {
			if intent.Model == "" {
				intent.Model = request.Model
			}
			if intent.Model != request.Model {
				return Intent{}, failure(http.StatusBadRequest, "invalid_request", "%s returned the model %s for a request for %s", hook, intent.Model, request.Model)
			}
		}
		if strings.TrimSpace(intent.Model) == "" {
			return Intent{}, failure(http.StatusBadRequest, "invalid_request", "the request names no model")
		}
		if !slices.Contains(meta.Models, intent.Model) {
			return Intent{}, failure(http.StatusBadRequest, "model_not_supported", "the task plugin %s does not serve the model %s", meta.Key, intent.Model)
		}
	case "query":
		if request.Protocol != "" || len(intent.TaskIDs) == 0 {
			return Intent{}, failure(http.StatusBadGateway, "plugin_error", "%s returned a query of no tasks", hook)
		}
	default:
		return Intent{}, failure(http.StatusBadGateway, "plugin_error", "%s returned the intent kind %q", hook, intent.Kind)
	}
	return intent, nil
}

// Task returns the task id names, while owner may read it: theirs, and
// keeping its result.
func (engine *Engine) Task(ctx context.Context, id, owner string) (storage.PluginTask, error) {
	if !ValidTaskID(id) {
		return storage.PluginTask{}, failure(http.StatusNotFound, "task_not_found", "no task %s", id)
	}
	task, err := engine.store.GetPluginTask(ctx, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return storage.PluginTask{}, failure(http.StatusNotFound, "task_not_found", "no task %s", id)
		}
		return storage.PluginTask{}, failure(http.StatusInternalServerError, "task_unavailable", "the task cannot be read")
	}
	if task.Owner != owner || !task.Retain {
		return storage.PluginTask{}, failure(http.StatusNotFound, "task_not_found", "no task %s", id)
	}
	return task, nil
}

// TaskView is a task as /v1/tasks/:id answers it.
func TaskView(task storage.PluginTask) json.RawMessage {
	encoded, _ := marshalOrdered(view(task))
	return encoded
}

// Present runs a native presenter on tasks: one task, or a list for a
// query of several.
//
// links, set for a query route, makes the download links of ctx.artifacts:
// those of the one task when it has succeeded, none otherwise. A presenter
// that throws artifact_not_found answers 404, as for a missing task.
func (engine *Engine) Present(ctx context.Context, pluginKey, presenter string, request Request, tasks []storage.PluginTask, list bool, full json.RawMessage, links func(key string) string) (json.RawMessage, error) {
	target, meta, err := engine.plugin(pluginKey)
	if err != nil {
		return nil, err
	}
	var artifacts map[string]artifactLink
	if links != nil {
		artifacts = map[string]artifactLink{}
		if !list && len(tasks) == 1 && tasks[0].Status == contract.PluginTaskSuccess && target.HasTaskHook("listArtifacts") {
			artifacts = engine.artifactLinks(ctx, target, tasks[0], links)
		}
	}
	views := make([]taskView, 0, len(tasks))
	for _, task := range tasks {
		current := view(task)
		if full != nil {
			current.Data = publicData(full, task.UpstreamTaskID, task.ID)
		}
		views = append(views, current)
	}
	var argument any = views
	if !list && len(views) == 1 {
		argument = views[0]
	}
	raw, err := engine.renderCall(ctx, target, "native."+presenter, request.view(meta.Has("json-order@1"), nil, artifacts), argument)
	if err != nil {
		if strings.Contains(err.Error(), "artifact_not_found") {
			return nil, failure(http.StatusNotFound, "task_not_found", "no such task")
		}
		return nil, err
	}
	return jsonOrNull(raw), nil
}

// NativeError renders an error on a native route with the plugin's
// native.error, when it has one.
func (engine *Engine) NativeError(ctx context.Context, pluginKey string, request Request, failed *Error) (json.RawMessage, bool) {
	target, meta, err := engine.plugin(pluginKey)
	if err != nil || !target.HasTaskHook("native.error") {
		return nil, false
	}
	argument := map[string]any{"code": failed.Code, "message": failed.Message, "httpStatus": failed.Status, "retryable": failed.Retryable}
	raw, err := engine.renderCall(ctx, target, "native.error", request.view(meta.Has("json-order@1"), nil, nil), argument)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil, false
	}
	return raw, true
}

// videoStatus is a task's status as OpenAI's video object says it.
func videoStatus(status string) string {
	switch status {
	case contract.PluginTaskInProgress:
		return "in_progress"
	case contract.PluginTaskSuccess:
		return "completed"
	case contract.PluginTaskFailure:
		return "failed"
	}
	return "queued"
}

func progressNumber(progress string) int {
	value, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(progress, "%")), 64)
	if err != nil || value < 0 {
		return 0
	}
	return int(min(value, 100))
}

// RenderVideo is a task as OpenAI's video object, from the plugin's
// renderer: the fields the gateway owns overwritten.
func (engine *Engine) RenderVideo(ctx context.Context, task storage.PluginTask, request Request) (json.RawMessage, error) {
	target, meta, err := engine.plugin(task.PluginKey)
	if err != nil {
		return nil, err
	}
	raw, err := engine.renderCall(ctx, target, "protocols.openai_video.render", request.view(meta.Has("json-order@1"), task.Request, nil), view(task))
	if err != nil {
		return nil, err
	}
	root, err := parseNode(bytes.TrimSpace(raw))
	if err != nil || root.kind != '{' {
		return nil, failure(http.StatusBadGateway, "plugin_error", "protocols.openai_video.render returned something other than an object")
	}
	set := func(name string, value any) {
		encoded, _ := marshalOrdered(value)
		for index, key := range root.keys {
			if key == name {
				root.children[index] = &jsonNode{raw: encoded}
				return
			}
		}
		root.keys = append(root.keys, name)
		root.children = append(root.children, &jsonNode{raw: encoded})
	}
	remove := func(name string) {
		for index, key := range root.keys {
			if key == name {
				root.keys = slices.Delete(root.keys, index, index+1)
				root.children = slices.Delete(root.children, index, index+1)
				return
			}
		}
	}
	set("id", task.ID)
	set("object", "video")
	set("model", task.Model)
	set("status", videoStatus(task.Status))
	set("progress", progressNumber(task.Progress))
	set("created_at", task.CreatedAt.Unix())
	if !task.FinishedAt.IsZero() {
		set("completed_at", task.FinishedAt.Unix())
	} else {
		remove("completed_at")
	}
	remove("task_id")
	if task.Status == contract.PluginTaskFailure {
		if _, ok := root.member("error"); !ok {
			set("error", map[string]string{"code": "video_generation_failed", "message": task.FailReason})
		}
	}
	return engine.compactData(task.ID, root.encode()), nil
}

// RenderImages is a finished task as OpenAI's image response, from the
// plugin's renderer: created added when it is missing, and each image
// fetched into b64_json when the client asked for that.
func (engine *Engine) RenderImages(ctx context.Context, task storage.PluginTask, request Request, full json.RawMessage, responseFormat string) (json.RawMessage, error) {
	target, meta, err := engine.plugin(task.PluginKey)
	if err != nil {
		return nil, err
	}
	current := view(task)
	if full != nil {
		current.Data = publicData(full, task.UpstreamTaskID, task.ID)
	}
	raw, err := engine.renderCall(ctx, target, "protocols.openai_image.render", request.view(meta.Has("json-order@1"), task.Request, nil), current)
	if err != nil {
		return nil, err
	}
	var rendered map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rendered); err != nil {
		return nil, failure(http.StatusBadGateway, "plugin_error", "protocols.openai_image.render returned something other than an object")
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(rendered["data"], &entries); err != nil {
		return nil, failure(http.StatusBadGateway, "plugin_error", "protocols.openai_image.render returned no data list")
	}
	if _, ok := rendered["created"]; !ok {
		rendered["created"], _ = json.Marshal(task.CreatedAt.Unix())
	}
	if responseFormat == "b64_json" {
		service, serviceErr := engine.taskService(ctx, task)
		for index, entry := range entries {
			if _, ok := entry["b64_json"]; ok {
				continue
			}
			var address string
			if json.Unmarshal(entry["url"], &address) != nil || address == "" {
				continue
			}
			if serviceErr != nil {
				break
			}
			data, err := engine.fetchImage(ctx, service, address)
			if err != nil {
				engine.logf("task %s: fetch image %d for b64_json: %v", task.ID, index, err)
				continue
			}
			entry["b64_json"], _ = json.Marshal(base64.StdEncoding.EncodeToString(data))
		}
		rendered["data"], _ = json.Marshal(entries)
	}
	return marshalOrdered(rendered)
}

func (engine *Engine) taskService(ctx context.Context, task storage.PluginTask) (contract.Service, error) {
	if engine.services == nil {
		return contract.Service{}, errors.New("providers are unavailable")
	}
	record, err := engine.services.GetService(ctx, task.ServiceID)
	if err != nil {
		return contract.Service{}, err
	}
	return record.Service, nil
}

// maxImageBytes bounds an image fetched into b64_json.
const maxImageBytes = 64 << 20

// fetchImage downloads an image a renderer linked to, as a credentialless
// download through the provider's proxy.
func (engine *Engine) fetchImage(ctx context.Context, service contract.Service, address string) ([]byte, error) {
	if strings.HasPrefix(address, "data:") {
		_, payload, found := strings.Cut(address, ",")
		if !found {
			return nil, errors.New("the data URL is malformed")
		}
		return base64.StdEncoding.DecodeString(payload)
	}
	target, err := url.Parse(address)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, errors.New("the image link is not http or https")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	call := &exchange{engine: engine, service: service}
	response, err := call.download(ctx, "openai_image", http.MethodGet, target, &descriptor{}, false, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the image host answered %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxImageBytes+1))
	if err != nil || len(data) > maxImageBytes {
		return nil, errors.New("the image could not be read")
	}
	return data, nil
}

// Response statuses of OpenAI's Responses API.
const (
	responseQueued     = "queued"
	responseInProgress = "in_progress"
	responseCompleted  = "completed"
	responseFailed     = "failed"
)

func responseStatus(task storage.PluginTask) string {
	switch task.Status {
	case contract.PluginTaskSuccess:
		return responseCompleted
	case contract.PluginTaskFailure:
		return responseFailed
	case contract.PluginTaskInProgress:
		return responseInProgress
	}
	return responseQueued
}

// ResponseObject is a task as OpenAI's response object, with output and
// metadata when it finished.
func ResponseObject(task storage.PluginTask, output, metadata json.RawMessage) json.RawMessage {
	response := map[string]any{
		"id": task.ID, "object": "response", "created_at": task.CreatedAt.Unix(), "status": responseStatus(task),
		"model": task.Model, "output": jsonOrEmptyList(output), "parallel_tool_calls": false, "tool_choice": "none", "tools": []any{},
		"error": nil, "incomplete_details": nil, "usage": nil,
	}
	if !task.FinishedAt.IsZero() && task.Status == contract.PluginTaskSuccess {
		response["completed_at"] = task.FinishedAt.Unix()
	}
	if trimmed := bytes.TrimSpace(metadata); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		response["metadata"] = json.RawMessage(trimmed)
	} else {
		response["metadata"] = map[string]any{}
	}
	if task.Status == contract.PluginTaskFailure {
		response["error"] = map[string]string{"code": "task_failed", "message": task.FailReason}
	}
	encoded, _ := marshalOrdered(response)
	return encoded
}

func jsonOrEmptyList(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return json.RawMessage("[]")
	}
	return trimmed
}

// responseFinal is what renderFinal returns.
type responseFinal struct {
	Output   json.RawMessage `json:"output"`
	Metadata json.RawMessage `json:"metadata"`
}

// RenderResponse is a task as a retrieved response: the plugin's final
// rendering once it succeeded; for a plugin that only streams, its events
// rendered once and their output as one message.
func (engine *Engine) RenderResponse(ctx context.Context, task storage.PluginTask, request Request, links func(key string) string) (json.RawMessage, error) {
	if task.Status != contract.PluginTaskSuccess {
		return ResponseObject(task, nil, nil), nil
	}
	target, meta, err := engine.plugin(task.PluginKey)
	if err != nil {
		return nil, err
	}
	artifacts := engine.artifactLinks(ctx, target, task, links)
	rendered := request.view(meta.Has("json-order@1"), task.Request, artifacts)
	if target.HasTaskHook("protocols.openai_responses.renderFinal") {
		raw, err := engine.renderCall(ctx, target, "protocols.openai_responses.renderFinal", rendered, view(task))
		if err != nil {
			return nil, err
		}
		var final responseFinal
		if err := json.Unmarshal(raw, &final); err != nil {
			return nil, failure(http.StatusBadGateway, "plugin_error", "renderFinal returned something other than {output, metadata}")
		}
		return ResponseObject(task, final.Output, final.Metadata), nil
	}
	events, _, _, err := engine.renderEvents(ctx, target, rendered, task, nil)
	if err != nil {
		return nil, err
	}
	var text strings.Builder
	for _, event := range events {
		if event.Type == "output" {
			text.WriteString(event.Text())
		}
	}
	return ResponseObject(task, outputMessage(task.ID, text.String()), nil), nil
}

// ResponseEvent is an event renderEvents returns: progress, output or
// error.
type ResponseEvent struct {
	Type     string          `json:"type"`
	Message  string          `json:"message"`
	Progress json.RawMessage `json:"progress"`
	Data     json.RawMessage `json:"data"`
	Code     string          `json:"code"`
}

// Text is an output event's data as text: a string as it is, anything else
// as its JSON.
func (event ResponseEvent) Text() string {
	var text string
	if json.Unmarshal(event.Data, &text) == nil {
		return text
	}
	return string(bytes.TrimSpace(event.Data))
}

type eventsResult struct {
	Events []ResponseEvent `json:"events"`
	State  json.RawMessage `json:"state"`
	Done   bool            `json:"done"`
}

func (engine *Engine) renderEvents(ctx context.Context, target *plugin.Plugin, rendered map[string]any, task storage.PluginTask, previous json.RawMessage) ([]ResponseEvent, json.RawMessage, bool, error) {
	raw, err := engine.renderCall(ctx, target, "protocols.openai_responses.renderEvents", rendered, view(task), jsonOrNull(previous))
	if err != nil {
		return nil, nil, false, err
	}
	var result eventsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, nil, false, failure(http.StatusBadGateway, "plugin_error", "renderEvents returned something other than {events, state, done}")
	}
	return result.Events, nullable(result.State), result.Done, nil
}

// RenderEvents runs renderEvents on a task as a stream observes it.
func (engine *Engine) RenderEvents(ctx context.Context, task storage.PluginTask, request Request, previous json.RawMessage, links func(key string) string) ([]ResponseEvent, json.RawMessage, bool, error) {
	target, meta, err := engine.plugin(task.PluginKey)
	if err != nil {
		return nil, nil, false, err
	}
	var artifacts map[string]artifactLink
	if task.Status == contract.PluginTaskSuccess {
		artifacts = engine.artifactLinks(ctx, target, task, links)
	}
	return engine.renderEvents(ctx, target, request.view(meta.Has("json-order@1"), task.Request, artifacts), task, previous)
}

// outputMessage is an assistant message holding text, as a response's
// output.
func outputMessage(id, text string) json.RawMessage {
	message := []map[string]any{{
		"type": "message", "id": "msg_" + strings.TrimPrefix(id, responsePrefix), "status": "completed", "role": "assistant",
		"content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}, "logprobs": []any{}}},
	}}
	encoded, _ := marshalOrdered(message)
	return encoded
}

// OutputMessage is the message a stream's output events add up to.
func OutputMessage(id, text string) json.RawMessage { return outputMessage(id, text) }
