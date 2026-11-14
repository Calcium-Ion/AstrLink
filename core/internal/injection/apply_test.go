package injection

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/tidwall/gjson"
)

func apply(t *testing.T, ledger *Ledger, protocol contract.ProtocolID, session, body string, plugins ...Plugin) Result {
	t.Helper()
	result, err := ledger.Apply(Request{Protocol: protocol, Session: session, Body: []byte(body), Plugins: plugins})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !json.Valid(result.Body) {
		t.Fatalf("Apply made invalid JSON: %s", result.Body)
	}
	return result
}

func settled(block string) Plugin {
	return Plugin{Key: "memory", Settled: true, Turn: block}
}

func TestApplyPutsTheTurnBlockAtTheOpeningMessage(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol contract.ProtocolID
		body     string
		want     string
	}{
		{
			"anthropic string content",
			contract.ProtocolAnthropicMessages,
			`{"model":"m","messages":[{"role":"user","content":"改一下分页"}]}`,
			`{"model":"m","messages":[{"role":"user","content":"<m>\n- a\n</m>\n\n改一下分页"}]}`,
		},
		{
			"anthropic agent loop: the opening message, not the tool result",
			contract.ProtocolAnthropicMessages,
			`{"messages":[{"role":"user","content":[{"type":"text","text":"改一下分页"}]},{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}]}`,
			`{"messages":[{"role":"user","content":[{"type":"text","text":"<m>\n- a\n</m>"},{"type":"text","text":"改一下分页"}]},{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}]}`,
		},
		{
			"anthropic words typed beside tool results go after them",
			contract.ProtocolAnthropicMessages,
			`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"},{"type":"text","text":"顺便看看分页"}]}]}`,
			`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"},{"type":"text","text":"<m>\n- a\n</m>"},{"type":"text","text":"顺便看看分页"}]}]}`,
		},
		{
			"chat parts",
			contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"system","content":"s"},{"role":"user","content":[{"type":"text","text":"改一下分页"},{"type":"image_url","image_url":{"url":"data:,"}}]},{"role":"assistant","tool_calls":[{"id":"c1"}]},{"role":"tool","tool_call_id":"c1","content":"ok"}]}`,
			`{"messages":[{"role":"system","content":"s"},{"role":"user","content":[{"type":"text","text":"<m>\n- a\n</m>"},{"type":"text","text":"改一下分页"},{"type":"image_url","image_url":{"url":"data:,"}}]},{"role":"assistant","tool_calls":[{"id":"c1"}]},{"role":"tool","tool_call_id":"c1","content":"ok"}]}`,
		},
		{
			"chat string keeps the client's escapes",
			contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"user","content":"改 \"a\"\/b"}]}`,
			`{"messages":[{"role":"user","content":"<m>\n- a\n</m>\n\n改 \"a\"\/b"}]}`,
		},
		{
			"responses bare input",
			contract.ProtocolOpenAIResponses,
			`{"model":"m","input":"改一下分页"}`,
			`{"model":"m","input":"<m>\n- a\n</m>\n\n改一下分页"}`,
		},
		{
			"responses message items",
			contract.ProtocolOpenAIResponses,
			`{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"改一下分页"}]},{"type":"function_call","call_id":"c1","name":"read","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"ok"}]}`,
			`{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"<m>\n- a\n</m>"},{"type":"input_text","text":"改一下分页"}]},{"type":"function_call","call_id":"c1","name":"read","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"ok"}]}`,
		},
		{
			"gemini parts after function responses",
			contract.ProtocolGoogleGenerateContent,
			`{"contents":[{"role":"user","parts":[{"functionResponse":{"name":"read","response":{}}},{"text":"改一下分页"}]}]}`,
			`{"contents":[{"role":"user","parts":[{"functionResponse":{"name":"read","response":{}}},{"text":"<m>\n- a\n</m>"},{"text":"改一下分页"}]}]}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := apply(t, NewLedger(), test.protocol, "s1", test.body, settled("<m>\n- a\n</m>"))
			if string(result.Body) != test.want {
				t.Fatalf("body\n got %s\nwant %s", result.Body, test.want)
			}
			if outcome := result.Outcomes[0]; outcome.TurnBytes != len("<m>\n- a\n</m>") || outcome.Replayed != 0 || outcome.NoOpening {
				t.Fatalf("outcome = %+v", outcome)
			}
		})
	}
}

func TestApplyReplaysEarlierBlocksByteForByte(t *testing.T) {
	ledger := NewLedger()
	turn1 := `{"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"改一下分页","cache_control":{"type":"ephemeral"}}]}]}`
	first := apply(t, ledger, contract.ProtocolAnthropicMessages, "s1", turn1,
		Plugin{Key: "memory", Resident: "<ctx>项目快照</ctx>", Settled: true, Turn: "<m>A</m>"})
	// The next call of the turn appends a tool round and settles nothing.
	call2 := `{"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"改一下分页"}]},{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok","cache_control":{"type":"ephemeral"}}]}]}`
	second := apply(t, ledger, contract.ProtocolAnthropicMessages, "s1", call2,
		Plugin{Key: "memory", Resident: "<ctx>项目快照</ctx>"})
	if got := gjson.GetBytes(second.Body, "messages.0.content.0.text").Str; got != "<m>A</m>" {
		t.Fatalf("the turn's later call lost its block: %s", second.Body)
	}
	if outcome := second.Outcomes[0]; outcome.TurnBytes != len("<m>A</m>") || outcome.Replayed != 0 {
		t.Fatalf("later call outcome = %+v", outcome)
	}
	// Turn 2: the client replays turn 1 without the block and opens a new turn.
	turn2 := `{"system":[{"type":"text","text":"s","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"改一下分页"}]},{"role":"assistant","content":[{"type":"text","text":"好了"}]},{"role":"user","content":[{"type":"text","text":"再加个测试","cache_control":{"type":"ephemeral"}}]}]}`
	third := apply(t, ledger, contract.ProtocolAnthropicMessages, "s1", turn2,
		Plugin{Key: "memory", Resident: "<ctx>项目快照</ctx>", Settled: true, Turn: "<m>B</m>"})
	firstMessage := func(body []byte) string { return gjson.GetBytes(body, "messages.0").Raw }
	if firstMessage(third.Body) != firstMessage(second.Body) {
		t.Fatalf("turn 1 changed bytes:\n%s\n%s", firstMessage(second.Body), firstMessage(third.Body))
	}
	if gjson.GetBytes(third.Body, "messages.2.content.0.text").Str != "<m>B</m>" {
		t.Fatalf("turn 2 block missing: %s", third.Body)
	}
	if outcome := third.Outcomes[0]; outcome.Replayed != 1 || outcome.TurnBytes != len("<m>B</m>") || outcome.ResidentBytes != len("<ctx>项目快照</ctx>") {
		t.Fatalf("turn 2 outcome = %+v", outcome)
	}
	system := gjson.GetBytes(third.Body, "system").Raw
	if system != gjson.GetBytes(first.Body, "system").Raw || !strings.HasSuffix(system, `{"text":"<ctx>项目快照</ctx>","type":"text"}]`) {
		t.Fatalf("resident block = %s", system)
	}
}

func TestApplyKeepsRepeatedMessagesApart(t *testing.T) {
	ledger := NewLedger()
	turn := func(texts ...string) string {
		var messages []string
		for index, text := range texts {
			if index > 0 {
				messages = append(messages, `{"role":"assistant","content":"好"}`)
			}
			messages = append(messages, `{"role":"user","content":"`+text+`"}`)
		}
		return `{"messages":[` + strings.Join(messages, ",") + `]}`
	}
	apply(t, ledger, contract.ProtocolOpenAIChat, "s1", turn("继续"), settled("A"))
	apply(t, ledger, contract.ProtocolOpenAIChat, "s1", turn("继续", "继续"), settled("B"))
	result := apply(t, ledger, contract.ProtocolOpenAIChat, "s1", turn("继续", "继续", "看看"), settled(""))
	for index, want := range map[int]string{0: "A\n\n继续", 2: "B\n\n继续", 4: "看看"} {
		if got := gjson.GetBytes(result.Body, "messages."+string(rune('0'+index))+".content").Str; got != want {
			t.Fatalf("message %d = %q, want %q", index, got, want)
		}
	}
	if outcome := result.Outcomes[0]; outcome.Replayed != 2 || outcome.TurnBytes != 0 {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestApplySettlingNoBlockForgetsAnOlderOne(t *testing.T) {
	ledger := NewLedger()
	body := `{"messages":[{"role":"user","content":"继续"}]}`
	apply(t, ledger, contract.ProtocolOpenAIChat, "s1", body, settled("A"))
	// The client compacted its history and the same words open a turn
	// again; this time the plugin has nothing.
	apply(t, ledger, contract.ProtocolOpenAIChat, "s1", body, settled(""))
	later := apply(t, ledger, contract.ProtocolOpenAIChat, "s1", body, Plugin{Key: "memory"})
	if string(later.Body) != body {
		t.Fatalf("a later call picked up a forgotten block: %s", later.Body)
	}
}

func TestApplyPutsNothingBackOnAStatefulRequest(t *testing.T) {
	ledger := NewLedger()
	apply(t, ledger, contract.ProtocolOpenAIResponses, "s1", `{"input":"改一下分页"}`, settled("A"))
	// With previous_response_id the upstream has turn 1 and its block; the
	// input holds only what is new.
	stateful := `{"previous_response_id":"resp_1","input":[{"role":"user","content":"改一下分页"},{"role":"user","content":"再加个测试"}]}`
	result := apply(t, ledger, contract.ProtocolOpenAIResponses, "s1", stateful, settled("B"))
	want := `{"previous_response_id":"resp_1","input":[{"role":"user","content":"改一下分页"},{"role":"user","content":"B\n\n再加个测试"}]}`
	if string(result.Body) != want {
		t.Fatalf("body\n got %s\nwant %s", result.Body, want)
	}
	// A later call of the turn carries only tool output.
	continued := `{"previous_response_id":"resp_2","input":[{"type":"function_call_output","call_id":"c1","output":"ok"}]}`
	if result := apply(t, ledger, contract.ProtocolOpenAIResponses, "s1", continued, Plugin{Key: "memory"}); string(result.Body) != continued {
		t.Fatalf("continued body = %s", result.Body)
	}
	if result := apply(t, ledger, contract.ProtocolOpenAIResponses, "s1", continued, settled("C")); !result.Outcomes[0].NoOpening {
		t.Fatalf("a block with no message to carry it = %+v", result.Outcomes[0])
	}
}

func TestApplyWithoutASessionPutsNothingBack(t *testing.T) {
	ledger := NewLedger()
	body := `{"messages":[{"role":"user","content":"改一下分页"}]}`
	apply(t, ledger, contract.ProtocolOpenAIChat, "", body, settled("A"))
	if result := apply(t, ledger, contract.ProtocolOpenAIChat, "", body, Plugin{Key: "memory"}); string(result.Body) != body {
		t.Fatalf("body = %s", result.Body)
	}
}

func TestApplyKeepsPluginsApart(t *testing.T) {
	ledger := NewLedger()
	body := `{"messages":[{"role":"user","content":[{"type":"text","text":"改一下分页"}]}]}`
	result := apply(t, ledger, contract.ProtocolAnthropicMessages, "s1", body,
		Plugin{Key: "alpha", Settled: true, Turn: "A"}, Plugin{Key: "beta", Settled: true, Turn: "B"})
	if got := gjson.GetBytes(result.Body, "messages.0.content.0.text").Str; got != "A\n\nB" {
		t.Fatalf("joined block = %q", got)
	}
	later := apply(t, ledger, contract.ProtocolAnthropicMessages, "s1", body, Plugin{Key: "beta"})
	if got := gjson.GetBytes(later.Body, "messages.0.content.0.text").Str; got != "B" {
		t.Fatalf("only beta's block should come back, got %q", got)
	}
}

func TestApplyResidentBlocks(t *testing.T) {
	ledger := NewLedger()
	chat := apply(t, ledger, contract.ProtocolOpenAIChat, "s1", `{"messages":[{"role":"user","content":"hi"}]}`,
		Plugin{Key: "memory", Resident: "R", Settled: true, Turn: "T"})
	if want := `{"messages":[{"content":"R","role":"system"},{"role":"user","content":"T\n\nhi"}]}`; string(chat.Body) != want {
		t.Fatalf("chat body\n got %s\nwant %s", chat.Body, want)
	}
	gemini := apply(t, ledger, contract.ProtocolGoogleGenerateContent, "s2", `{"cachedContent":"c/1","contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
		Plugin{Key: "memory", Resident: "R"})
	if outcome := gemini.Outcomes[0]; !outcome.NoSystem || outcome.ResidentBytes != 0 {
		t.Fatalf("gemini with cached content: %+v", outcome)
	}
	completion := apply(t, ledger, contract.ProtocolOpenAICompletions, "s3", `{"prompt":"hi"}`,
		Plugin{Key: "memory", Resident: "R", Settled: true, Turn: "T"})
	if outcome := completion.Outcomes[0]; !outcome.NoSystem || !outcome.NoOpening || string(completion.Body) != `{"prompt":"hi"}` {
		t.Fatalf("completion: %+v %s", outcome, completion.Body)
	}
}

func TestBlockRenderFitsItemsInOrder(t *testing.T) {
	block := Block{Open: "<m>", Items: []string{"- one", "", "- two", "- three"}, Close: "</m>"}
	if text, dropped := block.Render(MaxBlockBytes); text != "<m>\n- one\n- two\n- three\n</m>" || dropped != 0 {
		t.Fatalf("Render = %q, %d", text, dropped)
	}
	if text, dropped := block.Render(len("<m>\n- one\n- two\n</m>")); text != "<m>\n- one\n- two\n</m>" || dropped != 1 {
		t.Fatalf("Render = %q, %d", text, dropped)
	}
	// Items are ranked: a short item after one that did not fit is dropped too.
	ranked := Block{Items: []string{"- one", strings.Repeat("x", 100), "- 2"}}
	if text, dropped := ranked.Render(20); text != "- one" || dropped != 2 {
		t.Fatalf("Render = %q, %d", text, dropped)
	}
	if text, dropped := block.Render(len("<m>\n</m>")); text != "" || dropped != 3 {
		t.Fatalf("a block with no room for an item = %q, %d", text, dropped)
	}
	if text, dropped := (Block{Items: []string{strings.Repeat("字", 3000)}}).Render(MaxBlockBytes); text != "" || dropped != 1 {
		t.Fatalf("an oversized text = %d bytes, %d dropped", len(text), dropped)
	}
}
