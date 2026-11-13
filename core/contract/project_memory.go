package contract

import (
	"fmt"
	"time"
)

// ProjectMemory is what one client's agents keep in one project, told as
// they went: the memories its index lists, when the agent noted each and
// how often it looked one up again, the rules files every call carries,
// and the skills and MCP tools it carries and used. It is read from the
// calls that passed through the gateway; no local file is read.
type ProjectMemory struct {
	// Projects lists every project the calls named, the most recent first.
	Projects []MemoryProject `json:"projects"`
	// Project and ClientType name what the rest covers: the project asked
	// for, else the most recent one, and the client asked for, else the one
	// that called in it last. Project is absent for the calls that named
	// none; both are absent when no call named any.
	Project    *string     `json:"project,omitempty"`
	ClientType *ClientType `json:"client_type,omitempty"`
	// DayStart is when the newest day of the entries' reads began; each day
	// before it is 24 hours earlier.
	DayStart time.Time `json:"day_start"`
	// Recent counts the entries noted in the last seven days.
	Recent int `json:"recent"`
	// Entries are the memories the index lists, and those an agent wrote
	// since its calls last carried the index.
	Entries []MemoryEntry `json:"entries"`
	// Stories are the newest memories an agent noted, the newest first,
	// each with what the user said in that turn.
	Stories []MemoryStory `json:"stories"`
	// Rules are the rules files the client's newest main call carried.
	Rules   []MemoryRules `json:"rules"`
	Toolbox MemoryToolbox `json:"toolbox"`
	// HasMemory is set when the calls carried a memory index or wrote a
	// memory; HasRules when they carried a rules file.
	HasMemory bool `json:"has_memory"`
	HasRules  bool `json:"has_rules"`
}

// MemoryProject is one project and the clients whose calls named it.
type MemoryProject struct {
	// Project is the project directory, the home directory written as ~;
	// absent for the calls that named none.
	Project *string `json:"project,omitempty"`
	// ClientTypes lists the clients, the one that called last first.
	ClientTypes []ClientType `json:"client_types"`
	LastSeenAt  time.Time    `json:"last_seen_at"`
}

// Bounds of a memory view.
const (
	// MemoryDays is how many days the read counts of each entry cover.
	MemoryDays = 14
	// MemoryToolboxDays is how many days the toolbox counts uses over.
	MemoryToolboxDays = 30
	// MemoryRecentDays is how recent a memory noted counts as new.
	MemoryRecentDays = 7
	// MaxMemoryStories bounds the stories a view tells.
	MaxMemoryStories = 10
	// MaxMemoryEvents bounds the events one entry lists.
	MaxMemoryEvents = 50
	// MaxMemoryEntryFileRunes bounds the file an index line links.
	MaxMemoryEntryFileRunes = MaxContextNameRunes
)

// MemoryEntry is one memory: a file the index links, with its title and
// the line's description, cleaned like an input preview.
type MemoryEntry struct {
	// File is the file the index line links, relative to the index.
	File string `json:"file"`
	// Path is where the file lives, the home directory written as ~;
	// absent when the calls did not say where the index is.
	Path string `json:"path,omitempty"`
	// Title and Hook are absent when no record may show the client's text.
	Title string `json:"title,omitempty"`
	Hook  string `json:"hook,omitempty"`
	// NotedAt is when an agent noted it: the call whose history shows it
	// written, else the first call whose index listed it. Absent when the
	// index already listed it when the gateway first saw the index.
	NotedAt *time.Time `json:"noted_at,omitempty"`
	// Reads counts, per day, the turns an agent read the file in, the
	// oldest of MemoryDays days first and the day of DayStart last.
	Reads []int `json:"reads"`
	// TotalReads counts the turns it was read in over the records kept.
	TotalReads int `json:"total_reads"`
}

// MemoryStory is a memory an agent noted, with the call that shows it.
type MemoryStory struct {
	File  string    `json:"file"`
	Title string    `json:"title,omitempty"`
	Hook  string    `json:"hook,omitempty"`
	At    time.Time `json:"at"`
	// RequestID and SessionID name the call; absent when the memory shows
	// only in the index the calls carried.
	RequestID *RequestID `json:"request_id,omitempty"`
	SessionID *SessionID `json:"session_id,omitempty"`
	// Prompt is what the user said to open that turn, as its input preview.
	Prompt string `json:"prompt,omitempty"`
}

// MemoryRules is a rules file the calls carry, with its latest change.
type MemoryRules struct {
	Name  string           `json:"name"`
	Path  string           `json:"path,omitempty"`
	Scope ContextItemScope `json:"scope,omitempty"`
	// FirstSeenAt is when a call first carried the file in any version.
	FirstSeenAt *time.Time `json:"first_seen_at,omitempty"`
	// ChangedAt is when a call first carried the current version, once an
	// earlier one was seen. RequestID is that call; BeforeRequestID the
	// last call that carried the version before.
	ChangedAt       *time.Time `json:"changed_at,omitempty"`
	RequestID       *RequestID `json:"request_id,omitempty"`
	SessionID       *SessionID `json:"session_id,omitempty"`
	BeforeRequestID *RequestID `json:"before_request_id,omitempty"`
	// Captured is set when both calls' request bodies were captured, so
	// the two versions can be compared.
	Captured bool `json:"captured,omitempty"`
}

