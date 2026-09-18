package storage

import (
	"context"
	"github.com/QuantumNous/astrlink/core/contract"
)

type ServiceOrderRecord struct {
	Order contract.ServiceOrder
	ETag  string
}

type ServiceOrderStore interface {
	GetServiceOrder(context.Context) (ServiceOrderRecord, error)
	UpdateServiceOrder(context.Context, contract.ServiceOrder, string) (ServiceOrderRecord, error)
}
