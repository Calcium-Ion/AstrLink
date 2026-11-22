package ingress

import (
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

// levelTarget sends level at its budget, the way the fixed mode does.
func levelTarget(level string, budget int) reasoningTarget {
	return reasoningTarget{config: reasoningConfig{mode: contract.ReasoningModeFixed}, level: level, budget: budget}
}

// adaptiveConfig is the adaptive mode with a preset's range.
func adaptiveConfig(preset contract.AdaptivePreset, low, high contract.ReasoningLevel) reasoningConfig {
	settings := contract.DefaultRoutingSettings()
	settings.ReasoningMode, settings.AdaptivePreset, settings.AdaptiveMin, settings.AdaptiveMax = contract.ReasoningModeAdaptive, preset, low, high
	return reasoningConfigFrom(settings)
}

func chatMessages(count int, last string) string {
	items := make([]string, count)
	for index := range items {
		items[index] = `{"role":"user","content":"hi"}`
	}
	items[count-1] = `{"role":"user","content":"` + last + `"}`
	return `"messages":[` + strings.Join(items, ",") + `]`
}

func chatTools(count int) string {
	items := make([]string, count)
	for index := range items {
		items[index] = `{"type":"function"}`
	}
	return `"tools":[` + strings.Join(items, ",") + `]`
}

func TestBudgetLevelsAndAdaptiveRanges(t *testing.T) {
	for budget, want := range map[int]string{
		0: "none", 1: "low", 1024: "low", 1025: "medium", 8192: "medium", 8193: "high",
		32768: "high", 32769: "xhigh", 131071: "xhigh", 131072: "max", 301466: "max",
	} {
		if got := budgetLevel(budget); got != want {
			t.Errorf("budgetLevel(%d) = %q, want %q", budget, got, want)
		}
	}
	if _, ok := reasoningTargetFor(reasoningConfig{mode: contract.ReasoningModePassthrough, level: contract.ReasoningLevelHigh}, contract.ProtocolOpenAIChat, nil); ok {
		t.Fatal("passthrough must leave reasoning to the client")
	}

	long := strings.Repeat("a", 2001)
	for _, test := range []struct {
		name string
		body string
		hits int
	}{
		{"no signal", `{` + chatMessages(10, "hi") + `,` + chatTools(3) + `}`, 0},
		{"a last message of 2000 characters", `{` + chatMessages(1, strings.Repeat("a", 2000)) + `}`, 0},
		{"more than 10 messages", `{` + chatMessages(11, "hi") + `}`, 1},
		{"more than 3 tools", `{` + chatMessages(1, "hi") + `,` + chatTools(4) + `}`, 1},
		{"a last message over 2000 characters", `{` + chatMessages(1, long) + `}`, 1},
		{"messages and a long last message", `{` + chatMessages(11, long) + `}`, 2},
		{"all three", `{` + chatMessages(11, long) + `,` + chatTools(5) + `}`, 3},
	} {
		if got := requestSignals(contract.ProtocolOpenAIChat, []byte(test.body)).hits(); got != test.hits {
			t.Errorf("%s: %d signals, want %d", test.name, got, test.hits)
		}
	}

	// Requests with 0, 1, 2 and 3 signals.
	bodies := [4][]byte{
		[]byte(`{` + chatMessages(1, "fix the login bug") + `}`),
		[]byte(`{` + chatMessages(11, "and the tests") + `}`),
		[]byte(`{` + chatMessages(11, "and the tests") + `,` + chatTools(5) + `}`),
		[]byte(`{` + chatMessages(11, long) + `,` + chatTools(5) + `}`),
	}
	const (
		off    = contract.ReasoningLevelOff
		low    = contract.ReasoningLevelLow
		medium = contract.ReasoningLevelMedium
		high   = contract.ReasoningLevelHigh
		max    = contract.ReasoningLevelMax
	)
	// A stored custom range plays no part in the everyday and research
	// presets.
	for _, test := range []struct {
		name   string
		config reasoningConfig
		want   [4]contract.ReasoningLevel
	}{
		{"everyday", adaptiveConfig(contract.AdaptivePresetEveryday, high, high), [4]contract.ReasoningLevel{low, medium, high, max}},
		{"research", adaptiveConfig(contract.AdaptivePresetResearch, off, low), [4]contract.ReasoningLevel{medium, high, max, max}},
		{"custom low..high", adaptiveConfig(contract.AdaptivePresetCustom, low, high), [4]contract.ReasoningLevel{low, medium, high, high}},
		{"custom off..max", adaptiveConfig(contract.AdaptivePresetCustom, off, max), [4]contract.ReasoningLevel{off, low, medium, max}},
		{"custom high..max", adaptiveConfig(contract.AdaptivePresetCustom, high, max), [4]contract.ReasoningLevel{high, max, max, max}},
		{"custom medium..medium", adaptiveConfig(contract.AdaptivePresetCustom, medium, medium), [4]contract.ReasoningLevel{medium, medium, medium, medium}},
		{"custom off..off", adaptiveConfig(contract.AdaptivePresetCustom, off, off), [4]contract.ReasoningLevel{off, off, off, off}},
	} {
		for hits, body := range bodies {
			target, ok := reasoningTargetFor(test.config, contract.ProtocolOpenAIChat, body)
			want := test.want[hits]
			wantLevel := string(want)
			if want == off {
				wantLevel = "none"
			}
			if !ok || target.hits != hits || target.setting != want || target.level != wantLevel || target.budget != levelBudgets[want] {
				t.Errorf("%s with %d signals = %+v, want %s", test.name, hits, target, want)
			}
		}
	}
	// The fixed mode sends its level whatever the request shows.
	for _, level := range contract.ReasoningLevels {
		target, ok := reasoningTargetFor(reasoningConfig{mode: contract.ReasoningModeFixed, level: level, low: off, high: off}, contract.ProtocolOpenAIChat, bodies[3])
		if !ok || target.setting != level || target.budget != levelBudgets[level] || target.hits != 0 {
			t.Errorf("fixed %s = %+v", level, target)
		}
	}
	// A range whose ends are out of order sends nothing.
	if _, ok := reasoningTargetFor(reasoningConfig{mode: contract.ReasoningModeAdaptive, low: high, high: low}, contract.ProtocolOpenAIChat, bodies[0]); ok {
		t.Fatal("an inverted range must send nothing")
	}
}

func TestRequestSignals(t *testing.T) {
	const image = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	for _, test := range []struct {
		name     string
		protocol contract.ProtocolID
		body     string
		want     adaptiveSignals
	}{
		{"chat string content", contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"system","content":"rules"},{"role":"user","content":"hello"},{"role":"assistant","content":"hi"}],"tools":[{},{}]}`,
			adaptiveSignals{messages: 3, tools: 2, lastUserChars: 5}},
		{"inline images are left out of a string", contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"user","content":"look ` + image + `"}]}`,
			adaptiveSignals{messages: 1, lastUserChars: 5}},
		{"block content counts its JSON without the image", contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"` + image + `"}}]}]}`,
			adaptiveSignals{messages: 1, lastUserChars: len(`[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":""}}]`)}},
		{"JSON length counts escapes and UTF-16 units", contract.ProtocolAnthropicMessages,
			`{"messages":[{"role":"user","content":[{"type":"text","text":"a\"b\n😀中"}]}]}`,
			adaptiveSignals{messages: 1, lastUserChars: len(`[{"type":"text","text":"a\"b\n`) + 3 + len(`"}]`)}},
		{"string text counts UTF-16 units", contract.ProtocolOpenAIChat,
			`{"messages":[{"role":"user","content":"😀中"}]}`,
			adaptiveSignals{messages: 1, lastUserChars: 3}},
		{"anthropic tool result is the last user message", contract.ProtocolAnthropicMessages,
			`{"messages":[{"role":"user","content":"fix"},{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"Read","input":{}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"ok"}]}],"tools":[{},{},{},{}]}`,
			adaptiveSignals{messages: 3, tools: 4, lastUserChars: len(`[{"type":"tool_result","tool_use_id":"t","content":"ok"}]`)}},
		{"responses string input is one message", contract.ProtocolOpenAIResponses,
			`{"input":"hello there","tools":[{"type":"function"}]}`,
			adaptiveSignals{messages: 1, tools: 1, lastUserChars: 11}},
		{"responses items", contract.ProtocolOpenAIResponsesCompact,
			`{"input":[{"role":"user","content":"first"},{"type":"function_call","call_id":"c"},{"type":"function_call_output","call_id":"c","output":"x"}]}`,
			adaptiveSignals{messages: 3, lastUserChars: 5}},
		{"gemini contents and function declarations", contract.ProtocolGoogleGenerateContent,
			`{"contents":[{"role":"user","parts":[{"text":"a"}]},{"role":"model","parts":[{"text":"b"}]},{"parts":[{"text":"hello"}]}],` +
				`"tools":[{"functionDeclarations":[{"name":"a"},{"name":"b"}]},{"googleSearch":{}},{"function_declarations":[{"name":"c"},{"name":"d"},{"name":"e"}]}]}`,
			adaptiveSignals{messages: 3, tools: 5, lastUserChars: len(`[{"text":"hello"}]`)}},
		{"no messages", contract.ProtocolOpenAIChat, `{"model":"x"}`, adaptiveSignals{}},
	} {
		if got := requestSignals(test.protocol, []byte(test.body)); got != test.want {
			t.Errorf("%s: signals = %+v, want %+v", test.name, got, test.want)
		}
	}
}

