package contract

import "time"

// RequestActivityCall is the span of one root model call. Retries stay inside
// their root's span, so the spans of a session tell when it was busy.
type RequestActivityCall struct {
	SessionID SessionID `json:"session_id"`
	// ClientType is the inbound client; absent on historical records.
	ClientType  ClientType    `json:"client_type,omitempty"`
	StartedAt   time.Time     `json:"started_at"`
	CompletedAt *time.Time    `json:"completed_at"`
	Status      RequestStatus `json:"status"`
}

// RequestActivity lists the model calls that overlap a recent window, for the
// live view of how many conversations run at once. Now is the gateway's clock,
// so a client charts the window against it instead of its own.
type RequestActivity struct {
	Now   time.Time             `json:"now"`
	Calls []RequestActivityCall `json:"calls"`
}
