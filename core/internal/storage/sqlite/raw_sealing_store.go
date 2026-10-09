package sqlite

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/rawseal"
)

const (
	// resealBatchParts and resealBatchBytes bound one reseal transaction so
	// captures waiting for the writer lock are not starved.
	resealBatchParts = 100
	resealBatchBytes = 16 << 20
)

// rawSealingKey caches the verified public key, so a raw capture seals to
// it without reading the database. password records whether a raw password
// envelope protects the key: only then are new raw captures kept.
type rawSealingKey struct {
	mu       sync.RWMutex
	id       int64
	public   []byte
	password bool
}

func (cache *rawSealingKey) get() (int64, []byte) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if cache.id == 0 {
		return 0, nil
	}
	return cache.id, append([]byte(nil), cache.public...)
}

// captureKey returns the key new raw captures seal to: none until a raw
// password protects it.
func (cache *rawSealingKey) captureKey() (int64, []byte) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if cache.id == 0 || !cache.password {
		return 0, nil
	}
	return cache.id, append([]byte(nil), cache.public...)
}

func (cache *rawSealingKey) set(id int64, public []byte, password bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.id = id
	cache.public = append([]byte(nil), public...)
	cache.password = password
}

// notePassword records that key id gained a raw password envelope.
func (cache *rawSealingKey) notePassword(id int64) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.id == id {
		cache.password = true
	}
}

// HasRawSealingKey reports whether a verified raw sealing key is loaded,
// with or without a raw password.
func (store *Store) HasRawSealingKey() bool {
	id, _ := store.rawKey.get()
	return id != 0
}

// keepsRawCaptures reports whether new raw captures are kept: a verified
// raw sealing key is loaded and a raw password protects it.
func (store *Store) keepsRawCaptures() bool {
	id, _ := store.rawKey.captureKey()
	return id != 0
}

func hasPasswordEnvelope(envelopes []storagecontract.RawKeyEnvelope) bool {
	for _, envelope := range envelopes {
		if envelope.Kind == rawseal.KindPassword {
			return true
		}
	}
	return false
}

