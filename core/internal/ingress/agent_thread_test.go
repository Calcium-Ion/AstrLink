package ingress

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	claudeCodeTestSession = "5d3c1b2a-9e8f-4a7b-8c6d-0e1f2a3b4c5d"
	claudeCodeTestAgent   = "a3f9c2e1b4d6a8c0e"
	anthropicTestReply    = `{"id":"msg_01Jx9Lk2Pq7Zr4Vn8Bc3Wd6T","type":"message","role":"assistant","content":[{"type":"text","text":"Done."}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":2}}`
)

// claudeCodeBody is a Claude Code Messages body: metadata naming the session,
// the tool list unless tools is false, and the given messages.
func claudeCodeBody(t *testing.T, tools bool, system string, messages ...any) string {
	t.Helper()
	userID, _ := json.Marshal(map[string]string{"device_id": "d", "account_uuid": "", "session_id": claudeCodeTestSession})
	body := map[string]any{
		"model": "public-alias", "max_tokens": 1024, "messages": messages,
		"metadata": map[string]any{"user_id": string(userID)},
	}
	if tools {
		body["tools"] = []any{map[string]any{"name": "Agent", "input_schema": map[string]any{"type": "object"}}}
	}
	if system != "" {
		body["system"] = []any{map[string]any{"type": "text", "text": system}}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func textMessage(role, text string) map[string]any {
	return map[string]any{"role": role, "content": text}
}

func toolUseMessage(id string) map[string]any {
	return map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "tool_use", "id": id, "name": "Agent", "input": map[string]any{"prompt": "Find the router files"}},
	}}
}

func toolResultMessage(id string) map[string]any {
	return map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": id, "content": "router.go"},
	}}
}

func assertParent(t *testing.T, record contract.RequestRecord, parent contract.RequestRecord) {
	t.Helper()
	if record.ParentSessionID == nil || parent.SessionID == nil || *record.ParentSessionID != *parent.SessionID {
		t.Fatalf("parent_session_id=%v, want %v", record.ParentSessionID, parent.SessionID)
	}
}

func assertNoPreview(t *testing.T, record contract.RequestRecord) {
	t.Helper()
	if record.InputPreview != nil {
		t.Fatalf("helper call carries a preview %q", *record.InputPreview)
	}
}

