package pluginhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/injection"
	"github.com/QuantumNous/astrlink/core/internal/metadata"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/plugin/view"
)

// What became of a turn's capture. A capture awaiting the reply is queued
// once the reply binds the session, else held until the session is bound,
// or skipped as unbound when it cannot be held.
const (
	observeQueued             = "queued"
	observeAwaiting           = "awaiting_reply"
	observeHeld               = "held_unbound"
	observeSkippedUnbound     = "skipped_unbound"
	observeSkippedPrivacy     = "skipped_privacy"
	observeSkippedBindingTurn = "skipped_binding_turn"
	observeSkippedContinue    = "skipped_continuation"
	observeQueueFull          = "queue_full"
)

// Host reasons a plugin's block did not go in, beside the contract's.
const (
	skipInvalidResult = "invalid_result"
	skipNoSystem      = "no_system_channel"
	skipNoOpening     = "no_opening_message"
	skipInjectFailed  = "injection_failed"
)

// maxBlockItems bounds the items of one block a plugin returns, and
// maxChoices the values it offers for one attribute.
const (
	maxBlockItems = 256
	maxChoices    = 50
)

// skipReason is a reason a plugin gives for skipping a turn.
var skipReason = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// hookResult is what prepareSession or prepareTurn returned.
type hookResult struct {
	system  injection.Block
	context injection.Block
	skip    string
	// choices are the values a turn block offers for each attribute, for
	// the user's answer to name one.
	choices map[string][]string
	record  contract.PluginRecord
	// hasRecord is set when the plugin described its part itself.
	hasRecord bool
	// invalid is set when the result was not a result: the call counts as
	// failed.
	invalid bool
	// dropped counts the items of a resident block past the budget.
	dropped int
}

// parseResult reads a prepare hook's result. Undefined is an empty result.
// Choices count only for the plugin's own attributes, and only the values
// those accept.
func parseResult(raw json.RawMessage, owner *plugin.Plugin, registry *metadata.Registry) hookResult {
	var result hookResult
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return result
	}
	var fields struct {
		System  json.RawMessage     `json:"system"`
		Context json.RawMessage     `json:"context"`
		Skip    *string             `json:"skip"`
		Choices map[string][]string `json:"choices"`
		Record  json.RawMessage     `json:"record"`
	}
	if json.Unmarshal(trimmed, &fields) != nil {
		result.invalid = true
		return result
	}
	var ok bool
	if result.system, ok = parseBlock(fields.System); !ok {
		result.invalid = true
	}
	if result.context, ok = parseBlock(fields.Context); !ok {
		result.invalid = true
	}
	if fields.Skip != nil {
		if !skipReason.MatchString(*fields.Skip) {
			result.invalid = true
		}
		result.skip = *fields.Skip
	}
	if len(fields.Record) > 0 && !bytes.Equal(fields.Record, []byte("null")) {
		result.record, result.hasRecord = view.ParseRecord(fields.Record)
		if result.hasRecord && !owner.Can(plugin.CapabilityViews) {
			result.record.View = nil
		}
	}
	for key, values := range fields.Choices {
		owned := slices.ContainsFunc(owner.Meta.Metadata, func(attribute plugin.MetadataAttribute) bool { return attribute.Key == key })
		if !owned || registry == nil {
			continue
		}
		var kept []string
		for _, value := range values {
			if len(kept) < maxChoices && value != contract.MetadataNone && registry.Valid(key, value) && !slices.Contains(kept, value) {
				kept = append(kept, value)
			}
		}
		if len(kept) > 0 {
			if result.choices == nil {
				result.choices = map[string][]string{}
			}
			result.choices[key] = kept
		}
	}
	return result
}

// parseRecordOf reads a record a plugin returned, without its view unless
// the plugin may show views.
func parseRecordOf(raw json.RawMessage, owner *plugin.Plugin) (contract.PluginRecord, bool) {
	record, ok := view.ParseRecord(raw)
	if ok && !owner.Can(plugin.CapabilityViews) {
		record.View = nil
	}
	return record, ok
}

// parseBlock reads a block: a string, or an object of an opening line,
// items and a closing line. Absent is the empty block.
func parseBlock(raw json.RawMessage) (injection.Block, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return injection.Block{}, true
	}
	var text string
	if json.Unmarshal(trimmed, &text) == nil {
		return injection.Block{Items: []string{text}}, true
	}
	var block struct {
		Open  *string   `json:"open"`
		Items *[]string `json:"items"`
		Close *string   `json:"close"`
	}
	if json.Unmarshal(trimmed, &block) != nil || block.Items == nil || len(*block.Items) > maxBlockItems {
		return injection.Block{}, false
	}
	parsed := injection.Block{Items: *block.Items}
	if block.Open != nil {
		parsed.Open = *block.Open
	}
	if block.Close != nil {
		parsed.Close = *block.Close
	}
	return parsed, true
}

