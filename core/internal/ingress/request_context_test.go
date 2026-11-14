package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/convo"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

func contextBody(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func textBlock(text string) map[string]any { return map[string]any{"type": "text", "text": text} }

func toolUse(id, name string, input any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
}

func toolResult(id, content string, isError bool) map[string]any {
	block := map[string]any{"type": "tool_result", "tool_use_id": id, "content": content}
	if isError {
		block["is_error"] = true
	}
	return block
}

func message(role string, content ...any) map[string]any {
	return map[string]any{"role": role, "content": content}
}

func observedItemsByKind(observation *contextObservation) map[contract.ContextItemKind][]observedItem {
	byKind := map[contract.ContextItemKind][]observedItem{}
	for _, item := range observation.items {
		byKind[item.kind] = append(byKind[item.kind], item)
	}
	return byKind
}

// The context reminder Claude Code puts in the first user message, as its
// requests carry it.
const claudeCodeContextReminder = "<system-reminder>\nAs you answer the user's questions, you can use the following context:\n" +
	"# claudeMd\nCodebase and user instructions are shown below. Be sure to adhere to these instructions. " +
	"IMPORTANT: These instructions OVERRIDE any default behavior and you MUST follow them exactly as written.\n\n" +
	"Contents of /Users/alice/.claude/CLAUDE.md (user's private global instructions for all projects):\n\n" +
	"# Global Rules\n\n- Never add a Co-Authored-By trailer.\n- Sign every commit.\n\n" +
	"Contents of /Users/alice/proj/AGENTS.md (project instructions, checked into the codebase):\n\n" +
	"# Agent guidelines\n\nRun gofmt on changed Go files.\n\n" +
	"Contents of /Users/alice/.claude/projects/-Users-alice-proj/memory/MEMORY.md (user's auto-memory, persists across conversations):\n\n" +
	"- [No self plan mode](no-self-plan-mode.md) — never call EnterPlanMode\n" +
	"- [Reply in Chinese](reply-in-chinese.md) — 回复一律用中文\n" +
	"# currentDate\nToday's date is 2026-10-31.\n\n" +
	"      IMPORTANT: this context may or may not be relevant to your tasks. You should not respond to this context unless it is highly relevant to your task.\n" +
	"</system-reminder>\n"

// Claude Code sends its skills catalog as a system-role message to models
// that accept one.
const claudeCodeSkillsMessage = "The following skills are available for use with the Skill tool:\n\n" +
	"- humanizer: Rewrite text so it reads naturally.\n" +
	"- smartcmd:review: Review the working tree.\nTRIGGER when the user asks for a review.\n" +
	"- deploy: Ship the build.\n\n"

func claudeCodeContextBody(t *testing.T, billing string, messages ...any) []byte {
	t.Helper()
	tool := func(name string) map[string]any {
		return map[string]any{
			"name": name, "description": "Tool " + name + ".",
			"input_schema": map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}},
		}
	}
	return contextBody(t, map[string]any{
		"model": "claude-opus-5-5",
		"system": []any{
			textBlock("x-anthropic-billing-header: cc_version=2.1.296." + billing + "; cc_entrypoint=cli;"),
			textBlock("You are Claude Code, Anthropic's official CLI for Claude."),
			textBlock("You are an interactive agent that helps users with software engineering tasks.\n\n" +
				"# Environment\nYou have been invoked in the following environment: \n" +
				" - Primary working directory: /Users/alice/proj/core\n - Is a git repository: true\n - Platform: darwin\n"),
		},
		"tools": []any{
			tool("Bash"), tool("Edit"), tool("Write"),
			tool("mcp__paseo__list_agents"), tool("mcp__paseo__create_agent"), tool("mcp__github__get_issue"),
		},
		"messages": messages,
	})
}

func TestObserveClaudeCodeContext(t *testing.T) {
	bash := map[string]any{"command": "go test ./...", "description": "Run tests"}
	body := claudeCodeContextBody(t, "abc",
		message("user", textBlock(claudeCodeContextReminder), textBlock("Fix the failing test")),
		message("system", textBlock(claudeCodeSkillsMessage)),
		message("assistant", textBlock("Running the tests."), toolUse("toolu_1", "Bash", bash)),
		message("user", toolResult("toolu_1", "Exit code 1\n--- FAIL: TestRoute", true)),
		message("assistant", toolUse("toolu_2", "Bash", bash)),
		message("user", toolResult("toolu_2", "Exit code 1\n--- FAIL: TestRoute", true)),
		message("assistant", toolUse("toolu_3", "Bash", bash)),
		message("user", toolResult("toolu_3", "Exit code 1\n--- FAIL: TestRoute", true)),
	)
	observation := observeRequestContext(contract.ProtocolAnthropicMessages, body)
	if observation == nil {
		t.Fatal("no observation")
	}
	if observation.project != "/Users/alice/proj" {
		t.Fatalf("project = %q, want the root of the project rules, not the shell's subdirectory", observation.project)
	}
	byKind := observedItemsByKind(observation)
	rules := byKind[contract.ContextItemRules]
	if len(rules) != 2 ||
		rules[0].name != "CLAUDE.md" || rules[0].scope != contract.ContextScopeGlobal || rules[0].path != "/Users/alice/.claude/CLAUDE.md" ||
		rules[1].name != "AGENTS.md" || rules[1].scope != contract.ContextScopeProject || rules[1].path != "/Users/alice/proj/AGENTS.md" {
		t.Fatalf("rules = %#v", rules)
	}
	if memory := byKind[contract.ContextItemMemory]; len(memory) != 1 || memory[0].name != "MEMORY.md" || memory[0].count != 2 {
		t.Fatalf("memory = %#v", memory)
	}
	if skills := byKind[contract.ContextItemSkills]; len(skills) != 1 || skills[0].count != 3 ||
		!slices.Equal(skills[0].names, []string{"humanizer", "smartcmd:review", "deploy"}) {
		t.Fatalf("skills = %#v", skills)
	}
	mcp := byKind[contract.ContextItemMCP]
	if len(mcp) != 1 || mcp[0].count != 3 || !slices.Equal(mcp[0].names, []string{"github", "paseo"}) || mcp[0].tokens <= 0 {
		t.Fatalf("mcp = %#v", mcp)
	}
	carried := 0
	for _, item := range observation.items {
		if item.kind != contract.ContextItemMCP {
			carried += item.tokens
		}
	}
	parts := observation.parts
	if parts.Memory != carried || parts.Tools < mcp[0].tokens || parts.System <= 0 || parts.ToolResults <= 0 || parts.History <= 0 {
		t.Fatalf("parts = %#v, carried %d", parts, carried)
	}
	if observation.repeats == nil || *observation.repeats != (contract.FailedRepeats{Tool: "Bash", Count: 3, Command: true}) {
		t.Fatalf("repeats = %#v", observation.repeats)
	}
	if observation.summary || observation.compacted != nil || len(observation.writes) != 0 || observation.stateful {
		t.Fatalf("marks = %#v", observation)
	}

	// The billing line changes with the client build, not with the prompt.
	other := observeRequestContext(contract.ProtocolAnthropicMessages, claudeCodeContextBody(t, "def",
		message("user", textBlock(claudeCodeContextReminder), textBlock("Fix the failing test"))))
	if !bytes.Equal(other.system, observation.system) || !bytes.Equal(other.tools, observation.tools) {
		t.Fatal("billing header changed the prompt digests")
	}
	edited := strings.Replace(claudeCodeContextReminder, "Run gofmt on changed Go files.", "Run gofmt and go vet.", 1)
	changed := observeRequestContext(contract.ProtocolAnthropicMessages, claudeCodeContextBody(t, "abc",
		message("user", textBlock(edited), textBlock("Fix the failing test"))))
	if !bytes.Equal(changed.system, observation.system) {
		t.Fatal("a rules file change moved the system digest")
	}
	before, after := observedItemsByKind(observation)[contract.ContextItemRules][1], observedItemsByKind(changed)[contract.ContextItemRules][1]
	if bytes.Equal(before.digest, after.digest) {
		t.Fatal("the edited AGENTS.md kept its digest")
	}
}

