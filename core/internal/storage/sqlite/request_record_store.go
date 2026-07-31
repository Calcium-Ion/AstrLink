package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func (store *Store) InsertRequestRecord(ctx context.Context, record contract.RequestRecord) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	row, err := encodeRequestRecordRow(record, store.now().UTC())
	if err != nil {
		return err
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO request_records (
    id, started_at, completed_at, status, input_protocol, requested_model, streaming,
    route_id, service_id, local_access_token_id, plan_json, http_status, latency_ms,
    usage_json, error_json, audit_json, privacy_restore_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.id, row.startedAt, row.completedAt, row.status, row.inputProtocol, row.requestedModel,
		row.streaming, row.routeID, row.endpointID, row.localAccessTokenID, row.planJSON,
		row.httpStatus, row.latencyMs, row.usageJSON, row.errorJSON, row.auditJSON,
		row.privacyRestoreJSON, row.createdAt,
	)
	if err != nil {
		return fmt.Errorf("insert request record: %w", err)
	}
	return nil
}

// UpsertRequestRecord persists a live request snapshot. A terminal row is never
// downgraded by a late pending snapshot, which makes request-start and
// request-finish persistence safe even when their contexts complete out of
// order.
func (store *Store) UpsertRequestRecord(ctx context.Context, record contract.RequestRecord) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	row, err := encodeRequestRecordRow(record, store.now().UTC())
	if err != nil {
		return err
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO request_records (
    id, started_at, completed_at, status, input_protocol, requested_model, streaming,
    route_id, service_id, local_access_token_id, plan_json, http_status, latency_ms,
    usage_json, error_json, audit_json, privacy_restore_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    started_at = excluded.started_at,
    completed_at = excluded.completed_at,
    status = excluded.status,
    input_protocol = excluded.input_protocol,
    requested_model = excluded.requested_model,
    streaming = excluded.streaming,
    route_id = excluded.route_id,
    service_id = excluded.service_id,
    local_access_token_id = excluded.local_access_token_id,
    plan_json = excluded.plan_json,
    http_status = excluded.http_status,
    latency_ms = excluded.latency_ms,
    usage_json = excluded.usage_json,
    error_json = excluded.error_json,
    audit_json = excluded.audit_json,
    privacy_restore_json = excluded.privacy_restore_json
WHERE request_records.status = 'pending' OR excluded.status <> 'pending'`,
		row.id, row.startedAt, row.completedAt, row.status, row.inputProtocol, row.requestedModel,
		row.streaming, row.routeID, row.endpointID, row.localAccessTokenID, row.planJSON,
		row.httpStatus, row.latencyMs, row.usageJSON, row.errorJSON, row.auditJSON,
		row.privacyRestoreJSON, row.createdAt,
	)
	if err != nil {
		return fmt.Errorf("upsert request record: %w", err)
	}
	return nil
}

// RecoverPendingRequestRecords closes rows left live by a previous Core
// process. It is intentionally called once at startup, never by the periodic
// retention sweep.
func (store *Store) RecoverPendingRequestRecords(ctx context.Context) (int, error) {
	completedAt := store.now().UTC().Format(time.RFC3339Nano)
	errorJSON, err := json.Marshal(contract.ErrorSummary{
		Category:  "runtime",
		Code:      "core_interrupted",
		Message:   "request was interrupted when AstrLink Core stopped",
		Retryable: true,
	})
	if err != nil {
		return 0, fmt.Errorf("encode interrupted request error: %w", err)
	}
	result, err := store.db.ExecContext(ctx, `UPDATE request_records
SET status = 'failed',
    completed_at = ?,
    latency_ms = NULL,
    error_json = ?
WHERE status = 'pending'`, completedAt, string(errorJSON))
	if err != nil {
		return 0, fmt.Errorf("recover pending request records: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read pending request recovery result: %w", err)
	}
	return int(updated), nil
}

func (store *Store) GetRequestRecord(ctx context.Context, id contract.RequestID) (contract.RequestRecord, error) {
	if err := id.Validate(); err != nil {
		return contract.RequestRecord{}, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	row := store.db.QueryRowContext(ctx, `SELECT
    id, started_at, completed_at, status, input_protocol, requested_model, streaming,
    route_id, service_id, local_access_token_id, plan_json, http_status, latency_ms,
    usage_json, error_json, audit_json, privacy_restore_json, created_at
FROM request_records WHERE id = ?`, id)
	record, err := scanRequestRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return contract.RequestRecord{}, fmt.Errorf("%w: request %q", storagecontract.ErrNotFound, id)
		}
		return contract.RequestRecord{}, err
	}
	return record, nil
}

func (store *Store) DeleteRequestRecord(ctx context.Context, id contract.RequestID) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin request delete: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	if _, err = transaction.ExecContext(ctx, `DELETE FROM audit_blobs WHERE request_id = ?`, id); err != nil {
		return fmt.Errorf("delete request audit blobs: %w", err)
	}
	result, err := transaction.ExecContext(ctx, `DELETE FROM request_records WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete request record: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read request delete result: %w", err)
	}
	if deleted == 0 {
		err = fmt.Errorf("%w: request %q", storagecontract.ErrNotFound, id)
		return err
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit request delete: %w", err)
	}
	return nil
}

