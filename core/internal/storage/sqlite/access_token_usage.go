package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

// currentAccessTokenIDs returns the bounded set of currently persisted token IDs
// used by usage and billing breakdowns. Historical request and billing rows may
// mention deleted tokens; those IDs are intentionally excluded from current
// token breakdowns.
func (store *Store) currentAccessTokenIDs(ctx context.Context) (map[string]struct{}, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id FROM local_access_tokens ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list current access token IDs: %w", err)
	}
	defer rows.Close()
	ids := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan current access token ID: %w", err)
		}
		if contract.AccessTokenID(id).Validate() != nil {
			return nil, fmt.Errorf("%w: invalid current access token ID", storagecontract.ErrInvalidRecord)
		}
		ids[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate current access token IDs: %w", err)
	}
	return ids, nil
}

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
SELECT local_access_token_id, started_at >= ?,
       json_extract(usage_json, '$.total_tokens'), json_type(usage_json, '$.total_tokens'),
       usage_json, input_protocol, streaming, latency_ms, first_token_ms
FROM request_records
WHERE parent_request_id IS NULL AND status = 'succeeded' AND local_access_token_id IS NOT NULL
  AND (http_status IS NULL OR http_status < 400)`, boundary)
	if err != nil {
		return nil, fmt.Errorf("aggregate access token usage: %w", err)
	}
	defer rows.Close()
	type accumulator struct {
		item         storagecontract.AccessTokenUsage
		today, total servicePerformance
	}
	byToken := make(map[contract.AccessTokenID]*accumulator)
	for rows.Next() {
		var id contract.AccessTokenID
		var today, streaming bool
		var totalRaw any
		var totalType, usageJSON sql.NullString
		var latency, first sql.NullInt64
		var protocol contract.ProtocolID
		if err := rows.Scan(&id, &today, &totalRaw, &totalType, &usageJSON, &protocol, &streaming, &latency, &first); err != nil {
			return nil, fmt.Errorf("read access token usage: %w", err)
		}
		if id.Validate() != nil {
			return nil, fmt.Errorf("%w: invalid access token ID", storagecontract.ErrInvalidRecord)
		}
		var usage contract.Usage
		var count int64
		if usageJSON.Valid {
			var ok bool
			if count, ok = totalRaw.(int64); !ok || count < 0 || totalType.String != "integer" {
				return nil, fmt.Errorf("%w: invalid access token usage", storagecontract.ErrInvalidRecord)
			}
			if json.Unmarshal([]byte(usageJSON.String), &usage) != nil || usage.Validate() != nil {
				return nil, fmt.Errorf("%w: invalid access token performance usage", storagecontract.ErrInvalidRecord)
			}
		}
		if byToken[id] == nil {
			byToken[id] = &accumulator{item: storagecontract.AccessTokenUsage{TokenID: id}}
		}
		group := byToken[id]
		if count > math.MaxInt64-group.item.TotalTokens {
			return nil, fmt.Errorf("%w: access token usage overflow", storagecontract.ErrInvalidRecord)
		}
		group.item.TotalTokens += count
		if today {
			group.item.TodayTokens += count
		}
		// Preserve existing token totals; performance only measures inference.
		if protocol != contract.ProtocolOpenAIModels && protocol != contract.ProtocolGoogleModels {
			group.total.observe(usage, streaming, latency, first)
			if today {
				group.today.observe(usage, streaming, latency, first)
			}
		}
	}
	items := make([]storagecontract.AccessTokenUsage, 0, len(byToken))
	for _, group := range byToken {
		group.item.TodayPerformance = *group.today.summary()
		group.item.TotalPerformance = *group.total.summary()
		items = append(items, group.item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate access token usage: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return store.addAccessTokenBilling(ctx, todayFrom, items)
}

type tokenBillingAccumulator struct {
	storagecontract.AccessTokenBilling
	total big.Rat
	roots map[string]struct{}
}

func (a *tokenBillingAccumulator) add(root, reason string, amount *big.Rat, revalued bool) {
	if a.roots == nil {
		a.roots = make(map[string]struct{})
	}
	a.roots[root] = struct{}{}
	switch reason {
	case "priced":
		a.Priced++
		a.total.Add(&a.total, amount)
	case "pending":
		a.Pending++
	default:
		a.Unpriced++
	}
	if revalued {
		a.Revalued++
	}
}

func (a *tokenBillingAccumulator) amounts() storagecontract.AccessTokenBilling {
	a.AmountUSD = a.total.FloatString(9)
	a.Requests = int64(len(a.roots))
	return a.AccessTokenBilling
}

// Scan the ledger once for both periods. Its decimal amounts must never pass
// through SQLite SUM or floating point, and survive request-history cleanup.
func (store *Store) addAccessTokenBilling(ctx context.Context, todayFrom time.Time, items []storagecontract.AccessTokenUsage) ([]storagecontract.AccessTokenUsage, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT local_access_token_id, root_id,
started_at >= ?, amount_usd, reason, revalued
FROM billing_ledger WHERE local_access_token_id IS NOT NULL`, billingTime(todayFrom))
	if err != nil {
		return nil, fmt.Errorf("aggregate access token billing: %w", err)
	}
	defer rows.Close()
	type periods struct{ today, total tokenBillingAccumulator }
	byToken := make(map[contract.AccessTokenID]*periods)
	for rows.Next() {
		var id contract.AccessTokenID
		var root, raw, reason string
		var today, revalued bool
		if err := rows.Scan(&id, &root, &today, &raw, &reason, &revalued); err != nil {
			return nil, err
		}
		if id.Validate() != nil {
			return nil, fmt.Errorf("%w: invalid billing token ID", storagecontract.ErrInvalidRecord)
		}
		var amount *big.Rat
		if reason == "priced" {
			var ok bool
			amount, ok = new(big.Rat).SetString(raw)
			if !ok || amount.Sign() < 0 {
				return nil, fmt.Errorf("%w: invalid billing amount", storagecontract.ErrInvalidRecord)
			}
		}
		if byToken[id] == nil {
			byToken[id] = &periods{}
		}
		group := byToken[id]
		group.total.add(root, reason, amount, revalued)
		if today {
			group.today.add(root, reason, amount, revalued)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range items {
		group := byToken[items[i].TokenID]
		if group == nil {
			group = &periods{}
		}
		items[i].TodayBilling = group.today.amounts()
		items[i].TotalBilling = group.total.amounts()
		delete(byToken, items[i].TokenID)
	}
	for id, group := range byToken {
		items = append(items, storagecontract.AccessTokenUsage{
			TokenID: id, TodayBilling: group.today.amounts(), TotalBilling: group.total.amounts(),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].TokenID < items[j].TokenID })
	return items, nil
}