// Claude Code lists its skills once, then lists again only the skills added
// or changed since. The catalog of a call is every skill its listings name.
func TestObserveClaudeCodeSkillListingsAddUp(t *testing.T) {
	changed := "The following skills are available for use with the Skill tool:\n\n" +
		"- deploy: Ship the build to staging.\n- redaction-placeholders: Handle placeholders.\n- astrlink-debug: Debug requests.\n\n"
	added := "The following skills are also available for use with the Skill tool. " +
		"This list adds to the skills listed earlier in this conversation and is not the full list:\n\n- dataviz: Draw charts.\n\n"
	script := "The following skills are also available; a script loads one with `await tools.Skill({skill: \"name\"})`, " +
		"and its instructions arrive as a message right after the script's result. " +
		"This list adds to the skills listed earlier in this conversation and is not the full list:\n\n- notebook: Edit notebooks.\n\n"
	body := claudeCodeContextBody(t, "abc",
		message("user", textBlock(claudeCodeContextReminder), textBlock("Fix the failing test")),
		message("system", textBlock(claudeCodeSkillsMessage)),
		message("assistant", textBlock("Done.")),
		message("user", textBlock("Ship it")),
		message("system", textBlock(changed)),
		message("system", textBlock(added)),
		message("system", textBlock(script)),
	)
	observation := observeRequestContext(contract.ProtocolAnthropicMessages, body)
	if observation == nil {
		t.Fatal("no observation")
	}
	skills := observedItemsByKind(observation)[contract.ContextItemSkills]
	want := []string{"humanizer", "smartcmd:review", "deploy", "redaction-placeholders", "astrlink-debug", "dataviz", "notebook"}
	if len(skills) != 1 || skills[0].count != len(want) || !slices.Equal(skills[0].names, want) {
		t.Fatalf("skills = %#v", skills)
	}
	if deploy := slices.Index(skills[0].names, "deploy"); skills[0].descriptions[deploy] != "Ship the build to staging." {
		t.Fatalf("descriptions = %q, want the newest of deploy", skills[0].descriptions)
	}
	single := observeRequestContext(contract.ProtocolAnthropicMessages, claudeCodeContextBody(t, "abc",
		message("user", textBlock(claudeCodeContextReminder), textBlock("Fix the failing test")),
		message("system", textBlock(claudeCodeSkillsMessage)),
	))
	first := observedItemsByKind(single)[contract.ContextItemSkills]
	if len(first) != 1 || skills[0].tokens <= first[0].tokens || bytes.Equal(skills[0].digest, first[0].digest) {
		t.Fatalf("listings carried %d tokens, digest %x; the first alone %#v", skills[0].tokens, skills[0].digest, first)
	}
}

func TestObserveFailedRepeatsNeedsTheSameFailingCall(t *testing.T) {
	bash := map[string]any{"command": "go test ./..."}
	calls := func(results ...map[string]any) []any {
		var messages []any
		messages = append(messages, message("user", textBlock("Fix it")))
		for index, result := range results {
			id := result["tool_use_id"].(string)
			input := bash
			if index == 1 && result["content"] == "other" {
				input = map[string]any{"command": "go test ./internal/..."}
			}
			messages = append(messages, message("assistant", toolUse(id, "Bash", input)), message("user", result))
		}
		return messages
	}
	cases := []struct {
		name    string
		results []map[string]any
		want    int
	}{
		{"four failures", []map[string]any{
			toolResult("a", "Exit code 2", true), toolResult("b", "Exit code 2", true),
			toolResult("c", "Exit code 2", true), toolResult("d", "Exit code 2", true),
		}, 4},
		{"two failures", []map[string]any{toolResult("a", "Exit code 1", true), toolResult("b", "Exit code 1", true)}, 0},
		{"success between", []map[string]any{
			toolResult("a", "Exit code 1", true), toolResult("b", "ok", false),
			toolResult("c", "Exit code 1", true), toolResult("d", "Exit code 1", true),
		}, 0},
		{"different arguments", []map[string]any{
			toolResult("a", "Exit code 1", true), toolResult("b", "other", true), toolResult("c", "Exit code 1", true),
		}, 0},
		{"exit code without the flag", []map[string]any{
			toolResult("a", "FAIL\nExit code 1", false), toolResult("b", "FAIL\nExit code 1", false),
			toolResult("c", "FAIL\nExit code 1", false),
		}, 3},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			body := contextBody(t, map[string]any{"model": "m", "messages": calls(test.results...)})
			repeats := observeRequestContext(contract.ProtocolAnthropicMessages, body).repeats
			switch {
			case test.want == 0 && repeats != nil:
				t.Fatalf("repeats = %#v, want none", repeats)
			case test.want > 0 && (repeats == nil || repeats.Count != test.want || repeats.Tool != "Bash"):
				t.Fatalf("repeats = %#v, want %d", repeats, test.want)
			}
		})
	}
}

