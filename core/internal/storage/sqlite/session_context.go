package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
)

// cacheMissFloorTokens keeps small calls out of cache-miss detection: the
// previous main call must have cached at least this much, and this call must
// send at least this much. Rewriting a prefix below it costs a few cents at
// most, and short prompts sit near the providers' minimum cacheable length
// (1,024 to 4,096 tokens), where a miss is routine rather than a fault.
const cacheMissFloorTokens = 8192

// Prompt caches live five minutes after their last use, or an hour for
// prefixes written with the one-hour lifetime.
const (
	shortCacheTTL = 5 * time.Minute
	longCacheTTL  = time.Hour
)

// contextCall is one root call as the context fold reads it, in start order.
type contextCall struct {
	id       contract.RequestID
	started  time.Time
	protocol contract.ProtocolID
	model    string
	// usage is false for calls that reported none; the fold skips them.
	usage        bool
	input        int
	cacheRead    *int
	cacheWrite   *int
	cacheWrite1h int
	// observed is set when the record kept what the call carried; helper,
	// summary, compacted and repeats come from it.
	observed  bool
	helper    bool
	summary   bool
	compacted string
	repeats   *contract.FailedRepeats

	// Set for a session's detail only.
	ended   *time.Time
	service string
	account string
	price   string
	// usageValue is the call's whole usage, for pricing a cache miss.
	usageValue *contract.Usage
	// carried is what the call carried, to tell a changed prompt prefix.
	carried *contract.RequestContext
}

// newContextCall reads a root call off the session runtime query.
func newContextCall(
	id string,
	started time.Time,
	protocol string,
	model sql.NullString,
	usage bool,
	input, cacheRead, cacheWrite, cacheWrite1h sql.NullInt64,
) contextCall {
	call := contextCall{
		id:           contract.RequestID(id),
		started:      started,
		protocol:     contract.ProtocolID(protocol),
		model:        model.String,
		usage:        usage,
		input:        int(max(0, input.Int64)),
		cacheWrite1h: int(max(0, cacheWrite1h.Int64)),
	}
	if cacheRead.Valid {
		value := int(max(0, cacheRead.Int64))
		call.cacheRead = &value
	}
	if cacheWrite.Valid {
		value := int(max(0, cacheWrite.Int64))
		call.cacheWrite = &value
	}
	return call
}

// cacheReported tells whether the provider said anything about its cache.
func (call contextCall) cacheReported() bool {
	return call.cacheRead != nil || call.cacheWrite != nil
}

// readMarks reads the marks the fold needs off a root call's context, as
// the runtime query extracts them: [helper, summary, compacted,
// failed_repeats].
func (call *contextCall) readMarks(raw sql.NullString) {
	var marks [4]json.RawMessage
	if !raw.Valid || json.Unmarshal([]byte(raw.String), &marks) != nil {
		return
	}
	call.observed = true
	call.helper = string(marks[0]) == "true"
	call.summary = string(marks[1]) == "true"
	if json.Unmarshal(marks[2], &call.compacted) != nil {
		call.compacted = ""
	}
	var repeats *contract.FailedRepeats
	if json.Unmarshal(marks[3], &repeats) == nil && repeats != nil && repeats.Validate() == nil {
		call.repeats = repeats
	}
}

// summaryCall reports a call that asks the model for a compaction summary:
// one the client marked so, or one Codex sent to its own compact endpoint.
func (call contextCall) summaryCall() bool {
	return call.summary || call.protocol == contract.ProtocolOpenAIResponsesCompact
}

// markedCompaction reports a call whose history starts from another
// compaction summary than the previous main call's did. Before records kept
// what calls carried there is nothing to compare.
func markedCompaction(call, before contextCall) bool {
	return call.observed && before.observed && call.compacted != "" && call.compacted != before.compacted
}

// contextFold is what the fold learns from a conversation's root calls.
type contextFold struct {
	// main are the indices of the main calls in calls, in order.
	main []int
	// compactions holds, for each compaction, the position in main of the
	// first call after it.
	compactions     []contract.ContextCompaction
	compactionStart map[int]bool
	contextTokens   *int
	pointTokens     *int
	repeats         *contract.FailedRepeats
}

// sessionContextCalls are a conversation's root calls in start order and
// what the fold made of them.
type sessionContextCalls struct {
	calls []contextCall
	fold  contextFold
}