// loadRawPublicKey caches the stored public key when its MAC verifies under
// dek_audit. A key that does not verify is left for a proof to vouch for.
func (store *Store) loadRawPublicKey(ctx context.Context, logf func(string, ...any)) error {
	state, err := store.LoadRawSealing(ctx)
	if errors.Is(err, storagecontract.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !state.MACValid {
		logf("astrlink storage: the raw sealing key does not match this device's audit key; raw captures are not kept until the raw password is entered")
		return nil
	}
	store.rawKey.set(state.KeyID, state.PublicKey, state.Password != nil)
	return nil
}

func (store *Store) publicKeyMAC(public []byte) ([]byte, error) {
	auditKey := store.keys.audit()
	if auditKey == nil {
		return nil, fmt.Errorf("%w: audit key", storagecontract.ErrNotFound)
	}
	defer clear(auditKey)
	return rawseal.PublicKeyMAC(auditKey, public), nil
}

func (store *Store) LoadRawSealing(ctx context.Context) (storagecontract.RawSealingState, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id, public_key, pk_mac, created_at FROM raw_sealing_keys LIMIT 2`)
	if err != nil {
		return storagecontract.RawSealingState{}, fmt.Errorf("read raw sealing key: %w", err)
	}
	var (
		state storagecontract.RawSealingState
		mac   []byte
		found int
	)
	for rows.Next() {
		var createdAt string
		if err := rows.Scan(&state.KeyID, &state.PublicKey, &mac, &createdAt); err != nil {
			rows.Close()
			return storagecontract.RawSealingState{}, fmt.Errorf("scan raw sealing key: %w", err)
		}
		created, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			rows.Close()
			return storagecontract.RawSealingState{}, fmt.Errorf("%w: raw sealing key created_at", storagecontract.ErrInvalidRecord)
		}
		state.CreatedAt = created.UTC()
		found++
	}
	if err := rows.Close(); err != nil {
		return storagecontract.RawSealingState{}, err
	}
	if err := rows.Err(); err != nil {
		return storagecontract.RawSealingState{}, fmt.Errorf("iterate raw sealing keys: %w", err)
	}
	switch found {
	case 0:
		return storagecontract.RawSealingState{}, storagecontract.ErrNotFound
	case 1:
	default:
		return storagecontract.RawSealingState{}, fmt.Errorf("%w: more than one raw sealing key", storagecontract.ErrInvalidRecord)
	}
	expected, err := store.publicKeyMAC(state.PublicKey)
	if err != nil && !errors.Is(err, storagecontract.ErrNotFound) {
		return storagecontract.RawSealingState{}, err
	}
	state.MACValid = expected != nil && hmac.Equal(expected, mac)

	envelopeRows, err := store.db.QueryContext(ctx, `SELECT kind, kdf_json, salt, nonce, wrapped, created_at
FROM raw_key_envelopes WHERE key_id = ?`, state.KeyID)
	if err != nil {
		return storagecontract.RawSealingState{}, fmt.Errorf("read raw key envelopes: %w", err)
	}
	defer envelopeRows.Close()
	for envelopeRows.Next() {
		var (
			envelope  storagecontract.RawKeyEnvelope
			kdfJSON   sql.NullString
			createdAt string
		)
		if err := envelopeRows.Scan(&envelope.Kind, &kdfJSON, &envelope.Salt, &envelope.Nonce, &envelope.Wrapped, &createdAt); err != nil {
			return storagecontract.RawSealingState{}, fmt.Errorf("scan raw key envelope: %w", err)
		}
		envelope.KDFJSON = kdfJSON.String
		created, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return storagecontract.RawSealingState{}, fmt.Errorf("%w: raw key envelope created_at", storagecontract.ErrInvalidRecord)
		}
		envelope.CreatedAt = created.UTC()
		switch envelope.Kind {
		case rawseal.KindPassword:
			state.Password = &envelope
		}
	}
	if err := envelopeRows.Err(); err != nil {
		return storagecontract.RawSealingState{}, fmt.Errorf("iterate raw key envelopes: %w", err)
	}
	return state, nil
}

func validateNewRawSealingKey(key storagecontract.NewRawSealingKey) error {
	if key.KeyID <= 0 || len(key.PublicKey) != rawseal.PublicKeyBytes {
		return fmt.Errorf("%w: raw sealing key", storagecontract.ErrInvalidArgument)
	}
	seen := map[string]bool{}
	for _, envelope := range key.Envelopes {
		if err := validateRawKeyEnvelope(envelope); err != nil {
			return err
		}
		if seen[envelope.Kind] {
			return fmt.Errorf("%w: duplicate raw key envelope", storagecontract.ErrInvalidArgument)
		}
		seen[envelope.Kind] = true
	}
	return nil
}

func validateRawKeyEnvelope(envelope storagecontract.RawKeyEnvelope) error {
	if len(envelope.Nonce) != storagecontract.AuditNonceBytes || len(envelope.Wrapped) == 0 {
		return fmt.Errorf("%w: raw key envelope", storagecontract.ErrInvalidArgument)
	}
	switch envelope.Kind {
	case rawseal.KindPassword:
		if _, err := rawseal.ParseKDFJSON(envelope.KDFJSON); err != nil || len(envelope.Salt) != rawseal.SaltBytes {
			return fmt.Errorf("%w: raw password envelope", storagecontract.ErrInvalidArgument)
		}
	default:
		return fmt.Errorf("%w: raw key envelope kind", storagecontract.ErrInvalidArgument)
	}
	return nil
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func putRawKeyEnvelope(ctx context.Context, executor execer, keyID int64, envelope storagecontract.RawKeyEnvelope, stamp string) (int64, error) {
	var kdfJSON any
	if envelope.KDFJSON != "" {
		kdfJSON = envelope.KDFJSON
	}
	var salt any
	if envelope.Salt != nil {
		salt = envelope.Salt
	}
	result, err := executor.ExecContext(ctx, `INSERT INTO raw_key_envelopes (key_id, kind, kdf_json, salt, nonce, wrapped, created_at)
SELECT ?1, ?2, ?3, ?4, ?5, ?6, ?7 WHERE EXISTS (SELECT 1 FROM raw_sealing_keys WHERE id = ?1)
ON CONFLICT(key_id, kind) DO UPDATE SET
    kdf_json = excluded.kdf_json,
    salt = excluded.salt,
    nonce = excluded.nonce,
    wrapped = excluded.wrapped,
    created_at = excluded.created_at`, keyID, envelope.Kind, kdfJSON, salt, envelope.Nonce, envelope.Wrapped, stamp)
	if err != nil {
		return 0, fmt.Errorf("write raw key envelope: %w", err)
	}
	return result.RowsAffected()
}

func (store *Store) insertRawSealingKey(ctx context.Context, executor execer, key storagecontract.NewRawSealingKey, mac []byte, stamp string) error {
	result, err := executor.ExecContext(ctx, `INSERT INTO raw_sealing_keys (id, public_key, pk_mac, created_at)
SELECT ?, ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM raw_sealing_keys)`, key.KeyID, key.PublicKey, mac, stamp)
	if err != nil {
		return fmt.Errorf("insert raw sealing key: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		return fmt.Errorf("%w: raw sealing key", storagecontract.ErrConflict)
	}
	for _, envelope := range key.Envelopes {
		if _, err := putRawKeyEnvelope(ctx, executor, key.KeyID, envelope, stamp); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) CreateRawSealingKey(ctx context.Context, key storagecontract.NewRawSealingKey) (err error) {
	if err := validateNewRawSealingKey(key); err != nil {
		return err
	}
	mac, err := store.publicKeyMAC(key.PublicKey)
	if err != nil {
		return err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin raw sealing key: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	if err = store.insertRawSealingKey(ctx, transaction, key, mac, store.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit raw sealing key: %w", err)
	}
	store.rawKey.set(key.KeyID, key.PublicKey, hasPasswordEnvelope(key.Envelopes))
	return nil
}

// PutRawKeyEnvelope writes on a secure-delete connection: a replaced
// password envelope opens with the old password, so its bytes are zeroed
// rather than left in free pages.
func (store *Store) PutRawKeyEnvelope(ctx context.Context, keyID int64, envelope storagecontract.RawKeyEnvelope) error {
	if err := validateRawKeyEnvelope(envelope); err != nil {
		return err
	}
	return store.withSecureDelete(ctx, true, func(conn *sql.Conn) error {
		written, err := putRawKeyEnvelope(ctx, conn, keyID, envelope, store.now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		if written == 0 {
			return fmt.Errorf("%w: raw sealing key", storagecontract.ErrNotFound)
		}
		if envelope.Kind == rawseal.KindPassword {
			store.rawKey.notePassword(keyID)
		}
		return nil
	})
}

func (store *Store) RefreshRawSealingMAC(ctx context.Context, keyID int64) error {
	var (
		public   []byte
		password bool
	)
	err := store.db.QueryRowContext(ctx, `SELECT public_key,
    EXISTS (SELECT 1 FROM raw_key_envelopes WHERE key_id = raw_sealing_keys.id AND kind = 'password')
FROM raw_sealing_keys WHERE id = ?`, keyID).Scan(&public, &password)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: raw sealing key", storagecontract.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("read raw sealing key: %w", err)
	}
	mac, err := store.publicKeyMAC(public)
	if err != nil {
		return err
	}
	result, err := store.db.ExecContext(ctx, `UPDATE raw_sealing_keys SET pk_mac = ? WHERE id = ? AND public_key = ?`, mac, keyID, public)
	if err != nil {
		return fmt.Errorf("refresh raw sealing key mac: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed == 0 {
		return fmt.Errorf("%w: raw sealing key changed", storagecontract.ErrPrecondition)
	}
	store.rawKey.set(keyID, public, password)
	return nil
}

// capturedFlagPaths are the audit summary flags of each part a reset drops.
var capturedFlagPaths = map[storagecontract.AuditDirection][2]string{
	storagecontract.AuditDirectionRequest:          {"$.request_body_captured", "$.request_body_truncated"},
	storagecontract.AuditDirectionResponse:         {"$.response_content_captured", "$.response_content_truncated"},
	storagecontract.AuditDirectionUpstreamRequest:  {"$.upstream_request_body_captured", "$.upstream_request_body_truncated"},
	storagecontract.AuditDirectionUpstreamResponse: {"$.upstream_response_content_captured", "$.upstream_response_content_truncated"},
}

// ReplaceRawSealingKey is the reset that forgets the raw password: parts
// sealed to the old key can never open again, so they are deleted with the
// key, and their records no longer claim a capture. Privacy findings and the
// rest of each record stay.
func (store *Store) ReplaceRawSealingKey(ctx context.Context, key storagecontract.NewRawSealingKey) (storagecontract.RawResetResult, error) {
	if err := validateNewRawSealingKey(key); err != nil {
		return storagecontract.RawResetResult{}, err
	}
	mac, err := store.publicKeyMAC(key.PublicKey)
	if err != nil {
		return storagecontract.RawResetResult{}, err
	}
	result, err := store.discardRawSealing(ctx, func(transaction *sql.Tx) error {
		return store.insertRawSealingKey(ctx, transaction, key, mac, store.now().UTC().Format(time.RFC3339Nano))
	})
	if err != nil {
		return storagecontract.RawResetResult{}, err
	}
	store.rawKey.set(key.KeyID, key.PublicKey, hasPasswordEnvelope(key.Envelopes))
	return result, nil
}

// ClearRawSealingKey is ReplaceRawSealingKey without a new key: the raw
// password is gone and new raw captures are not kept until one is set.
func (store *Store) ClearRawSealingKey(ctx context.Context) (storagecontract.RawResetResult, error) {
	result, err := store.discardRawSealing(ctx, nil)
	if err != nil {
		return storagecontract.RawResetResult{}, err
	}
	store.rawKey.set(0, nil, false)
	return result, nil
}

// discardRawSealing deletes every raw_v1 part and the raw sealing key in one
// transaction, clears the captured flags of affected records, and runs
// install, when given, before committing.
func (store *Store) discardRawSealing(ctx context.Context, install func(*sql.Tx) error) (storagecontract.RawResetResult, error) {
	var result storagecontract.RawResetResult
	err := store.withSecureDelete(ctx, true, func(conn *sql.Conn) (err error) {
		transaction, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin raw sealing reset: %w", err)
		}
		defer rollbackOnError(transaction, &err)
		// Take the writer lock before reading what to drop.
		if _, err = transaction.ExecContext(ctx, `UPDATE raw_sealing_keys SET id = id`); err != nil {
			return fmt.Errorf("lock raw sealing key: %w", err)
		}
		if err = transaction.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(DISTINCT b.request_id)
FROM audit_blobs b JOIN audit_payloads p ON p.id = b.payload_id WHERE p.sealing = 'raw_v1'`).Scan(&result.DeletedParts, &result.AffectedRecords); err != nil {
			return fmt.Errorf("count raw sealed parts: %w", err)
		}
		for direction, paths := range capturedFlagPaths {
			if _, err = transaction.ExecContext(ctx, `UPDATE request_records
SET audit_json = json_set(audit_json, ?1, json('false'), ?2, json('false'))
WHERE json_valid(audit_json) AND id IN (
    SELECT b.request_id FROM audit_blobs b JOIN audit_payloads p ON p.id = b.payload_id
    WHERE p.sealing = 'raw_v1' AND b.direction = ?3)`, paths[0], paths[1], string(direction)); err != nil {
				return fmt.Errorf("clear captured flags: %w", err)
			}
		}
		if _, err = transaction.ExecContext(ctx, `DELETE FROM audit_blobs
WHERE payload_id IN (SELECT id FROM audit_payloads WHERE sealing = 'raw_v1')`); err != nil {
			return fmt.Errorf("delete raw sealed parts: %w", err)
		}
		if _, err = transaction.ExecContext(ctx, `DELETE FROM audit_payloads WHERE sealing = 'raw_v1'`); err != nil {
			return fmt.Errorf("delete raw sealed payloads: %w", err)
		}
		if _, err = transaction.ExecContext(ctx, `DELETE FROM raw_sealing_keys`); err != nil {
			return fmt.Errorf("delete raw sealing key: %w", err)
		}
		if install != nil {
			if err = install(transaction); err != nil {
				return err
			}
		}
		return transaction.Commit()
	})
	if err != nil {
		return storagecontract.RawResetResult{}, err
	}
	return result, nil
}

