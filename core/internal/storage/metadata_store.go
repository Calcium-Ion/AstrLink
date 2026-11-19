package storage

import (
	"context"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Targets a metadata value is kept for.
const (
	MetadataTargetToken   = "token"
	MetadataTargetSession = "session"
)

// MetadataValueRecord is a value kept for an access token (its default) or a
// session (its binding). Values from request headers stay in request
// records.
type MetadataValueRecord struct {
	TargetKind string
	TargetID   string
	Key        string
	Value      string
	// SetBy is token_default, model_marker, user_marker or manual.
	SetBy string
	// Turn is the session turn a binding was set in, 0 for none.
	Turn      int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MetadataValueStat is a value sessions are bound to: how many, and when
// one of them last made a request, nil when none did.
type MetadataValueStat struct {
	Value      string
	Sessions   int
	LastUsedAt *time.Time
}

// MetadataWorkspaceRecord is the value a workspace was bound to, for the
// sessions that start there. Workspace is a keyed fingerprint of the
// directory's path and Name its last element; the path is not kept.
type MetadataWorkspaceRecord struct {
	Key       string
	Workspace string
	Name      string
	Value     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type MetadataStore interface {
	ListMetadataAttributes(context.Context) ([]contract.MetadataAttribute, error)
	// CreateMetadataAttribute returns ErrConflict when the key or the marker
	// is taken.
	CreateMetadataAttribute(context.Context, contract.MetadataAttribute) error
	// UpdateMetadataAttribute changes everything but the key and the owner;
	// ErrNotFound for a missing key, ErrConflict for a marker taken.
	UpdateMetadataAttribute(context.Context, contract.MetadataAttribute) error
	// DeleteMetadataAttribute removes the attribute and every value kept for
	// it; ErrNotFound for a missing key.
	DeleteMetadataAttribute(ctx context.Context, key string) error
	// RegisterPluginAttributes adds or updates the attributes plugin
	// registers. A key or a marker the user or another plugin owns is
	// ErrConflict, and then nothing changes.
	RegisterPluginAttributes(ctx context.Context, plugin string, attributes []contract.MetadataAttribute) error

	// MetadataValues returns the values kept for one target, by key.
	MetadataValues(ctx context.Context, targetKind, targetID string) (map[string]MetadataValueRecord, error)
	// ListMetadataValues returns every value kept for one kind of target.
	ListMetadataValues(ctx context.Context, targetKind string) ([]MetadataValueRecord, error)
	// MetadataValueStats returns the values of key sessions are bound to,
	// the most recently used first, at most limit.
	MetadataValueStats(ctx context.Context, key string, limit int) ([]MetadataValueStat, error)
	PutMetadataValue(context.Context, MetadataValueRecord) error
	DeleteMetadataValue(ctx context.Context, targetKind, targetID, key string) error
	// ReplaceTokenMetadata makes values the whole of a token's defaults.
	ReplaceTokenMetadata(ctx context.Context, tokenID contract.AccessTokenID, values map[string]string) error

	// WorkspaceValues returns the values a workspace is bound to, by key.
	WorkspaceValues(ctx context.Context, workspace string) (map[string]MetadataWorkspaceRecord, error)
	// PutWorkspaceValue binds a workspace's value of an attribute. Without
	// replace, a value bound before stays; bound reports whether record is
	// what the workspace is bound to now.
	PutWorkspaceValue(ctx context.Context, record MetadataWorkspaceRecord, replace bool) (bound bool, err error)
	// CountWorkspaceValues counts the workspaces bound to a value of any of
	// keys, and DeleteWorkspaceValues removes them.
	CountWorkspaceValues(ctx context.Context, keys []string) (int, error)
	DeleteWorkspaceValues(ctx context.Context, keys []string) (int, error)
}
