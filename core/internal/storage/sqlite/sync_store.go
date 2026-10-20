package sqlite

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// syncKeyAADKey is the primary key the cached sync key is sealed under, so a
// value copied onto another row does not open.
const syncKeyAADKey = "sync"

// syncBaselineKey is the primary key of the one stored merge base.
const syncBaselineKey = "baseline"

// SyncState reads the one row of sync settings and status, creating the
// defaults on first read. The row is always present after that.
func (store *Store) SyncState(ctx context.Context) (storage.SyncStateRecord, error) {
	record := storage.SyncStateRecord{Config: contract.DefaultSyncConfig()}
	// Only new installations opt in by default. A saved configuration from
	// before token sync existed must keep its original scope on upgrade.
	record.Config.Scope.Tokens = false
	var (
		configJSON     string
		keySalt        []byte
		keyParams      []byte
		remoteIdentity sql.NullString
		remoteETag     sql.NullString
		syncedAt       sql.NullString
		retained       sql.NullString
		lastError      sql.NullString
	)
	err := store.db.QueryRowContext(ctx, `SELECT device_id, config_json, passphrase_state,
    key_salt, key_params_json, remote_identity, remote_etag, remote_revision,
    last_synced_at, last_error, retained_through, force_push_generation
FROM sync_state WHERE id = 1`).Scan(
		&record.DeviceID, &configJSON, &record.PassphraseState,
		&keySalt, &keyParams, &remoteIdentity, &remoteETag, &record.RemoteRevision,
		&syncedAt, &lastError, &retained, &record.ForcePushGeneration,
	)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := store.ensureSyncState(ctx); err != nil {
			return storage.SyncStateRecord{}, err
		}
		return store.SyncState(ctx)
	case err != nil:
		return storage.SyncStateRecord{}, fmt.Errorf("read sync state: %w", err)
	}
	if err := json.Unmarshal([]byte(configJSON), &record.Config); err != nil {
		return storage.SyncStateRecord{}, fmt.Errorf("%w: sync config", storage.ErrInvalidRecord)
	}
	record.KeySalt = keySalt
	record.KeyParams = keyParams
	record.RemoteIdentity = remoteIdentity.String
	record.RemoteETag = remoteETag.String
	record.LastError = lastError.String
	record.Configured = record.Config.Remote.Kind != ""
	record.LastSyncedAt = parseOptionalTime(syncedAt)
	record.RetainedThrough = parseOptionalTime(retained)
	return record, nil
}

// ensureSyncState writes the one row once, with a fresh device identity.
func (store *Store) ensureSyncState(ctx context.Context) error {
	random, err := randomHex(16)
	if err != nil {
		return err
	}
	config, err := json.Marshal(contract.DefaultSyncConfig())
	if err != nil {
		return err
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO sync_state (id, device_id, config_json)
SELECT 1, ?, ? WHERE NOT EXISTS (SELECT 1 FROM sync_state WHERE id = 1)`,
		"device_"+random, string(config))
	if err != nil {
		return fmt.Errorf("create sync state: %w", err)
	}
	return nil
}

// UpdateSyncConfig replaces the settings row.
func (store *Store) UpdateSyncConfig(ctx context.Context, config contract.SyncConfig) error {
	if err := contract.ValidateSyncConfig(config); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	if err := store.ensureSyncState(ctx); err != nil {
		return err
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return err
	}
	// Compare against the row being updated, not an earlier settings read.
	// Editing a name or schedule must not rearm an in-flight overwrite; a new
	// target or scope does describe a different overwrite request.
	if _, err := store.db.ExecContext(ctx, `UPDATE sync_state SET
    force_push_generation = force_push_generation + CASE
        WHEN json_extract(?1, '$.force_push') = 1 AND (
            COALESCE(json_extract(config_json, '$.force_push'), 0) <> 1
            OR json_extract(config_json, '$.remote') IS NOT json_extract(?1, '$.remote')
            OR json_extract(config_json, '$.scope') IS NOT json_extract(?1, '$.scope')
        ) THEN 1 ELSE 0 END,
    config_json = ?1
WHERE id = 1`, string(encoded)); err != nil {
		return fmt.Errorf("write sync config: %w", err)
	}
	return nil
}

// ConsumeForcePush changes only the one-shot flag, and only for the generation
// the completed run observed. A concurrent save can neither be reverted nor
// have a newly armed force push cleared by the older run.
func (store *Store) ConsumeForcePush(ctx context.Context, generation int64) error {
	if _, err := store.db.ExecContext(ctx, `UPDATE sync_state
SET config_json = json_set(config_json, '$.force_push', json('false'))
WHERE id = 1 AND force_push_generation = ? AND json_extract(config_json, '$.force_push') = 1`, generation); err != nil {
		return fmt.Errorf("consume sync force push: %w", err)
	}
	return nil
}

// PutSyncKey caches the derived key sealed under the secrets key, so automatic
// sync survives a restart without keeping the passphrase. It also marks the
// passphrase as set.
func (store *Store) PutSyncKey(ctx context.Context, key, salt, params []byte) error {
	if len(key) != 32 || len(salt) == 0 || len(params) == 0 {
		return fmt.Errorf("%w: sync key material", storage.ErrInvalidArgument)
	}
	if err := store.ensureSyncState(ctx); err != nil {
		return err
	}
	sealed, err := store.keys.sealColumn(syncKeyMaterialTable, syncKeyAADKey, key)
	if err != nil {
		return fmt.Errorf("seal sync key: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE sync_state SET
    passphrase_state = 'set', key_salt = ?, key_params_json = ? WHERE id = 1`,
		salt, params); err != nil {
		return fmt.Errorf("write sync key parameters: %w", err)
	}
	if err := store.putSyncKeyMaterial(ctx, sealed); err != nil {
		return err
	}
	return nil
}

