package sqlite

import (
	"context"
	"fmt"
	"time"

	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

// ListAccessTokenUsage aggregates both periods for every token in one scan,
// without loading request details or applying the request-list pagination cap.
func (store *Store) ListAccessTokenUsage(ctx context.Context, todayFrom time.Time) ([]storagecontract.AccessTokenUsage, error) {
	if todayFrom.IsZero() || todayFrom.Nanosecond() != 0 {
		return nil, fmt.Errorf("%w: today_from must be a whole-second timestamp", storagecontract.ErrInvalidArgument)
	}
	// Stored timestamps are UTC RFC3339Nano. A second-prefix boundary includes
	// both the exact second (...00Z) and its fractions (...00.123Z).
	boundary := todayFrom.UTC().Format("2006-01-02T15:04:05")
	rows, err := store.db.QueryContext(ctx, `
SELECT local_access_token_id,
       COALESCE(SUM(CASE WHEN started_at >= ? THEN json_extract(usage_json, '$.total_tokens') ELSE 0 END), 0),
       COALESCE(SUM(json_extract(usage_json, '$.total_tokens')), 0),
       SUM(CASE WHEN usage_json IS NOT NULL AND
           (COALESCE(json_type(usage_json, '$.total_tokens'), '') <> 'integer'
            OR json_extract(usage_json, '$.total_tokens') < 0) THEN 1 ELSE 0 END)
FROM request_records
WHERE parent_request_id IS NULL AND status = 'succeeded' AND local_access_token_id IS NOT NULL
  AND (http_status IS NULL OR http_status < 400)
GROUP BY local_access_token_id
ORDER BY local_access_token_id`, boundary)
	if err != nil {
		return nil, fmt.Errorf("aggregate access token usage: %w", err)
	}
	defer rows.Close()
	items := make([]storagecontract.AccessTokenUsage, 0)
	for rows.Next() {
		var item storagecontract.AccessTokenUsage
		var todayRaw, totalRaw any
		var invalid int
		if err := rows.Scan(&item.TokenID, &todayRaw, &totalRaw, &invalid); err != nil {
			return nil, fmt.Errorf("read access token usage: %w", err)
		}
		if item.TokenID.Validate() != nil || invalid != 0 {
			return nil, fmt.Errorf("%w: invalid access token usage", storagecontract.ErrInvalidRecord)
		}
		var ok bool
		if item.TodayTokens, ok = todayRaw.(int64); !ok || item.TodayTokens < 0 {
			return nil, fmt.Errorf("%w: invalid access token usage", storagecontract.ErrInvalidRecord)
		}
		if item.TotalTokens, ok = totalRaw.(int64); !ok || item.TotalTokens < item.TodayTokens {
			return nil, fmt.Errorf("%w: invalid access token usage", storagecontract.ErrInvalidRecord)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate access token usage: %w", err)
	}
	return items, nil
}
