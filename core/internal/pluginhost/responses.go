package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// StreamEvent is one event of a server-sent stream: its name, id and data,
// the data lines joined by line breaks.
type StreamEvent struct {
	Event string
	ID    string
	Data  []byte
}

// value is the event as a stream hook sees it: the data parsed when it is
// JSON, else as text.
func (event StreamEvent) value() map[string]any {
	value := map[string]any{}
	if event.Event != "" {
		value["event"] = event.Event
	}
	if event.ID != "" {
		value["id"] = event.ID
	}
	if json.Valid(event.Data) {
		value["data"] = json.RawMessage(event.Data)
	} else {
		value["data"] = string(event.Data)
	}
	return value
}

type streamEventResult struct {
	Event string          `json:"event"`
	ID    string          `json:"id"`
	Data  json.RawMessage `json:"data"`
}

// streamEvent turns a hook's event back into one to send: text data goes as
// it is, any other value as JSON.
func (result streamEventResult) streamEvent() StreamEvent {
	data := []byte(result.Data)
	var text string
	if len(data) > 0 && data[0] == '"' && json.Unmarshal(data, &text) == nil {
		data = []byte(text)
	}
	return StreamEvent{Event: result.Event, ID: result.ID, Data: data}
}

func sameEvents(a, b []StreamEvent) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index].Event != b[index].Event || a[index].ID != b[index].ID || !bytes.Equal(a[index].Data, b[index].Data) {
			return false
		}
	}
	return true
}

// maxStreamFailures is how many events in a row a stream hook may fail on
// before the stream's later events no longer go to it.
const maxStreamFailures = 3

// StreamRewrite runs the stream hooks of one attempt's response over its
// events, in the plugins' order, each with the state it returned last.
type StreamRewrite struct {
	ext     *Extensions
	request map[string]any
	stages  []*streamStage
}

type streamStage struct {
	target      *plugin.Plugin
	state       json.RawMessage
	events      int
	failures    int
	consecutive int
	changed     bool
	off         bool
	lastError   string
	info        plugin.CallInfo
}

// Stream starts the stream hooks of plugins granted response.write over one
// attempt's stream; nil when none rewrites streams.
func (ext *Extensions) Stream() *StreamRewrite {
	plugins := ext.serving(plugin.HookOnStreamEvent, plugin.CapabilityResponseWrite)
	if len(plugins) == 0 {
		return nil
	}
	stream := &StreamRewrite{ext: ext, request: ext.requestContext()}
	for _, target := range plugins {
		stream.stages = append(stream.stages, &streamStage{target: target})
	}
	return stream
}

// Event runs an event through every stream hook and returns the events to
// send in its place.
func (stream *StreamRewrite) Event(ctx context.Context, event StreamEvent) []StreamEvent {
	return stream.through(ctx, 0, []StreamEvent{event})
}

// through runs events through the stages from first on.
func (stream *StreamRewrite) through(ctx context.Context, first int, events []StreamEvent) []StreamEvent {
	for _, stage := range stream.stages[first:] {
		var outputs []StreamEvent
		for _, event := range events {
			outputs = append(outputs, stream.run(ctx, stage, event)...)
		}
		events = outputs
	}
	return events
}

// run gives one event to one stage. An event it fails on goes on as it
// came.
func (stream *StreamRewrite) run(ctx context.Context, stage *streamStage, event StreamEvent) []StreamEvent {
	if stage.off {
		return []StreamEvent{event}
	}
	stage.events++
	state := any(nil)
	if stage.state != nil {
		state = stage.state
	}
	result, info, skip, err := stream.ext.call(ctx, stage.target, plugin.HookOnStreamEvent, stream.request, event.value(), state)
	stage.add(info)
	if skip != "" {
		stage.off, stage.lastError = true, skip
		return []StreamEvent{event}
	}
	var decided struct {
		Events *[]streamEventResult `json:"events"`
		State  json.RawMessage      `json:"state"`
	}
	if err == nil {
		err = decode(result, &decided)
	}
	if err != nil {
		stage.failures++
		stage.consecutive++
		stage.lastError = err.Error()
		if stage.consecutive >= maxStreamFailures {
			stage.off = true
		}
		return []StreamEvent{event}
	}
	stage.consecutive = 0
	if len(decided.State) > 0 && string(decided.State) != "null" {
		stage.state = decided.State
	}
	if decided.Events == nil {
		return []StreamEvent{event}
	}
	outputs := make([]StreamEvent, 0, len(*decided.Events))
	for _, output := range *decided.Events {
		outputs = append(outputs, output.streamEvent())
	}
	if !sameEvents(outputs, []StreamEvent{event}) {
		stage.changed = true
	}
	return outputs
}

