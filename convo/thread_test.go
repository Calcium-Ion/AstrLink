package convo_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/convo"
	"github.com/QuantumNous/astrlink/convo/memindex"
)

// threadHost plays a gateway that names every call of one client thread with
// the same session header, as Claude Code does with its session id.
type threadHost struct {
	t        *testing.T
	policy   convo.Policy
	fp       *convo.Fingerprinter
	index    *memindex.Index
	now      time.Time
	sessions int
}

func newThreadHost(t *testing.T) *threadHost {
	policy := convo.DefaultPolicy()
	policy.ToolFreeHelpers = true
	return &threadHost{
		t:      t,
		policy: policy,
		fp:     convo.NewFingerprinter([]byte("thread-test-key")),
		index:  memindex.New(memindex.Options{}),
		now:    time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC),
	}
}

// call resolves one Anthropic Messages body sent under cursor and stores it.
func (host *threadHost) call(cursor string, body map[string]any, sideCall bool) (string, convo.Decision) {
	host.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		host.t.Fatal(err)
	}
	summary, err := host.policy.InspectBody(convo.AnthropicMessages, raw)
	if err != nil {
		host.t.Fatal(err)
	}
	summary.SessionCursor = cursor
	summary.SideCall = sideCall
	host.now = host.now.Add(time.Second)
	decision, err := host.policy.Resolve(context.Background(), summary, host.fp, host.index.LookupFor("token"), host.now)
	if err != nil {
		host.t.Fatal(err)
	}
	session := decision.Match.SessionID
	if !decision.Matched {
		host.sessions++
		session = fmt.Sprintf("session_%d", host.sessions)
	}
	host.index.Put(memindex.Record{
		SessionID: session, Principal: "token", Turn: decision.Turn, At: host.now,
		Cursors: decision.PersistentInbound(),
	})
	return session, decision
}

var threadTools = []any{map[string]any{"name": "Bash", "input_schema": map[string]any{"type": "object"}}}

func user(text string) map[string]any { return map[string]any{"role": "user", "content": text} }

func assistant(text string) map[string]any {
	return map[string]any{"role": "assistant", "content": text}
}

func toolUse(id string) map[string]any {
	return map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{}},
	}}
}

func toolResult(id string) map[string]any {
	return map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": id, "content": "ok"},
	}}
}

func withTools(messages ...map[string]any) map[string]any {
	return map[string]any{"tools": threadTools, "messages": messages}
}

func turnIndex(t *testing.T, decision convo.Decision) int {
	t.Helper()
	if decision.Turn == nil {
		t.Fatal("decision has no turn")
	}
	return decision.Turn.Index
}

func TestResolveHelperCallsKeepTheThreadTurn(t *testing.T) {
	host := newThreadHost(t)
	const cursor = "6b0c2f3e-5a8d-4c1b-9e7f-2d4a6c8e0b13"
	prompt := user("Fix the flaky retry test in the gateway")
	first, opened := host.call(cursor, withTools(prompt), false)
	if opened.Matched || turnIndex(t, opened) != 1 {
		t.Fatalf("first call = %+v", opened)
	}

	// A title helper shares the session header but replays none of the
	// thread and offers no tools: it stays in the turn and moves nothing.
	title := map[string]any{"messages": []any{user("<session>\nFix the flaky retry test\n</session>\nWrite the title in English.")}}
	session, helper := host.call(cursor, title, false)
	if session != first || !helper.SideCall || turnIndex(t, helper) != 1 || helper.Turn.UserMessages != 1 {
		t.Fatalf("title helper = %s %+v", session, helper)
	}

	// The thread's next call continues the call before the helper.
	_, loop := host.call(cursor, withTools(prompt, toolUse("toolu_01A9xQ7rT3mK5pL8wZ2cV6bN"), toolResult("toolu_01A9xQ7rT3mK5pL8wZ2cV6bN")), false)
	if loop.SideCall || turnIndex(t, loop) != 1 {
		t.Fatalf("loop call after the helper = %+v", loop)
	}

	// A host-marked helper with tools still keeps the turn.
	_, marked := host.call(cursor, withTools(user("Generate a title for this conversation:"), prompt), true)
	if !marked.SideCall || turnIndex(t, marked) != 1 {
		t.Fatalf("marked helper = %+v", marked)
	}

	_, next := host.call(cursor, withTools(prompt, toolUse("toolu_01A9xQ7rT3mK5pL8wZ2cV6bN"), toolResult("toolu_01A9xQ7rT3mK5pL8wZ2cV6bN"),
		assistant("Fixed."), user("Now run the whole suite")), false)
	if next.SideCall || turnIndex(t, next) != 2 {
		t.Fatalf("second prompt = %+v", next)
	}
}

