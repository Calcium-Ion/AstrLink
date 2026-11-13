package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/modelmeta"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

var contextTestStart = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

// testContextCall is a main call minutes into the conversation that read and
// wrote the given cache; negative counts leave the cache unreported.
func testContextCall(id string, minute float64, model string, input, read, write int) contextCall {
	started := contextTestStart.Add(time.Duration(minute * float64(time.Minute)))
	ended := started.Add(5 * time.Second)
	call := contextCall{id: contract.RequestID(id), started: started, ended: &ended, model: model, usage: true, input: input}
	if read >= 0 {
		call.cacheRead = &read
	}
	if write >= 0 {
		call.cacheWrite = &write
	}
	return call
}

func foldedIDs(calls []contextCall, fold contextFold) []string {
	ids := make([]string, 0, len(fold.main))
	for _, index := range fold.main {
		ids = append(ids, string(calls[index].id))
	}
	return ids
}

func TestFoldContextSeparatesHelpersFromCompactions(t *testing.T) {
	calls := []contextCall{
		testContextCall("request_1", 0, "opus", 50_000, -1, -1),
		testContextCall("request_2", 1, "opus", 80_000, -1, -1),
		// A title on a smaller model.
		testContextCall("request_title", 1.5, "haiku", 2_000, -1, -1),
		testContextCall("request_3", 2, "opus", 120_000, -1, -1),
		testContextCall("request_4", 3, "opus", 150_000, -1, -1),
		// Still running: no usage yet.
		{id: "request_running", started: contextTestStart.Add(3*time.Minute + time.Second), model: "opus"},
		testContextCall("request_5", 4, "opus", 20_000, -1, -1),
		testContextCall("request_6", 5, "opus", 40_000, -1, -1),
		// A quick check on the same model; the conversation then carries on.
		testContextCall("request_check", 5.5, "opus", 1_000, -1, -1),
		testContextCall("request_7", 6, "opus", 45_000, -1, -1),
		testContextCall("request_8", 7, "opus", 60_000, -1, -1),
		// The newest call dropped: a compaction until more calls say otherwise.
		testContextCall("request_9", 8, "opus", 10_000, -1, -1),
	}
	fold := foldContext(calls)
	want := []string{"request_1", "request_2", "request_3", "request_4", "request_5", "request_6", "request_7", "request_8", "request_9"}
	if got := foldedIDs(calls, fold); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("main=%v, want %v", got, want)
	}
	if len(fold.compactions) != 2 ||
		fold.compactions[0] != (contract.ContextCompaction{RequestID: "request_5", BeforeTokens: 150_000, AfterTokens: 20_000}) ||
		fold.compactions[1] != (contract.ContextCompaction{RequestID: "request_9", BeforeTokens: 60_000, AfterTokens: 10_000}) {
		t.Fatalf("compactions=%+v", fold.compactions)
	}
	if !fold.compactionStart[4] || !fold.compactionStart[8] || len(fold.compactionStart) != 2 {
		t.Fatalf("compaction starts=%v", fold.compactionStart)
	}
	// The point is the larger of the last two compactions.
	if fold.contextTokens == nil || *fold.contextTokens != 10_000 || fold.pointTokens == nil || *fold.pointTokens != 150_000 {
		t.Fatalf("context=%v point=%v", fold.contextTokens, fold.pointTokens)
	}

	// Once the conversation climbs back, the newest drop was a helper.
	calls = append(calls, testContextCall("request_10", 9, "opus", 65_000, -1, -1))
	fold = foldContext(calls)
	if len(fold.compactions) != 1 || *fold.contextTokens != 65_000 || *fold.pointTokens != 150_000 {
		t.Fatalf("compactions=%+v context=%d", fold.compactions, *fold.contextTokens)
	}
}