func TestOverrideReasoningPerModel(t *testing.T) {
	const (
		chat      = contract.ProtocolOpenAIChat
		responses = contract.ProtocolOpenAIResponses
		compact   = contract.ProtocolOpenAIResponsesCompact
		claude    = contract.ProtocolAnthropicMessages
		gemini    = contract.ProtocolGoogleGenerateContent
	)
	var (
		off    = levelTarget("none", 0)
		low    = levelTarget("low", 1024)
		medium = levelTarget("medium", 8192)
		high   = levelTarget("high", 32768)
		max    = levelTarget("max", 131072)
	)
	catalog := codexModel{levels: []string{"low", "medium", "high", "xhigh"}}
	for _, test := range []struct {
		name     string
		protocol contract.ProtocolID
		model    string
		target   reasoningTarget
		codex    codexModel
		body     string
		want     string
		sent     string
	}{
		// Claude models with adaptive thinking take the level as their effort.
		{"Opus 4.7 gets adaptive thinking at medium", claude, "claude-opus-4-7", medium, codexModel{},
			`{"max_tokens":32000,"temperature":0.5,"messages":[{"role":"user","content":"hi"}]}`,
			`{"max_tokens":32000,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"adaptive"},"output_config":{"effort":"medium"}}`, "medium"},
		{"Opus 4.7 moves to high", claude, "claude-opus-4-7", high, codexModel{},
			`{"max_tokens":32000,"thinking":{"type":"adaptive"},"output_config":{"effort":"medium"}}`,
			`{"max_tokens":32000,"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`, "high"},
		{"Opus 4.7 takes max", claude, "claude-opus-4-7", max, codexModel{},
			`{"max_tokens":32000,"thinking":{"type":"adaptive"}}`,
			`{"max_tokens":32000,"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`, "max"},
		{"Opus 4.7 low", claude, "claude-opus-4-7", low, codexModel{},
			`{"max_tokens":32000,"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`,
			`{"max_tokens":32000,"thinking":{"type":"adaptive"},"output_config":{"effort":"low"}}`, "low"},
		{"Claude 4.6 uses adaptive thinking, not its budget", claude, "claude-sonnet-4-6", high, codexModel{},
			`{"max_tokens":32000,"thinking":{"type":"enabled","budget_tokens":4000}}`,
			`{"max_tokens":32000,"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`, "high"},
		{"Claude 4.6 takes max", claude, "claude-opus-4-6", max, codexModel{},
			`{"max_tokens":32000,"thinking":{"type":"adaptive"}}`,
			`{"max_tokens":32000,"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`, "max"},
		{"Claude 4.6 off", claude, "claude-opus-4-6", off, codexModel{},
			`{"max_tokens":32000,"thinking":{"type":"adaptive"}}`, `{"max_tokens":32000,"thinking":{"type":"disabled"}}`, "none"},
		{"Opus 5.5 takes max", claude, "claude-opus-5-5", max, codexModel{},
			`{"max_tokens":32000}`, `{"max_tokens":32000,"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`, "max"},
		{"Sonnet 5.5 takes max", claude, "claude-sonnet-5-5", max, codexModel{},
			`{"max_tokens":32000}`, `{"max_tokens":32000,"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`, "max"},
		{"a model that thinks by default", claude, "claude-opus-5", high, codexModel{},
			`{"max_tokens":32000,"messages":[{"role":"user","content":"hi"}]}`,
			`{"max_tokens":32000,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`, "high"},
		{"Opus 5.5 off is adaptive at low", claude, "claude-opus-5-5", off, codexModel{},
			`{"max_tokens":32000,"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`,
			`{"max_tokens":32000,"thinking":{"type":"adaptive"},"output_config":{"effort":"low"}}`, "low"},
		{"Opus 5.5 off without thinking fields", claude, "claude-opus-5-5", off, codexModel{},
			`{"max_tokens":32000}`, `{"max_tokens":32000,"thinking":{"type":"adaptive"},"output_config":{"effort":"low"}}`, "low"},
		{"Fable off is adaptive at low", claude, "claude-fable-5", off, codexModel{},
			`{"max_tokens":32000}`, `{"max_tokens":32000,"thinking":{"type":"adaptive"},"output_config":{"effort":"low"}}`, "low"},
		{"Sonnet 5.5 off is between_tools", claude, "claude-sonnet-5-5", off, codexModel{},
			`{"max_tokens":32000,"thinking":{"type":"adaptive","display":"summarized"}}`,
			`{"max_tokens":32000,"thinking":{"type":"between_tools"}}`, "none"},
		{"Sonnet 5.5 off without thinking fields", claude, "claude-sonnet-5-5", off, codexModel{},
			`{"max_tokens":32000}`, `{"max_tokens":32000,"thinking":{"type":"between_tools"}}`, "none"},
		{"off keeps an effort the model accepts with thinking off", claude, "claude-opus-4-7", off, codexModel{},
			`{"max_tokens":2000,"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`,
			`{"max_tokens":2000,"thinking":{"type":"disabled"},"output_config":{"effort":"high"}}`, "none"},
		{"off on a model already without thinking", claude, "claude-opus-4-7", off, codexModel{},
			`{"max_tokens":64,"messages":[]}`, `{"max_tokens":64,"messages":[]}`, "none"},

		// Claude models with manual thinking take the budget.
		{"Haiku 4.5 gets the medium budget", claude, "claude-haiku-4-5", medium, codexModel{},
			`{"max_tokens":32000,"messages":[{"role":"user","content":"hi"}]}`,
			`{"max_tokens":32000,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":8192}}`, "medium"},
		{"Haiku 4.5 max stays below max_tokens", claude, "claude-haiku-4-5", max, codexModel{},
			`{"max_tokens":64000,"messages":[{"role":"user","content":"hi"}]}`,
			`{"max_tokens":64000,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":63999}}`, "xhigh"},
		{"a large max_tokens takes the whole max budget", claude, "claude-sonnet-4-5", max, codexModel{},
			`{"max_tokens":200000,"messages":[{"role":"user","content":"hi"}]}`,
			`{"max_tokens":200000,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":131072}}`, "max"},
		{"a budget stays below max_tokens", claude, "claude-sonnet-4-5", max, codexModel{},
			`{"max_tokens":32000,"temperature":0.7,"top_k":5,"top_p":0.97,"messages":[{"role":"user","content":"hi"}]}`,
			`{"max_tokens":32000,"top_p":0.97,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":31999}}`, "high"},
		{"a small max_tokens is raised", claude, "claude-sonnet-4-5", low, codexModel{},
			`{"max_tokens":512,"messages":[{"role":"user","content":"hi"}]}`,
			`{"max_tokens":1280,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":1024}}`, "low"},
		{"a missing max_tokens is filled", claude, "claude-sonnet-4-5", medium, codexModel{},
			`{"messages":[{"role":"user","content":"hi"}]}`,
			`{"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":8191},"max_tokens":8192}`, "medium"},
		{"the display stays", claude, "claude-sonnet-4-5", medium, codexModel{},
			`{"max_tokens":32000,"thinking":{"type":"enabled","budget_tokens":2000,"display":"summarized"}}`,
			`{"max_tokens":32000,"thinking":{"type":"enabled","budget_tokens":8192,"display":"summarized"}}`, "medium"},
		{"manual off", claude, "claude-sonnet-4-5", off, codexModel{},
			`{"max_tokens":32000,"thinking":{"type":"enabled","budget_tokens":4000,"display":"summarized"}}`,
			`{"max_tokens":32000,"thinking":{"type":"disabled"}}`, "none"},
		{"an effort the model takes none of is dropped", claude, "claude-sonnet-4-5", high, codexModel{},
			`{"max_tokens":40000,"output_config":{"format":{"type":"json_schema"},"effort":"high"},"messages":[{"role":"user","content":"hi"}]}`,
			`{"max_tokens":40000,"output_config":{"format":{"type":"json_schema"}},"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":32768}}`, "high"},
		{"Opus 4.5 takes a budget and an effort", claude, "claude-opus-4-5", medium, codexModel{},
			`{"max_tokens":32000,"messages":[{"role":"user","content":"hi"}]}`,
			`{"max_tokens":32000,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":8192},"output_config":{"effort":"medium"}}`, "medium"},

		// Claude models outside the tables keep the shape their client chose.
		{"outside: adaptive gets an effort", claude, "glm-4.6", max, codexModel{},
			`{"thinking":{"type":"adaptive"}}`, `{"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`, "high"},
		{"outside: a higher effort the client sent stays", claude, "glm-4.6", max, codexModel{},
			`{"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`, `{"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`, "max"},
		{"outside: a manual budget is rewritten", claude, "kimi-k2", medium, codexModel{},
			`{"max_tokens":16000,"temperature":0.6,"thinking":{"type":"enabled","budget_tokens":2000}}`,
			`{"max_tokens":16000,"temperature":0.6,"thinking":{"type":"enabled","budget_tokens":8192}}`, "medium"},
		{"outside: nothing is added", claude, "claude-3-5-haiku-20241022", high, codexModel{},
			`{"max_tokens":64,"messages":[]}`, `{"max_tokens":64,"messages":[]}`, ""},
		{"outside: off leaves thinking on", claude, "kimi-k2", off, codexModel{},
			`{"thinking":{"type":"enabled","budget_tokens":2000}}`, `{"thinking":{"type":"enabled","budget_tokens":2000}}`, ""},
		{"outside: an effort alone", claude, "glm-4.6", high, codexModel{},
			`{"output_config":{"effort":"low"}}`, `{"output_config":{"effort":"high"}}`, "high"},

		// OpenAI Chat.
		{"chat adds an effort", chat, "gpt-5.4", high, codexModel{},
			`{"model":"gpt-5.4","temperature":0.3,"top_p":0.9,"logprobs":true,"messages":[]}`,
			`{"model":"gpt-5.4","messages":[],"reasoning_effort":"high"}`, "high"},
		{"chat off on GPT-5.4 is none", chat, "gpt-5.4", off, codexModel{},
			`{"temperature":0.2,"reasoning_effort":"high","messages":[]}`, `{"temperature":0.2,"reasoning_effort":"none","messages":[]}`, "none"},
		{"chat off on the first GPT-5", chat, "gpt-5", off, codexModel{},
			`{"messages":[]}`, `{"messages":[],"reasoning_effort":"minimal"}`, "minimal"},
		{"chat off on o3", chat, "o3", off, codexModel{},
			`{"reasoning_effort":"high"}`, `{"reasoning_effort":"low"}`, "low"},
		{"chat max on GPT-5.4 without a catalog is xhigh", chat, "gpt-5.4", max, codexModel{},
			`{"messages":[]}`, `{"messages":[],"reasoning_effort":"xhigh"}`, "xhigh"},
		{"chat max on GPT-5.1 without a catalog is high", chat, "gpt-5.1-2025-11-13", max, codexModel{},
			`{"reasoning_effort":"low"}`, `{"reasoning_effort":"high"}`, "high"},
		{"responses max on GPT-5.1 Codex without a catalog is high", responses, "gpt-5.1-codex", max, codexModel{},
			`{"reasoning":{"effort":"medium"}}`, `{"reasoning":{"effort":"high"}}`, "high"},
		{"responses max on GPT-5.1 Codex Max without a catalog is xhigh", responses, "gpt-5.1-codex-max", max, codexModel{},
			`{"reasoning":{"effort":"medium"}}`, `{"reasoning":{"effort":"xhigh"}}`, "xhigh"},
		{"responses max on GPT-5.2 Codex without a catalog is xhigh", responses, "gpt-5.2-codex", max, codexModel{},
			`{"reasoning":{"effort":"medium"}}`, `{"reasoning":{"effort":"xhigh"}}`, "xhigh"},
		{"chat max on the first GPT-5 is high", chat, "gpt-5", max, codexModel{},
			`{"reasoning_effort":"low"}`, `{"reasoning_effort":"high"}`, "high"},
		{"chat max on GPT-5 mini is high", chat, "gpt-5-mini", max, codexModel{},
			`{"messages":[]}`, `{"messages":[],"reasoning_effort":"high"}`, "high"},
		{"chat max on o3 is high", chat, "o3", max, codexModel{},
			`{"reasoning_effort":"low"}`, `{"reasoning_effort":"high"}`, "high"},
		{"chat high on GPT-5.4", chat, "gpt-5.4", high, codexModel{},
			`{"reasoning_effort":"xhigh"}`, `{"reasoning_effort":"high"}`, "high"},
		{"chat keeps a higher level the client sent", chat, "gpt-5", max, codexModel{},
			`{"reasoning_effort":"xhigh"}`, `{"reasoning_effort":"xhigh"}`, "xhigh"},
		{"chat nested budget becomes a level", chat, "model-test", medium, codexModel{},
			`{"reasoning":{"max_tokens":2000,"exclude":true}}`, `{"reasoning":{"exclude":true,"effort":"medium"}}`, "medium"},
		{"chat disabled reasoning object on a known model", chat, "gpt-5.4", high, codexModel{},
			`{"reasoning":{"enabled":false}}`, `{"reasoning":{"effort":"high"}}`, "high"},
		{"chat outside: effort rewritten", chat, "model-test", high, codexModel{},
			`{"model":"model-test", "reasoning_effort" : "low","messages":[]}`, `{"model":"model-test", "reasoning_effort" : "high","messages":[]}`, "high"},
		{"chat outside: nothing added", chat, "deepseek-chat", high, codexModel{},
			`{"messages":[]}`, `{"messages":[]}`, ""},
		{"chat outside: off leaves it", chat, "model-test", off, codexModel{},
			`{"reasoning_effort":"high"}`, `{"reasoning_effort":"high"}`, ""},
		{"chat catalog nearest level", chat, "gpt-codex", medium, codexModel{levels: []string{"low", "high"}},
			`{"reasoning_effort":"low"}`, `{"reasoning_effort":"high"}`, "high"},
		{"responses catalog listing low and high at high", responses, "gpt-codex", high, codexModel{levels: []string{"low", "high"}},
			`{"reasoning":{"effort":"low"}}`, `{"reasoning":{"effort":"high"}}`, "high"},
		{"chat catalog max is the largest", chat, "gpt-codex", max, catalog,
			`{"reasoning_effort":"low"}`, `{"reasoning_effort":"xhigh"}`, "xhigh"},
		{"chat catalog off is the smallest", chat, "gpt-codex", off, catalog,
			`{"reasoning_effort":"high"}`, `{"reasoning_effort":"low"}`, "low"},
		{"chat catalog with none", chat, "gpt-codex", off, codexModel{levels: []string{"none", "low", "high"}},
			`{"reasoning_effort":"high"}`, `{"reasoning_effort":"none"}`, "none"},

		// OpenAI Responses.
		{"responses creates the reasoning object", responses, "gpt-5.4", low, codexModel{},
			`{"model":"gpt-5.4","input":"hi"}`, `{"model":"gpt-5.4","input":"hi","reasoning":{"effort":"low"}}`, "low"},
		{"responses keeps the summary", responses, "gpt-5.4", high, codexModel{},
			`{"reasoning":{"effort":"low","summary":"auto"},"input":"hi"}`, `{"reasoning":{"effort":"high","summary":"auto"},"input":"hi"}`, "high"},
		{"responses catalog max is xhigh", responses, "gpt-codex", max, catalog,
			`{"reasoning":{"effort":"medium"}}`, `{"reasoning":{"effort":"xhigh"}}`, "xhigh"},
		{"responses catalog without xhigh", responses, "gpt-codex", max, codexModel{levels: []string{"low", "high"}},
			`{"reasoning":{"effort":"medium"}}`, `{"reasoning":{"effort":"high"}}`, "high"},
		{"responses max on GPT-5.4 without a catalog", responses, "gpt-5.4", max, codexModel{},
			`{"reasoning":{"effort":"medium"}}`, `{"reasoning":{"effort":"xhigh"}}`, "xhigh"},
		{"responses outside: with reasoning", responses, "model-test", low, codexModel{},
			`{"reasoning":{"summary":"auto"}}`, `{"reasoning":{"summary":"auto","effort":"low"}}`, "low"},
		{"responses outside: without reasoning", responses, "model-test", high, codexModel{},
			`{"input":[]}`, `{"input":[]}`, ""},
		{"compact", compact, "gpt-5.4", high, codexModel{},
			`{"model":"gpt-5.4","input":[]}`, `{"model":"gpt-5.4","input":[],"reasoning":{"effort":"high"}}`, "high"},

		// Gemini 2.5 takes the budget, clamped to the model's range.
		{"Gemini 2.5 Flash medium", gemini, "gemini-2.5-flash", medium, codexModel{},
			`{"contents":[]}`, `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingBudget":8192}}}`, "medium"},
		{"Gemini 2.5 Flash high is capped", gemini, "gemini-2.5-flash", high, codexModel{},
			`{"contents":[]}`, `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingBudget":24576}}}`, "high"},
		{"Gemini 2.5 Pro high", gemini, "gemini-2.5-pro", high, codexModel{},
			`{"contents":[]}`, `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingBudget":32768}}}`, "high"},
		{"Gemini 2.5 Flash clamps max", gemini, "gemini-2.5-flash", max, codexModel{},
			`{"generationConfig":{"thinkingConfig":{"thinkingBudget":1024,"includeThoughts":true}}}`,
			`{"generationConfig":{"thinkingConfig":{"thinkingBudget":24576,"includeThoughts":true}}}`, "high"},
		{"Gemini 2.5 Pro clamps max", gemini, "gemini-2.5-pro", max, codexModel{},
			`{"contents":[]}`, `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingBudget":32768}}}`, "high"},
		{"Gemini 2.5 off is zero", gemini, "gemini-2.5-flash", off, codexModel{},
			`{"generationConfig":{"thinkingConfig":{"thinkingBudget":-1}}}`, `{"generationConfig":{"thinkingConfig":{"thinkingBudget":0}}}`, "none"},
		{"Gemini 2.5 Pro off is its minimum", gemini, "gemini-2.5-pro", off, codexModel{},
			`{"contents":[]}`, `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingBudget":128}}}`, "low"},
		{"Gemini 2.5 replaces a level", gemini, "gemini-2.5-flash-lite", low, codexModel{},
			`{"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}`, `{"generationConfig":{"thinkingConfig":{"thinkingBudget":1024}}}`, "low"},
		{"Gemini snake case", gemini, "gemini-2.5-pro", medium, codexModel{},
			`{"generation_config":{"thinking_config":{"thinking_budget":1024}}}`, `{"generation_config":{"thinking_config":{"thinking_budget":8192}}}`, "medium"},

		// Gemini 3 takes a level; max becomes high.
		{"Gemini 3 medium", gemini, "gemini-3-flash-preview", medium, codexModel{},
			`{"contents":[]}`, `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingLevel":"medium"}}}`, "medium"},
		{"Gemini 3 high replaces a budget", gemini, "gemini-3-flash-preview", high, codexModel{},
			`{"generationConfig":{"thinkingConfig":{"thinkingBudget":2048}}}`, `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}`, "high"},
		{"Gemini 3 max is high", gemini, "gemini-3.1-pro-preview", max, codexModel{},
			`{"generationConfig":{"thinkingConfig":{"thinkingLevel":"low"}}}`, `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}`, "high"},
		{"Gemini 3 off is its lowest level", gemini, "gemini-3-flash-preview", off, codexModel{},
			`{"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}`, `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"minimal"}}}`, "minimal"},
		// Gemini 4 takes a level; max is xhigh on Neon and high on Argon.
		{"Gemini 4 Neon gets a level the client did not send", gemini, "gemini-4-neon", high, codexModel{},
			`{"contents":[]}`, `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}`, "high"},
		{"Gemini 4 Neon max is xhigh", gemini, "gemini-4-neon", max, codexModel{},
			`{"contents":[]}`, `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingLevel":"xhigh"}}}`, "xhigh"},
		{"Gemini 4 Argon max is high", gemini, "gemini-4-argon-contributor", max, codexModel{},
			`{"contents":[]}`, `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}`, "high"},
		{"Gemini outside: nothing added", gemini, "gemini-exp-9", high, codexModel{},
			`{"contents":[]}`, `{"contents":[]}`, ""},
		{"Gemini outside: a level is rewritten", gemini, "gemini-exp-9", max, codexModel{},
			`{"generationConfig":{"thinkingConfig":{"thinkingLevel":"low"}}}`, `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}`, "high"},
		{"Gemini outside: a budget stays", gemini, "gemini-exp-9", max, codexModel{},
			`{"generationConfig":{"thinkingConfig":{"thinkingBudget":1024}}}`, `{"generationConfig":{"thinkingConfig":{"thinkingBudget":1024}}}`, ""},

		{"completions", contract.ProtocolOpenAICompletions, "gpt-5.4", high, codexModel{},
			`{"reasoning_effort":"low"}`, `{"reasoning_effort":"low"}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			edit := overrideReasoning(test.protocol, []byte(test.body), test.model, test.target, test.codex)
			if edit.err != nil || string(edit.body) != test.want || edit.sent != test.sent {
				t.Fatalf("got  %s sent=%q err=%v\nwant %s sent=%q", edit.body, edit.sent, edit.err, test.want, test.sent)
			}
			if (len(edit.changed) > 0) != (test.body != test.want) {
				t.Fatalf("changed = %q for body %s", edit.changed, edit.body)
			}
		})
	}
}

func TestClaudeThinkingGuards(t *testing.T) {
	toolLoop := func(assistant string) string {
		return `{"max_tokens":32000,"messages":[{"role":"user","content":"fix it"},` +
			`{"role":"assistant","content":[` + assistant + `]},` +
			`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}]}`
	}
	tool := `{"type":"tool_use","id":"t1","name":"Read","input":{}}`
	for _, test := range []struct {
		name string
		body string
		kept string
	}{
		{"tool call without thinking", toolLoop(`{"type":"text","text":"reading"},` + tool), "tool loop without thinking block"},
		{"tool call after thinking", toolLoop(`{"type":"thinking","thinking":"plan","signature":"s"},` + tool), ""},
		{"tool call after redacted thinking", toolLoop(`{"type":"redacted_thinking","data":"x"},` + tool), ""},
		{"server tool call without thinking", toolLoop(`{"type":"server_tool_use","id":"s1","name":"web_search","input":{}}`), "tool loop without thinking block"},
		{"finished turn without thinking", `{"max_tokens":32000,"messages":[{"role":"user","content":"a"},` +
			`{"role":"assistant","content":[{"type":"text","text":"b"}]},{"role":"user","content":"c"}]}`, ""},
		{"forced tool", `{"max_tokens":32000,"tool_choice":{"type":"any"},"messages":[{"role":"user","content":"a"}]}`, "forced tool choice"},
		{"named tool", `{"max_tokens":32000,"tool_choice":{"type":"tool","name":"Read"},"messages":[{"role":"user","content":"a"}]}`, "forced tool choice"},
		{"automatic tool choice", `{"max_tokens":32000,"tool_choice":{"type":"auto"},"messages":[{"role":"user","content":"a"}]}`, ""},
		{"assistant prefill", `{"max_tokens":32000,"messages":[{"role":"user","content":"a"},{"role":"assistant","content":"{"}]}`, "assistant prefill"},
	} {
		t.Run(test.name, func(t *testing.T) {
			edit := overrideReasoning(contract.ProtocolAnthropicMessages, []byte(test.body), "claude-opus-4-7", levelTarget("high", 32768), codexModel{})
			if edit.err != nil || edit.kept != test.kept {
				t.Fatalf("kept = %q, want %q (err %v)", edit.kept, test.kept, edit.err)
			}
			if unchanged := string(edit.body) == test.body; unchanged != (test.kept != "") || (test.kept != "" && edit.sent != "") {
				t.Fatalf("kept=%q sent=%q body=%s", edit.kept, edit.sent, edit.body)
			}
		})
	}
	// Thinking the client already turned on only changes level.
	body := `{"max_tokens":32000,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"a"},` +
		`{"role":"assistant","content":[` + tool + `]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}]}`
	if edit := overrideReasoning(contract.ProtocolAnthropicMessages, []byte(body), "claude-opus-4-7", levelTarget("max", 131072), codexModel{}); edit.err != nil || edit.sent != "max" || edit.kept != "" {
		t.Fatalf("level change on a thinking request: sent=%q kept=%q err=%v", edit.sent, edit.kept, edit.err)
	}
	// Turning thinking off never trips a guard.
	prefill := `{"max_tokens":32000,"thinking":{"type":"adaptive"},"tool_choice":{"type":"any"},"messages":[{"role":"user","content":"a"},{"role":"assistant","content":"{"}]}`
	if edit := overrideReasoning(contract.ProtocolAnthropicMessages, []byte(prefill), "claude-opus-4-7", levelTarget("none", 0), codexModel{}); edit.sent != "none" || edit.kept != "" {
		t.Fatalf("turning thinking off: sent=%q kept=%q", edit.sent, edit.kept)
	}
}

func TestClaudeCodeHaikuRule(t *testing.T) {
	for _, test := range []struct {
		client contract.ClientType
		model  string
		want   bool
	}{
		{contract.ClientClaudeCode, "claude-haiku-4-5-20251001", true},
		{contract.ClientClaudeCode, "claude-3-5-haiku-20241022", true},
		{contract.ClientClaudeCode, "anthropic/Claude-Haiku-4.5", true},
		{contract.ClientClaudeCode, "claude-sonnet-4-5", false},
		// A Haiku slot the user pointed at another model's name is the main
		// conversation.
		{contract.ClientClaudeCode, "glm-4.5-air", false},
		{contract.ClientClaudeCode, "haiku", false},
		{contract.ClientCodex, "claude-haiku-4-5", false},
		{contract.ClientDroid, "claude-haiku-4-5", false},
		{contract.ClientUnknown, "claude-haiku-4-5", false},
	} {
		if got := claudeCodeHaikuRequest(test.client, test.model); got != test.want {
			t.Errorf("%s %s = %t, want %t", test.client, test.model, got, test.want)
		}
	}
}

func TestReasoningDiagnostics(t *testing.T) {
	signals := adaptiveSignals{messages: 12, tools: 5, lastUserChars: 300}
	everyday := reasoningTarget{
		config:  adaptiveConfig(contract.AdaptivePresetEveryday, contract.ReasoningLevelLow, contract.ReasoningLevelMax),
		setting: contract.ReasoningLevelHigh, level: "high", budget: 32768, signals: signals, hits: 2,
	}
	research := reasoningTarget{
		config:  adaptiveConfig(contract.AdaptivePresetResearch, contract.ReasoningLevelLow, contract.ReasoningLevelMax),
		setting: contract.ReasoningLevelMax, level: "max", budget: 131072,
		signals: adaptiveSignals{messages: 12, tools: 5, lastUserChars: 2300}, hits: 3,
	}
	custom := reasoningTarget{
		config:  adaptiveConfig(contract.AdaptivePresetCustom, contract.ReasoningLevelLow, contract.ReasoningLevelHigh),
		setting: contract.ReasoningLevelHigh, level: "high", budget: 32768,
		signals: adaptiveSignals{messages: 12, tools: 5, lastUserChars: 2300}, hits: 3,
	}
	single := reasoningTarget{
		config:  adaptiveConfig(contract.AdaptivePresetCustom, contract.ReasoningLevelMedium, contract.ReasoningLevelMedium),
		setting: contract.ReasoningLevelMedium, level: "medium", budget: 8192, signals: adaptiveSignals{messages: 11}, hits: 1,
	}
	off := reasoningTarget{
		config:  adaptiveConfig(contract.AdaptivePresetCustom, contract.ReasoningLevelOff, contract.ReasoningLevelMax),
		setting: contract.ReasoningLevelOff, level: "none", signals: adaptiveSignals{messages: 1, lastUserChars: 5},
	}
	fixed := reasoningTarget{config: reasoningConfig{mode: contract.ReasoningModeFixed, level: contract.ReasoningLevelHigh},
		setting: contract.ReasoningLevelHigh, level: "high", budget: 32768}
	const (
		chat   = contract.ProtocolOpenAIChat
		claude = contract.ProtocolAnthropicMessages
		cache  = "; on the turn the level changes, Claude caches the whole conversation again"
	)
	for _, test := range []struct {
		target   reasoningTarget
		edit     reasoningEdit
		protocol contract.ProtocolID
		want     string
	}{
		{everyday, reasoningEdit{sent: "high", changed: []string{"reasoning_effort"}}, chat,
			"adaptive everyday low..max: messages 12, tools 5, last 300 chars, 2 signals, sent high"},
		{everyday, reasoningEdit{sent: "high", changed: []string{"output_config.effort"}}, claude,
			"adaptive everyday low..max: messages 12, tools 5, last 300 chars, 2 signals, sent high" + cache},
		{research, reasoningEdit{sent: "xhigh", changed: []string{"reasoning.effort"}}, contract.ProtocolOpenAIResponses,
			"adaptive research medium..max: messages 12, tools 5, last 2300 chars, 3 signals, sent xhigh (model top)"},
		{research, reasoningEdit{sent: "max", changed: []string{"output_config.effort"}}, claude,
			"adaptive research medium..max: messages 12, tools 5, last 2300 chars, 3 signals, sent max (upper bound)" + cache},
		{custom, reasoningEdit{sent: "high", changed: []string{"reasoning_effort"}}, chat,
			"adaptive custom low..high: messages 12, tools 5, last 2300 chars, 3 signals, sent high (upper bound)"},
		// One level never changes, so Claude keeps its cache.
		{single, reasoningEdit{sent: "medium", changed: []string{"output_config.effort"}}, claude,
			"adaptive custom medium..medium: messages 11, tools 0, last 0 chars, 1 signal, sent medium (upper bound)"},
		{off, reasoningEdit{sent: "low", changed: []string{"output_config.effort"}}, claude,
			"adaptive custom off..max: messages 1, tools 0, last 5 chars, 0 signals, sent low (model lowest)" + cache},
		{everyday, reasoningEdit{kept: "tool loop without thinking block"}, claude,
			"adaptive everyday low..max: messages 12, tools 5, last 300 chars, 2 signals, wanted high, kept client thinking: tool loop without thinking block"},
		{fixed, reasoningEdit{sent: "high", asked: "low", changed: []string{"reasoning_effort"}}, chat, "fixed: sent high, client asked low"},
		{fixed, reasoningEdit{sent: "high"}, claude, "fixed: sent high, client asked nothing"},
		{fixed, reasoningEdit{kept: "tool loop without thinking block"}, claude,
			"fixed: wanted high, kept client thinking: tool loop without thinking block, client asked nothing"},
		{fixed, reasoningEdit{sent: "high", asked: "low", outside: true, changed: []string{"output_config.effort"}}, claude,
			"fixed: sent high, client asked low; model not in capability table: changed output_config.effort only"},
		{fixed, reasoningEdit{outside: true}, chat, "fixed: nothing sent, client asked nothing; model not in capability table: no field changed"},
	} {
		if got := test.target.describe(test.edit, test.protocol); got != test.want {
			t.Errorf("describe = %q\nwant       %q", got, test.want)
		}
	}
}

func TestCodexCatalogModels(t *testing.T) {
	var cache codexCatalogModels
	cache.remember([]discoveryEntry{
		{id: "gpt-codex-a", codexCatalog: []byte(`{"slug":"gpt-codex-a","default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"low"},{"effort":"high"},{"effort":"xhigh"}]}`)},
		{id: "plain"},
		{id: "gpt-codex-b", codexCatalog: []byte(`{"slug":"gpt-codex-b","supported_reasoning_levels":[]}`)},
	})
	if got := cache.lookup("gpt-codex-a"); strings.Join(got.levels, ",") != "low,high,xhigh" {
		t.Fatalf("catalog model = %+v", got)
	}
	if len(cache.lookup("plain").levels) != 0 || len(cache.lookup("gpt-codex-b").levels) != 0 {
		t.Fatal("models without catalog levels must not be clamped")
	}
	cache.remember([]discoveryEntry{{id: "gpt-codex-a", codexCatalog: []byte(`{"supported_reasoning_levels":[]}`)}})
	if len(cache.lookup("gpt-codex-a").levels) != 0 {
		t.Fatal("a catalog without levels must clear the model's old levels")
	}
}
