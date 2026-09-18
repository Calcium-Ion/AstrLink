package storage

import (
	"context"
	"github.com/QuantumNous/astrlink/core/contract"
)

type RoutingSettingsStore interface {
	GetRoutingSettings(context.Context) (contract.RoutingSettings, error)
	UpdateRoutingSettings(context.Context, contract.RoutingSettings) error
}
