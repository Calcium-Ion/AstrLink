package convo

import "encoding/json"

// Adapter teaches convo one protocol's request and response shapes. Adapters
// are stateless values; per-response state lives in the ResponseParser they
// create.
//
// Request-side methods receive raw JSON so adapters can tolerate unknown
// fields. Items are elements of the history array named by HistoryField.
type Adapter interface {
	// HistoryField is the top-level request field holding the conversation
	// history, for example "messages", "input", or "contents".
	HistoryField() string
	// ExplicitCursors returns conversation identifiers the request names on
	// purpose, most trusted first.
	ExplicitCursors(fields map[string]json.RawMessage) []string
	// IsStateful reports whether the request continues server-side state so
	// the history array contains only the newest turn.
	IsStateful(fields map[string]json.RawMessage) bool
	// IsUserTurn reports whether item is a genuine user message rather than a
	// tool result, harness note, or assistant/system content.
	IsUserTurn(item json.RawMessage) bool
	// InboundEchoIDs returns ids in item that an earlier response produced.
	InboundEchoIDs(item json.RawMessage) []string
	// AssistantText returns the visible assistant text of item and ok=true
	// when item is an assistant message; reasoning is excluded.
	AssistantText(item json.RawMessage) (text string, ok bool)
	// UserText returns the visible user text of item, or "" when item is not
	// a user message or carries no visible text.
	UserText(item json.RawMessage) string
	// NewResponseParser returns a parser for one response body.
	NewResponseParser(streaming bool) ResponseParser
}

// ResponseParser consumes decoded response events (one JSON document per SSE
// data line, or the whole body for non-streaming responses) and reports
// signals to the sink.
type ResponseParser interface {
	Observe(doc map[string]json.RawMessage, sink EventSink)
}

// EventSink receives response-side signals from a ResponseParser.
type EventSink interface {
	// OutputID reports the response's own identifier. Only the first
	// non-empty value is kept.
	OutputID(id string)
	// EchoID reports an id the client is expected to send back verbatim.
	EchoID(id string)
	// AssistantText appends visible assistant text in stream order.
	AssistantText(text string)
	// ResetAssistantText discards accumulated assistant text; adapters call
	// it when a new assistant message item begins so only the last one is
	// fingerprinted.
	ResetAssistantText()
}