func TestFoldContextLinksCodexCompactSummaries(t *testing.T) {
	calls := []contextCall{
		testContextCall("request_1", 0, "gpt", 200_000, 0, -1),
		testContextCall("request_2", 1, "gpt", 230_000, 200_000, -1),
		testContextCall("request_compact", 2, "gpt", 231_000, 230_000, -1),
		testContextCall("request_3", 3, "gpt", 18_000, 0, -1),
	}
	calls[2].protocol = contract.ProtocolOpenAIResponsesCompact
	fold := foldContext(calls)
	if len(fold.main) != 4 || len(fold.compactions) != 1 {
		t.Fatalf("main=%v compactions=%+v", foldedIDs(calls, fold), fold.compactions)
	}
	compaction := fold.compactions[0]
	if compaction.RequestID != "request_3" || compaction.SummaryRequestID == nil ||
		*compaction.SummaryRequestID != "request_compact" || compaction.BeforeTokens != 231_000 {
		t.Fatalf("compaction=%+v", compaction)
	}
	if session := (contract.RequestSession{}); true {
		applyContextFold(&session, fold)
		if session.CompactionCount != 1 || *session.ContextTokens != 18_000 || *session.CompactionPointTokens != 231_000 {
			t.Fatalf("session=%+v", session)
		}
	}
	if fold := foldContext(nil); fold.contextTokens != nil || fold.pointTokens != nil || len(fold.main) != 0 {
		t.Fatalf("empty fold=%+v", fold)
	}
}