// foldContext separates main calls from helper calls and finds where the
// context was compacted. A call is a helper when the session placed it as
// one, when its context is under half of the previous main call's and the
// conversation then returns to that level, or when it asked another model: a
// title or a quick check, not the conversation itself. A main call is a
// compaction when its history starts from a new compaction summary, or,
// for clients that mark none, when its context dropped under half of the
// previous one. A drop on the newest call counts at once and is read again
// once more calls arrive.
func foldContext(calls []contextCall) contextFold {
	fold := contextFold{compactionStart: map[int]bool{}}
	previous := -1
	var summary *contract.RequestID
	for index, call := range calls {
		if !call.usage || call.input <= 0 || call.helper {
			continue
		}
		if previous >= 0 {
			before := calls[previous]
			marked := markedCompaction(call, before)
			if marked || (call.input*2 < before.input && !call.summaryCall()) {
				if !marked && helperCall(calls, index, before) {
					continue
				}
				compaction := contract.ContextCompaction{
					RequestID:    call.id,
					BeforeTokens: before.input,
					AfterTokens:  call.input,
					Marked:       marked,
				}
				if summary != nil {
					value := *summary
					compaction.SummaryRequestID = &value
				}
				fold.compactions = append(fold.compactions, compaction)
				fold.compactionStart[len(fold.main)] = true
				summary = nil
			}
		}
		if call.summaryCall() {
			value := call.id
			summary = &value
		}
		fold.main = append(fold.main, index)
		previous = index
	}
	if previous >= 0 {
		tokens := calls[previous].input
		fold.contextTokens = &tokens
		fold.repeats = calls[previous].repeats
	}
	if count := len(fold.compactions); count > 0 {
		point := fold.compactions[count-1].BeforeTokens
		if count > 1 {
			point = max(point, fold.compactions[count-2].BeforeTokens)
		}
		fold.pointTokens = &point
	}
	return fold
}

// helperCall tells a helper call from a compaction once the context fell
// under half of before: a helper asked another model, or the next call with
// usage climbs back over that half.
func helperCall(calls []contextCall, index int, before contextCall) bool {
	call := calls[index]
	if call.model != "" && before.model != "" && call.model != before.model {
		return true
	}
	for _, next := range calls[index+1:] {
		if !next.usage || next.input <= 0 || next.helper {
			continue
		}
		return next.input*2 >= before.input
	}
	return false
}

// applyContextFold copies a conversation's fold onto its summary.
func applyContextFold(session *contract.RequestSession, fold contextFold) {
	session.ContextTokens = fold.contextTokens
	session.CompactionPointTokens = fold.pointTokens
	session.CompactionCount = len(fold.compactions)
	session.FailedRepeats = fold.repeats
}

// sessionUsageTotals sums token usage over every call of a conversation.
type sessionUsageTotals struct {
	seen                    bool
	usage                   contract.SessionUsage
	cachedInput, cachedRead int64
	cacheSeen               bool
}

func (totals *sessionUsageTotals) observe(input, output int64, cacheRead, cacheWrite sql.NullInt64) {
	totals.seen = true
	totals.usage.InputTokens += max(0, input)
	totals.usage.OutputTokens += max(0, output)
	totals.usage.CacheReadTokens += max(0, cacheRead.Int64)
	totals.usage.CacheWriteTokens += max(0, cacheWrite.Int64)
	if cacheRead.Valid || cacheWrite.Valid {
		totals.cacheSeen = true
		totals.cachedInput += max(0, input)
		totals.cachedRead += max(0, min(cacheRead.Int64, input))
	}
}

func (totals *sessionUsageTotals) apply(session *contract.RequestSession) {
	if !totals.seen {
		session.Usage = nil
		return
	}
	usage := totals.usage
	if totals.cacheSeen && totals.cachedInput > 0 {
		ratio := float64(totals.cachedRead) / float64(totals.cachedInput)
		usage.CacheHitRatio = &ratio
	}
	session.Usage = &usage
}

// modelWindowCache maps official models to their context windows, read from
// the newest pricing catalog and kept until a newer one is activated.
type modelWindowCache struct {
	mu      sync.Mutex
	version string
	windows map[string]int
}

func modelWindowKey(provider, model string) string { return provider + "/" + model }