func TestResolveForkThatEndsDoesNotCountATurn(t *testing.T) {
	host := newThreadHost(t)
	const cursor = "1f6d8a2c-3b5e-4f7a-8c9d-0e1f2a3b4c5d"
	prompt := user("Summarize the open pull requests")
	host.call(cursor, withTools(prompt), false)

	// A forked helper replays the thread and appends its own instruction,
	// which reads as a new turn until the thread moves on without it.
	_, fork := host.call(cursor, withTools(prompt, assistant("There are three."), user("[SUGGESTION MODE: Suggest what the user might type next.]")), false)
	if turnIndex(t, fork) != 2 {
		t.Fatalf("fork = %+v", fork)
	}
	_, next := host.call(cursor, withTools(prompt, assistant("There are three."), user("Close the stale one")), false)
	if next.SideCall || turnIndex(t, next) != 2 || next.Turn.UserMessages != 2 {
		t.Fatalf("real next prompt = %+v, want turn 2 continued from the first call", next)
	}
	_, loop := host.call(cursor, withTools(prompt, assistant("There are three."), user("Close the stale one"),
		toolUse("toolu_07Hq2Lm9Pz4Xc1Vb8Nk3Jd6F"), toolResult("toolu_07Hq2Lm9Pz4Xc1Vb8Nk3Jd6F")), false)
	if turnIndex(t, loop) != 2 {
		t.Fatalf("loop call = %+v", loop)
	}
}

func TestResolveMovedThreadStartsATurn(t *testing.T) {
	host := newThreadHost(t)
	const cursor = "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
	host.call(cursor, withTools(user("Port the parser to Rust")), false)
	// After a compaction the history starts over from a summary, so it
	// continues no stored call; with tools it is the conversation going on.
	_, compacted := host.call(cursor, withTools(user("This session is being continued from a previous conversation."), user("Keep going")), false)
	if compacted.SideCall || turnIndex(t, compacted) != 2 {
		t.Fatalf("compacted call = %+v", compacted)
	}
	_, loop := host.call(cursor, withTools(user("This session is being continued from a previous conversation."), user("Keep going"),
		toolUse("toolu_02Rt5Yu8Io1Pa4Sd7Fg0Hj3K"), toolResult("toolu_02Rt5Yu8Io1Pa4Sd7Fg0Hj3K")), false)
	if turnIndex(t, loop) != 2 {
		t.Fatalf("loop after compaction = %+v", loop)
	}
}

func TestResolveHelperThatOpensTheSession(t *testing.T) {
	host := newThreadHost(t)
	const cursor = "0d1c2b3a-4e5f-4a6b-9c7d-8e9f0a1b2c3d"
	// The title request can reach the gateway before the first real call.
	first, helper := host.call(cursor, map[string]any{"messages": []any{user("Generate a title for this conversation:"), user("Add dark mode")}}, true)
	if helper.Matched || !helper.SideCall || helper.Turn == nil || *helper.Turn != (convo.TurnState{Index: 1}) {
		t.Fatalf("opening helper = %+v", helper)
	}
	session, main := host.call(cursor, withTools(user("Add dark mode")), false)
	if session != first || main.SideCall || turnIndex(t, main) != 1 || main.Turn.UserMessages != 1 || main.Turn.LastUserFingerprint == "" {
		t.Fatalf("first real call = %s %+v", session, main)
	}
	_, next := host.call(cursor, withTools(user("Add dark mode"), assistant("Done."), user("Also the settings page")), false)
	if turnIndex(t, next) != 2 {
		t.Fatalf("second prompt = %+v", next)
	}
}

