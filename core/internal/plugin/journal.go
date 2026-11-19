package plugin

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// Bounds of a plugin's journal.
const (
	maxJournalEntries = 400
	// maxJournalSamples is how many recent calls of each hook its timing
	// is read from.
	maxJournalSamples = 256
)

// JournalEntry is one line of a plugin's journal: a log line one of its calls
// wrote, or a call that failed.
type JournalEntry struct {
	Seq     uint64
	Time    time.Time
	Hook    string
	Level   string
	Message string
	// Reason and Stack are set for a call that failed: the CallError reason
	// and the JavaScript stack, mapped to the plugin's own sources when it
	// came with a source map.
	Reason string
	Stack  string
	// DryRun marks the lines of a call the plugin page ran by hand.
	DryRun bool
}

// Levels of a journal entry: those of astrlink.log, and error for a failed
// call.
const (
	JournalError = "error"
)

// HookTiming is how long a hook's recent calls took, failed ones included.
type HookTiming struct {
	Hook     string
	Calls    int
	Failures int
	P50      time.Duration
	P95      time.Duration
	Max      time.Duration
	// Script is the median JavaScript time of the recent calls.
	Script time.Duration
}

// journal keeps what the development tab of the plugin page shows: the log
// lines and failures of the plugin's recent calls, and how long each hook
// took. It lives in memory, across the plugin's generations, until Core
// stops.
type journal struct {
	mutex   sync.Mutex
	entries []JournalEntry
	seq     uint64
	hooks   map[string]*hookSamples
}

type hookSamples struct {
	calls, failures int
	elapsed         []time.Duration
	script          []time.Duration
}

func (book *journal) note(hook string, info CallInfo, failure *CallError, stack string, dryRun bool) {
	book.mutex.Lock()
	defer book.mutex.Unlock()
	for _, line := range info.Logs {
		book.append(JournalEntry{Time: line.Time, Hook: hook, Level: line.Level, Message: line.Message, DryRun: dryRun})
	}
	if info.DroppedLogs > 0 {
		book.append(JournalEntry{Time: time.Now(), Hook: hook, Level: "warn", Message: fmt.Sprintf("%d more log lines of the call were dropped", info.DroppedLogs), DryRun: dryRun})
	}
	if failure != nil {
		book.append(JournalEntry{Time: time.Now(), Hook: hook, Level: JournalError, Message: failure.Message, Reason: failure.Reason, Stack: stack, DryRun: dryRun})
	}
	if dryRun {
		return
	}
	if book.hooks == nil {
		book.hooks = make(map[string]*hookSamples)
	}
	samples := book.hooks[hook]
	if samples == nil {
		samples = &hookSamples{}
		book.hooks[hook] = samples
	}
	samples.calls++
	if failure != nil {
		samples.failures++
	}
	samples.elapsed = appendSample(samples.elapsed, info.Elapsed)
	samples.script = appendSample(samples.script, info.Script)
}

func appendSample(samples []time.Duration, sample time.Duration) []time.Duration {
	if len(samples) == maxJournalSamples {
		copy(samples, samples[1:])
		samples = samples[:len(samples)-1]
	}
	return append(samples, sample)
}

// append adds an entry. book.mutex is held.
func (book *journal) append(entry JournalEntry) {
	book.seq++
	entry.Seq = book.seq
	if len(book.entries) == maxJournalEntries {
		copy(book.entries, book.entries[1:])
		book.entries = book.entries[:len(book.entries)-1]
	}
	book.entries = append(book.entries, entry)
}

// read returns the entries after since, oldest first, and each hook's
// timing in name order.
func (book *journal) read(since uint64) ([]JournalEntry, []HookTiming) {
	book.mutex.Lock()
	defer book.mutex.Unlock()
	var entries []JournalEntry
	for _, entry := range book.entries {
		if entry.Seq > since {
			entries = append(entries, entry)
		}
	}
	timings := make([]HookTiming, 0, len(book.hooks))
	for hook, samples := range book.hooks {
		elapsed := slices.Clone(samples.elapsed)
		slices.Sort(elapsed)
		script := slices.Clone(samples.script)
		slices.Sort(script)
		timings = append(timings, HookTiming{
			Hook: hook, Calls: samples.calls, Failures: samples.failures,
			P50: quantile(elapsed, 0.5), P95: quantile(elapsed, 0.95), Max: quantile(elapsed, 1),
			Script: quantile(script, 0.5),
		})
	}
	slices.SortFunc(timings, func(a, b HookTiming) int {
		switch {
		case a.Hook < b.Hook:
			return -1
		case a.Hook > b.Hook:
			return 1
		}
		return 0
	})
	return entries, timings
}

// quantile reads the q quantile of sorted samples, nearest rank.
func quantile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(q*float64(len(sorted))+0.5) - 1
	return sorted[min(max(index, 0), len(sorted)-1)]
}

// note keeps a call in the plugin's journal.
func (plugin *Plugin) note(hook string, info CallInfo, failure *CallError, dryRun bool) {
	if plugin.counter == nil {
		return
	}
	plugin.counter.journal.note(hook, info, failure, plugin.Stack(failure), dryRun)
}

// Stack returns the JavaScript stack of a failed call, mapped to the
// plugin's own sources, "" when it has none.
func (plugin *Plugin) Stack(failure *CallError) string {
	var thrown *thrown
	if failure == nil || !errors.As(failure.Err, &thrown) {
		return ""
	}
	return plugin.program.mapStack(withoutPrelude(thrown.stack))
}

// Journal returns the plugin's journal entries after since, oldest first,
// and each hook's timing.
func (plugin *Plugin) Journal(since uint64) ([]JournalEntry, []HookTiming) {
	if plugin.counter == nil {
		return nil, nil
	}
	return plugin.counter.journal.read(since)
}
