package contract

import (
	"fmt"
	"math"
	"regexp"
	"time"
	"unicode/utf8"
)

type RequestID string

func (id RequestID) Validate() error {
	return validateResourceID("request", string(id))
}

type SessionID string

func (id SessionID) Validate() error {
	return validateResourceID("session", string(id))
}

type RequestEventKind string

const (
	RequestEventAccepted      RequestEventKind = "accepted"
	RequestEventModelRedirect RequestEventKind = "model_redirect"
	RequestEventPrivacy       RequestEventKind = "privacy"
	RequestEventRouted        RequestEventKind = "routed"
	RequestEventUpstream      RequestEventKind = "upstream"
	RequestEventRestore       RequestEventKind = "restore"
	RequestEventCompleted     RequestEventKind = "completed"
)

func (kind RequestEventKind) Valid() bool {
	switch kind {
	case RequestEventAccepted, RequestEventModelRedirect, RequestEventPrivacy, RequestEventRouted,
		RequestEventUpstream, RequestEventRestore, RequestEventCompleted:
		return true
	default:
		return false
	}
}

const (
	MaxInputPreviewRunes   = 80
	MaxEventSummaryRunes   = 256
	MaxProtocolCursorRunes = 256
	MaxSessionTitleRunes   = 80
)

type RequestEvent struct {
	Kind         RequestEventKind `json:"kind"`
	StartedAt    time.Time        `json:"started_at"`
	EndedAt      *time.Time       `json:"ended_at"`
	Status       RequestStatus    `json:"status"`
	Summary      string           `json:"summary"`
	AttemptIndex int              `json:"attempt_index"`
}

func (event RequestEvent) Validate() error {
	if !event.Kind.Valid() {
		return fmt.Errorf("unknown request event kind %q", event.Kind)
	}
	if event.StartedAt.IsZero() {
		return fmt.Errorf("started_at is required")
	}
	if !event.Status.Valid() {
		return fmt.Errorf("unknown request status %q", event.Status)
	}
	if err := validateBoundedText("summary", event.Summary, MaxEventSummaryRunes, true); err != nil {
		return err
	}
	if event.AttemptIndex < 0 {
		return fmt.Errorf("attempt_index must be non-negative")
	}
	return nil
}

type RequestStatus string

const (
	RequestStatusPending   RequestStatus = "pending"
	RequestStatusSucceeded RequestStatus = "succeeded"
	RequestStatusFailed    RequestStatus = "failed"
	RequestStatusCancelled RequestStatus = "cancelled"
	RequestStatusBlocked   RequestStatus = "blocked"
)

func (status RequestStatus) Valid() bool {
	switch status {
	case RequestStatusPending, RequestStatusSucceeded, RequestStatusFailed,
		RequestStatusCancelled, RequestStatusBlocked:
		return true
	default:
		return false
	}
}

// SessionStatus is the outcome of a whole conversation. It carries every
// record status plus interrupted, which no single record can hold: the
// conversation produced answers and then the client abandoned the last
// stream. Keeping it off RequestStatus means a record or a phase event can
// never claim an outcome that only exists once calls are aggregated.
type SessionStatus string

const (
	SessionStatusPending     SessionStatus = "pending"
	SessionStatusSucceeded   SessionStatus = "succeeded"
	SessionStatusFailed      SessionStatus = "failed"
	SessionStatusCancelled   SessionStatus = "cancelled"
	SessionStatusBlocked     SessionStatus = "blocked"
	SessionStatusInterrupted SessionStatus = "interrupted"
)

func (status SessionStatus) Valid() bool {
	switch status {
	case SessionStatusPending, SessionStatusSucceeded, SessionStatusFailed,
		SessionStatusCancelled, SessionStatusBlocked, SessionStatusInterrupted:
		return true
	default:
		return false
	}
}

// SessionStatusFromRequest lifts one record outcome onto the session scale.
func SessionStatusFromRequest(status RequestStatus) SessionStatus {
	return SessionStatus(status)
}

