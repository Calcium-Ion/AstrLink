package convo

import (
	"bytes"
	"encoding/json"

	"github.com/QuantumNous/astrlink/convo/internal/jsonx"
)

// ResponseSummary is what one response contributed to session identity.
type ResponseSummary struct {
	// OutputID is the response's own id (chatcmpl-*, resp_*, msg_*, Gemini
	// responseId). It is stored as an explicit outbound cursor so a later
	// previous_response_id can find it.
	OutputID string
	// EchoIDs are ids the client must send back verbatim, in stream order,
	// deduplicated, entropy-checked, and bounded by Policy.MaxOutputEchoIDs.
	EchoIDs []string
	// AssistantDigest is the normalized digest of the visible assistant text
	// of the last assistant message, or nil when too short.
	AssistantDigest []byte
	// Truncated is true when Write gave up because the body or a single
	// event exceeded Policy.MaxResponseBytes; the summary may be incomplete.
	Truncated bool
}

// ResponseObserver accumulates response-side signals. Feed it either decoded
// events through ObserveEvent (preferred when the host already parses the
// stream) or raw bytes through Write, which frames SSE data lines for
// streaming responses and buffers whole bodies otherwise. Do not mix the two.
//
// The observer keeps no text: assistant output goes straight into a
// TextNormalizer hash.
type ResponseObserver struct {
	policy    Policy
	parser    ResponseParser
	streaming bool

	outputID   string
	echoIDs    []string
	echoSeen   map[string]struct{}
	normalizer *TextNormalizer

	carry     []byte
	body      bytes.Buffer
	truncated bool
	consumed  bool
}

// NewResponseObserver returns an observer with DefaultPolicy, or nil when the
// protocol has no adapter. A nil observer is safe to call.
func NewResponseObserver(protocol Protocol, streaming bool) *ResponseObserver {
	return DefaultPolicy().NewResponseObserver(protocol, streaming)
}

// NewResponseObserver returns an observer for protocol, or nil when the
// protocol has no adapter.
func (policy Policy) NewResponseObserver(protocol Protocol, streaming bool) *ResponseObserver {
	adapter, ok := policy.adapterFor(protocol)
	if !ok {
		return nil
	}
	return &ResponseObserver{
		policy:     policy,
		parser:     adapter.NewResponseParser(streaming),
		streaming:  streaming,
		echoSeen:   make(map[string]struct{}),
		normalizer: NewTextNormalizer(policy.StripLeadingThink),
	}
}

// ObserveEvent feeds one decoded event: an SSE data payload for streaming
// responses or the whole body for non-streaming ones.
func (observer *ResponseObserver) ObserveEvent(doc map[string]json.RawMessage) {
	if observer == nil || doc == nil {
		return
	}
	observer.parser.Observe(doc, observer)
}

// Write feeds raw response bytes. It always reports the full length as
// written so it can sit inside an io.MultiWriter without disturbing the
// client copy.
func (observer *ResponseObserver) Write(chunk []byte) (int, error) {
	if observer == nil || observer.truncated {
		return len(chunk), nil
	}
	if !observer.streaming {
		if observer.body.Len()+len(chunk) > observer.policy.maxResponseBytes() {
			observer.truncated = true
			observer.body.Reset()
			return len(chunk), nil
		}
		observer.body.Write(chunk)
		return len(chunk), nil
	}
	data := chunk
	if len(observer.carry) > 0 {
		data = append(observer.carry, chunk...)
		observer.carry = nil
	}
	for {
		newline := bytes.IndexByte(data, '\n')
		if newline < 0 {
			break
		}
		observer.consumeLine(data[:newline])
		data = data[newline+1:]
	}
	if len(data) > observer.policy.maxResponseBytes() {
		observer.truncated = true
		return len(chunk), nil
	}
	if len(data) > 0 {
		observer.carry = append(observer.carry[:0], data...)
	}
	return len(chunk), nil
}

func (observer *ResponseObserver) consumeLine(line []byte) {
	line = bytes.TrimRight(line, "\r")
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	payload := bytes.TrimSpace(line[len("data:"):])
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return
	}
	if doc, ok := jsonx.Object(payload); ok {
		observer.parser.Observe(doc, observer)
	}
}

// flush parses what Write buffered. It runs once, from Summary.
func (observer *ResponseObserver) flush() {
	if observer.consumed {
		return
	}
	observer.consumed = true
	if observer.streaming {
		if len(observer.carry) > 0 {
			observer.consumeLine(observer.carry)
			observer.carry = nil
		}
		return
	}
	if observer.body.Len() == 0 {
		return
	}
	if doc, ok := jsonx.Object(observer.body.Bytes()); ok {
		observer.parser.Observe(doc, observer)
	}
	observer.body.Reset()
}

// Summary returns what has been observed so far. After Summary, further Write
// calls of a non-streaming body are ignored; ObserveEvent keeps working.
func (observer *ResponseObserver) Summary() ResponseSummary {
	if observer == nil {
		return ResponseSummary{}
	}
	observer.flush()
	summary := ResponseSummary{OutputID: observer.outputID, Truncated: observer.truncated}
	if len(observer.echoIDs) > 0 {
		summary.EchoIDs = append([]string(nil), observer.echoIDs...)
	}
	if observer.normalizer.Runes() >= observer.policy.minFingerprintRunes() {
		summary.AssistantDigest = observer.normalizer.Digest()
	}
	return summary
}

// OutputID implements EventSink.
func (observer *ResponseObserver) OutputID(id string) {
	if observer.outputID != "" {
		return
	}
	observer.outputID = clampCursorValue(id)
}

// EchoID implements EventSink.
func (observer *ResponseObserver) EchoID(id string) {
	value := clampCursorValue(id)
	if value == "" || !observer.policy.entropyCheck()(value) {
		return
	}
	if _, dup := observer.echoSeen[value]; dup {
		return
	}
	if len(observer.echoIDs) >= observer.policy.maxOutputEchoIDs() {
		return
	}
	observer.echoSeen[value] = struct{}{}
	observer.echoIDs = append(observer.echoIDs, value)
}

// AssistantText implements EventSink.
func (observer *ResponseObserver) AssistantText(text string) {
	observer.normalizer.WriteString(text)
}

// ResetAssistantText implements EventSink.
func (observer *ResponseObserver) ResetAssistantText() {
	observer.normalizer.Reset()
}
