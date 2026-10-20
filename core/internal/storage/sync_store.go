package storage

import (
	"context"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// ChangeRecord is one configuration write the change-log trigger captured.
type ChangeRecord struct {
	Kind      string
	EntityID  string
	ChangedAt time.Time
}

// ChangeLogStore reads and clears the configuration change log the sync
// triggers write. The log is gateway-owned and never leaves the device.
type ChangeLogStore interface {
	// PendingChanges counts the configuration writes the sync has not packaged
	// yet, so a device can report how much has changed without reading every
	// row.
	PendingChanges(context.Context) (int, error)
	// ChangesSince returns the rows newer than since, oldest first.
	ChangesSince(context.Context, time.Time) ([]ChangeRecord, error)
	// ClearChangesThrough removes rows at or before through, so a successful
	// sync does not schedule another one.
	ClearChangesThrough(context.Context, time.Time) error
}

// SyncStateRecord is the one row of sync settings and status.
type SyncStateRecord struct {
	Config contract.SyncConfig
	// ForcePushGeneration identifies the currently armed overwrite. Ordinary
	// settings edits preserve it; rearming or changing its target advances it.
	ForcePushGeneration int64
	// PassphraseState is "set" once a sync passphrase produced the cached key.
	PassphraseState string
	// KeySalt and KeyParams are the salt and KDF parameters the cached key was
	// derived with, so a package can be sealed with the cached key without the
	// passphrase again. The salt is derived from the passphrase, so the same
	// passphrase produces the same key on every device.
	KeySalt    []byte
	KeyParams  []byte
	DeviceID   string
	Configured bool
	// RemoteIdentity pins the remote confirmed on the last successful sync.
	RemoteIdentity string
	RemoteETag     string
	RemoteRevision int64
	LastSyncedAt   *time.Time
	LastError      string
	// RetainedThrough is how far deletions have been propagated; a tombstone
	// younger than this may still need uploading.
	RetainedThrough *time.Time
}

// Tombstone records one deletion so a device that still has the entity
// deletes it rather than resurrects it.
type Tombstone struct {
	Kind      string
	SyncID    string
	DeletedAt time.Time
}

// SyncLocalEntity is one row of a kind a package carries, as it stands on this
// device. A document kind fills Document; a credential kind fills Secret with
// the plaintext, which the engine seals into the package.
type SyncLocalEntity struct {
	LocalID  string
	Document []byte
	Secret   []byte
	// UpdatedAt is when this device last changed the row. A package stamps
	// each entity with it, so a conflict between two edits is resolved by
	// which is later rather than by which device synced first.
	UpdatedAt time.Time
}

// SyncStore owns everything the sync engine reads and writes: the settings and
// status row, the cached key, the change log, and the configuration kinds a
// package carries.
//
// A package names an entity by the id this device stores it under, because a
// provider's name is the identity a user recognises across devices. That makes
// the sync a per-device-state problem only for the values that are genuinely
// device-local — a credential's ciphertext and a subscription's token — and
// those are handled explicitly rather than by renaming entities.
type SyncStore interface {
	ChangeLogStore
	// SyncState returns the settings row, creating the default on first read.
	SyncState(context.Context) (SyncStateRecord, error)
	// UpdateSyncConfig replaces the settings.
	UpdateSyncConfig(ctx context.Context, config contract.SyncConfig) error
	// ConsumeForcePush disarms only the overwrite a run completed, preserving
	// other settings and any newer overwrite request.
	ConsumeForcePush(ctx context.Context, generation int64) error

	// PutSyncKey stores the cached key sealed under the local key hierarchy,
	// with the salt and parameters that produced it. It is how automatic sync
	// survives a restart without keeping the passphrase.
	PutSyncKey(ctx context.Context, key, salt, params []byte) error
	// SyncKey returns the cached key and the salt it was derived with. A
	// device without a passphrase reports ErrNotFound.
	SyncKey(ctx context.Context) (key, salt []byte, err error)
	// ClearSyncKey forgets the cached key, so sync stops until the passphrase
	// is entered again.
	ClearSyncKey(ctx context.Context) error

	// RecordSyncResult stores the outcome of one run: the pinned remote, the
	// entity tag, the revision, and the error text. A nil syncedAt leaves the
	// last success untouched, as a failed run does.
	RecordSyncResult(ctx context.Context, remote contract.RemoteIdentity, syncedAt *time.Time, lastError string) error
	// RetainTombstonesThrough advances how far deletions have propagated.
	RetainTombstonesThrough(ctx context.Context, through time.Time) error

	// PutTombstone records a deletion, newest wins.
	PutTombstone(ctx context.Context, kind, syncID string, deletedAt time.Time) error
	// Tombstones lists deletions newer than since, oldest first. A zero since
	// returns every tombstone this device keeps.
	Tombstones(ctx context.Context, since time.Time) ([]Tombstone, error)
	// DeleteTombstone drops one deletion, once the entity is back on this
	// device, so the package never says both that an entity is gone and that
	// it is here.
	DeleteTombstone(ctx context.Context, kind, syncID string) error
	// DeleteTombstonesBefore drops tombstones older than cutoff, which ends
	// their protection against resurrection.
	DeleteTombstonesBefore(ctx context.Context, cutoff time.Time) error

	// PutBaseline stores the last package this device synced, sealed under the
	// secrets key. It is the merge base.
	PutBaseline(ctx context.Context, payload []byte, revision int64) error
	// Baseline returns the stored package. A device that has never synced
	// reports ErrNotFound.
	Baseline(ctx context.Context) (payload []byte, revision int64, err error)
	// HasBaseline reports whether a package is stored, without opening it. A
	// device that has never exchanged a package with its remote has none, which
	// is what makes its next run a first contact.
	HasBaseline(ctx context.Context) (bool, error)
	// ResetBaseline forgets the merge base, for when the remote now holds a
	// package this device cannot open or a different remote is configured.
	// The next merge then treats every remote entity as new.
	ResetBaseline(ctx context.Context) error

	// ExportCredential returns the plaintext of one credential column, for
	// re-sealing into a package. The table and key name the closed set of
	// sealed tables; an unknown pair is refused.
	ExportCredential(ctx context.Context, table, key string) ([]byte, error)
	// ImportCredential re-seals plaintext under this device's secrets key with
	// the AAD of table and key, so a credential copied from another device or
	// onto a new service id opens here.
	ImportCredential(ctx context.Context, table, key string, plaintext []byte) error

	// SyncEntities returns every row of one kind this device holds, with the
	// credential plaintext for a credential kind. It is the local half of a
	// package.
	SyncEntities(ctx context.Context, kind string) ([]SyncLocalEntity, error)
	// SyncServiceOrder returns the provider priority list, in order.
	SyncServiceOrder(ctx context.Context) ([]contract.ServiceID, error)

	// SyncWriteEntity writes one entity a merge decided this device should
	// hold, under its local id. It validates the document first, so a package
	// from another device cannot store a row this device would refuse.
	SyncWriteEntity(ctx context.Context, kind, localID string, document []byte) error
	// SyncDeleteEntity removes one entity a merge decided is gone.
	SyncDeleteEntity(ctx context.Context, kind, localID string) error
	// SyncWriteOrder applies the provider priority list a merge resolved. An id
	// this device does not have is skipped.
	SyncWriteOrder(ctx context.Context, ids []contract.ServiceID) error
	// SyncBindCredentialRefs points every provider's device-local credential
	// references at the rows this device holds, and signs out a subscription
	// account whose token stayed on the other device. It runs after imported
	// credentials are in place.
	SyncBindCredentialRefs(ctx context.Context) error
	// SyncImportCredential re-seals one credential of a kind a package carries
	// under the row this device stores it on.
	SyncImportCredential(ctx context.Context, kind, localID string, plaintext []byte) error
	// SyncDeleteCredential removes one credential of a kind a package carries.
	SyncDeleteCredential(ctx context.Context, kind, localID string) error
	// SyncWriteAccessToken writes one gateway access token a merge decided this
	// device should hold: its metadata row, which carries the digest and the
	// hint an authentication reads, and the value itself, sealed again under
	// this device's keys. The two are written together because the metadata is
	// derived from the value and neither is usable alone.
	SyncWriteAccessToken(ctx context.Context, localID string, document, value []byte) error
}
