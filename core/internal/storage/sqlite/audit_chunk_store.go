package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

// Agents resend their whole history on every call. A request body that is
// shareable once its request ends is moved into chunks shared by its
// session (storagecontract.AuditLayoutChunks), so a session's bodies grow
// with what each call adds rather than with the square of its length. Raw
// parts never enter audit_chunks; sealRawPart only refers to chunks that are
// already there.

// auditChunkQueue bounds the requests waiting to be chunked. A request that
// does not fit is left to the retention sweep.
const auditChunkQueue = 256

// chunkSweepParts and chunkSweepBytes bound one sweep's catch-up pass, as
// compactLegacyAuditBlobs is bounded: the first sweep runs before Core
// serves. 16 MiB takes about 130 ms.
const (
	chunkSweepParts = 128
	chunkSweepBytes = 16 << 20
)

type auditChunkWorker struct {
	start  sync.Once
	closed atomic.Bool
	queue  chan contract.RequestID
	cancel context.CancelFunc
	done   chan struct{}
	// processed, set by tests before the first request, hears of each
	// request the worker finished.
	processed func(contract.RequestID)
}

// ChunkRequestAudit implements storagecontract.AuditChunker.
func (store *Store) ChunkRequestAudit(id contract.RequestID) {
	worker := &store.chunker
	if worker.closed.Load() {
		return
	}
	worker.start.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		worker.queue = make(chan contract.RequestID, auditChunkQueue)
		worker.cancel = cancel
		worker.done = make(chan struct{})
		go store.runAuditChunker(ctx)
	})
	select {
	case worker.queue <- id:
	default:
	}
}

func (store *Store) runAuditChunker(ctx context.Context) {
	defer close(store.chunker.done)
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-store.chunker.queue:
			if err := store.chunkRequestParts(ctx, id); err != nil && ctx.Err() == nil {
				log.Printf("audit chunking failed: %v", err)
			}
			if store.chunker.processed != nil {
				store.chunker.processed(id)
			}
		}
	}
}

// stopAuditChunker ends the worker; what it had not reached is left to the
// retention sweep.
func (store *Store) stopAuditChunker() {
	worker := &store.chunker
	worker.closed.Store(true)
	worker.start.Do(func() {})
	if worker.cancel != nil {
		worker.cancel()
		<-worker.done
	}
}

// chunkRequestParts chunks the shareable request bodies of a finished
// request and of its failed attempts.
func (store *Store) chunkRequestParts(ctx context.Context, id contract.RequestID) error {
	auditKey := store.keys.audit()
	if auditKey == nil {
		return nil
	}
	defer clear(auditKey)
	rows, err := store.db.QueryContext(ctx, `SELECT b.rowid FROM audit_blobs b
JOIN request_records r ON r.id = b.request_id
WHERE (r.id = ?1 OR r.parent_request_id = ?1)`+chunkablePart, id)
	if err != nil {
		return fmt.Errorf("list request parts to chunk: %w", err)
	}
	rowIDs, err := scanRowIDs(rows)
	if err != nil {
		return err
	}
	for _, rowID := range rowIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := store.chunkPart(ctx, auditKey, rowID); err != nil {
			return err
		}
	}
	return nil
}

// chunkEndedParts is the sweep's catch-up: parts whose request ended while
// the worker was busy, stopped, or not yet told, and parts stored before
// chunking existed.
func (store *Store) chunkEndedParts(ctx context.Context) error {
	auditKey := store.keys.audit()
	if auditKey == nil {
		return nil
	}
	defer clear(auditKey)
	var cursor int64
	processed := 0
	for count := 0; count < chunkSweepParts && processed < chunkSweepBytes; count++ {
		var rowID int64
		err := store.db.QueryRowContext(ctx, `SELECT b.rowid FROM audit_blobs b
WHERE b.rowid > ?1`+chunkablePart+` ORDER BY b.rowid LIMIT 1`, cursor).Scan(&rowID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find part to chunk: %w", err)
		}
		cursor = rowID
		size, err := store.chunkPart(ctx, auditKey, rowID)
		if err != nil {
			return err
		}
		processed += size
	}
	return nil
}