func (store *Store) ListRequestRecords(
	ctx context.Context,
	options storagecontract.RequestRecordListOptions,
) (storagecontract.RequestRecordPage, error) {
	limit := options.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	if limit < 1 || limit > maxListLimit {
		return storagecontract.RequestRecordPage{}, fmt.Errorf(
			"%w: limit must be between 1 and %d",
			storagecontract.ErrInvalidArgument,
			maxListLimit,
		)
	}
	cursorStarted, cursorID, err := decodeRequestRecordCursor(options.Cursor)
	if err != nil {
		return storagecontract.RequestRecordPage{}, err
	}

	query := strings.Builder{}
	query.WriteString(`SELECT
    id, started_at, completed_at, status, input_protocol, requested_model, streaming,
    route_id, service_id, local_access_token_id, plan_json, http_status, latency_ms,
    usage_json, error_json, audit_json, privacy_restore_json, created_at
FROM request_records WHERE 1 = 1`)
	args := make([]any, 0, 8)
	if options.From != nil {
		query.WriteString(` AND started_at >= ?`)
		args = append(args, options.From.UTC().Format(time.RFC3339Nano))
	}
	if options.To != nil {
		query.WriteString(` AND started_at < ?`)
		args = append(args, options.To.UTC().Format(time.RFC3339Nano))
	}
	if options.Protocol != nil {
		query.WriteString(` AND input_protocol = ?`)
		args = append(args, string(*options.Protocol))
	}
	if options.ServiceID != nil {
		query.WriteString(` AND service_id = ?`)
		args = append(args, string(*options.ServiceID))
	}
	if options.Status != nil {
		query.WriteString(` AND status = ?`)
		args = append(args, string(*options.Status))
	}
	if options.Cursor != "" {
		query.WriteString(` AND (started_at < ? OR (started_at = ? AND id < ?))`)
		args = append(args, cursorStarted, cursorStarted, cursorID)
	}
	query.WriteString(` ORDER BY started_at DESC, id DESC`)

	rows, err := store.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return storagecontract.RequestRecordPage{}, fmt.Errorf("list request records: %w", err)
	}
	defer rows.Close()

	matched := make([]contract.RequestRecord, 0, limit+1)
	for rows.Next() {
		record, err := scanRequestRecord(rows)
		if err != nil {
			return storagecontract.RequestRecordPage{}, err
		}
		matched = append(matched, record)
		if len(matched) == limit+1 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return storagecontract.RequestRecordPage{}, fmt.Errorf("iterate request records: %w", err)
	}
	page := storagecontract.RequestRecordPage{Items: matched}
	if len(matched) > limit {
		page.Items = matched[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeRequestRecordCursor(last.StartedAt, last.ID)
	}
	return page, nil
}

func (store *Store) PurgeRequestRecords(
	ctx context.Context,
	request contract.PurgeRequest,
) (contract.PurgeResult, error) {
	if err := request.Validate(); err != nil {
		return contract.PurgeResult{}, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.PurgeResult{}, fmt.Errorf("begin request purge: %w", err)
	}
	defer rollbackOnError(transaction, &err)

	var blobCount int
	switch request.Scope {
	case contract.PurgeScopeAll:
		if err = transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_blobs`).Scan(&blobCount); err != nil {
			return contract.PurgeResult{}, fmt.Errorf("count purge audit blobs: %w", err)
		}
		if _, err = transaction.ExecContext(ctx, `DELETE FROM audit_blobs`); err != nil {
			return contract.PurgeResult{}, fmt.Errorf("purge audit blobs: %w", err)
		}
		result, execErr := transaction.ExecContext(ctx, `DELETE FROM request_records`)
		if execErr != nil {
			err = execErr
			return contract.PurgeResult{}, fmt.Errorf("purge request records: %w", err)
		}
		deleted, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			err = rowsErr
			return contract.PurgeResult{}, fmt.Errorf("read purge result: %w", err)
		}
		if err = transaction.Commit(); err != nil {
			return contract.PurgeResult{}, fmt.Errorf("commit request purge: %w", err)
		}
		return contract.PurgeResult{DeletedRecords: int(deleted), DeletedAuditBlobs: blobCount}, nil
	case contract.PurgeScopeBefore:
		before := request.Before.UTC().Format(time.RFC3339Nano)
		if err = transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_blobs
WHERE request_id IN (SELECT id FROM request_records WHERE started_at < ?)`, before).Scan(&blobCount); err != nil {
			return contract.PurgeResult{}, fmt.Errorf("count purge audit blobs: %w", err)
		}
		if _, err = transaction.ExecContext(ctx, `DELETE FROM audit_blobs
WHERE request_id IN (SELECT id FROM request_records WHERE started_at < ?)`, before); err != nil {
			return contract.PurgeResult{}, fmt.Errorf("purge audit blobs: %w", err)
		}
		result, execErr := transaction.ExecContext(
			ctx,
			`DELETE FROM request_records WHERE started_at < ?`,
			before,
		)
		if execErr != nil {
			err = execErr
			return contract.PurgeResult{}, fmt.Errorf("purge request records: %w", err)
		}
		deleted, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			err = rowsErr
			return contract.PurgeResult{}, fmt.Errorf("read purge result: %w", err)
		}
		if err = transaction.Commit(); err != nil {
			return contract.PurgeResult{}, fmt.Errorf("commit request purge: %w", err)
		}
		return contract.PurgeResult{DeletedRecords: int(deleted), DeletedAuditBlobs: blobCount}, nil
	default:
		err = fmt.Errorf("%w: unknown purge scope", storagecontract.ErrInvalidArgument)
		return contract.PurgeResult{}, err
	}
}