// SyncKey returns the cached key and its salt. A device with no passphrase
// reports ErrNotFound, so the engine knows sync has to wait for one.
func (store *Store) SyncKey(ctx context.Context) (key, salt []byte, err error) {
	var stored []byte
	if err := store.db.QueryRowContext(ctx, `SELECT credential_value FROM sync_key_material WHERE key = ?`, syncKeyAADKey).Scan(&stored); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, fmt.Errorf("%w: sync passphrase", secretstore.ErrNotFound)
		}
		return nil, nil, fmt.Errorf("read sync key: %w", err)
	}
	key, err = store.openSecret(syncKeyMaterialTable, syncKeyAADKey, stored, true)
	if err != nil {
		return nil, nil, err
	}
	if err := store.db.QueryRowContext(ctx, `SELECT key_salt FROM sync_state WHERE id = 1`).Scan(&salt); err != nil {
		clear(key)
		return nil, nil, fmt.Errorf("read sync salt: %w", err)
	}
	return key, salt, nil
}

// ClearSyncKey forgets the cached key and the cached salt and parameters. The
// remote configuration stays, so entering the passphrase again resumes sync.
func (store *Store) ClearSyncKey(ctx context.Context) error {
	if err := store.ensureSyncState(ctx); err != nil {
		return err
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM sync_key_material WHERE key = ?`, syncKeyAADKey); err != nil {
		return fmt.Errorf("clear sync key: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE sync_state SET
    passphrase_state = 'unset', key_salt = NULL, key_params_json = NULL WHERE id = 1`); err != nil {
		return fmt.Errorf("clear sync key parameters: %w", err)
	}
	return nil
}

// putSyncKeyMaterial writes the sealed key in one statement.
func (store *Store) putSyncKeyMaterial(ctx context.Context, sealed []byte) error {
	now := store.now().UTC().Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO sync_key_material (key, credential_value, created_at, updated_at, sealed)
VALUES (?, ?, ?, ?, 1)
ON CONFLICT(key) DO UPDATE SET credential_value = excluded.credential_value, updated_at = excluded.updated_at, sealed = 1`,
		syncKeyAADKey, sealed, now, now); err != nil {
		return fmt.Errorf("write sync key material: %w", err)
	}
	return nil
}

// RecordSyncResult stores the outcome of one run.
func (store *Store) RecordSyncResult(ctx context.Context, remote contract.RemoteIdentity, syncedAt *time.Time, lastError string) error {
	if err := store.ensureSyncState(ctx); err != nil {
		return err
	}
	var synced any
	if syncedAt != nil {
		synced = syncedAt.UTC().Format(time.RFC3339Nano)
	}
	var identity, etag any
	if remote.Identity != "" {
		identity = remote.Identity
	}
	if remote.ETag != "" {
		etag = remote.ETag
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE sync_state SET
    remote_identity = COALESCE(?, remote_identity),
    remote_etag = COALESCE(?, remote_etag),
    remote_revision = ?,
    last_synced_at = COALESCE(?, last_synced_at),
    last_error = ? WHERE id = 1`,
		identity, etag, remote.Revision, synced, lastError); err != nil {
		return fmt.Errorf("record sync result: %w", err)
	}
	return nil
}