// injected is what the injection did for one plugin.
type injected struct {
	outcome injection.Outcome
	// dropped counts the turn block's items past the block's budget.
	dropped int
	// record is how the session's resident block came about, when the
	// session has one; fresh is set on the first request that carries it.
	record      residentRecord
	kept, fresh bool
	// overBudget is set when the plugin's blocks stayed out because the
	// plugins before it used up the injection budget.
	overBudget bool
	// turnTitle is the title the plugin gave the record that settled the
	// turn block.
	turnTitle string
	// commands name the queries whose commands the turn block carried.
	commands []string
}

// residentTitle is the title the plugin gave the record of the resident
// block, "" for none.
func (result injected) residentTitle() string {
	if result.kept && result.record.hasGiven {
		return result.record.given.Title
	}
	return ""
}

// report builds the plugins' records and events from what the injection
// did; failed marks an injection that was given up. The caller holds
// run.mutex.
func (run *Run) report(results []injected, failed bool) {
	run.reported = true
	ended := time.Now().UTC()
	for index, current := range run.parts {
		var records []contract.RequestPluginRecord
		if current.off != "" {
			if current.settle {
				record := run.newRecord(current, plugin.HookPrepareSession, "")
				record.Status, record.SkippedReason = contract.PluginRecordSkipped, current.off
				records = append(records, record)
			}
		} else {
			if record, ok := run.sessionRecord(current, results[index], failed); ok {
				records = append(records, record)
			}
			if record, ok := run.turnRecord(current, results[index], failed); ok {
				records = append(records, record)
			}
		}
		if len(records) == 0 || len(run.records)+len(records) > contract.MaxRequestPluginRecords {
			continue
		}
		run.records = append(run.records, records...)
		main := records[len(records)-1]
		status := contract.RequestStatusSucceeded
		if main.Status == contract.PluginRecordFailed {
			status = contract.RequestStatusFailed
		}
		// The event goes by the plugin's name, the same in every request;
		// what it did this time is the summary and its records.
		run.events = append(run.events, contract.RequestEvent{
			Kind: contract.RequestEventPlugin, Plugin: current.plugin.Key, Title: current.plugin.Meta.Name,
			StartedAt: run.began.UTC(), EndedAt: &ended, Status: status,
			Summary: contract.ClampRunes(main.Summary, contract.MaxEventSummaryRunes),
		})
	}
}

// sessionRecord describes the plugin's resident block in the request: the
// call it waited for, or the block a call computed after an earlier request
// stopped waiting, on the first request that carries it.
func (run *Run) sessionRecord(current *part, result injected, failed bool) (contract.RequestPluginRecord, bool) {
	outcome := result.outcome
	waited := current.sessionCall != nil && !current.background
	if !waited && !result.fresh && outcome.ResidentBytes == 0 && !outcome.NoSystem && !result.overBudget {
		return contract.RequestPluginRecord{}, false
	}
	record := run.newRecord(current, plugin.HookPrepareSession, result.residentTitle())
	dropped := 0
	switch {
	case waited:
		record.Trigger = current.trigger
		run.describeCall(&record, current.sessionCall)
		if called, ok := current.sessionCall.finished(); ok {
			dropped = called.dropped
		}
	case result.fresh:
		record.Trigger = result.record.trigger
		describeKept(&record, result.record)
		dropped = result.record.dropped
	}
	if result.kept && result.record.hasGiven && record.Actions == nil {
		record.Actions = result.record.given.Actions
	}
	if outcome.ResidentBytes > 0 {
		// The usage of the plugin's lookups rides in the same block, after
		// what the plugin gave, or alone when it gave nothing.
		record.Injection = &contract.PluginInjection{
			Placement: contract.PluginPlacementSystem, InjectedBytes: outcome.ResidentBytes, DroppedItems: dropped,
			UsageBytes: min(len(current.usage), outcome.ResidentBytes),
		}
	}
	if record.SkippedReason == "" {
		switch {
		case result.overBudget:
			record.Status, record.SkippedReason = contract.PluginRecordSkipped, contract.PluginSkipOverBudget
		case outcome.NoSystem:
			record.SkippedReason = skipNoSystem
		case failed:
			record.SkippedReason = skipInjectFailed
		}
	}
	return record, true
}

// describeKept fills record from how a kept block came about.
func describeKept(record *contract.RequestPluginRecord, kept residentRecord) {
	record.ElapsedMs, record.Network = kept.info.elapsedMs, kept.info.network
	if kept.hasGiven {
		record.Title, record.Status, record.Summary = kept.given.Title, kept.given.Status, kept.given.Summary
		record.View, record.Truncated, record.Actions = kept.given.View, kept.given.Truncated, kept.given.Actions
	}
}

