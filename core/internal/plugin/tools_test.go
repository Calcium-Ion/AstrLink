package plugin

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func toolsMeta() Meta {
	meta := validMeta()
	meta.Capabilities = append(meta.Capabilities, CapabilityTools)
	meta.Tools = []Tool{
		{
			Name:        "search",
			Description: Localized{"zh-CN": "按一句话检索本项目的记忆。", "en": "Search this project's memory."},
			Params: []ToolParam{
				{Name: "query", Type: ToolParamString, Required: true, MaxRunes: 20, Example: Localized{"": "要查的内容"}},
				{Name: "limit", Type: ToolParamNumber, Min: number(1), Max: number(10)},
			},
		},
		{Name: "projects", Description: Localized{"": "List the projects."}, Unbound: true},
	}
	return meta
}

func TestToolsValidate(t *testing.T) {
	if err := validateMeta(toolsMeta(), "powercontext"); err != nil {
		t.Fatalf("validateMeta: %v", err)
	}
	// A report turned on and off by a switch of the plugin's.
	reporting := toolsMeta()
	reporting.Settings = []Setting{{Key: "report", Label: "上报", Type: SettingSwitch, Default: json.RawMessage(`true`)}}
	reporting.Tools = append(reporting.Tools, Tool{
		Name: "note", Description: Localized{"": "Note what is worth keeping."}, Writes: true, Setting: "report",
		Params: []ToolParam{{Name: "text", Type: ToolParamString, Required: true, MaxRunes: 1000, Example: Localized{"": "what to note"}}},
	})
	if err := validateMeta(reporting, "powercontext"); err != nil {
		t.Fatalf("validateMeta with a report: %v", err)
	}
	for _, test := range []struct {
		name   string
		change func(*Meta)
		want   string
	}{
		{"without the capability", func(meta *Meta) { meta.Capabilities = meta.Capabilities[:len(meta.Capabilities)-1] }, "needs the model.tools capability"},
		{"bad name", func(meta *Meta) { meta.Tools[0].Name = "Search" }, "name"},
		{"name twice", func(meta *Meta) { meta.Tools[1].Name = "search" }, "declared twice"},
		{"no description", func(meta *Meta) { meta.Tools[0].Description = nil }, "description"},
		{"unknown language", func(meta *Meta) { meta.Tools[0].Description = Localized{"fr": "Chercher"} }, "description"},
		{"long description", func(meta *Meta) { meta.Tools[1].Description = Localized{"": strings.Repeat("长", 301)} }, "description"},
		{"required without example", func(meta *Meta) { meta.Tools[0].Params[0].Example = nil }, "needs an example"},
		{"unknown type", func(meta *Meta) { meta.Tools[0].Params[1].Type = "boolean" }, "string or number"},
		{"min on a string", func(meta *Meta) { meta.Tools[0].Params[0].Min = number(1) }, "for numbers"},
		{"length on a number", func(meta *Meta) { meta.Tools[0].Params[1].MaxRunes = 3 }, "for strings"},
		{"min above max", func(meta *Meta) { meta.Tools[0].Params[1].Min = number(20) }, "min is above max"},
		{"param twice", func(meta *Meta) { meta.Tools[0].Params[1].Name = "query" }, "declared twice"},
		{"lookup without tools", func(meta *Meta) {
			meta.Tools = nil
			meta.Settings = []Setting{{Key: HostSettingLookup}}
		}, "$lookup needs a query"},
		{"a report before the session has a value", func(meta *Meta) { meta.Tools[1].Writes = true }, "cannot be unbound"},
		{"a switch not declared", func(meta *Meta) { meta.Tools[0].Setting = "report" }, `setting "report" must name a switch`},
		{"a setting that is no switch", func(meta *Meta) {
			meta.Settings = []Setting{{Key: "report", Label: "上报", Type: SettingText}}
			meta.Tools[0].Setting = "report"
		}, "must name a switch"},
		{"a host setting", func(meta *Meta) {
			meta.Settings = []Setting{{Key: HostSettingTrusted}}
			meta.Tools[0].Setting = HostSettingTrusted
		}, "must name a switch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := toolsMeta()
			test.change(&meta)
			err := validateMeta(meta, "powercontext")
			var loadErr *LoadError
			if !errors.As(err, &loadErr) || !strings.Contains(loadErr.Message, test.want) {
				t.Fatalf("validateMeta = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLocalizedTextsFallBackToEnglishThenThePlainOne(t *testing.T) {
	var text Localized
	if err := json.Unmarshal([]byte(`{"zh-CN":"检索","en":"Search"}`), &text); err != nil {
		t.Fatal(err)
	}
	if text.In("zh-CN") != "检索" || text.In("ja") != "Search" {
		t.Fatalf("In = %q, %q", text.In("zh-CN"), text.In("ja"))
	}
	if err := json.Unmarshal([]byte(`"Search"`), &text); err != nil || text.In("ja") != "Search" {
		t.Fatalf("a plain text = %v, %v", text, err)
	}
	if err := json.Unmarshal([]byte(`{"ja":"検索"}`), &text); err != nil || text.In("en") != "検索" {
		t.Fatalf("the only language = %q", text.In("en"))
	}
	if err := json.Unmarshal([]byte(`3`), &text); err == nil {
		t.Fatal("a number was taken as a text")
	}
}

func TestCheckInputReadsWhatTheToolDeclares(t *testing.T) {
	tool := toolsMeta().Tools[0]
	input, err := tool.CheckInput(json.RawMessage(`{"query":"  分页游标 ","limit":3}`))
	if err != nil || input["query"] != "分页游标" || input["limit"] != float64(3) {
		t.Fatalf("CheckInput = %v, %v", input, err)
	}
	// Parameters of a GET arrive as text.
	if input, err := tool.CheckInput(json.RawMessage(`{"query":"x","limit":"2"}`)); err != nil || input["limit"] != float64(2) {
		t.Fatalf("a number as text = %v, %v", input, err)
	}
	for _, raw := range []string{
		`{}`, `{"query":""}`, `{"query":"` + strings.Repeat("长", 21) + `"}`, `{"query":"x","limit":0}`,
		`{"query":"x","limit":"many"}`, `{"query":"x","page":1}`, `{"query":["x"]}`, `[]`, `"x"`, `{"query":"x","limit":"NaN"}`,
	} {
		if input, err := tool.CheckInput(json.RawMessage(raw)); err == nil {
			t.Fatalf("CheckInput(%s) = %v, want a refusal", raw, input)
		}
	}
	if input, err := toolsMeta().Tools[1].CheckInput(nil); err != nil || len(input) != 0 {
		t.Fatalf("no input = %v, %v", input, err)
	}
}