// RetainTombstonesThrough advances how far deletions have propagated.
func (store *Store) RetainTombstonesThrough(ctx context.Context, through time.Time) error {
	if err := store.ensureSyncState(ctx); err != nil {
		return err
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE sync_state SET retained_through = ? WHERE id = 1`,
		through.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("retain tombstones: %w", err)
	}
	return nil
}

// PendingChanges counts configuration writes the sync has not packaged yet.
func (store *Store) PendingChanges(ctx context.Context) (int, error) {
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM config_change_log`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count pending changes: %w", err)
	}
	return count, nil
}

// changeStampLayout is the layout the change-log triggers write, and the one
// every comparison against the log has to use.
//
// The log is compared as text, so a variable-width stamp is wrong: Go writes
// RFC3339Nano without trailing zeros, and "12:00:00Z" sorts after
// "12:00:00.5Z" while being earlier in fact. A fixed nine-digit fraction makes
// the text order the time order.
const changeStampLayout = "2006-01-02T15:04:05.000000000Z"

func formatChangeStamp(at time.Time) string {
	return at.UTC().Format(changeStampLayout)
}

// ChangesSince returns the log rows newer than since, oldest first. It also
// collapses repeated edits of one entity, keeping the newest.
func (store *Store) ChangesSince(ctx context.Context, since time.Time) ([]storage.ChangeRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT entity_kind, entity_id, MAX(changed_at)
FROM config_change_log
WHERE changed_at > ?
GROUP BY entity_kind, entity_id
ORDER BY MAX(changed_at)`, formatChangeStamp(since))
	if err != nil {
		return nil, fmt.Errorf("read changed entities: %w", err)
	}
	defer rows.Close()
	var changes []storage.ChangeRecord
	for rows.Next() {
		var record storage.ChangeRecord
		var changedAt string
		if err := rows.Scan(&record.Kind, &record.EntityID, &changedAt); err != nil {
			return nil, fmt.Errorf("read changed entity: %w", err)
		}
		if parsed, err := time.Parse(changeStampLayout, changedAt); err == nil {
			record.ChangedAt = parsed
		}
		changes = append(changes, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read changed entities: %w", err)
	}
	return changes, nil
}

// ClearChangesThrough removes log rows at or before through, so a successful
// sync does not schedule another one.
func (store *Store) ClearChangesThrough(ctx context.Context, through time.Time) error {
	if _, err := store.db.ExecContext(ctx, `DELETE FROM config_change_log WHERE changed_at <= ?`,
		formatChangeStamp(through)); err != nil {
		return fmt.Errorf("clear change log: %w", err)
	}
	return nil
}

// PutTombstone records a deletion. The newest deletion for one entity wins, so
// a re-delete after a failed run does not fight an earlier one.
func (store *Store) PutTombstone(ctx context.Context, kind, syncID string, deletedAt time.Time) error {
	if kind == "" || syncID == "" {
		return fmt.Errorf("%w: tombstone", storage.ErrInvalidArgument)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO sync_tombstone (entity_kind, sync_id, deleted_at)
VALUES (?, ?, ?)
ON CONFLICT(entity_kind, sync_id) DO UPDATE SET deleted_at = MAX(deleted_at, excluded.deleted_at)`,
		kind, syncID, formatChangeStamp(deletedAt)); err != nil {
		return fmt.Errorf("write tombstone: %w", err)
	}
	return nil
}

