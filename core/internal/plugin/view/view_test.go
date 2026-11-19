package view

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func encode(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestParseKeepsTheDesignViews(t *testing.T) {
	raw := `[
	  {"type":"kv","items":[{"label":"项目","value":"astrlink（会话标记）"},{"label":"Scope","value":"scp_7f3a…c21","mono":true}]},
	  {"type":"table","columns":["家族","请求","返回","保留","耗时","说明"],
	   "rows":[["atomic-memory",4,2,2,"380 ms","无分数"],["topic-memory",3,3,1,"402 ms","低于阈值 0.5，滤掉 2 条"]]},
	  {"type":"badges","items":[{"text":"PowerContext 1.3.0","tone":"ok"},{"text":"明文 HTTP","tone":"warn"}]},
	  {"type":"text","text":"第一行\n第二行"}
	]`
	view, truncated := Parse(json.RawMessage(raw))
	if truncated || len(view) != 4 {
		t.Fatalf("Parse = %d blocks, truncated %v", len(view), truncated)
	}
	want := `[{"type":"kv","items":[{"label":"项目","value":"astrlink（会话标记）"},{"label":"Scope","value":"scp_7f3a…c21","mono":true}]},` +
		`{"type":"table","columns":["家族","请求","返回","保留","耗时","说明"],"rows":[["atomic-memory",4,2,2,"380 ms","无分数"],["topic-memory",3,3,1,"402 ms","低于阈值 0.5，滤掉 2 条"]]},` +
		`{"type":"badges","items":[{"text":"PowerContext 1.3.0","tone":"ok"},{"text":"明文 HTTP","tone":"warn"}]},` +
		`{"type":"text","text":"第一行\n第二行"}]`
	if got := encode(t, view); got != want {
		t.Fatalf("Parse =\n%s\nwant\n%s", got, want)
	}
}

func TestParseCleansAndCuts(t *testing.T) {
	rows := make([][]any, 250)
	for index := range rows {
		rows[index] = []any{fmt.Sprint(index), index, "extra"}
	}
	raw, _ := json.Marshal([]any{
		map[string]any{"type": "kv", "items": []any{
			map[string]any{"label": "名\x1b[31m称‮", "value": 42},
			map[string]any{"label": "", "value": "no label"},
			map[string]any{"label": "object", "value": map[string]any{"nested": true}},
		}},
		map[string]any{"type": "table", "columns": []any{"a", "b"}, "rows": rows},
		map[string]any{"type": "html", "html": "<script>alert(1)</script>"},
		map[string]any{"type": "text", "text": strings.Repeat("长", 1500)},
		map[string]any{"type": "badges", "items": []any{map[string]any{"text": "ok", "tone": "purple"}}},
		"not a block",
	})
	view, truncated := Parse(raw)
	if !truncated {
		t.Fatal("Parse did not mark the cut")
	}
	if len(view) != 4 {
		t.Fatalf("Parse kept %d blocks: %s", len(view), encode(t, view))
	}
	if kv := view[0]; len(kv.Items) != 2 || kv.Items[0].Label != "名[31m称" || kv.Items[0].Value != "42" || kv.Items[1].Value != "" {
		t.Fatalf("kv = %s", encode(t, kv))
	}
	if table := view[1]; len(table.Rows) != contract.MaxPluginTableRows || len(table.Rows[0]) != 2 || *table.Rows[199][1].Number != 199 {
		t.Fatalf("table has %d rows", len(table.Rows))
	}
	if text := view[2].Text; len([]rune(text)) != contract.MaxPluginViewTextRunes {
		t.Fatalf("text has %d runes", len([]rune(text)))
	}
	if badge := view[3].Items[0]; badge.Text != "ok" || badge.Tone != "" {
		t.Fatalf("badge = %+v", badge)
	}
	if err := contract.ValidatePluginView(view); err != nil {
		t.Fatalf("the parsed view does not validate: %v", err)
	}
}

func TestParseFitsTheSizeLimit(t *testing.T) {
	var blocks []any
	for index := range 30 {
		blocks = append(blocks, map[string]any{"type": "text", "text": fmt.Sprintf("%02d %s", index, strings.Repeat("x", 1000))})
	}
	raw, _ := json.Marshal(blocks)
	view, truncated := Parse(raw)
	encoded, _ := json.Marshal(view)
	if !truncated || len(encoded) > contract.MaxPluginViewBytes || len(view) == 0 || !strings.HasPrefix(view[0].Text, "00 ") {
		t.Fatalf("Parse kept %d blocks, %d bytes, truncated %v", len(view), len(encoded), truncated)
	}
}

func TestParseOfWhatIsNotAView(t *testing.T) {
	for _, test := range []struct {
		raw       string
		truncated bool
	}{
		{"", false},
		{"null", false},
		{`{"type":"text","text":"not in an array"}`, true},
		{`"text"`, true},
		{`[1, 2`, true},
	} {
		view, truncated := Parse(json.RawMessage(test.raw))
		if view != nil || truncated != test.truncated {
			t.Fatalf("Parse(%q) = %v, %v", test.raw, view, truncated)
		}
	}
}

func TestParseRecord(t *testing.T) {
	record, ok := ParseRecord(json.RawMessage(`{"title":"记忆","status":"ok","summary":"召回 3/9","view":[{"type":"kv","items":[{"label":"项目","value":"astrlink"}]}],"extra":1}`))
	if !ok || record.Title != "记忆" || record.Status != contract.PluginRecordOK || record.Summary != "召回 3/9" || len(record.View) != 1 || record.Truncated {
		t.Fatalf("ParseRecord = %+v, %v", record, ok)
	}
	record, ok = ParseRecord(json.RawMessage(`{"title":"` + strings.Repeat("题", 40) + `","status":"skipped","summary":"` + strings.Repeat("s", 300) + `"}`))
	if !ok || len([]rune(record.Title)) != contract.MaxPluginRecordTitleRunes || len(record.Summary) != contract.MaxPluginRecordSummaryRunes || !record.Truncated {
		t.Fatalf("ParseRecord of long texts = %+v, %v", record, ok)
	}
	for _, raw := range []string{`{"status":"ok"}`, `{"title":"记忆","status":"done"}`, `[]`, `null`, `{"title":"\u001b","status":"ok"}`} {
		if record, ok := ParseRecord(json.RawMessage(raw)); ok {
			t.Fatalf("ParseRecord(%s) = %+v, want it dropped", raw, record)
		}
	}
}

func TestParseRecordKeepsValidActions(t *testing.T) {
	record, ok := ParseRecord(json.RawMessage(`{"title":"记忆装载","status":"ok","summary":"","actions":[
		{"id":"refresh","label":"刷新记忆","confirm":"会让这个会话的缓存重写一次。"},
		{"id":"refresh","label":"又一个"},
		{"id":"Bad Id","label":"坏的"},
		{"id":"pause","label":""},
		{"id":"open","label":"打开\u001b"},
		{"id":"more","label":"更多"},
		{"id":"most","label":"最多"}]}`))
	if !ok || !record.Truncated || len(record.Actions) != 3 {
		t.Fatalf("ParseRecord = %+v, %v", record, ok)
	}
	if first := record.Actions[0]; first.ID != "refresh" || first.Label != "刷新记忆" || first.Confirm != "会让这个会话的缓存重写一次。" {
		t.Fatalf("first action = %+v", first)
	}
	if record.Actions[1].ID != "open" || record.Actions[1].Label != "打开" || record.Actions[2].ID != "more" {
		t.Fatalf("actions = %+v", record.Actions)
	}
	if record, ok := ParseRecord(json.RawMessage(`{"title":"记忆","status":"ok","summary":"","actions":"refresh"}`)); !ok || record.Actions != nil || !record.Truncated {
		t.Fatalf("actions that are no list = %+v, %v", record, ok)
	}
}
