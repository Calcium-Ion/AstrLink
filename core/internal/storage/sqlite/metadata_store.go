package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func (store *Store) ListMetadataAttributes(ctx context.Context) ([]contract.MetadataAttribute, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT key, label, description, pattern, COALESCE(marker, ''), owner, created_at, updated_at
FROM metadata_attributes ORDER BY created_at, key`)
	if err != nil {
		return nil, fmt.Errorf("read metadata attributes: %w", err)
	}
	defer rows.Close()
	var attributes []contract.MetadataAttribute
	for rows.Next() {
		var attribute contract.MetadataAttribute
		var created, updated string
		if err := rows.Scan(&attribute.Key, &attribute.Label, &attribute.Description, &attribute.Pattern, &attribute.Marker, &attribute.Owner, &created, &updated); err != nil {
			return nil, fmt.Errorf("read metadata attributes: %w", err)
		}
		if attribute.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, fmt.Errorf("%w: metadata attribute %s", storage.ErrInvalidRecord, attribute.Key)
		}
		if attribute.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
			return nil, fmt.Errorf("%w: metadata attribute %s", storage.ErrInvalidRecord, attribute.Key)
		}
		attributes = append(attributes, attribute)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read metadata attributes: %w", err)
	}
	return attributes, nil
}

type executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// claimed reports whether key is taken by an attribute other than one of
// owner's, or marker by any attribute other than the one named key.
func claimed(ctx context.Context, db executor, key, marker, owner string) (bool, error) {
	var taken bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM metadata_attributes WHERE key = ? AND owner <> ?)
    OR (? <> '' AND EXISTS(SELECT 1 FROM metadata_attributes WHERE marker = ? AND key <> ?))`,
		key, owner, marker, marker, key).Scan(&taken); err != nil {
		return false, fmt.Errorf("read metadata attributes: %w", err)
	}
	return taken, nil
}

func nullableMarker(marker string) any {
	if marker == "" {
		return nil
	}
	return marker
}

func (store *Store) CreateMetadataAttribute(ctx context.Context, attribute contract.MetadataAttribute) (err error) {
	if err := attribute.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin metadata attribute create: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	if err = lockMetadataAttributes(ctx, transaction); err != nil {
		return err
	}
	var exists bool
	if err = transaction.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM metadata_attributes WHERE key = ?)`, attribute.Key).Scan(&exists); err != nil {
		return fmt.Errorf("read metadata attributes: %w", err)
	}
	taken, err := claimed(ctx, transaction, attribute.Key, attribute.Marker, attribute.Owner)
	if err != nil {
		return err
	}
	if exists || taken {
		err = fmt.Errorf("%w: metadata attribute %s or its marker", storage.ErrConflict, attribute.Key)
		return err
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	if _, err = transaction.ExecContext(ctx, `INSERT INTO metadata_attributes (key, label, description, pattern, marker, owner, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, attribute.Key, attribute.Label, attribute.Description, attribute.Pattern, nullableMarker(attribute.Marker), attribute.Owner, now, now); err != nil {
		return fmt.Errorf("write metadata attribute: %w", err)
	}
	return transaction.Commit()
}

func (store *Store) UpdateMetadataAttribute(ctx context.Context, attribute contract.MetadataAttribute) (err error) {
	if err := attribute.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin metadata attribute update: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	if err = lockMetadataAttributes(ctx, transaction); err != nil {
		return err
	}
	taken, err := claimed(ctx, transaction, attribute.Key, attribute.Marker, attribute.Owner)
	if err != nil {
		return err
	}
	if taken {
		err = fmt.Errorf("%w: metadata attribute %s or its marker", storage.ErrConflict, attribute.Key)
		return err
	}
	result, err := transaction.ExecContext(ctx, `UPDATE metadata_attributes SET label = ?, description = ?, pattern = ?, marker = ?, updated_at = ? WHERE key = ? AND owner = ?`,
		attribute.Label, attribute.Description, attribute.Pattern, nullableMarker(attribute.Marker), store.now().UTC().Format(time.RFC3339Nano), attribute.Key, attribute.Owner)
	if err != nil {
		return fmt.Errorf("write metadata attribute: %w", err)
	}
	if changed, readErr := result.RowsAffected(); readErr != nil || changed == 0 {
		err = fmt.Errorf("%w: metadata attribute %s", storage.ErrNotFound, attribute.Key)
		return err
	}
	return transaction.Commit()
}

