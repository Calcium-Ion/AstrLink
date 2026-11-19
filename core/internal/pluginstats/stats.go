// Package pluginstats sums a plugin's account from the request records:
// what its blocks cost, how the model used its queries, how sessions got
// their values, what it wrote, what went wrong, and how the prompt cache of
// the sessions that carried its blocks compares with the others'. It reads
// counts, sizes, durations and reasons, never what anyone wrote.
package pluginstats

import (
	"math"
	"slices"
	"sort"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// Account sums one plugin's activity over consecutive calendar days.
type Account struct {
	plugin     string
	attributes []string
	zone       *time.Location
	first      time.Time
	days       []*day
	total      *day
	// requests keep each request's day, session and usage until every
	// session that carried the plugin's blocks is known.
	requests []request
	injected map[string]bool
	// asked holds the day each session was first asked for a value while it
	// had none; answered the sessions that got one, none included.
	asked    map[string]int
	answered map[string]bool
	// automatic holds the first value each session took from its
	// workspace, with its day.
	automatic map[string]automaticBinding
}

type day struct {
	stats              contract.PluginDayStats
	waits, lookups     []int
	sessions, lookedUp map[string]bool
}

type request struct {
	day     int
	session string
	usage   *contract.Usage
}

type automaticBinding struct {
	day        int
	key, value string
}

// New starts the account of plugin, whose attributes are those keys, over
// days calendar days in zone, the last of them the one now falls on.
func New(plugin string, attributes []string, zone *time.Location, now time.Time, days int) *Account {
	local := now.In(zone)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
	account := &Account{
		plugin: plugin, attributes: attributes, zone: zone,
		first:    today.AddDate(0, 0, -(days - 1)),
		total:    newDay(""),
		injected: map[string]bool{}, asked: map[string]int{}, answered: map[string]bool{},
		automatic: map[string]automaticBinding{},
	}
	for index := range days {
		account.days = append(account.days, newDay(account.first.AddDate(0, 0, index).Format(time.DateOnly)))
	}
	return account
}

func newDay(date string) *day {
	return &day{
		stats:    contract.PluginDayStats{Date: date, Load: contract.PluginLoadStats{ByTrigger: map[string]int{}}},
		sessions: map[string]bool{}, lookedUp: map[string]bool{},
	}
}

// Since is when the first day starts: the records to read start there.
func (account *Account) Since() time.Time { return account.first }

// dayOf returns the index of the day at falls on, -1 before the first. A
// moment past the last day counts on the last.
func (account *Account) dayOf(at time.Time) int {
	if at.Before(account.first) {
		return -1
	}
	local := at.In(account.zone)
	date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, account.zone)
	index := 0
	for index < len(account.days)-1 && !account.first.AddDate(0, 0, index+1).After(date) {
		index++
	}
	return index
}

// Add counts one request record.
func (account *Account) Add(activity storage.PluginActivity) {
	index := account.dayOf(activity.StartedAt)
	if index < 0 {
		return
	}
	both := []*day{account.days[index], account.total}
	session := activity.SessionID
	carried := false
	for _, record := range activity.Records {
		if record.Plugin != account.plugin {
			continue
		}
		for _, counted := range both {
			counted.record(record, session)
		}
		if record.Injection != nil && record.Injection.InjectedBytes > 0 {
			carried = true
			if record.Hook == plugin.HookPrepareTurn && session != "" && !account.bound(activity.Metadata) {
				if _, found := account.asked[session]; !found {
					account.asked[session] = index
				}
			}
		}
	}
	if carried {
		for _, counted := range both {
			counted.stats.Load.Requests++
			if session != "" {
				counted.sessions[session] = true
			}
		}
		if session != "" {
			account.injected[session] = true
		}
	}
	account.bindings(both, index, session, activity.Metadata)
	account.requests = append(account.requests, request{day: index, session: session, usage: activity.Usage})
}

// bound reports whether a request had a value of each of the plugin's
// attributes.
func (account *Account) bound(metadata *contract.RequestMetadata) bool {
	if metadata == nil {
		return false
	}
	for _, key := range account.attributes {
		if _, found := metadata.Values[key]; !found {
			return false
		}
	}
	return true
}

