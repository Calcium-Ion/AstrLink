package ingress

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/tidwall/gjson"
)

// An upstream can accept a request with HTTP 200 and then refuse it in the
// stream, as the Codex backend does with server_is_overloaded. Holding back
// the events that only announce the response lets that refusal go to the
// failure policy like the HTTP error it stands for, before the client has
// received a byte.
const (
	// An error can be followed by a recovered response. Bound that wait if
	// the upstream never sends another event or closes its stream.
	streamStartErrorWaitLimit = 10 * time.Second
	// streamStartHoldBytes bounds the announcements held in memory.
	streamStartHoldBytes = 1 << 20
	// An explicit capacity refusal cools only this model route. Retry-After
	// can extend it; another model on the same account remains available.
	streamOverloadCooldown = 30 * time.Second
)

// holdsStreamStart reports whether a stream in this upstream protocol tells
// its announcements and refusals apart.
func holdsStreamStart(protocol contract.ProtocolID) bool {
	return protocol == contract.ProtocolOpenAIResponses || protocol == contract.ProtocolAnthropicMessages
}

// holdsStreamResponse reports whether a response is an event stream whose
// start can be read: successful and not compressed.
func holdsStreamResponse(status int, header http.Header) bool {
	encoding := strings.TrimSpace(header.Get("Content-Encoding"))
	return status >= 200 && status < 300 &&
		(encoding == "" || strings.EqualFold(encoding, "identity")) &&
		strings.HasPrefix(strings.ToLower(header.Get("Content-Type")), "text/event-stream")
}

type streamStartEvent uint8

const (
	// streamStartAnnouncement only announces the response: response.created,
	// message_start, a ping or an SSE comment.
	streamStartAnnouncement streamStartEvent = iota
	// streamStartError reports an error the stream may still go on after.
	streamStartError
	// streamStartFailed ends the response as failed.
	streamStartFailed
	// streamStartProgress is anything else; the response is under way.
	streamStartProgress
)

func classifyStreamStart(protocol contract.ProtocolID, frame []byte) streamStartEvent {
	name, data := parseSSEFrame(frame)
	if len(data) == 0 {
		return streamStartAnnouncement
	}
	eventType := gjson.GetBytes(data, "type").Str
	if eventType == "" {
		eventType = name
	}
	switch streamEventOutcome(protocol, eventType) {
	case streamOutcomeError:
		return streamStartError
	case streamOutcomeFailed:
		return streamStartFailed
	}
	switch protocol {
	case contract.ProtocolOpenAIResponses:
		switch eventType {
		case "response.created", "response.in_progress", "response.queued", "response.metadata":
			return streamStartAnnouncement
		case "response.output_item.added":
			item := gjson.GetBytes(data, "item")
			switch item.Get("type").Str {
			case "message", "reasoning":
				// Empty containers can precede an overload, even though their
				// names mention output. Populated or encrypted content commits.
				encrypted := item.Get("encrypted_content")
				if emptyStreamStartArray(item.Get("content")) && emptyStreamStartArray(item.Get("summary")) &&
					(encrypted.Type == gjson.Null || encrypted.Type == gjson.String && encrypted.Str == "") {
					return streamStartAnnouncement
				}
			}
		case "response.content_part.added", "response.reasoning_summary_part.added":
			part := gjson.GetBytes(data, "part")
			field := "text"
			switch part.Get("type").Str {
			case "refusal":
				field = "refusal"
			case "output_text", "summary_text":
			default:
				return streamStartProgress
			}
			if value := part.Get(field); value.Type == gjson.String && value.Str == "" {
				return streamStartAnnouncement
			}
		case "response.output_text.delta", "response.refusal.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			if delta := gjson.GetBytes(data, "delta"); delta.Type == gjson.String && delta.Str == "" {
				return streamStartAnnouncement
			}
		}
	case contract.ProtocolAnthropicMessages:
		switch eventType {
		case "message_start", "ping":
			return streamStartAnnouncement
		}
	}
	return streamStartProgress
}

func emptyStreamStartArray(value gjson.Result) bool {
	return value.Type == gjson.Null || value.IsArray() && len(value.Array()) == 0
}

// streamOutcome is what one event says about how a stream ends.
type streamOutcome uint8

const (
	streamOutcomeNone streamOutcome = iota
	// streamOutcomeError reports an error. The stream fails unless a
	// finished response follows; the Codex backend can still complete.
	streamOutcomeError
	// streamOutcomeFailed ends the stream as failed.
	streamOutcomeFailed
	// streamOutcomeFinished ends the stream with a response.
	streamOutcomeFinished
)

