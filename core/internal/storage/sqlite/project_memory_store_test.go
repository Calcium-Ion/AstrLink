package sqlite

import (
	"context"
	"fmt"
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
		skills[1].Name != "dataviz" || skills[1].Uses != 0 || !skills[1].Carried || skills[2].Name != "deploy" {
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

// toolboxTestStore holds Claude Code calls in ~/proj: a session that
// carried every skill and three paseo tools, loaded humanizer in two turns
// and made one paseo call, then a newer session whose newest call listed
// only the skills Claude Code listed again and the paseo tool it loaded.
func toolboxTestStore(t *testing.T) (*Store, func(id string, session string, minutes float64, turn int, requestContext *contract.RequestContext, edit func(*contract.RequestRecord))) {
	t.Helper()
	store := openTestStore(t, filepath.Join(t.TempDir(), "toolbox.db"))
	t.Cleanup(func() { store.Close() })
	project := "~/proj"
	insert := func(id string, session string, minutes float64, turn int, requestContext *contract.RequestContext, edit func(*contract.RequestRecord)) {
		t.Helper()
		record := contextTestRecord(id, contract.SessionID(session), minutes, requestContext)
		record.Project = &project
		record.TurnIndex = &turn
		if edit != nil {
			edit(&record)
		}
		if err := store.InsertRequestRecord(context.Background(), record); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	const day = 24 * 60
	full := memoryTestContext("fp1_rules_a", "fp1_index_a", "old.md")
	used := func(skill string) *contract.RequestContext {
		return continuing(full, func(requestContext *contract.RequestContext) { requestContext.SkillsUsed = []string{skill} })
	}
	insert("request_tb_full", "session_tb_1", -3*day, 1, full, nil)
	insert("request_tb_use_a", "session_tb_1", -3*day+1, 1, used("humanizer"), nil)
	insert("request_tb_use_a_again", "session_tb_1", -3*day+2, 1, used("humanizer"), nil)
	insert("request_tb_use_b", "session_tb_1", -3*day+5, 2, used("humanizer"), func(record *contract.RequestRecord) {
		record.Output = &contract.OutputActivity{Kind: contract.OutputKindTool, Tools: []string{"mcp__paseo__list_agents"}}
	})
	relisted := continuing(full, func(requestContext *contract.RequestContext) {
		requestContext.Items = []contract.ContextItem{
			{Kind: contract.ContextItemSkills, Count: ptrInt(2), Names: []string{"redaction-placeholders", "astrlink-debug"}, Tokens: 100, Hash: "fp1_skills_b"},
			{Kind: contract.ContextItemMCP, Count: ptrInt(1), Servers: []string{"paseo"}, Tokens: 1_000, Hash: "fp1_mcp_b"},
		}
		requestContext.Catalog = &contract.ContextCatalog{
			Skills: []contract.CatalogSkill{{Name: "redaction-placeholders", Description: "Copy placeholders"}, {Name: "astrlink-debug"}},
			MCP:    []contract.CatalogServer{{Server: "paseo", Tools: []string{"get_agent_status"}}},
		}
	})
	insert("request_tb_relisted", "session_tb_2", -10, 1, relisted, nil)
	return store, insert
}

func toolboxNames(toolbox contract.MemoryToolbox) (skills []string, tools []string) {
	for _, skill := range toolbox.Skills {
		skills = append(skills, skill.Name)
	}
	for _, server := range toolbox.MCP {
		for _, tool := range server.Tools {
			tools = append(tools, server.Server+"/"+tool.Name)
		}
	}
	return skills, tools
}

// The toolbox lists every skill and MCP tool a call of its days carried,
// whatever the newest call listed, and keeps listing them as calls that
// list fewer arrive.
func TestToolboxListsWhatItsDaysCarried(t *testing.T) {
	ctx := context.Background()
	store, insert := toolboxTestStore(t)
	project := "~/proj"
	options := storagecontract.ProjectMemoryOptions{Project: &project, DayStart: contextTestStart.Add(-time.Hour), Now: contextTestStart}
	memory, err := store.GetProjectMemory(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	toolbox := memory.Toolbox
	wantSkills := []string{"humanizer", "astrlink-debug", "dataviz", "deploy", "redaction-placeholders"}
	wantTools := []string{"paseo/list_agents", "paseo/create_agent", "paseo/get_agent_status", "paseo/send_agent_prompt"}
	skills, tools := toolboxNames(toolbox)
	if !slices.Equal(skills, wantSkills) || !slices.Equal(tools, wantTools) {
		t.Fatalf("toolbox lists skills %q and tools %q, want %q and %q", skills, tools, wantSkills, wantTools)
	}
	if toolbox.Skills[0] != (contract.ToolboxSkill{Name: "humanizer", Description: "Rewrite text in plain words", Uses: 2, Carried: true}) ||
		toolbox.Skills[4] != (contract.ToolboxSkill{Name: "redaction-placeholders", Description: "Copy placeholders", Carried: true}) ||
		toolbox.MCP[0].Tools[0] != (contract.ToolboxTool{Name: "list_agents", Uses: 1, Carried: true}) {
		t.Fatalf("toolbox = %+v", toolbox)
	}
	if want := options.DayStart.Add(-29 * 24 * time.Hour); !toolbox.Since.Equal(want) {
		t.Fatalf("since = %v, want %v", toolbox.Since, want)
	}
	// The newest call carries two skills and one tool, none used; the
	// other idle ones it no longer carries add nothing.
	if want := 100*2/2 + 1_000*1/1; toolbox.IdleTokens != want {
		t.Fatalf("idle tokens = %d, want %d", toolbox.IdleTokens, want)
	}

	// A newer call that lists one skill and no MCP tool changes neither
	// list nor any count.
	insert("request_tb_fewer", "session_tb_3", -5, 1, carriedContext("fp1_rules_c", "fp1_skills_c"), nil)
	again, err := store.GetProjectMemory(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	skills, tools = toolboxNames(again.Toolbox)
	if !slices.Equal(skills, wantSkills) || !slices.Equal(tools, wantTools) ||
		again.Toolbox.Skills[0].Uses != 2 || again.Toolbox.MCP[0].Tools[0].Uses != 1 {
		t.Fatalf("after a call that lists fewer, toolbox = %+v", again.Toolbox)
	}
}

// Every call of the toolbox's days counts, however many newer calls read
// memory; a use leaves the counts only when its day leaves those days.
func TestToolboxCountsEveryCallOfItsDays(t *testing.T) {
	ctx := context.Background()
	store, insert := toolboxTestStore(t)
	project := "~/proj"
	options := storagecontract.ProjectMemoryOptions{Project: &project, DayStart: contextTestStart.Add(-time.Hour), Now: contextTestStart}
	counts := func(options storagecontract.ProjectMemoryOptions) (skill, tool int) {
		t.Helper()
		memory, err := store.GetProjectMemory(ctx, options)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range memory.Toolbox.Skills {
			if entry.Name == "humanizer" {
				skill = entry.Uses
			}
		}
		for _, server := range memory.Toolbox.MCP {
			for _, entry := range server.Tools {
				if entry.Name == "list_agents" {
					tool = entry.Uses
				}
			}
		}
		return skill, tool
	}
	if skill, tool := counts(options); skill != 2 || tool != 1 {
		t.Fatalf("humanizer used %d times, list_agents %d, want 2 and 1", skill, tool)
	}
	reads := continuing(memoryTestContext("fp1_rules_a", "fp1_index_a", "old.md"), func(requestContext *contract.RequestContext) {
		requestContext.MemoryReads = []contract.MemoryRead{{Name: "old.md", Path: "~/.claude/projects/~-proj/memory/old.md"}}
	})
	for index := range memoryActivityScan {
		insert(fmt.Sprintf("request_tb_read_%05d", index), fmt.Sprintf("session_tb_read_%05d", index), -2*24*60+float64(index)/10, 1, reads, nil)
	}
	if skill, tool := counts(options); skill != 2 || tool != 1 {
		t.Fatalf("after %d newer memory reads, humanizer used %d times, list_agents %d, want 2 and 1", memoryActivityScan, skill, tool)
	}
	// 27 days on, the day of those uses has left the toolbox's days.
	later := options
	later.DayStart, later.Now = options.DayStart.Add(27*24*time.Hour), options.Now.Add(27*24*time.Hour)
	if skill, tool := counts(later); skill != 0 || tool != 0 {
		t.Fatalf("once their day left, humanizer used %d times, list_agents %d, want 0 and 0", skill, tool)
	}
}
