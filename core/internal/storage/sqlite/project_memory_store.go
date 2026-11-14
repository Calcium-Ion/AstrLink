package sqlite

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/migrate"
)

// A memory view tells what one client's agents keep in one project. The
// memories are the lines its memory index listed, kept once per file when a
// new version of the index arrived, and the memory files its calls show
// written since. Notes and reads come from the calls whose history shows a
// memory file written or read, and what the user said from the turns they
// belong to. The rules are those its newest main call carried; the skills
// and MCP tools those any of its calls in the toolbox's days carried or
// used. Counts are read from the records still kept; nothing else is stored
// for them.

const (
	memoryDay = 24 * time.Hour
	// memoryActivityScan bounds how many calls that wrote or read memory a
	// view reads. The toolbox counts every call in its days.
	memoryActivityScan = 4096
	// memoryTurnDepth bounds how far a subagent's call is followed up to the
	// conversation the user spoke in.
	memoryTurnDepth = 4
)

// GetProjectMemory reads what one client's agents keep in one project.
func (store *Store) GetProjectMemory(ctx context.Context, options storage.ProjectMemoryOptions) (contract.ProjectMemory, error) {
	memory := contract.ProjectMemory{
		Projects: []contract.MemoryProject{},
		DayStart: options.DayStart.UTC(),
		Entries:  []contract.MemoryEntry{},
		Stories:  []contract.MemoryStory{},
		Rules:    []contract.MemoryRules{},
		Toolbox: contract.MemoryToolbox{
			Since: toolboxSince(options.DayStart), Skills: []contract.ToolboxSkill{}, MCP: []contract.ToolboxServer{},
		},
	}
	projects, err := store.memoryProjects(ctx)
	if err != nil {
		return memory, err
	}
	memory.Projects = projects
	scope := pickMemoryScope(projects, options)
	if scope == nil {
		return memory, nil
	}
	memory.Project, memory.ClientType = scope.project, &scope.client
	view, err := store.readMemoryView(ctx, *scope, options)
	if err != nil {
		return memory, err
	}
	memory.Entries = view.entryList()
	recent := options.Now.Add(-contract.MemoryRecentDays * memoryDay)
	for _, entry := range memory.Entries {
		if entry.NotedAt != nil && entry.NotedAt.After(recent) {
			memory.Recent++
		}
	}
	if memory.Stories, err = store.memoryStories(ctx, view); err != nil {
		return memory, err
	}
	if memory.Rules, err = store.memoryRules(ctx, view); err != nil {
		return memory, err
	}
	if memory.Toolbox, err = store.memoryToolbox(ctx, view); err != nil {
		return memory, err
	}
	memory.HasMemory = len(memory.Entries) > 0 || view.index != nil
	memory.HasRules = len(memory.Rules) > 0
	return memory, nil
}

// GetProjectMemoryEntry reads one memory of a view and what happened to it:
// the note, then each turn that read it, with what the user said then.
func (store *Store) GetProjectMemoryEntry(ctx context.Context, options storage.ProjectMemoryOptions, file string) (contract.MemoryEntryDetail, error) {
	detail := contract.MemoryEntryDetail{Events: []contract.MemoryEvent{}}
	projects, err := store.memoryProjects(ctx)
	if err != nil {
		return detail, err
	}
	scope := pickMemoryScope(projects, options)
	if scope == nil {
		return detail, storage.ErrNotFound
	}
	view, err := store.readMemoryView(ctx, *scope, options)
	if err != nil {
		return detail, err
	}
	key := strings.ToLower(file)
	index := slices.IndexFunc(view.entryList(), func(entry contract.MemoryEntry) bool { return strings.ToLower(entry.File) == key })
	if index < 0 {
		return detail, storage.ErrNotFound
	}
	detail.Entry = view.entryList()[index]
	var calls []memoryCall
	if note := view.notes[key]; note != nil {
		calls = append(calls, note.call)
	}
	if log := view.reads[key]; log != nil {
		reads := log.calls()
		if room := contract.MaxMemoryEvents - len(calls); len(reads) > room {
			reads = reads[len(reads)-room:]
		}
		calls = append(calls, reads...)
	}
	prompts, err := store.turnPrompts(ctx, calls)
	if err != nil {
		return detail, err
	}
	for position, call := range calls {
		kind := contract.MemoryEventRead
		if position == 0 && view.notes[key] != nil {
			kind = contract.MemoryEventNoted
		}
		event := contract.MemoryEvent{
			Kind: kind, At: call.at, RequestID: contract.RequestID(call.id),
			Prompt: prompts[call.id], Captured: call.captured,
		}
		if call.session != "" {
			session := contract.SessionID(call.session)
			event.SessionID = &session
		}
		detail.Events = append(detail.Events, event)
	}
	slices.SortStableFunc(detail.Events, func(left, right contract.MemoryEvent) int { return left.At.Compare(right.At) })
	return detail, nil
}

// memoryScope is the project and client a view covers.
type memoryScope struct {
	project *string
	client  contract.ClientType
}

// pickMemoryScope picks the project asked for, else the most recent one,
// and the client asked for, else the one that called in it last. Nil when
// no call named the project.
func pickMemoryScope(projects []contract.MemoryProject, options storage.ProjectMemoryOptions) *memoryScope {
	var shown *contract.MemoryProject
	for index := range projects {
		project := &projects[index]
		switch {
		case options.NoProject:
			if project.Project == nil {
				shown = project
			}
		case options.Project != nil:
			if project.Project != nil && *project.Project == *options.Project {
				shown = project
			}
		case index == 0:
			shown = project
		}
	}
	if shown == nil {
		return nil
	}
	scope := &memoryScope{project: shown.Project}
	switch {
	case options.ClientType != nil:
		scope.client = *options.ClientType
	case len(shown.ClientTypes) > 0:
		scope.client = shown.ClientTypes[0]
	default:
		return nil
	}
	return scope
}