func streamEventOutcome(protocol contract.ProtocolID, eventType string) streamOutcome {
	switch protocol {
	case contract.ProtocolOpenAIResponses:
		switch eventType {
		case "error":
			return streamOutcomeError
		case "response.failed":
			return streamOutcomeFailed
		case "response.completed", "response.incomplete":
			return streamOutcomeFinished
		}
	case contract.ProtocolAnthropicMessages:
		switch eventType {
		case "error":
			return streamOutcomeError
		case "message_stop":
			return streamOutcomeFinished
		}
	}
	return streamOutcomeNone
}

// streamFailure is an error an upstream sent inside a successful stream.
type streamFailure struct {
	protocol contract.ProtocolID
	// event is the data of the first failure event.
	event []byte
	// status is the HTTP status the error stands for.
	status int
	// held counts the bytes held back.
	held int
}

func newStreamFailure(protocol contract.ProtocolID, event []byte) *streamFailure {
	event = bytes.Clone(event)
	return &streamFailure{protocol: protocol, event: event, status: streamFailureStatus(protocol, event)}
}

func (failure *streamFailure) summary() contract.ErrorSummary {
	return streamFailureSummary(failure.protocol, failure.event)
}

// streamFailureStatus maps a failure event to the HTTP status the upstream
// answers the same error with outside a stream, so one set of failure rules
// covers both.
func streamFailureStatus(protocol contract.ProtocolID, event []byte) int {
	errType, code, _ := streamFailureFields(event)
	if protocol == contract.ProtocolAnthropicMessages {
		return anthropicErrorStatus(errType)
	}
	return responsesFailureStatus(code, errType)
}

// responsesFailureStatus maps an OpenAI or Codex error code, or its type when
// the code is unknown, to an HTTP status. Other failures are the upstream's
// own, so they count as a bad gateway.
func responsesFailureStatus(code, errType string) int {
	switch code {
	case "server_is_overloaded", "slow_down":
		return http.StatusServiceUnavailable
	case "rate_limit_exceeded", "usage_limit_reached", "usage_not_included", "insufficient_quota":
		return http.StatusTooManyRequests
	case "context_length_exceeded", "invalid_prompt", "invalid_request_error", "invalid_value", "bad_request", "cyber_policy":
		return http.StatusBadRequest
	}
	switch errType {
	case "service_unavailable_error":
		return http.StatusServiceUnavailable
	case "invalid_request_error":
		return http.StatusBadRequest
	}
	return http.StatusBadGateway
}

// Only explicit capacity errors establish an overload cooldown. Generic
// server errors and connection failures do not withhold otherwise usable routes.
func streamFailureOverloaded(protocol contract.ProtocolID, event []byte) bool {
	errType, code, _ := streamFailureFields(event)
	if protocol == contract.ProtocolAnthropicMessages {
		return errType == "overloaded_error"
	}
	return protocol == contract.ProtocolOpenAIResponses &&
		(code == "server_is_overloaded" || code == "slow_down" || errType == "service_unavailable_error")
}

// anthropicErrorStatus follows Anthropic's documented error types.
func anthropicErrorStatus(errType string) int {
	switch errType {
	case "invalid_request_error":
		return http.StatusBadRequest
	case "authentication_error":
		return http.StatusUnauthorized
	case "billing_error":
		return http.StatusPaymentRequired
	case "permission_error":
		return http.StatusForbidden
	case "not_found_error":
		return http.StatusNotFound
	case "request_too_large":
		return http.StatusRequestEntityTooLarge
	case "rate_limit_error":
		return http.StatusTooManyRequests
	case "api_error":
		return http.StatusInternalServerError
	case "timeout_error":
		return http.StatusGatewayTimeout
	case "overloaded_error":
		return 529
	default:
		return http.StatusBadGateway
	}
}

// streamFailureFields reads the error of a failure event: nested under
// response.error or error, or flat in OpenAI's error event.
func streamFailureFields(event []byte) (errType, code, message string) {
	document := gjson.ParseBytes(event)
	problem := document.Get("response.error")
	if !problem.IsObject() {
		problem = document.Get("error")
	}
	if problem.IsObject() {
		return problem.Get("type").String(), problem.Get("code").String(), problem.Get("message").String()
	}
	return "", document.Get("code").String(), document.Get("message").String()
}

