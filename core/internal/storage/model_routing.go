package storage

import (
	"context"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

type ModelRoutingBindingStore interface {
	GetModelRoutingBinding(context.Context, string, contract.SessionID, contract.ProtocolID, string) (contract.ModelRoutingBinding, error)
	// UpsertModelRoutingBinding skips the write when the session's bindings
	// were released at or after started, the time the writing request began.
	UpsertModelRoutingBinding(ctx context.Context, binding contract.ModelRoutingBinding, started time.Time) error
	DeleteModelRoutingBinding(context.Context, string, contract.SessionID, contract.ProtocolID, string) error
}

// ModelRoutingVersionStore keeps every saved configuration of each routing
// group under a per-group version number.
type ModelRoutingVersionStore interface {
	EnsureModelRoutingGroupVersion(context.Context, contract.ModelRoutingGroup) (int, error)
	GetModelRoutingGroupVersion(ctx context.Context, groupID string, version int) (contract.ModelRoutingGroupVersion, error)
}

// ModelRoutingWriteCoordinator is the atomic settings write seam. A store that
// implements it validates the referenced service/model closure in the same
// write transaction as the settings document.
type ModelRoutingWriteCoordinator interface {
	UpdateRoutingSettingsAtomic(context.Context, contract.RoutingSettings) error
}
