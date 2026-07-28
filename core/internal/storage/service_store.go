package storage

import (
	"context"

	"github.com/QuantumNous/astrlink/core/contract"
)

// ServiceRecord couples a validated Service document with the strong entity
// tag used for optimistic concurrency.
type ServiceRecord struct {
	Service contract.Service
	ETag    string
}

type ServiceListOptions struct {
	Limit   int
	Cursor  string
	Enabled *bool
	Kind    *contract.ServiceKind
}

type ServicePage struct {
	Items      []ServiceRecord
	NextCursor string
}

// ServiceStore is the only persistence owner for configured API-service
// identity and non-secret state.
type ServiceStore interface {
	CreateService(context.Context, contract.Service, CredentialMutation) (ServiceRecord, error)
	GetService(context.Context, contract.ServiceID) (ServiceRecord, error)
	ListServices(context.Context, ServiceListOptions) (ServicePage, error)
	UpdateService(context.Context, contract.Service, CredentialMutation, string) (ServiceRecord, error)
	DeleteService(context.Context, contract.ServiceID, string) error
}
