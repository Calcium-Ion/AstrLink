package contract

import (
	"fmt"
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
	RequestEventAccepted  RequestEventKind = "accepted"
	RequestEventPrivacy   RequestEventKind = "privacy"
	RequestEventRouted    RequestEventKind = "routed"
	RequestEventUpstream  RequestEventKind = "upstream"
	RequestEventRestore   RequestEventKind = "restore"
	RequestEventCompleted RequestEventKind = "completed"
)

func (kind RequestEventKind) Valid() bool {
	switch kind {
	case RequestEventAccepted, RequestEventPrivacy, RequestEventRouted,
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

// Usage uses OpenAI-style input accounting: input_tokens includes all
// prompt-side tokens (uncached, cache read, cache write/creation, and
// multimodal input such as images). cache_read_tokens / cache_write_tokens
// are subsets of that input when the upstream reports them.
type Usage struct {
	InputTokens      int  `json:"input_tokens"`
	OutputTokens     int  `json:"output_tokens"`
	TotalTokens      int  `json:"total_tokens"`
	CacheReadTokens  *int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int `json:"cache_write_tokens,omitempty"`
}

func (usage Usage) Validate() error {
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

type RequestRecord struct {
	ID                 RequestID              `json:"id"`
	ParentRequestID    *RequestID             `json:"parent_request_id"`
	AttemptIndex       int                    `json:"attempt_index"`
	ChildCount         int                    `json:"child_count"`
	StartedAt          time.Time              `json:"started_at"`
	CompletedAt        *time.Time             `json:"completed_at"`
	Status             RequestStatus          `json:"status"`
	InputProtocol      ProtocolID             `json:"input_protocol"`
	RequestedModel     *string                `json:"requested_model"`
	Streaming          bool                   `json:"streaming"`
	RouteID            *RouteID               `json:"route_id"`
	ServiceID          *ServiceID             `json:"service_id"`
	LocalAccessTokenID *AccessTokenID         `json:"local_access_token_id"`
	Plan               *ExecutionPlan         `json:"plan"`
	HTTPStatus         *int                   `json:"http_status"`
	LatencyMs          *int                   `json:"latency_ms"`
	Usage              *Usage                 `json:"usage"`
	Error              *ErrorSummary          `json:"error"`
	Audit              AuditRecordSummary     `json:"audit"`
	PrivacyRestore     *PrivacyRestoreSummary `json:"privacy_restore"`
	SessionID          *SessionID             `json:"session_id"`
	PreviousResponseID *string                `json:"previous_response_id"`
	OutputResponseID   *string                `json:"output_response_id"`
	InputPreview       *string                `json:"input_preview"`
	Events             []RequestEvent         `json:"events"`
	Extensions         map[string]any         `json:"extensions,omitempty"`
}

func (record RequestRecord) Validate() error {
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
	for index, event := range record.Events {
		if err := event.Validate(); err != nil {
			return fmt.Errorf("events[%d]: %w", index, err)
		}
	}
	return nil
}

type RequestSession struct {
	ID                 SessionID      `json:"id"`
	Title              string         `json:"title"`
	StartedAt          time.Time      `json:"started_at"`
	LastStartedAt      time.Time      `json:"last_started_at"`
	CompletedAt        *time.Time     `json:"completed_at"`
	TurnCount          int            `json:"turn_count"`
	CallCount          int            `json:"call_count"`
	Status             RequestStatus  `json:"status"`
	RequestedModel     *string        `json:"requested_model"`
	InputProtocol      ProtocolID     `json:"input_protocol"`
	ServiceID          *ServiceID     `json:"service_id"`
	LocalAccessTokenID *AccessTokenID `json:"local_access_token_id"`
}

func (session RequestSession) Validate() error {
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
	if !session.Status.Valid() {
		return fmt.Errorf("unknown request status %q", session.Status)
	}
	if err := session.InputProtocol.Validate(); err != nil {
		return fmt.Errorf("input protocol: %w", err)
	}
	if session.RequestedModel != nil {
		if *session.RequestedModel == "" || utf8.RuneCountInString(*session.RequestedModel) > 256 {
			return fmt.Errorf("requested_model must contain 1 to 256 characters when set")
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