func TestClaudeCodeSubagentGetsItsOwnConversation(t *testing.T) {
	harness := newSessionHarness(t, contract.ProtocolAnthropicMessages, "/v1/messages", sessionHarnessOptions{auditBlobs: &memoryAuditBlobs{}})
	harness.headers = http.Header{}
	harness.headers.Set("User-Agent", "claude-cli/2.1.310 (external, cli)")
	harness.headers.Set("X-Claude-Code-Session-Id", claudeCodeTestSession)
	prompt := textMessage("user", "Refactor the request router")
	const toolID = "toolu_01Hq8Zx3Lm5Pk9Rt2Vb7Nc4D"

	main := harness.serve(claudeCodeBody(t, true, "", prompt), anthropicTestReply)
	assertTurn(t, main, 1)

	harness.headers.Set(claudeCodeAgentHeader, claudeCodeTestAgent)
	task := textMessage("user", "<system-reminder>Context.</system-reminder>\nFind the router files")
	sub := harness.serve(claudeCodeBody(t, true, "", task), anthropicTestReply)
	assertNewSession(t, main, sub)
	assertParent(t, sub, main)
	assertTurn(t, sub, 1)
	if sub.InputPreview == nil || *sub.InputPreview != "Find the router files" {
		t.Fatalf("subagent preview=%v", sub.InputPreview)
	}
	subLoop := harness.serve(claudeCodeBody(t, true, "", task, toolUseMessage("toolu_01Sb7Kx2Qw9Ez4Rt6Yu1Io3P"), toolResultMessage("toolu_01Sb7Kx2Qw9Ez4Rt6Yu1Io3P")), anthropicTestReply)
	assertSameSession(t, sub, subLoop)
	assertParent(t, subLoop, main)
	assertTurn(t, subLoop, 1)

	// A nested subagent hangs off the subagent that started it.
	harness.headers.Set(claudeCodeAgentHeader, "a7e1d4c9b2f60a3b5")
	harness.headers.Set(claudeCodeParentAgentHeader, claudeCodeTestAgent)
	nested := harness.serve(claudeCodeBody(t, true, "", textMessage("user", "Read router.go")), anthropicTestReply)
	assertNewSession(t, sub, nested)
	assertParent(t, nested, sub)

	// The main thread goes on in its own turn: the subagent's calls under
	// the same session id moved nothing.
	harness.headers.Del(claudeCodeAgentHeader)
	harness.headers.Del(claudeCodeParentAgentHeader)
	mainLoop := harness.serve(claudeCodeBody(t, true, "", prompt, toolUseMessage(toolID), toolResultMessage(toolID)), anthropicTestReply)
	assertSameSession(t, main, mainLoop)
	assertTurn(t, mainLoop, 1)
	if mainLoop.ParentSessionID != nil {
		t.Fatalf("main thread got a parent %v", *mainLoop.ParentSessionID)
	}

	// Its title request shares the session but is a helper call.
	title := harness.serve(claudeCodeBody(t, false, "You are naming a coding session so the user can pick it out.",
		textMessage("user", "<session>\nRefactor the request router\n</session>\nWrite the title in English.")), anthropicTestReply)
	assertSameSession(t, main, title)
	assertTurn(t, title, 1)
	assertNoPreview(t, title)

	next := harness.serve(claudeCodeBody(t, true, "", prompt, toolUseMessage(toolID), toolResultMessage(toolID),
		textMessage("assistant", "Refactored."), textMessage("user", "Now add tests")), anthropicTestReply)
	assertSameSession(t, main, next)
	assertTurn(t, next, 2)
}

func TestClaudeCodeTitleBeforeTheFirstCallDoesNotCountATurn(t *testing.T) {
	harness := newSessionHarness(t, contract.ProtocolAnthropicMessages, "/v1/messages", sessionHarnessOptions{auditBlobs: &memoryAuditBlobs{}})
	harness.headers = http.Header{}
	harness.headers.Set("User-Agent", "claude-cli/2.1.310 (external, cli)")
	title := harness.serve(claudeCodeBody(t, false, "You are naming a coding session so the user can pick it out.",
		textMessage("user", "<session>\nAdd dark mode\n</session>\nWrite the title in English.")), anthropicTestReply)
	assertTurn(t, title, 1)
	assertNoPreview(t, title)
	main := harness.serve(claudeCodeBody(t, true, "", textMessage("user", "Add dark mode")), anthropicTestReply)
	assertSameSession(t, title, main)
	assertTurn(t, main, 1)
	if main.InputPreview == nil || *main.InputPreview != "Add dark mode" {
		t.Fatalf("first real call preview=%v", main.InputPreview)
	}
}

const codexTestReply = `{"id":"resp_0a1b2c3d","output":[{"type":"message","id":"msg_7f6e5d4c3b2a1908","role":"assistant","content":[{"type":"output_text","text":"Done."}]}]}`

