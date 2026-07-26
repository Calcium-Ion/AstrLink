package storage

import (
	"context"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

type AuditDirection string

const (
	AuditDirectionRequest  AuditDirection = "request"
	AuditDirectionResponse AuditDirection = "response"
)

func (direction AuditDirection) Valid() bool {
	return direction == AuditDirectionRequest || direction == AuditDirectionResponse
}

type AuditBlob struct {
	RequestID     contract.RequestID
	Direction     AuditDirection
	MediaType     string
	Nonce         []byte
	Ciphertext    []byte
	Truncated     bool
	CapturedBytes int
	CreatedAt     time.Time
}

type AuditSettingsStore interface {
	GetAuditSettings(context.Context) (contract.AuditSettings, error)
	UpdateAuditSettings(context.Context, contract.AuditSettings) error
}

type AuditKeyStore interface {
	// GetOrCreateAuditKey returns the 32-byte AES key, generating it once.
	GetOrCreateAuditKey(context.Context) ([]byte, error)
	// GetAuditKey returns the key when present without creating one.
	GetAuditKey(context.Context) ([]byte, error)
}

type AuditBlobStore interface {
	InsertAuditBlob(context.Context, AuditBlob) error
	GetAuditBlobsByRequest(context.Context, contract.RequestID) ([]AuditBlob, error)
	DeleteAuditBlobsByRequest(context.Context, contract.RequestID) (int, error)
	DeleteAuditBlobsOlderThan(context.Context, time.Time) (int, error)
}

type AuditRetentionStore interface {
	SweepExpiredAuditData(context.Context) (SweepResult, error)
}

type SweepResult struct {
	DeletedRecords    int
	DeletedAuditBlobs int
}
