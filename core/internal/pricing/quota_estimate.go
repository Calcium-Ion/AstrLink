package pricing

import (
	"cmp"
	"slices"
	"strconv"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// QuotaEstimateLookback is how far back an estimate reads an account's quota
// windows: the previous weekly window and the recent 5-hour ones. A plan
// change shows in the estimate within a week.
const QuotaEstimateLookback = 7 * 24 * time.Hour

// QuotaEstimateMinPercent is the least share of a window, summed over the
// windows read, that an estimate needs. Providers round the share they
// report, so below it an estimate would be mostly rounding.
const QuotaEstimateMinPercent = 10.0

// QuotaWindowEstimate values the whole of one official quota window of an
// account at official API prices.
type QuotaWindowEstimate struct {
	// Kind is the usage window the estimate is for: primary or secondary.
	Kind     string `json:"kind"`
	TotalUSD string `json:"total_usd"`
}

// ServiceQuotaEstimate lists the windows of one service that have an
// estimate; a service without any is left out.
type ServiceQuotaEstimate struct {
	ServiceID contract.ServiceID    `json:"service_id"`
	Windows   []QuotaWindowEstimate `json:"windows"`
}

type QuotaEstimates struct {
	Services []ServiceQuotaEstimate `json:"services"`
}

// QuotaSample is one observed quota window of an account: the share the
// provider last reported used, and the calls recorded in the window up to
// that report.
type QuotaSample struct {
	Start, End, ObservedAt time.Time
	UsedPercent            float64
	// AmountUSD sums the priced calls from Start to ObservedAt.
	AmountUSD float64
	// Priced and Unpriced count those calls; calls still running count in
	// neither.
	Priced, Unpriced int64
}

// DistinctQuotaWindows keeps one sample per window. A window read again with
// a slightly different reset time is stored twice, and a provider can report
// a window before it starts; overlapping samples are one window, seen last by
// the sample observed latest. The result runs from the latest window back.
func DistinctQuotaWindows(samples []QuotaSample) []QuotaSample {
	sorted := slices.Clone(samples)
	slices.SortFunc(sorted, func(a, b QuotaSample) int { return b.End.Compare(a.End) })
	kept := make([]QuotaSample, 0, len(sorted))
	for _, sample := range sorted {
		if last := len(kept) - 1; last >= 0 && sample.End.After(kept[last].Start) {
			if sample.ObservedAt.After(kept[last].ObservedAt) {
				kept[last] = sample
			}
			continue
		}
		kept = append(kept, sample)
	}
	return kept
}

// EstimateWindowTotal values a whole window from the windows read: what their
// calls cost divided by the share of the window they used. A window counts
// only when every call in it was priced and the provider reported a share; a
// window with no recorded call was used elsewhere and says nothing.
func EstimateWindowTotal(samples []QuotaSample) (string, bool) {
	var amount, used float64
	for _, sample := range samples {
		if sample.Priced == 0 || sample.Unpriced > 0 || sample.UsedPercent <= 0 {
			continue
		}
		amount += sample.AmountUSD
		used += sample.UsedPercent
	}
	if used < QuotaEstimateMinPercent || amount <= 0 {
		return "", false
	}
	return strconv.FormatFloat(amount*100/used, 'f', 2, 64), true
}

// SortQuotaEstimates orders services and their windows for a stable reply.
func SortQuotaEstimates(estimates []ServiceQuotaEstimate) {
	slices.SortFunc(estimates, func(a, b ServiceQuotaEstimate) int { return cmp.Compare(a.ServiceID, b.ServiceID) })
	for _, estimate := range estimates {
		slices.SortFunc(estimate.Windows, func(a, b QuotaWindowEstimate) int { return cmp.Compare(a.Kind, b.Kind) })
	}
}