// chunkablePart narrows a query over audit_blobs b to whole shareable
// request bodies.
const chunkablePart = `
  AND b.layout = 'whole' AND b.exposure = 'shareable'
  AND b.direction IN ('request', 'upstream_request')`

func scanRowIDs(rows *sql.Rows) ([]int64, error) {
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

// chunkPart moves one whole shareable request body of an ended request into
// chunks and reports the bytes it read. A part that changed meanwhile, or
// that does not open under this audit key, is left as it is. A stop takes
// effect between parts, never inside one: database/sql rolls back a
// cancelled transaction from its own goroutine, which can still hold the
// connection after Close returned, and Windows then refuses to delete the
// database file.
func (store *Store) chunkPart(ctx context.Context, auditKey []byte, rowID int64) (int, error) {
	ctx = context.WithoutCancel(ctx)
	var (
		requestID, direction, scope string
		payloadID                   sql.NullInt64
		nonce, ciphertext           []byte
		inlineNonce, inlineCipher   []byte
	)
	err := store.db.QueryRowContext(ctx, `SELECT b.request_id, b.direction, b.payload_id,
    COALESCE(p.nonce, b.nonce), COALESCE(p.ciphertext, b.ciphertext), b.nonce, b.ciphertext,
    COALESCE(r.session_id, r.id)
FROM audit_blobs b
JOIN request_records r ON r.id = b.request_id
LEFT JOIN audit_payloads p ON p.id = b.payload_id
WHERE b.rowid = ?1 AND r.status <> 'pending'`+chunkablePart+`
  AND (p.sealing = 'audit' OR (b.payload_id IS NULL AND length(b.ciphertext) > 0))`, rowID).Scan(
		&requestID, &direction, &payloadID, &nonce, &ciphertext, &inlineNonce, &inlineCipher, &scope,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read part to chunk: %w", err)
	}
	plain, err := storagecontract.OpenAuditBlob(auditKey, nonce, ciphertext)
	if err != nil {
		// The reader reports it; chunking cannot help.
		return len(ciphertext), nil
	}
	defer clear(plain)
	chunks := storagecontract.SplitAuditChunks(plain)
	sealed := make([]storagecontract.AuditChunk, len(chunks))
	keys := make([][]byte, len(chunks))
	for index, chunk := range chunks {
		keys[index] = storagecontract.AuditChunkKey(auditKey, scope, chunk)
		chunkNonce, chunkCiphertext, err := storagecontract.SealAuditBlob(auditKey, chunk)
		if err != nil {
			return 0, err
		}
		sealed[index] = storagecontract.AuditChunk{Nonce: chunkNonce, Ciphertext: chunkCiphertext}
	}

	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin audit chunking: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	var oldPayload any
	if payloadID.Valid {
		oldPayload = payloadID.Int64
	}
	// An empty column scans as nil, which would bind as NULL and never match.
	inlineNonce, inlineCipher = append([]byte{}, inlineNonce...), append([]byte{}, inlineCipher...)
	// Write first, so the writer lock is held before anything is read, and
	// only if the part is still what was read. The payload update trigger
	// drops the old payload once nothing references it.
	result, err := transaction.ExecContext(ctx, `UPDATE audit_blobs
SET layout = 'chunks', payload_id = NULL, nonce = x'', ciphertext = x''
WHERE rowid = ?1 AND layout = 'whole' AND exposure = 'shareable'
  AND payload_id IS ?2 AND nonce = ?3 AND ciphertext = ?4`,
		rowID, oldPayload, inlineNonce, inlineCipher)
	if err != nil {
		return 0, fmt.Errorf("mark audit part chunked: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if changed == 0 {
		return len(ciphertext), transaction.Commit()
	}
	ids, err := storeAuditChunks(ctx, transaction, scope, keys, sealed)
	if err != nil {
		return 0, err
	}
	stored, err := insertPartChunkRefs(ctx, transaction, contract.RequestID(requestID), storagecontract.AuditDirection(direction), ids)
	if err != nil {
		return 0, err
	}
	if !stored {
		// The chunks were written in this transaction and cannot be gone.
		err = fmt.Errorf("%w: chunk missing while chunking", storagecontract.ErrInvalidRecord)
		return 0, err
	}
	if err = transaction.Commit(); err != nil {
		return 0, fmt.Errorf("commit audit chunking: %w", err)
	}
	return len(ciphertext), nil
}

// storeAuditChunks adds the chunks a scope does not hold yet and returns
// every chunk's id, in order.
func storeAuditChunks(ctx context.Context, transaction *sql.Tx, scope string, keys [][]byte, sealed []storagecontract.AuditChunk) ([]int64, error) {
	insert, err := transaction.PrepareContext(ctx, `INSERT INTO audit_chunks (scope, content_key, nonce, ciphertext)
VALUES (?, ?, ?, ?) ON CONFLICT(scope, content_key) DO NOTHING`)
	if err != nil {
		return nil, fmt.Errorf("prepare audit chunk insert: %w", err)
	}
	defer insert.Close()
	lookup, err := transaction.PrepareContext(ctx, `SELECT id FROM audit_chunks WHERE scope = ? AND content_key = ?`)
	if err != nil {
		return nil, fmt.Errorf("prepare audit chunk lookup: %w", err)
	}
	defer lookup.Close()
	ids := make([]int64, len(keys))
	for index := range keys {
		if _, err := insert.ExecContext(ctx, scope, keys[index], sealed[index].Nonce, sealed[index].Ciphertext); err != nil {
			return nil, fmt.Errorf("insert audit chunk: %w", err)
		}
		if err := lookup.QueryRowContext(ctx, scope, keys[index]).Scan(&ids[index]); err != nil {
			return nil, fmt.Errorf("read audit chunk: %w", err)
		}
	}
	return ids, nil
}

// insertPartChunkRefs replaces a part's chunk references. It reports false,
// having written nothing, when a chunk is gone: a recipe that named it
// would no longer open.
func insertPartChunkRefs(ctx context.Context, transaction *sql.Tx, id contract.RequestID, direction storagecontract.AuditDirection, chunkIDs []int64) (bool, error) {
	if _, err := transaction.ExecContext(ctx, `DELETE FROM audit_part_chunks WHERE request_id = ? AND direction = ?`, id, string(direction)); err != nil {
		return false, fmt.Errorf("clear audit part chunks: %w", err)
	}
	insert, err := transaction.PrepareContext(ctx, `INSERT INTO audit_part_chunks (request_id, direction, seq, chunk_id)
SELECT ?, ?, ?, id FROM audit_chunks WHERE id = ?`)
	if err != nil {
		return false, fmt.Errorf("prepare audit part chunk insert: %w", err)
	}
	defer insert.Close()
	for seq, chunkID := range chunkIDs {
		result, err := insert.ExecContext(ctx, id, string(direction), seq, chunkID)
		if err != nil {
			return false, fmt.Errorf("insert audit part chunk: %w", err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if inserted == 0 {
			if _, err := transaction.ExecContext(ctx, `DELETE FROM audit_part_chunks WHERE request_id = ? AND direction = ?`, id, string(direction)); err != nil {
				return false, fmt.Errorf("clear audit part chunks: %w", err)
			}
			return false, nil
		}
	}
	return true, nil
}

// loadPartChunks reads a part's chunks in order.
func loadPartChunks(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, id contract.RequestID, direction storagecontract.AuditDirection) ([]storagecontract.AuditChunk, error) {
	rows, err := query.QueryContext(ctx, `SELECT c.nonce, c.ciphertext
FROM audit_part_chunks pc JOIN audit_chunks c ON c.id = pc.chunk_id
WHERE pc.request_id = ? AND pc.direction = ? ORDER BY pc.seq`, id, string(direction))
	if err != nil {
		return nil, fmt.Errorf("list audit part chunks: %w", err)
	}
	defer rows.Close()
	var chunks []storagecontract.AuditChunk
	for rows.Next() {
		var chunk storagecontract.AuditChunk
		if err := rows.Scan(&chunk.Nonce, &chunk.Ciphertext); err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, rows.Err()
}

// rawRecipe lays out a raw part's plaintext as a recipe: each chunk equal to
// one its session already shares becomes a reference, the rest stay literal
// and are sealed with the part. Only matches are kept; the key of a chunk
// that matched nothing is never stored, so no guess about raw content can be
// checked against the store. It returns nil when nothing matched.
func rawRecipe(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, auditKey []byte, id contract.RequestID, plain []byte) ([]byte, []int64, error) {
	var scope string
	err := query.QueryRowContext(ctx, `SELECT COALESCE(session_id, id) FROM request_records WHERE id = ?`, id).Scan(&scope)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read audit part scope: %w", err)
	}
	var (
		pieces  []storagecontract.AuditRecipePiece
		refs    []int64
		literal []byte
	)
	defer func() {
		for _, piece := range pieces {
			clear(piece.Literal)
		}
		clear(literal)
	}()
	for _, chunk := range storagecontract.SplitAuditChunks(plain) {
		var chunkID int64
		err := query.QueryRowContext(ctx, `SELECT id FROM audit_chunks WHERE scope = ? AND content_key = ?`,
			scope, storagecontract.AuditChunkKey(auditKey, scope, chunk)).Scan(&chunkID)
		if errors.Is(err, sql.ErrNoRows) {
			literal = append(literal, chunk...)
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("look up audit chunk: %w", err)
		}
		if len(literal) > 0 {
			pieces = append(pieces, storagecontract.AuditRecipePiece{Literal: literal})
			literal = nil
		}
		pieces = append(pieces, storagecontract.AuditRecipePiece{})
		refs = append(refs, chunkID)
	}
	if len(refs) == 0 {
		return nil, nil, nil
	}
	if len(literal) > 0 {
		pieces = append(pieces, storagecontract.AuditRecipePiece{Literal: literal})
		literal = nil
	}
	return storagecontract.EncodeAuditRecipe(pieces), refs, nil
}

// unchunkPart puts a chunked part back into one shared payload, so a part
// that must tighten to raw can be resealed like any other.
func (store *Store) unchunkPart(ctx context.Context, auditKey []byte, id contract.RequestID, direction storagecontract.AuditDirection) error {
	blobs, err := store.listAuditBlobs(ctx, id, false)
	if err != nil {
		return err
	}
	for _, blob := range blobs {
		if blob.Direction != direction || blob.Layout != storagecontract.AuditLayoutChunks {
			continue
		}
		plain, err := storagecontract.OpenAuditChunks(auditKey, blob.Chunks)
		if err != nil {
			return err
		}
		defer clear(plain)
		blob.Nonce, blob.Ciphertext, err = storagecontract.SealAuditBlob(auditKey, plain)
		if err != nil {
			return err
		}
		blob.Layout, blob.Chunks = storagecontract.AuditLayoutWhole, nil
		contentKey := auditContentKey(auditKey, blob)
		if contentKey == nil {
			return fmt.Errorf("%w: audit part does not reopen", storagecontract.ErrInvalidRecord)
		}
		return store.writeSharedAuditBlob(ctx, blob, contentKey, false)
	}
	return nil
}

var _ storagecontract.AuditChunker = (*Store)(nil)
