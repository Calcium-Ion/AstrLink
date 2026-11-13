package pluginstats

import (
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func ptr[T any](value T) *T { return &value }

func record(hook, status string, change func(*contract.RequestPluginRecord)) contract.RequestPluginRecord {
	entry := contract.RequestPluginRecord{Plugin: "memo", PluginVersion: "1.0.0", Hook: hook, Title: "Memo", Status: contract.PluginRecordStatus(status)}
	if change != nil {
		change(&entry)
	}
	return entry
}

func injected(bytes int) func(*contract.RequestPluginRecord) {
	return func(entry *contract.RequestPluginRecord) {
		entry.Injection = &contract.PluginInjection{Placement: contract.PluginPlacementSystem, InjectedBytes: bytes}
	}
}

func usage(input, read int) *contract.Usage {
	return &contract.Usage{InputTokens: input, OutputTokens: 10, TotalTokens: input + 10, CacheReadTokens: &read}
}

func TestAnAccountSumsEachDayAndTheWhole(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 21, 22, 0, 0, 0, zone)
	account := New("memo", []string{"project"}, zone, now, 3)
	if want := time.Date(2026, 10, 19, 0, 0, 0, 0, zone); !account.Since().Equal(want) {
		t.Fatalf("Since = %v, want %v", account.Since(), want)
	}
	at := func(day, hour int) time.Time { return time.Date(2026, 10, day, hour, 0, 0, 0, zone) }
	project := func(value string) *contract.RequestMetadata {
		return &contract.RequestMetadata{Values: map[string]contract.MetadataValue{"project": {Value: value, Source: "session"}}}
	}
	bound := func(value, setBy string, created bool) *contract.RequestMetadata {
		metadata := project(value)
		metadata.Events = []contract.MetadataEvent{{Key: "project", Event: contract.MetadataEventBound, Value: value, SetBy: setBy, New: created}}
		return metadata
	}
	activities := []storage.PluginActivity{
		// Before the first day: left out.
		{StartedAt: at(18, 23), SessionID: "s0", Records: []contract.RequestPluginRecord{record(plugin.HookPrepareSession, "ok", func(entry *contract.RequestPluginRecord) { entry.Trigger = "start" })}},
		// The 19th, 23:30 in New York, is the 20th in UTC: it counts on the 19th.
		{StartedAt: time.Date(2026, 10, 20, 3, 30, 0, 0, time.UTC), SessionID: "s1", Usage: usage(1000, 0), Metadata: bound("astrlink", contract.MetadataSetByWorkspace, false),
			Records: []contract.RequestPluginRecord{
				record(plugin.HookPrepareSession, "ok", func(entry *contract.RequestPluginRecord) {
					entry.Trigger, entry.ElapsedMs = "bound", 300
					injected(4000)(entry)
				}),
				record(plugin.HookObserveTurn, "ok", nil),
				// Another plugin's records count nothing here.
				{Plugin: "other", PluginVersion: "1", Hook: plugin.HookObserveTurn, Title: "Other", Status: "failed"},
			}},
		{StartedAt: at(20, 9), SessionID: "s1", Usage: usage(5000, 4000), Metadata: project("astrlink"),
			Records: []contract.RequestPluginRecord{
				record(plugin.HookPrepareSession, "ok", injected(4000)),
				record(plugin.HookCallTool, "ok", func(entry *contract.RequestPluginRecord) {
					entry.ElapsedMs, entry.Lookup = 800, &contract.PluginLookup{Tool: "search", InputRunes: 6, ResultBytes: 900, Hits: ptr(3)}
				}),
				record(plugin.HookCallTool, "ok", func(entry *contract.RequestPluginRecord) {
					entry.ElapsedMs, entry.Lookup = 200, &contract.PluginLookup{Tool: "search", InputRunes: 4, ResultBytes: 40, Hits: ptr(0)}
				}),
				record(plugin.HookCallTool, "skipped", func(entry *contract.RequestPluginRecord) {
					entry.SkippedReason, entry.Lookup = contract.PluginSkipTurnLimit, &contract.PluginLookup{Tool: "search"}
				}),
				// A report the plugin wrote counts with the writes, one
				// turned away nowhere; neither is a lookup.
				record(plugin.HookCallTool, "ok", func(entry *contract.RequestPluginRecord) {
					entry.ElapsedMs, entry.Lookup = 50, &contract.PluginLookup{Tool: "note", InputRunes: 8, Writes: true}
				}),
				record(plugin.HookCallTool, "skipped", func(entry *contract.RequestPluginRecord) {
					entry.SkippedReason, entry.Lookup = contract.PluginSkipTurnLimit, &contract.PluginLookup{Tool: "note", Writes: true}
				}),
				record(plugin.HookObserveTurn, "failed", func(entry *contract.RequestPluginRecord) { entry.SkippedReason = contract.PluginSkipPluginError }),
			}},
		// A session asked for its project that never answered.
		{StartedAt: at(20, 10), SessionID: "s2", Usage: usage(2000, 0), Metadata: &contract.RequestMetadata{},
			Records: []contract.RequestPluginRecord{
				record(plugin.HookPrepareSession, "skipped", func(entry *contract.RequestPluginRecord) { entry.SkippedReason = "no_project" }),
				record(plugin.HookPrepareTurn, "ok", func(entry *contract.RequestPluginRecord) {
					entry.Injection = &contract.PluginInjection{Placement: contract.PluginPlacementTurnMessage, InjectedBytes: 900}
				}),
			}},
		// A session asked that answered with a new project, one that picked
		// a listed one, one that declined; one bound by its workspace and
		// moved since.
		{StartedAt: at(21, 8), SessionID: "s3", Metadata: &contract.RequestMetadata{},
			Records: []contract.RequestPluginRecord{record(plugin.HookPrepareTurn, "ok", func(entry *contract.RequestPluginRecord) {
				entry.Injection = &contract.PluginInjection{Placement: contract.PluginPlacementTurnMessage, InjectedBytes: 900}
			})}},
		{StartedAt: at(21, 9), SessionID: "s3", Metadata: bound("brand-new", contract.MetadataSetByModelMarker, true),
			Records: []contract.RequestPluginRecord{record(plugin.HookPrepareSession, "skipped", func(entry *contract.RequestPluginRecord) {
				entry.Trigger, entry.ElapsedMs, entry.SkippedReason = "bound", 2500, contract.PluginSkipTimeout
			})}},
		{StartedAt: at(21, 10), SessionID: "s4", Usage: usage(3000, 2700), Metadata: bound("paseo", contract.MetadataSetByUserAnswer, false),
			Records: []contract.RequestPluginRecord{record(plugin.HookPrepareSession, "ok", func(entry *contract.RequestPluginRecord) {
				entry.Trigger, entry.ElapsedMs = "bound", 100
				injected(3000)(entry)
			})}},
		{StartedAt: at(21, 11), SessionID: "s5", Metadata: bound("none", contract.MetadataSetByUserMarker, false),
			Records: []contract.RequestPluginRecord{record(plugin.HookPrepareSession, "skipped", func(entry *contract.RequestPluginRecord) {
				entry.SkippedReason = contract.PluginSkipOverBudget
			})}},
		{StartedAt: at(21, 12), SessionID: "s6", Metadata: bound("datav", contract.MetadataSetByWorkspace, false),
			Records: []contract.RequestPluginRecord{record(plugin.HookPrepareSession, "failed", func(entry *contract.RequestPluginRecord) {
				entry.SkippedReason = contract.PluginSkipPrivacyBlocked
			})}},
		// A session the plugin never touched, and a request without a
		// session.
		{StartedAt: at(21, 13), SessionID: "s7", Usage: usage(4000, 1000)},
		{StartedAt: at(21, 14), Usage: usage(1000, 1000)},
		// A moment past the last day counts on it.
		{StartedAt: at(22, 1), SessionID: "s4", Records: []contract.RequestPluginRecord{record(plugin.HookObserveTurn, "ok", nil)}},
	}
	for _, activity := range activities {
		account.Add(activity)
	}
	current := map[string]storage.MetadataValueRecord{
		"s1": {Value: "astrlink", SetBy: contract.MetadataSetByWorkspace},
		"s3": {Value: "brand-new", SetBy: contract.MetadataSetByModelMarker},
		"s4": {Value: "paseo", SetBy: contract.MetadataSetByUserAnswer},
		"s6": {Value: "paseo", SetBy: contract.MetadataSetByManual},
	}
	stats := account.Result(func(session, key string) (storage.MetadataValueRecord, bool) {
		value, found := current[session]
		return value, found && key == "project"
	})
	if len(stats.Days) != 3 || stats.Days[0].Date != "2026-10-19" || stats.Days[2].Date != "2026-10-21" || stats.Total.Date != "" {
		t.Fatalf("days = %+v", stats.Days)
	}
	first, second, third := stats.Days[0], stats.Days[1], stats.Days[2]
	if first.Load.Count != 1 || first.Load.ByTrigger["bound"] != 1 || first.Load.InjectedBytes != 4000 || first.Load.Requests != 1 ||
		first.Write.OK != 1 || first.Binding.Workspace != 1 || first.Problems.Failed != 0 {
		t.Fatalf("19th = %+v", first)
	}
	if second.Lookup.Ran != 2 || second.Lookup.Refused != 1 || second.Lookup.Counted != 2 || second.Lookup.WithHits != 1 ||
		second.Lookup.Ms == nil || *second.Lookup.Ms != (contract.PluginPercentiles{P50: 200, P95: 800}) ||
		second.Lookup.Sessions != 2 || second.Lookup.LookingUp != 1 || second.Write.Failed != 1 || second.Problems.Failed != 1 ||
		second.Write.OK != 1 || second.Write.Reported != 1 ||
		second.Binding.Unanswered != 1 || second.Load.InjectedBytes != 4900 {
		t.Fatalf("20th = %+v %+v", second, second.Lookup.Ms)
	}
	if third.Binding != (contract.PluginBindingStats{Workspace: 1, Picked: 1, Created: 1, Declined: 1, Changed: 1}) ||
		third.Problems != (contract.PluginProblemStats{Failed: 1, Timeouts: 1, Privacy: 1, OverBudget: 1}) ||
		third.Load.Count != 1 || third.Load.WaitMs == nil || *third.Load.WaitMs != (contract.PluginPercentiles{P50: 100, P95: 2500}) ||
		third.Write.OK != 1 {
		t.Fatalf("21st = %+v", third)
	}
	// The cache of the sessions that carried the plugin's blocks, the ask
	// included, against the others'.
	if stats.Total.Cache.With != (contract.PluginCacheShare{Requests: 4, InputTokens: 11000, CacheReadTokens: 6700}) ||
		stats.Total.Cache.Without != (contract.PluginCacheShare{Requests: 2, InputTokens: 5000, CacheReadTokens: 2000}) {
		t.Fatalf("cache = %+v", stats.Total.Cache)
	}
	// The whole counts each session once.
	total := stats.Total
	if total.Load.Count != 2 || total.Lookup.Sessions != 4 || total.Lookup.LookingUp != 1 || total.Binding.Unanswered != 1 ||
		total.Binding.Changed != 1 || total.Load.WaitMs == nil || *total.Load.WaitMs != (contract.PluginPercentiles{P50: 300, P95: 2500}) {
		t.Fatalf("total = %+v", total)
	}
}

func TestPercentilesTakeTheNearestRank(t *testing.T) {
	if percentiles(nil) != nil {
		t.Fatal("no samples gave percentiles")
	}
	samples := make([]int, 0, 20)
	for value := 20; value >= 1; value-- {
		samples = append(samples, value*10)
	}
	if got := percentiles(samples); *got != (contract.PluginPercentiles{P50: 100, P95: 190}) {
		t.Fatalf("percentiles = %+v", got)
	}
	if got := percentiles([]int{7}); *got != (contract.PluginPercentiles{P50: 7, P95: 7}) {
		t.Fatalf("one sample = %+v", got)
	}
}
