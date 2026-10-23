package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// GetPrivacySummary streams only the privacy columns of finished root calls,
// over the same window rules as GetUsageSummary. Retry children are skipped
// because the root carries the decision all attempts combined into. Pending
// calls are skipped because their restore counts are still growing. Blocked
// and cancelled calls stay in: the policy acted on them before they ended.
func (store *Store) GetPrivacySummary(ctx context.Context, options storage.PrivacySummaryOptions) (storage.PrivacySummary, error) {
	result := storage.PrivacySummary{
		ByDay: []storage.PrivacyTimeBucket{}, ByHour: []storage.PrivacyTimeBucket{}, ByKind: []storage.PrivacyKindCount{},
	}
	zone, err := options.Validate()
	if err != nil {
		return result, err
	}
	rows, err := store.db.QueryContext(ctx, `SELECT started_at, status, privacy_decision, privacy_restore_json, privacy_findings_json,
  privacy_ignored_json
FROM request_records
WHERE parent_request_id IS NULL AND started_at >= ? AND started_at < ?
  AND status IN ('succeeded', 'failed', 'blocked', 'cancelled') AND input_protocol NOT IN (?, ?)`,
		options.From.UTC().Format("2006-01-02T15:04:05"), options.To.UTC().Format("2006-01-02T15:04:05"),
		string(contract.ProtocolOpenAIModels), string(contract.ProtocolGoogleModels))
	if err != nil {
		return result, fmt.Errorf("query privacy summary: %w", err)
	}
	defer rows.Close()
	days, hours := map[string]*storage.PrivacyTimeBucket{}, map[string]*storage.PrivacyTimeBucket{}
	kinds := map[string]int64{}
	for rows.Next() {
		var startedAt, status string
		var decision, restoreJSON, findingsJSON, ignoredJSON sql.NullString
		if err := rows.Scan(&startedAt, &status, &decision, &restoreJSON, &findingsJSON, &ignoredJSON); err != nil {
			return result, fmt.Errorf("scan privacy summary: %w", err)
		}
		started, err := time.Parse(time.RFC3339Nano, startedAt)
		if err != nil {
			return result, fmt.Errorf("%w: privacy timestamp", storage.ErrInvalidRecord)
		}
		var restore *contract.PrivacyRestoreSummary
		if restoreJSON.Valid {
			restore = &contract.PrivacyRestoreSummary{}
			if err := json.Unmarshal([]byte(restoreJSON.String), restore); err != nil || restore.Validate() != nil {
				return result, fmt.Errorf("%w: privacy restore counts", storage.ErrInvalidRecord)
			}
		}
		var findings []contract.PrivacyFinding
		if findingsJSON.Valid && findingsJSON.String != "" {
			if err := json.Unmarshal([]byte(findingsJSON.String), &findings); err != nil {
				return result, fmt.Errorf("%w: privacy findings", storage.ErrInvalidRecord)
			}
		}
		var ignored privacyIgnoredCounts
		if ignoredJSON.Valid && ignoredJSON.String != "" {
			var entries []contract.PrivacyIgnored
			if err := json.Unmarshal([]byte(ignoredJSON.String), &entries); err != nil {
				return result, fmt.Errorf("%w: privacy ignored", storage.ErrInvalidRecord)
			}
			for _, entry := range entries {
				if !entry.Kind.Valid() || !entry.Reason.Valid() || entry.Count < 1 {
					return result, fmt.Errorf("%w: privacy ignored", storage.ErrInvalidRecord)
				}
				switch entry.Reason {
				case contract.PrivacyIgnoreAllowlisted:
					ignored.allowlisted += int64(entry.Count)
				case contract.PrivacyIgnoreLowConfidence:
					ignored.lowConfidence += int64(entry.Count)
				}
			}
		}
		outcome, err := privacyOutcome(decision, status, restore != nil)
		if err != nil {
			return result, err
		}

		local := started.In(zone)
		date := local.Format("2006-01-02")
		if days[date] == nil {
			days[date] = &storage.PrivacyTimeBucket{Date: date}
		}
		targets := []*storage.PrivacyTotals{&result.Totals, &days[date].PrivacyTotals}
		if options.Bucket == "hour" {
			key := local.Format("2006-01-02T15")
			if hours[key] == nil {
				hour := local.Hour()
				hours[key] = &storage.PrivacyTimeBucket{Date: date, Hour: &hour}
			}
			targets = append(targets, &hours[key].PrivacyTotals)
		}
		for _, target := range targets {
			addPrivacyCall(target, outcome, restore, ignored)
		}

		// A redacted call names its kinds by distinct replaced value; a call
		// that was warned on or blocked has only its findings to go by.
		if restore != nil && len(restore.Hits) > 0 {
			for _, hit := range restore.Hits {
				kinds[string(hit.Kind)] += int64(hit.Count)
			}
			continue
		}
		for _, finding := range findings {
			if !finding.Kind.Valid() || finding.Count < 1 {
				return result, fmt.Errorf("%w: privacy finding", storage.ErrInvalidRecord)
			}
			kinds[string(finding.Kind)] += int64(finding.Count)
		}
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("read privacy summary: %w", err)
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
	order := contract.PrivacyKinds()
	for kind, count := range kinds {
		result.ByKind = append(result.ByKind, storage.PrivacyKindCount{Kind: kind, Count: count})
	}
	sort.Slice(result.ByKind, func(i, j int) bool {
		a, b := result.ByKind[i], result.ByKind[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return slices.Index(order, a.Kind) < slices.Index(order, b.Kind)
	})
	return result, nil
}

// privacyOutcome reads the recorded decision. Rows written before decisions
// existed fall back to their own evidence: only the privacy policy blocks a
// call, and a restore summary is written only when values were replaced.
// Empty means no decision is known, so the call counts as unchecked.
func privacyOutcome(decision sql.NullString, status string, restored bool) (contract.PrivacyDecision, error) {
	if decision.Valid {
		outcome := contract.PrivacyDecision(decision.String)
		if !outcome.Valid() {
			return "", fmt.Errorf("%w: privacy decision", storage.ErrInvalidRecord)
		}
		return outcome, nil
	}
	switch {
	case status == string(contract.RequestStatusBlocked):
		return contract.PrivacyDecisionBlock, nil
	case restored:
		return contract.PrivacyDecisionRedact, nil
	default:
		return "", nil
	}
}

// privacyIgnoredCounts are one call's matches left in place by the allowlist
// or the confidence threshold. Matches of a disabled kind are not among them:
// the user chose not to protect that kind. Nor are AstrLink's own identifiers,
// which hold nothing to protect.
type privacyIgnoredCounts struct {
	allowlisted, lowConfidence int64
}

func addPrivacyCall(
	target *storage.PrivacyTotals,
	outcome contract.PrivacyDecision,
	restore *contract.PrivacyRestoreSummary,
	ignored privacyIgnoredCounts,
) {
	target.Requests++
	target.IgnoredAllowlisted += ignored.allowlisted
	target.IgnoredLowConfidence += ignored.lowConfidence
	switch outcome {
	case contract.PrivacyDecisionAllow:
		if ignored.allowlisted+ignored.lowConfidence > 0 {
			target.Ignored++
		} else {
			target.Allowed++
		}
	case contract.PrivacyDecisionWarn:
		target.Warned++
	case contract.PrivacyDecisionRedact:
		target.Redacted++
	case contract.PrivacyDecisionBlock:
		target.Blocked++
	case contract.PrivacyDecisionFailOpen:
		target.FailOpen++
	case contract.PrivacyDecisionTrusted:
		target.Trusted++
	}
	if restore == nil {
		return
	}
	target.Replaced += int64(restore.MappingCount)
	target.Restored += int64(restore.RestoredCount)
	target.RestoredVisible += int64(restore.VisibleRestoredCount)
	target.RestoredToolArguments += int64(restore.ToolArgumentRestoredCount)
	target.Unrestored += int64(restore.FallbackCount)
}
