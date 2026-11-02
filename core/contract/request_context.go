package contract

import (
	"fmt"
)

// RequestContext is what one call sent the model, read from its request body
// on arrival. It keeps metadata only: token estimates per source, the rules,
// memory and tool catalogs the call carried by kind, path and keyed hash, and
// a few marks; never their text. Root records carry it; retries send the same
// body and leave it out.
type RequestContext struct {
	// Parts splits the call's input by where it came from. Nil when the body
	// only held what is new since a response the provider keeps.
	Parts *ContextParts `json:"parts,omitempty"`
	// Items are the rules files, memory files, skills catalog and MCP tools
	// the call carried, in the order they appeared.
	Items []ContextItem `json:"items,omitempty"`
	// SystemHash and ToolsHash are keyed digests of the system prompt the
	// call opened with and of its tool definitions.
	SystemHash string `json:"system_hash,omitempty"`
	ToolsHash  string `json:"tools_hash,omitempty"`
	// Summary is set on a call that asked the model to summarize the
	// conversation for a compaction.
	Summary bool `json:"summary,omitempty"`
	// Compacted is a keyed digest of the compaction summary the call's
	// history starts from. Each compaction changes it.
	Compacted string `json:"compacted,omitempty"`
	// Helper is set on a helper call of the conversation, such as a title.
	Helper bool `json:"helper,omitempty"`
	// MemoryWrites are the memory and rules files the tool calls of the
	// previous response wrote, as this call's history shows them succeed.
	MemoryWrites []MemoryWrite `json:"memory_writes,omitempty"`
	// MemoryReads are the memory files those tool calls read, the index
	// left out.
	MemoryReads []MemoryRead `json:"memory_reads,omitempty"`
	// SkillsUsed names the skills those tool calls loaded.
	SkillsUsed []string `json:"skills_used,omitempty"`
	// MemoryIndex lists the entries of the memory index the call carried,
	// and Catalog the skills' descriptions and the MCP tools' names. The
	// store keeps each once for the calls that carry it, so records never
	// keep them and they never leave the gateway.
	MemoryIndex []MemoryIndexEntry `json:"-"`
	Catalog     *ContextCatalog    `json:"-"`
	// FailedRepeats is set when the newest tool calls of the history repeat
	// one call with the same arguments, and it failed each time.
	FailedRepeats *FailedRepeats `json:"failed_repeats,omitempty"`
}

// ContextParts estimates, in tokens, where a call's input came from. Once the
// call reports usage the parts are scaled to sum to its input tokens.
type ContextParts struct {
	// History is the conversation: messages, reasoning and tool calls.
	History int `json:"history"`
	// ToolResults is what the tools returned.
	ToolResults int `json:"tool_results"`
	// Tools is the tool definitions, MCP tools included.
	Tools int `json:"tools"`
	// System is the system prompt, less the rules and memory inside it.
	System int `json:"system"`
	// Memory is the rules files, memory files and skills catalog.
	Memory int `json:"memory"`
	// Other is images, documents and encrypted reasoning.
	Other int `json:"other"`
}

// Total sums the parts.
func (parts ContextParts) Total() int {
	return parts.History + parts.ToolResults + parts.Tools + parts.System + parts.Memory + parts.Other
}

func (parts ContextParts) Validate() error {
	if parts.History < 0 || parts.ToolResults < 0 || parts.Tools < 0 || parts.System < 0 ||
		parts.Memory < 0 || parts.Other < 0 {
		return fmt.Errorf("context parts must be non-negative")
	}
	return nil
}

// ContextItemKind says what a carried item is.
type ContextItemKind string

const (
	// ContextItemRules is a rules file such as CLAUDE.md or AGENTS.md.
	ContextItemRules ContextItemKind = "rules"
	// ContextItemMemory is a memory index such as MEMORY.md, or a memory file.
	ContextItemMemory ContextItemKind = "memory"
	// ContextItemSkills is the catalog of skills the agent may load.
	ContextItemSkills ContextItemKind = "skills"
	// ContextItemMCP is the tool definitions MCP servers added.
	ContextItemMCP ContextItemKind = "mcp"
)

func (kind ContextItemKind) Valid() bool {
	switch kind {
	case ContextItemRules, ContextItemMemory, ContextItemSkills, ContextItemMCP:
		return true
	default:
		return false
	}
}

// ContextItemScope says whose a rules file is.
type ContextItemScope string

const (
	// ContextScopeGlobal applies to every project of the user.
	ContextScopeGlobal ContextItemScope = "global"
	// ContextScopeProject is checked into the project.
	ContextScopeProject ContextItemScope = "project"
	// ContextScopeLocal is the user's own file for the project, not checked in.
	ContextScopeLocal ContextItemScope = "local"
)

func (scope ContextItemScope) Valid() bool {
	switch scope {
	case "", ContextScopeGlobal, ContextScopeProject, ContextScopeLocal:
		return true
	default:
		return false
	}
}