type requestRecordRow struct {
	id                 string
	startedAt          string
	completedAt        any
	status             string
	inputProtocol      string
	requestedModel     any
	streaming          int
	routeID            any
	endpointID         any
	localAccessTokenID any
	planJSON           any
	httpStatus         any
	latencyMs          any
	usageJSON          any
	errorJSON          any
	auditJSON          string
	privacyRestoreJSON any
	createdAt          string
}

func encodeRequestRecordRow(record contract.RequestRecord, createdAt time.Time) (requestRecordRow, error) {
	auditJSON, err := json.Marshal(record.Audit)
	if err != nil {
		return requestRecordRow{}, fmt.Errorf("encode audit summary: %w", err)
	}
	row := requestRecordRow{
		id:            string(record.ID),
		startedAt:     record.StartedAt.UTC().Format(time.RFC3339Nano),
		status:        string(record.Status),
		inputProtocol: string(record.InputProtocol),
		streaming:     boolToInt(record.Streaming),
		auditJSON:     string(auditJSON),
		createdAt:     createdAt.Format(time.RFC3339Nano),
	}
	if record.CompletedAt != nil {
		row.completedAt = record.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	if record.RequestedModel != nil {
		row.requestedModel = *record.RequestedModel
	}
	if record.RouteID != nil {
		row.routeID = string(*record.RouteID)
	}
	if record.ServiceID != nil {
		row.endpointID = string(*record.ServiceID)
	}
	if record.LocalAccessTokenID != nil {
		row.localAccessTokenID = string(*record.LocalAccessTokenID)
	}
	if record.Plan != nil {
		encoded, err := json.Marshal(record.Plan)
		if err != nil {
			return requestRecordRow{}, fmt.Errorf("encode plan: %w", err)
		}
		row.planJSON = string(encoded)
	}
	if record.HTTPStatus != nil {
		row.httpStatus = *record.HTTPStatus
	}
	if record.LatencyMs != nil {
		row.latencyMs = *record.LatencyMs
	}
	if record.Usage != nil {
		encoded, err := json.Marshal(record.Usage)
		if err != nil {
			return requestRecordRow{}, fmt.Errorf("encode usage: %w", err)
		}
		row.usageJSON = string(encoded)
	}
	if record.Error != nil {
		encoded, err := json.Marshal(record.Error)
		if err != nil {
			return requestRecordRow{}, fmt.Errorf("encode error summary: %w", err)
		}
		row.errorJSON = string(encoded)
	}
	if record.PrivacyRestore != nil {
		encoded, err := json.Marshal(record.PrivacyRestore)
		if err != nil {
			return requestRecordRow{}, fmt.Errorf("encode privacy restore summary: %w", err)
		}
		row.privacyRestoreJSON = string(encoded)
	}
	return row, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanRequestRecord(row scannable) (contract.RequestRecord, error) {
	var (
		id, startedAt, status, inputProtocol, auditJSON, createdAt string
		completedAt, requestedModel, routeID, endpointID           sql.NullString
		localAccessTokenID, planJSON, usageJSON, errorJSON         sql.NullString
		privacyRestoreJSON                                         sql.NullString
		streaming                                                  int
		httpStatus, latencyMs                                      sql.NullInt64
	)
	if err := row.Scan(
		&id, &startedAt, &completedAt, &status, &inputProtocol, &requestedModel, &streaming,
		&routeID, &endpointID, &localAccessTokenID, &planJSON, &httpStatus, &latencyMs,
		&usageJSON, &errorJSON, &auditJSON, &privacyRestoreJSON, &createdAt,
	); err != nil {
		return contract.RequestRecord{}, err
	}
	started, err := time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		return contract.RequestRecord{}, fmt.Errorf("%w: request %q started_at", storagecontract.ErrInvalidRecord, id)
	}
	record := contract.RequestRecord{
		ID:            contract.RequestID(id),
		StartedAt:     started.UTC(),
		Status:        contract.RequestStatus(status),
		InputProtocol: contract.ProtocolID(inputProtocol),
		Streaming:     streaming != 0,
	}
	if completedAt.Valid {
		completed, err := time.Parse(time.RFC3339Nano, completedAt.String)
		if err != nil {
			return contract.RequestRecord{}, fmt.Errorf("%w: request %q completed_at", storagecontract.ErrInvalidRecord, id)
		}
		completed = completed.UTC()
		record.CompletedAt = &completed
	}
	if requestedModel.Valid {
		model := requestedModel.String
		record.RequestedModel = &model
	}
	if routeID.Valid {
		value := contract.RouteID(routeID.String)
		record.RouteID = &value
	}
	if endpointID.Valid {
		value := contract.ServiceID(endpointID.String)
		record.ServiceID = &value
	}
	if localAccessTokenID.Valid {
		value := contract.AccessTokenID(localAccessTokenID.String)
		record.LocalAccessTokenID = &value
	}
	if planJSON.Valid {
		var plan contract.ExecutionPlan
		if err := json.Unmarshal([]byte(planJSON.String), &plan); err != nil {
			return contract.RequestRecord{}, fmt.Errorf("%w: request %q plan", storagecontract.ErrInvalidRecord, id)
		}
		record.Plan = &plan
	}
	if httpStatus.Valid {
		value := int(httpStatus.Int64)
		record.HTTPStatus = &value
	}
	if latencyMs.Valid {
		value := int(latencyMs.Int64)
		record.LatencyMs = &value
	}
	if usageJSON.Valid {
		var usage contract.Usage
		if err := json.Unmarshal([]byte(usageJSON.String), &usage); err != nil {
			return contract.RequestRecord{}, fmt.Errorf("%w: request %q usage", storagecontract.ErrInvalidRecord, id)
		}
		record.Usage = &usage
	}
	if errorJSON.Valid {
		var summary contract.ErrorSummary
		if err := json.Unmarshal([]byte(errorJSON.String), &summary); err != nil {
			return contract.RequestRecord{}, fmt.Errorf("%w: request %q error", storagecontract.ErrInvalidRecord, id)
		}
		record.Error = &summary
	}
	if err := json.Unmarshal([]byte(auditJSON), &record.Audit); err != nil {
		return contract.RequestRecord{}, fmt.Errorf("%w: request %q audit", storagecontract.ErrInvalidRecord, id)
	}
	if privacyRestoreJSON.Valid {
		var summary contract.PrivacyRestoreSummary
		if err := json.Unmarshal([]byte(privacyRestoreJSON.String), &summary); err != nil {
			return contract.RequestRecord{}, fmt.Errorf(
				"%w: request %q privacy_restore",
				storagecontract.ErrInvalidRecord,
				id,
			)
		}
		record.PrivacyRestore = &summary
	}
	if _, err := time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return contract.RequestRecord{}, fmt.Errorf("%w: request %q created_at", storagecontract.ErrInvalidRecord, id)
	}
	if err := record.Validate(); err != nil {
		return contract.RequestRecord{}, fmt.Errorf("%w: request %q: %v", storagecontract.ErrInvalidRecord, id, err)
	}
	return record, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func encodeRequestRecordCursor(startedAt time.Time, id contract.RequestID) string {
	payload := startedAt.UTC().Format(time.RFC3339Nano) + "|" + string(id)
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func decodeRequestRecordCursor(cursor string) (string, string, error) {
	if cursor == "" {
		return "", "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != cursor {
		return "", "", fmt.Errorf("%w: request cursor encoding", storagecontract.ErrInvalidCursor)
	}
	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("%w: request cursor payload", storagecontract.ErrInvalidCursor)
	}
	if _, err := time.Parse(time.RFC3339Nano, parts[0]); err != nil {
		return "", "", fmt.Errorf("%w: request cursor timestamp", storagecontract.ErrInvalidCursor)
	}
	id := contract.RequestID(parts[1])
	if err := id.Validate(); err != nil {
		return "", "", fmt.Errorf("%w: request cursor id", storagecontract.ErrInvalidCursor)
	}
	return parts[0], string(id), nil
}

var _ storagecontract.RequestRecordStore = (*Store)(nil)
