package ingress

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func midTurnReminder(message string) string {
	return "<system-reminder>\n" + midTurnOpener + message +
		"\n\nThis is how Claude Code surfaces messages the user sends mid-turn — within the running turn, often alongside the next tool result, rather than as a separate conversation turn. Address the message above as you continue this turn.\n</system-reminder>"
}

func claudeBody(t *testing.T, messages ...any) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"model": "claude-fable-5-2", "messages": messages})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestClaudeMidTurnMessagesReadsOnlyTheNewestMessage(t *testing.T) {
	first := midTurnReminder("先别动数据库")
	second := midTurnReminder("顺便把测试也跑了\n第二行")
	body := claudeBody(t,
		map[string]any{"role": "user", "content": "1.A"},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{}}}},
		// Replayed from an earlier call: already recorded there.
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "ok"},
			map[string]any{"type": "text", "text": first},
		}},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "t2", "name": "Bash", "input": map[string]any{}}}},
		map[string]any{"role": "user", "content": []any{
			// Folded into the tool result.
			map[string]any{"type": "tool_result", "tool_use_id": "t2", "content": []any{
				map[string]any{"type": "text", "text": "done"},
				map[string]any{"type": "text", "text": second},
			}},
			map[string]any{"type": "text", "text": "<system-reminder>\nunrelated note\n</system-reminder>"},
		}},
	)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatal(err)
	}
	got := claudeMidTurnMessages(fields)
	if want := []string{"顺便把测试也跑了\n第二行"}; !slices.Equal(got, want) {
		t.Fatalf("messages = %q, want %q", got, want)
	}
}

func TestClaudeMidTurnMessagesIgnoresOtherReminders(t *testing.T) {
	body := claudeBody(t, map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "text", "text": "<system-reminder>\nThe coordinator sent a message while you were working:\nhi\n</system-reminder>"},
		map[string]any{"type": "text", "text": "plain"},
	}})
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatal(err)
	}
	if got := claudeMidTurnMessages(fields); got != nil {
		t.Fatalf("messages = %q", got)
	}
}

func TestCleanUserTextKeepsLinesAndCutsSecrets(t *testing.T) {
	got := cleanUserText("第一行\r\n第二行\x07 sk-abcdefghijklmnop\n", false)
	if got != "第一行\n第二行  …" {
		t.Fatalf("text = %q", got)
	}
	long := cleanUserText(strings.Repeat("字", contract.MaxInputTextRunes+10), false)
	if !strings.HasSuffix(long, "…") || len([]rune(long)) != contract.MaxInputTextRunes {
		t.Fatalf("long text has %d runes", len([]rune(long)))
	}
}

func recordMessages(t *testing.T, body string, filter *privacy.Engine) *writtenRecordStore {
	t.Helper()
	records := &writtenRecordStore{}
	dependencies := Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolAnthropicMessages, false)}, nil
		}),
		RequestRecords: records,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, _ *http.Request, _ transport.Target) error {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, err := writer.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[]}`))
			return err
		}),
	}
	if filter != nil {
		dependencies.PrivacyFilter = filter
	}
	handler := NewWithDependencies(dependencies)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	return records
}

func TestRecordKeepsWholePromptAndMidTurnMessages(t *testing.T) {
	prompt := "修一下\n1. 截断\n2. 中途消息丢了"
	records := recordMessages(t, claudeBody(t, map[string]any{"role": "user", "content": prompt}), nil)
	record := records.records[0]
	if record.InputPreview == nil || *record.InputPreview != "修一下" {
		t.Fatalf("preview = %v", record.InputPreview)
	}
	if record.InputText == nil || *record.InputText != prompt {
		t.Fatalf("input text = %v", record.InputText)
	}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}

	body := claudeBody(t,
		map[string]any{"role": "user", "content": "1.A"},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{}}}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "ok"},
			map[string]any{"type": "text", "text": midTurnReminder("进行到哪了")},
		}},
	)
	record = recordMessages(t, body, nil).records[0]
	if want := []string{"进行到哪了"}; !slices.Equal(record.MidTurnMessages, want) {
		t.Fatalf("mid-turn = %q, want %q", record.MidTurnMessages, want)
	}
	// "1.A" says it all; the record keeps no second copy.
	if record.InputText != nil {
		t.Fatalf("input text repeats the preview: %q", *record.InputText)
	}
}

func TestMidTurnMessagesFollowPrivacyRedaction(t *testing.T) {
	const email = "alice@example.com"
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact,
	}, nil)
	body := claudeBody(t,
		map[string]any{"role": "user", "content": "看看日志\n然后发给 " + email},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{}}}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "ok"},
			map[string]any{"type": "text", "text": midTurnReminder("也抄送 " + email)},
		}},
	)
	records := recordMessages(t, body, filter)
	assertNothingWrittenContains(t, records, email)
	record := records.records[0]
	if len(record.MidTurnMessages) != 1 || !strings.HasPrefix(record.MidTurnMessages[0], "也抄送 ") {
		t.Fatalf("mid-turn = %q", record.MidTurnMessages)
	}
}
