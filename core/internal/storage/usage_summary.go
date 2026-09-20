package storage

import (
	"fmt"
	"time"
)

type UsageSummaryOptions struct {
	From, To time.Time
	TimeZone string
	Bucket   string
}

func (options UsageSummaryOptions) Validate() (*time.Location, error) {
	if options.From.IsZero() || options.To.IsZero() || !options.To.After(options.From) ||
		options.To.Sub(options.From) > 32*24*time.Hour || options.From.Nanosecond() != 0 || options.To.Nanosecond() != 0 {
		return nil, fmt.Errorf("%w: usage range must be whole seconds and span at most 32 days", ErrInvalidArgument)
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
}

type UsageTimeBucket struct {
	Date string `json:"date"`
	Hour *int   `json:"hour,omitempty"`
	UsageTotals
}

// UsageSummary contains sparse calendar buckets; clients can pad empty periods.
type UsageSummary struct {
	Totals         UsageTotals       `json:"totals"`
	ByDay          []UsageTimeBucket `json:"by_day"`
	ByHour         []UsageTimeBucket `json:"by_hour"`
	ByService      []UsageGroup      `json:"by_service"`
	ByModel        []UsageGroup      `json:"by_model"`
	ScannedRecords int64             `json:"scanned_records"`
}
