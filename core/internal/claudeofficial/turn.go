package claudeofficial

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// outcome is how a turn left its process.
type outcome int

const (
	// outcomeFailed stops the process.
	outcomeFailed outcome = iota
	// outcomeParked leaves the process waiting for the caller's tool results.
	outcomeParked
	// outcomeIdle leaves the process ready for the next user message.
	outcomeIdle
)

type turnResult struct {
	outcome outcome
	// reply is the assistant turn as the caller received it.
	reply group
	// pending are the tool calls the caller must answer.
	pending []string
}

// httpFailure is an error the caller receives as an HTTP response.
type httpFailure struct {
	status     int
	kind       string
	message    string
	retryAfter time.Duration
}

func errorType(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case 529:
		return "overloaded_error"
	}
	if status >= 500 {
		return "api_error"
	}
	return "invalid_request_error"
}

func newFailure(status int, message string) httpFailure {
	return httpFailure{status: status, kind: errorType(status), message: message}
}

func (failure httpFailure) body() []byte {
	raw, _ := json.Marshal(map[string]any{
		"type": "error", "error": map[string]any{"type": failure.kind, "message": failure.message},
	})
	return raw
}

func (failure httpFailure) response(request *http.Request) *http.Response {
	body := failure.body()
	header := http.Header{"Content-Type": {"application/json"}}
	if failure.retryAfter > 0 {
		header.Set("Retry-After", strconv.FormatInt(int64((failure.retryAfter+time.Second-1)/time.Second), 10))
	}
	return &http.Response{
		StatusCode: failure.status, Status: fmt.Sprintf("%d %s", failure.status, http.StatusText(failure.status)),
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: header, Request: request,
		Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)),
	}
}

type messageUsage struct {
	input, cacheCreation, cacheRead, output int64
	cache5m, cache1h                        int64
}

func (usage *messageUsage) merge(raw map[string]any) {
	maximize := func(target *int64, value any) {
		if number, ok := toInt(value); ok && number > *target {
			*target = number
		}
	}
	maximize(&usage.input, raw["input_tokens"])
	maximize(&usage.cacheCreation, raw["cache_creation_input_tokens"])
	maximize(&usage.cacheRead, raw["cache_read_input_tokens"])
	maximize(&usage.output, raw["output_tokens"])
	if creation, ok := raw["cache_creation"].(map[string]any); ok {
		maximize(&usage.cache5m, creation["ephemeral_5m_input_tokens"])
		maximize(&usage.cache1h, creation["ephemeral_1h_input_tokens"])
	}
}

func toInt(value any) (int64, bool) {
	switch typed := value.(type) {
	case json.Number:
		number, err := typed.Int64()
		return number, err == nil
	case float64:
		return int64(typed), true
	}
	return 0, false
}

type outputBlock struct {
	kind      string
	id        string
	name      string
	text      strings.Builder
	signature string
	data      string
	input     strings.Builder
}

func (output *outputBlock) content() block {
	switch output.kind {
	case "text":
		return block{"type": "text", "text": output.text.String()}
	case "thinking":
		return block{"type": "thinking", "thinking": output.text.String(), "signature": output.signature}
	case "redacted_thinking":
		return block{"type": "redacted_thinking", "data": output.data}
	}
	return block{"type": "tool_use", "id": output.id, "name": output.name, "input": parseInput(output.input.String())}
}

func parseInput(raw string) any {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return map[string]any{}
	}
	return value
}

// turn relays one caller request: the CLI's model output for it, merged into
// one Claude message, until the model asks for the caller's tools or ends.
type turn struct {
	stream bool
	// names maps MCP tool names back to the caller's names.
	names   map[string]string
	request *http.Request
	done    func(turnResult)
	now     func() time.Time

	head chan *http.Response

	mu           sync.Mutex
	writer       *io.PipeWriter
	started      bool
	finished     bool
	messageID    string
	model        string
	startUsage   any
	mapping      map[int]int
	blocks       []*outputBlock
	current      string
	currentTools int
	usage        map[string]*messageUsage
	order        []string
	stopReason   any
	stopSequence any
	errorKind    string
	retryAt      time.Time
}