func TestCacheMissesExplainTheFirstReasonThatHolds(t *testing.T) {
	built := func(id string, minute float64) contextCall {
		return testContextCall(id, minute, "opus", 100_000, 90_000, 10_000)
	}
	for _, tc := range []struct {
		name     string
		previous contextCall
		current  contextCall
		edit     func(previous, current *contextCall)
		want     *contract.CacheMiss
	}{
		{
			name: "hit", previous: built("request_a", 0),
			current: testContextCall("request_b", 1, "opus", 104_000, 100_000, 4_000),
		},
		{
			// Half of what the previous call built still counts as a hit.
			name: "partial hit", previous: built("request_a", 0),
			current: testContextCall("request_b", 1, "opus", 104_000, 50_000, 54_000),
		},
		{
			name: "unknown", previous: built("request_a", 0),
			current: testContextCall("request_b", 1, "opus", 104_000, 0, 104_000),
			want:    &contract.CacheMiss{RequestID: "request_b", Reason: contract.CacheMissUnknown, RewrittenTokens: 100_000},
		},
		{
			// A call measured against the part of the cache it could reuse.
			name: "shrunk prompt", previous: built("request_a", 0),
			current: testContextCall("request_b", 1, "opus", 60_000, 20_000, 40_000),
			want:    &contract.CacheMiss{RequestID: "request_b", Reason: contract.CacheMissUnknown, RewrittenTokens: 40_000},
		},
		{
			name: "expired", previous: built("request_a", 0),
			current: testContextCall("request_b", 12, "opus", 104_000, 0, 104_000),
			want: &contract.CacheMiss{RequestID: "request_b", Reason: contract.CacheMissExpired, RewrittenTokens: 100_000,
				IdleMs: ptrInt64((12*time.Minute - 5*time.Second).Milliseconds()), TTLMs: ptrInt64(5 * time.Minute.Milliseconds())},
		},
		{
			name: "one-hour cache outlives the gap", previous: built("request_a", 0),
			current: testContextCall("request_b", 30, "opus", 104_000, 0, 104_000),
			edit:    func(previous, _ *contextCall) { previous.cacheWrite1h = 10_000 },
			want:    &contract.CacheMiss{RequestID: "request_b", Reason: contract.CacheMissUnknown, RewrittenTokens: 100_000},
		},
		{
			name: "model", previous: built("request_a", 0),
			current: testContextCall("request_b", 12, "sonnet", 104_000, 0, 104_000),
			want:    &contract.CacheMiss{RequestID: "request_b", Reason: contract.CacheMissModelChanged, RewrittenTokens: 100_000},
		},
		{
			name: "service", previous: built("request_a", 0),
			current: testContextCall("request_b", 12, "sonnet", 104_000, 0, 104_000),
			edit:    func(previous, current *contextCall) { previous.service, current.service = "service_a", "service_b" },
			want:    &contract.CacheMiss{RequestID: "request_b", Reason: contract.CacheMissProviderChanged, RewrittenTokens: 100_000},
		},
		{
			name: "account", previous: built("request_a", 0),
			current: testContextCall("request_b", 1, "opus", 104_000, 0, 104_000),
			edit: func(previous, current *contextCall) {
				previous.service, current.service = "service_a", "service_a"
				previous.account, current.account = "account_a", "account_b"
			},
			want: &contract.CacheMiss{RequestID: "request_b", Reason: contract.CacheMissProviderChanged, RewrittenTokens: 100_000},
		},
		{
			// A provider that reports reads only caches the whole prompt.
			name: "implicit cache", previous: testContextCall("request_a", 0, "gpt", 100_000, 80_000, -1),
			current: testContextCall("request_b", 1, "gpt", 104_000, 10_000, -1),
			want:    &contract.CacheMiss{RequestID: "request_b", Reason: contract.CacheMissUnknown, RewrittenTokens: 90_000},
		},
		{
			name: "small prefix", previous: testContextCall("request_a", 0, "opus", 6_000, 0, 6_000),
			current: testContextCall("request_b", 1, "opus", 9_000, 0, 9_000),
		},
		{
			name: "small call", previous: built("request_a", 0),
			current: testContextCall("request_b", 1, "opus", 7_000, 0, 7_000),
		},
		{
			name: "unreported", previous: built("request_a", 0),
			current: testContextCall("request_b", 1, "opus", 104_000, -1, -1),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous, current := tc.previous, tc.current
			if tc.edit != nil {
				tc.edit(&previous, &current)
			}
			calls := []contextCall{previous, current}
			misses := cacheMisses(calls, contextFold{main: []int{0, 1}, compactionStart: map[int]bool{}})
			if tc.want == nil {
				if len(misses) != 0 {
					t.Fatalf("misses=%+v", misses)
				}
				return
			}
			if len(misses) != 1 {
				t.Fatalf("misses=%+v", misses)
			}
			got := misses[0]
			if got.RequestID != tc.want.RequestID || got.Reason != tc.want.Reason || got.RewrittenTokens != tc.want.RewrittenTokens ||
				fmt.Sprint(deref(got.IdleMs)) != fmt.Sprint(deref(tc.want.IdleMs)) ||
				fmt.Sprint(deref(got.TTLMs)) != fmt.Sprint(deref(tc.want.TTLMs)) || got.ExtraUSD != nil {
				t.Fatalf("miss=%+v idle=%v ttl=%v, want %+v", got, deref(got.IdleMs), deref(got.TTLMs), *tc.want)
			}
			if err := got.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}

	// The rebuild after a compaction is expected.
	calls := []contextCall{
		testContextCall("request_a", 0, "opus", 100_000, 90_000, 10_000),
		testContextCall("request_b", 1, "opus", 30_000, 0, 30_000),
	}
	if misses := cacheMisses(calls, contextFold{main: []int{0, 1}, compactionStart: map[int]bool{1: true}}); len(misses) != 0 {
		t.Fatalf("misses after compaction=%+v", misses)
	}
}

func TestRewriteCostPricesTheCacheHitThatDidNotHappen(t *testing.T) {
	// Per million tokens: input 3, output 15, cache read 0.3, cache write 3.75.
	expression := `tier("standard", p * 3 + c * 15 + cr * 0.3 + cc * 3.75)`
	call := testContextCall("request_b", 1, "opus", 120_000, 0, 120_000)
	call.price = expression
	call.usageValue = &contract.Usage{InputTokens: 120_000, OutputTokens: 2_000, TotalTokens: 122_000,
		CacheReadTokens: ptrInt(0), CacheWriteTokens: ptrInt(120_000)}
	// Written: 120k × 3.75. A hit reads 110k × 0.3 and writes 10k × 3.75.
	if got := rewriteCost(call, 110_000); got == nil || *got != "0.379500000" {
		t.Fatalf("extra=%v", deref(got))
	}
	// Without cache writes the rewritten tokens were sent at the input price.
	call.usageValue = &contract.Usage{InputTokens: 120_000, OutputTokens: 2_000, TotalTokens: 122_000, CacheReadTokens: ptrInt(10_000)}
	if got := rewriteCost(call, 100_000); got == nil || *got != "0.270000000" {
		t.Fatalf("extra=%v", deref(got))
	}
	call.price = ""
	if got := rewriteCost(call, 100_000); got != nil {
		t.Fatalf("extra without price=%v", *got)
	}
}

func TestRequestSessionsReportContextUsageAndCost(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "session-context.db"))
	defer store.Close()
	expression := `tier("standard", p * 3 + c * 15 + cr * 0.3 + cc * 3.75)`
	catalog := pricing.Catalog{
		Version: "context_v1", ActivatedAt: contextTestStart.Add(-time.Hour), GeneratedAt: contextTestStart,
		Prices: []pricing.Price{{Provider: "anthropic", Model: "claude-opus", Expression: expression}},
		Models: []modelmeta.Model{
			{Provider: "anthropic", Model: "claude-opus", ContextTokens: 200_000},
			{Provider: "anthropic", Model: "claude-haiku", ContextTokens: 100_000},
		},
		Warnings: []string{},
	}
	if err := store.SavePricingCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	session := contract.SessionID("session_context")
	type call struct {
		id                         string
		minute                     float64
		model                      string
		input, read, write, output int
		parent                     string
		ledger                     string
	}
	calls := []call{
		{id: "request_ctx_1", minute: 0, model: "claude-opus", input: 100_000, read: 0, write: 100_000, output: 1_000, ledger: "0.390000000"},
		// The cache missed: request_ctx_1 built 100k, this call read none.
		{id: "request_ctx_2", minute: 1.5, model: "claude-opus", input: 110_000, read: 0, write: 110_000, output: 1_000, ledger: "0.427500000"},
		// An earlier attempt of request_ctx_2 that failed after reading the cache.
		{id: "request_ctx_2_retry", minute: 1, model: "claude-opus", input: 105_000, read: 100_000, write: 5_000, parent: "request_ctx_2", ledger: "unpriced"},
		{id: "request_ctx_title", minute: 1.6, model: "claude-haiku", input: 2_000, read: -1, write: -1, output: 20, ledger: "pending"},
		// Compacted.
		{id: "request_ctx_3", minute: 2, model: "claude-opus", input: 30_000, read: 0, write: 30_000, output: 1_000, ledger: "0.127500000"},
		{id: "request_ctx_4", minute: 3, model: "claude-opus", input: 35_000, read: 30_000, write: 5_000, output: 1_000, ledger: "0.042750000"},
	}
	for _, item := range calls {
		started := contextTestStart.Add(time.Duration(item.minute * float64(time.Minute)))
		completed := started.Add(10 * time.Second)
		latency := 10_000
		model := item.model
		record := contract.RequestRecord{
			ID: contract.RequestID(item.id), SessionID: &session, StartedAt: started, CompletedAt: &completed, LatencyMs: &latency,
			Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolAnthropicMessages, RequestedModel: &model,
			AttemptIndex: 1, Audit: contract.NotCapturedAuditSummary(),
			Usage: &contract.Usage{InputTokens: item.input, OutputTokens: item.output, TotalTokens: item.input + item.output},
		}
		if item.read >= 0 {
			record.Usage.CacheReadTokens = ptrInt(item.read)
		}
		if item.write >= 0 {
			record.Usage.CacheWriteTokens = ptrInt(item.write)
		}
		root, attempt := item.id, 1
		if item.parent != "" {
			parent := contract.RequestID(item.parent)
			record.ParentRequestID = &parent
			record.Status = contract.RequestStatusFailed
			root = item.parent
		} else if item.id == "request_ctx_2" {
			record.AttemptIndex, attempt = 2, 2
		}
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
		amount, reason, price := item.ledger, "priced", fmt.Sprintf(`{"provider":"anthropic","model":%q,"expression":%q}`, item.model, expression)
		switch item.ledger {
		case "unpriced":
			amount, reason = "0", "missing_price"
		case "pending":
			amount, reason, price = "0", "pending", ""
		}
		if _, err := store.db.ExecContext(ctx, `INSERT INTO billing_ledger
(root_id,attempt,service_id,account_key,model,started_at,terminal,price_json,amount_usd,reason)
VALUES (?,?,'service_context','account_a',?,?,1,?,?,?)`, root, attempt, item.model, billingTime(started), price, amount, reason); err != nil {
			t.Fatal(err)
		}
	}

	page, err := store.ListRequestSessions(ctx, storagecontract.RequestSessionListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("sessions=%+v", page.Items)
	}
	listed := page.Items[0]
	if deref(listed.ContextTokens) != 35_000 || deref(listed.CompactionPointTokens) != 110_000 ||
		listed.CompactionCount != 1 || deref(listed.ContextWindowTokens) != 200_000 || listed.Cost != nil {
		t.Fatalf("listed context=%v point=%v compactions=%d window=%v cost=%+v", deref(listed.ContextTokens),
			deref(listed.CompactionPointTokens), listed.CompactionCount, deref(listed.ContextWindowTokens), listed.Cost)
	}
	// Retries count toward the totals; the hit ratio covers every call that
	// reported its cache.
	wantUsage := contract.SessionUsage{InputTokens: 382_000, OutputTokens: 4_020, CacheReadTokens: 130_000, CacheWriteTokens: 250_000}
	if listed.Usage == nil || listed.Usage.InputTokens != wantUsage.InputTokens || listed.Usage.OutputTokens != wantUsage.OutputTokens ||
		listed.Usage.CacheReadTokens != wantUsage.CacheReadTokens || listed.Usage.CacheWriteTokens != wantUsage.CacheWriteTokens ||
		listed.Usage.CacheHitRatio == nil || fmt.Sprintf("%.4f", *listed.Usage.CacheHitRatio) != fmt.Sprintf("%.4f", 130_000.0/380_000.0) {
		t.Fatalf("usage=%+v ratio=%v", listed.Usage, deref(listed.Usage.CacheHitRatio))
	}

	detail, err := store.GetRequestSession(ctx, string(session))
	if err != nil {
		t.Fatal(err)
	}
	if err := detail.Validate(); err != nil {
		t.Fatal(err)
	}
	if detail.Cost == nil || *detail.Cost != (contract.SessionCost{AmountUSD: "0.987750000", Priced: 4, Unpriced: 1, Pending: 1}) {
		t.Fatalf("cost=%+v", detail.Cost)
	}
	view := detail.Context
	if view == nil {
		t.Fatal("detail has no context view")
	}
	wantCalls := []contract.ContextCall{
		{RequestID: "request_ctx_1", Tokens: 100_000}, {RequestID: "request_ctx_2", Tokens: 110_000},
		{RequestID: "request_ctx_3", Tokens: 30_000}, {RequestID: "request_ctx_4", Tokens: 35_000},
	}
	if fmt.Sprint(view.Calls) != fmt.Sprint(wantCalls) {
		t.Fatalf("calls=%+v", view.Calls)
	}
	if len(view.Compactions) != 1 || view.Compactions[0] != (contract.ContextCompaction{
		RequestID: "request_ctx_3", BeforeTokens: 110_000, AfterTokens: 30_000}) {
		t.Fatalf("compactions=%+v", view.Compactions)
	}
	// The miss is priced from the final attempt's ledger entry.
	if len(view.CacheMisses) != 1 {
		t.Fatalf("misses=%+v", view.CacheMisses)
	}
	miss := view.CacheMisses[0]
	if miss.RequestID != "request_ctx_2" || miss.Reason != contract.CacheMissUnknown || miss.RewrittenTokens != 100_000 ||
		miss.ExtraUSD == nil || *miss.ExtraUSD != "0.345000000" {
		t.Fatalf("miss=%+v extra=%v", miss, deref(miss.ExtraUSD))
	}
}

func ptrInt64(value int64) *int64 { return &value }

func deref[T any](value *T) any {
	if value == nil {
		return nil
	}
	return *value
}