func TestObserveClaudeCodeMemoryWrites(t *testing.T) {
	memoryDir := "/Users/alice/.claude/projects/-Users-alice-proj/memory/"
	writeFile := toolUse("w1", "Write", map[string]any{
		"file_path": memoryDir + "reply-in-chinese.md",
		"content":   "---\nname: reply-in-chinese\ndescription: Reply in Chinese\nmetadata:\n  type: feedback\n---\n\nAlways reply in Chinese.\n",
	})
	editIndex := toolUse("w2", "Edit", map[string]any{
		"file_path":  memoryDir + "MEMORY.md",
		"old_string": "- [No self plan mode](no-self-plan-mode.md) — never call EnterPlanMode\n",
		"new_string": "- [No self plan mode](no-self-plan-mode.md) — never call EnterPlanMode\n- [Reply in Chinese](reply-in-chinese.md) — 回复一律用中文\n",
	})
	editRules := toolUse("w3", "Edit", map[string]any{
		"file_path":  "/Users/alice/proj/AGENTS.md",
		"old_string": "# Agent guidelines\n",
		"new_string": "# Agent guidelines\n\n- Run go vet before finishing.\n",
	})
	failed := toolUse("w4", "Write", map[string]any{"file_path": memoryDir + "lost.md", "content": "# Lost\n"})
	older := toolUse("w0", "Write", map[string]any{"file_path": memoryDir + "older.md", "content": "# Older memory\n"})
	body := claudeCodeContextBody(t, "abc",
		message("user", textBlock(claudeCodeContextReminder), textBlock("Remember to reply in Chinese")),
		message("assistant", older),
		message("user", toolResult("w0", "File created successfully", false)),
		message("assistant", textBlock("Saving it."), writeFile, editIndex, editRules, failed),
		message("user",
			toolResult("w1", "File created successfully", false), toolResult("w2", "The file has been updated.", false),
			toolResult("w3", "The file has been updated.", false), toolResult("w4", "Permission denied", true)),
	)
	writes := observeRequestContext(contract.ProtocolAnthropicMessages, body).writes
	want := []observedWrite{
		{kind: contract.ContextItemMemory, name: "MEMORY.md", path: memoryDir + "MEMORY.md", title: "Reply in Chinese", hook: "回复一律用中文",
			entry: "reply-in-chinese.md", content: "- [Reply in Chinese](reply-in-chinese.md) — 回复一律用中文\n"},
		{kind: contract.ContextItemRules, name: "AGENTS.md", path: "/Users/alice/proj/AGENTS.md", title: "Run go vet before finishing.",
			content: "- Run go vet before finishing.\n"},
	}
	if !slices.Equal(writes, want) {
		t.Fatalf("writes = %#v\nwant %#v", writes, want)
	}

	// The call after a plain answer shows no write again.
	later := claudeCodeContextBody(t, "abc",
		message("user", textBlock("Remember to reply in Chinese")),
		message("assistant", writeFile, editIndex),
		message("user", toolResult("w1", "File created successfully", false), toolResult("w2", "The file has been updated.", false)),
		message("assistant", textBlock("Saved.")),
		message("user", textBlock("Thanks")),
	)
	if writes := observeRequestContext(contract.ProtocolAnthropicMessages, later).writes; len(writes) != 0 {
		t.Fatalf("writes after the answer = %#v", writes)
	}
}

func TestObserveClaudeCodeCompaction(t *testing.T) {
	history := []any{
		message("user", textBlock("Refactor ingress")),
		message("assistant", textBlock("Done.")),
	}
	request := append(slices.Clone(history), message("user", textBlock(
		"Your task is to create a detailed summary of the conversation so far, paying close attention to the user's explicit requests and your previous actions.")))
	if observation := observeRequestContext(contract.ProtocolAnthropicMessages, claudeCodeContextBody(t, "abc", request...)); !observation.summary {
		t.Fatal("the summary request was not marked")
	}
	continued := func(summary string) []byte {
		return claudeCodeContextBody(t, "abc",
			message("user", textBlock(claudeCodeContextReminder), textBlock(
				"This session is being continued from a previous conversation that ran out of context. "+
					"The summary below covers the earlier portion of the conversation.\n\nSummary:\n"+summary)),
			message("assistant", textBlock("Continuing.")),
			message("user", textBlock("This session is being continued from a previous conversation, pasted by the user.")),
		)
	}
	first := observeRequestContext(contract.ProtocolAnthropicMessages, continued("1. Refactor ingress"))
	second := observeRequestContext(contract.ProtocolAnthropicMessages, continued("1. Refactor ingress\n2. Add tests"))
	if first.compacted == nil || second.compacted == nil || bytes.Equal(first.compacted, second.compacted) {
		t.Fatalf("compacted digests %x and %x", first.compacted, second.compacted)
	}
	if first.summary {
		t.Fatal("a continued conversation was taken for a summary request")
	}
}

const codexSkillsInstructions = "<skills_instructions>\n## Skills\n" +
	"A skill is a set of local instructions to follow that is stored in a `SKILL.md` file.\n" +
	"### Skill roots\n- `r0` = `/Users/alice/.agents/skills`\n### Available skills\n" +
	"- humanizer: Rewrite text. (file: r0/humanizer/SKILL.md)\n" +
	"- deploy: Ship it. (file: r0/deploy/SKILL.md)\n" +
	"</skills_instructions>"