func (stage *streamStage) add(info plugin.CallInfo) {
	stage.info.Elapsed += info.Elapsed
	stage.info.Script += info.Script
	stage.info.Network.Requests += info.Network.Requests
	stage.info.Network.Denied += info.Network.Denied
	stage.info.Network.Errors += info.Network.Errors
	stage.info.Network.Wait += info.Network.Wait
}

// End runs the onStreamEnd hooks once the stream ended: the events each
// returns go through the stream hooks after it.
func (stream *StreamRewrite) End(ctx context.Context) []StreamEvent {
	var events []StreamEvent
	for index, stage := range stream.stages {
		if stage.off || !stage.target.Has(plugin.HookOnStreamEnd) {
			continue
		}
		state := any(nil)
		if stage.state != nil {
			state = stage.state
		}
		result, info, skip, err := stream.ext.call(ctx, stage.target, plugin.HookOnStreamEnd, stream.request, state)
		stage.add(info)
		if skip != "" || err != nil {
			stage.failures++
			if err != nil {
				stage.lastError = err.Error()
			}
			continue
		}
		var decided struct {
			Events []streamEventResult `json:"events"`
		}
		if decode(result, &decided) != nil || len(decided.Events) == 0 {
			continue
		}
		ended := make([]StreamEvent, 0, len(decided.Events))
		for _, output := range decided.Events {
			ended = append(ended, output.streamEvent())
		}
		stage.changed = true
		events = append(events, stream.through(ctx, index+1, ended)...)
	}
	return events
}

// Close keeps one record of each stream hook with the request: how many
// events it saw, how many it failed on, and whether it changed any.
func (stream *StreamRewrite) Close() {
	if stream == nil {
		return
	}
	for _, stage := range stream.stages {
		line := record{status: contract.PluginRecordOK, events: stage.events, failed: stage.failures}
		if stage.changed {
			line.effects = []string{contract.PluginEffectStream}
		}
		if stage.off {
			line.status, line.skipped, line.summary = contract.PluginRecordFailed, "stream_stopped", stage.lastError
			if stage.lastError == "circuit_open" {
				line.status, line.skipped, line.summary = contract.PluginRecordSkipped, "circuit_open", ""
			}
		} else if stage.failures > 0 {
			line.summary = stage.lastError
		}
		stream.ext.note(stage.target, plugin.HookOnStreamEvent, stage.info, line)
	}
}

// RewriteResponse runs the rewriteResponse hooks of plugins granted
// response.write over a whole JSON response, in the client's protocol, and
// returns the body the client gets.
func (ext *Extensions) RewriteResponse(ctx context.Context, status int, body []byte) []byte {
	const hook = plugin.HookRewriteResponse
	current := body
	for _, target := range ext.serving(hook, plugin.CapabilityResponseWrite) {
		arg := map[string]any{"request": ext.requestContext(), "response": map[string]any{"status": status, "body": json.RawMessage(current)}}
		result, info, skip, err := ext.call(ctx, target, hook, arg)
		if skip != "" {
			ext.note(target, hook, info, skipped(skip))
			continue
		}
		if err != nil {
			ext.note(target, hook, info, failure(err))
			continue
		}
		var rewritten struct {
			Body json.RawMessage `json:"body"`
		}
		if err := decode(result, &rewritten); err != nil {
			ext.note(target, hook, info, record{status: contract.PluginRecordFailed, skipped: plugin.ReasonInvalidResult, summary: "rewriteResponse returns { body? }"})
			continue
		}
		line := record{status: contract.PluginRecordOK}
		if len(rewritten.Body) > 0 && string(rewritten.Body) != "null" {
			if json.Valid(rewritten.Body) && strings.HasPrefix(strings.TrimSpace(string(rewritten.Body)), "{") {
				if !bytes.Equal(rewritten.Body, current) {
					line.effects = []string{contract.PluginEffectRewrite}
				}
				current = []byte(rewritten.Body)
			} else {
				line.skipped = "invalid_body"
			}
		}
		ext.note(target, hook, info, line)
	}
	return current
}

// RewritesResponses reports whether a plugin of the request changes its
// responses, whole or streamed.
func (ext *Extensions) RewritesResponses(streaming bool) bool {
	if streaming {
		return ext.Serves(plugin.HookOnStreamEvent, plugin.CapabilityResponseWrite)
	}
	return ext.Serves(plugin.HookRewriteResponse, plugin.CapabilityResponseWrite)
}

