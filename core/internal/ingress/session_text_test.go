package ingress

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestInputPreviewIsNewestVisibleUserText(t *testing.T) {
	tests := []struct {
		name     string
		protocol contract.ProtocolID
		body     string
		want     string
	}{
		{name: "responses string input", protocol: contract.ProtocolOpenAIResponses, body: `{"model":"m","input":"创建启动快捷方式"}`, want: "创建启动快捷方式"},
		{
			name:     "responses item list",
			protocol: contract.ProtocolOpenAIResponses,
			body:     `{"input":[{"role":"user","content":[{"type":"input_text","text":"hello world"}]}]}`,
			want:     "hello world",
		},
		{
			name:     "chat messages take the newest user turn",
			protocol: contract.ProtocolOpenAIChat,
			body:     `{"messages":[{"role":"system","content":"sys"},{"role":"user","content":"ask this"},{"role":"assistant","content":"sure"},{"role":"user","content":"then that"}]}`,
			want:     "then that",
		},
		{
			name:     "chat tool results are not user text",
			protocol: contract.ProtocolOpenAIChat,
			body:     `{"messages":[{"role":"user","content":"list files"},{"role":"assistant","tool_calls":[{"id":"call_abcdefgh12345","type":"function","function":{"name":"ls","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_abcdefgh12345","content":"a.go"}]}`,
			want:     "list files",
		},
		{
			name:     "gemini contents",
			protocol: contract.ProtocolGoogleGenerateContent,
			body:     `{"contents":[{"role":"user","parts":[{"text":"画一只猫"}]}]}`,
			want:     "画一只猫",
		},
		{
			name:     "strips url and secret",
			protocol: contract.ProtocolOpenAIResponses,
			body:     `{"input":"see https://example.com/path with sk-abcdefghijklmnopqrstuvwxyz"}`,
			want:     "see … with …",
		},
		{
			name:     "skips anthropic system-reminder inside the newest user turn",
			protocol: contract.ProtocolAnthropicMessages,
			body:     `{"messages":[{"role":"user","content":[{"type":"text","text":"你能做什么"}]},{"role":"assistant","content":"能聊天"},{"role":"user","content":[{"type":"text","text":"<system-reminder>\nToday is 2026-08-16.\n</system-reminder>\n"},{"type":"text","text":"你能搜索吗"}]}]}`,
			want:     "你能搜索吗",
		},
		{
			name:     "skips a newest message that is only a system-reminder",
			protocol: contract.ProtocolOpenAIResponses,
			body:     `{"input":[{"role":"user","content":"你能搜索吗"},{"role":"user","content":"<system-reminder>\nToday\n</system-reminder>"}]}`,
			want:     "你能搜索吗",
		},
		{
			name:     "unwraps a client wrapper around the newest prompt",
			protocol: contract.ProtocolOpenAIChat,
			body:     `{"messages":[{"role":"user","content":"<skill name=\"advisor\">\nSpin up an advisor.\n</skill>"},{"role":"user","content":"<skill name=\"advisor\">\nSpin up an advisor.\n</skill>\n\n审核完先别改，给我结论"}]}`,
			want:     "审核完先别改，给我结论",
		},
		{
			name:     "completions has no history",
			protocol: contract.ProtocolOpenAICompletions,
			body:     `{"model":"m","prompt":"complete me"}`,
			want:     "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			metadata, err := inspectJSONMetadata(request, test.protocol)
			if err != nil {
				t.Fatal(err)
			}
			if metadata.InputPreview != test.want {
				t.Fatalf("preview = %q, want %q", metadata.InputPreview, test.want)
			}
		})
	}
}

