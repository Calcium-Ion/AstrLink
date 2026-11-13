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
)

func (source AccessTokenSource) Valid() bool {
	return source == AccessTokenSourceSystemDefault || source == AccessTokenSourceUser
}

// AccessTokenMetadata is safe to return from list operations. It deliberately
// contains neither the bearer value nor its SHA-256 digest. Source tells the
// bootstrap token this installation issued apart from the ones a user created.
type AccessTokenMetadata struct {
	ID        contract.AccessTokenID `json:"id"`
	Name      string                 `json:"name"`
	Hint      string                 `json:"hint"`
	Source    AccessTokenSource      `json:"source"`
	CreatedAt time.Time              `json:"created_at"`
	// Services is the set of API providers the token may reach.
	Services contract.AccessTokenServices `json:"-"`
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
	Value   string
	// Services is the provider scope the token starts with. The zero value
	// is unrestricted.
	Services contract.AccessTokenServices
}

// AccessTokenUpdate replaces a token's name and provider scope.
type AccessTokenUpdate struct {
	Name     string
	NameKey  string
	Services contract.AccessTokenServices
}

type AccessTokenStore interface {
	EnsureDefaultAccessToken(context.Context, NewAccessToken) (AccessTokenMetadata, bool, error)
	CreateAccessToken(context.Context, NewAccessToken) (AccessTokenMetadata, error)
	ListAccessTokens(context.Context) ([]AccessTokenMetadata, error)
	RevealAccessToken(context.Context, contract.AccessTokenID) (string, error)
	UpdateAccessToken(context.Context, contract.AccessTokenID, AccessTokenUpdate) (AccessTokenMetadata, error)
	DeleteAccessToken(context.Context, contract.AccessTokenID) error
	FindAccessTokenByHash(context.Context, AccessTokenHash) (AccessTokenMetadata, error)
}