// Observes reports whether a plugin of the request reads its response once
// it ended.
func (ext *Extensions) Observes() bool {
	return ext.Serves(plugin.HookOnResponse, plugin.CapabilityResponseRead)
}

// ResponseSummary is what onResponse sees of a response once it ended, and
// onEvent without its text.
type ResponseSummary struct {
	Status     string         `json:"status"`
	HTTPStatus int            `json:"httpStatus,omitempty"`
	Provider   string         `json:"provider,omitempty"`
	Model      string         `json:"model,omitempty"`
	ElapsedMs  int64          `json:"elapsedMs"`
	Usage      *ResponseUsage `json:"usage,omitempty"`
	Error      string         `json:"error,omitempty"`
	// Text is the reply's visible text, as the privacy check left it.
	Text string `json:"text,omitempty"`
}

// ResponseUsage counts a response's tokens.
type ResponseUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead,omitempty"`
	CacheWrite int64 `json:"cacheWrite,omitempty"`
}

// maxObservedText bounds the reply text onResponse sees.
const maxObservedText = 64 << 10

// Finish runs, outside the request, the onResponse hooks of plugins granted
// response.read with the response's summary, and the onEvent hooks of
// plugins granted events with what happened, without its text. Their
// records join the request's once it is written.
func (ext *Extensions) Finish(summary ResponseSummary) {
	if ext == nil {
		return
	}
	summary.Text = clipBytes(summary.Text, maxObservedText)
	request := ext.requestContext()
	for _, target := range ext.serving(plugin.HookOnResponse, plugin.CapabilityResponseRead) {
		ext.host.after.push(func(ctx context.Context) {
			const hook = plugin.HookOnResponse
			_, info, skip, err := ext.call(ctx, target, hook, map[string]any{"request": request, "response": summary})
			line := record{status: contract.PluginRecordOK, effects: []string{contract.PluginEffectObserved}}
			switch {
			case skip != "":
				line = skipped(skip)
			case err != nil:
				line = failure(err)
			}
			ext.deliver(target, hook, info, line)
		})
	}
	event := summary
	event.Text = ""
	for _, target := range ext.serving(plugin.HookOnEvent, plugin.CapabilityEvents) {
		ext.host.after.push(func(ctx context.Context) {
			_, _, _, _ = ext.call(ctx, target, plugin.HookOnEvent, map[string]any{"type": "request.finished", "request": request, "response": event})
		})
	}
}

// deliver keeps a record of a call that ended after the request: with the
// request record once it is written, else until it is.
func (ext *Extensions) deliver(target *plugin.Plugin, hook string, info plugin.CallInfo, line record) {
	ext.note(target, hook, info, line)
	ext.mutex.Lock()
	persisted := ext.persisted
	ext.mutex.Unlock()
	if persisted {
		ext.host.appendRecords(ext.input.RequestID, ext.Records())
	}
}

// Persisted tells the extensions that the request record is written: what
// ended after the request joins it from now on.
func (ext *Extensions) Persisted() {
	if ext == nil {
		return
	}
	ext.mutex.Lock()
	ext.persisted = true
	ext.mutex.Unlock()
	ext.host.appendRecords(ext.input.RequestID, ext.Records())
}

// afterQueue runs the calls that follow a request: 256 waiting at most, two
// at once. A call that finds the queue full is dropped.
type afterQueue struct {
	jobs    chan func(context.Context)
	stop    chan struct{}
	once    sync.Once
	workers sync.WaitGroup
}

const (
	afterQueueSize = 256
	afterWorkers   = 2
)

func newAfterQueue() *afterQueue {
	queue := &afterQueue{jobs: make(chan func(context.Context), afterQueueSize), stop: make(chan struct{})}
	queue.workers.Add(afterWorkers)
	for range afterWorkers {
		go func() {
			defer queue.workers.Done()
			for {
				select {
				case <-queue.stop:
					return
				case job := <-queue.jobs:
					job(context.Background())
				}
			}
		}()
	}
	return queue
}

func (queue *afterQueue) push(job func(context.Context)) bool {
	select {
	case <-queue.stop:
		return false
	case queue.jobs <- job:
		return true
	default:
		return false
	}
}

func (queue *afterQueue) close() {
	queue.once.Do(func() { close(queue.stop) })
	queue.workers.Wait()
}