func newTurn(parsed request, httpRequest *http.Request, now func() time.Time, done func(turnResult)) *turn {
	names := make(map[string]string, len(parsed.Tools))
	for _, item := range parsed.Tools {
		names[item.MCPName] = item.Name
	}
	return &turn{
		stream: parsed.Stream, names: names, request: httpRequest, done: done, now: now,
		head: make(chan *http.Response, 1), mapping: map[int]int{}, usage: map[string]*messageUsage{},
	}
}

type turnBody struct {
	*io.PipeReader
	turn *turn
}

func (body turnBody) Close() error {
	err := body.PipeReader.Close()
	body.turn.abort()
	return err
}

func sse(name string, value any) []byte {
	raw, _ := json.Marshal(value)
	return []byte("event: " + name + "\ndata: " + string(raw) + "\n\n")
}

// emit writes to the caller's stream. Only the process reader calls it, so
// events keep their order without holding the lock while the caller reads.
func (current *turn) emit(writer *io.PipeWriter, data []byte) {
	if writer == nil || len(data) == 0 {
		return
	}
	_, _ = writer.Write(data)
}

type streamEvent struct {
	Type         string          `json:"type"`
	Index        int             `json:"index"`
	Message      json.RawMessage `json:"message"`
	ContentBlock map[string]any  `json:"content_block"`
	Delta        map[string]any  `json:"delta"`
	Usage        map[string]any  `json:"usage"`
}

func decodeNumbers(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func (current *turn) handleEvent(raw json.RawMessage) {
	var event streamEvent
	if decodeNumbers(raw, &event) != nil {
		return
	}
	current.mu.Lock()
	if current.finished {
		current.mu.Unlock()
		return
	}
	var out []byte
	var head *http.Response
	var finish bool
	switch event.Type {
	case "message_start":
		var message struct {
			ID    string         `json:"id"`
			Model string         `json:"model"`
			Usage map[string]any `json:"usage"`
		}
		_ = decodeNumbers(event.Message, &message)
		current.current = message.ID
		current.mapping = map[int]int{}
		current.currentTools = 0
		current.stopReason, current.stopSequence = nil, nil
		current.usageFor(message.ID).merge(message.Usage)
		if !current.started {
			current.started = true
			current.messageID, current.model, current.startUsage = message.ID, message.Model, message.Usage
			if current.stream {
				var start map[string]any
				_ = decodeNumbers(raw, &start)
				out = sse("message_start", start)
				head = current.streamResponse()
			}
		}
	case "content_block_start":
		kind, _ := event.ContentBlock["type"].(string)
		keep := kind == "text" || kind == "thinking" || kind == "redacted_thinking"
		output := &outputBlock{kind: kind}
		if kind == "tool_use" {
			name, _ := event.ContentBlock["name"].(string)
			if caller, ok := current.names[strings.TrimPrefix(name, toolPrefix)]; ok && strings.HasPrefix(name, toolPrefix) {
				keep = true
				output.id, _ = event.ContentBlock["id"].(string)
				output.name = caller
				event.ContentBlock["name"] = caller
				current.currentTools++
			}
		}
		if kind == "redacted_thinking" {
			output.data, _ = event.ContentBlock["data"].(string)
		}
		if !keep {
			current.mapping[event.Index] = -1
			break
		}
		index := len(current.blocks)
		current.mapping[event.Index] = index
		current.blocks = append(current.blocks, output)
		if current.stream {
			out = sse("content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": event.ContentBlock})
		}
	case "content_block_delta":
		index, ok := current.mapping[event.Index]
		if !ok || index < 0 {
			break
		}
		output := current.blocks[index]
		switch event.Delta["type"] {
		case "text_delta":
			text, _ := event.Delta["text"].(string)
			output.text.WriteString(text)
		case "thinking_delta":
			text, _ := event.Delta["thinking"].(string)
			output.text.WriteString(text)
		case "signature_delta":
			output.signature, _ = event.Delta["signature"].(string)
		case "input_json_delta":
			text, _ := event.Delta["partial_json"].(string)
			output.input.WriteString(text)
		}
		if current.stream {
			out = sse("content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": event.Delta})
		}
	case "content_block_stop":
		index, ok := current.mapping[event.Index]
		if ok && index >= 0 && current.stream {
			out = sse("content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
		}
	case "message_delta":
		if reason, ok := event.Delta["stop_reason"]; ok {
			current.stopReason = reason
		}
		if sequence, ok := event.Delta["stop_sequence"]; ok {
			current.stopSequence = sequence
		}
		current.usageFor(current.current).merge(event.Usage)
	case "message_stop":
		// A message that asks for the caller's tools ends the turn; the
		// process waits for their results. Any other stop waits for the
		// CLI's result, which may follow more messages.
		finish = current.stopReason == "tool_use" && current.currentTools > 0
	}
	writer := current.writer
	current.mu.Unlock()
	if head != nil {
		current.head <- head
	}
	current.emit(writer, out)
	if finish {
		current.finish(outcomeParked)
	}
}