func codexContextBody(t *testing.T, extra map[string]any, items ...any) []byte {
	t.Helper()
	input := []any{
		map[string]any{"type": "message", "role": "developer", "content": []any{
			map[string]any{"type": "input_text", "text": "<permissions instructions>\nFilesystem sandboxing defines which files can be read or written.\n</permissions instructions>"},
			map[string]any{"type": "input_text", "text": codexSkillsInstructions},
		}},
		map[string]any{"type": "message", "role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "# AGENTS.md instructions for /Users/alice/proj\n\n<INSTRUCTIONS>\n# Global\nAlways answer briefly.\n\n--- project-doc ---\n\n# Agent guidelines\nRun gofmt on changed Go files.\n</INSTRUCTIONS>"},
			map[string]any{"type": "input_text", "text": "<environment_context>\n  <cwd>/Users/alice/proj</cwd>\n  <shell>zsh</shell>\n</environment_context>"},
		}},
	}
	body := map[string]any{
		"model":        "gpt-5.5-codex",
		"instructions": "You are Codex, based on GPT-5. You are running as a coding agent in the Codex CLI on a user's computer.",
		"input":        append(input, items...),
		"tools": []any{
			map[string]any{"type": "function", "name": "shell_command", "description": "Runs a shell command.", "parameters": map[string]any{"type": "object"}},
			map[string]any{"type": "custom", "name": "apply_patch", "description": "Edits files."},
			map[string]any{"type": "namespace", "name": "mcp__paseo__", "description": "Paseo tools.", "tools": []any{
				map[string]any{"type": "function", "name": "list_agents", "parameters": map[string]any{"type": "object"}},
				map[string]any{"type": "function", "name": "create_agent", "parameters": map[string]any{"type": "object"}},
			}},
		},
	}
	for key, value := range extra {
		body[key] = value
	}
	return contextBody(t, body)
}

func TestObserveCodexContext(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: AGENTS.md\n@@\n # Agent guidelines\n+Always run gofmt before committing.\n*** End Patch\n"
	body := codexContextBody(t, nil,
		map[string]any{"type": "compaction", "encrypted_content": "gAAAAABcompacted-history"},
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Remember to run gofmt"}}},
		map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": "Adding the rule."}}, "encrypted_content": "gAAAAABreasoning"},
		map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "call_1", "input": patch},
		map[string]any{"type": "custom_tool_call_output", "call_id": "call_1", "output": "Success. Updated the following files:\nM AGENTS.md\n"},
	)
	observation := observeRequestContext(contract.ProtocolOpenAIResponses, body)
	if observation.project != "/Users/alice/proj" {
		t.Fatalf("project = %q", observation.project)
	}
	byKind := observedItemsByKind(observation)
	rules := byKind[contract.ContextItemRules]
	if len(rules) != 2 || rules[0].scope != contract.ContextScopeGlobal || rules[0].path != "~/.codex/AGENTS.md" ||
		rules[1].scope != contract.ContextScopeProject || rules[1].path != "/Users/alice/proj/AGENTS.md" {
		t.Fatalf("rules = %#v", rules)
	}
	if skills := byKind[contract.ContextItemSkills]; len(skills) != 1 || !slices.Equal(skills[0].names, []string{"humanizer", "deploy"}) {
		t.Fatalf("skills = %#v", skills)
	}
	if mcp := byKind[contract.ContextItemMCP]; len(mcp) != 1 || mcp[0].count != 2 || !slices.Equal(mcp[0].names, []string{"paseo"}) {
		t.Fatalf("mcp = %#v", mcp)
	}
	if observation.compacted == nil || observation.summary || observation.stateful {
		t.Fatalf("marks = compacted %x summary %v stateful %v", observation.compacted, observation.summary, observation.stateful)
	}
	want := []observedWrite{{kind: contract.ContextItemRules, name: "AGENTS.md", path: "/Users/alice/proj/AGENTS.md", title: "Always run gofmt before committing.",
		content: "Always run gofmt before committing.\n"}}
	if !slices.Equal(observation.writes, want) {
		t.Fatalf("writes = %#v", observation.writes)
	}

	// Codex asks for its summary on its own endpoint.
	if compact := observeRequestContext(contract.ProtocolOpenAIResponsesCompact, codexContextBody(t, nil)); !compact.summary {
		t.Fatal("the compact request was not marked")
	}
	// A request chained to a stored response holds only what is new.
	if chained := observeRequestContext(contract.ProtocolOpenAIResponses, codexContextBody(t, map[string]any{"previous_response_id": "resp_1"})); !chained.stateful {
		t.Fatal("a chained request was taken for the whole input")
	}

	shell := func(id string) []any {
		return []any{
			map[string]any{"type": "function_call", "name": "shell_command", "call_id": id, "arguments": `{"command":"go test ./...","workdir":"/Users/alice/proj"}`},
			map[string]any{"type": "function_call_output", "call_id": id, "output": "Exit code: 1\nWall time: 0.4 seconds\nOutput:\nFAIL"},
		}
	}
	var failing []any
	for _, id := range []string{"c1", "c2", "c3"} {
		failing = append(failing, shell(id)...)
	}
	repeats := observeRequestContext(contract.ProtocolOpenAIResponses, codexContextBody(t, nil, failing...)).repeats
	if repeats == nil || *repeats != (contract.FailedRepeats{Tool: "shell_command", Count: 3, Command: true}) {
		t.Fatalf("repeats = %#v", repeats)
	}
}

