package contract

import "time"

// RequestActivityCall is the span of one root model call. Retries stay inside
// their root's span, so the spans of a session tell when it was busy.
type RequestActivityCall struct {
	SessionID SessionID `json:"session_id"`
	// ParentSessionID is set on a subagent's calls: the conversation that
	// started it.
	ParentSessionID *SessionID `json:"parent_session_id,omitempty"`
	// ClientType is the inbound client; absent on historical records.
	ClientType  ClientType    `json:"client_type,omitempty"`
	StartedAt   time.Time     `json:"started_at"`
	CompletedAt *time.Time    `json:"completed_at"`
	Status      RequestStatus `json:"status"`
	// Output is what the call produced, as on its record.
	Output *OutputActivity `json:"output,omitempty"`
	// OutputTokens is what the upstream reported the call generated,
	// reasoning included. Absent while the call runs, when no usage came back,
	// and when the stream stopped before its usage was final.
	OutputTokens int `json:"output_tokens,omitempty"`
}

// RequestActivity lists the model calls that overlap a recent window, for the
// live view of how many conversations run at once and how many tokens the
// models generate. Now is the gateway's clock, so a client charts the window
// against it instead of its own.
type RequestActivity struct {
	Now   time.Time             `json:"now"`
	Calls []RequestActivityCall `json:"calls"`
}