func TestConversationCursorFromExplicitCursors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "claude code json user_id",
			body: `{"metadata":{"user_id":"{\"device_id\":\"abc\",\"account_uuid\":\"acc\",\"session_id\":\"94460a08-ccc1-4a26-b7e0-fdeae8c82009\"}"}}`,
			want: "94460a08-ccc1-4a26-b7e0-fdeae8c82009",
		},
		{
			name: "claude code compound user_id",
			body: `{"metadata":{"user_id":"user_ab12_account_9f8e7d6c-1a2b-3c4d-5e6f-708192a3b4c5_session_b4e5aaaa-9a3e-47c4-84ad-3a70f38c676e"}}`,
			want: "b4e5aaaa-9a3e-47c4-84ad-3a70f38c676e",
		},
		{
			name: "explicit metadata session_id",
			body: `{"metadata":{"session_id":"sess_from_proxy"}}`,
			want: "sess_from_proxy",
		},
		{
			name: "bare user hash is not a session",
			body: `{"metadata":{"user_id":"user_only_hash_no_session"}}`,
			want: "",
		},
		{
			name: "cherry body without metadata",
			body: `{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"你能搜索吗"}]}`,
			want: "",
		},
		{
			name: "responses prompt_cache_key",
			body: `{"model":"gpt-5.6-sol","prompt_cache_key":"cherry-agent:cdd2c1c84409d105a4fe9c878d39a23d","input":"你能做什么"}`,
			want: "cherry-agent:cdd2c1c84409d105a4fe9c878d39a23d",
		},
		{
			name: "empty prompt_cache_key is ignored",
			body: `{"prompt_cache_key":"   "}`,
			want: "",
		},
		{
			name: "conversation_id beats prompt_cache_key",
			body: `{"conversation_id":"conv_official","prompt_cache_key":"cherry-agent:other"}`,
			want: "conv_official",
		},
		{
			name: "claude metadata session beats prompt_cache_key",
			body: `{"metadata":{"session_id":"sess_meta"},"prompt_cache_key":"cherry-agent:other"}`,
			want: "sess_meta",
		},
		{
			name: "previous_response_id is not a conversation cursor",
			body: `{"previous_response_id":"resp_prev","input":"下一轮"}`,
			want: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			metadata, err := inspectJSONMetadata(request, contract.ProtocolOpenAIResponses)
			if err != nil {
				t.Fatal(err)
			}
			if metadata.ConversationID != test.want {
				t.Fatalf("cursor = %q, want %q", metadata.ConversationID, test.want)
			}
		})
	}
}

func TestExtractProtocolCursors(t *testing.T) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(`{
		"previous_response_id":"resp_abc",
		"conversation":{"id":"conv_1"}
	}`), &fields); err != nil {
		t.Fatal(err)
	}
	if got := extractProtocolCursor(fields, "previous_response_id"); got != "resp_abc" {
		t.Fatalf("previous = %q", got)
	}
	if got := extractProtocolCursor(fields, "conversation"); got != "conv_1" {
		t.Fatalf("conversation = %q", got)
	}
}

func TestClassifyExtractsSessionFields(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4.1","previous_response_id":"resp_prev","input":"下一轮"}`))
	got, err := classify(request)
	if err != nil {
		t.Fatal(err)
	}
	if got.PreviousResponseID != "resp_prev" || got.InputPreview != "下一轮" {
		t.Fatalf("classified = %#v", got)
	}
	if !got.Conversation.Stateful || got.Conversation.UserTurnCount != 1 {
		t.Fatalf("conversation summary = %#v", got.Conversation)
	}

	messages := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-5","max_tokens":16,"metadata":{"user_id":"user_ab_account_acc_session_94460a08-ccc1-4a26-b7e0-fdeae8c82009"},"messages":[{"role":"user","content":"你能搜索吗"}]}`))
	got, err = classify(messages)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConversationID != "94460a08-ccc1-4a26-b7e0-fdeae8c82009" || got.InputPreview != "你能搜索吗" {
		t.Fatalf("messages classified = %#v", got)
	}

	cherry := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.6-sol","stream":true,"prompt_cache_key":"cherry-agent:cdd2c1c84409d105a4fe9c878d39a23d","input":[{"role":"user","content":"你能做什么"}]}`))
	got, err = classify(cherry)
	if err != nil {
		t.Fatal(err)
	}
	if got.PreviousResponseID != "" || got.ConversationID != "cherry-agent:cdd2c1c84409d105a4fe9c878d39a23d" || got.InputPreview != "你能做什么" {
		t.Fatalf("cherry responses classified = %#v", got)
	}

	official := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4.1","previous_response_id":"resp_prev","prompt_cache_key":"cherry-agent:other","input":"下一轮"}`))
	got, err = classify(official)
	if err != nil {
		t.Fatal(err)
	}
	if got.PreviousResponseID != "resp_prev" {
		t.Fatalf("official previous = %#v", got)
	}
	if got.ConversationID != "cherry-agent:other" {
		t.Fatalf("cache key still extracted as conversation fallback = %#v", got)
	}
	if len(got.Conversation.ExplicitCursors) != 2 || got.Conversation.ExplicitCursors[0] != "resp_prev" {
		t.Fatalf("explicit cursors must list previous_response_id first: %#v", got.Conversation.ExplicitCursors)
	}

	completions := httptest.NewRequest(http.MethodPost, "/v1/completions", strings.NewReader(`{"model":"m","prompt":"hi"}`))
	got, err = classify(completions)
	if err != nil {
		t.Fatal(err)
	}
	if got.InputPreview != "" || got.Conversation.UserTurnCount != 0 {
		t.Fatalf("completions must stay a singleton: %#v", got)
	}
}

