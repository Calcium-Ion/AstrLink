package storage

import (
	"context"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

type ChannelBindingStore interface {
	GetChannelBinding(context.Context, contract.ChannelBindingScope) (contract.ChannelBinding, bool, error)
	RememberChannelBinding(context.Context, contract.ChannelBinding, time.Time) error
	RecordChannelBindingEvent(context.Context, contract.ChannelBindingEvent) error
	GetChannelBindingAudit(context.Context, contract.SessionID, int64) (contract.ChannelBindingAudit, error)
	ReleaseChannelBindings(context.Context, contract.SessionID) error
}
