package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// GetUsageSummary streams only the fields used by inference statistics. Detailed
// plans, events, audit data, cursors, discovery and retry children stay in the database.
func (store *Store) GetUsageSummary(ctx context.Context, options storage.UsageSummaryOptions) (storage.UsageSummary, error) {
	result := storage.UsageSummary{
		ByDay: []storage.UsageTimeBucket{}, ByHour: []storage.UsageTimeBucket{},
		ByService: []storage.UsageGroup{}, ByModel: []storage.UsageGroup{},
	}
	zone, err := options.Validate()
	if err != nil {
		return result, err
	}
	// A second prefix includes fractional timestamps at the inclusive boundary
	// and excludes them at the exclusive boundary, including legacy exact seconds.
	rows, err := store.db.QueryContext(ctx, `SELECT started_at, status, http_status,
    service_id, requested_model, usage_json
FROM request_records
WHERE parent_request_id IS NULL AND started_at >= ? AND started_at < ?
  AND status IN ('succeeded', 'failed') AND input_protocol NOT IN (?, ?)`,
		options.From.UTC().Format("2006-01-02T15:04:05"), options.To.UTC().Format("2006-01-02T15:04:05"),
		string(contract.ProtocolOpenAIModels), string(contract.ProtocolGoogleModels))
	if err != nil {
		return result, fmt.Errorf("query usage summary: %w", err)
	}
	defer rows.Close()
	days, hours := map[string]*storage.UsageTimeBucket{}, map[string]*storage.UsageTimeBucket{}
	services, models := map[string]*storage.UsageGroup{}, map[string]*storage.UsageGroup{}
	for rows.Next() {
		var startedAt, status string
		var httpStatus sql.NullInt64
		var service, model, usageJSON sql.NullString
		if err := rows.Scan(&startedAt, &status, &httpStatus, &service, &model, &usageJSON); err != nil {
			return result, fmt.Errorf("scan usage summary: %w", err)
		}
		started, err := time.Parse(time.RFC3339Nano, startedAt)
		if err != nil {
			return result, fmt.Errorf("%w: usage timestamp", storage.ErrInvalidRecord)
		}
		if service.Valid && contract.ServiceID(service.String).Validate() != nil {
			return result, fmt.Errorf("%w: usage service", storage.ErrInvalidRecord)
		}
		local := started.In(zone)
		date := local.Format("2006-01-02")
		if days[date] == nil {
			days[date] = &storage.UsageTimeBucket{Date: date}
		}
		targets := []*storage.UsageTotals{&result.Totals, &days[date].UsageTotals}
		if options.Bucket == "hour" {
			key := local.Format("2006-01-02T15")
			if hours[key] == nil {
				hour := local.Hour()
				hours[key] = &storage.UsageTimeBucket{Date: date, Hour: &hour}
			}
			targets = append(targets, &hours[key].UsageTotals)
		}
		result.ScannedRecords++
		if status == "failed" || (httpStatus.Valid && httpStatus.Int64 >= 400) {
			for _, target := range targets {
				target.FailedRequests++
			}
			continue
		}
		var usage contract.Usage
		if usageJSON.Valid {
			if err := json.Unmarshal([]byte(usageJSON.String), &usage); err != nil || usage.Validate() != nil {
				return result, fmt.Errorf("%w: usage token counts", storage.ErrInvalidRecord)
			}
		}
		targets = append(targets, usageGroupTotals(services, service.String), usageGroupTotals(models, model.String))
		for _, target := range targets {
			target.Requests++
			target.InputTokens += int64(usage.InputTokens)
			target.OutputTokens += int64(usage.OutputTokens)
			target.TotalTokens += int64(usage.TotalTokens)
			if usage.CacheReadTokens != nil {
				target.CacheReadTokens += int64(*usage.CacheReadTokens)
			}
			if usage.CacheWriteTokens != nil {
				target.CacheWriteTokens += int64(*usage.CacheWriteTokens)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("read usage summary: %w", err)
	}
	for _, day := range days {
		result.ByDay = append(result.ByDay, *day)
	}
	for _, hour := range hours {
		result.ByHour = append(result.ByHour, *hour)
	}
	sort.Slice(result.ByDay, func(i, j int) bool { return result.ByDay[i].Date < result.ByDay[j].Date })
	sort.Slice(result.ByHour, func(i, j int) bool {
		a, b := result.ByHour[i], result.ByHour[j]
		return a.Date < b.Date || (a.Date == b.Date && *a.Hour < *b.Hour)
	})
	result.ByService = sortedUsageGroups(services)
	result.ByModel = sortedUsageGroups(models)
	return result, nil
}

func usageGroupTotals(groups map[string]*storage.UsageGroup, id string) *storage.UsageTotals {
	id = strings.TrimSpace(id)
	if groups[id] == nil {
		group := &storage.UsageGroup{}
		if id != "" {
			group.ID = &id
		}
		groups[id] = group
	}
	return &groups[id].UsageTotals
}

func sortedUsageGroups(groups map[string]*storage.UsageGroup) []storage.UsageGroup {
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := groups[keys[i]], groups[keys[j]]
		if a.TotalTokens != b.TotalTokens {
			return a.TotalTokens > b.TotalTokens
		}
		if a.Requests != b.Requests {
			return a.Requests > b.Requests
		}
		return keys[i] < keys[j]
	})
	result := make([]storage.UsageGroup, 0, len(keys))
	for _, key := range keys {
		result = append(result, *groups[key])
	}
	return result
}
