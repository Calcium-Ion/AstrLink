package storage

import (
	"fmt"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

type UsageSummaryOptions struct {
	From, To time.Time
	TimeZone string
	Bucket   string
}

func (options UsageSummaryOptions) Validate() (*time.Location, error) {
	if options.From.IsZero() || options.To.IsZero() || !options.To.After(options.From) ||
		options.To.Sub(options.From) > 366*24*time.Hour || options.From.Nanosecond() != 0 || options.To.Nanosecond() != 0 {
		return nil, fmt.Errorf("%w: usage range must be whole seconds and span at most 366 days", ErrInvalidArgument)
	}
	if options.Bucket != "day" && options.Bucket != "hour" {
		return nil, fmt.Errorf("%w: bucket must be day or hour", ErrInvalidArgument)
	}
	if options.TimeZone == "" || options.TimeZone == "Local" {
		return nil, fmt.Errorf("%w: time_zone must be an IANA time zone", ErrInvalidArgument)
	}
	zone, err := time.LoadLocation(options.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid time_zone", ErrInvalidArgument)
	}
	return zone, nil
}

type UsageTotals struct {
	Requests         int64 `json:"requests"`
	FailedRequests   int64 `json:"failed_requests"`
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}

type UsageGroup struct {
	ID *string `json:"id"`
	UsageTotals
	Performance *ServicePerformance `json:"performance,omitempty"`
}

// ServicePerformance uses successful root inference requests with complete usage.
// Missing cache reporting or streaming timing is excluded, not counted as zero.
type ServicePerformance struct {
	CacheHitRate          *float64 `json:"cache_hit_rate"`
	OutputTokensPerSecond *float64 `json:"output_tokens_per_second"`
	CacheSamples          int64    `json:"cache_samples"`
	SpeedSamples          int64    `json:"speed_samples"`
}

// UsageStatusCounts counts root inference requests by outcome over the same
// window as Totals. Succeeded and Failed equal Totals.Requests and
// Totals.FailedRequests; the other outcomes never count as requests.
type UsageStatusCounts struct {
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
	Blocked   int64 `json:"blocked"`
	Cancelled int64 `json:"cancelled"`
	Pending   int64 `json:"pending"`
}

type UsageTimeBucket struct {
	Date string `json:"date"`
	Hour *int   `json:"hour,omitempty"`
	UsageTotals
}

// UsageDetailMaxSpan is the longest window that gets hour-by-model buckets
// and routes: a month of local days with room for a DST shift. Longer windows
// leave them empty, so a year of history never ships thousands of buckets.
const UsageDetailMaxSpan = 32 * 24 * time.Hour

// UsageHourModelBucket counts one model's successful calls in one local hour.
// Model is null when no model was recorded, matching the by_model null group.
type UsageHourModelBucket struct {
	Date        string  `json:"date"`
	Hour        int     `json:"hour"`
	Model       *string `json:"model"`
	Requests    int64   `json:"requests"`
	TotalTokens int64   `json:"total_tokens"`
}

// UsageRoute counts the calls that took one way through the gateway: from a
// client, with the same rewrites, to the provider that answered. Failed calls
// count too. ClientType is null for unknown and historical clients.
type UsageRoute struct {
	ClientType *string `json:"client_type"`
	// ServiceID is the provider of the call's last attempt; null when the call
	// never reached one.
	ServiceID *string `json:"service_id"`
	// FailedServiceID is the provider that failed before ServiceID took the
	// call over.
	FailedServiceID *string                   `json:"failed_service_id"`
	Failed          bool                      `json:"failed"`
	Rewrites        []contract.GatewayRewrite `json:"rewrites"`
	Requests        int64                     `json:"requests"`
}

// UsageSummary contains sparse calendar buckets; clients can pad empty periods.
type UsageSummary struct {
	Totals       UsageTotals       `json:"totals"`
	StatusCounts UsageStatusCounts `json:"status_counts"`
	ByDay        []UsageTimeBucket `json:"by_day"`
	ByHour       []UsageTimeBucket `json:"by_hour"`
	// ByHourModel holds every local hour with successful calls, split by model,
	// for windows up to UsageDetailMaxSpan regardless of Bucket.
	ByHourModel []UsageHourModelBucket `json:"by_hour_model"`
	ByService   []UsageGroup           `json:"by_service"`
	ByModel     []UsageGroup           `json:"by_model"`
	ByToken     []UsageGroup           `json:"by_token"`
	// ByClient groups by detected client; unknown and historical records
	// without a client share the null group.
	ByClient []UsageGroup `json:"by_client"`
	// ByRoute holds the routes of succeeded and failed calls for windows up to
	// UsageDetailMaxSpan, busiest first.
	ByRoute        []UsageRoute `json:"by_route"`
	ScannedRecords int64        `json:"scanned_records"`
}
