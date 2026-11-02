package sqlite

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// QuotaEstimates values the whole official quota windows of every service
// with observed windows, from its signed-in account's windows of the last
// pricing.QuotaEstimateLookback. Windows of an earlier account, and windows
// that began before billing started recording, are left out.
func (s *Store) QuotaEstimates(ctx context.Context) (pricing.QuotaEstimates, error) {
	result := pricing.QuotaEstimates{Services: []pricing.ServiceQuotaEstimate{}}
	var since string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM billing_metadata WHERE key='recording_since'`).Scan(&since); err != nil {
		return result, err
	}
	recordingSince, _ := time.Parse(time.RFC3339Nano, since)
	type window struct{ service, account, kind string }
	rows, err := s.db.QueryContext(ctx, `SELECT service_id,account_key,kind,start_at,end_at,observed_at,used_percent FROM billing_periods
WHERE end_at>? AND kind IN ('primary','secondary')`, billingTime(s.now().Add(-pricing.QuotaEstimateLookback)))
	if err != nil {
		return result, err
	}
	samples := map[window][]pricing.QuotaSample{}
	for rows.Next() {
		var key window
		var start, end, observed string
		var sample pricing.QuotaSample
		if err = rows.Scan(&key.service, &key.account, &key.kind, &start, &end, &observed, &sample.UsedPercent); err != nil {
			rows.Close()
			return result, err
		}
		sample.Start, _ = time.Parse(time.RFC3339Nano, start)
		sample.End, _ = time.Parse(time.RFC3339Nano, end)
		sample.ObservedAt, _ = time.Parse(time.RFC3339Nano, observed)
		samples[key] = append(samples[key], sample)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	accounts := map[string]string{}
	estimates := map[string]*pricing.ServiceQuotaEstimate{}
	for key, list := range samples {
		account, known := accounts[key.service]
		if !known {
			record, err := s.GetService(ctx, contract.ServiceID(key.service))
			switch {
			case errors.Is(err, storage.ErrNotFound):
			case err != nil:
				return result, err
			default:
				account = pricing.AccountKey(record.Service)
			}
			accounts[key.service] = account
		}
		if account != key.account {
			continue
		}
		windows := pricing.DistinctQuotaWindows(list)
		read := windows[:0]
		for _, sample := range windows {
			if sample.Start.Before(recordingSince) {
				continue
			}
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(CASE WHEN reason='priced' THEN 1 END),
COUNT(CASE WHEN reason NOT IN ('priced','pending') THEN 1 END),
TOTAL(CASE WHEN reason='priced' THEN CAST(amount_usd AS REAL) END)
FROM billing_ledger WHERE service_id=? AND account_key=? AND started_at>=? AND started_at<?`,
				key.service, key.account, billingTime(sample.Start), billingTime(sample.ObservedAt)).Scan(&sample.Priced, &sample.Unpriced, &sample.AmountUSD); err != nil {
				return result, err
			}
			read = append(read, sample)
		}
		total, ok := pricing.EstimateWindowTotal(read)
		if !ok {
			continue
		}
		estimate := estimates[key.service]
		if estimate == nil {
			estimate = &pricing.ServiceQuotaEstimate{ServiceID: contract.ServiceID(key.service)}
			estimates[key.service] = estimate
		}
		estimate.Windows = append(estimate.Windows, pricing.QuotaWindowEstimate{Kind: key.kind, TotalUSD: total})
	}
	for _, estimate := range estimates {
		result.Services = append(result.Services, *estimate)
	}
	pricing.SortQuotaEstimates(result.Services)
	return result, nil
}