func codexBody(t *testing.T, cacheKey string, input ...any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"model": "public-alias", "prompt_cache_key": cacheKey, "input": input,
		"tools": []any{map[string]any{"type": "function", "name": "shell"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestCodexThreadHeadersSeparateSubagents(t *testing.T) {
	const root = "019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5b"
	const child = "019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a6c"
	for _, spelling := range []struct{ name, session, thread string }{
		{"hyphenated", "Session-Id", "Thread-Id"},
		{"underscore", "Session_id", "Thread_id"},
	} {
		t.Run(spelling.name, func(t *testing.T) {
			harness := newSessionHarness(t, contract.ProtocolOpenAIResponses, "/v1/responses", sessionHarnessOptions{auditBlobs: &memoryAuditBlobs{}})
			harness.headers = http.Header{}
			harness.headers.Set("User-Agent", "codex_cli_rs/0.164.0 (Mac OS 26.0.0; arm64)")
			harness.headers[spelling.session] = []string{root}
			harness.headers[spelling.thread] = []string{root}
			prompt := textMessage("user", "Fix the session header grouping")
			main := harness.serve(codexBody(t, root, prompt), codexTestReply)
			assertTurn(t, main, 1)

			// A spawned thread shares the root id and cache key but names
			// its own thread and its parent.
			harness.headers[spelling.thread] = []string{child}
			harness.headers.Set(codexParentThreadHeader, root)
			sub := harness.serve(codexBody(t, root, textMessage("user", "Find every reader of the session header")), codexTestReply)
			assertNewSession(t, main, sub)
			assertParent(t, sub, main)
			again := harness.serve(codexBody(t, root, textMessage("user", "Find every reader of the session header"),
				map[string]any{"type": "function_call", "call_id": "call_Qz8Lk3Mx7Pw2Rt5Vb9Nc", "name": "shell", "arguments": "{}"},
				map[string]any{"type": "function_call_output", "call_id": "call_Qz8Lk3Mx7Pw2Rt5Vb9Nc", "output": "ok"}), codexTestReply)
			assertSameSession(t, sub, again)

			harness.headers[spelling.thread] = []string{root}
			harness.headers.Del(codexParentThreadHeader)
			mainLoop := harness.serve(codexBody(t, root, prompt,
				map[string]any{"type": "function_call", "call_id": "call_Ab3Cd5Ef7Gh9Ij1Kl2Mn", "name": "spawn_agent", "arguments": "{}"},
				map[string]any{"type": "function_call_output", "call_id": "call_Ab3Cd5Ef7Gh9Ij1Kl2Mn", "output": "spawned"}), codexTestReply)
			assertSameSession(t, main, mainLoop)
			assertTurn(t, mainLoop, 1)

			// The TUI title runs as a fresh hidden thread that quotes the
			// prompt; it joins the conversation it names as a helper call.
			harness.headers[spelling.session] = []string{"019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a7d"}
			harness.headers[spelling.thread] = []string{"019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a7d"}
			harness.headers.Set(codexTurnMetadataHeader, `{"thread_source":"thread_title","request_kind":"turn"}`)
			title := harness.serve(codexBody(t, "019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a7d",
				textMessage("user", "Generate a concise, single-line task title of at most 36 characters.\n\nUser prompt:\nFix the session header grouping")), codexTestReply)
			assertSameSession(t, main, title)
			assertTurn(t, title, 1)
			assertNoPreview(t, title)
		})
	}
}

func TestCodexLegacySessionHeaderStillGroups(t *testing.T) {
	harness := newSessionHarness(t, contract.ProtocolOpenAIResponses, "/v1/responses", sessionHarnessOptions{})
	harness.headers = http.Header{}
	harness.headers.Set("Session_id", "legacy-thread")
	first := harness.serve(codexBody(t, "shared", textMessage("user", "first")), codexTestReply)
	harness.headers.Set("Session_id", "legacy-child")
	harness.headers.Set(codexParentThreadHeader, "legacy-thread")
	child := harness.serve(codexBody(t, "shared", textMessage("user", "child task")), codexTestReply)
	assertNewSession(t, first, child)
	assertParent(t, child, first)
	harness.headers.Set("Session_id", "legacy-thread")
	harness.headers.Del(codexParentThreadHeader)
	again := harness.serve(codexBody(t, "shared", textMessage("user", "first"), textMessage("assistant", "ok"), textMessage("user", "second")), codexTestReply)
	assertSameSession(t, first, again)
	assertTurn(t, again, 2)
}

const chatTestReply = `{"id":"chatcmpl-test","choices":[{"index":0,"message":{"role":"assistant","content":"Done."}}]}`

func chatBody(t *testing.T, tools bool, messages ...any) string {
	t.Helper()
	return chatRequest(t, false, tools, messages...)
}

func chatRequest(t *testing.T, stream, tools bool, messages ...any) string {
	t.Helper()
	body := map[string]any{"model": "public-alias", "messages": messages, "stream": stream}
	if tools {
		body["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{"name": "bash"}}}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestOpenCodeChildSessionLinksToItsParent(t *testing.T) {
	harness := newSessionHarness(t, contract.ProtocolOpenAIChat, "/v1/chat/completions", sessionHarnessOptions{auditBlobs: &memoryAuditBlobs{}})
	harness.headers = http.Header{}
	harness.headers.Set("User-Agent", "opencode/1.19.4 ai-sdk/provider-utils/3.0.0")
	harness.headers.Set(openCodeSessionHeader, "ses_parent0123456789")
	// The title request may arrive before the first real call.
	title := harness.serve(chatBody(t, false, textMessage("user", "Generate a title for this conversation:\n"), textMessage("user", "Add dark mode")), chatTestReply)
	assertNoPreview(t, title)
	main := harness.serve(chatBody(t, true, textMessage("user", "Add dark mode")), chatTestReply)
	assertSameSession(t, title, main)
	assertTurn(t, main, 1)
	if main.InputPreview == nil {
		t.Fatal("first real call lost its preview")
	}

	harness.headers.Set(openCodeSessionHeader, "ses_child0123456789")
	harness.headers.Set("X-Opencode-Parent-Session-Id", "ses_parent0123456789")
	child := harness.serve(chatBody(t, true, textMessage("user", "Find the theme tokens")), chatTestReply)
	assertNewSession(t, main, child)
	assertParent(t, child, main)

	harness.headers.Set(openCodeSessionHeader, "ses_other0123456789")
	harness.headers.Del("X-Opencode-Parent-Session-Id")
	harness.headers.Set("X-Parent-Session-Id", "ses_parent0123456789")
	other := harness.serve(chatBody(t, true, textMessage("user", "Check contrast")), chatTestReply)
	assertParent(t, other, main)
}

const deepSeekHarnessParentNote = `Your parent agent id is "ses_dsh_parent_01". Before you finish, send your result to that agent with send_message({ agent_id: "ses_dsh_parent_01", message: "<self-contained result>" }).`

func deepSeekRuntimeNote() map[string]any {
	return textMessage("user", "Current runtime context. This snapshot supersedes earlier runtime-context snapshots.")
}

func TestDeepSeekHarnessSubagentLinksByItsPrompt(t *testing.T) {
	blobs := &memoryAuditBlobs{}
	harness := newSessionHarness(t, contract.ProtocolOpenAIChat, "/v1/chat/completions", sessionHarnessOptions{auditBlobs: blobs, streaming: true})
	harness.headers = http.Header{}
	harness.headers.Set("User-Agent", "deepseek-harness/0.2.1-rc.3 (darwin; arm64)")
	const prompt = "Reply with exactly: child answer 42."
	opening := textMessage("user", "Use the subagent tool once with the prompt: "+prompt)
	// The tool call's arguments stream in pieces.
	spawnStream := `data: {"id":"chatcmpl-p1","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_00_oHPNQ1nLoakoaAGXIxCM7404","type":"function","function":{"name":"subagent","arguments":""}}]}}]}` + "\n\n" +
		`data: {"id":"chatcmpl-p1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"description\":\"echo probe\",\"prompt\":\"Reply with exactly:"}}]}}]}` + "\n\n" +
		`data: {"id":"chatcmpl-p1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":" child answer 42.\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n" +
		"data: [DONE]\n\n"
	parent := harness.serve(chatRequest(t, true, true, opening, deepSeekRuntimeNote()), spawnStream)
	for _, cursor := range parent.Cursors {
		if strings.Contains(cursor.Value, "child answer") {
			t.Fatalf("a cursor stored the prompt: %#v", cursor)
		}
	}

	childOpener := map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "text", "text": prompt},
		map[string]any{"type": "text", "text": deepSeekHarnessParentNote},
	}}
	child := harness.serve(chatRequest(t, true, true, childOpener, deepSeekRuntimeNote()), "data: {\"id\":\"chatcmpl-c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"child answer 42.\"}}]}\n\ndata: [DONE]\n\n")
	assertNewSession(t, parent, child)
	assertParent(t, child, parent)
	for _, cursor := range child.Cursors {
		if strings.Contains(cursor.Value, "child answer") || strings.Contains(cursor.Value, "ses_dsh_parent_01") {
			t.Fatalf("a cursor stored client text: %#v", cursor)
		}
	}
	childNext := harness.serve(chatRequest(t, true, true, childOpener, deepSeekRuntimeNote(), textMessage("assistant", "child answer 42."), textMessage("user", "Also say goodbye")),
		"data: {\"id\":\"chatcmpl-c2\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Goodbye.\"}}]}\n\ndata: [DONE]\n\n")
	assertSameSession(t, child, childNext)
	assertParent(t, childNext, parent)

	// The parent's next call is its own conversation's, not the child's.
	parentNext := harness.serve(chatRequest(t, true, true, opening, deepSeekRuntimeNote(),
		map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "call_00_oHPNQ1nLoakoaAGXIxCM7404", "type": "function", "function": map[string]any{"name": "subagent", "arguments": "{}"}}}},
		map[string]any{"role": "tool", "tool_call_id": "call_00_oHPNQ1nLoakoaAGXIxCM7404", "content": "started subagent ses_dsh_child_01"}),
		"data: {\"id\":\"chatcmpl-p2\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Waiting.\"}}]}\n\ndata: [DONE]\n\n")
	assertSameSession(t, parent, parentNext)
	if parentNext.ParentSessionID != nil {
		t.Fatalf("parent conversation got a parent %v", *parentNext.ParentSessionID)
	}

	// The title request quotes the opening message and joins it.
	title := harness.serve(chatRequest(t, true, false, textMessage("user", deepSeekHarnessTitlePrefix+"\n"+`[{"seq":8,"text":"Use the subagent tool once with the prompt: `+prompt+`"}]`)),
		"data: {\"id\":\"chatcmpl-t\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Echo probe\"}}]}\n\ndata: [DONE]\n\n")
	assertSameSession(t, parent, title)
	assertNoPreview(t, title)
	assertTurn(t, title, *parentNext.TurnIndex)
}

func TestDeepSeekHarnessForkIsNotMergedIntoItsParent(t *testing.T) {
	harness := newSessionHarness(t, contract.ProtocolOpenAIChat, "/v1/chat/completions", sessionHarnessOptions{auditBlobs: &memoryAuditBlobs{}})
	harness.headers = http.Header{}
	harness.headers.Set("User-Agent", "deepseek-harness/0.2.1-rc.3 (darwin; arm64)")
	remember := textMessage("user", "Remember this fact for later: the project codeword is MARMALADE.")
	first := harness.serve(chatBody(t, true, remember, deepSeekRuntimeNote()),
		`{"id":"chatcmpl-f1","choices":[{"index":0,"message":{"role":"assistant","content":"`+longAssistantReply+`"}}]}`)
	const prompt = "What is the project codeword mentioned earlier in this conversation?"
	spawn := harness.serve(chatBody(t, true, remember, deepSeekRuntimeNote(), textMessage("assistant", longAssistantReply), textMessage("user", "Use subagent_fork once.")),
		`{"id":"chatcmpl-f2","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_00_sAtKUseRzHRBvL4CF7XF1334","type":"function","function":{"name":"subagent_fork","arguments":"{\"prompt\":\"`+prompt+`\"}"}}]}}]}`)
	assertSameSession(t, first, spawn)

	// The fork replays the parent's finished turn, whose reply would link it
	// to the parent by fingerprint; its opening note makes it a child.
	fork := harness.serve(chatBody(t, true, remember, deepSeekRuntimeNote(), textMessage("assistant", longAssistantReply),
		textMessage("user", prompt+deepSeekHarnessParentNote), deepSeekRuntimeNote()), chatTestReply)
	assertNewSession(t, first, fork)
	assertParent(t, fork, first)
}