func (store *Store) DeleteMetadataAttribute(ctx context.Context, key string) error {
	result, err := store.db.ExecContext(ctx, `DELETE FROM metadata_attributes WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("delete metadata attribute: %w", err)
	}
	if deleted, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("read metadata attribute delete result: %w", err)
	} else if deleted == 0 {
		return fmt.Errorf("%w: metadata attribute %s", storage.ErrNotFound, key)
	}
	return nil
}

func (store *Store) RegisterPluginAttributes(ctx context.Context, plugin string, attributes []contract.MetadataAttribute) (err error) {
	for _, attribute := range attributes {
		attribute.Owner = plugin
		if err := attribute.Validate(); err != nil {
			return fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
		}
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin metadata attribute registration: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	if err = lockMetadataAttributes(ctx, transaction); err != nil {
		return err
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	for _, attribute := range attributes {
		taken, claimErr := claimed(ctx, transaction, attribute.Key, attribute.Marker, plugin)
		if claimErr != nil {
			err = claimErr
			return err
		}
		if taken {
			err = fmt.Errorf("%w: metadata attribute %s or its marker belongs to another owner", storage.ErrConflict, attribute.Key)
			return err
		}
		if _, err = transaction.ExecContext(ctx, `INSERT INTO metadata_attributes (key, label, description, pattern, marker, owner, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(key) DO UPDATE SET label = excluded.label, description = excluded.description, pattern = excluded.pattern, marker = excluded.marker, updated_at = excluded.updated_at`,
			attribute.Key, attribute.Label, attribute.Description, attribute.Pattern, nullableMarker(attribute.Marker), plugin, now, now); err != nil {
			return fmt.Errorf("write metadata attribute: %w", err)
		}
	}
	return transaction.Commit()
}

// lockMetadataAttributes writes first, so a transaction that reads the
// attributes before it changes them waits on busy_timeout for another
// connection's write instead of failing to upgrade with SQLITE_BUSY.
func lockMetadataAttributes(ctx context.Context, transaction *sql.Tx) error {
	if _, err := transaction.ExecContext(ctx, `UPDATE metadata_attributes SET key = key WHERE 0`); err != nil {
		return fmt.Errorf("lock metadata attributes: %w", err)
	}
	return nil
}

func (store *Store) MetadataValues(ctx context.Context, targetKind, targetID string) (map[string]storage.MetadataValueRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT target_kind, target_id, key, value, source, COALESCE(turn, 0), created_at, updated_at
FROM metadata_values WHERE target_kind = ? AND target_id = ?`, targetKind, targetID)
	if err != nil {
		return nil, fmt.Errorf("read metadata values: %w", err)
	}
	records, err := scanMetadataValues(rows)
	if err != nil {
		return nil, err
	}
	values := make(map[string]storage.MetadataValueRecord, len(records))
	for _, record := range records {
		values[record.Key] = record
	}
	return values, nil
}

func (store *Store) ListMetadataValues(ctx context.Context, targetKind string) ([]storage.MetadataValueRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT target_kind, target_id, key, value, source, COALESCE(turn, 0), created_at, updated_at
FROM metadata_values WHERE target_kind = ? ORDER BY target_id, key`, targetKind)
	if err != nil {
		return nil, fmt.Errorf("read metadata values: %w", err)
	}
	return scanMetadataValues(rows)
}

func (store *Store) MetadataValueStats(ctx context.Context, key string, limit int) ([]storage.MetadataValueStat, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT v.value, COUNT(DISTINCT v.target_id), MAX(r.started_at)
FROM metadata_values v LEFT JOIN request_records r ON r.session_id = v.target_id
WHERE v.target_kind = ? AND v.key = ?
GROUP BY v.value
ORDER BY MAX(r.started_at) IS NULL, MAX(r.started_at) DESC, v.value
LIMIT ?`, storage.MetadataTargetSession, key, limit)
	if err != nil {
		return nil, fmt.Errorf("read metadata value stats: %w", err)
	}
	defer rows.Close()
	var stats []storage.MetadataValueStat
	for rows.Next() {
		var stat storage.MetadataValueStat
		var last sql.NullString
		if err := rows.Scan(&stat.Value, &stat.Sessions, &last); err != nil {
			return nil, fmt.Errorf("read metadata value stats: %w", err)
		}
		if last.Valid {
			used, err := time.Parse(time.RFC3339Nano, last.String)
			if err != nil {
				return nil, fmt.Errorf("%w: request started at %q", storage.ErrInvalidRecord, last.String)
			}
			stat.LastUsedAt = &used
		}
		stats = append(stats, stat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read metadata value stats: %w", err)
	}
	return stats, nil
}

func scanMetadataValues(rows *sql.Rows) ([]storage.MetadataValueRecord, error) {
	defer rows.Close()
	var records []storage.MetadataValueRecord
	for rows.Next() {
		var record storage.MetadataValueRecord
		var created, updated string
		if err := rows.Scan(&record.TargetKind, &record.TargetID, &record.Key, &record.Value, &record.SetBy, &record.Turn, &created, &updated); err != nil {
			return nil, fmt.Errorf("read metadata values: %w", err)
		}
		var err error
		if record.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, fmt.Errorf("%w: metadata value %s", storage.ErrInvalidRecord, record.Key)
		}
		if record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
			return nil, fmt.Errorf("%w: metadata value %s", storage.ErrInvalidRecord, record.Key)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read metadata values: %w", err)
	}
	return records, nil
}

func nullableTurn(turn int) any {
	if turn <= 0 {
		return nil
	}
	return turn
}