func TestObserveDeepSeekHarnessContext(t *testing.T) {
	user := func(text string) map[string]any { return map[string]any{"role": "user", "content": text} }
	messages := []any{
		map[string]any{"role": "system", "content": "You are DeepSeek Harness, a coding agent."},
		user("<system-reminder>\nThe following workspace instructions may be relevant to your work. Use them as guidance when applicable.\n\n" +
			"Instructions from: ~/.dsh/AGENTS.md\n\nPrefer small commits.\n\n" +
			"Instructions from: AGENTS.md\n\nRun npm test before finishing.\n</system-reminder>"),
		user("<system-reminder>\nA skill is a reusable set of task-specific instructions. The following skills are available in this session:\n\n" +
			"<available_skills>\n- `humanizer`: Rewrite text.\n- `deploy`: Ship it.\n</available_skills>\n\n" +
			"If the user names a skill, call the `skill` tool.\n</system-reminder>"),
		user("Current runtime context. This snapshot supersedes earlier runtime-context snapshots.\n\n" +
			"Current DSH file policy: workspace-write. Any available operation enforced by the DSH file sandbox may modify files under the session workspace: \"/home/bob/work/api\". Some platform temporary areas may also be writable."),
		user("This is an automatically generated checkpoint condensing an earlier span of the conversation to free up context.\n\n## Goal\nShip the API."),
		user("add the endpoint"),
	}
	for _, id := range []string{"c1", "c2", "c3"} {
		messages = append(messages,
			map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{
				"id": id, "type": "function", "function": map[string]any{"name": "bash", "arguments": `{"command":"npm test"}`},
			}}},
			map[string]any{"role": "tool", "tool_call_id": id, "content": "npm ERR! Test failed.\n[exit code: 1]"},
		)
	}
	observation := observeRequestContext(contract.ProtocolOpenAIChat, contextBody(t, map[string]any{"model": "deepseek-v4", "messages": messages}))
	if observation.project != "/home/bob/work/api" {
		t.Fatalf("project = %q", observation.project)
	}
	rules := observedItemsByKind(observation)[contract.ContextItemRules]
	if len(rules) != 2 || rules[0].path != "~/.dsh/AGENTS.md" || rules[0].scope != contract.ContextScopeGlobal ||
		rules[1].path != "/home/bob/work/api/AGENTS.md" || rules[1].scope != contract.ContextScopeProject {
		t.Fatalf("rules = %#v", rules)
	}
	if skills := observedItemsByKind(observation)[contract.ContextItemSkills]; len(skills) != 1 || !slices.Equal(skills[0].names, []string{"humanizer", "deploy"}) {
		t.Fatalf("skills = %#v", skills)
	}
	if observation.compacted == nil {
		t.Fatal("the checkpoint was not taken for a compaction summary")
	}
	if observation.repeats == nil || *observation.repeats != (contract.FailedRepeats{Tool: "bash", Count: 3, Command: true}) {
		t.Fatalf("repeats = %#v", observation.repeats)
	}

	summary := append(slices.Clone(messages[:6]), user("You are now acting as a compaction engine for this AI coding assistant. Condense the conversation ABOVE into a structured checkpoint."))
	if !observeRequestContext(contract.ProtocolOpenAIChat, contextBody(t, map[string]any{"model": "deepseek-v4", "messages": summary})).summary {
		t.Fatal("the compaction request was not marked")
	}
}

func TestObservePiContext(t *testing.T) {
	system := "You are an expert coding assistant.\n\n<project_context>\nProject-specific instructions and guidelines:\n\n" +
		"<project_instructions path=\"/Users/alice/proj/AGENTS.md\">\nRun gofmt.\n</project_instructions>\n</project_context>\n\n" +
		"<skills>\nThe following skills provide specialized instructions for specific tasks.\n<available_skills>\n" +
		"  <skill>\n    <name>humanizer</name>\n    <description>Rewrite text.</description>\n  </skill>\n" +
		"</available_skills>\n</skills>\n\n<cwd>\n/Users/alice/proj/web\n</cwd>"
	body := contextBody(t, map[string]any{"model": "m", "system": system, "messages": []any{
		message("user", textBlock("The conversation history before this point was compacted into the following summary:\n\n<summary>\nBuilt the page.\n</summary>")),
		message("user", textBlock("next step")),
	}})
	observation := observeRequestContext(contract.ProtocolAnthropicMessages, body)
	if observation.project != "/Users/alice/proj" || observation.compacted == nil {
		t.Fatalf("project %q compacted %x", observation.project, observation.compacted)
	}
	byKind := observedItemsByKind(observation)
	if rules := byKind[contract.ContextItemRules]; len(rules) != 1 || rules[0].scope != contract.ContextScopeProject {
		t.Fatalf("rules = %#v", rules)
	}
	if skills := byKind[contract.ContextItemSkills]; len(skills) != 1 || !slices.Equal(skills[0].names, []string{"humanizer"}) {
		t.Fatalf("skills = %#v", skills)
	}
}

func TestObserveContextKeepsQuotedTextOut(t *testing.T) {
	// A transcript pasted into a plain user message carries no reminder.
	body := contextBody(t, map[string]any{"model": "m", "messages": []any{
		message("user", textBlock("Why does this fail?\n\nContents of /Users/alice/proj/CLAUDE.md (project instructions, checked into the codebase):\n\n# Rules")),
	}})
	observation := observeRequestContext(contract.ProtocolAnthropicMessages, body)
	if len(observation.items) != 0 || observation.parts.Memory != 0 {
		t.Fatalf("pasted text counted as carried: %#v", observation.items)
	}
	if observeRequestContext(contract.ProtocolGoogleGenerateContent, body) != nil {
		t.Fatal("a protocol without the parser observed something")
	}
}

func testContextFingerprinter() *convo.Fingerprinter {
	return convo.NewFingerprinter(convo.DeriveFingerprintKey([]byte("0123456789abcdef0123456789abcdef"), contextFingerprintInfo))
}

func TestStoredContextIsScaledToTheReportedInput(t *testing.T) {
	body := claudeCodeContextBody(t, "abc",
		message("user", textBlock(claudeCodeContextReminder), textBlock("Fix the failing test")),
		message("system", textBlock(claudeCodeSkillsMessage)),
	)
	session := newRecordSession(Request{Protocol: contract.ProtocolAnthropicMessages}, "", contract.DefaultAuditSettings())
	session.observeContext(observeRequestContext(contract.ProtocolAnthropicMessages, body), testContextFingerprinter())
	if session.project != "~/proj" {
		t.Fatalf("project = %q", session.project)
	}
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "context.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// The record keeps the body's estimates; the store scales them on read.
	estimates := session.recordContext()
	started := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for index, input := range []int{0, 1, 777, 48_213} {
		completed, latency, project := started.Add(time.Second), 1_000, session.project
		record := contract.RequestRecord{
			ID: contract.RequestID(fmt.Sprintf("request_scaled_%d", index)), StartedAt: started, CompletedAt: &completed,
			LatencyMs: &latency, Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolAnthropicMessages,
			AttemptIndex: 1, Audit: contract.NotCapturedAuditSummary(), ClientType: contract.ClientClaudeCode,
			Context: session.recordContext(), Project: &project,
		}
		if input > 0 {
			record.Usage = &contract.Usage{InputTokens: input, TotalTokens: input}
		}
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
		stored, err := store.GetRequestRecord(ctx, record.ID)
		if err != nil {
			t.Fatal(err)
		}
		requestContext := stored.Context
		if err := requestContext.Validate(); err != nil {
			t.Fatal(err)
		}
		if stored.Project == nil || *stored.Project != "~/proj" || len(requestContext.Items) != len(estimates.Items) {
			t.Fatalf("stored project %v items %#v", stored.Project, requestContext.Items)
		}
		if input == 0 {
			// Without usage the estimates stay.
			if *requestContext.Parts != *estimates.Parts || requestContext.Items[0].Tokens != estimates.Items[0].Tokens {
				t.Fatalf("parts %#v, want the estimates %#v", requestContext.Parts, estimates.Parts)
			}
			continue
		}
		if requestContext.Parts.Total() != input {
			t.Fatalf("parts %#v sum to %d, want %d", requestContext.Parts, requestContext.Parts.Total(), input)
		}
		carried, mcp := 0, false
		for _, item := range requestContext.Items {
			if item.Kind == contract.ContextItemMCP {
				mcp = true
			} else {
				carried += item.Tokens
			}
			if item.Hash == "" || strings.Contains(item.Path, "alice") || item.Names != nil {
				t.Fatalf("item %#v keeps the account name or the skill names, or lacks its hash", item)
			}
		}
		if carried != requestContext.Parts.Memory || !mcp {
			t.Fatalf("items sum to %d, memory part %d, mcp %v", carried, requestContext.Parts.Memory, mcp)
		}
	}
}

