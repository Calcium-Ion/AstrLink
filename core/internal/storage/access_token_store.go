package storage

import (
	"context"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

const AccessTokenLimit = 100

type AccessTokenSource string

const (
	AccessTokenSourceSystemDefault AccessTokenSource = "system_default"
	AccessTokenSourceUser          AccessTokenSource = "user"

	// AccessTokenSourceBootstrap names the creation path while preserving the
	// public wire value system_default.
	AccessTokenSourceBootstrap = AccessTokenSourceSystemDefault
)

func (source AccessTokenSource) Valid() bool {
	return source == AccessTokenSourceSystemDefault || source == AccessTokenSourceUser
}

// AccessTokenMetadata is safe to return from list operations. It deliberately
// contains neither the bearer value nor its SHA-256 digest.
type AccessTokenMetadata struct {
	ID        contract.AccessTokenID `json:"id"`
	Name      string                 `json:"name"`
	Hint      string                 `json:"hint"`
	Source    AccessTokenSource      `json:"source"`
	CreatedAt time.Time              `json:"created_at"`
}

type AccessTokenHash [32]byte

// NewAccessToken carries secret material across the narrow Manager-to-Store
// boundary. Store implementations must persist Value separately from metadata
// and never return Hash or Value from list operations.
type NewAccessToken struct {
	ID      contract.AccessTokenID
	Name    string
	NameKey string
	Hash    AccessTokenHash
	Hint    string
	Source  AccessTokenSource
	Value   string
}

type AccessTokenStore interface {
	EnsureDefaultAccessToken(context.Context, NewAccessToken) (AccessTokenMetadata, bool, error)
	CreateAccessToken(context.Context, NewAccessToken) (AccessTokenMetadata, error)
	ListAccessTokens(context.Context) ([]AccessTokenMetadata, error)
	RevealAccessToken(context.Context, contract.AccessTokenID) (string, error)
	DeleteAccessToken(context.Context, contract.AccessTokenID) error
	FindAccessTokenByHash(context.Context, AccessTokenHash) (AccessTokenMetadata, error)
}
