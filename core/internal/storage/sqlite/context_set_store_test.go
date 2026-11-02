package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func contextTestRecord(id string, session contract.SessionID, minute float64, requestContext *contract.RequestContext) contract.RequestRecord {
	started := contextTestStart.Add(time.Duration(minute * float64(time.Minute)))
	completed := started.Add(10 * time.Second)
	latency := 10_000
	model := "claude-opus"
	project := "~/proj"
	return contract.RequestRecord{
		ID: contract.RequestID(id), SessionID: &session, StartedAt: started, CompletedAt: &completed, LatencyMs: &latency,
		Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolAnthropicMessages, RequestedModel: &model,
		AttemptIndex: 1, Audit: contract.NotCapturedAuditSummary(), ClientType: contract.ClientClaudeCode,
		Context: requestContext, Project: &project,
	}
}

func carriedContext(rules, skills string) *contract.RequestContext {
	return &contract.RequestContext{
		Parts: &contract.ContextParts{History: 1_000, Memory: 700, System: 300},
		Items: []contract.ContextItem{
			{Kind: contract.ContextItemRules, Name: "AGENTS.md", Path: "~/proj/AGENTS.md", Scope: contract.ContextScopeProject, Tokens: 400, Hash: rules},
			{Kind: contract.ContextItemSkills, Count: ptrInt(2), Names: []string{"humanizer", "deploy"}, Tokens: 300, Hash: skills},
		},
		SystemHash: "fp1_system",
	}
}

// contextSetOf is the set a record points at.
func contextSetOf(t *testing.T, store *Store, id string) int64 {
	t.Helper()
	var set sql.NullInt64
	if err := store.db.QueryRowContext(context.Background(), `SELECT context_set_id FROM request_records WHERE id = ?`, id).Scan(&set); err != nil {
		t.Fatal(err)
	}
	if !set.Valid {
		t.Fatalf("record %s points at no context set", id)
	}
	return set.Int64
}