func (current *turn) usageFor(id string) *messageUsage {
	usage := current.usage[id]
	if usage == nil {
		usage = &messageUsage{}
		current.usage[id] = usage
		current.order = append(current.order, id)
	}
	return usage
}

// totals sums the turn's model calls; each call counts its largest report.
func (current *turn) totals() map[string]any {
	var sum messageUsage
	for _, id := range current.order {
		usage := current.usage[id]
		sum.input += usage.input
		sum.cacheCreation += usage.cacheCreation
		sum.cacheRead += usage.cacheRead
		sum.output += usage.output
		sum.cache5m += usage.cache5m
		sum.cache1h += usage.cache1h
	}
	totals := map[string]any{
		"input_tokens": sum.input, "cache_creation_input_tokens": sum.cacheCreation,
		"cache_read_input_tokens": sum.cacheRead, "output_tokens": sum.output,
	}
	if sum.cache5m > 0 || sum.cache1h > 0 {
		totals["cache_creation"] = map[string]any{
			"ephemeral_5m_input_tokens": sum.cache5m, "ephemeral_1h_input_tokens": sum.cache1h,
		}
	}
	return totals
}

func (current *turn) streamResponse() *http.Response {
	reader, writer := io.Pipe()
	current.writer = writer
	return &http.Response{
		StatusCode: http.StatusOK, Status: "200 OK", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:  http.Header{"Content-Type": {"text/event-stream"}, "Cache-Control": {"no-cache"}},
		Request: current.request, Body: turnBody{PipeReader: reader, turn: current}, ContentLength: -1,
	}
}

func (current *turn) noteError(kind string) {
	current.mu.Lock()
	current.errorKind = kind
	current.mu.Unlock()
}

func (current *turn) noteRateLimit(raw json.RawMessage) {
	var info struct {
		Status   string      `json:"status"`
		ResetsAt json.Number `json:"resetsAt"`
	}
	if decodeNumbers(raw, &info) != nil || info.Status != "rejected" {
		return
	}
	seconds, err := info.ResetsAt.Int64()
	if err != nil || seconds <= 0 {
		return
	}
	current.mu.Lock()
	current.retryAt = time.Unix(seconds, 0)
	current.mu.Unlock()
}

func (current *turn) handleResult(item envelope) {
	if !item.IsError && item.Subtype == "success" {
		current.finish(outcomeIdle)
		return
	}
	current.mu.Lock()
	failure := current.resultFailure(item)
	current.mu.Unlock()
	current.fail(failure)
}

// resultFailure turns the CLI's failed result into the error the caller
// receives. Callers hold current.mu.
func (current *turn) resultFailure(item envelope) httpFailure {
	status := 0
	if item.APIErrorStatus != nil {
		status = *item.APIErrorStatus
	}
	if status < 400 {
		switch current.errorKind {
		case "rate_limit":
			status = http.StatusTooManyRequests
		case "authentication_failed":
			status = http.StatusUnauthorized
		case "billing_error", "invalid_request":
			status = http.StatusBadRequest
		case "server_error":
			status = http.StatusInternalServerError
		default:
			status = http.StatusBadGateway
		}
	}
	message := strings.TrimSpace(item.Result)
	if message == "" {
		message = "Claude Code ended the request with an error"
	}
	if len(message) > 500 {
		message = message[:500]
	}
	failure := newFailure(status, message)
	if status == http.StatusTooManyRequests && !current.retryAt.IsZero() {
		if wait := current.retryAt.Sub(current.now()); wait > 0 {
			failure.retryAfter = wait
		}
	}
	return failure
}

