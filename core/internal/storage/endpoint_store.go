package storage

import (
	"context"
	"errors"

	"github.com/QuantumNous/astrlink/core/contract"
)

var (
	ErrNotFound        = errors.New("resource not found")
	ErrConflict        = errors.New("resource already exists")
	ErrPrecondition    = errors.New("resource precondition failed")
	ErrInvalidArgument = errors.New("storage argument is invalid")
	ErrInvalidCursor   = errors.New("storage cursor is invalid")
	ErrInvalidRecord   = errors.New("persisted resource is invalid")
	ErrUnsupportedRef  = errors.New("credential reference backend is unsupported")
	ErrLimitReached    = errors.New("storage resource limit reached")
	ErrAuditDecrypt    = errors.New("audit content cannot be decrypted")
	// ErrModelRoutingReference marks a service change a model routing group
	// still depends on: the change would remove or disable the service, or
	// take away a model one of its members uses.
	ErrModelRoutingReference = errors.New("a model routing group depends on the service")
	// ErrClassifierTarget marks classifier settings whose existing model
	// cannot serve: its service is missing or disabled, or does not list it.
	ErrClassifierTarget = errors.New("the classifier model is unavailable")
)

// EndpointRecord couples a validated Endpoint document with the strong entity
// tag used by the authenticated control API for optimistic concurrency.
type EndpointRecord struct {
	Endpoint contract.Endpoint
	ETag     string
}

type EndpointListOptions struct {
	Limit   int
	Cursor  string
	Enabled *bool
	Kind    *contract.EndpointKind
}

type EndpointPage struct {
	Items      []EndpointRecord
	NextCursor string
}

// CredentialMutation distinguishes an omitted credential update from explicit
// deletion. Secret bytes never enter the Endpoint JSON document.
type CredentialMutation struct {
	ProxyPresent bool
	Proxy        *contract.ProxyCredential
	Present      bool
	Secret       []byte
}

type EndpointStore interface {
	CreateEndpoint(context.Context, contract.Endpoint, CredentialMutation) (EndpointRecord, error)
	GetEndpoint(context.Context, contract.ServiceID) (EndpointRecord, error)
	ListEndpoints(context.Context, EndpointListOptions) (EndpointPage, error)
	UpdateEndpoint(context.Context, contract.Endpoint, CredentialMutation, string) (EndpointRecord, error)
	DeleteEndpoint(context.Context, contract.ServiceID, string) error
}