// Bounds of the request context metadata.
const (
	MaxContextItems          = 32
	MaxContextNameRunes      = 128
	MaxContextPathRunes      = 512
	MaxContextHashRunes      = 64
	MaxContextServers        = 16
	MaxContextServerRunes    = 64
	MaxContextNames          = 128
	MaxMemoryWrites          = 16
	MaxMemoryReads           = 16
	MaxSkillsUsed            = 16
	MaxMemoryIndexEntries    = 512
	MaxCatalogSkills         = MaxContextNames
	MaxCatalogTools          = 256
	MaxMemoryTitleRunes      = MaxInputPreviewRunes
	MaxFailedRepeatToolRunes = MaxOutputToolNameRunes
)

// ContextItem is one file or catalog a call carried to the model.
type ContextItem struct {
	Kind ContextItemKind `json:"kind"`
	// Name is the file name, such as AGENTS.md; empty for skills and MCP.
	Name string `json:"name,omitempty"`
	// Path is where the file lives, with the home directory written as ~.
	// Empty when the request does not say.
	Path  string           `json:"path,omitempty"`
	Scope ContextItemScope `json:"scope,omitempty"`
	// Count is how many entries the item lists: the lines of a memory index,
	// the skills of a catalog, the MCP tools.
	Count *int `json:"count,omitempty"`
	// Servers names the MCP servers whose tools the call carried.
	Servers []string `json:"servers,omitempty"`
	// Names lists the skills of a catalog. The store keeps them once with
	// the items calls share, not on each record, so records read back
	// without them.
	Names  []string `json:"names,omitempty"`
	Tokens int      `json:"tokens"`
	// Hash is a keyed digest of the item's content; equal hashes mean the
	// content did not change.
	Hash string `json:"hash,omitempty"`
}

func (item ContextItem) Validate() error {
	if !item.Kind.Valid() {
		return fmt.Errorf("unknown context item kind %q", item.Kind)
	}
	if !item.Scope.Valid() {
		return fmt.Errorf("unknown context item scope %q", item.Scope)
	}
	if err := validateBoundedText("name", item.Name, MaxContextNameRunes, true); err != nil {
		return err
	}
	if err := validateBoundedText("path", item.Path, MaxContextPathRunes, true); err != nil {
		return err
	}
	if err := validateBoundedText("hash", item.Hash, MaxContextHashRunes, true); err != nil {
		return err
	}
	if item.Count != nil && *item.Count < 0 {
		return fmt.Errorf("count must be non-negative")
	}
	if item.Tokens < 0 {
		return fmt.Errorf("tokens must be non-negative")
	}
	if len(item.Servers) > MaxContextServers {
		return fmt.Errorf("servers must list at most %d names", MaxContextServers)
	}
	for _, server := range item.Servers {
		if err := validateBoundedText("server", server, MaxContextServerRunes, false); err != nil {
			return err
		}
	}
	if len(item.Names) > MaxContextNames {
		return fmt.Errorf("names must list at most %d skills", MaxContextNames)
	}
	for _, name := range item.Names {
		if err := validateBoundedText("names", name, MaxContextNameRunes, false); err != nil {
			return err
		}
	}
	return nil
}

// MemoryWrite is a memory or rules file a tool call wrote.
type MemoryWrite struct {
	// Kind is memory for a memory index or file, rules for a rules file.
	Kind ContextItemKind `json:"kind"`
	// Name is the file written, such as MEMORY.md.
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
	// Title is the entry the write added, cleaned like an input preview.
	// Empty when the record may not show the client's text.
	Title string `json:"title,omitempty"`
	// Entry names the memory file a line added to a memory index points to,
	// and Hook is what the line says after the link, cleaned like Title.
	Entry string `json:"entry,omitempty"`
	Hook  string `json:"hook,omitempty"`
}

func (write MemoryWrite) Validate() error {
	if write.Kind != ContextItemRules && write.Kind != ContextItemMemory {
		return fmt.Errorf("memory write kind must be rules or memory")
	}
	if err := validateBoundedText("name", write.Name, MaxContextNameRunes, false); err != nil {
		return err
	}
	if err := validateBoundedText("path", write.Path, MaxContextPathRunes, true); err != nil {
		return err
	}
	if err := validateBoundedText("title", write.Title, MaxMemoryTitleRunes, true); err != nil {
		return err
	}
	if err := validateBoundedText("hook", write.Hook, MaxMemoryTitleRunes, true); err != nil {
		return err
	}
	return validateBoundedText("entry", write.Entry, MaxContextNameRunes, true)
}

// MemoryRead is a memory file a tool call read.
type MemoryRead struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

func (read MemoryRead) Validate() error {
	if err := validateBoundedText("name", read.Name, MaxContextNameRunes, false); err != nil {
		return err
	}
	return validateBoundedText("path", read.Path, MaxContextPathRunes, true)
}

// MemoryIndexEntry is one line of a memory index: "- [Title](file.md) —
// hook". Title and Hook are cleaned like an input preview, and empty when
// the record may not show the client's text.
type MemoryIndexEntry struct {
	File  string
	Title string
	Hook  string
}

