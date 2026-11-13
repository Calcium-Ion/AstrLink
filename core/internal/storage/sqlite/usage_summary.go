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
	return cachedHistoryRead(ctx, store.db, &store.usageSummaries, options, func(conn *sql.Conn) (storage.UsageSummary, error) {
		return getUsageSummary(ctx, conn, options)
	})
}

func getUsageSummary(ctx context.Context, reader historyReader, options storage.UsageSummaryOptions) (storage.UsageSummary, error) {
	result := storage.UsageSummary{
		ByDay: []storage.UsageTimeBucket{}, ByHour: []storage.UsageTimeBucket{}, ByHourModel: []storage.UsageHourModelBucket{},
		ByService: []storage.UsageGroup{}, ByModel: []storage.UsageGroup{}, ByToken: []storage.UsageGroup{},
		ByClient: []storage.UsageGroup{}, ByRoute: []storage.UsageRoute{},
	}
	zone, err := options.Validate()
	if err != nil {
		return result, err
	}
	tokenWhitelist, err := currentAccessTokenIDs(ctx, reader)
	if err != nil {
		return result, err
	}
	// A second prefix includes fractional timestamps at the inclusive boundary
	// and excludes them at the exclusive boundary, including legacy exact seconds.
	// Models group by what was sent upstream, matching billing: the served
	// model, then a routing redirect target, then the client's model.
	// Pending calls have no outcome yet, the client abandoned cancelled ones,
	// and a policy block never reached a provider, so none of them counts as a
	// request or a failure; they only add to StatusCounts. This matches the
	// desktop, where a recorded "blocked" is its own outcome rather than a
	// failure, and where a legacy row stored as succeeded with a 4xx/5xx
	// counts as failed.
	withDetail := options.To.Sub(options.From) <= storage.UsageDetailMaxSpan
	routeColumns := noGatewayRewriteColumns
	if withDetail {
		routeColumns = gatewayRewriteColumns("request_records")
	}
	rows, err := reader.QueryContext(ctx, `SELECT started_at, status, http_status,
    service_id, COALESCE(
        NULLIF(json_extract(NULLIF(recovery_json, ''), '$.upstream_model'), ''),
        NULLIF(json_extract(NULLIF(model_redirect_json, ''), '$.to'), ''),
        requested_model), local_access_token_id, usage_json, latency_ms, client_type,
    `+routeColumns+`
FROM request_records
WHERE parent_request_id IS NULL AND started_at >= ? AND started_at < ?
  AND input_protocol NOT IN (?, ?)`,
		options.From.UTC().Format("2006-01-02T15:04:05"), options.To.UTC().Format("2006-01-02T15:04:05"),
		string(contract.ProtocolOpenAIModels), string(contract.ProtocolGoogleModels))
	if err != nil {
		return result, fmt.Errorf("query usage summary: %w", err)
	}
	defer rows.Close()
	days, hours := map[string]*storage.UsageTimeBucket{}, map[string]*storage.UsageTimeBucket{}
	services, models, tokens := map[string]*storage.UsageGroup{}, map[string]*storage.UsageGroup{}, map[string]*storage.UsageGroup{}
	clients := map[string]*storage.UsageGroup{}
	hourModels := map[usageHourModelKey]*storage.UsageHourModelBucket{}
	routes := map[string]*storage.UsageRoute{}
	performance := map[string]*servicePerformance{}
	tokenPerformance := map[string]*servicePerformance{}
	var startedAt, status string
	var httpStatus, latency sql.NullInt64
	var service, model, localAccessTokenID, usageJSON, clientType sql.NullString
	var rewritten gatewayRewriteRow
	for rows.Next() {
		if err := rows.Scan(append([]any{&startedAt, &status, &httpStatus, &service, &model, &localAccessTokenID, &usageJSON, &latency, &clientType},
			rewritten.targets()...)...); err != nil {
			return result, fmt.Errorf("scan usage summary: %w", err)
		}
		switch contract.RequestStatus(status) {
		case contract.RequestStatusSucceeded, contract.RequestStatusFailed:
		case contract.RequestStatusBlocked:
			result.StatusCounts.Blocked++
			continue
		case contract.RequestStatusCancelled:
			result.StatusCounts.Cancelled++
			continue
		case contract.RequestStatusPending:
			result.StatusCounts.Pending++
			continue
		default:
			return result, fmt.Errorf("%w: usage status", storage.ErrInvalidRecord)
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

		// Service and model groups retain their historical null-ID bucket. Token
		// groups use the current-token whitelist and never create id: null.
		targets = append(targets,
			usageGroupTotals(services, service.String),
			usageGroupTotals(models, model.String),
			usageGroupTotals(clients, usageClientID(clientType)),
		)
		if localAccessTokenID.Valid {
			if _, ok := tokenWhitelist[localAccessTokenID.String]; ok {
				targets = append(targets, usageGroupTotals(tokens, localAccessTokenID.String))
			}
		}

		result.ScannedRecords++
		// The same definition the desktop shows per call: an explicit failure,
		// or a call stored as succeeded that came back 4xx/5xx. A failure
		// consumes no tokens and is not a request here, so it only ever adds to
		// FailedRequests.
		failed := status == "failed" || (httpStatus.Valid && httpStatus.Int64 >= 400)
		if withDetail {
			usageRouteFor(routes, usageClientID(clientType), service, rewritten, failed).Requests++
		}
		if failed {
			result.StatusCounts.Failed++
			for _, target := range targets {
				target.FailedRequests++
			}
			continue
		}
		result.StatusCounts.Succeeded++
		var usage contract.Usage
		if usageJSON.Valid {
			if err := json.Unmarshal([]byte(usageJSON.String), &usage); err != nil || usage.Validate() != nil {
				return result, fmt.Errorf("%w: usage token counts", storage.ErrInvalidRecord)
			}
		}
		if service.Valid {
			if performance[service.String] == nil {
				performance[service.String] = &servicePerformance{}
			}
			performance[service.String].observe(usage, latency)
		}
		if _, ok := tokenWhitelist[localAccessTokenID.String]; localAccessTokenID.Valid && ok {
			if tokenPerformance[localAccessTokenID.String] == nil {
				tokenPerformance[localAccessTokenID.String] = &servicePerformance{}
			}
			tokenPerformance[localAccessTokenID.String].observe(usage, latency)
		}
		if withDetail {
			bucket := usageHourModelBucket(hourModels, date, local.Hour(), model.String)
			bucket.Requests++
			bucket.TotalTokens += int64(usage.TotalTokens)
		}
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
	for _, bucket := range hourModels {
		result.ByHourModel = append(result.ByHourModel, *bucket)
	}
	sort.Slice(result.ByDay, func(i, j int) bool { return result.ByDay[i].Date < result.ByDay[j].Date })
	sort.Slice(result.ByHour, func(i, j int) bool {
		a, b := result.ByHour[i], result.ByHour[j]
		return a.Date < b.Date || (a.Date == b.Date && *a.Hour < *b.Hour)
	})
	sort.Slice(result.ByHourModel, func(i, j int) bool {
		a, b := result.ByHourModel[i], result.ByHourModel[j]
		if a.Date != b.Date || a.Hour != b.Hour {
			return a.Date < b.Date || (a.Date == b.Date && a.Hour < b.Hour)
		}
		if a.TotalTokens != b.TotalTokens {
			return a.TotalTokens > b.TotalTokens
		}
		if a.Requests != b.Requests {
			return a.Requests > b.Requests
		}
		// A null model sorts first, like the empty key it is grouped under.
		return b.Model != nil && (a.Model == nil || *a.Model < *b.Model)
	})
	for id, group := range services {
		group.Performance = performance[id].summary()
	}
	for id, group := range tokens {
		group.Performance = tokenPerformance[id].summary()
	}
	result.ByService = sortedUsageGroups(services)
	result.ByModel = sortedUsageGroups(models)
	result.ByToken = sortedUsageGroups(tokens)
	result.ByClient = sortedUsageGroups(clients)
	result.ByRoute = sortedUsageRoutes(routes)
	return result, nil
}

// usageRouteFor finds or adds the route of one call. A failover target equal
// to the call's own provider cannot happen; it is dropped all the same.
func usageRouteFor(routes map[string]*storage.UsageRoute, client string, service sql.NullString, rewritten gatewayRewriteRow, failed bool) *storage.UsageRoute {
	rewrites := rewritten.rewrites()
	from := rewritten.failedService
	if from.Valid && service.Valid && from.String == service.String {
		from = sql.NullString{}
	}
	parts := []string{client, service.String, from.String, fmt.Sprint(failed)}
	for _, rewrite := range rewrites {
		parts = append(parts, string(rewrite))
	}
	key := strings.Join(parts, "\x00")
	if routes[key] == nil {
		route := &storage.UsageRoute{Failed: failed, Rewrites: rewrites}
		if client != "" {
			route.ClientType = &client
		}
		if service.Valid && service.String != "" {
			id := service.String
			route.ServiceID = &id
		}
		if from.Valid && from.String != "" {
			id := from.String
			route.FailedServiceID = &id
		}
		routes[key] = route
	}
	return routes[key]
}

// sortedUsageRoutes lists the busiest routes first, then by their fields so
// equal counts keep one order.
func sortedUsageRoutes(routes map[string]*storage.UsageRoute) []storage.UsageRoute {
	keys := make([]string, 0, len(routes))
	for key := range routes {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := routes[keys[i]], routes[keys[j]]
		if a.Requests != b.Requests {
			return a.Requests > b.Requests
		}
		return keys[i] < keys[j]
	})
	sorted := make([]storage.UsageRoute, 0, len(keys))
	for _, key := range keys {
		sorted = append(sorted, *routes[key])
	}
	return sorted
}

type usageHourModelKey struct {
	date  string
	hour  int
	model string
}

func usageHourModelBucket(buckets map[usageHourModelKey]*storage.UsageHourModelBucket, date string, hour int, model string) *storage.UsageHourModelBucket {
	model = strings.TrimSpace(model)
	key := usageHourModelKey{date: date, hour: hour, model: model}
	if buckets[key] == nil {
		bucket := &storage.UsageHourModelBucket{Date: date, Hour: hour}
		if model != "" {
			bucket.Model = &model
		}
		buckets[key] = bucket
	}
	return buckets[key]
}

// usageClientID folds unknown clients, historical records without one, and a
// label this build does not recognize into the null group.
func usageClientID(clientType sql.NullString) string {
	client := contract.ClientType(clientType.String)
	if !clientType.Valid || client == contract.ClientUnknown || !client.Valid() {
		return ""
	}
	return string(client)
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