// stubSentence opens with a two-rune sentence and then runs on without any
// sentence end or clause break inside the truncation window, so the cut must
// fall at the target, not after "好。".
const stubSentence = "好。那我们先从最容易复现的那个失败测试开始把它的输入输出和期望值全部打印出来再逐行对比一下，然后再决定要不要动手改代码还是先写一个最小复现的用例出来"

// clauseSentence has its only comma inside the last quarter of the budget, so
// the cut backs up to it instead of splitting the clause.
const clauseSentence = "先把这三个失败的测试定位出来不要急着改然后把完整测试套件跑一遍最后给我一份结论说明每个失败的根因和修复方式以及影响范围，再决定要不要合并这个分支"

func TestPreviewShaping(t *testing.T) {
	clauseRunes := []rune(clauseSentence)
	comma := slices.Index(clauseRunes, '，')
	if comma <= previewTargetRunes-previewTargetRunes/4 || comma > previewTargetRunes || len(clauseRunes) <= previewTargetRunes {
		t.Fatalf("fixture drifted: comma at %d of %d runes", comma, len(clauseRunes))
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"short text unchanged", "帮我看看仓库里有哪些文件", "帮我看看仓库里有哪些文件"},
		{"inner whitespace collapsed", "fix   the\tbuild", "fix the build"},
		{"first line wins", "# 标题\n\n第一段正文。\n第二段", "标题"},
		{"blank and markup-only lines skipped", "\n---\n> \n- 请审核这个 PR", "请审核这个 PR"},
		{"bullet stripped, negative number kept", "-1 和 -2 的区别", "-1 和 -2 的区别"},
		{"bracket role label stripped", "[User]: what does this do", "what does this do"},
		{"bare role label stripped", "Human: 解释一下", "解释一下"},
		{"chinese role label stripped", "用户：解释一下", "解释一下"},
		{"url and secret redacted", "see https://example.com/x with sk-abcdefghijklmnopqrstuvwxyz ok", "see … with … ok"},
		{"first sentence kept when long", "请先把这三个失败的测试定位出来，不要急着改。然后把完整测试套件跑一遍，最后给我一份结论，说明每个失败的根因和修复方式，以及有没有影响到别的模块。", "请先把这三个失败的测试定位出来，不要急着改"},
		{"question mark kept", "为什么这个函数在并发下会 panic，而单线程完全正常？我已经加了锁但还是不行，是不是锁的粒度有问题，还是说根本不该在这里加锁，或者换成原子操作呢", "为什么这个函数在并发下会 panic，而单线程完全正常？"},
		{"english period must end a word", "Refactor main.go and stream.go so the retry loop shares one ticker. Then run the suite and report back.", "Refactor main.go and stream.go so the retry loop shares one ticker"},
		{"short first sentence is not a stub", stubSentence, string([]rune(stubSentence)[:previewTargetRunes]) + "…"},
		{"clause boundary truncation", clauseSentence, string(clauseRunes[:comma]) + "…"},
		{"word boundary truncation", strings.Repeat("words ", 20) + "tail", strings.TrimSpace(strings.Repeat("words ", 10)) + "…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizePreview(tc.in)
			if got != tc.want {
				t.Fatalf("sanitizePreview(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
			if n := len([]rune(got)); n > contract.MaxInputPreviewRunes {
				t.Fatalf("preview has %d runes, over the contract bound", n)
			}
		})
	}
}

func TestPreviewClamp(t *testing.T) {
	long := strings.Repeat("字", 120)
	got := sanitizePreview(long)
	if runes := []rune(got); len(runes) != previewTargetRunes+1 || runes[len(runes)-1] != '…' {
		t.Fatalf("unbroken CJK must be cut at the target with an ellipsis, got %d runes: %q", len(runes), got)
	}
}