func (counted *day) record(record contract.RequestPluginRecord, session string) {
	stats := &counted.stats
	switch record.Hook {
	case plugin.HookPrepareSession:
		// A record with a trigger is the one of the request that first
		// carried a block computed for it.
		if record.Trigger != "" {
			if record.Status == contract.PluginRecordOK {
				stats.Load.Count++
				stats.Load.ByTrigger[record.Trigger]++
			}
			counted.waits = append(counted.waits, record.ElapsedMs)
		}
	case plugin.HookCallTool:
		if record.Lookup == nil {
			break
		}
		if record.Lookup.Writes {
			// A report the plugin wrote counts with the writes; one the
			// gateway turned away is no write.
			switch record.Status {
			case contract.PluginRecordOK:
				stats.Write.OK++
				stats.Write.Reported++
			case contract.PluginRecordFailed:
				stats.Write.Failed++
			}
			break
		}
		if record.Status == contract.PluginRecordSkipped {
			stats.Lookup.Refused++
			break
		}
		stats.Lookup.Ran++
		counted.lookups = append(counted.lookups, record.ElapsedMs)
		if session != "" {
			counted.lookedUp[session] = true
		}
		if hits := record.Lookup.Hits; hits != nil {
			stats.Lookup.Counted++
			if *hits > 0 {
				stats.Lookup.WithHits++
			}
		}
	case plugin.HookObserveTurn:
		switch record.Status {
		case contract.PluginRecordOK:
			stats.Write.OK++
		case contract.PluginRecordFailed:
			stats.Write.Failed++
		}
	}
	if record.Injection != nil {
		stats.Load.InjectedBytes += int64(record.Injection.InjectedBytes)
	}
	if record.Status == contract.PluginRecordFailed {
		stats.Problems.Failed++
	}
	switch record.SkippedReason {
	case contract.PluginSkipTimeout:
		stats.Problems.Timeouts++
	case contract.PluginSkipPrivacyBlocked, contract.PluginSkipPrivacyUnavailable:
		stats.Problems.Privacy++
	case contract.PluginSkipOverBudget:
		stats.Problems.OverBudget++
	}
}

// bindings counts how the request bound its session to a value of the
// plugin's attributes.
func (account *Account) bindings(both []*day, index int, session string, metadata *contract.RequestMetadata) {
	if metadata == nil {
		return
	}
	for _, event := range metadata.Events {
		if !slices.Contains(account.attributes, event.Key) || (event.Event != contract.MetadataEventBound && event.Event != contract.MetadataEventRebound) {
			continue
		}
		if session != "" {
			account.answered[session] = true
		}
		for _, counted := range both {
			binding := &counted.stats.Binding
			switch {
			case event.Value == contract.MetadataNone:
				binding.Declined++
			case event.SetBy == contract.MetadataSetByWorkspace:
				binding.Workspace++
			case event.SetBy == contract.MetadataSetByUserAnswer:
				binding.Picked++
			case event.New:
				binding.Created++
			default:
				binding.Picked++
			}
		}
		if event.SetBy == contract.MetadataSetByWorkspace && event.Value != contract.MetadataNone && session != "" {
			if _, found := account.automatic[session]; !found {
				account.automatic[session] = automaticBinding{day: index, key: event.Key, value: event.Value}
			}
		}
	}
}

// Current returns a session's binding as stored now.
type Current func(session, key string) (storage.MetadataValueRecord, bool)

// Result finishes the account: the sessions asked that have no value now,
// those bound by their workspace that hold another value now, and the
// prompt cache of the sessions that carried the plugin's blocks against the
// others'.
func (account *Account) Result(current Current) contract.PluginStats {
	for session, index := range account.asked {
		if account.answered[session] || account.hasValue(current, session) {
			continue
		}
		account.days[index].stats.Binding.Unanswered++
		account.total.stats.Binding.Unanswered++
	}
	for session, bound := range account.automatic {
		now, found := current(session, bound.key)
		if found && now.Value != bound.value {
			account.days[bound.day].stats.Binding.Changed++
			account.total.stats.Binding.Changed++
		}
	}
	for _, request := range account.requests {
		usage := request.usage
		if usage == nil || usage.BillingIncomplete || usage.InputTokens <= 0 || usage.CacheReadTokens == nil || *usage.CacheReadTokens > usage.InputTokens {
			continue
		}
		for _, counted := range []*day{account.days[request.day], account.total} {
			share := &counted.stats.Cache.Without
			if account.injected[request.session] {
				share = &counted.stats.Cache.With
			}
			share.Requests++
			share.InputTokens += int64(usage.InputTokens)
			share.CacheReadTokens += int64(*usage.CacheReadTokens)
		}
	}
	result := contract.PluginStats{Days: make([]contract.PluginDayStats, 0, len(account.days))}
	for _, counted := range account.days {
		result.Days = append(result.Days, counted.finish())
	}
	result.Total = account.total.finish()
	return result
}

func (account *Account) hasValue(current Current, session string) bool {
	for _, key := range account.attributes {
		if _, found := current(session, key); found {
			return true
		}
	}
	return false
}

func (counted *day) finish() contract.PluginDayStats {
	stats := counted.stats
	stats.Load.WaitMs = percentiles(counted.waits)
	stats.Lookup.Ms = percentiles(counted.lookups)
	stats.Lookup.Sessions = len(counted.sessions)
	stats.Lookup.LookingUp = len(counted.lookedUp)
	return stats
}

// percentiles returns the median and the slowest 5% by nearest rank, nil
// for no samples.
func percentiles(samples []int) *contract.PluginPercentiles {
	if len(samples) == 0 {
		return nil
	}
	sorted := slices.Clone(samples)
	sort.Ints(sorted)
	rank := func(share float64) int {
		at := int(math.Ceil(share*float64(len(sorted)))) - 1
		return sorted[min(max(at, 0), len(sorted)-1)]
	}
	return &contract.PluginPercentiles{P50: rank(0.5), P95: rank(0.95)}
}
