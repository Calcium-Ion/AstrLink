package contract

// Bounds of the days a plugin's statistics cover.
const (
	DefaultPluginStatsDays = 7
	MaxPluginStatsDays     = 30
)

// PluginStats is a plugin's account over its last days, summed from this
// computer's request records: counts, sizes, durations and reasons, never
// what anyone wrote. Nothing of it leaves the computer.
type PluginStats struct {
	// Days are the calendar days in the gateway's time zone, the oldest
	// first, today last.
	Days []PluginDayStats `json:"days"`
	// Total covers every day; it counts each session once.
	Total PluginDayStats `json:"total"`
}

// PluginDayStats is a plugin's account of one day, or of all of them.
type PluginDayStats struct {
	// Date is the day as YYYY-MM-DD; the total has none.
	Date     string             `json:"date,omitempty"`
	Load     PluginLoadStats    `json:"load"`
	Lookup   PluginLookupStats  `json:"lookup"`
	Binding  PluginBindingStats `json:"binding"`
	Write    PluginWriteStats   `json:"write"`
	Problems PluginProblemStats `json:"problems"`
	Cache    PluginCacheStats   `json:"cache"`
}

// PluginLoadStats is what the plugin's blocks cost.
type PluginLoadStats struct {
	// Count is how many resident blocks were computed, ByTrigger why.
	Count     int            `json:"count"`
	ByTrigger map[string]int `json:"by_trigger"`
	// InjectedBytes is what the plugin added to requests, each request
	// counted; Requests is how many requests carried something of it.
	InjectedBytes int64 `json:"injected_bytes"`
	Requests      int   `json:"requests"`
	// WaitMs is how long computing a block took.
	WaitMs *PluginPercentiles `json:"wait_ms,omitempty"`
}

// PluginPercentiles are the median and the slowest 5% of some durations.
type PluginPercentiles struct {
	P50 int `json:"p50"`
	P95 int `json:"p95"`
}

// PluginLookupStats is how the model used the plugin's queries.
type PluginLookupStats struct {
	// Ran counts the lookups the plugin answered or failed, Refused those
	// the gateway turned away: limits, privacy, sessions without a value.
	Ran     int `json:"ran"`
	Refused int `json:"refused"`
	// Counted is how many lookups said how many hits they had, WithHits
	// how many of those had any.
	Counted  int `json:"counted"`
	WithHits int `json:"with_hits"`
	// Ms is how long the lookups that ran took.
	Ms *PluginPercentiles `json:"ms,omitempty"`
	// Sessions is how many sessions carried the plugin's blocks, LookingUp
	// how many sessions ran a lookup.
	Sessions  int `json:"sessions"`
	LookingUp int `json:"looking_up"`
}

// PluginBindingStats is how sessions got a value of the plugin's
// attributes, counted by event.
type PluginBindingStats struct {
	// Workspace: the value the session's working directory had.
	Workspace int `json:"workspace"`
	// Picked: a value the plugin offered or one known before.
	Picked int `json:"picked"`
	// Created: a value no session had.
	Created int `json:"created"`
	// Declined: none.
	Declined int `json:"declined"`
	// Unanswered counts the sessions asked that still have no value;
	// Changed the sessions bound by their workspace that hold another value
	// now.
	Unanswered int `json:"unanswered"`
	Changed    int `json:"changed"`
}

// PluginWriteStats counts what observeTurn and the model's reports wrote:
// OK and Failed both, Reported the reports among OK.
type PluginWriteStats struct {
	OK       int `json:"ok"`
	Failed   int `json:"failed"`
	Reported int `json:"reported"`
}

// PluginProblemStats counts the plugin's records that failed or stayed out
// for a reason worth watching.
type PluginProblemStats struct {
	Failed     int `json:"failed"`
	Timeouts   int `json:"timeouts"`
	Privacy    int `json:"privacy"`
	OverBudget int `json:"over_budget"`
}

// PluginCacheStats compares the prompt cache of the sessions that carried
// the plugin's blocks with the others'.
type PluginCacheStats struct {
	With    PluginCacheShare `json:"with"`
	Without PluginCacheShare `json:"without"`
}

// PluginCacheShare sums the input of the requests that reported their cache
// reads.
type PluginCacheShare struct {
	Requests        int   `json:"requests"`
	InputTokens     int64 `json:"input_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
}