// turnRecord describes the plugin's turn block in the request.
func (run *Run) turnRecord(current *part, result injected, failed bool) (contract.RequestPluginRecord, bool) {
	outcome := result.outcome
	called := current.turnCall != nil || current.skip != ""
	waiting := !called && current.settle && !run.bindsOnly && run.prepares(current) && !run.checked()
	if !called && !waiting && outcome.TurnBytes == 0 && outcome.Replayed == 0 && !outcome.NoOpening {
		return contract.RequestPluginRecord{}, false
	}
	record := run.newRecord(current, plugin.HookPrepareTurn, result.turnTitle)
	switch {
	case current.skip != "":
		record.Status, record.SkippedReason = contract.PluginRecordSkipped, current.skip
	case current.turnCall != nil:
		run.describeCall(&record, current.turnCall)
	case waiting:
		// The privacy check outlasted the budget: no prepareTurn will run
		// for this request.
		record.Status, record.SkippedReason = contract.PluginRecordSkipped, contract.PluginSkipTimeout
	}
	if outcome.TurnBytes > 0 || outcome.Replayed > 0 {
		record.Injection = &contract.PluginInjection{
			Placement: contract.PluginPlacementTurnMessage, InjectedBytes: outcome.TurnBytes,
			ReplayedBlocks: outcome.Replayed, DroppedItems: result.dropped,
		}
	}
	if outcome.TurnBytes > 0 {
		record.Commands = result.commands
	}
	if record.SkippedReason == "" {
		switch {
		case result.overBudget && current.turnCall != nil:
			record.Status, record.SkippedReason = contract.PluginRecordSkipped, contract.PluginSkipOverBudget
		case outcome.NoOpening:
			record.SkippedReason = skipNoOpening
		case failed && current.turnCall != nil:
			record.SkippedReason = skipInjectFailed
		}
	}
	switch {
	case current.settle && current.observe != "pending":
		record.Observe = current.observe
	case !current.settle && current.plugin.Has(plugin.HookObserveTurn):
		record.Observe = observeSkippedContinue
	}
	return record, true
}

// prepares reports whether the plugin's prepareTurn runs on a settling call.
func (run *Run) prepares(current *part) bool {
	return current.plugin.Can(plugin.CapabilityInject) && current.plugin.Has(plugin.HookPrepareTurn)
}

// checked reports whether the turn's text has passed the privacy check.
func (run *Run) checked() bool {
	select {
	case <-run.ready:
		return true
	default:
		return false
	}
}

// newRecord is a record of a hook the host ran or stood in for. The title is
// the plugin's own from earlier, or its name.
func (run *Run) newRecord(current *part, hook, title string) contract.RequestPluginRecord {
	if title == "" {
		title = current.plugin.Meta.Name
	}
	return contract.RequestPluginRecord{
		Plugin: current.plugin.Key, PluginVersion: current.plugin.Meta.Version, Hook: hook,
		Title: contract.ClampRunes(title, contract.MaxPluginRecordTitleRunes), Status: contract.PluginRecordOK,
	}
}

// describeCall fills record from what a hook call did: the plugin's own
// record when it gave one, the host's account otherwise.
func (run *Run) describeCall(record *contract.RequestPluginRecord, call *hookCall) {
	select {
	case <-call.done:
	default:
		record.Status, record.SkippedReason = contract.PluginRecordSkipped, contract.PluginSkipTimeout
		return
	}
	record.ElapsedMs = int(call.info.Elapsed.Milliseconds())
	record.Network = networkOf(call.info)
	if call.err != nil {
		record.Status, record.SkippedReason = contract.PluginRecordFailed, contract.PluginSkipPluginError
		var failure *plugin.CallError
		if errors.As(call.err, &failure) && (failure.Reason == plugin.ReasonTimeout || failure.Reason == plugin.ReasonCanceled) {
			record.SkippedReason = contract.PluginSkipTimeout
		}
		return
	}
	result := call.result
	if result.hasRecord {
		record.Title, record.Status, record.Summary = result.record.Title, result.record.Status, result.record.Summary
		record.View, record.Truncated, record.Actions = result.record.View, result.record.Truncated, result.record.Actions
	}
	switch {
	case result.invalid:
		record.Status, record.SkippedReason = contract.PluginRecordFailed, skipInvalidResult
	case result.skip != "":
		record.SkippedReason = result.skip
		if !result.hasRecord {
			record.Status = contract.PluginRecordSkipped
		}
	}
}
