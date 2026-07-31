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

type Usage struct {
	InputTokens       int  `json:"input_tokens"`
	OutputTokens      int  `json:"output_tokens"`
	TotalTokens       int  `json:"total_tokens"`
	CachedInputTokens *int `json:"cached_input_tokens,omitempty"`
}

func (usage Usage) Validate() error {
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.TotalTokens < 0 {
		return fmt.Errorf("usage token counts must be non-negative")
	}
	if usage.CachedInputTokens != nil && *usage.CachedInputTokens < 0 {
		return fmt.Errorf("cached_input_tokens must be non-negative")
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

// PrivacyRestoreSummary contains bounded, non-sensitive diagnostics for the
// request-scoped response placeholder mapping. It never contains categories,
// placeholders, or original values.
type PrivacyRestoreSummary struct {
	Enabled       bool `json:"enabled"`
	MappingCount  int  `json:"mapping_count"`
	RestoredCount int  `json:"restored_count"`
	FallbackCount int  `json:"fallback_count"`
}

func (summary PrivacyRestoreSummary) Validate() error {
	if summary.MappingCount < 0 ||
		summary.RestoredCount < 0 ||
		summary.FallbackCount < 0 {
		return fmt.Errorf("privacy restore counts must be non-negative")
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
	return nil
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
