package storage

import (
	"context"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

type RequestRecordListOptions struct {
	Limit               int
	Cursor              string
	From                *time.Time
	To                  *time.Time
	Protocol            *contract.ProtocolID
	ServiceID           *contract.ServiceID
	LocalAccessTokenIDs []contract.AccessTokenID
	Status              *contract.RequestStatus
}

type RequestRecordPage struct {
	Items      []contract.RequestRecord
	NextCursor string
}

// AccessTokenUsage counts successful root requests in retained history only.
// Retry children must not be counted again alongside their root.
type AccessTokenUsage struct {
	TokenID     contract.AccessTokenID `json:"token_id"`
	TodayTokens int64                  `json:"today_tokens"`
	TotalTokens int64                  `json:"total_tokens"`
}

// RequestRecordStore persists always-on inference metadata records (ADR 0007).
// Implementations must validate every row on read so corrupt history fails closed.
type RequestSessionListOptions struct {
	// Kind is empty for all, inference for calls, or discovery for model listings.
	Kind                string
	Limit               int
	Cursor              string
	From                *time.Time
	To                  *time.Time
	Protocol            *contract.ProtocolID
	ServiceID           *contract.ServiceID
	LocalAccessTokenIDs []contract.AccessTokenID
	Status              *contract.RequestStatus
}

type RequestSessionPage struct {
	Items      []contract.RequestSession
	NextCursor string
}

// SessionCursorScope narrows a FindSessionLink query. It mirrors convo.Scope
// with the principal made concrete: AstrLink scopes by local access token.
type SessionCursorScope struct {
	// SamePrincipal restricts matches to roots recorded under
	// LocalAccessTokenID (a nil token only matches other nil-token roots).
	SamePrincipal      bool
	LocalAccessTokenID *contract.AccessTokenID
	// NotBefore, when non-zero, excludes roots that started earlier.
	NotBefore time.Time
}

// SessionLinkMatch is the root record FindSessionLink selected.
type SessionLinkMatch struct {
	SessionID contract.SessionID
	// TurnIndex, TurnUserMessages, and TurnUserFingerprint are the matched
	// record's stored turn state (contract.RequestRecord fields of the same
	// name); TurnIndex is nil on rows written before turns existed.
	TurnIndex           *int
	TurnUserMessages    *int
	TurnUserFingerprint string
	// Value is the queried cursor value that matched.
	Value string
}

type RequestRecordStore interface {
	InsertRequestRecord(context.Context, contract.RequestRecord) error
	UpsertRequestRecord(context.Context, contract.RequestRecord) error
	ListRequestRecords(context.Context, RequestRecordListOptions) (RequestRecordPage, error)
	ListAccessTokenUsage(context.Context, time.Time) ([]AccessTokenUsage, error)
	GetUsageSummary(context.Context, UsageSummaryOptions) (UsageSummary, error)
	ListRequestRecordChildren(context.Context, contract.RequestID) ([]contract.RequestRecord, error)
	GetRequestRecord(context.Context, contract.RequestID) (contract.RequestRecord, error)
	DeleteRequestRecord(context.Context, contract.RequestID) error
	PurgeRequestRecords(context.Context, contract.PurgeRequest) (contract.PurgeResult, error)
	// FindSessionLink returns the most recent root whose stored cursors of
	// kind contain any of values, honouring scope. Roots that produced a
	// value (direction out, or the legacy output_response_id column) win
	// over roots that only named it (direction in / previous_response_id);
	// inbound matches are considered for explicit cursors only. ok is false
	// when nothing matched.
	FindSessionLink(ctx context.Context, kind contract.SessionCursorKind, values []string, scope SessionCursorScope) (match SessionLinkMatch, ok bool, err error)
	ListRequestSessions(context.Context, RequestSessionListOptions) (RequestSessionPage, error)
	GetRequestSession(context.Context, string) (contract.RequestSessionDetail, error)
}
