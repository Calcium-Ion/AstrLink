package ingress

import (
	"slices"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestContextTextsReadWhatACapturedCallCarried(t *testing.T) {
	memoryDir := "/Users/alice/.claude/projects/-Users-alice-proj/memory/"
	note := "---\nname: reply-in-chinese\ndescription: Reply in Chinese\n---\n\nAlways reply in Chinese.\n"
	writeFile := toolUse("w1", "Write", map[string]any{"file_path": memoryDir + "reply-in-chinese.md", "content": note})
	editIndex := toolUse("w2", "Edit", map[string]any{
		"file_path":  memoryDir + "MEMORY.md",
		"old_string": "- [No self plan mode](no-self-plan-mode.md) — never call EnterPlanMode\n",
		"new_string": "- [No self plan mode](no-self-plan-mode.md) — never call EnterPlanMode\n- [Reply in Chinese](reply-in-chinese.md) — 回复一律用中文\n",
	})
	summary := "This session is being continued from a previous conversation that ran out of context.\n\nSummary:\n1. Refactor ingress"
	body := claudeCodeContextBody(t, "abc",
		message("user", textBlock(claudeCodeContextReminder), textBlock(summary)),
		message("assistant", textBlock("Saving it."), writeFile, editIndex),
		message("user", toolResult("w1", "File created successfully", false), toolResult("w2", "The file has been updated.", false)),
	)
	texts := ContextTexts(contract.ProtocolAnthropicMessages, body)
	wantItems := []contract.ContextItemText{
		{Kind: contract.ContextItemRules, Name: "CLAUDE.md", Path: "~/.claude/CLAUDE.md",
			Text: "# Global Rules\n\n- Never add a Co-Authored-By trailer.\n- Sign every commit."},
		{Kind: contract.ContextItemRules, Name: "AGENTS.md", Path: "~/proj/AGENTS.md",
			Text: "# Agent guidelines\n\nRun gofmt on changed Go files."},
		{Kind: contract.ContextItemMemory, Name: "MEMORY.md", Path: "~/.claude/projects/~-proj/memory/MEMORY.md",
			Text: "- [No self plan mode](no-self-plan-mode.md) — never call EnterPlanMode\n- [Reply in Chinese](reply-in-chinese.md) — 回复一律用中文"},
	}
	if !slices.Equal(texts.Items, wantItems) {
		t.Fatalf("items = %#v", texts.Items)
	}
	if texts.Summary != summary {
		t.Fatalf("summary = %q", texts.Summary)
	}
	// The memory file keeps its own content beside the index line that
	// links it; the records fold the two into one write.
	wantWrites := []contract.MemoryWriteText{
		{Kind: contract.ContextItemMemory, Name: "reply-in-chinese.md", Path: "~/.claude/projects/~-proj/memory/reply-in-chinese.md", Content: note},
		{Kind: contract.ContextItemMemory, Name: "MEMORY.md", Path: "~/.claude/projects/~-proj/memory/MEMORY.md",
			Content: "- [Reply in Chinese](reply-in-chinese.md) — 回复一律用中文\n"},
	}
	if !slices.Equal(texts.Writes, wantWrites) {
		t.Fatalf("writes = %#v", texts.Writes)
	}
	if observed := observeRequestContext(contract.ProtocolAnthropicMessages, body); observed.items[0].text != "" || observed.written != nil {
		t.Fatal("the records' observation kept texts")
	}

	// A body it cannot read leaves every list empty rather than absent.
	for _, empty := range []contract.RequestContextTexts{
		ContextTexts(contract.ProtocolOpenAIEmbeddings, body),
		ContextTexts(contract.ProtocolAnthropicMessages, []byte(`{"messages": [`)),
	} {
		if empty.Items == nil || empty.Writes == nil || len(empty.Items)+len(empty.Writes) != 0 || empty.Summary != "" {
			t.Fatalf("unreadable body = %#v", empty)
		}
	}
}