func (entry MemoryIndexEntry) Validate() error {
	if err := validateBoundedText("file", entry.File, MaxContextNameRunes, false); err != nil {
		return err
	}
	if err := validateBoundedText("title", entry.Title, MaxMemoryTitleRunes, true); err != nil {
		return err
	}
	return validateBoundedText("hook", entry.Hook, MaxMemoryTitleRunes, true)
}

// ContextCatalog is what a call's skills catalog and MCP tools say beyond
// their names and size: each skill's description and each server's tools.
type ContextCatalog struct {
	Skills []CatalogSkill  `json:"skills,omitempty"`
	MCP    []CatalogServer `json:"mcp,omitempty"`
}

// CatalogSkill is a skill and its description, cleaned like an input
// preview and empty when the record may not show the client's text.
type CatalogSkill struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// CatalogServer is an MCP server and the names of its tools, without the
// mcp__<server>__ prefix.
type CatalogServer struct {
	Server string   `json:"server"`
	Tools  []string `json:"tools"`
}

func (catalog ContextCatalog) Validate() error {
	if len(catalog.Skills) > MaxCatalogSkills {
		return fmt.Errorf("catalog lists at most %d skills", MaxCatalogSkills)
	}
	for _, skill := range catalog.Skills {
		if err := validateBoundedText("skill", skill.Name, MaxContextNameRunes, false); err != nil {
			return err
		}
		if err := validateBoundedText("description", skill.Description, MaxMemoryTitleRunes, true); err != nil {
			return err
		}
	}
	if len(catalog.MCP) > MaxContextServers {
		return fmt.Errorf("catalog lists at most %d servers", MaxContextServers)
	}
	for _, server := range catalog.MCP {
		if err := validateBoundedText("server", server.Server, MaxContextServerRunes, false); err != nil {
			return err
		}
		if len(server.Tools) > MaxCatalogTools {
			return fmt.Errorf("catalog lists at most %d tools per server", MaxCatalogTools)
		}
		for _, tool := range server.Tools {
			if err := validateBoundedText("tool", tool, MaxOutputToolNameRunes, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// FailedRepeats is one tool call the agent repeated with the same arguments,
// failing each time.
type FailedRepeats struct {
	Tool  string `json:"tool"`
	Count int    `json:"count"`
	// Command is set when the tool runs shell commands.
	Command bool `json:"command,omitempty"`
}

// MinFailedRepeats is how many failures in a row make a repeat worth showing.
const MinFailedRepeats = 3

func (repeats FailedRepeats) Validate() error {
	if err := validateBoundedText("tool", repeats.Tool, MaxFailedRepeatToolRunes, false); err != nil {
		return err
	}
	if repeats.Count < MinFailedRepeats {
		return fmt.Errorf("failed repeats count at least %d calls", MinFailedRepeats)
	}
	return nil
}

func (context RequestContext) Validate() error {
	if context.Parts != nil {
		if err := context.Parts.Validate(); err != nil {
			return err
		}
	}
	if len(context.Items) > MaxContextItems {
		return fmt.Errorf("context lists at most %d items", MaxContextItems)
	}
	for index, item := range context.Items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("items[%d]: %w", index, err)
		}
	}
	for _, hash := range []string{context.SystemHash, context.ToolsHash, context.Compacted} {
		if err := validateBoundedText("hash", hash, MaxContextHashRunes, true); err != nil {
			return err
		}
	}
	if len(context.MemoryWrites) > MaxMemoryWrites {
		return fmt.Errorf("context lists at most %d memory writes", MaxMemoryWrites)
	}
	for index, write := range context.MemoryWrites {
		if err := write.Validate(); err != nil {
			return fmt.Errorf("memory_writes[%d]: %w", index, err)
		}
	}
	if len(context.MemoryReads) > MaxMemoryReads {
		return fmt.Errorf("context lists at most %d memory reads", MaxMemoryReads)
	}
	for index, read := range context.MemoryReads {
		if err := read.Validate(); err != nil {
			return fmt.Errorf("memory_reads[%d]: %w", index, err)
		}
	}
	if len(context.SkillsUsed) > MaxSkillsUsed {
		return fmt.Errorf("context lists at most %d skills used", MaxSkillsUsed)
	}
	for _, name := range context.SkillsUsed {
		if err := validateBoundedText("skills_used", name, MaxContextNameRunes, false); err != nil {
			return err
		}
	}
	if len(context.MemoryIndex) > MaxMemoryIndexEntries {
		return fmt.Errorf("memory index lists at most %d entries", MaxMemoryIndexEntries)
	}
	for index, entry := range context.MemoryIndex {
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("memory index[%d]: %w", index, err)
		}
	}
	if context.Catalog != nil {
		if err := context.Catalog.Validate(); err != nil {
			return fmt.Errorf("catalog: %w", err)
		}
	}
	if context.FailedRepeats != nil {
		if err := context.FailedRepeats.Validate(); err != nil {
			return fmt.Errorf("failed_repeats: %w", err)
		}
	}
	return nil
}
