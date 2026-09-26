package storage

import (
	"context"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// SubscriptionRiskEventStore keeps a bounded per-account history of upstream
// risk signals and manual restores.
type SubscriptionRiskEventStore interface {
	AppendSubscriptionRiskEvent(context.Context, contract.SubscriptionRiskEvent) error
	ListSubscriptionRiskEvents(context.Context, contract.ServiceID, int) ([]contract.SubscriptionRiskEvent, error)
	CountSubscriptionRiskEvents(context.Context, contract.ServiceID, []string, time.Time) (int, error)
}