// withSecureDelete runs fn on one connection with secure_delete on, so the
// rows it deletes or shrinks are overwritten with zeros rather than left in
// free pages. checkpoint then folds the WAL back and truncates it, so older
// frames holding the same bytes go too; it gives up quietly while readers
// hold the WAL.
func (store *Store) withSecureDelete(ctx context.Context, checkpoint bool, fn func(*sql.Conn) error) error {
	conn, err := store.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve secure delete connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA secure_delete = ON`); err != nil {
		return fmt.Errorf("enable secure delete: %w", err)
	}
	// A pooled connection must not keep the setting; a background context
	// still restores it when ctx was cancelled mid-run.
	defer func() { _, _ = conn.ExecContext(context.Background(), `PRAGMA secure_delete = OFF`) }()
	if err := fn(conn); err != nil {
		return err
	}
	if checkpoint {
		var busy, frames, done int
		_ = conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &frames, &done)
	}
	return nil
}

// sealedRawPart is a raw part sealed to the raw sealing key, with the
// shared chunks its recipe refers to in order.
type sealedRawPart struct {
	blob       storagecontract.AuditBlob
	contentKey []byte
	refs       []int64
}

// sealRawPart re-seals a part captured under dek_audit with a fresh part key
// wrapped to the raw sealing public key. The content key is random: raw
// parts are never shared, so equal bodies are not linkable. With recipes,
// what equals a chunk its session shares is referred to rather than copied
// (rawRecipe); the references must be stored in the transaction that stores
// the part, and a chunk gone by then makes it stale (errRawRecipeStale).
func sealRawPart(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, auditKey []byte, blob storagecontract.AuditBlob, keyID int64, public []byte, recipes bool) (sealedRawPart, error) {
	plain, err := storagecontract.OpenAuditBlob(auditKey, blob.Nonce, blob.Ciphertext)
	if err != nil {
		return sealedRawPart{}, err
	}
	defer clear(plain)
	sealed, refs := plain, []int64(nil)
	blob.Layout = storagecontract.AuditLayoutWhole
	if recipes {
		// A lookup that fails only costs the sharing: the part is sealed whole.
		recipe, recipeRefs, err := rawRecipe(ctx, query, auditKey, blob.RequestID, plain)
		if err == nil && recipe != nil {
			defer clear(recipe)
			sealed, refs = recipe, recipeRefs
			blob.Layout = storagecontract.AuditLayoutRecipe
		}
	}
	partKey, err := rawseal.NewBlobKey()
	if err != nil {
		return sealedRawPart{}, err
	}
	defer clear(partKey)
	nonce, ciphertext, err := storagecontract.SealAuditBlob(partKey, sealed)
	if err != nil {
		return sealedRawPart{}, err
	}
	wrapped, err := rawseal.SealBlobKey(public, rawseal.BlobKeyInfo(string(blob.RequestID), string(blob.Direction)), partKey)
	if err != nil {
		return sealedRawPart{}, err
	}
	contentKey := make([]byte, storagecontract.AuditKeyBytes)
	if _, err := io.ReadFull(rand.Reader, contentKey); err != nil {
		return sealedRawPart{}, fmt.Errorf("generate raw content key: %w", err)
	}
	blob.Nonce, blob.Ciphertext = nonce, ciphertext
	blob.Sealing, blob.RawKeyID, blob.WrappedKey = storagecontract.AuditSealingRawV1, keyID, wrapped
	blob.Exposure = storagecontract.AuditExposureRaw
	return sealedRawPart{blob: blob, contentKey: contentKey, refs: refs}, nil
}

// errRawRecipeStale reports a recipe naming a chunk deleted since it was
// looked up.
var errRawRecipeStale = errors.New("raw audit recipe names a deleted chunk")

func insertRawPayload(ctx context.Context, executor execer, blob storagecontract.AuditBlob, contentKey []byte) (int64, error) {
	result, err := executor.ExecContext(ctx, `INSERT INTO audit_payloads (request_id, content_key, nonce, ciphertext, sealing, key_id, wrapped_key)
VALUES (?, ?, ?, ?, 'raw_v1', ?, ?)`, blob.RequestID, contentKey, blob.Nonce, blob.Ciphertext, blob.RawKeyID, blob.WrappedKey)
	if err != nil {
		return 0, fmt.Errorf("insert raw audit payload: %w", err)
	}
	return result.LastInsertId()
}

// writeRawAuditBlob seals a captured raw part and stores it. A recipe that
// went stale is sealed again whole rather than looked up a second time.
func (store *Store) writeRawAuditBlob(ctx context.Context, auditKey []byte, blob storagecontract.AuditBlob, keyID int64, public []byte) error {
	for _, recipes := range []bool{true, false} {
		part, err := sealRawPart(ctx, store.db, auditKey, blob, keyID, public, recipes)
		if err != nil {
			return errRawPartUnsealable
		}
		err = store.writeSealedRawPart(ctx, part)
		clear(part.contentKey)
		if !errors.Is(err, errRawRecipeStale) {
			return err
		}
	}
	return errRawRecipeStale
}

func (store *Store) writeSealedRawPart(ctx context.Context, part sealedRawPart) (err error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin raw audit write: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	blob := part.blob
	payloadID, err := insertRawPayload(ctx, transaction, blob, part.contentKey)
	if err != nil {
		return err
	}
	blob.Nonce, blob.Ciphertext = []byte{}, []byte{}
	if err = upsertAuditBlob(ctx, transaction, blob, payloadID); err != nil {
		return err
	}
	if blob.Layout == storagecontract.AuditLayoutRecipe {
		stored, refErr := insertPartChunkRefs(ctx, transaction, blob.RequestID, blob.Direction, part.refs)
		if refErr != nil {
			err = refErr
			return err
		}
		if !stored {
			err = errRawRecipeStale
			return err
		}
	}
	return transaction.Commit()
}

// dropRawContent is the SET clause that keeps a part only as a withheld
// marker: labelled raw, with no inline copy and no reference to content the
// raw sealing key does not seal. The payload update trigger then deletes an
// audit payload nothing references any more. Run it on a secure-delete
// connection so the dropped bytes are zeroed.
const dropRawContent = `exposure = 'raw', nonce = x'', ciphertext = x'',
    payload_id = CASE WHEN payload_id IN (SELECT id FROM audit_payloads WHERE sealing = 'raw_v1') THEN payload_id END,
    layout = CASE WHEN payload_id IN (SELECT id FROM audit_payloads WHERE sealing = 'raw_v1') THEN layout ELSE 'whole' END`

// resealCandidate is one stored part still sealed under dek_audit that
// readers withhold, with what it looked like when read.
type resealCandidate struct {
	rowID     int64
	payloadID sql.NullInt64
	inline    bool
	exposure  storagecontract.AuditExposure
	blob      storagecontract.AuditBlob
}

// loadResealCandidate reads one part if it still needs resealing. In-flight
// requests are skipped: their pending body is settled, and resealed if it
// becomes raw, by UpdateAuditExposure. Markers never qualify, having nothing
// to seal.
func (store *Store) loadResealCandidate(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, rowID int64) (resealCandidate, bool, error) {
	var (
		candidate                                            resealCandidate
		requestID, direction, mediaType, createdAt, exposure string
		nonce, ciphertext, inlineCiphertext                  []byte
		truncated, capturedBytes                             int
	)
	err := query.QueryRowContext(ctx, `SELECT b.rowid, b.payload_id, b.request_id, b.direction, b.media_type,
    COALESCE(p.nonce, b.nonce), COALESCE(p.ciphertext, b.ciphertext), b.ciphertext,
    b.truncated, b.captured_bytes, b.created_at, b.exposure
FROM audit_blobs b
LEFT JOIN audit_payloads p ON p.id = b.payload_id
LEFT JOIN request_records r ON r.id = b.request_id
WHERE b.rowid = ?1
  AND (b.exposure = 'raw' OR (b.exposure = 'pending' AND COALESCE(r.status, '') <> 'pending'))
  AND (p.sealing = 'audit' OR (b.payload_id IS NULL AND length(b.ciphertext) > 0))`, rowID).Scan(
		&candidate.rowID, &candidate.payloadID, &requestID, &direction, &mediaType,
		&nonce, &ciphertext, &inlineCiphertext, &truncated, &capturedBytes, &createdAt, &exposure,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return resealCandidate{}, false, nil
	}
	if err != nil {
		return resealCandidate{}, false, fmt.Errorf("read raw part to reseal: %w", err)
	}
	created, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return resealCandidate{}, false, fmt.Errorf("%w: audit blob created_at", storagecontract.ErrInvalidRecord)
	}
	candidate.inline = !candidate.payloadID.Valid
	candidate.exposure = storagecontract.AuditExposure(exposure)
	candidate.blob = storagecontract.AuditBlob{
		RequestID: contract.RequestID(requestID), Direction: storagecontract.AuditDirection(direction),
		MediaType: mediaType, Nonce: nonce, Ciphertext: ciphertext, Truncated: truncated != 0,
		CapturedBytes: capturedBytes, CreatedAt: created.UTC(), Exposure: candidate.exposure,
	}
	if !candidate.blob.Direction.Valid() {
		return resealCandidate{}, false, fmt.Errorf("%w: audit blob identity", storagecontract.ErrInvalidRecord)
	}
	return candidate, true, nil
}

type resealedPart struct {
	candidate resealCandidate
	sealedRawPart
}

// applyReseal points the part at its new raw_v1 payload if it is unchanged
// since it was read; a recapture or delete in between wins. The update
// trigger removes the old payload once nothing references it, and clearing
// the inline columns drops a legacy copy.
func applyReseal(ctx context.Context, transaction *sql.Tx, part resealedPart) (bool, error) {
	payloadID, err := insertRawPayload(ctx, transaction, part.blob, part.contentKey)
	if err != nil {
		return false, err
	}
	dropPayload := func() (bool, error) {
		if _, err := transaction.ExecContext(ctx, `DELETE FROM audit_payloads WHERE id = ?`, payloadID); err != nil {
			return false, fmt.Errorf("drop unused raw payload: %w", err)
		}
		return false, nil
	}
	if part.blob.Layout == storagecontract.AuditLayoutRecipe {
		stored, err := insertPartChunkRefs(ctx, transaction, part.blob.RequestID, part.blob.Direction, part.refs)
		if err != nil {
			return false, err
		}
		if !stored {
			if _, err := dropPayload(); err != nil {
				return false, err
			}
			return false, errRawRecipeStale
		}
	}
	candidate := part.candidate
	var oldPayload any
	if candidate.payloadID.Valid {
		oldPayload = candidate.payloadID.Int64
	}
	inlineNonce, inlineCiphertext := []byte{}, []byte{}
	if candidate.inline {
		inlineNonce, inlineCiphertext = candidate.blob.Nonce, candidate.blob.Ciphertext
	}
	result, err := transaction.ExecContext(ctx, `UPDATE audit_blobs
SET payload_id = ?1, nonce = x'', ciphertext = x'', exposure = 'raw', layout = ?7
WHERE rowid = ?2 AND payload_id IS ?3 AND nonce = ?4 AND ciphertext = ?5 AND exposure = ?6 AND layout = 'whole'`,
		payloadID, candidate.rowID, oldPayload, inlineNonce, inlineCiphertext, string(candidate.exposure),
		string(part.blob.Layout))
	if err != nil {
		return false, fmt.Errorf("repoint resealed audit blob: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if changed == 0 {
		if _, err := transaction.ExecContext(ctx, `DELETE FROM audit_part_chunks WHERE request_id = ? AND direction = ?`,
			part.blob.RequestID, string(part.blob.Direction)); err != nil {
			return false, fmt.Errorf("drop unused audit part chunks: %w", err)
		}
		return dropPayload()
	}
	return true, nil
}

// ResealRawParts moves every raw part still sealed under dek_audit — a
// settled one that could not move at once, or a pending one whose request is
// no longer in flight — onto the raw sealing key (§5.11.9.4). Before a raw
// password is set, the pending parts are dropped instead: they settled as
// raw, or never settled, while no raw capture could be kept. It is idempotent:
// resealed parts no longer qualify, and parts that do not decrypt are
// skipped, not retried in a loop. limit caps the parts per transaction;
// zero uses the default.
func (store *Store) ResealRawParts(ctx context.Context, limit int) (storagecontract.RawResealResult, error) {
	var result storagecontract.RawResealResult
	keyID, public := store.rawKey.captureKey()
	if keyID == 0 {
		dropped, err := store.dropSettledPendingParts(ctx)
		if err != nil {
			return result, err
		}
		result.Dropped, result.Done = dropped, true
		return result, nil
	}
	auditKey := store.keys.audit()
	if auditKey == nil {
		return result, fmt.Errorf("%w: audit key", storagecontract.ErrNotFound)
	}
	defer clear(auditKey)
	if limit <= 0 || limit > resealBatchParts {
		limit = resealBatchParts
	}
	stale := false
	err := store.withSecureDelete(ctx, true, func(conn *sql.Conn) error {
		var cursor int64
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			rowIDs, err := resealRowIDs(ctx, conn, cursor, limit)
			if err != nil {
				return err
			}
			if len(rowIDs) == 0 {
				result.Done = true
				return nil
			}
			batch := make([]resealedPart, 0, len(rowIDs))
			var batchBytes int
			for _, rowID := range rowIDs {
				cursor = rowID
				candidate, ok, err := store.loadResealCandidate(ctx, conn, rowID)
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
				part, err := sealRawPart(ctx, conn, auditKey, candidate.blob, keyID, public, true)
				if err != nil {
					// Unreadable under this audit key; the reader reports it.
					continue
				}
				batch = append(batch, resealedPart{candidate: candidate, sealedRawPart: part})
				batchBytes += len(part.blob.Ciphertext)
				if batchBytes >= resealBatchBytes {
					break
				}
			}
			resealed, batchStale, err := commitReseals(ctx, conn, batch)
			if err != nil {
				return err
			}
			result.Resealed += resealed
			stale = stale || batchStale
		}
	})
	if stale && err == nil {
		// A part whose recipe went stale is still a candidate; the next pass
		// looks its chunks up afresh.
		store.deferReseal()
	}
	return result, err
}

// dropSettledPendingParts drops the content of pending parts whose request
// is no longer in flight, for a store that keeps no raw captures. Nothing
// will settle them any more, and one that settled while the request ended
// may have settled as raw.
func (store *Store) dropSettledPendingParts(ctx context.Context) (int, error) {
	var dropped int64
	err := store.withSecureDelete(ctx, true, func(conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx, `UPDATE audit_blobs SET `+dropRawContent+`
WHERE exposure = 'pending' AND NOT EXISTS (
    SELECT 1 FROM request_records r WHERE r.id = audit_blobs.request_id AND r.status = 'pending')`)
		if err != nil {
			return fmt.Errorf("drop settled pending audit parts: %w", err)
		}
		dropped, err = result.RowsAffected()
		return err
	})
	return int(dropped), err
}

func resealRowIDs(ctx context.Context, conn *sql.Conn, cursor int64, limit int) ([]int64, error) {
	rows, err := conn.QueryContext(ctx, `SELECT b.rowid FROM audit_blobs b
LEFT JOIN audit_payloads p ON p.id = b.payload_id
WHERE b.rowid > ?1 AND b.exposure IN ('raw', 'pending')
  AND (p.sealing = 'audit' OR (b.payload_id IS NULL AND length(b.ciphertext) > 0))
ORDER BY b.rowid LIMIT ?2`, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("list raw parts to reseal: %w", err)
	}
	defer rows.Close()
	var rowIDs []int64
	for rows.Next() {
		var rowID int64
		if err := rows.Scan(&rowID); err != nil {
			return nil, err
		}
		rowIDs = append(rowIDs, rowID)
	}
	return rowIDs, rows.Err()
}

// commitReseals applies a batch and reports whether a part was left behind
// because its recipe went stale.
func commitReseals(ctx context.Context, conn *sql.Conn, batch []resealedPart) (resealed int, stale bool, err error) {
	if len(batch) == 0 {
		return 0, false, nil
	}
	transaction, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, fmt.Errorf("begin reseal batch: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	for _, part := range batch {
		applied, applyErr := applyReseal(ctx, transaction, part)
		if errors.Is(applyErr, errRawRecipeStale) {
			stale = true
			continue
		}
		if applyErr != nil {
			err = applyErr
			return 0, false, err
		}
		if applied {
			resealed++
		}
	}
	if err = transaction.Commit(); err != nil {
		return 0, false, fmt.Errorf("commit reseal batch: %w", err)
	}
	return resealed, stale, nil
}

// resealSettledPart reseals one part that just settled to raw. The request
// path pays one HPKE wrap and no KDF; the old copy is zeroed on its page,
// and older WAL frames go with the next checkpoint. A part it cannot seal
// is dropped: a raw capture is never left under dek_audit alone.
func (store *Store) resealSettledPart(ctx context.Context, id contract.RequestID, direction storagecontract.AuditDirection) error {
	keyID, public := store.rawKey.captureKey()
	if keyID == 0 {
		return store.dropRawPart(ctx, id, direction)
	}
	auditKey := store.keys.audit()
	if auditKey == nil {
		return store.dropRawPart(ctx, id, direction)
	}
	defer clear(auditKey)
	sealErr := store.withSecureDelete(ctx, false, func(conn *sql.Conn) error {
		var rowID int64
		err := conn.QueryRowContext(ctx, `SELECT rowid FROM audit_blobs WHERE request_id = ? AND direction = ?`, id, string(direction)).Scan(&rowID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find settled audit part: %w", err)
		}
		candidate, ok, err := store.loadResealCandidate(ctx, conn, rowID)
		if err != nil || !ok {
			return err
		}
		// A recipe that went stale is sealed again whole: this part must not
		// wait for a later pass under dek_audit.
		for _, recipes := range []bool{true, false} {
			part, err := sealRawPart(ctx, conn, auditKey, candidate.blob, keyID, public, recipes)
			if err != nil {
				return errRawPartUnsealable
			}
			_, stale, err := commitReseals(ctx, conn, []resealedPart{{candidate: candidate, sealedRawPart: part}})
			if err != nil || !stale {
				return err
			}
		}
		return nil
	})
	if errors.Is(sealErr, errRawPartUnsealable) {
		return store.dropRawPart(ctx, id, direction)
	}
	return sealErr
}

// errRawPartUnsealable reports a settled raw part that would not seal, such
// as one that does not open under dek_audit.
var errRawPartUnsealable = errors.New("raw audit part cannot be sealed")

// dropRawPart keeps a raw part only as a withheld marker.
func (store *Store) dropRawPart(ctx context.Context, id contract.RequestID, direction storagecontract.AuditDirection) error {
	return store.withSecureDelete(ctx, false, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, `UPDATE audit_blobs SET `+dropRawContent+`
WHERE request_id = ? AND direction = ? AND exposure = 'raw'`, id, string(direction)); err != nil {
			return fmt.Errorf("drop raw audit part: %w", err)
		}
		return nil
	})
}

// OnResealDeferred implements storagecontract.RawSealingStore.
func (store *Store) OnResealDeferred(retry func()) {
	if retry == nil {
		store.resealDeferred.Store(nil)
		return
	}
	store.resealDeferred.Store(&retry)
}

func (store *Store) deferReseal() {
	if retry := store.resealDeferred.Load(); retry != nil {
		(*retry)()
	}
}

// deferSettle hands a part a failed settle left pending to the reseal pass.
// The pass leaves a pending part alone while its request is in flight, so
// such a request is remembered until requestEnded asks for another pass.
func (store *Store) deferSettle(ctx context.Context, id contract.RequestID) {
	store.settleDeferred.Store(id, struct{}{})
	// A request that has already ended does not end again; the pass asked
	// for here covers its part. Remembering it first means an end that
	// lands meanwhile is not missed.
	var status string
	err := store.db.QueryRowContext(context.WithoutCancel(ctx),
		`SELECT status FROM request_records WHERE id = ?`, id).Scan(&status)
	if err == nil && status != string(contract.RequestStatusPending) {
		store.settleDeferred.Delete(id)
	}
	store.deferReseal()
}

// requestEnded asks for another reseal pass once a request whose part a
// failed settle left pending is no longer in flight.
func (store *Store) requestEnded(id contract.RequestID) {
	if _, waiting := store.settleDeferred.LoadAndDelete(id); waiting {
		store.deferReseal()
	}
}

var _ storagecontract.RawSealingStore = (*Store)(nil)
