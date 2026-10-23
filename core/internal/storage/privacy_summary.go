package storage

// PrivacySummaryOptions uses the usage summary's window, time zone and bucket
// rules, so both summaries can cover the same calendar span.
type PrivacySummaryOptions = UsageSummaryOptions

// PrivacyTotals counts what the privacy policy did to finished root calls.
// Each call lands in at most one outcome column; a call no policy applied to
// is counted in Requests alone. Counts never contain placeholders or values.
type PrivacyTotals struct {
	// Requests is every finished root inference call, checked or not.
	Requests int64 `json:"requests"`
	// Allowed calls had nothing to protect. A match of a kind the user
	// turned off, or one of AstrLink's own identifiers, still counts as
	// allowed.
	Allowed int64 `json:"allowed"`
	// Ignored calls were allowed only because every match was allowlisted or
	// below the confidence threshold.
	Ignored  int64 `json:"ignored"`
	Warned   int64 `json:"warned"`
	Redacted int64 `json:"redacted"`
	Blocked  int64 `json:"blocked"`
	// FailOpen calls went upstream unchecked because the check failed.
	FailOpen int64 `json:"fail_open"`
	// Trusted calls went uninspected to a provider the user trusts.
	Trusted int64 `json:"trusted"`
	// Replaced sums the distinct values each call replaced before it went
	// upstream; a value repeated across calls counts once per call.
	Replaced              int64 `json:"replaced"`
	Restored              int64 `json:"restored"`
	RestoredVisible       int64 `json:"restored_visible"`
	RestoredToolArguments int64 `json:"restored_tool_arguments"`
	// Unrestored counts response parts left with replacements in them.
	Unrestored int64 `json:"unrestored"`
	// IgnoredAllowlisted and IgnoredLowConfidence count matches left in the
	// request for that reason, whatever the call's outcome.
	IgnoredAllowlisted   int64 `json:"ignored_allowlisted"`
	IgnoredLowConfidence int64 `json:"ignored_low_confidence"`
}

type PrivacyTimeBucket struct {
	Date string `json:"date"`
	Hour *int   `json:"hour,omitempty"`
	PrivacyTotals
}

// PrivacyKindCount is how often the policy found one kind: distinct replaced
// values for redacted calls, matches for the calls it warned on or blocked.
type PrivacyKindCount struct {
	Kind  string `json:"kind"`
	Count int64  `json:"count"`
}

// PrivacySummary contains sparse calendar buckets; clients pad empty periods.
// ByKind lists the most frequent kind first.
type PrivacySummary struct {
	Totals PrivacyTotals       `json:"totals"`
	ByDay  []PrivacyTimeBucket `json:"by_day"`
	ByHour []PrivacyTimeBucket `json:"by_hour"`
	ByKind []PrivacyKindCount  `json:"by_kind"`
}