func TestMemoryTitlesFollowThePreviewExposure(t *testing.T) {
	secret := "sk-" + "test" + "0123456789abcdef"
	observation := &contextObservation{writes: []observedWrite{{
		kind: contract.ContextItemMemory, name: "MEMORY.md", title: "Deploy with " + secret + " from https://internal.example/x",
	}}}
	newSession := func() *recordSession {
		session := newRecordSession(Request{Protocol: contract.ProtocolAnthropicMessages}, "", contract.DefaultAuditSettings())
		session.observeContext(observation, nil)
		return session
	}

	shareable := newSession()
	title := shareable.recordContext().MemoryWrites[0].Title
	if strings.Contains(title, secret) || strings.Contains(title, "internal.example") || !strings.HasPrefix(title, "Deploy with") {
		t.Fatalf("shareable title = %q", title)
	}

	gated := newSession()
	gated.privacyGated = true
	if title := gated.recordContext().MemoryWrites[0].Title; title != "" {
		t.Fatalf("title before the privacy decision = %q", title)
	}

	redacted := newSession()
	redacted.privacyGated = true
	redacted.privacyDecision = contract.PrivacyDecisionRedact
	redacted.protectedValues = []string{"Deploy"}
	if title := redacted.recordContext().MemoryWrites[0].Title; strings.Contains(title, "Deploy") || title == "" {
		t.Fatalf("redacted title = %q", title)
	}
}

func TestContextPaths(t *testing.T) {
	for input, want := range map[string]string{
		"/Users/alice/proj/AGENTS.md":        "~/proj/AGENTS.md",
		"/home/bob":                          "~",
		"/root/.codex/AGENTS.md":             "~/.codex/AGENTS.md",
		`C:\Users\Carol\proj\AGENTS.md`:      `~\proj\AGENTS.md`,
		"/srv/app/AGENTS.md":                 "/srv/app/AGENTS.md",
		"/Users/alice.bak/../alice/x":        "~/../alice/x",
		"relative/AGENTS.md":                 "relative/AGENTS.md",
		"/Users/aliceproj/AGENTS.md":         "~/AGENTS.md",
		"/Users/alice/.claude/CLAUDE.md":     "~/.claude/CLAUDE.md",
		"/Users/alice/proj/sub/../AGENTS.md": "~/proj/sub/../AGENTS.md",
		"/Users/alice/.claude/projects/-Users-alice-proj/memory/MEMORY.md":     "~/.claude/projects/~-proj/memory/MEMORY.md",
		"/Users/al/.claude/projects/-Users-alice-proj/memory/MEMORY.md":        "~/.claude/projects/-Users-alice-proj/memory/MEMORY.md",
		`C:\Users\Carol\.claude\projects\C--Users-Carol-proj\memory\MEMORY.md`: `~\.claude\projects\~-proj\memory\MEMORY.md`,
	} {
		if got := homePath(input); got != want {
			t.Errorf("homePath(%q) = %q, want %q", input, got, want)
		}
	}
	items := []observedItem{
		{kind: contract.ContextItemRules, scope: contract.ContextScopeGlobal, path: "/Users/alice/.claude/CLAUDE.md"},
		{kind: contract.ContextItemRules, scope: contract.ContextScopeProject, path: "/Users/alice/CLAUDE.md"},
		{kind: contract.ContextItemRules, scope: contract.ContextScopeProject, path: "/Users/alice/proj/core/AGENTS.md"},
		{kind: contract.ContextItemRules, scope: contract.ContextScopeProject, path: "/Users/alice/proj/AGENTS.md"},
		{kind: contract.ContextItemRules, scope: contract.ContextScopeProject, path: "/Users/alice/other/AGENTS.md"},
	}
	if root := projectRoot("/Users/alice/proj/core/internal", items); root != "/Users/alice/proj" {
		t.Fatalf("projectRoot = %q", root)
	}
	if root := projectRoot("/srv/app/web", nil); root != "/srv/app/web" {
		t.Fatalf("projectRoot without rules = %q", root)
	}
	if root := projectRoot("", items[3:4]); root != "/Users/alice/proj" {
		t.Fatalf("projectRoot from rules alone = %q", root)
	}
}