// memoryCall is a call a view tells of.
type memoryCall struct {
	id string
	// session is the call's conversation; parent the one that started it,
	// for a subagent's call.
	session  string
	parent   string
	turn     *int
	at       time.Time
	captured bool
}

// turnKey names the user turn a call belongs to; a call without a turn is
// its own.
func (call memoryCall) turnKey() string {
	if call.turn == nil {
		return "\x00" + call.id
	}
	return fmt.Sprintf("%s\x00%d", call.session, *call.turn)
}

// memoryNote is the earliest call whose history shows a memory written, and
// the title and hook its index line gave.
type memoryNote struct {
	call  memoryCall
	title string
	hook  string
	path  string
	// indexed is set once an index line named the memory; its title wins
	// over the file's.
	indexed bool
	indexAt time.Time
}

// memoryReadLog is the turns that read one memory file, each at its first
// call that shows the read.
type memoryReadLog struct {
	turns map[string]memoryCall
}

// calls lists the reads oldest first.
func (log *memoryReadLog) calls() []memoryCall {
	calls := make([]memoryCall, 0, len(log.turns))
	for _, call := range log.turns {
		calls = append(calls, call)
	}
	slices.SortFunc(calls, func(left, right memoryCall) int {
		if order := left.at.Compare(right.at); order != 0 {
			return order
		}
		return cmp.Compare(left.id, right.id)
	})
	return calls
}

// storedEntry is a memory_entries row still listed.
type storedEntry struct {
	file, title, hook string
	firstSeen         time.Time
	preexisting       bool
}

// memoryView is what a view reads once and tells in its parts.
type memoryView struct {
	scope    memoryScope
	dayStart time.Time
	now      time.Time
	// sets are the client's context sets in the project, by id.
	sets map[int64][]contract.ContextItem
	// carrying is its newest finished main call that sent its whole body.
	carrying *carryingCall
	// index is the memory index that call carried, if any.
	index   *contract.ContextItem
	entries []storedEntry
	// dropped holds the files a newer index no longer lists.
	dropped map[string]bool
	notes   map[string]*memoryNote
	reads   map[string]*memoryReadLog
}

func (store *Store) readMemoryView(ctx context.Context, scope memoryScope, options storage.ProjectMemoryOptions) (*memoryView, error) {
	view := &memoryView{
		scope: scope, dayStart: options.DayStart.UTC(), now: options.Now.UTC(),
		sets: map[int64][]contract.ContextItem{}, dropped: map[string]bool{},
		notes: map[string]*memoryNote{}, reads: map[string]*memoryReadLog{},
	}
	project, client := textOf(scope.project), string(scope.client)
	rows, err := store.db.QueryContext(ctx, `SELECT id, items_json FROM context_sets WHERE project = ? AND client_type = ?`, project, client)
	if err != nil {
		return nil, fmt.Errorf("read memory context sets: %w", err)
	}
	for rows.Next() {
		var id int64
		var encoded string
		if err := rows.Scan(&id, &encoded); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan memory context set: %w", err)
		}
		items, err := decodeContextItems(encoded)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("decode memory context set: %w", err)
		}
		view.sets[id] = items
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close memory context sets: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memory context sets: %w", err)
	}
	if view.carrying, err = store.newestCarryingCall(ctx, scope.project, scope.client); err != nil {
		return nil, err
	}
	if view.carrying != nil {
		view.index = memoryIndexItem(view.carrying.context.Items)
	}
	if err := store.readMemoryEntries(ctx, view, project, client); err != nil {
		return nil, err
	}
	if err := store.readMemoryActivity(ctx, view, project, client); err != nil {
		return nil, err
	}
	return view, nil
}