func TestResolveToolFreeCallsOfChatClientsMoveTheConversationOn(t *testing.T) {
	host := newThreadHost(t)
	host.policy.ToolFreeHelpers = false
	const cursor = "chat-conversation-1"
	host.call(cursor, map[string]any{"messages": []any{user("Draft a haiku about rain")}}, false)
	// The user edited their only message: it continues nothing stored, and
	// a chat client offers no tools on any call.
	_, edited := host.call(cursor, map[string]any{"messages": []any{user("Draft a haiku about snow")}}, false)
	if edited.SideCall || turnIndex(t, edited) != 2 {
		t.Fatalf("edited first message = %+v", edited)
	}
}

func TestResolveChecksWhatTheLookupReturns(t *testing.T) {
	policy := convo.DefaultPolicy()
	policy.ToolFreeHelpers = true
	fp := convo.NewFingerprinter([]byte("k"))
	raw, _ := json.Marshal(map[string]any{"messages": []any{user("Draft the release notes")}})
	summary, err := policy.InspectBody(convo.AnthropicMessages, raw)
	if err != nil {
		t.Fatal(err)
	}
	summary.SessionCursor = "thread-1"
	stale := convo.Match{SessionID: "s1", Turn: &convo.TurnState{Index: 4, UserMessages: 3, LastUserFingerprint: "fp1_other"}}
	var scopes []convo.Scope
	// A host that ignores Scope.Continues returns the same stale record.
	lookup := func(_ context.Context, kind convo.Kind, _ []string, scope convo.Scope) (convo.Match, bool, error) {
		scopes = append(scopes, scope)
		return stale, kind == convo.KindExplicit, nil
	}
	decision, err := policy.Resolve(context.Background(), summary, fp, lookup, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 2 || scopes[1].Continues == nil || len(scopes[1].Continues.Points) != 1 {
		t.Fatalf("lookups = %+v", scopes)
	}
	if !decision.SideCall || decision.Turn == nil || *decision.Turn != *stale.Turn {
		t.Fatalf("decision = %+v, want a helper call keeping the stale turn", decision)
	}
}

func TestInspectUserDigestsAndTools(t *testing.T) {
	policy := convo.DefaultPolicy()
	policy.MaxContinuationUsers = 2
	body := `{"functions":[{"name":"f"}],"messages":[
		{"role":"user","content":"one"},
		{"role":"assistant","content":"a"},
		{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:,"}}]},
		{"role":"assistant","content":"b"},
		{"role":"user","content":"three"}]}`
	summary, err := policy.InspectBody(convo.OpenAIChat, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if !summary.HasTools || summary.UserTurnCount != 3 || len(summary.UserDigests) != 2 {
		t.Fatalf("summary = %+v", summary)
	}
	// The image-only message carries the text before it.
	if string(summary.UserDigests[0]) != string(convo.DigestText("one", false)) ||
		string(summary.UserDigests[1]) != string(summary.LastUserDigest) {
		t.Fatalf("digests do not follow the newest user text")
	}

	lite := `{"input":[{"type":"additional_tools","tools":[{"type":"function","name":"shell"}]},{"role":"user","content":"hi"}]}`
	if summary, _ := policy.InspectBody(convo.OpenAIResponses, []byte(lite)); !summary.HasTools {
		t.Fatal("Responses Lite tools in the input were missed")
	}
	if summary, _ := policy.InspectBody(convo.AnthropicMessages, []byte(`{"tools":[],"messages":[{"role":"user","content":"hi"}]}`)); summary.HasTools {
		t.Fatal("an empty tool list offers no tools")
	}
	stateful := `{"previous_response_id":"resp_1","input":[{"role":"user","content":"next"}]}`
	if summary, _ := policy.InspectBody(convo.OpenAIResponses, []byte(stateful)); summary.UserDigests != nil {
		t.Fatal("a stateful delta has no positions to compare")
	}
}