// Tombstones lists deletions newer than since, oldest first.
func (store *Store) Tombstones(ctx context.Context, since time.Time) ([]storage.Tombstone, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT entity_kind, sync_id, deleted_at FROM sync_tombstone
WHERE deleted_at > ? ORDER BY deleted_at`, formatChangeStamp(since))
	if err != nil {
		return nil, fmt.Errorf("read tombstones: %w", err)
	}
	defer rows.Close()
	var tombstones []storage.Tombstone
	for rows.Next() {
		var tombstone storage.Tombstone
		var deletedAt string
		if err := rows.Scan(&tombstone.Kind, &tombstone.SyncID, &deletedAt); err != nil {
			return nil, fmt.Errorf("read tombstone: %w", err)
		}
		tombstone.DeletedAt, _ = time.Parse(changeStampLayout, deletedAt)
		tombstones = append(tombstones, tombstone)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read tombstones: %w", err)
	}
	return tombstones, nil
}

// DeleteTombstone drops one deletion, once the entity is back on this device.
func (store *Store) DeleteTombstone(ctx context.Context, kind, syncID string) error {
	if _, err := store.db.ExecContext(ctx, `DELETE FROM sync_tombstone WHERE entity_kind = ? AND sync_id = ?`, kind, syncID); err != nil {
		return fmt.Errorf("delete tombstone: %w", err)
	}
	return nil
}

// DeleteTombstonesBefore drops tombstones older than cutoff.
func (store *Store) DeleteTombstonesBefore(ctx context.Context, cutoff time.Time) error {
	if _, err := store.db.ExecContext(ctx, `DELETE FROM sync_tombstone WHERE deleted_at < ?`,
		formatChangeStamp(cutoff)); err != nil {
		return fmt.Errorf("drop tombstones: %w", err)
	}
	return nil
}

// PutBaseline stores the last package this device synced, sealed under the
// secrets key, so a copy of the database does not read it without the local
// key. It is the merge base three-way merges compare against.
func (store *Store) PutBaseline(ctx context.Context, payload []byte, revision int64) error {
	if len(payload) == 0 {
		return fmt.Errorf("%w: empty baseline", storage.ErrInvalidArgument)
	}
	sealed, err := store.keys.sealColumn(syncBaselineTable, syncBaselineKey, payload)
	if err != nil {
		return fmt.Errorf("seal sync baseline: %w", err)
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO sync_baseline (key, payload_blob, revision, updated_at, sealed)
VALUES (?, ?, ?, ?, 1)
ON CONFLICT(key) DO UPDATE SET payload_blob = excluded.payload_blob, revision = excluded.revision, updated_at = excluded.updated_at, sealed = 1`,
		syncBaselineKey, sealed, revision, now); err != nil {
		return fmt.Errorf("write sync baseline: %w", err)
	}
	return nil
}

// Baseline returns the stored package. A device that has never synced reports
// ErrNotFound.
func (store *Store) Baseline(ctx context.Context) (payload []byte, revision int64, err error) {
	var sealed []byte
	if err := store.db.QueryRowContext(ctx, `SELECT payload_blob, revision FROM sync_baseline WHERE key = ?`,
		syncBaselineKey).Scan(&sealed, &revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, 0, fmt.Errorf("%w: sync baseline", secretstore.ErrNotFound)
		}
		return nil, 0, fmt.Errorf("read sync baseline: %w", err)
	}
	payload, err = store.openSecret(syncBaselineTable, syncBaselineKey, sealed, true)
	if err != nil {
		return nil, 0, err
	}
	return payload, revision, nil
}