// streamFailureSummary describes a failure event in the provider's own words.
// The upstream answered HTTP 200, so the summary keeps no upstream HTTP error.
func streamFailureSummary(protocol contract.ProtocolID, event []byte) contract.ErrorSummary {
	status := streamFailureStatus(protocol, event)
	summary := errorSummaryFromInference(
		"upstream_stream_failed",
		"upstream reported an error in its stream",
		status == http.StatusTooManyRequests || status >= http.StatusInternalServerError,
	)
	errType, code, message := streamFailureFields(event)
	label := errType
	if label == "" {
		label = code
	}
	if message = strings.TrimSpace(message); message != "" {
		if label != "" && !strings.Contains(message, label) {
			message = label + ": " + message
		}
		summary.Message = contract.ClampRunes(message, 1024)
	}
	return summary
}

// holdStreamStart reads an event stream until it shows whether the upstream
// accepted the request: an event that does more than announce the response
// means it did. An error event means it did not once the stream fails or ends
// after it. holdStreamStart returns a body that replays what it read and then
// the rest of the stream, the failure, and the read error when the stream
// broke before either. Announcements remain held until output, failure,
// cancellation, EOF or the memory bound; elapsed time alone must not commit
// a response that has generated nothing.
func holdStreamStart(ctx context.Context, body io.ReadCloser, protocol contract.ProtocolID) (io.ReadCloser, *streamFailure, error) {
	held := &heldStream{ReadCloser: body}
	var errorTimer *time.Timer
	var errorTimeout <-chan time.Time
	defer func() {
		if errorTimer != nil {
			errorTimer.Stop()
		}
	}()
	var failure *streamFailure
	refused := func() (io.ReadCloser, *streamFailure, error) {
		failure.held = len(held.held)
		return held, failure, nil
	}
	var unread []byte // bytes after the last whole event, with LF line ends
	for len(held.held) <= streamStartHoldBytes {
		if err := context.Cause(ctx); err != nil {
			return held, nil, err
		}
		// The read runs aside so cancellation can end the hold. Only that
		// goroutine touches body until it delivers.
		reads := make(chan heldRead, 1)
		go func() {
			chunk := make([]byte, 32<<10)
			n, err := body.Read(chunk)
			reads <- heldRead{data: chunk[:n], err: err}
		}()
		var read heldRead
		select {
		case read = <-reads:
		case <-ctx.Done():
			held.pending = reads
			return held, nil, context.Cause(ctx)
		case <-errorTimeout:
			held.pending = reads
			return refused()
		}
		held.held = append(held.held, read.data...)
		held.err = read.err
		unread = bytes.ReplaceAll(append(unread, read.data...), []byte("\r\n"), []byte("\n"))
		frames := bytes.Split(unread, []byte("\n\n"))
		unread = frames[len(frames)-1]
		frames = frames[:len(frames)-1]
		if read.err == io.EOF {
			// The last event may lack its closing blank line.
			frames = append(frames, unread)
		}
		for _, frame := range frames {
			class := classifyStreamStart(protocol, frame)
			if class == streamStartProgress {
				// Even after an error event, output means the upstream went on.
				return held, nil, nil
			}
			if class != streamStartAnnouncement && failure == nil {
				_, event := parseSSEFrame(frame)
				failure = newStreamFailure(protocol, event)
				errorTimer = time.NewTimer(streamStartErrorWaitLimit)
				errorTimeout = errorTimer.C
			}
			if class == streamStartFailed {
				return refused()
			}
		}
		switch {
		case read.err != nil && failure != nil:
			// The stream ended on its error.
			return refused()
		case read.err == io.EOF:
			// A stream that ended this early goes out as it is.
			return held, nil, nil
		case read.err != nil:
			return held, nil, read.err
		}
	}
	if failure != nil {
		return refused()
	}
	return held, nil, nil
}

type heldRead struct {
	data []byte
	err  error
}

// heldStream replays the bytes held at the start of a stream, then reads on.
type heldStream struct {
	io.ReadCloser
	held []byte
	// pending is a read still running when the hold ended.
	pending chan heldRead
	// err ends the stream after the held bytes.
	err error
}

func (stream *heldStream) Read(buffer []byte) (int, error) {
	if len(stream.held) == 0 && stream.pending != nil {
		read := <-stream.pending
		stream.pending = nil
		stream.held, stream.err = read.data, read.err
	}
	if len(stream.held) > 0 {
		n := copy(buffer, stream.held)
		stream.held = stream.held[n:]
		return n, nil
	}
	if stream.err != nil {
		return 0, stream.err
	}
	return stream.ReadCloser.Read(buffer)
}
