package storage

import (
	"context"
	"time"
)

// RawKeyEnvelope is one stored wrapping of the raw sealing private key.
type RawKeyEnvelope struct {
	// Kind is "password".
	Kind string
	// KDFJSON and Salt are the Argon2id parameters and salt.
	KDFJSON   string
	Salt      []byte
	Nonce     []byte
	Wrapped   []byte
	CreatedAt time.Time
}

// RawSealingState is the stored raw sealing key without its private half.
type RawSealingState struct {
	KeyID     int64
	PublicKey []byte
	CreatedAt time.Time
	// MACValid is false when pk_mac does not verify under the current
	// audit key. Raw captures are then not kept until a proof shows the
	// pair is intact.
	MACValid bool
	Password *RawKeyEnvelope
}

// NewRawSealingKey is a key pair ready to store, already wrapped.
type NewRawSealingKey struct {
	KeyID     int64
	PublicKey []byte
	Envelopes []RawKeyEnvelope
}

// RawResealResult counts one reseal pass.
type RawResealResult struct {
	Resealed int
	// Dropped counts parts whose content was discarded instead: they
	// settled as raw, or never settled, while no raw password was set.
	Dropped int
	// Done is true when the pass reached the end; parts that do not decrypt
	// under the audit key are skipped and stay where they are.
	Done bool
}

// RawResetResult counts what a raw sealing reset discarded.
type RawResetResult struct {
	DeletedParts    int
	AffectedRecords int
}

// RawSealingStore keeps the raw sealing key and moves raw parts onto it.
type RawSealingStore interface {
	// LoadRawSealing returns the stored key, or ErrNotFound when none exists.
	LoadRawSealing(context.Context) (RawSealingState, error)
	// CreateRawSealingKey stores the first key. It reports ErrConflict when a
	// key already exists.
	CreateRawSealingKey(context.Context, NewRawSealingKey) error
	// PutRawKeyEnvelope adds or replaces one envelope of the stored key.
	PutRawKeyEnvelope(context.Context, int64, RawKeyEnvelope) error
	// RefreshRawSealingMAC re-authenticates the stored public key under the
	// current audit key after a proof showed the pair is intact.
	RefreshRawSealingMAC(context.Context, int64) error
	// ReplaceRawSealingKey discards every raw_v1 part and the old key and
	// stores a new one, clearing the captured flags of affected records.
	ReplaceRawSealingKey(context.Context, NewRawSealingKey) (RawResetResult, error)
	// ClearRawSealingKey discards like ReplaceRawSealingKey but stores no
	// new key, so no raw password is set until one is set again.
	ClearRawSealingKey(context.Context) (RawResetResult, error)
	// ResealRawParts moves every raw part still sealed under the audit key
	// onto the raw sealing key, committing at most limit parts at a time.
	// Without a raw password it only drops the content of pending parts whose
	// request ended, since nothing will settle them any more.
	ResealRawParts(context.Context, int) (RawResealResult, error)
	// OnResealDeferred registers retry, which the store calls when a part
	// that settled as raw could not be moved onto the raw sealing key at
	// once. nil unregisters it.
	OnResealDeferred(retry func())
}