// windowsFor returns the context windows of the newest catalog.
func (cache *modelWindowCache) windowsFor(ctx context.Context, reader historyReader) (map[string]int, error) {
	if cache == nil {
		return nil, nil
	}
	rows, err := reader.QueryContext(ctx, `SELECT version FROM pricing_versions ORDER BY activated_at DESC LIMIT 1`)
	if err != nil {
		return nil, fmt.Errorf("read pricing catalog version: %w", err)
	}
	version := ""
	for rows.Next() {
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return nil, err
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if version == cache.version && cache.windows != nil {
		return cache.windows, nil
	}
	windows := map[string]int{}
	if version != "" {
		rows, err := reader.QueryContext(ctx, `SELECT document_json FROM pricing_versions WHERE version = ?`, version)
		if err != nil {
			return nil, fmt.Errorf("read pricing catalog: %w", err)
		}
		var raw string
		for rows.Next() {
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		var catalog pricing.Catalog
		if raw != "" && json.Unmarshal([]byte(raw), &catalog) == nil {
			for _, model := range catalog.Models {
				if model.ContextTokens > 0 {
					windows[modelWindowKey(model.Provider, model.Model)] = int(min(model.ContextTokens, int64(1<<31-1)))
				}
			}
		}
	}
	cache.version, cache.windows = version, windows
	return windows, nil
}

// contextWindowRoots is how many of a conversation's newest main calls
// loadContextWindows asks the ledger about.
const contextWindowRoots = 2

// loadContextWindows sets each session's context window from the official
// model its newest priced main call reached. Helper calls are left out: they
// often ask a smaller model with a smaller window.
func loadContextWindows(
	ctx context.Context,
	reader historyReader,
	cache *modelWindowCache,
	sessions []contract.RequestSession,
	contexts map[string]*sessionContextCalls,
) error {
	roots := make(map[string][]contract.RequestID, len(sessions))
	args := make([]any, 0, contextWindowRoots*len(sessions))
	for _, session := range sessions {
		entry := contexts[string(session.ID)]
		if entry == nil {
			continue
		}
		ids := make([]contract.RequestID, 0, contextWindowRoots)
		for position := len(entry.fold.main) - 1; position >= 0 && len(ids) < contextWindowRoots; position-- {
			id := entry.calls[entry.fold.main[position]].id
			ids = append(ids, id)
			args = append(args, string(id))
		}
		roots[string(session.ID)] = ids
	}
	if len(args) == 0 {
		return nil
	}
	windows, err := cache.windowsFor(ctx, reader)
	if err != nil || len(windows) == 0 {
		return err
	}
	rows, err := reader.QueryContext(ctx, `SELECT root_id,
       COALESCE(json_extract(price_json, '$.provider'), ''), COALESCE(json_extract(price_json, '$.model'), '')
FROM billing_ledger WHERE root_id IN (`+sessionPlaceholders(len(args))+`) AND COALESCE(price_json, '') <> ''`, args...)
	if err != nil {
		return fmt.Errorf("load context windows: %w", err)
	}
	defer rows.Close()
	byRoot := map[contract.RequestID]int{}
	for rows.Next() {
		var root, provider, model string
		if err := rows.Scan(&root, &provider, &model); err != nil {
			return fmt.Errorf("scan context window: %w", err)
		}
		if window, ok := windows[modelWindowKey(provider, model)]; ok {
			byRoot[contract.RequestID(root)] = window
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate context windows: %w", err)
	}
	for index := range sessions {
		session := &sessions[index]
		for _, id := range roots[string(session.ID)] {
			if window, ok := byRoot[id]; ok {
				value := window
				session.ContextWindowTokens = &value
				break
			}
		}
	}
	return nil
}

// ledgerEntry is one billing ledger row of a conversation.
type ledgerEntry struct {
	root    contract.RequestID
	attempt int
	account string
	amount  string
	reason  string
	price   string
}

// loadSessionLedger reads the billing ledger rows of a conversation's calls.
func (store *Store) loadSessionLedger(ctx context.Context, sessionID contract.SessionID) ([]ledgerEntry, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT b.root_id, b.attempt, b.account_key, b.amount_usd, b.reason,
       COALESCE(json_extract(NULLIF(b.price_json, ''), '$.expression'), '')
FROM billing_ledger b
WHERE b.root_id IN (
    SELECT id FROM request_records INDEXED BY request_records_session_turns_idx
    WHERE parent_request_id IS NULL AND COALESCE(session_id, id) = ?
)`, string(sessionID))
	if err != nil {
		return nil, fmt.Errorf("load session ledger: %w", err)
	}
	defer rows.Close()
	var entries []ledgerEntry
	for rows.Next() {
		var entry ledgerEntry
		if err := rows.Scan(&entry.root, &entry.attempt, &entry.account, &entry.amount, &entry.reason,
			&entry.price); err != nil {
			return nil, fmt.Errorf("scan session ledger: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session ledger: %w", err)
	}
	return entries, nil
}

// sessionCost sums the priced attempts of a conversation.
func sessionCost(entries []ledgerEntry) (*contract.SessionCost, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	total := new(big.Rat)
	cost := contract.SessionCost{}
	for _, entry := range entries {
		switch entry.reason {
		case "priced":
			amount, ok := new(big.Rat).SetString(entry.amount)
			if !ok {
				return nil, fmt.Errorf("invalid stored amount")
			}
			total.Add(total, amount)
			cost.Priced++
		case "pending":
			cost.Pending++
		default:
			cost.Unpriced++
		}
	}
	cost.AmountUSD = total.FloatString(9)
	return &cost, nil
}

// sessionContextDetail builds a session's context view from its root calls,
// enriched with the full records and the billing ledger.
func sessionContextDetail(
	calls []contextCall,
	fold contextFold,
	turns []contract.RequestRecord,
	ledger []ledgerEntry,
) *contract.SessionContext {
	byID := make(map[contract.RequestID]*contract.RequestRecord, len(turns))
	for index := range turns {
		byID[turns[index].ID] = &turns[index]
	}
	final := make(map[contract.RequestID]ledgerEntry, len(ledger))
	for _, entry := range ledger {
		if record := byID[entry.root]; record != nil && entry.attempt == max(1, record.AttemptIndex) {
			final[entry.root] = entry
		}
	}
	for index := range calls {
		call := &calls[index]
		record := byID[call.id]
		if record == nil {
			continue
		}
		call.ended = callEnd(*record)
		call.model = effectiveModel(*record)
		call.usageValue = record.Usage
		call.carried = record.Context
		if record.ServiceID != nil {
			call.service = string(*record.ServiceID)
		}
		if entry, ok := final[call.id]; ok {
			call.account, call.price = entry.account, entry.price
		}
	}
	view := &contract.SessionContext{
		Calls:       make([]contract.ContextCall, 0, len(fold.main)),
		Compactions: fold.compactions,
		CacheMisses: cacheMisses(calls, fold),
	}
	if view.Compactions == nil {
		view.Compactions = []contract.ContextCompaction{}
	}
	for _, index := range fold.main {
		view.Calls = append(view.Calls, contract.ContextCall{RequestID: calls[index].id, Tokens: calls[index].input})
	}
	return view
}

// cacheMisses finds the main calls that read less than half of the prompt
// cache the previous main call built. The previous call built what it read
// and wrote; a provider that reports no writes caches implicitly, so its
// whole input counts. A call is measured against the part of that cache it
// could reuse, at most its own input, so a call that grew by a large tool
// result still reads as a hit. The call after a compaction rebuilds its
// cache by design and is skipped.
func cacheMisses(calls []contextCall, fold contextFold) []contract.CacheMiss {
	misses := []contract.CacheMiss{}
	longTTL := false
	for position, index := range fold.main {
		call := calls[index]
		longTTL = longTTL || call.cacheWrite1h > 0
		if position == 0 || fold.compactionStart[position] {
			continue
		}
		previous := calls[fold.main[position-1]]
		if call.input < cacheMissFloorTokens || !call.cacheReported() || !previous.cacheReported() {
			continue
		}
		built := previous.input
		if previous.cacheWrite != nil {
			built = *previous.cacheWrite
			if previous.cacheRead != nil {
				built += *previous.cacheRead
			}
		}
		if built < cacheMissFloorTokens {
			continue
		}
		expected := min(built, call.input)
		read := 0
		if call.cacheRead != nil {
			read = *call.cacheRead
		}
		if read*2 >= expected {
			continue
		}
		miss := contract.CacheMiss{RequestID: call.id, RewrittenTokens: expected - read}
		ttl := shortCacheTTL
		if longTTL {
			ttl = longCacheTTL
		}
		switch {
		case call.service != "" && previous.service != "" && call.service != previous.service,
			call.account != "" && previous.account != "" && call.account != previous.account:
			miss.Reason = contract.CacheMissProviderChanged
		case call.model != "" && previous.model != "" && call.model != previous.model:
			miss.Reason = contract.CacheMissModelChanged
		case idleBetween(previous, call) > ttl:
			miss.Reason = contract.CacheMissExpired
			idle, lifetime := idleBetween(previous, call).Milliseconds(), ttl.Milliseconds()
			miss.IdleMs, miss.TTLMs = &idle, &lifetime
		case prefixChange(previous.carried, call.carried) != "":
			miss.Reason = contract.CacheMissPrefixChanged
			miss.Changed = prefixChange(previous.carried, call.carried)
		default:
			miss.Reason = contract.CacheMissUnknown
		}
		miss.ExtraUSD = rewriteCost(call, miss.RewrittenTokens)
		misses = append(misses, miss)
	}
	return misses
}

// prefixChange names what changed at the head of what two calls sent, in the
// order a prompt cache reads it: "system" for the system prompt, "tools" for
// the tool definitions, "skills" for the skills catalog, else the rules or
// memory file that changed, appeared or went. Empty when nothing did or
// either call's context is unknown.
func prefixChange(previous, call *contract.RequestContext) string {
	if previous == nil || call == nil {
		return ""
	}
	if previous.SystemHash != "" && call.SystemHash != "" && previous.SystemHash != call.SystemHash {
		return "system"
	}
	if previous.ToolsHash != "" && call.ToolsHash != "" && previous.ToolsHash != call.ToolsHash {
		return "tools"
	}
	key := func(item contract.ContextItem) string {
		return string(item.Kind) + "\x00" + item.Path + "\x00" + item.Name
	}
	before := make(map[string]string, len(previous.Items))
	for _, item := range previous.Items {
		before[key(item)] = item.Hash
	}
	name := func(item contract.ContextItem) string {
		if item.Kind == contract.ContextItemSkills || item.Name == "" {
			return string(item.Kind)
		}
		return contract.ClampRunes(item.Name, contract.MaxContextChangedRunes)
	}
	now := make(map[string]bool, len(call.Items))
	for _, item := range call.Items {
		now[key(item)] = true
		if item.Kind == contract.ContextItemMCP {
			// MCP tools are tool definitions, compared above.
			continue
		}
		hash, ok := before[key(item)]
		if !ok || (hash != "" && item.Hash != "" && hash != item.Hash) {
			return name(item)
		}
	}
	for _, item := range previous.Items {
		if item.Kind != contract.ContextItemMCP && !now[key(item)] {
			return name(item)
		}
	}
	return ""
}

// idleBetween is the gap from the end of previous to the start of call.
func idleBetween(previous, call contextCall) time.Duration {
	end := previous.started
	if previous.ended != nil {
		end = *previous.ended
	}
	return max(0, call.started.Sub(end))
}

// rewriteCost is what rewriting tokens cost call over reading them from the
// cache: the call priced as it was, less the call priced with those tokens
// read instead. They come out of the cache writes first, then out of the
// uncached input. Nil without a price.
func rewriteCost(call contextCall, tokens int) *string {
	if call.price == "" || call.usageValue == nil || tokens <= 0 {
		return nil
	}
	actual, err := pricing.Evaluate(call.price, call.usageValue, call.started)
	if err != nil {
		return nil
	}
	hit := *call.usageValue
	read := 0
	if hit.CacheReadTokens != nil {
		read = *hit.CacheReadTokens
	}
	moved := max(0, min(tokens, hit.InputTokens-read))
	read += moved
	hit.CacheReadTokens = &read
	if hit.CacheWriteTokens != nil {
		// Evaluate cuts the one-hour and twelve-hour parts down to what is left.
		write := max(0, *hit.CacheWriteTokens-moved)
		hit.CacheWriteTokens = &write
	}
	saved, err := pricing.Evaluate(call.price, &hit, call.started)
	if err != nil {
		return nil
	}
	actualAmount, ok := new(big.Rat).SetString(actual.AmountUSD)
	savedAmount, okSaved := new(big.Rat).SetString(saved.AmountUSD)
	if !ok || !okSaved {
		return nil
	}
	extra := actualAmount.Sub(actualAmount, savedAmount)
	if extra.Sign() < 0 {
		extra.SetInt64(0)
	}
	value := extra.FloatString(9)
	return &value
}

// callEnd is when a call finished: its completion, or its start plus its
// latency when only that survived. Nil while it runs.
func callEnd(record contract.RequestRecord) *time.Time {
	if record.CompletedAt != nil {
		return record.CompletedAt
	}
	if record.LatencyMs != nil {
		end := record.StartedAt.Add(time.Duration(*record.LatencyMs) * time.Millisecond)
		return &end
	}
	return nil
}

// effectiveModel is the model a call ran on: the one a recovery served,
// else the redirect target, else the one the client asked for.
func effectiveModel(record contract.RequestRecord) string {
	if record.Recovery != nil && record.Recovery.UpstreamModel != "" {
		return record.Recovery.UpstreamModel
	}
	if record.ModelRedirect != nil && record.ModelRedirect.To != "" {
		return record.ModelRedirect.To
	}
	if record.RequestedModel != nil {
		return *record.RequestedModel
	}
	return ""
}

// sortContextCalls orders a session's root calls by start, the order the
// fold reads them in. Calls arrive from the runtime query in no order.
func sortContextCalls(calls []contextCall) {
	slices.SortStableFunc(calls, func(left, right contextCall) int {
		if order := left.started.Compare(right.started); order != 0 {
			return order
		}
		return strings.Compare(string(left.id), string(right.id))
	})
}