func TestObserveMemoryIndexAndCatalogs(t *testing.T) {
	index := "<system-reminder>\nContents of /Users/alice/.claude/projects/-Users-alice-proj/memory/MEMORY.md (user's auto-memory, persists across conversations):\n\n" +
		"# Index\nSee [the guide](guide.md) first.\n" +
		"- [No self plan mode](no-self-plan-mode.md) — never call EnterPlanMode\n" +
		"* [Reply in Chinese](feedback/reply-in-chinese.md): 回复一律用中文\n" +
		"- [Bare link](bare.md)\n" +
		"- [Not a memory](notes.txt) — skipped\n" +
		"- [No self plan mode again](no-self-plan-mode.md) — a repeat\n" +
		"</system-reminder>\n"
	body := claudeCodeContextBody(t, "abc",
		message("user", textBlock(index), textBlock("Fix the failing test")),
		message("system", textBlock(claudeCodeSkillsMessage)),
	)
	observation := observeRequestContext(contract.ProtocolAnthropicMessages, body)
	byKind := observedItemsByKind(observation)
	wantEntries := []observedIndexEntry{
		{file: "no-self-plan-mode.md", title: "No self plan mode", hook: "never call EnterPlanMode"},
		{file: "reply-in-chinese.md", title: "Reply in Chinese", hook: "回复一律用中文"},
		{file: "bare.md", title: "Bare link"},
	}
	if memory := byKind[contract.ContextItemMemory]; len(memory) != 1 || !slices.Equal(memory[0].entries, wantEntries) {
		t.Fatalf("memory = %#v", memory)
	}
	skills := byKind[contract.ContextItemSkills]
	if len(skills) != 1 || !slices.Equal(skills[0].descriptions, []string{"Rewrite text so it reads naturally.", "Review the working tree.", "Ship the build."}) {
		t.Fatalf("skills = %#v", skills)
	}
	mcp := byKind[contract.ContextItemMCP]
	if len(mcp) != 1 || !slices.Equal(mcp[0].tools["paseo"], []string{"list_agents", "create_agent"}) ||
		!slices.Equal(mcp[0].tools["github"], []string{"get_issue"}) {
		t.Fatalf("mcp = %#v", mcp)
	}

	// Codex's catalog says where each skill's file is; its MCP tools come
	// in a namespace.
	codex := contextBody(t, map[string]any{"model": "gpt-5.5", "instructions": "You are Codex.", "tools": []any{
		map[string]any{"type": "namespace", "name": "mcp__paseo__", "tools": []any{
			map[string]any{"type": "function", "name": "create_agent"}, map[string]any{"type": "function", "name": "send_agent_prompt"},
		}},
	}, "input": []any{
		map[string]any{"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": codexSkillsInstructions}}},
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
	}})
	byKind = observedItemsByKind(observeRequestContext(contract.ProtocolOpenAIResponses, codex))
	if skills := byKind[contract.ContextItemSkills]; len(skills) != 1 || !slices.Equal(skills[0].descriptions, []string{"Rewrite text.", "Ship it."}) {
		t.Fatalf("codex skills = %#v", skills)
	}
	if mcp := byKind[contract.ContextItemMCP]; len(mcp) != 1 || !slices.Equal(mcp[0].tools["paseo"], []string{"create_agent", "send_agent_prompt"}) {
		t.Fatalf("codex mcp = %#v", mcp)
	}

	// Pi tags each skill.
	pi := contextBody(t, map[string]any{"model": "m", "system": "<skills>\n<available_skills>\n" +
		"  <skill>\n    <name>humanizer</name>\n    <description>Rewrite text &amp; reports.</description>\n  </skill>\n" +
		"  <skill>\n    <name>deploy</name>\n  </skill>\n</available_skills>\n</skills>", "messages": []any{message("user", textBlock("hi"))}})
	byKind = observedItemsByKind(observeRequestContext(contract.ProtocolAnthropicMessages, pi))
	if skills := byKind[contract.ContextItemSkills]; len(skills) != 1 || !slices.Equal(skills[0].names, []string{"humanizer", "deploy"}) ||
		!slices.Equal(skills[0].descriptions, []string{"Rewrite text & reports.", ""}) {
		t.Fatalf("pi skills = %#v", skills)
	}
}

func TestObserveMemoryReadsAndSkillsUsed(t *testing.T) {
	memoryDir := "/Users/alice/.claude/projects/-Users-alice-proj/memory/"
	older := toolUse("r0", "Read", map[string]any{"file_path": memoryDir + "older.md"})
	body := claudeCodeContextBody(t, "abc",
		message("user", textBlock(claudeCodeContextReminder), textBlock("Why does CI fail?")),
		message("assistant", older),
		message("user", toolResult("r0", "     1\t# Older", false)),
		message("assistant",
			toolUse("r1", "Read", map[string]any{"file_path": memoryDir + "no-jitter.md"}),
			toolUse("r2", "Read", map[string]any{"file_path": memoryDir + "MEMORY.md"}),
			toolUse("r3", "Bash", map[string]any{"command": "cat '" + memoryDir + "reply-in-chinese.md'"}),
			toolUse("r4", "Bash", map[string]any{"command": "cat " + memoryDir + "piped.md | head"}),
			toolUse("r5", "Read", map[string]any{"file_path": memoryDir + "denied.md"}),
			toolUse("r6", "Skill", map[string]any{"skill": "humanizer", "args": "the report"}),
			toolUse("r7", "Read", map[string]any{"file_path": "/Users/alice/.claude/skills/dataviz/SKILL.md"}),
			toolUse("r8", "Read", map[string]any{"file_path": "/Users/alice/proj/README.md"}),
			toolUse("r9", "Read", map[string]any{"file_path": memoryDir + "no-jitter.md", "offset": 10}),
		),
		message("user",
			toolResult("r1", "     1\t---\n     2\tname: no-jitter\n     3\t---\n     4\tWait on done channels.\n\n<system-reminder>\nWhenever you read a file...\n</system-reminder>", false),
			toolResult("r2", "     1\t- [No jitter](no-jitter.md)", false),
			toolResult("r3", "回复一律用中文", false), toolResult("r4", "x", false),
			toolResult("r5", "Permission denied", true), toolResult("r6", "Launching skill: humanizer", false),
			toolResult("r7", "     1\t# Dataviz", false), toolResult("r8", "     1\t# Proj", false),
			toolResult("r9", "    10\tMore.", false),
		),
	)
	observation := observeRequestContext(contract.ProtocolAnthropicMessages, body)
	wantReads := []observedRead{
		{name: "no-jitter.md", path: memoryDir + "no-jitter.md"},
		{name: "reply-in-chinese.md", path: memoryDir + "reply-in-chinese.md"},
	}
	if !slices.Equal(observation.reads, wantReads) {
		t.Fatalf("reads = %#v", observation.reads)
	}
	if !slices.Equal(observation.skills, []string{"humanizer", "dataviz"}) {
		t.Fatalf("skills = %#v", observation.skills)
	}
	texts := ContextTexts(contract.ProtocolAnthropicMessages, body)
	wantTexts := []contract.MemoryReadText{
		{Name: "no-jitter.md", Path: "~/.claude/projects/~-proj/memory/no-jitter.md", Content: "---\nname: no-jitter\n---\nWait on done channels.\n"},
		{Name: "reply-in-chinese.md", Path: "~/.claude/projects/~-proj/memory/reply-in-chinese.md", Content: "回复一律用中文\n"},
	}
	if !slices.Equal(texts.Reads, wantTexts) {
		t.Fatalf("read texts = %#v", texts.Reads)
	}

	// Codex reads a skill's file through its shell.
	codex := contextBody(t, map[string]any{"model": "gpt-5.5", "input": []any{
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "humanize this"}}},
		map[string]any{"type": "function_call", "call_id": "c1", "name": "exec_command",
			"arguments": `{"cmd":"sed -n '1,200p' /Users/alice/.agents/skills/humanizer/SKILL.md","workdir":"/Users/alice/proj"}`},
		map[string]any{"type": "function_call", "call_id": "c2", "name": "shell",
			"arguments": `{"command":["bash","-lc","head -n 40 /Users/alice/.codex/memories/style.md"]}`},
		map[string]any{"type": "function_call", "call_id": "c3", "name": "exec_command",
			"arguments": `{"cmd":"sed -i 's/a/b/' /Users/alice/.agents/skills/deploy/SKILL.md"}`},
		map[string]any{"type": "function_call_output", "call_id": "c1", "output": "Chunk ID: 1\nWall time: 0.1 seconds\nProcess exited with code 0\nOutput:\n# Humanizer\n"},
		map[string]any{"type": "function_call_output", "call_id": "c2", "output": "Use plain words."},
		map[string]any{"type": "function_call_output", "call_id": "c3", "output": "Process exited with code 0"},
	}})
	observation = observeRequestContext(contract.ProtocolOpenAIResponses, codex)
	if !slices.Equal(observation.skills, []string{"humanizer"}) ||
		!slices.Equal(observation.reads, []observedRead{{name: "style.md", path: "/Users/alice/.codex/memories/style.md"}}) {
		t.Fatalf("codex skills %#v reads %#v", observation.skills, observation.reads)
	}
}