// MemoryToolbox is the skills and MCP tools the client's newest main call
// carried, and those its calls used in the last MemoryToolboxDays days.
type MemoryToolbox struct {
	// Skills lists the skills used, the most used first, then those not
	// used, in catalog order.
	Skills []ToolboxSkill `json:"skills"`
	// MCP lists the servers in the order the calls carried them.
	MCP []ToolboxServer `json:"mcp"`
	// IdleTokens estimates what the skills and MCP tools not used add to
	// each call.
	IdleTokens int `json:"idle_tokens"`
}

// ToolboxSkill is one skill and how many turns used it.
type ToolboxSkill struct {
	Name string `json:"name"`
	// Description is the catalog's, cleaned like an input preview; absent
	// when no record may show the client's text.
	Description string `json:"description,omitempty"`
	Uses        int    `json:"uses"`
	// Carried is set when the newest main call's catalog lists it.
	Carried bool `json:"carried"`
}

// ToolboxServer is one MCP server and its tools.
type ToolboxServer struct {
	Server string        `json:"server"`
	Tools  []ToolboxTool `json:"tools"`
}

// ToolboxTool is one MCP tool and how many calls made it.
type ToolboxTool struct {
	Name    string `json:"name"`
	Uses    int    `json:"uses"`
	Carried bool   `json:"carried"`
}

// MemoryEntryDetail is one memory and what happened to it: when an agent
// noted it and each turn that read it again.
type MemoryEntryDetail struct {
	Entry MemoryEntry `json:"entry"`
	// Events are the oldest first: the note, then the newest reads up to
	// MaxMemoryEvents.
	Events []MemoryEvent `json:"events"`
}

type MemoryEventKind string

const (
	// MemoryEventNoted is the call whose history shows the memory written.
	MemoryEventNoted MemoryEventKind = "noted"
	// MemoryEventRead is a turn whose calls read the memory's file.
	MemoryEventRead MemoryEventKind = "read"
)

// MemoryEvent is one note or read, at the call whose history shows it.
type MemoryEvent struct {
	Kind      MemoryEventKind `json:"kind"`
	At        time.Time       `json:"at"`
	RequestID RequestID       `json:"request_id"`
	SessionID *SessionID      `json:"session_id,omitempty"`
	// Prompt is what the user said to open that turn.
	Prompt string `json:"prompt,omitempty"`
	// Captured is set when the call's request body was captured: a read
	// shows the file as the tool returned it, a note as it was written.
	Captured bool `json:"captured,omitempty"`
}

func (entry MemoryEntry) Validate() error {
	if err := validateBoundedText("file", entry.File, MaxMemoryEntryFileRunes, false); err != nil {
		return err
	}
	if len(entry.Reads) != MemoryDays {
		return fmt.Errorf("reads must count %d days", MemoryDays)
	}
	for _, count := range entry.Reads {
		if count < 0 {
			return fmt.Errorf("reads must be non-negative")
		}
	}
	if entry.TotalReads < 0 {
		return fmt.Errorf("total reads must be non-negative")
	}
	return nil
}

// RequestContextTexts is what one captured call carried, as text: its rules
// and memory files, the compaction summary it continued from, what the tool
// calls its history ends with wrote to memory and rules files, and the
// memory files they read. It is read from the call's captured request body,
// in the audit view asked for.
type RequestContextTexts struct {
	RequestID RequestID `json:"request_id"`
	// Withheld is set when the body cannot be read in this view; Reason says
	// why, and is absent when the body was not captured.
	Withheld bool                `json:"withheld,omitempty"`
	Reason   AuditWithheldReason `json:"reason,omitempty"`
	// Truncated is set when capture kept only the start of the body, so
	// texts may be missing.
	Truncated bool              `json:"truncated,omitempty"`
	Items     []ContextItemText `json:"items"`
	// Summary is the compaction summary the call continued from.
	Summary string            `json:"summary,omitempty"`
	Writes  []MemoryWriteText `json:"writes"`
	Reads   []MemoryReadText  `json:"reads"`
}

// ContextItemText is a rules file, memory file or skills catalog as the call
// carried it.
type ContextItemText struct {
	Kind ContextItemKind `json:"kind"`
	Name string          `json:"name,omitempty"`
	Path string          `json:"path,omitempty"`
	Text string          `json:"text"`
}

// MemoryWriteText is what one tool call wrote to a memory or rules file: the
// file a write created, or the text an edit or patch added.
type MemoryWriteText struct {
	Kind    ContextItemKind `json:"kind"`
	Name    string          `json:"name"`
	Path    string          `json:"path,omitempty"`
	Content string          `json:"content"`
}

// MemoryReadText is a memory file one tool call read, as the tool returned
// it, without the line numbers a read tool puts in front.
type MemoryReadText struct {
	Name    string `json:"name"`
	Path    string `json:"path,omitempty"`
	Content string `json:"content"`
}