// contextSetsLeft lists which of sets still exist.
func contextSetsLeft(t *testing.T, store *Store, sets ...int64) []int64 {
	t.Helper()
	var left []int64
	for _, set := range sets {
		var count int
		if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM context_sets WHERE id = ?`, set).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			left = append(left, set)
		}
	}
	return left
}

func contextSetCount(t *testing.T, store *Store) int {
	t.Helper()
	var count int
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM context_sets`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestRequestContextIsStoredInSharedSets(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "request-context.db"))
	defer store.Close()

	first := contextTestRecord("request_carry_1", "session_carry_a", 0, carriedContext("fp1_rules_a", "fp1_skills"))
	first.Context.MemoryWrites = []contract.MemoryWrite{{Kind: contract.ContextItemMemory, Name: "MEMORY.md", Title: "Reply in Chinese", Entry: "reply.md"}}
	first.Context.FailedRepeats = &contract.FailedRepeats{Tool: "Bash", Count: 3, Command: true}
	// The same items in another conversation share the set; edited rules
	// make another. A helper call carries no items and still names its
	// project.
	second := contextTestRecord("request_carry_2", "session_carry_b", 1, carriedContext("fp1_rules_a", "fp1_skills"))
	third := contextTestRecord("request_carry_3", "session_carry_b", 2, carriedContext("fp1_rules_b", "fp1_skills"))
	helper := contextTestRecord("request_carry_title", "session_carry_b", 3, &contract.RequestContext{Helper: true})
	for _, record := range []contract.RequestRecord{first, second, third, helper} {
		if err := store.UpsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	if contextSetOf(t, store, "request_carry_1") != contextSetOf(t, store, "request_carry_2") ||
		contextSetOf(t, store, "request_carry_2") == contextSetOf(t, store, "request_carry_3") || contextSetCount(t, store) != 3 {
		t.Fatalf("calls carrying the same items do not share one set; %d sets", contextSetCount(t, store))
	}
	var encoded, items string
	if err := store.db.QueryRowContext(ctx, `SELECT r.context_json, s.items_json FROM request_records r
JOIN context_sets s ON s.id = r.context_set_id WHERE r.id = 'request_carry_1'`).Scan(&encoded, &items); err != nil {
		t.Fatal(err)
	}
	// The record keeps its own facts; the set keeps the items with the
	// skills' names.
	if strings.Contains(encoded, `"items"`) || !strings.Contains(encoded, `"system_hash":"fp1_system"`) ||
		!strings.Contains(items, `"names":["humanizer","deploy"]`) {
		t.Fatalf("record context %s, set items %s", encoded, items)
	}

	stored, err := store.GetRequestRecord(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Project == nil || *stored.Project != "~/proj" || stored.Context == nil || stored.Context.SystemHash != "fp1_system" {
		t.Fatalf("stored project %v context %+v", deref(stored.Project), stored.Context)
	}
	if len(stored.Context.Items) != 2 || stored.Context.Items[0].Tokens != 400 || stored.Context.Items[0].Hash != "fp1_rules_a" ||
		stored.Context.Items[1].Names != nil || deref(stored.Context.Items[1].Count) != 2 ||
		stored.Context.MemoryWrites[0].Title != "Reply in Chinese" || stored.Context.FailedRepeats.Count != 3 {
		t.Fatalf("stored context %+v", stored.Context)
	}
	page, err := store.ListRequestRecords(ctx, storagecontract.RequestRecordListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 4 || page.Items[0].ID != "request_carry_title" || page.Items[0].Context == nil || !page.Items[0].Context.Helper ||
		page.Items[0].Context.Items != nil || deref(page.Items[0].Project) != "~/proj" || len(page.Items[1].Context.Items) != 2 {
		t.Fatalf("listed = %+v", page.Items)
	}
}

func TestStoredContextIsScaledOnRead(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "context-scale.db"))
	defer store.Close()
	record := contextTestRecord("request_scaled", "session_scaled", 0, &contract.RequestContext{
		Parts: &contract.ContextParts{History: 6_000, Tools: 3_000, System: 1_000, Memory: 2_000},
		Items: []contract.ContextItem{
			{Kind: contract.ContextItemRules, Name: "AGENTS.md", Path: "~/proj/AGENTS.md", Tokens: 1_500, Hash: "fp1_rules"},
			{Kind: contract.ContextItemSkills, Count: ptrInt(1), Names: []string{"deploy"}, Tokens: 500, Hash: "fp1_skills"},
			{Kind: contract.ContextItemMCP, Count: ptrInt(4), Servers: []string{"paseo"}, Tokens: 2_400, Hash: "fp1_mcp"},
		},
	})
	record.Usage = &contract.Usage{InputTokens: 6_000, OutputTokens: 100, TotalTokens: 6_100}
	if err := store.InsertRequestRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetRequestRecord(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The parts sum to the input; the rules and skills to the memory part;
	// the MCP tools scale by the same factor.
	if *stored.Context.Parts != (contract.ContextParts{History: 3_000, Tools: 1_500, System: 500, Memory: 1_000}) {
		t.Fatalf("parts = %+v", stored.Context.Parts)
	}
	var tokens []int
	for _, item := range stored.Context.Items {
		tokens = append(tokens, item.Tokens)
	}
	if !slices.Equal(tokens, []int{750, 250, 1_200}) {
		t.Fatalf("item tokens = %v", tokens)
	}
	var items string
	if err := store.db.QueryRowContext(ctx, `SELECT items_json FROM context_sets`).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(items, `"tokens":1500`) || !strings.Contains(items, `"tokens":2400`) {
		t.Fatalf("the set lost the estimates: %s", items)
	}
}

func TestContextSetsGoWithTheLastCallCarryingThem(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "context-orphans.db"))
	defer store.Close()
	old := time.Now().UTC().AddDate(0, 0, -40)
	record := func(id, session, rules string, minute float64) contract.RequestRecord {
		return contextTestRecord(id, contract.SessionID(session), minute, carriedContext(rules, "fp1_skills"))
	}
	aged := func(record contract.RequestRecord) contract.RequestRecord {
		completed := old.Add(10 * time.Second)
		record.StartedAt, record.CompletedAt = old, &completed
		return record
	}
	for _, item := range []contract.RequestRecord{
		record("request_set_a", "session_set_a", "fp1_rules_a", 0),
		record("request_set_b", "session_set_b", "fp1_rules_a", 1),
		record("request_set_b2", "session_set_b", "fp1_rules_b", 2),
		record("request_set_single", "session_set_single", "fp1_rules_c", 3),
		aged(record("request_set_old", "session_set_old", "fp1_rules_a", 0)),
		aged(record("request_set_old_own", "session_set_old", "fp1_rules_d", 0)),
	} {
		if err := store.InsertRequestRecord(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	shared, edited := contextSetOf(t, store, "request_set_a"), contextSetOf(t, store, "request_set_b2")
	single, agedOwn := contextSetOf(t, store, "request_set_single"), contextSetOf(t, store, "request_set_old_own")
	if contextSetOf(t, store, "request_set_old") != shared || contextSetCount(t, store) != 4 {
		t.Fatalf("%d sets", contextSetCount(t, store))
	}

	// The retention sweep takes the aged calls; the set they shared with
	// newer calls stays.
	settings := contract.DefaultAuditSettings()
	settings.MetadataRetentionDays = 30
	if err := store.UpdateAuditSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SweepExpiredAuditData(ctx); err != nil {
		t.Fatal(err)
	}
	if left := contextSetsLeft(t, store, shared, edited, single, agedOwn); !slices.Equal(left, []int64{shared, edited, single}) {
		t.Fatalf("after the sweep sets %v are left", left)
	}
	// Deleting one call keeps a set another call carries.
	for _, id := range []contract.RequestID{"request_set_single", "request_set_a"} {
		if err := store.DeleteRequestRecord(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if left := contextSetsLeft(t, store, shared, edited, single); !slices.Equal(left, []int64{shared, edited}) {
		t.Fatalf("after the deletes sets %v are left", left)
	}
	if _, err := store.PurgeRequestRecords(ctx, contract.PurgeRequest{Confirm: true, Scope: contract.PurgeScopeSessions, SessionIDs: []contract.SessionID{"session_set_b"}}); err != nil {
		t.Fatal(err)
	}
	if count := contextSetCount(t, store); count != 0 {
		t.Fatalf("after the last calls went %d sets are left", count)
	}

	// A call whose final snapshot carries other items leaves the set of its
	// first snapshot; a stale snapshot that is not applied leaves none.
	running := record("request_set_running", "session_set_c", "fp1_rules_e", 4)
	running.Status, running.CompletedAt, running.LatencyMs = contract.RequestStatusPending, nil, nil
	finished := record("request_set_running", "session_set_c", "fp1_rules_f", 4)
	stale := record("request_set_running", "session_set_c", "fp1_rules_g", 4)
	stale.Status, stale.CompletedAt, stale.LatencyMs = contract.RequestStatusPending, nil, nil
	for _, snapshot := range []contract.RequestRecord{running, finished, stale} {
		if err := store.UpsertRequestRecord(ctx, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := store.GetRequestRecord(ctx, "request_set_running")
	if err != nil {
		t.Fatal(err)
	}
	if contextSetCount(t, store) != 1 || stored.Status != contract.RequestStatusSucceeded || stored.Context.Items[0].Hash != "fp1_rules_f" {
		t.Fatalf("%d sets, status %s, items %+v", contextSetCount(t, store), stored.Status, stored.Context.Items)
	}

	// Purging by age and purging everything take their sets too.
	for _, item := range []contract.RequestRecord{
		record("request_set_d", "session_set_d", "fp1_rules_f", 5),
		record("request_set_e", "session_set_e", "fp1_rules_h", 6),
	} {
		if err := store.InsertRequestRecord(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	before := contextTestStart.Add(5*time.Minute + 30*time.Second)
	if _, err := store.PurgeRequestRecords(ctx, contract.PurgeRequest{Confirm: true, Scope: contract.PurgeScopeBefore, Before: &before}); err != nil {
		t.Fatal(err)
	}
	if left := contextSetsLeft(t, store, contextSetOf(t, store, "request_set_e")); contextSetCount(t, store) != 1 || len(left) != 1 {
		t.Fatalf("after purging by age %d sets are left", contextSetCount(t, store))
	}
	if _, err := store.PurgeRequestRecords(ctx, contract.PurgeRequest{Confirm: true, Scope: contract.PurgeScopeAll}); err != nil {
		t.Fatal(err)
	}
	if count := contextSetCount(t, store); count != 0 {
		t.Fatalf("after purging everything %d sets are left", count)
	}
}

func TestCacheMissNamesTheFileTheCallsCarried(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "context-miss.db"))
	defer store.Close()
	session := contract.SessionID("session_miss")
	for _, call := range []struct {
		id     string
		minute float64
		input  int
		read   int
		rules  string
	}{
		{"request_miss_1", 0, 100_000, 0, "fp1_rules_a"},
		// AGENTS.md changed, so the prefix the cache held did.
		{"request_miss_2", 1, 104_000, 0, "fp1_rules_b"},
		{"request_miss_3", 2, 106_000, 104_000, "fp1_rules_b"},
	} {
		record := contextTestRecord(call.id, session, call.minute, carriedContext(call.rules, "fp1_skills"))
		record.Usage = &contract.Usage{InputTokens: call.input, OutputTokens: 100, TotalTokens: call.input + 100,
			CacheReadTokens: ptrInt(call.read), CacheWriteTokens: ptrInt(call.input - call.read)}
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	detail, err := store.GetRequestSession(ctx, string(session))
	if err != nil {
		t.Fatal(err)
	}
	view := detail.Context
	if view == nil || len(view.Calls) != 3 || view.Calls[1] != (contract.ContextCall{RequestID: "request_miss_2", Tokens: 104_000}) {
		t.Fatalf("context = %+v", view)
	}
	if len(view.CacheMisses) != 1 || view.CacheMisses[0].RequestID != "request_miss_2" ||
		view.CacheMisses[0].Reason != contract.CacheMissPrefixChanged || view.CacheMisses[0].Changed != "AGENTS.md" {
		t.Fatalf("misses = %+v", view.CacheMisses)
	}
}

func TestRequestSessionsReadClientContextMarks(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "context-marks.db"))
	defer store.Close()
	session := contract.SessionID("session_marks")
	call := func(id string, minute float64, input int, requestContext *contract.RequestContext) contract.RequestRecord {
		record := contextTestRecord(id, session, minute, requestContext)
		record.Usage = &contract.Usage{InputTokens: input, OutputTokens: 100, TotalTokens: input + 100}
		return record
	}
	summarized := carriedContext("fp1_rules_a", "fp1_skills")
	summarized.Summary = true
	compacted := carriedContext("fp1_rules_a", "fp1_skills")
	compacted.Compacted = "fp1_summary_1"
	repeating := carriedContext("fp1_rules_a", "fp1_skills")
	repeating.Compacted = "fp1_summary_1"
	repeating.FailedRepeats = &contract.FailedRepeats{Tool: "Bash", Count: 4, Command: true}
	helper := &contract.RequestContext{Helper: true}
	for _, record := range []contract.RequestRecord{
		call("request_mark_1", 0, 100_000, carriedContext("fp1_rules_a", "fp1_skills")),
		// A title the session placed as a helper, even at the same size.
		call("request_mark_title", 0.5, 90_000, helper),
		call("request_mark_2", 1, 120_000, summarized),
		// The client marked a compaction the context hardly shows.
		call("request_mark_3", 2, 70_000, compacted),
		call("request_mark_4", 3, 75_000, repeating),
	} {
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.ListRequestSessions(ctx, storagecontract.RequestSessionListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("sessions = %+v", page.Items)
	}
	listed := page.Items[0]
	if listed.CompactionCount != 1 || deref(listed.ContextTokens) != 75_000 ||
		listed.FailedRepeats == nil || *listed.FailedRepeats != (contract.FailedRepeats{Tool: "Bash", Count: 4, Command: true}) {
		t.Fatalf("listed compactions %d context %v repeats %+v", listed.CompactionCount, deref(listed.ContextTokens), listed.FailedRepeats)
	}
	detail, err := store.GetRequestSession(ctx, string(session))
	if err != nil {
		t.Fatal(err)
	}
	summary := contract.RequestID("request_mark_2")
	if len(detail.Context.Compactions) != 1 || detail.Context.Compactions[0] != (contract.ContextCompaction{
		RequestID: "request_mark_3", SummaryRequestID: detail.Context.Compactions[0].SummaryRequestID,
		BeforeTokens: 120_000, AfterTokens: 70_000, Marked: true,
	}) || deref(detail.Context.Compactions[0].SummaryRequestID) != summary {
		t.Fatalf("compactions = %+v", detail.Context.Compactions)
	}
	for _, main := range detail.Context.Calls {
		if main.RequestID == "request_mark_title" {
			t.Fatal("the helper call joined the curve")
		}
	}
}

func TestFoldContextNeedsBothCallsObservedToMarkACompaction(t *testing.T) {
	before := testContextCall("request_a", 0, "opus", 100_000, -1, -1)
	after := testContextCall("request_b", 1, "opus", 90_000, -1, -1)
	after.observed, after.compacted = true, "fp1_summary"
	// Records from before the upgrade kept nothing to compare against.
	if fold := foldContext([]contextCall{before, after}); len(fold.compactions) != 0 {
		t.Fatalf("compactions = %+v", fold.compactions)
	}
	before.observed = true
	if fold := foldContext([]contextCall{before, after}); len(fold.compactions) != 1 || !fold.compactions[0].Marked {
		t.Fatalf("compactions = %+v", fold.compactions)
	}
	// The same summary on the next call is the same compaction.
	next := testContextCall("request_c", 2, "opus", 95_000, -1, -1)
	next.observed, next.compacted = true, "fp1_summary"
	if fold := foldContext([]contextCall{before, after, next}); len(fold.compactions) != 1 {
		t.Fatalf("compactions = %+v", fold.compactions)
	}
}

func TestPrefixChangeNamesWhatChangedFirst(t *testing.T) {
	base := func() *contract.RequestContext {
		return &contract.RequestContext{
			SystemHash: "fp1_s", ToolsHash: "fp1_t",
			Items: []contract.ContextItem{
				{Kind: contract.ContextItemRules, Name: "CLAUDE.md", Path: "~/.claude/CLAUDE.md", Hash: "fp1_g"},
				{Kind: contract.ContextItemRules, Name: "AGENTS.md", Path: "~/proj/AGENTS.md", Hash: "fp1_p"},
				{Kind: contract.ContextItemSkills, Hash: "fp1_k"},
				{Kind: contract.ContextItemMCP, Hash: "fp1_m"},
			},
		}
	}
	for _, tc := range []struct {
		name string
		edit func(*contract.RequestContext)
		want string
	}{
		{"nothing", func(*contract.RequestContext) {}, ""},
		{"system before tools", func(c *contract.RequestContext) { c.SystemHash, c.ToolsHash = "fp1_s2", "fp1_t2" }, "system"},
		{"tools", func(c *contract.RequestContext) { c.ToolsHash = "fp1_t2" }, "tools"},
		{"rules file", func(c *contract.RequestContext) { c.Items[1].Hash = "fp1_p2" }, "AGENTS.md"},
		{"skills", func(c *contract.RequestContext) { c.Items[2].Hash = "fp1_k2" }, "skills"},
		{"mcp alone", func(c *contract.RequestContext) { c.Items[3].Hash = "fp1_m2" }, ""},
		{"file added", func(c *contract.RequestContext) {
			c.Items = append(c.Items, contract.ContextItem{Kind: contract.ContextItemMemory, Name: "MEMORY.md", Hash: "fp1_n"})
		}, "MEMORY.md"},
		{"file removed", func(c *contract.RequestContext) { c.Items = c.Items[1:] }, "CLAUDE.md"},
		{"hash unknown", func(c *contract.RequestContext) { c.SystemHash, c.Items[1].Hash = "", "" }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := base()
			tc.edit(current)
			if got := prefixChange(base(), current); got != tc.want {
				t.Fatalf("prefixChange = %q, want %q", got, tc.want)
			}
		})
	}
	if prefixChange(nil, base()) != "" {
		t.Fatal("a call without its context named a change")
	}

	previous := testContextCall("request_a", 0, "opus", 100_000, 90_000, 10_000)
	current := testContextCall("request_b", 1, "opus", 104_000, 0, 104_000)
	previous.carried, current.carried = base(), base()
	current.carried.Items[1].Hash = "fp1_p2"
	misses := cacheMisses([]contextCall{previous, current}, foldContext([]contextCall{previous, current}))
	if len(misses) != 1 || misses[0].Reason != contract.CacheMissPrefixChanged || misses[0].Changed != "AGENTS.md" {
		t.Fatalf("misses = %+v", misses)
	}
}
