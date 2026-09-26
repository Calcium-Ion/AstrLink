package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type statisticsAccumulator struct {
	storage.StatisticsTotals
	amount, eligibleAmount big.Rat
	first, duration, tps   float64
}

func (a *statisticsAccumulator) add(raw, reason, amount string, first, duration sql.NullInt64) {
	a.Records++
	var u *contract.Usage
	valid := json.Unmarshal([]byte(raw), &u) == nil && u != nil && u.Validate() == nil && !u.BillingIncomplete
	cost, ok := new(big.Rat).SetString(amount)
	if reason == "priced" && ok {
		a.Priced++
		a.amount.Add(&a.amount, cost)
		if valid && u.InputTokens+u.OutputTokens > 0 {
			a.CostSamples++
			a.InputTokens += int64(u.InputTokens)
			a.OutputTokens += int64(u.OutputTokens)
			a.eligibleAmount.Add(&a.eligibleAmount, cost)
		}
	} else {
		a.Unpriced++
	}
	if valid && u.CacheReadTokens != nil && *u.CacheReadTokens <= u.InputTokens {
		a.CacheSamples++
		a.CacheReadTokens += int64(*u.CacheReadTokens)
		a.CacheInputTokens += int64(u.InputTokens)
		if *u.CacheReadTokens > 0 {
			a.CacheHits++
		}
	}
	if first.Valid && first.Int64 >= 0 {
		a.FirstTokenSamples++
		a.first += float64(first.Int64)
	}
	if duration.Valid && duration.Int64 > 0 {
		a.DurationSamples++
		a.duration += float64(duration.Int64)
		if valid {
			a.TPSSamples++
			a.tps += float64(u.OutputTokens) * 1000 / float64(duration.Int64)
		}
	}
}
func (a *statisticsAccumulator) finish() storage.StatisticsTotals {
	a.AmountUSD = a.amount.FloatString(9)
	if tokens := a.InputTokens + a.OutputTokens; tokens > 0 {
		v := new(big.Rat).Mul(&a.eligibleAmount, big.NewRat(1000000, tokens)).FloatString(9)
		a.PerMillionUSD = &v
	}
	mean := func(total float64, n int64) *float64 {
		if n == 0 {
			return nil
		}
		v := total / float64(n)
		return &v
	}
	a.FirstTokenMS = mean(a.first, a.FirstTokenSamples)
	a.DurationMS = mean(a.duration, a.DurationSamples)
	a.TPS = mean(a.tps, a.TPSSamples)
	return a.StatisticsTotals
}
func (s *Store) ServiceStatistics(ctx context.Context, id contract.ServiceID, from, to time.Time) (storage.ServiceStatistics, error) {
	result := storage.ServiceStatistics{From: from, To: to, Models: []storage.StatisticsModel{}}
	if from.IsZero() || !to.After(from) || to.Sub(from) > 31*24*time.Hour || from.Nanosecond() != 0 || to.Nanosecond() != 0 {
		return result, fmt.Errorf("%w: 请选择不超过 31 天的时间范围", storage.ErrInvalidArgument)
	}
	if _, err := s.GetService(ctx, id); err != nil {
		return result, err
	}
	// The ledger uniquely owns each actual upstream attempt. Root records are
	// updated on retry, while children retain earlier attempts; never sum both.
	rows, err := s.db.QueryContext(ctx, `WITH metadata AS (
 SELECT COALESCE(parent_request_id,id) root_id,attempt_index,requested_model,usage_json,first_token_ms,latency_ms,
 ROW_NUMBER() OVER(PARTITION BY COALESCE(parent_request_id,id),attempt_index ORDER BY parent_request_id IS NULL,id) n
 FROM request_records WHERE service_id=? AND started_at>=? AND started_at<? AND attempt_index>=1
 AND status IN ('succeeded','failed','cancelled') AND input_protocol NOT IN ('openai.models','google.models')
 )
 SELECT b.model,COALESCE(b.usage_json,'null'),b.reason,b.amount_usd,m.first_token_ms,m.latency_ms
 FROM billing_ledger b LEFT JOIN metadata m ON m.root_id=b.root_id AND m.attempt_index=b.attempt AND m.n=1
 WHERE b.service_id=? AND b.started_at>=? AND b.started_at<? AND b.terminal=1
 UNION ALL
 SELECT COALESCE(m.requested_model,''),COALESCE(m.usage_json,'null'),'missing_price','0',m.first_token_ms,m.latency_ms
 FROM metadata m WHERE m.n=1 AND NOT EXISTS(SELECT 1 FROM billing_ledger b WHERE b.root_id=m.root_id AND b.attempt=m.attempt_index)`,
		id, from.UTC().Format("2006-01-02T15:04:05"), to.UTC().Format("2006-01-02T15:04:05"), id, billingTime(from), billingTime(to))
	if err != nil {
		return result, err
	}
	defer rows.Close()
	total := &statisticsAccumulator{}
	models := map[string]*statisticsAccumulator{}
	for rows.Next() {
		var model, raw, reason, amount string
		var first, duration sql.NullInt64
		if err = rows.Scan(&model, &raw, &reason, &amount, &first, &duration); err != nil {
			return result, err
		}
		if models[model] == nil {
			models[model] = &statisticsAccumulator{}
		}
		total.add(raw, reason, amount, first, duration)
		models[model].add(raw, reason, amount, first, duration)
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	result.StatisticsTotals = total.finish()
	for model, a := range models {
		result.Models = append(result.Models, storage.StatisticsModel{Model: model, StatisticsTotals: a.finish()})
	}
	sort.Slice(result.Models, func(i, j int) bool { return result.Models[i].Model < result.Models[j].Model })
	return result, nil
}
