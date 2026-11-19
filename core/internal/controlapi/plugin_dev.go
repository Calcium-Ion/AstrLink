package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// dryRunTimeout bounds a hook the plugin page runs by hand.
const dryRunTimeout = 10 * time.Second

// pluginJournal answers GET /control/v1/plugins/{key}/journal?since=N: the
// plugin's log lines and failures after N, and each hook's timing.
func (handler *Handler) pluginJournal(writer http.ResponseWriter, request *http.Request, target *plugin.Plugin) {
	var since uint64
	if text := request.URL.Query().Get("since"); text != "" {
		parsed, err := strconv.ParseUint(text, 10, 64)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_request", "since must be a sequence number")
			return
		}
		since = parsed
	}
	entries, timings := target.Journal(since)
	journal := contract.PluginJournal{Entries: journalEntries(entries), Timings: make([]contract.PluginHookTiming, 0, len(timings)), Latest: since, Breakers: []contract.PluginBreaker{}}
	if handler.pluginHost != nil {
		for hook, until := range handler.pluginHost.Breakers(target.Key) {
			journal.Breakers = append(journal.Breakers, contract.PluginBreaker{Hook: hook, Until: until.UTC().Format(time.RFC3339)})
		}
		slices.SortFunc(journal.Breakers, func(a, b contract.PluginBreaker) int { return strings.Compare(a.Hook, b.Hook) })
	}
	if len(entries) > 0 {
		journal.Latest = entries[len(entries)-1].Seq
	}
	for _, timing := range timings {
		journal.Timings = append(journal.Timings, contract.PluginHookTiming{
			Hook: timing.Hook, Calls: timing.Calls, Failures: timing.Failures,
			P50Ms: milliseconds(timing.P50), P95Ms: milliseconds(timing.P95), MaxMs: milliseconds(timing.Max), ScriptMs: milliseconds(timing.Script),
		})
	}
	writeJSON(writer, http.StatusOK, journal)
}

func journalEntries(entries []plugin.JournalEntry) []contract.PluginJournalEntry {
	converted := make([]contract.PluginJournalEntry, 0, len(entries))
	for _, entry := range entries {
		converted = append(converted, contract.PluginJournalEntry{
			Seq: entry.Seq, Time: entry.Time.UTC().Format(time.RFC3339Nano), Hook: entry.Hook, Level: entry.Level,
			Message: entry.Message, Reason: entry.Reason, Stack: entry.Stack, DryRun: entry.DryRun,
		})
	}
	return converted
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000
}

// pluginDryRun answers POST /control/v1/plugins/{key}/dry-run: it runs one
// hook with the arguments given, as the user granted the plugin, without
// the network. Its lines go to the journal marked as a dry run.
func (handler *Handler) pluginDryRun(writer http.ResponseWriter, request *http.Request, target *plugin.Plugin) {
	body, err := io.ReadAll(io.LimitReader(request.Body, contract.MaxPluginDryRunBytes+1))
	if err != nil || len(body) > contract.MaxPluginDryRunBytes {
		writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large", "the dry run is too large")
		return
	}
	var run contract.PluginDryRunRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&run); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "the dry run must be JSON with hook and args")
		return
	}
	if !target.Loaded() {
		writeError(writer, http.StatusUnprocessableEntity, "plugin_not_loaded", "the plugin did not load")
		return
	}
	if !slices.Contains(target.Hooks(), run.Hook) {
		writeError(writer, http.StatusUnprocessableEntity, "unknown_hook", "the plugin does not export that hook")
		return
	}
	args := make([]any, 0, len(run.Args))
	for _, arg := range run.Args {
		args = append(args, arg)
	}
	ctx, cancel := context.WithTimeout(request.Context(), dryRunTimeout)
	defer cancel()
	result, info, callErr := target.CallArgs(ctx, run.Hook, args, plugin.CallOptions{DryRun: true})
	outcome := contract.PluginDryRunResult{Result: result, ElapsedMs: milliseconds(info.Elapsed), ScriptMs: milliseconds(info.Script), Logs: []contract.PluginJournalEntry{}}
	for _, line := range info.Logs {
		outcome.Logs = append(outcome.Logs, contract.PluginJournalEntry{Time: line.Time.UTC().Format(time.RFC3339Nano), Hook: run.Hook, Level: line.Level, Message: line.Message, DryRun: true})
	}
	var failure *plugin.CallError
	if errors.As(callErr, &failure) {
		outcome.Error = &contract.PluginCallFailure{Reason: failure.Reason, Message: failure.Message, Stack: target.Stack(failure)}
	}
	writeJSON(writer, http.StatusOK, outcome)
}
