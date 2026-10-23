package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// maxActivityCallSpan bounds how long before the window a call may have
// started and still be read, so each poll walks only the recent roots.
const maxActivityCallSpan = time.Hour

// ListRequestActivity reads the spans of root model calls running at or after
// since, newest first. A call the core interrupted is left out: recovery
// stamps when the core came back, not when the call stopped, so its span
// would cover the downtime.
func (store *Store) ListRequestActivity(
	ctx context.Context,
	since time.Time,
) ([]contract.RequestActivityCall, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT COALESCE(session_id, id), client_type, started_at, completed_at, status, http_status
FROM request_records INDEXED BY request_records_root_started_idx
WHERE parent_request_id IS NULL
  AND started_at >= ?
  AND (completed_at IS NULL OR completed_at >= ?)
  AND input_protocol NOT IN (?, ?)
  AND COALESCE(json_extract(error_json, '$.code'), '') <> 'core_interrupted'
ORDER BY started_at DESC, id DESC`,
		since.Add(-maxActivityCallSpan).UTC().Format(time.RFC3339Nano),
		since.UTC().Format(time.RFC3339Nano),
		string(contract.ProtocolOpenAIModels),
		string(contract.ProtocolGoogleModels),
	)
	if err != nil {
		return nil, fmt.Errorf("list request activity: %w", err)
	}
	defer rows.Close()
	calls := make([]contract.RequestActivityCall, 0)
	for rows.Next() {
		var sessionID, startedAt, status string
		var clientType, completedAt sql.NullString
		var httpStatus sql.NullInt64
		if err := rows.Scan(&sessionID, &clientType, &startedAt, &completedAt, &status, &httpStatus); err != nil {
			return nil, fmt.Errorf("scan request activity: %w", err)
		}
		call := contract.RequestActivityCall{
			SessionID:  contract.SessionID(sessionID),
			ClientType: contract.ClientType(clientType.String),
		}
		if call.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt); err != nil {
			return nil, fmt.Errorf("parse request activity start: %w", err)
		}
		if completedAt.Valid {
			completed, err := time.Parse(time.RFC3339Nano, completedAt.String)
			if err != nil {
				return nil, fmt.Errorf("parse request activity completion: %w", err)
			}
			call.CompletedAt = &completed
		}
		record := contract.RequestRecord{Status: contract.RequestStatus(status)}
		if httpStatus.Valid {
			value := int(httpStatus.Int64)
			record.HTTPStatus = &value
		}
		call.Status = record.EffectiveStatus()
		calls = append(calls, call)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate request activity: %w", err)
	}
	return calls, nil
}
