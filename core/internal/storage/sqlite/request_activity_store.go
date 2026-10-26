package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

// maxActivityCallSpan bounds how long before the window a call may have
// started and still be read, so each poll walks only the recent roots.
const maxActivityCallSpan = time.Hour

// ListRequestActivity reads the spans of root model calls running at or after
// since, newest first, with the output tokens of those that ended. A call that
// ended before since on the tool calls it asked for is read too while its
// conversation has made no later call before since: the client may still be
// running those tools. A call the core interrupted is left out: recovery
// stamps when the core came back, not when the call stopped, so its span would
// cover the downtime.
func (store *Store) ListRequestActivity(
	ctx context.Context,
	since time.Time,
) ([]contract.RequestActivityCall, error) {
	sinceText := since.UTC().Format(time.RFC3339Nano)
	models, googleModels := string(contract.ProtocolOpenAIModels), string(contract.ProtocolGoogleModels)
	// The later-call lookup applies the outer filters, so a call counts as the
	// next one only when this read could list it.
	rows, err := store.db.QueryContext(ctx, `
SELECT COALESCE(root.session_id, root.id), root.client_type, root.started_at, root.completed_at,
       root.status, root.http_status, root.output_json, root.parent_session_id,
       json_extract(root.usage_json, '$.output_tokens'), json_extract(root.usage_json, '$.billing_incomplete')
FROM request_records AS root INDEXED BY request_records_root_started_idx
WHERE root.parent_request_id IS NULL
  AND root.started_at >= ?
  AND root.input_protocol NOT IN (?, ?)
  AND COALESCE(json_extract(root.error_json, '$.code'), '') <> 'core_interrupted'
  AND (root.completed_at IS NULL OR root.completed_at >= ?
    OR (root.status = 'succeeded' AND COALESCE(root.http_status, 0) < 400
      AND json_extract(root.output_json, '$.kind') = 'tool'
      AND json_array_length(root.output_json, '$.tools') > 0
      AND NOT EXISTS (
        SELECT 1 FROM request_records AS later INDEXED BY request_records_session_turns_idx
        WHERE COALESCE(later.session_id, later.id) = COALESCE(root.session_id, root.id)
          AND later.parent_request_id IS NULL
          AND later.id <> root.id
          AND later.started_at >= root.completed_at
          AND later.started_at < ?
          AND later.input_protocol NOT IN (?, ?)
          AND COALESCE(json_extract(later.error_json, '$.code'), '') <> 'core_interrupted')))
ORDER BY root.started_at DESC, root.id DESC`,
		since.Add(-maxActivityCallSpan).UTC().Format(time.RFC3339Nano),
		models, googleModels,
		sinceText,
		sinceText,
		models, googleModels,
	)
	if err != nil {
		return nil, fmt.Errorf("list request activity: %w", err)
	}
	defer rows.Close()
	calls := make([]contract.RequestActivityCall, 0)
	for rows.Next() {
		var sessionID, startedAt, status string
		var clientType, completedAt, output, parent sql.NullString
		var httpStatus, outputTokens sql.NullInt64
		var incomplete sql.NullBool
		if err := rows.Scan(&sessionID, &clientType, &startedAt, &completedAt, &status, &httpStatus, &output, &parent,
			&outputTokens, &incomplete); err != nil {
			return nil, fmt.Errorf("scan request activity: %w", err)
		}
		call := contract.RequestActivityCall{
			SessionID:  contract.SessionID(sessionID),
			ClientType: contract.ClientType(clientType.String),
		}
		if parent.Valid && parent.String != "" && parent.String != sessionID {
			value := contract.SessionID(parent.String)
			call.ParentSessionID = &value
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
			// A stream cut short keeps the usage it saw so far, which undercounts.
			if outputTokens.Valid && outputTokens.Int64 > 0 && !incomplete.Bool {
				call.OutputTokens = int(outputTokens.Int64)
			}
		}
		record := contract.RequestRecord{Status: contract.RequestStatus(status)}
		if httpStatus.Valid {
			value := int(httpStatus.Int64)
			record.HTTPStatus = &value
		}
		call.Status = record.EffectiveStatus()
		if output.Valid && output.String != "" {
			call.Output = &contract.OutputActivity{}
			if err := json.Unmarshal([]byte(output.String), call.Output); err != nil {
				return nil, fmt.Errorf("%w: request activity output", storagecontract.ErrInvalidRecord)
			}
		}
		calls = append(calls, call)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate request activity: %w", err)
	}
	return calls, nil
}
