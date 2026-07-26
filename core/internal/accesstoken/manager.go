// Package accesstoken owns creation and authentication of persistent local
// inference access tokens. Raw values are generated with crypto/rand, cross the
// storage boundary only for explicit create/reveal operations, and are never
// present in list metadata.
package accesstoken

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const (
	DefaultTokenName = "默认令牌"
	MaxTokens        = storage.AccessTokenLimit
	tokenPrefix      = "astr_"
	tokenRandomBytes = 32
	idRandomBytes    = 16
	idPrefix         = "access_token_"
)

var (
	ErrInvalidName  = errors.New("access token name is invalid")
	ErrInvalidToken = errors.New("access token is invalid")

	ErrTokenLimit    = storage.ErrLimitReached
	ErrNotFound      = storage.ErrNotFound
	ErrConflict      = storage.ErrConflict
	ErrInvalidRecord = storage.ErrInvalidRecord
)

type Source = storage.AccessTokenSource

const (
	SourceSystemDefault = storage.AccessTokenSourceSystemDefault
	SourceUser          = storage.AccessTokenSourceUser

	SourceBootstrap = SourceSystemDefault
)

type Token = storage.AccessTokenMetadata

type CreatedToken struct {
	Token Token  `json:"token"`
	Value string `json:"access_token"`
}

type Manager struct {
	store  storage.AccessTokenStore
	random io.Reader
}

func NewManager(store storage.AccessTokenStore) (*Manager, error) {
	if store == nil {
		return nil, fmt.Errorf("access token store is required")
	}
	return &Manager{store: store, random: rand.Reader}, nil
}

// EnsureDefault atomically creates the single bootstrap token and records that
// initialization has completed. Once completed, deleting every token does not
// cause another bootstrap token to be created.
func (manager *Manager) EnsureDefault(ctx context.Context) (Token, bool, error) {
	candidate, err := manager.newToken(DefaultTokenName, SourceSystemDefault)
	if err != nil {
		return Token{}, false, err
	}
	token, created, err := manager.store.EnsureDefaultAccessToken(ctx, candidate)
	if err != nil {
		return Token{}, false, fmt.Errorf("ensure default access token: %w", err)
	}
	return token, created, nil
}

func (manager *Manager) Create(ctx context.Context, name string) (CreatedToken, error) {
	candidate, err := manager.newToken(name, SourceUser)
	if err != nil {
		return CreatedToken{}, err
	}
	token, err := manager.store.CreateAccessToken(ctx, candidate)
	if err != nil {
		return CreatedToken{}, fmt.Errorf("create access token: %w", err)
	}
	return CreatedToken{Token: token, Value: candidate.Value}, nil
}

func (manager *Manager) List(ctx context.Context) ([]Token, error) {
	tokens, err := manager.store.ListAccessTokens(ctx)
	if err != nil {
		return nil, fmt.Errorf("list access tokens: %w", err)
	}
	return tokens, nil
}

func (manager *Manager) Reveal(ctx context.Context, id contract.AccessTokenID) (string, error) {
	if err := id.Validate(); err != nil {
		return "", fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	value, err := manager.store.RevealAccessToken(ctx, id)
	if err != nil {
		return "", fmt.Errorf("reveal access token: %w", err)
	}
	return value, nil
}

func (manager *Manager) Delete(ctx context.Context, id contract.AccessTokenID) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	if err := manager.store.DeleteAccessToken(ctx, id); err != nil {
		return fmt.Errorf("delete access token: %w", err)
	}
	return nil
}

// Authenticate hashes a canonical bearer value and returns only its stable,
// non-secret identifier. Missing or internally inconsistent records collapse
// to the same public error and therefore fail closed.
func (manager *Manager) Authenticate(ctx context.Context, raw string) (contract.AccessTokenID, error) {
	if !validRawToken(raw) {
		return "", ErrInvalidToken
	}
	sum := sha256.Sum256([]byte(raw))
	token, err := manager.store.FindAccessTokenByHash(ctx, storage.AccessTokenHash(sum))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrInvalidRecord) {
			return "", ErrInvalidToken
		}
		return "", fmt.Errorf("authenticate access token: %w", err)
	}
	return token.ID, nil
}

func (manager *Manager) newToken(name string, source Source) (storage.NewAccessToken, error) {
	canonicalName, nameKey, err := normalizeName(name)
	if err != nil {
		return storage.NewAccessToken{}, err
	}
	if !source.Valid() {
		return storage.NewAccessToken{}, fmt.Errorf("access token source %q is invalid", source)
	}

	idBytes := make([]byte, idRandomBytes)
	if _, err := io.ReadFull(manager.random, idBytes); err != nil {
		return storage.NewAccessToken{}, fmt.Errorf("generate access token id: %w", err)
	}
	defer clear(idBytes)
	secretBytes := make([]byte, tokenRandomBytes)
	if _, err := io.ReadFull(manager.random, secretBytes); err != nil {
		return storage.NewAccessToken{}, fmt.Errorf("generate access token: %w", err)
	}
	defer clear(secretBytes)

	id := contract.AccessTokenID(idPrefix + hex.EncodeToString(idBytes))
	raw := tokenPrefix + base64.RawURLEncoding.EncodeToString(secretBytes)
	hash := sha256.Sum256([]byte(raw))
	return storage.NewAccessToken{
		ID:      id,
		Name:    canonicalName,
		NameKey: nameKey,
		Hash:    storage.AccessTokenHash(hash),
		Hint:    tokenHint(raw),
		Source:  source,
		Value:   raw,
	}, nil
}

func normalizeName(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > 64 {
		return "", "", ErrInvalidName
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", "", ErrInvalidName
		}
	}
	return value, simpleFoldKey(value), nil
}

func simpleFoldKey(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, character := range value {
		smallest := character
		for folded := unicode.SimpleFold(character); folded != character; folded = unicode.SimpleFold(folded) {
			if folded < smallest {
				smallest = folded
			}
		}
		builder.WriteRune(smallest)
	}
	return builder.String()
}

func validRawToken(value string) bool {
	if !strings.HasPrefix(value, tokenPrefix) {
		return false
	}
	encoded := strings.TrimPrefix(value, tokenPrefix)
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != tokenRandomBytes {
		return false
	}
	return base64.RawURLEncoding.EncodeToString(decoded) == encoded
}

func tokenHint(value string) string {
	const visibleSuffix = 6
	return tokenPrefix + "…" + value[len(value)-visibleSuffix:]
}