func (store *Store) readMemoryEntries(ctx context.Context, view *memoryView, project, client string) error {
	rows, err := store.db.QueryContext(ctx, `SELECT file, title, hook, first_seen_at, preexisting, removed_at IS NOT NULL
FROM memory_entries WHERE project = ? AND client_type = ?`, project, client)
	if err != nil {
		return fmt.Errorf("read memory entries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var entry storedEntry
		var seen string
		var removed bool
		if err := rows.Scan(&entry.file, &entry.title, &entry.hook, &seen, &entry.preexisting, &removed); err != nil {
			return fmt.Errorf("scan memory entry: %w", err)
		}
		if removed {
			view.dropped[strings.ToLower(entry.file)] = true
			continue
		}
		if entry.firstSeen, err = time.Parse(time.RFC3339Nano, seen); err != nil {
			return fmt.Errorf("parse memory entry time: %w", err)
		}
		view.entries = append(view.entries, entry)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate memory entries: %w", err)
	}
	return nil
}

// readMemoryActivity reads the calls whose history shows a memory file
// written or read: the earliest note of each memory and the turns that read
// each.
func (store *Store) readMemoryActivity(ctx context.Context, view *memoryView, project, client string) error {
	rows, err := store.db.QueryContext(ctx, `SELECT id, COALESCE(session_id, id), COALESCE(parent_session_id, ''), turn_index, started_at,
    json_extract(context_json, '$.memory_writes'), json_extract(context_json, '$.memory_reads'),
    COALESCE(json_extract(audit_json, '$.request_body_captured'), 0)
FROM request_records
WHERE context_set_id IN (SELECT id FROM context_sets WHERE project = ? AND client_type = ?)
    AND `+migrate.ContextActivityFilter+`
ORDER BY started_at DESC LIMIT ?`, project, client, memoryActivityScan)
	if err != nil {
		return fmt.Errorf("read memory activity: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var call memoryCall
		var turn sql.NullInt64
		var started string
		var writes, reads sql.NullString
		if err := rows.Scan(&call.id, &call.session, &call.parent, &turn, &started, &writes, &reads, &call.captured); err != nil {
			return fmt.Errorf("scan memory activity: %w", err)
		}
		if call.at, err = time.Parse(time.RFC3339Nano, started); err != nil {
			return fmt.Errorf("parse memory activity time: %w", err)
		}
		if turn.Valid {
			value := int(turn.Int64)
			call.turn = &value
		}
		if writes.Valid {
			var list []contract.MemoryWrite
			if err := json.Unmarshal([]byte(writes.String), &list); err != nil {
				return fmt.Errorf("decode memory writes: %w", err)
			}
			for _, write := range list {
				view.noteWrite(call, write)
			}
		}
		if reads.Valid {
			var list []contract.MemoryRead
			if err := json.Unmarshal([]byte(reads.String), &list); err != nil {
				return fmt.Errorf("decode memory reads: %w", err)
			}
			for _, read := range list {
				key := strings.ToLower(read.Name)
				log := view.reads[key]
				if log == nil {
					log = &memoryReadLog{turns: map[string]memoryCall{}}
					view.reads[key] = log
				}
				// Newest first, so the last call kept for a turn is its first.
				log.turns[call.turnKey()] = call
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate memory activity: %w", err)
	}
	return nil
}

// noteWrite keeps a memory write as the memory's note when it is the
// earliest seen: the file an index line links, or a new memory file.
func (view *memoryView) noteWrite(call memoryCall, write contract.MemoryWrite) {
	if write.Kind != contract.ContextItemMemory {
		return
	}
	file, filePath := write.Entry, ""
	if file == "" {
		if strings.EqualFold(write.Name, "MEMORY.md") {
			return
		}
		file, filePath = write.Name, write.Path
	} else if write.Path != "" {
		filePath = siblingPath(write.Path, file)
	}
	key := strings.ToLower(file)
	note := view.notes[key]
	if note == nil {
		note = &memoryNote{}
		view.notes[key] = note
	}
	// Calls arrive newest first, so each earlier one replaces the note.
	if note.call.id == "" || !call.at.After(note.call.at) {
		note.call = call
	}
	if note.path == "" {
		note.path = filePath
	}
	switch {
	case write.Entry != "" && (!note.indexed || !call.at.After(note.indexAt)):
		note.title, note.hook, note.indexed, note.indexAt = write.Title, write.Hook, true, call.at
	case !note.indexed && write.Title != "":
		note.title = write.Title
	}
}

// siblingPath is the path of file beside filePath, in its separators.
func siblingPath(filePath, file string) string {
	at := strings.LastIndexAny(filePath, `/\`)
	if at < 0 {
		return ""
	}
	return filePath[:at+1] + file
}

// entryList is the memories of the view: the lines the index lists, then
// the memory files written that no index listed yet. The ones noted in the
// last week come first, the newest first, then the most read.
func (view *memoryView) entryList() []contract.MemoryEntry {
	entries := make([]contract.MemoryEntry, 0, len(view.entries)+len(view.notes))
	listed := map[string]bool{}
	indexPath := ""
	if view.index != nil {
		indexPath = view.index.Path
	}
	for _, stored := range view.entries {
		key := strings.ToLower(stored.file)
		listed[key] = true
		entry := contract.MemoryEntry{File: stored.file, Title: stored.title, Hook: stored.hook}
		if indexPath != "" {
			entry.Path = siblingPath(indexPath, stored.file)
		}
		note := view.notes[key]
		switch {
		case note != nil:
			at := note.call.at
			entry.NotedAt = &at
			if entry.Path == "" {
				entry.Path = note.path
			}
		case !stored.preexisting:
			at := stored.firstSeen
			entry.NotedAt = &at
		}
		entries = append(entries, view.withReads(entry))
	}
	for key, note := range view.notes {
		if listed[key] || view.dropped[key] {
			continue
		}
		at := note.call.at
		entry := contract.MemoryEntry{
			File: contract.ClampRunes(note.fileName(key), contract.MaxMemoryEntryFileRunes),
			Path: note.path, Title: note.title, Hook: note.hook, NotedAt: &at,
		}
		entries = append(entries, view.withReads(entry))
	}
	recent := view.now.Add(-contract.MemoryRecentDays * memoryDay)
	isRecent := func(entry contract.MemoryEntry) bool { return entry.NotedAt != nil && entry.NotedAt.After(recent) }
	slices.SortFunc(entries, func(left, right contract.MemoryEntry) int {
		leftRecent, rightRecent := isRecent(left), isRecent(right)
		switch {
		case leftRecent && !rightRecent:
			return -1
		case rightRecent && !leftRecent:
			return 1
		case leftRecent:
			if order := right.NotedAt.Compare(*left.NotedAt); order != 0 {
				return order
			}
		}
		if order := cmp.Compare(right.TotalReads, left.TotalReads); order != 0 {
			return order
		}
		switch {
		case left.NotedAt != nil && right.NotedAt != nil:
			if order := right.NotedAt.Compare(*left.NotedAt); order != 0 {
				return order
			}
		case left.NotedAt != nil:
			return -1
		case right.NotedAt != nil:
			return 1
		}
		return cmp.Compare(strings.ToLower(left.File), strings.ToLower(right.File))
	})
	return entries
}

// fileName is the file a note names, as its writes spelled it.
func (note *memoryNote) fileName(key string) string {
	if note.path != "" {
		at := strings.LastIndexAny(note.path, `/\`)
		if name := note.path[at+1:]; strings.EqualFold(name, key) {
			return name
		}
	}
	return key
}

// withReads counts the turns that read the entry's file: per day over the
// view's days, and over the records kept.
func (view *memoryView) withReads(entry contract.MemoryEntry) contract.MemoryEntry {
	entry.Reads = make([]int, contract.MemoryDays)
	log := view.reads[strings.ToLower(entry.File)]
	if log == nil {
		return entry
	}
	first := view.dayStart.Add(-(contract.MemoryDays - 1) * memoryDay)
	for _, call := range log.turns {
		entry.TotalReads++
		if call.at.Before(first) {
			continue
		}
		day := min(int(call.at.Sub(first)/memoryDay), contract.MemoryDays-1)
		entry.Reads[day]++
	}
	return entry
}

// memoryStories are the newest memories noted, each with what the user
// said in the turn that wrote it. A memory known only from the index has
// no turn to tell.
func (store *Store) memoryStories(ctx context.Context, view *memoryView) ([]contract.MemoryStory, error) {
	type story struct {
		story contract.MemoryStory
		call  *memoryCall
	}
	var stories []story
	for _, entry := range view.entryList() {
		if entry.NotedAt == nil {
			continue
		}
		found := story{story: contract.MemoryStory{File: entry.File, Title: entry.Title, Hook: entry.Hook, At: *entry.NotedAt}}
		if note := view.notes[strings.ToLower(entry.File)]; note != nil {
			call := note.call
			found.call = &call
		}
		stories = append(stories, found)
	}
	slices.SortStableFunc(stories, func(left, right story) int { return right.story.At.Compare(left.story.At) })
	if len(stories) > contract.MaxMemoryStories {
		stories = stories[:contract.MaxMemoryStories]
	}
	var calls []memoryCall
	for _, found := range stories {
		if found.call != nil {
			calls = append(calls, *found.call)
		}
	}
	prompts, err := store.turnPrompts(ctx, calls)
	if err != nil {
		return nil, err
	}
	told := make([]contract.MemoryStory, 0, len(stories))
	for _, found := range stories {
		if call := found.call; call != nil {
			id := contract.RequestID(call.id)
			session := contract.SessionID(call.session)
			found.story.RequestID, found.story.SessionID, found.story.Prompt = &id, &session, prompts[call.id]
		}
		told = append(told, found.story)
	}
	return told, nil
}

// turnPrompts finds what the user said to open each call's turn: the input
// preview of the turn's first call. A subagent's call belongs to the turn
// of the conversation that started it, at the time it ran.
func (store *Store) turnPrompts(ctx context.Context, calls []memoryCall) (map[string]string, error) {
	prompts := map[string]string{}
	found := map[string]string{}
	for _, call := range calls {
		session, turn, parent := call.session, call.turn, call.parent
		started := call.at.UTC().Format(time.RFC3339Nano)
		for depth := 0; parent != "" && depth < memoryTurnDepth; depth++ {
			var next sql.NullString
			var nextTurn sql.NullInt64
			var nextSession string
			err := store.db.QueryRowContext(ctx, `SELECT COALESCE(session_id, id), turn_index, parent_session_id FROM request_records
WHERE COALESCE(session_id, id) = ? AND parent_request_id IS NULL AND started_at <= ?
ORDER BY started_at DESC, id DESC LIMIT 1`, parent, started).Scan(&nextSession, &nextTurn, &next)
			if err == sql.ErrNoRows {
				turn = nil
				break
			}
			if err != nil {
				return nil, fmt.Errorf("read parent turn: %w", err)
			}
			session, parent = nextSession, next.String
			turn = nil
			if nextTurn.Valid {
				value := int(nextTurn.Int64)
				turn = &value
			}
		}
		if turn == nil {
			continue
		}
		key := fmt.Sprintf("%s\x00%d\x00%s", session, *turn, started)
		if prompt, ok := found[key]; ok {
			prompts[call.id] = prompt
			continue
		}
		var prompt sql.NullString
		err := store.db.QueryRowContext(ctx, `SELECT input_preview FROM request_records
WHERE COALESCE(session_id, id) = ? AND parent_request_id IS NULL AND turn_index = ? AND started_at <= ?
    AND input_preview IS NOT NULL AND input_preview <> ''
ORDER BY started_at DESC, id DESC LIMIT 1`, session, *turn, started).Scan(&prompt)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("read turn prompt: %w", err)
		}
		found[key] = prompt.String
		prompts[call.id] = prompt.String
	}
	return prompts, nil
}

// memoryRules are the rules files the newest main call carried, each with
// when the calls first carried it and its latest change.
func (store *Store) memoryRules(ctx context.Context, view *memoryView) ([]contract.MemoryRules, error) {
	rules := []contract.MemoryRules{}
	if view.carrying == nil {
		return rules, nil
	}
	history, err := store.contextHistory(ctx, view.scope.project, view.scope.client)
	if err != nil {
		return nil, err
	}
	for _, item := range view.carrying.context.Items {
		if item.Kind != contract.ContextItemRules {
			continue
		}
		file := contract.MemoryRules{Name: item.Name, Path: item.Path, Scope: item.Scope}
		versions := history[versionKey(item.Kind, item.Path, item.Name)]
		if len(versions) > 0 {
			first := versions[0].firstSeen
			file.FirstSeenAt = &first
		}
		current := slices.IndexFunc(versions, func(version contextVersion) bool { return version.hash == item.Hash })
		if current > 0 {
			changed := versions[current].firstSeen
			after := contract.RequestID(versions[current].firstRequestID)
			before := contract.RequestID(versions[current-1].lastRequestID)
			file.ChangedAt, file.RequestID, file.BeforeRequestID = &changed, &after, &before
		}
		rules = append(rules, file)
	}
	return rules, store.settleRuleCalls(ctx, rules)
}

// settleRuleCalls fills in the conversation of the call that first carried
// each file's current version, and whether both calls' bodies were
// captured.
func (store *Store) settleRuleCalls(ctx context.Context, rules []contract.MemoryRules) error {
	var args []any
	for _, file := range rules {
		if file.RequestID != nil {
			args = append(args, string(*file.RequestID), string(*file.BeforeRequestID))
		}
	}
	if len(args) == 0 {
		return nil
	}
	rows, err := store.db.QueryContext(ctx, `SELECT id, COALESCE(session_id, id), COALESCE(json_extract(audit_json, '$.request_body_captured'), 0)
FROM request_records WHERE id IN (`+sessionPlaceholders(len(args))+`)`, args...)
	if err != nil {
		return fmt.Errorf("read rules calls: %w", err)
	}
	defer rows.Close()
	type call struct {
		session  contract.SessionID
		captured bool
	}
	calls := map[contract.RequestID]call{}
	for rows.Next() {
		var id, session string
		var captured bool
		if err := rows.Scan(&id, &session, &captured); err != nil {
			return fmt.Errorf("scan rules call: %w", err)
		}
		calls[contract.RequestID(id)] = call{session: contract.SessionID(session), captured: captured}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate rules calls: %w", err)
	}
	for index := range rules {
		file := &rules[index]
		if file.RequestID == nil {
			continue
		}
		after, ok := calls[*file.RequestID]
		if !ok {
			file.RequestID, file.BeforeRequestID = nil, nil
			continue
		}
		before := calls[*file.BeforeRequestID]
		file.SessionID = &after.session
		file.Captured = after.captured && before.captured
	}
	return nil
}

// toolboxSince is when the toolbox's days begin: MemoryToolboxDays days
// that end with the day of dayStart.
func toolboxSince(dayStart time.Time) time.Time {
	return dayStart.UTC().Add(-(contract.MemoryToolboxDays - 1) * memoryDay)
}

// memoryToolbox is the skills and MCP tools the client's calls carried or
// used in the toolbox's days, and how often they used each: a skill once
// per turn that loaded it, an MCP tool once per call that made it. Every
// call of those days counts, so within the same days the lists only grow
// and no count drops; a call leaves them only when its day leaves the
// toolbox's days.
func (store *Store) memoryToolbox(ctx context.Context, view *memoryView) (contract.MemoryToolbox, error) {
	since := toolboxSince(view.dayStart)
	toolbox := contract.MemoryToolbox{Since: since, Skills: []contract.ToolboxSkill{}, MCP: []contract.ToolboxServer{}}
	carried, err := store.toolboxCarried(ctx, view, since)
	if err != nil {
		return toolbox, err
	}
	skillUses, err := store.skillUses(ctx, view, since)
	if err != nil {
		return toolbox, err
	}
	uses, err := store.mcpUses(ctx, view, since)
	if err != nil {
		return toolbox, err
	}

	// Skills: those the calls carried, then any used that none carried.
	for _, name := range carried.skills {
		toolbox.Skills = append(toolbox.Skills, contract.ToolboxSkill{
			Name: name, Description: carried.described[name], Uses: len(skillUses[name]), Carried: true,
		})
	}
	for name, turns := range skillUses {
		if !slices.Contains(carried.skills, name) {
			toolbox.Skills = append(toolbox.Skills, contract.ToolboxSkill{Name: name, Uses: len(turns)})
		}
	}
	slices.SortFunc(toolbox.Skills, func(left, right contract.ToolboxSkill) int {
		return cmp.Or(cmp.Compare(right.Uses, left.Uses), cmp.Compare(left.Name, right.Name))
	})

	// MCP: the servers the calls carried, by name, then those only used.
	owner := map[string]string{}
	for _, server := range carried.servers {
		for _, tool := range carried.tools[server] {
			if _, taken := owner[tool]; !taken {
				owner[tool] = server
			}
		}
	}
	counted := map[string]map[string]int{}
	for name, count := range uses {
		server, tool := mcpToolOf(name)
		if server == "" {
			if server = owner[name]; server == "" {
				continue
			}
			tool = name
		}
		if counted[server] == nil {
			counted[server] = map[string]int{}
		}
		counted[server][tool] += count
	}
	idleTools := 0
	for _, server := range carried.servers {
		entry := contract.ToolboxServer{Server: server, Tools: []contract.ToolboxTool{}}
		for _, tool := range carried.tools[server] {
			count := counted[server][tool]
			entry.Tools = append(entry.Tools, contract.ToolboxTool{Name: tool, Uses: count, Carried: true})
			if count == 0 {
				idleTools++
			}
		}
		for tool, count := range counted[server] {
			if !slices.Contains(carried.tools[server], tool) {
				entry.Tools = append(entry.Tools, contract.ToolboxTool{Name: tool, Uses: count})
			}
		}
		delete(counted, server)
		sortToolboxTools(entry.Tools)
		toolbox.MCP = append(toolbox.MCP, entry)
	}
	var gone []string
	for server := range counted {
		gone = append(gone, server)
	}
	slices.Sort(gone)
	for _, server := range gone {
		entry := contract.ToolboxServer{Server: server, Tools: []contract.ToolboxTool{}}
		for tool, count := range counted[server] {
			entry.Tools = append(entry.Tools, contract.ToolboxTool{Name: tool, Uses: count})
		}
		sortToolboxTools(entry.Tools)
		toolbox.MCP = append(toolbox.MCP, entry)
	}

	// What the idle ones still carried cost each call: each its share of
	// what the newest main call's catalog and MCP tools carry. One that call
	// no longer carries costs nothing.
	now := view.carriedNow()
	idleSkills, idleTools := 0, 0
	for _, skill := range toolbox.Skills {
		if skill.Uses == 0 && now.skills[skill.Name] {
			idleSkills++
		}
	}
	for _, server := range toolbox.MCP {
		for _, tool := range server.Tools {
			if tool.Uses == 0 && now.tools[server.Server+"\x00"+tool.Name] {
				idleTools++
			}
		}
	}
	if len(now.skills) > 0 {
		toolbox.IdleTokens += now.skillTokens * idleSkills / len(now.skills)
	}
	if len(now.tools) > 0 {
		toolbox.IdleTokens += now.mcpTokens * idleTools / len(now.tools)
	}
	return toolbox, nil
}

// carriedNow is what the newest main call carried: the skills its catalog
// lists and the MCP tools, each server's by server and name, with what
// each kind carried in tokens.
type carriedNow struct {
	skills      map[string]bool
	tools       map[string]bool
	skillTokens int
	mcpTokens   int
}

func (view *memoryView) carriedNow() carriedNow {
	now := carriedNow{skills: map[string]bool{}, tools: map[string]bool{}}
	call := view.carrying
	if call == nil {
		return now
	}
	for _, item := range view.sets[call.setID] {
		if item.Kind == contract.ContextItemSkills {
			for _, name := range item.Names {
				now.skills[name] = true
			}
		}
	}
	var catalog contract.ContextCatalog
	if call.catalog != "" && json.Unmarshal([]byte(call.catalog), &catalog) == nil {
		if len(now.skills) == 0 {
			for _, skill := range catalog.Skills {
				now.skills[skill.Name] = true
			}
		}
		for _, server := range catalog.MCP {
			for _, tool := range server.Tools {
				now.tools[server.Server+"\x00"+tool] = true
			}
		}
	}
	for _, item := range call.context.Items {
		switch item.Kind {
		case contract.ContextItemSkills:
			now.skillTokens = item.Tokens
		case contract.ContextItemMCP:
			now.mcpTokens = item.Tokens
		}
	}
	return now
}

// toolboxCatalog is the skills and MCP tools the calls carried.
type toolboxCatalog struct {
	// skills are by name; described holds what the newest catalog that
	// describes each says of it.
	skills    []string
	described map[string]string
	// servers are by name; tools holds each server's tools.
	servers []string
	tools   map[string][]string
}

// toolboxCarried reads the skills and MCP tools the client's calls carried
// since the toolbox's days began: every set a call of those days carried,
// the newest first, with its catalog.
func (store *Store) toolboxCarried(ctx context.Context, view *memoryView, since time.Time) (toolboxCatalog, error) {
	carried := toolboxCatalog{described: map[string]string{}, tools: map[string][]string{}}
	rows, err := store.db.QueryContext(ctx, `SELECT id, catalog FROM (
    SELECT s.id, COALESCE(c.catalog_json, '') AS catalog,
        (SELECT MAX(started_at) FROM request_records WHERE context_set_id = s.id) AS last_at
    FROM context_sets s LEFT JOIN context_catalogs c ON c.id = s.catalog_id
    WHERE s.project = ? AND s.client_type = ?)
WHERE last_at >= ?
ORDER BY last_at DESC, id DESC`, textOf(view.scope.project), string(view.scope.client), since.Format(time.RFC3339Nano))
	if err != nil {
		return carried, fmt.Errorf("read toolbox sets: %w", err)
	}
	defer rows.Close()
	skills := map[string]bool{}
	servers := map[string]bool{}
	addTool := func(server, tool string) {
		if !servers[server] {
			servers[server] = true
			carried.servers = append(carried.servers, server)
		}
		if tool != "" && !slices.Contains(carried.tools[server], tool) {
			carried.tools[server] = append(carried.tools[server], tool)
		}
	}
	for rows.Next() {
		var id int64
		var encoded string
		if err := rows.Scan(&id, &encoded); err != nil {
			return carried, fmt.Errorf("scan toolbox set: %w", err)
		}
		var catalog contract.ContextCatalog
		if encoded != "" {
			if err := json.Unmarshal([]byte(encoded), &catalog); err != nil {
				return carried, fmt.Errorf("decode context catalog: %w", err)
			}
		}
		for _, item := range view.sets[id] {
			switch item.Kind {
			case contract.ContextItemSkills:
				for _, name := range item.Names {
					skills[name] = true
				}
			case contract.ContextItemMCP:
				for _, server := range item.Servers {
					addTool(server, "")
				}
			}
		}
		for _, skill := range catalog.Skills {
			skills[skill.Name] = true
			if _, ok := carried.described[skill.Name]; !ok && skill.Description != "" {
				carried.described[skill.Name] = skill.Description
			}
		}
		for _, server := range catalog.MCP {
			addTool(server.Server, "")
			for _, tool := range server.Tools {
				addTool(server.Server, tool)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return carried, fmt.Errorf("iterate toolbox sets: %w", err)
	}
	for name := range skills {
		carried.skills = append(carried.skills, name)
	}
	slices.Sort(carried.skills)
	slices.Sort(carried.servers)
	return carried, nil
}

// skillUses reads, per skill, the turns whose calls loaded it since the
// toolbox's days began.
func (store *Store) skillUses(ctx context.Context, view *memoryView, since time.Time) (map[string]map[string]bool, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id, COALESCE(session_id, id), turn_index, json_extract(context_json, '$.skills_used')
FROM request_records
WHERE context_set_id IN (SELECT id FROM context_sets WHERE project = ? AND client_type = ?)
    AND `+migrate.ContextActivityFilter+`
    AND started_at >= ? AND json_type(context_json, '$.skills_used') IS NOT NULL`,
		textOf(view.scope.project), string(view.scope.client), since.Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("read skill uses: %w", err)
	}
	defer rows.Close()
	uses := map[string]map[string]bool{}
	for rows.Next() {
		var call memoryCall
		var turn sql.NullInt64
		var encoded string
		if err := rows.Scan(&call.id, &call.session, &turn, &encoded); err != nil {
			return nil, fmt.Errorf("scan skill uses: %w", err)
		}
		if turn.Valid {
			value := int(turn.Int64)
			call.turn = &value
		}
		var names []string
		if err := json.Unmarshal([]byte(encoded), &names); err != nil {
			return nil, fmt.Errorf("decode skills used: %w", err)
		}
		for _, name := range names {
			if uses[name] == nil {
				uses[name] = map[string]bool{}
			}
			uses[name][call.turnKey()] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate skill uses: %w", err)
	}
	return uses, nil
}

func sortToolboxTools(tools []contract.ToolboxTool) {
	slices.SortFunc(tools, func(left, right contract.ToolboxTool) int {
		return cmp.Or(cmp.Compare(right.Uses, left.Uses), cmp.Compare(left.Name, right.Name))
	})
}

// mcpToolOf reads mcp__<server>__<tool>; empty for any other name.
func mcpToolOf(name string) (server, tool string) {
	rest, ok := strings.CutPrefix(name, "mcp__")
	if !ok {
		return "", ""
	}
	server, tool, ok = strings.Cut(rest, "__")
	if !ok || server == "" || tool == "" {
		return "", ""
	}
	return server, tool
}

// mcpUses counts the tool calls the client's main and subagent calls made
// since the toolbox's days began, by the name each response gave.
func (store *Store) mcpUses(ctx context.Context, view *memoryView, since time.Time) (map[string]int, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT json_extract(output_json, '$.tools') FROM request_records
WHERE context_set_id IN (SELECT id FROM context_sets WHERE project = ? AND client_type = ?)
    AND started_at >= ? AND json_type(output_json, '$.tools') IS NOT NULL`,
		textOf(view.scope.project), string(view.scope.client), since.Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("read tool uses: %w", err)
	}
	defer rows.Close()
	uses := map[string]int{}
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return nil, fmt.Errorf("scan tool uses: %w", err)
		}
		var names []string
		if err := json.Unmarshal([]byte(encoded), &names); err != nil {
			return nil, fmt.Errorf("decode tool uses: %w", err)
		}
		for _, name := range names {
			uses[name]++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tool uses: %w", err)
	}
	return uses, nil
}

func versionKey(kind contract.ContextItemKind, path, name string) string {
	return string(kind) + "\x00" + path + "\x00" + name
}

// memoryProjects lists the projects the calls named, each with the clients
// whose calls named it, the most recent first.
func (store *Store) memoryProjects(ctx context.Context) ([]contract.MemoryProject, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT project, client_type,
    (SELECT MAX(started_at) FROM request_records WHERE context_set_id = context_sets.id)
FROM context_sets`)
	if err != nil {
		return nil, fmt.Errorf("list memory projects: %w", err)
	}
	defer rows.Close()
	type seen struct {
		client contract.ClientType
		at     time.Time
	}
	byProject := map[string]*contract.MemoryProject{}
	clients := map[string]map[contract.ClientType]time.Time{}
	for rows.Next() {
		var project, client string
		var last sql.NullString
		if err := rows.Scan(&project, &client, &last); err != nil {
			return nil, fmt.Errorf("scan memory project: %w", err)
		}
		if !last.Valid {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, last.String)
		if err != nil {
			return nil, fmt.Errorf("parse memory project time: %w", err)
		}
		entry := byProject[project]
		if entry == nil {
			entry = &contract.MemoryProject{}
			if project != "" {
				value := project
				entry.Project = &value
			}
			byProject[project] = entry
			clients[project] = map[contract.ClientType]time.Time{}
		}
		if at.After(entry.LastSeenAt) {
			entry.LastSeenAt = at
		}
		if at.After(clients[project][contract.ClientType(client)]) {
			clients[project][contract.ClientType(client)] = at
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memory projects: %w", err)
	}
	projects := make([]contract.MemoryProject, 0, len(byProject))
	for key, entry := range byProject {
		list := make([]seen, 0, len(clients[key]))
		for client, at := range clients[key] {
			list = append(list, seen{client: client, at: at})
		}
		slices.SortFunc(list, func(left, right seen) int {
			if order := right.at.Compare(left.at); order != 0 {
				return order
			}
			return cmp.Compare(left.client, right.client)
		})
		for _, client := range list {
			entry.ClientTypes = append(entry.ClientTypes, client.client)
		}
		projects = append(projects, *entry)
	}
	slices.SortFunc(projects, func(left, right contract.MemoryProject) int {
		if order := right.LastSeenAt.Compare(left.LastSeenAt); order != 0 {
			return order
		}
		return cmp.Compare(textOf(left.Project), textOf(right.Project))
	})
	return projects, nil
}

func textOf(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

type carryingCall struct {
	setID   int64
	started time.Time
	context contract.RequestContext
	// catalog is the set's catalog, encoded; empty when it has none.
	catalog string
}

// newestCarryingCall is a client's newest finished main call in a project
// that sent its whole body, so it shows everything the calls carry. Helper
// calls and subagents are left out. Each set gives its newest such call.
func (store *Store) newestCarryingCall(ctx context.Context, project *string, client contract.ClientType) (*carryingCall, error) {
	var started, encoded, usageJSON, items, catalog string
	call := &carryingCall{}
	err := store.db.QueryRowContext(ctx, `SELECT s.id, r.started_at, r.context_json, COALESCE(r.usage_json, ''), s.items_json,
    COALESCE((SELECT catalog_json FROM context_catalogs WHERE id = s.catalog_id), '')
FROM context_sets s
JOIN request_records r ON r.rowid = (
    SELECT rowid FROM request_records
    WHERE context_set_id = s.id AND parent_session_id IS NULL AND status <> 'pending'
        AND json_type(context_json, '$.parts') IS NOT NULL AND json_extract(context_json, '$.helper') IS NULL
    ORDER BY started_at DESC LIMIT 1)
WHERE s.project = ? AND s.client_type = ?
ORDER BY r.started_at DESC LIMIT 1`, textOf(project), string(client)).Scan(&call.setID, &started, &encoded, &usageJSON, &items, &catalog)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read newest carrying call: %w", err)
	}
	call.catalog = catalog
	if call.started, err = time.Parse(time.RFC3339Nano, started); err != nil {
		return nil, fmt.Errorf("parse carrying call time: %w", err)
	}
	carried, err := decodeContextItems(items)
	if err != nil {
		return nil, fmt.Errorf("decode carrying call items: %w", err)
	}
	assembled, err := assembleContext(encoded, carried, decodeStoredUsage(usageJSON))
	if err != nil {
		return nil, fmt.Errorf("decode carrying call context: %w", err)
	}
	call.context = *assembled
	return call, nil
}

// decodeStoredUsage reads a record's usage; nil when it has none or it does
// not decode.
func decodeStoredUsage(encoded string) *contract.Usage {
	if encoded == "" {
		return nil
	}
	var usage contract.Usage
	if json.Unmarshal([]byte(encoded), &usage) != nil {
		return nil
	}
	return &usage
}

// contextVersion is one version of a carried item and the first and last
// finished calls that carried it.
type contextVersion struct {
	hash           string
	firstRequestID string
	firstSeen      time.Time
	lastRequestID  string
	lastSeen       time.Time
}

// contextHistory reads the sets a client's finished calls carried in a
// project, each with the first and last finished call that carried it, and
// dates each version of an item by the earliest and latest of those calls,
// by item and oldest first. Items without a hash cannot be told apart from
// their other versions and are left out.
func (store *Store) contextHistory(ctx context.Context, project *string, client contract.ClientType) (map[string][]contextVersion, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT s.items_json, earliest.id, earliest.started_at, latest.id, latest.started_at
FROM context_sets s
JOIN request_records earliest ON earliest.rowid = (
    SELECT rowid FROM request_records WHERE context_set_id = s.id AND status <> 'pending'
    ORDER BY started_at LIMIT 1)
JOIN request_records latest ON latest.rowid = (
    SELECT rowid FROM request_records WHERE context_set_id = s.id AND status <> 'pending'
    ORDER BY started_at DESC LIMIT 1)
WHERE s.project = ? AND s.client_type = ?`, textOf(project), string(client))
	if err != nil {
		return nil, fmt.Errorf("read context sets: %w", err)
	}
	defer rows.Close()
	found := map[string]*contextVersion{}
	for rows.Next() {
		var items, firstID, firstStarted, lastID, lastStarted string
		if err := rows.Scan(&items, &firstID, &firstStarted, &lastID, &lastStarted); err != nil {
			return nil, fmt.Errorf("scan context set: %w", err)
		}
		carried, err := decodeContextItems(items)
		if err != nil {
			return nil, fmt.Errorf("decode context set: %w", err)
		}
		first, err := time.Parse(time.RFC3339Nano, firstStarted)
		if err != nil {
			return nil, fmt.Errorf("parse context version time: %w", err)
		}
		last, err := time.Parse(time.RFC3339Nano, lastStarted)
		if err != nil {
			return nil, fmt.Errorf("parse context version time: %w", err)
		}
		for _, item := range carried {
			if item.Hash == "" {
				continue
			}
			key := versionKey(item.Kind, item.Path, item.Name) + "\x00" + item.Hash
			version := found[key]
			if version == nil {
				version = &contextVersion{hash: item.Hash}
				found[key] = version
			}
			if version.firstRequestID == "" || first.Before(version.firstSeen) {
				version.firstRequestID, version.firstSeen = firstID, first
			}
			if version.lastRequestID == "" || !last.Before(version.lastSeen) {
				version.lastRequestID, version.lastSeen = lastID, last
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate context sets: %w", err)
	}
	versions := map[string][]contextVersion{}
	for key, version := range found {
		item := key[:strings.LastIndexByte(key, 0)]
		versions[item] = append(versions[item], *version)
	}
	for _, list := range versions {
		slices.SortFunc(list, func(left, right contextVersion) int {
			if order := left.firstSeen.Compare(right.firstSeen); order != 0 {
				return order
			}
			return cmp.Compare(left.hash, right.hash)
		})
	}
	return versions, nil
}