// Usage uses OpenAI-style input accounting: input_tokens includes all
// prompt-side tokens (uncached, cache read, cache write/creation, and
// multimodal input such as images). cache_read_tokens / cache_write_tokens
// are subsets of that input when the upstream reports them.
type Usage struct {
	CacheWrite1hTokens *int  `json:"cache_write_1h_tokens,omitempty"`
	InputAudioTokens   *int  `json:"input_audio_tokens,omitempty"`
	OutputAudioTokens  *int  `json:"output_audio_tokens,omitempty"`
	ThinkingEnabled    *bool `json:"thinking_enabled,omitempty"`
	BillingIncomplete  bool  `json:"billing_incomplete,omitempty"`
	InputTokens        int   `json:"input_tokens"`
	OutputTokens       int   `json:"output_tokens"`
	TotalTokens        int   `json:"total_tokens"`
	CacheReadTokens    *int  `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens   *int  `json:"cache_write_tokens,omitempty"`
}

func (usage Usage) Validate() error {
	for _, count := range []*int{usage.CacheWrite1hTokens, usage.InputAudioTokens, usage.OutputAudioTokens} {
		if count != nil && *count < 0 {
			return fmt.Errorf("billing token counts must be non-negative")
		}
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.TotalTokens < 0 {
		return fmt.Errorf("usage token counts must be non-negative")
	}
	if usage.CacheReadTokens != nil && *usage.CacheReadTokens < 0 {
		return fmt.Errorf("cache_read_tokens must be non-negative")
	}
	if usage.CacheWriteTokens != nil && *usage.CacheWriteTokens < 0 {
		return fmt.Errorf("cache_write_tokens must be non-negative")
	}
	return nil
}

type ErrorSummary struct {
	Category  string `json:"category"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

var errorSummaryTokenPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func (summary ErrorSummary) Validate() error {
	if !errorSummaryTokenPattern.MatchString(summary.Category) ||
		utf8.RuneCountInString(summary.Category) > 64 {
		return fmt.Errorf("error category is invalid")
	}
	if !errorSummaryTokenPattern.MatchString(summary.Code) ||
		utf8.RuneCountInString(summary.Code) > 96 {
		return fmt.Errorf("error code is invalid")
	}
	if summary.Message == "" || utf8.RuneCountInString(summary.Message) > 1024 {
		return fmt.Errorf("error message must contain 1 to 1024 characters")
	}
	return nil
}

type AuditRecordSummary struct {
	RequestBodyCaptured              bool `json:"request_body_captured"`
	ResponseContentCaptured          bool `json:"response_content_captured"`
	RequestBodyTruncated             bool `json:"request_body_truncated"`
	ResponseContentTruncated         bool `json:"response_content_truncated"`
	UpstreamRequestBodyCaptured      bool `json:"upstream_request_body_captured"`
	UpstreamResponseContentCaptured  bool `json:"upstream_response_content_captured"`
	UpstreamRequestBodyTruncated     bool `json:"upstream_request_body_truncated"`
	UpstreamResponseContentTruncated bool `json:"upstream_response_content_truncated"`
}

// PrivacyHitCount is a request-time snapshot of how many unique placeholders
// a canonical kind produced. It never contains placeholders or originals.
type PrivacyHitCount struct {
	Kind  CanonicalKind `json:"kind"`
	Count int           `json:"count"`
}

// PrivacyRestoreSummary contains bounded, non-sensitive diagnostics for the
// request-scoped response placeholder mapping. Hits are optional kind counts
// recorded when redaction ran. The summary never contains placeholders or
// original values.
//
// RestoredCount is the total across both channels. VisibleRestoredCount and
// ToolArgumentRestoredCount split it, because a placeholder reaching a tool
// argument means the local agent was about to act on it, which is a materially
// different event from the model merely quoting it back to the reader.
type PrivacyRestoreSummary struct {
	Enabled                   bool              `json:"enabled"`
	MappingCount              int               `json:"mapping_count"`
	RestoredCount             int               `json:"restored_count"`
	VisibleRestoredCount      int               `json:"visible_restored_count"`
	ToolArgumentRestoredCount int               `json:"tool_argument_restored_count"`
	FallbackCount             int               `json:"fallback_count"`
	Hits                      []PrivacyHitCount `json:"hits,omitempty"`
}

func (summary PrivacyRestoreSummary) Validate() error {
	if summary.MappingCount < 0 ||
		summary.RestoredCount < 0 ||
		summary.VisibleRestoredCount < 0 ||
		summary.ToolArgumentRestoredCount < 0 ||
		summary.FallbackCount < 0 {
		return fmt.Errorf("privacy restore counts must be non-negative")
	}
	// The per-channel counts are bounded rather than required to sum exactly:
	// records written before the split decode with both channels at zero, and
	// rejecting those would make every historical row unreadable.
	if summary.VisibleRestoredCount > summary.RestoredCount ||
		summary.ToolArgumentRestoredCount > summary.RestoredCount ||
		summary.VisibleRestoredCount+summary.ToolArgumentRestoredCount > summary.RestoredCount {
		return fmt.Errorf("privacy restore channel counts must not exceed restored_count")
	}
	seen := make(map[CanonicalKind]struct{}, len(summary.Hits))
	for _, hit := range summary.Hits {
		if !hit.Kind.Valid() {
			return fmt.Errorf("privacy restore hit kind is invalid")
		}
		if hit.Count < 1 {
			return fmt.Errorf("privacy restore hit counts must be positive")
		}
		if _, exists := seen[hit.Kind]; exists {
			return fmt.Errorf("privacy restore hit kinds must be unique")
		}
		seen[hit.Kind] = struct{}{}
	}
	return nil
}

// NotCapturedAuditSummary reports that neither body direction was captured.
func NotCapturedAuditSummary() AuditRecordSummary {
	return AuditRecordSummary{}
}

// SessionCursorKind classifies how a session cursor value came to exist. The
// values mirror the convo module's Kind but are declared here so the public
// contract stays self-contained.
type SessionCursorKind string

const (
	// SessionCursorExplicit is an identifier the client sent on purpose
	// (previous_response_id, conversation ids, prompt_cache_key, Claude Code
	// session, Anthropic container, Gemini cachedContent) or the response's
	// own id. Matched globally.
	SessionCursorExplicit SessionCursorKind = "explicit"
	// SessionCursorEchoID is an opaque id the model produced and the client
	// echoed back verbatim (tool call ids, Responses item ids, Gemini thought
	// signatures). Matched within the same access token and a time window.
	SessionCursorEchoID SessionCursorKind = "echo_id"
	// SessionCursorFingerprint is a keyed digest of the last assistant text.
	// The stored value cannot be reversed to the text. Matched like echo ids.
	SessionCursorFingerprint SessionCursorKind = "fingerprint"
)

func (kind SessionCursorKind) Valid() bool {
	switch kind {
	case SessionCursorExplicit, SessionCursorEchoID, SessionCursorFingerprint:
		return true
	default:
		return false
	}
}

// SessionCursorDirection tells whether the cursor arrived with the request
// (in) or was produced by the response (out).
type SessionCursorDirection string

const (
	SessionCursorIn  SessionCursorDirection = "in"
	SessionCursorOut SessionCursorDirection = "out"
)

func (direction SessionCursorDirection) Valid() bool {
	return direction == SessionCursorIn || direction == SessionCursorOut
}

// MaxSessionCursors bounds the cursors stored per record.
const MaxSessionCursors = 48

// SessionCursor is one typed value that can link this record to others.
type SessionCursor struct {
	Kind      SessionCursorKind      `json:"kind"`
	Direction SessionCursorDirection `json:"direction"`
	Value     string                 `json:"value"`
}

func (cursor SessionCursor) Validate() error {
	if !cursor.Kind.Valid() {
		return fmt.Errorf("unknown session cursor kind %q", cursor.Kind)
	}
	if !cursor.Direction.Valid() {
		return fmt.Errorf("unknown session cursor direction %q", cursor.Direction)
	}
	return validateProtocolCursor("cursor value", cursor.Value)
}

// SessionLink records how a request was attached to an existing session:
// which cursor kind matched and the matching value. Null for the first
// request of a session and for records that started their own session.
type SessionLink struct {
	Kind  SessionCursorKind `json:"kind"`
	Value string            `json:"value"`
}

func (link SessionLink) Validate() error {
	if !link.Kind.Valid() {
		return fmt.Errorf("unknown session link kind %q", link.Kind)
	}
	return validateProtocolCursor("session_link value", link.Value)
}

type RequestRecord struct {
	// ClientType is absent on historical records.
	ClientType      ClientType       `json:"client_type,omitempty"`
	Recovery        *RequestRecovery `json:"recovery,omitempty"`
	ID              RequestID        `json:"id"`
	ParentRequestID *RequestID       `json:"parent_request_id"`
	AttemptIndex    int              `json:"attempt_index"`
	ChildCount      int              `json:"child_count"`
	StartedAt       time.Time        `json:"started_at"`
	CompletedAt     *time.Time       `json:"completed_at"`
	Status          RequestStatus    `json:"status"`
	InputProtocol   ProtocolID       `json:"input_protocol"`
	RequestedModel  *string          `json:"requested_model"`
	// ModelRedirect is set when a routing-settings redirect replaced
	// RequestedModel for provider selection.
	ModelRedirect *RequestModelRedirect `json:"model_redirect,omitempty"`
	// ReasoningEffort is the explicitly requested level; nil means unspecified or historical.
	ReasoningEffort *string    `json:"reasoning_effort"`
	Streaming       bool       `json:"streaming"`
	RouteID         *RouteID   `json:"route_id"`
	ServiceID       *ServiceID `json:"service_id"`
	// RoutingDecision explains why routing chose ServiceID, or why no
	// provider was eligible; nil on historical records.
	RoutingDecision    *RequestRoutingDecision `json:"routing_decision,omitempty"`
	LocalAccessTokenID *AccessTokenID          `json:"local_access_token_id"`
	Plan               *ExecutionPlan          `json:"plan"`
	HTTPStatus         *int                    `json:"http_status"`
	LatencyMs          *int                    `json:"latency_ms"`
	// FirstTokenMs measures upstream send to first generated stream content
	// (text, reasoning, or tool call). Nil for non-streaming and historical calls.
	FirstTokenMs       *int                   `json:"first_token_ms"`
	Usage              *Usage                 `json:"usage"`
	Error              *ErrorSummary          `json:"error"`
	Audit              AuditRecordSummary     `json:"audit"`
	PrivacyRestore     *PrivacyRestoreSummary `json:"privacy_restore"`
	SessionID          *SessionID             `json:"session_id"`
	PreviousResponseID *string                `json:"previous_response_id"`
	OutputResponseID   *string                `json:"output_response_id"`
	InputPreview       *string                `json:"input_preview"`
	// TurnIndex is the 1-based user turn within the session. Every model call
	// of one agent loop shares the same value. Null when the protocol has no
	// user turns or on legacy rows.
	TurnIndex *int `json:"turn_index"`
	// TurnUserMessages and TurnUserFingerprint are what the next request in
	// the session compares itself against to decide whether it starts a new
	// turn: the number of user messages this request's history held and the
	// keyed fingerprint of its newest user text (null without a fingerprint
	// key). Set together with TurnIndex; the fingerprint may be null.
	TurnUserMessages    *int    `json:"turn_user_messages"`
	TurnUserFingerprint *string `json:"turn_user_fingerprint"`
	// SessionLink tells how this record joined its session; null when it
	// started the session.
	SessionLink *SessionLink `json:"session_link"`
	// Cursors are the typed values stored for this record: explicit cursors
	// the request named plus everything the response produced.
	Cursors    []SessionCursor `json:"cursors"`
	Events     []RequestEvent  `json:"events"`
	Extensions map[string]any  `json:"extensions,omitempty"`
}

func (record RequestRecord) Validate() error {
	if record.ClientType != "" && !record.ClientType.Valid() {
		return fmt.Errorf("unknown client type %q", record.ClientType)
	}
	if record.Recovery != nil {
		if err := record.Recovery.Validate(); err != nil {
			return err
		}
	}
	if err := record.ID.Validate(); err != nil {
		return err
	}
	if record.ParentRequestID != nil {
		if err := record.ParentRequestID.Validate(); err != nil {
			return fmt.Errorf("parent_request_id: %w", err)
		}
		if *record.ParentRequestID == record.ID {
			return fmt.Errorf("parent_request_id must not equal id")
		}
	}
	if record.AttemptIndex < 0 {
		return fmt.Errorf("attempt_index must be non-negative")
	}
	if record.ChildCount < 0 {
		return fmt.Errorf("child_count must be non-negative")
	}
	if record.ParentRequestID != nil && record.ChildCount != 0 {
		return fmt.Errorf("child records must have child_count 0")
	}
	if record.StartedAt.IsZero() {
		return fmt.Errorf("started_at is required")
	}
	if !record.Status.Valid() {
		return fmt.Errorf("unknown request status %q", record.Status)
	}
	if err := record.InputProtocol.Validate(); err != nil {
		return fmt.Errorf("input protocol: %w", err)
	}
	if record.RequestedModel != nil {
		if *record.RequestedModel == "" || utf8.RuneCountInString(*record.RequestedModel) > 256 {
			return fmt.Errorf("requested_model must contain 1 to 256 characters when set")
		}
	}
	if record.ModelRedirect != nil {
		if err := record.ModelRedirect.Validate(); err != nil {
			return err
		}
	}
	if record.RoutingDecision != nil {
		if err := record.RoutingDecision.Validate(); err != nil {
			return err
		}
	}
	if record.ReasoningEffort != nil {
		if err := validateBoundedText("reasoning_effort", *record.ReasoningEffort, 32, false); err != nil {
			return err
		}
	}
	if record.RouteID != nil {
		if err := record.RouteID.Validate(); err != nil {
			return fmt.Errorf("route_id: %w", err)
		}
	}
	if record.ServiceID != nil {
		if err := record.ServiceID.Validate(); err != nil {
			return fmt.Errorf("service_id: %w", err)
		}
	}
	if record.LocalAccessTokenID != nil {
		if err := record.LocalAccessTokenID.Validate(); err != nil {
			return fmt.Errorf("local_access_token_id: %w", err)
		}
	}
	if record.Plan != nil {
		if err := record.Plan.Validate(); err != nil {
			return fmt.Errorf("plan: %w", err)
		}
	}
	if record.HTTPStatus != nil {
		if *record.HTTPStatus < 100 || *record.HTTPStatus > 599 {
			return fmt.Errorf("http_status must be between 100 and 599")
		}
	}
	if record.LatencyMs != nil && *record.LatencyMs < 0 {
		return fmt.Errorf("latency_ms must be non-negative")
	}
	if record.FirstTokenMs != nil && (!record.Streaming || *record.FirstTokenMs < 0 ||
		(record.LatencyMs != nil && *record.FirstTokenMs > *record.LatencyMs)) {
		return fmt.Errorf("first_token_ms requires streaming and must be between zero and latency_ms")
	}
	if record.Usage != nil {
		if err := record.Usage.Validate(); err != nil {
			return err
		}
	}
	if record.Error != nil {
		if err := record.Error.Validate(); err != nil {
			return err
		}
	}
	if record.PrivacyRestore != nil {
		if err := record.PrivacyRestore.Validate(); err != nil {
			return err
		}
	}
	if record.SessionID != nil {
		if err := record.SessionID.Validate(); err != nil {
			return fmt.Errorf("session_id: %w", err)
		}
	}
	if record.PreviousResponseID != nil {
		if err := validateProtocolCursor("previous_response_id", *record.PreviousResponseID); err != nil {
			return err
		}
	}
	if record.OutputResponseID != nil {
		if err := validateProtocolCursor("output_response_id", *record.OutputResponseID); err != nil {
			return err
		}
	}
	if record.InputPreview != nil {
		if err := validateBoundedText("input_preview", *record.InputPreview, MaxInputPreviewRunes, false); err != nil {
			return err
		}
	}
	if record.TurnIndex != nil && *record.TurnIndex < 1 {
		return fmt.Errorf("turn_index must be at least 1 when set")
	}
	if record.TurnUserMessages != nil {
		if record.TurnIndex == nil {
			return fmt.Errorf("turn_user_messages requires turn_index")
		}
		if *record.TurnUserMessages < 0 {
			return fmt.Errorf("turn_user_messages must be non-negative")
		}
	}
	if record.TurnUserFingerprint != nil {
		if record.TurnIndex == nil {
			return fmt.Errorf("turn_user_fingerprint requires turn_index")
		}
		if err := validateProtocolCursor("turn_user_fingerprint", *record.TurnUserFingerprint); err != nil {
			return err
		}
	}
	if record.SessionLink != nil {
		if err := record.SessionLink.Validate(); err != nil {
			return fmt.Errorf("session_link: %w", err)
		}
	}
	if len(record.Cursors) > MaxSessionCursors {
		return fmt.Errorf("cursors must contain at most %d entries", MaxSessionCursors)
	}
	for index, cursor := range record.Cursors {
		if err := cursor.Validate(); err != nil {
			return fmt.Errorf("cursors[%d]: %w", index, err)
		}
	}
	for index, event := range record.Events {
		if err := event.Validate(); err != nil {
			return fmt.Errorf("events[%d]: %w", index, err)
		}
	}
	return nil
}

// EffectiveStatus is the user-visible outcome. A completed relay that still
// returned HTTP 4xx/5xx is failed, including legacy rows stored as succeeded.
func (record RequestRecord) EffectiveStatus() RequestStatus {
	if record.Status == RequestStatusSucceeded &&
		record.HTTPStatus != nil &&
		*record.HTTPStatus >= 400 {
		return RequestStatusFailed
	}
	return record.Status
}

type RequestSession struct {
	// ClientType mirrors the latest call, like the model and provider fields.
	ClientType    ClientType `json:"client_type,omitempty"`
	ID            SessionID  `json:"id"`
	Title         string     `json:"title"`
	StartedAt     time.Time  `json:"started_at"`
	LastStartedAt time.Time  `json:"last_started_at"`
	CompletedAt   *time.Time `json:"completed_at"`
	DurationMs    int64      `json:"duration_ms"`
	// ToolDurationMs estimates gaps between calls in the same user turn;
	// retry backoff and gaps between user turns are excluded. Nil if unknown.
	ToolDurationMs        *int64        `json:"tool_duration_ms"`
	AverageTTFTMs         *float64      `json:"average_ttft_ms"`
	OutputTokensPerSecond *float64      `json:"output_tokens_per_second"`
	ActiveRequestStarts   []time.Time   `json:"active_request_starts"`
	TurnCount             int           `json:"turn_count"`
	CallCount             int           `json:"call_count"`
	Status                SessionStatus `json:"status"`
	RequestedModel        *string       `json:"requested_model"`
	// ModelRedirect mirrors the latest call's redirect, if any.
	ModelRedirect      *RequestModelRedirect `json:"model_redirect,omitempty"`
	ReasoningEffort    *string               `json:"reasoning_effort"`
	InputProtocol      ProtocolID            `json:"input_protocol"`
	ServiceID          *ServiceID            `json:"service_id"`
	LocalAccessTokenID *AccessTokenID        `json:"local_access_token_id"`
}

func (session RequestSession) Validate() error {
	if session.ClientType != "" && !session.ClientType.Valid() {
		return fmt.Errorf("unknown client type %q", session.ClientType)
	}
	if err := session.ID.Validate(); err != nil {
		return err
	}
	if err := validateBoundedText("title", session.Title, MaxSessionTitleRunes, false); err != nil {
		return err
	}
	if session.StartedAt.IsZero() || session.LastStartedAt.IsZero() {
		return fmt.Errorf("session timestamps are required")
	}
	if session.TurnCount < 1 || session.CallCount < 1 {
		return fmt.Errorf("session counts must be at least 1")
	}
	if session.DurationMs < 0 {
		return fmt.Errorf("duration_ms must be non-negative")
	}
	if session.ToolDurationMs != nil && *session.ToolDurationMs < 0 {
		return fmt.Errorf("tool_duration_ms must be non-negative")
	}
	for _, metric := range []struct {
		name  string
		value *float64
	}{
		{"average_ttft_ms", session.AverageTTFTMs}, {"output_tokens_per_second", session.OutputTokensPerSecond},
	} {
		if metric.value != nil && (*metric.value < 0 || math.IsNaN(*metric.value) || math.IsInf(*metric.value, 0)) {
			return fmt.Errorf("%s must be finite and non-negative", metric.name)
		}
	}
	for _, started := range session.ActiveRequestStarts {
		if started.IsZero() {
			return fmt.Errorf("active_request_starts must contain valid timestamps")
		}
	}
	if !session.Status.Valid() {
		return fmt.Errorf("unknown session status %q", session.Status)
	}
	if err := session.InputProtocol.Validate(); err != nil {
		return fmt.Errorf("input protocol: %w", err)
	}
	if session.RequestedModel != nil {
		if *session.RequestedModel == "" || utf8.RuneCountInString(*session.RequestedModel) > 256 {
			return fmt.Errorf("requested_model must contain 1 to 256 characters when set")
		}
	}
	if session.ModelRedirect != nil {
		if err := session.ModelRedirect.Validate(); err != nil {
			return err
		}
	}
	if session.ReasoningEffort != nil {
		if err := validateBoundedText("reasoning_effort", *session.ReasoningEffort, 32, false); err != nil {
			return err
		}
	}
	if session.ServiceID != nil {
		if err := session.ServiceID.Validate(); err != nil {
			return fmt.Errorf("service_id: %w", err)
		}
	}
	if session.LocalAccessTokenID != nil {
		if err := session.LocalAccessTokenID.Validate(); err != nil {
			return fmt.Errorf("local_access_token_id: %w", err)
		}
	}
	return nil
}

type RequestSessionPage struct {
	Items      []RequestSession `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

type RequestSessionDetail struct {
	RequestSession
	Turns []RequestRecord `json:"turns"`
}

func (detail RequestSessionDetail) Validate() error {
	if err := detail.RequestSession.Validate(); err != nil {
		return err
	}
	if len(detail.Turns) == 0 {
		return fmt.Errorf("session turns are required")
	}
	for index, turn := range detail.Turns {
		if err := turn.Validate(); err != nil {
			return fmt.Errorf("turns[%d]: %w", index, err)
		}
	}
	return nil
}

func validateProtocolCursor(field, value string) error {
	return validateBoundedText(field, value, MaxProtocolCursorRunes, false)
}

func validateBoundedText(field, value string, maxRunes int, allowEmpty bool) error {
	if value == "" {
		if allowEmpty {
			return nil
		}
		return fmt.Errorf("%s must not be empty", field)
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("%s must contain at most %d characters", field, maxRunes)
	}
	for _, runeValue := range value {
		if runeValue < 32 && runeValue != '\t' {
			return fmt.Errorf("%s must not contain control characters", field)
		}
	}
	return nil
}

func ClampRunes(value string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes])
}

type PurgeScope string

const (
	PurgeScopeAll    PurgeScope = "all"
	PurgeScopeBefore PurgeScope = "before"
)

func (scope PurgeScope) Valid() bool {
	return scope == PurgeScopeAll || scope == PurgeScopeBefore
}

type PurgeRequest struct {
	Scope   PurgeScope `json:"scope"`
	Before  *time.Time `json:"before,omitempty"`
	Confirm bool       `json:"confirm"`
}

func (request PurgeRequest) Validate() error {
	if !request.Scope.Valid() {
		return fmt.Errorf("unknown purge scope %q", request.Scope)
	}
	if !request.Confirm {
		return fmt.Errorf("confirm must be true")
	}
	switch request.Scope {
	case PurgeScopeBefore:
		if request.Before == nil || request.Before.IsZero() {
			return fmt.Errorf("before is required when scope is before")
		}
	case PurgeScopeAll:
		if request.Before != nil {
			return fmt.Errorf("before must be omitted when scope is all")
		}
	}
	return nil
}

type PurgeResult struct {
	DeletedRecords    int `json:"deleted_records"`
	DeletedAuditBlobs int `json:"deleted_audit_blobs"`
}

func (result PurgeResult) Validate() error {
	if result.DeletedRecords < 0 || result.DeletedAuditBlobs < 0 {
		return fmt.Errorf("purge counts must be non-negative")
	}
	return nil
}

type RequestRecordPage struct {
	Items      []RequestRecord `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}
