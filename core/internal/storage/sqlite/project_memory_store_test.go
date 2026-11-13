package sqlite

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

// memoryTestContext is what a Claude Code call in ~/proj carried: a rules
// file, a memory index listing files, the skills catalog and the MCP tools.
func memoryTestContext(rulesHash, indexHash string, files ...string) *contract.RequestContext {
	index := make([]contract.MemoryIndexEntry, 0, len(files))
	for _, file := range files {
		index = append(index, contract.MemoryIndexEntry{File: file, Title: "Title of " + file, Hook: "Hook of " + file})
	}
	return &contract.RequestContext{
		Parts: &contract.ContextParts{History: 6_000, Tools: 3_000, Memory: 1_000},
		Items: []contract.ContextItem{
			{Kind: contract.ContextItemRules, Name: "AGENTS.md", Path: "~/proj/AGENTS.md", Scope: contract.ContextScopeProject, Tokens: 400, Hash: rulesHash},
			{Kind: contract.ContextItemMemory, Name: "MEMORY.md", Path: "~/.claude/projects/~-proj/memory/MEMORY.md", Count: ptrInt(len(files)), Tokens: 300, Hash: indexHash},
			{Kind: contract.ContextItemSkills, Count: ptrInt(3), Names: []string{"humanizer", "deploy", "dataviz"}, Tokens: 300, Hash: "fp1_skills"},
			{Kind: contract.ContextItemMCP, Count: ptrInt(3), Servers: []string{"paseo"}, Tokens: 3_000, Hash: "fp1_mcp"},
		},
		MemoryIndex: index,
		Catalog: &contract.ContextCatalog{
			Skills: []contract.CatalogSkill{
				{Name: "humanizer", Description: "Rewrite text in plain words"}, {Name: "deploy", Description: "Ship it"}, {Name: "dataviz"},
			},
			MCP: []contract.CatalogServer{{Server: "paseo", Tools: []string{"create_agent", "list_agents", "send_agent_prompt"}}},
		},
	}
}

// continuing is a call later in a turn: it carries what the turn opened
// with, and its history shows what the previous response did.
func continuing(base *contract.RequestContext, edit func(*contract.RequestContext)) *contract.RequestContext {
	copied := *base
	edit(&copied)
	return &copied
}