func TestShellReadTarget(t *testing.T) {
	for command, want := range map[string]string{
		"cat ~/.claude/memory/a.md":              "~/.claude/memory/a.md",
		"cat -n 'my notes/a.md'":                 "my notes/a.md",
		`head -n 20 "a b.md"`:                    "a b.md",
		"head -50 a.md":                          "a.md",
		"sed -n '1,120p' a.md":                   "a.md",
		"sed -n 5p a.md":                         "a.md",
		"cat a.md b.md":                          "",
		"cat a.md | grep x":                      "",
		"cat a.md > b.md":                        "",
		"cd x && cat a.md":                       "",
		"sed -i 's/a/b/' a.md":                   "",
		"sed -n '1,120p' a.md b.md":              "",
		"cat $HOME/a.md":                         "",
		"cat 'open":                              "",
		"tail a.md":                              "",
		"rg memory ~/.claude/projects/x/memory/": "",
	} {
		if got := shellReadTarget(command); got != want {
			t.Errorf("shellReadTarget(%q) = %q, want %q", command, got, want)
		}
	}
}

func TestRecordContextCarriesTheIndexAndCatalogOnceTheCallEnds(t *testing.T) {
	secret := "sk-" + "test" + "0123456789abcdef"
	body := claudeCodeContextBody(t, "abc",
		message("user", textBlock(strings.Replace(claudeCodeContextReminder, "never call EnterPlanMode", "token "+secret, 1)), textBlock("Fix the failing test")),
		message("system", textBlock(claudeCodeSkillsMessage)),
	)
	newSession := func() *recordSession {
		session := newRecordSession(Request{Protocol: contract.ProtocolAnthropicMessages}, "", contract.DefaultAuditSettings())
		session.observeContext(observeRequestContext(contract.ProtocolAnthropicMessages, body), testContextFingerprinter())
		return session
	}
	session := newSession()
	session.status = contract.RequestStatusPending
	if running := session.recordContext(); running.MemoryIndex != nil || running.Catalog != nil {
		t.Fatalf("a running call carried the index %#v or the catalog %#v", running.MemoryIndex, running.Catalog)
	}
	session.status = contract.RequestStatusSucceeded
	ended := session.recordContext()
	if err := ended.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(ended.MemoryIndex) != 2 || ended.MemoryIndex[0].File != "no-self-plan-mode.md" || ended.MemoryIndex[0].Title != "No self plan mode" ||
		strings.Contains(ended.MemoryIndex[0].Hook, secret) || ended.MemoryIndex[1].Hook != "回复一律用中文" {
		t.Fatalf("index = %#v", ended.MemoryIndex)
	}
	catalog := ended.Catalog
	if catalog == nil || len(catalog.Skills) != 3 || catalog.Skills[1] != (contract.CatalogSkill{Name: "smartcmd:review", Description: "Review the working tree."}) ||
		len(catalog.MCP) != 2 || catalog.MCP[0].Server != "github" || !slices.Equal(catalog.MCP[1].Tools, []string{"list_agents", "create_agent"}) {
		t.Fatalf("catalog = %#v", catalog)
	}
	// Neither leaves the gateway with the record.
	encoded, err := json.Marshal(ended)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "No self plan mode") || strings.Contains(string(encoded), "Review the working tree") {
		t.Fatalf("record context JSON carries the index or catalog: %s", encoded)
	}

	gated := newSession()
	gated.status, gated.privacyGated, gated.finishing = contract.RequestStatusSucceeded, true, true
	hidden := gated.recordContext()
	if len(hidden.MemoryIndex) != 2 || hidden.MemoryIndex[0].Title != "" || hidden.MemoryIndex[0].File != "no-self-plan-mode.md" ||
		hidden.Catalog.Skills[0].Description != "" || hidden.Catalog.Skills[0].Name != "humanizer" {
		t.Fatalf("gated index %#v catalog %#v", hidden.MemoryIndex, hidden.Catalog)
	}
}