// HasBaseline reports whether a merge base is stored. It answers the question
// the status asks without opening the package, which the status does not need.
func (store *Store) HasBaseline(ctx context.Context) (bool, error) {
	var stored bool
	if err := store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sync_baseline WHERE key = ?)`,
		syncBaselineKey).Scan(&stored); err != nil {
		return false, fmt.Errorf("read sync baseline presence: %w", err)
	}
	return stored, nil
}

// ResetBaseline forgets the merge base. It is what the engine calls when the
// remote turns out to hold a package this device cannot open, or when the
// remote it points at changes: without a base every remote entity is treated as
// new, which is a merge this device can still make.
func (store *Store) ResetBaseline(ctx context.Context) error {
	if _, err := store.db.ExecContext(ctx, `DELETE FROM sync_baseline WHERE key = ?`, syncBaselineKey); err != nil {
		return fmt.Errorf("reset sync baseline: %w", err)
	}
	return nil
}

// ExportCredential returns one credential's plaintext for re-sealing into a
// package. Only the closed set of sealed tables is accepted.
func (store *Store) ExportCredential(ctx context.Context, table, key string) ([]byte, error) {
	if !syncExportableTable(table) {
		return nil, fmt.Errorf("%w: %s is not exportable", storage.ErrInvalidArgument, table)
	}
	var stored []byte
	if err := store.db.QueryRowContext(ctx, `SELECT credential_value FROM `+table+` WHERE `+syncTableKeyColumn(table)+` = ?`, key).Scan(&stored); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s %s", secretstore.ErrNotFound, table, key)
		}
		return nil, fmt.Errorf("read %s: %w", table, err)
	}
	return store.openSecret(table, key, stored, true)
}

// ImportCredential re-seals plaintext under this device's secrets key with the
// AAD of table and key. The value copied from another device or written under
// a new service id opens here because the sealing happens now, on this device.
// The owning service must exist first: every credential table cascades from
// the services table.
func (store *Store) ImportCredential(ctx context.Context, table, key string, plaintext []byte) error {
	if !syncExportableTable(table) {
		return fmt.Errorf("%w: %s is not importable", storage.ErrInvalidArgument, table)
	}
	if len(plaintext) == 0 {
		return fmt.Errorf("%w: empty credential", storage.ErrInvalidArgument)
	}
	// A credential that already holds this exact value is left alone. Re-sealing
	// it would write a new ciphertext — the nonce differs every time — and the
	// change-log trigger would record a change that never happened, scheduling
	// another sync for it. A value this device cannot read is rewritten, which
	// is also how a row left sealed under an older key heals.
	if existing, err := store.ExportCredential(ctx, table, key); err == nil {
		same := bytes.Equal(existing, plaintext)
		clear(existing)
		if same {
			return nil
		}
	}
	sealed, err := store.keys.sealColumn(table, key, plaintext)
	if err != nil {
		return fmt.Errorf("seal imported credential: %w", err)
	}
	statement, err := syncImportStatement(table)
	if err != nil {
		return err
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	args := []any{key, sealed, now}
	if table == serviceCredentialsTable {
		// service_credentials keeps both a created_at and an updated_at.
		args = append(args, now)
	}
	if _, err := store.db.ExecContext(ctx, statement, args...); err != nil {
		return fmt.Errorf("write imported credential: %w", err)
	}
	return nil
}

// syncImportStatement returns the upsert for one credential table. The
// arguments are the row key, the sealed value, and a timestamp the tables that
// carry one use.
func syncImportStatement(table string) (string, error) {
	switch table {
	case serviceCredentialsTable:
		return `INSERT INTO service_credentials (service_id, credential_value, created_at, updated_at, sealed)
VALUES (?, ?, ?, ?, 1)
ON CONFLICT(service_id) DO UPDATE SET credential_value = excluded.credential_value, updated_at = excluded.updated_at, sealed = 1`, nil
	case serviceProxyCredentialsTable:
		return `INSERT INTO service_proxy_credentials (service_id, credential_value, sealed)
VALUES (?, ?, 1)
ON CONFLICT(service_id) DO UPDATE SET credential_value = excluded.credential_value, sealed = 1`, nil
	case builtinToolCredentialsTable:
		return `INSERT INTO builtin_tool_credentials (kind, credential_value, sealed)
VALUES (?, ?, 1)
ON CONFLICT(kind) DO UPDATE SET credential_value = excluded.credential_value, sealed = 1`, nil
	case subscriptionCredentialsTable:
		return `INSERT INTO subscription_credentials (service_id, credential_value, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(service_id) DO UPDATE SET credential_value = excluded.credential_value, updated_at = excluded.updated_at`, nil
	default:
		return "", fmt.Errorf("%w: %s is not importable", storage.ErrInvalidArgument, table)
	}
}

// syncExportableTable names the credential tables a package may carry.
func syncExportableTable(table string) bool {
	switch table {
	case serviceCredentialsTable, serviceProxyCredentialsTable, builtinToolCredentialsTable, subscriptionCredentialsTable:
		return true
	default:
		return false
	}
}

func syncTableKeyColumn(table string) string {
	switch table {
	case builtinToolCredentialsTable:
		return "kind"
	default:
		return "service_id"
	}
}

// parseOptionalTime reads a nullable timestamp column.
func parseOptionalTime(value sql.NullString) *time.Time {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil
	}
	return &parsed
}

func randomHex(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// parseEntityTime reads the timestamp an entity query returned. An unreadable
// stamp becomes the zero time, which makes the entity look unchanged: a merge
// then keeps whichever side carries a time, rather than treating a damaged
// stamp as a fresh edit.
func parseEntityTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

var _ storage.SyncStore = (*Store)(nil)