func (store *Store) PutMetadataValue(ctx context.Context, record storage.MetadataValueRecord) error {
	if err := validMetadataTarget(record.TargetKind, record.TargetID); err != nil {
		return err
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO metadata_values (target_kind, target_id, key, value, source, turn, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(target_kind, target_id, key) DO UPDATE SET value = excluded.value, source = excluded.source, turn = excluded.turn, updated_at = excluded.updated_at`,
		record.TargetKind, record.TargetID, record.Key, record.Value, record.SetBy, nullableTurn(record.Turn), now, now); err != nil {
		return fmt.Errorf("write metadata value: %w", err)
	}
	return nil
}

func (store *Store) DeleteMetadataValue(ctx context.Context, targetKind, targetID, key string) error {
	if _, err := store.db.ExecContext(ctx, `DELETE FROM metadata_values WHERE target_kind = ? AND target_id = ? AND key = ?`, targetKind, targetID, key); err != nil {
		return fmt.Errorf("delete metadata value: %w", err)
	}
	return nil
}

func (store *Store) ReplaceTokenMetadata(ctx context.Context, tokenID contract.AccessTokenID, values map[string]string) (err error) {
	if err := validMetadataTarget(storage.MetadataTargetToken, string(tokenID)); err != nil {
		return err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin token metadata write: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	if _, err = transaction.ExecContext(ctx, `DELETE FROM metadata_values WHERE target_kind = 'token' AND target_id = ?`, tokenID); err != nil {
		return fmt.Errorf("write token metadata: %w", err)
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	for key, value := range values {
		if _, err = transaction.ExecContext(ctx, `INSERT INTO metadata_values (target_kind, target_id, key, value, source, created_at, updated_at)
VALUES ('token', ?, ?, ?, ?, ?, ?)`, tokenID, key, value, contract.MetadataSetByTokenDefault, now, now); err != nil {
			// The only row the insert can refuse is one for an attribute
			// that does not exist.
			err = fmt.Errorf("%w: token metadata %s: %v", storage.ErrInvalidArgument, key, err)
			return err
		}
	}
	return transaction.Commit()
}

func (store *Store) WorkspaceValues(ctx context.Context, workspace string) (map[string]storage.MetadataWorkspaceRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT key, workspace, name, value, created_at, updated_at
FROM metadata_workspace_values WHERE workspace = ?`, workspace)
	if err != nil {
		return nil, fmt.Errorf("read workspace values: %w", err)
	}
	defer rows.Close()
	values := map[string]storage.MetadataWorkspaceRecord{}
	for rows.Next() {
		var record storage.MetadataWorkspaceRecord
		var created, updated string
		if err := rows.Scan(&record.Key, &record.Workspace, &record.Name, &record.Value, &created, &updated); err != nil {
			return nil, fmt.Errorf("read workspace values: %w", err)
		}
		if record.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, fmt.Errorf("%w: workspace value %s", storage.ErrInvalidRecord, record.Key)
		}
		if record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
			return nil, fmt.Errorf("%w: workspace value %s", storage.ErrInvalidRecord, record.Key)
		}
		values[record.Key] = record
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read workspace values: %w", err)
	}
	return values, nil
}

func (store *Store) PutWorkspaceValue(ctx context.Context, record storage.MetadataWorkspaceRecord, replace bool) (bool, error) {
	if len(record.Workspace) != 64 || len(record.Name) > 256 || record.Key == "" || record.Value == "" {
		return false, fmt.Errorf("%w: workspace value", storage.ErrInvalidArgument)
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	statement := `INSERT INTO metadata_workspace_values (key, workspace, name, value, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(key, workspace) DO NOTHING`
	if replace {
		statement = `INSERT INTO metadata_workspace_values (key, workspace, name, value, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(key, workspace) DO UPDATE SET name = excluded.name, value = excluded.value, updated_at = excluded.updated_at`
	}
	result, err := store.db.ExecContext(ctx, statement, record.Key, record.Workspace, record.Name, record.Value, now, now)
	if err != nil {
		return false, fmt.Errorf("write workspace value: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("write workspace value: %w", err)
	}
	return changed > 0, nil
}

func (store *Store) CountWorkspaceValues(ctx context.Context, keys []string) (int, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	var count int
	query, args := workspaceKeysQuery(`SELECT COUNT(*) FROM metadata_workspace_values WHERE key IN (`, keys)
	if err := store.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count workspace values: %w", err)
	}
	return count, nil
}

func (store *Store) DeleteWorkspaceValues(ctx context.Context, keys []string) (int, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	query, args := workspaceKeysQuery(`DELETE FROM metadata_workspace_values WHERE key IN (`, keys)
	result, err := store.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("delete workspace values: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete workspace values: %w", err)
	}
	return int(removed), nil
}

func workspaceKeysQuery(prefix string, keys []string) (string, []any) {
	args := make([]any, len(keys))
	for index, key := range keys {
		args[index] = key
	}
	return prefix + strings.TrimSuffix(strings.Repeat("?, ", len(keys)), ", ") + ")", args
}

func validMetadataTarget(kind, id string) error {
	if (kind != storage.MetadataTargetToken && kind != storage.MetadataTargetSession) || id == "" || len(id) > 256 {
		return fmt.Errorf("%w: metadata target", storage.ErrInvalidArgument)
	}
	return nil
}