func TestProjectMemoryTellsWhatTheAgentNotedAndReadAgain(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "project-memory.db"))
	defer store.Close()
	const day = 24 * 60
	project := "~/proj"
	first := memoryTestContext("fp1_rules_a", "fp1_index_a", "old.md")
	second := memoryTestContext("fp1_rules_b", "fp1_index_b", "old.md", "no-jitter.md")
	write := []contract.MemoryWrite{
		{Kind: contract.ContextItemMemory, Name: "no-jitter.md", Path: "~/.claude/projects/~-proj/memory/no-jitter.md", Title: "# No jitter"},
		{Kind: contract.ContextItemMemory, Name: "MEMORY.md", Path: "~/.claude/projects/~-proj/memory/MEMORY.md",
			Title: "No jitter-sensitive tests", Hook: "wait on done channels", Entry: "no-jitter.md"},
	}
	read := func(files ...string) func(*contract.RequestContext) {
		return func(requestContext *contract.RequestContext) {
			for _, file := range files {
				requestContext.MemoryReads = append(requestContext.MemoryReads, contract.MemoryRead{Name: file, Path: "~/.claude/projects/~-proj/memory/" + file})
			}
		}
	}
	type call struct {
		id, session string
		minutes     float64
		turn        int
		prompt      string
		context     *contract.RequestContext
		edit        func(*contract.RequestRecord)
	}
	captured := func(record *contract.RequestRecord) { record.Audit.RequestBodyCaptured = true }
	calls := []call{
		{id: "request_mem_first", session: "session_mem_1", minutes: -20 * day, turn: 1, prompt: "Set up the project", context: first},
		{id: "request_mem_ask", session: "session_mem_2", minutes: -3 * day, turn: 1, prompt: "Fix the failover tests", context: first},
		{id: "request_mem_write", session: "session_mem_2", minutes: -3*day + 5, turn: 1, edit: captured,
			context: continuing(first, func(requestContext *contract.RequestContext) { requestContext.MemoryWrites = write })},
		// The same history sent again repeats the write.
		{id: "request_mem_resend", session: "session_mem_2", minutes: -3*day + 6, turn: 1,
			context: continuing(first, func(requestContext *contract.RequestContext) { requestContext.MemoryWrites = write })},
		{id: "request_mem_ci", session: "session_mem_3", minutes: -1 * day, turn: 1, prompt: "CI flakes again", context: second},
		{id: "request_mem_read", session: "session_mem_3", minutes: -1*day + 1, turn: 1, edit: captured,
			context: continuing(second, func(requestContext *contract.RequestContext) {
				read("no-jitter.md")(requestContext)
				requestContext.SkillsUsed = []string{"humanizer"}
			})},
		{id: "request_mem_reread", session: "session_mem_3", minutes: -1*day + 2, turn: 1, context: continuing(second, read("no-jitter.md")),
			edit: func(record *contract.RequestRecord) {
				record.Output = &contract.OutputActivity{Kind: contract.OutputKindTool, Tools: []string{"mcp__paseo__create_agent", "mcp__paseo__create_agent", "Bash"}}
			}},
		{id: "request_mem_old_ask", session: "session_mem_4", minutes: -10, turn: 2, prompt: "Check the old notes", context: second},
		{id: "request_mem_old_read", session: "session_mem_4", minutes: -9, turn: 2, context: continuing(second, read("old.md"))},
		{id: "request_mem_sub", session: "session_mem_sub", minutes: -8, turn: 1, prompt: "Look up the old note", context: continuing(second, read("old.md")),
			edit: func(record *contract.RequestRecord) {
				parent := contract.SessionID("session_mem_4")
				record.ParentSessionID = &parent
			}},
		// A session that started before the index changed still sends the
		// version it started with; it changes no entry.
		{id: "request_mem_stale", session: "session_mem_1", minutes: -5, turn: 1,
			context: memoryTestContext("fp1_rules_c", "fp1_index_a", "old.md")},
		// A running call's set derives nothing yet.
		{id: "request_mem_running", session: "session_mem_5", minutes: -1, turn: 1,
			context: memoryTestContext("fp1_rules_b", "fp1_index_c", "old.md", "no-jitter.md", "running.md"),
			edit: func(record *contract.RequestRecord) {
				record.Status, record.CompletedAt, record.LatencyMs = contract.RequestStatusPending, nil, nil
			}},
	}
	now := contextTestStart
	for _, item := range calls {
		record := contextTestRecord(item.id, contract.SessionID(item.session), item.minutes, item.context)
		record.Project = &project
		turn := item.turn
		record.TurnIndex = &turn
		if item.prompt != "" {
			prompt := item.prompt
			record.InputPreview = &prompt
		}
		if item.edit != nil {
			item.edit(&record)
		}
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatalf("%s: %v", item.id, err)
		}
	}
	// Codex calls in the project carry nothing to remember.
	codex := contextTestRecord("request_mem_codex", "session_mem_codex", -2*day, &contract.RequestContext{Parts: &contract.ContextParts{History: 100}})
	codex.ClientType = contract.ClientCodex
	if err := store.InsertRequestRecord(ctx, codex); err != nil {
		t.Fatal(err)
	}
	at := func(minutes float64) time.Time {
		return contextTestStart.Add(time.Duration(minutes * float64(time.Minute))).UTC()
	}
	options := storagecontract.ProjectMemoryOptions{Project: &project, DayStart: at(-60), Now: now}

	memory, err := store.GetProjectMemory(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	if memory.ClientType == nil || *memory.ClientType != contract.ClientClaudeCode || memory.Project == nil || *memory.Project != project ||
		!memory.HasMemory || !memory.HasRules || memory.Recent != 1 {
		t.Fatalf("view of %v %v, memory %v rules %v recent %d", memory.Project, memory.ClientType, memory.HasMemory, memory.HasRules, memory.Recent)
	}
	if len(memory.Entries) != 2 {
		t.Fatalf("entries = %+v", memory.Entries)
	}
	noted, old := memory.Entries[0], memory.Entries[1]
	if noted.File != "no-jitter.md" || noted.Title != "Title of no-jitter.md" || noted.NotedAt == nil || !noted.NotedAt.Equal(at(-3*day+5)) ||
		noted.Path != "~/.claude/projects/~-proj/memory/no-jitter.md" || noted.TotalReads != 1 || noted.Reads[12] != 1 {
		t.Fatalf("noted entry = %+v", noted)
	}
	if old.File != "old.md" || old.NotedAt != nil || old.TotalReads != 2 || old.Reads[13] != 2 {
		t.Fatalf("old entry = %+v", old)
	}
	if len(memory.Stories) != 1 {
		t.Fatalf("stories = %+v", memory.Stories)
	}
	story := memory.Stories[0]
	if story.File != "no-jitter.md" || story.Prompt != "Fix the failover tests" || story.RequestID == nil || *story.RequestID != "request_mem_write" ||
		story.SessionID == nil || *story.SessionID != "session_mem_2" || !story.At.Equal(at(-3*day+5)) {
		t.Fatalf("story = %+v", story)
	}
	if len(memory.Rules) != 1 {
		t.Fatalf("rules = %+v", memory.Rules)
	}
	rules := memory.Rules[0]
	// The newest main call is the stale session's, which carried rules c.
	if rules.Name != "AGENTS.md" || rules.ChangedAt == nil || !rules.ChangedAt.Equal(at(-5)) || rules.RequestID == nil ||
		*rules.RequestID != "request_mem_stale" || rules.FirstSeenAt == nil || !rules.FirstSeenAt.Equal(at(-20*day)) {
		t.Fatalf("rules = %+v", rules)
	}
	skills := memory.Toolbox.Skills
	if len(skills) != 3 || skills[0] != (contract.ToolboxSkill{Name: "humanizer", Description: "Rewrite text in plain words", Uses: 1, Carried: true}) ||
		skills[1].Name != "deploy" || skills[1].Uses != 0 || !skills[1].Carried {
		t.Fatalf("skills = %+v", skills)
	}
	if len(memory.Toolbox.MCP) != 1 || memory.Toolbox.MCP[0].Server != "paseo" ||
		memory.Toolbox.MCP[0].Tools[0] != (contract.ToolboxTool{Name: "create_agent", Uses: 2, Carried: true}) {
		t.Fatalf("mcp = %+v", memory.Toolbox.MCP)
	}
	// Two of three skills and two of three tools sit idle.
	if memory.Toolbox.IdleTokens != 300*2/3+3_000*2/3 {
		t.Fatalf("idle tokens = %d", memory.Toolbox.IdleTokens)
	}

	detail, err := store.GetProjectMemoryEntry(ctx, options, "no-jitter.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Events) != 2 ||
		detail.Events[0] != (contract.MemoryEvent{Kind: contract.MemoryEventNoted, At: at(-3*day + 5), RequestID: "request_mem_write",
			SessionID: detail.Events[0].SessionID, Prompt: "Fix the failover tests", Captured: true}) ||
		detail.Events[1].Kind != contract.MemoryEventRead || detail.Events[1].RequestID != "request_mem_read" || detail.Events[1].Prompt != "CI flakes again" {
		t.Fatalf("events = %+v", detail.Events)
	}
	// A subagent's read tells what the user said to the conversation that
	// started it.
	detail, err = store.GetProjectMemoryEntry(ctx, options, "OLD.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Events) != 2 || detail.Events[0].Prompt != "Check the old notes" || detail.Events[1].Prompt != "Check the old notes" {
		t.Fatalf("old events = %+v", detail.Events)
	}
	if _, err := store.GetProjectMemoryEntry(ctx, options, "running.md"); err != storagecontract.ErrNotFound {
		t.Fatalf("a running call's index listed an entry: %v", err)
	}

	codexClient := contract.ClientCodex
	options.ClientType = &codexClient
	plain, err := store.GetProjectMemory(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	if plain.HasMemory || plain.HasRules || len(plain.Entries) != 0 || *plain.ClientType != contract.ClientCodex {
		t.Fatalf("codex view = %+v", plain)
	}
}

func TestMemoryEntriesFollowTheIndexAndGoWithTheRecords(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "memory-entries.db"))
	defer store.Close()
	project := "~/proj"
	insert := func(id string, minutes float64, requestContext *contract.RequestContext) {
		t.Helper()
		record := contextTestRecord(id, contract.SessionID("session_"+id), minutes, requestContext)
		record.Project = &project
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	files := func() []string {
		t.Helper()
		rows, err := store.db.QueryContext(ctx, `SELECT file || CASE WHEN removed_at IS NULL THEN '' ELSE ' removed' END
    || CASE WHEN preexisting THEN ' preexisting' ELSE '' END || ' ' || title FROM memory_entries ORDER BY file`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var found []string
		for rows.Next() {
			var file string
			if err := rows.Scan(&file); err != nil {
				t.Fatal(err)
			}
			found = append(found, file)
		}
		return found
	}
	insert("request_entries_a", 0, memoryTestContext("fp1_rules", "fp1_index_a", "a.md", "b.md"))
	// A title the call may not show keeps the one kept before.
	hidden := memoryTestContext("fp1_rules", "fp1_index_b", "b.md", "c.md")
	hidden.MemoryIndex[0].Title = ""
	insert("request_entries_b", 10, hidden)
	if got := files(); !slices.Equal(got, []string{"a.md removed preexisting Title of a.md", "b.md preexisting Title of b.md", "c.md Title of c.md"}) {
		t.Fatalf("entries = %q", got)
	}
	// Listed again, an entry comes back.
	insert("request_entries_c", 20, memoryTestContext("fp1_rules", "fp1_index_c", "a.md", "b.md", "c.md"))
	if got := files(); !slices.Equal(got, []string{"a.md preexisting Title of a.md", "b.md preexisting Title of b.md", "c.md Title of c.md"}) {
		t.Fatalf("entries = %q", got)
	}
	insert("request_entries_d", 30, memoryTestContext("fp1_rules", "fp1_index_d", "b.md", "c.md"))
	var catalogs int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM context_catalogs`).Scan(&catalogs); err != nil || catalogs != 1 {
		t.Fatalf("%d catalogs, %v", catalogs, err)
	}

	// Once no call kept started before a.md was dropped, it goes; the
	// others stay with the calls that still carry them.
	for _, id := range []contract.RequestID{"request_entries_a", "request_entries_b", "request_entries_c"} {
		if err := store.DeleteRequestRecord(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if got := files(); !slices.Equal(got, []string{"b.md preexisting Title of b.md", "c.md Title of c.md"}) {
		t.Fatalf("entries after the deletes = %q", got)
	}
	if _, err := store.PurgeRequestRecords(ctx, contract.PurgeRequest{Confirm: true, Scope: contract.PurgeScopeAll}); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM memory_entries) + (SELECT COUNT(*) FROM context_catalogs)`).Scan(&catalogs); err != nil || catalogs != 0 {
		t.Fatalf("%d derived rows outlived the records, %v", catalogs, err)
	}
}
