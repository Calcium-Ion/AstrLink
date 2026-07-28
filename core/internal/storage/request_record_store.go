package storage

import (
	"context"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

type RequestRecordListOptions struct {
	Limit     int
	Cursor    string
	From      *time.Time
	To        *time.Time
	Protocol  *contract.ProtocolID
	ServiceID *contract.ServiceID
	Status    *contract.RequestStatus
}

type RequestRecordPage struct {
	Items      []contract.RequestRecord
	NextCursor string
}

// RequestRecordStore persists always-on inference metadata records (ADR 0007).
// Implementations must validate every row on read so corrupt history fails closed.
type RequestRecordStore interface {
	InsertRequestRecord(context.Context, contract.RequestRecord) error
	UpsertRequestRecord(context.Context, contract.RequestRecord) error
	ListRequestRecords(context.Context, RequestRecordListOptions) (RequestRecordPage, error)
	GetRequestRecord(context.Context, contract.RequestID) (contract.RequestRecord, error)
	DeleteRequestRecord(context.Context, contract.RequestID) error
	PurgeRequestRecords(context.Context, contract.PurgeRequest) (contract.PurgeResult, error)
}