// signedOut reports whether the turn failed because the account is signed
// out.
func (current *turn) signedOut() bool {
	current.mu.Lock()
	defer current.mu.Unlock()
	return current.errorKind == "authentication_failed"
}

// finish ends the turn with what the model produced.
func (current *turn) finish(result outcome) {
	current.mu.Lock()
	if current.finished {
		current.mu.Unlock()
		return
	}
	current.finished = true
	var out []byte
	var head *http.Response
	if current.stream {
		if !current.started {
			head = current.streamResponse()
			out = sse("message_start", map[string]any{"type": "message_start", "message": current.messageBody(false)})
		}
		out = append(out, sse("message_delta", map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": current.stopReason, "stop_sequence": current.stopSequence},
			"usage": current.totals(),
		})...)
		out = append(out, sse("message_stop", map[string]any{"type": "message_stop"})...)
	} else {
		raw, _ := json.Marshal(current.messageBody(true))
		head = &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
			Header: http.Header{"Content-Type": {"application/json"}}, Request: current.request,
			Body: io.NopCloser(bytes.NewReader(raw)), ContentLength: int64(len(raw)),
		}
	}
	turnResult := turnResult{outcome: result, reply: current.reply()}
	if result == outcomeParked {
		for _, output := range current.blocks {
			if output.kind == "tool_use" {
				turnResult.pending = append(turnResult.pending, output.id)
			}
		}
	}
	writer := current.writer
	current.mu.Unlock()
	// Record the outcome before the caller sees the end: a caller that
	// answers a tool call at once must find the conversation parked.
	current.done(turnResult)
	if head != nil {
		current.head <- head
	}
	current.emit(writer, out)
	if writer != nil {
		_ = writer.Close()
	}
}

// messageBody is the merged message; full includes content for a
// non-streaming reply. Callers hold current.mu.
func (current *turn) messageBody(full bool) map[string]any {
	content := make([]block, 0, len(current.blocks))
	if full {
		for _, output := range current.blocks {
			content = append(content, output.content())
		}
	}
	body := map[string]any{
		"id": current.messageID, "type": "message", "role": "assistant", "model": current.model,
		"content": content, "stop_reason": nil, "stop_sequence": nil, "usage": current.startUsage,
	}
	if full {
		body["stop_reason"], body["stop_sequence"], body["usage"] = current.stopReason, current.stopSequence, current.totals()
	}
	if current.startUsage == nil {
		body["usage"] = current.totals()
	}
	return body
}

// reply is the assistant turn as the caller will send it back in its
// history. Callers hold current.mu.
func (current *turn) reply() group {
	reply := group{Role: "assistant"}
	for _, output := range current.blocks {
		reply.Blocks = append(reply.Blocks, output.content())
	}
	return reply
}

// fail ends the turn with an error. Before any output the caller receives it
// as an HTTP error; after, as a stream error event.
func (current *turn) fail(failure httpFailure) {
	current.mu.Lock()
	if current.finished {
		current.mu.Unlock()
		return
	}
	current.finished = true
	writer := current.writer
	streaming := current.stream && current.started
	current.mu.Unlock()
	if streaming {
		current.emit(writer, sse("error", map[string]any{
			"type": "error", "error": map[string]any{"type": failure.kind, "message": failure.message},
		}))
		_ = writer.Close()
	} else {
		current.head <- failure.response(current.request)
	}
	current.done(turnResult{outcome: outcomeFailed})
}

// abort ends a turn the caller left. The process is stopped: it cannot be
// told to forget what it was producing.
func (current *turn) abort() {
	current.mu.Lock()
	if current.finished {
		current.mu.Unlock()
		return
	}
	current.finished = true
	writer := current.writer
	current.mu.Unlock()
	if writer != nil {
		_ = writer.CloseWithError(io.ErrClosedPipe)
	}
	current.done(turnResult{outcome: outcomeFailed})
}
