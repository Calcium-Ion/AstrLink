// Package secretstore defines the storage-neutral credential boundary. The
// default Alpha adapter stores local:// references in SQLite; optional adapters
// may serve other accepted schemes such as keyring://.
package secretstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
)

var (
	ErrNotFound    = errors.New("secret not found")
	ErrUnavailable = errors.New("credential store is unavailable")
)

type Ref string

func ParseRef(value string) (Ref, error) {
	if err := contract.ValidateCredentialRef(value); err != nil {
		return "", fmt.Errorf("invalid secret reference: %w", err)
	}
	return Ref(value), nil
}

// SecretStore stores opaque bytes. Implementations must copy caller-provided
// buffers if they retain them after Put returns.
type SecretStore interface {
	Get(context.Context, Ref) ([]byte, error)
	Put(context.Context, Ref, []byte) error
	Delete(context.Context, Ref) error
}
