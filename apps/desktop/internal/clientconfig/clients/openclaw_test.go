package clients

import (
	"reflect"
	"strings"
	"testing"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func at(value any, path ...string) any {
	for _, key := range path {
		object, _ := value.(map[string]any)
		value = object[key]
	}
	return value
}

func TestOpenClawJSON5KeepsItsStyleAndTheModelShorthand(t *testing.T) {
	existing := "// OpenClaw\n{\n  agents: { defaults: { model: 'anthropic/claude-opus', workspace: '~/claw' } },\n  models: { providers: { other: { baseUrl: 'http://x', models: [{ id: 'a', name: 'A' }], }, }, },\n}\n"
	fields := models("model", "oc-route")
	details := cc.Catalog{"oc-route": {ContextWindow: cc.Some(uint64(200_000)), MaxTokens: cc.Some(uint64(32_000))}}
	plan, err := cc.PlanWrite(OpenClaw, texts(existing), listed(fields, "oc-route"), details, nil)
	if err != nil {
		t.Fatal(err)
	}
	sameStrings(t, plan.Conflicts, openClawPrimaryKey)
	if plan.Record.Backup[openClawPrimaryKey] != "anthropic/claude-opus" {
		t.Fatal(plan.Record.Backup)
	}
	text := plan.Contents[0]
	if !strings.HasPrefix(text, "// OpenClaw\n{\n  agents: { defaults: { model: ") ||
		!strings.Contains(text, "workspace: '~/claw'") || !strings.Contains(text, "other: { baseUrl: 'http://x'") {
		t.Fatal(text)
	}
	value := jsonc(t, text)
	if at(value, "agents", "defaults", "model") != "astrlink/oc-route" {
		t.Fatal(text)
	}
	want := decode(t, `{"baseUrl": "http://127.0.0.1:18317/v1", "apiKey": "`+token+`", "api": "openai-completions", "models": [{"id": "oc-route", "name": "oc-route", "contextWindow": 200000, "maxTokens": 32000}]}`)
	if got := at(value, "models", "providers", "astrlink"); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	written := texts(text)
	if state := inspect(OpenClaw, written, plan.Record, origin); state != cc.Configured {
		t.Fatal(state)
	}
	if model := cc.CurrentModelsOf(OpenClaw, written).Model; model == nil || *model != "oc-route" {
		t.Fatal(model)
	}
	removals, restored, err := cc.PlanRestore(OpenClaw, written, plan.Record)
	if err != nil || restored != 1 {
		t.Fatal(restored, err)
	}
	left := onlyWrite(t, removals)
	if !reflect.DeepEqual(jsonc(t, left), jsonc(t, existing)) || !strings.HasPrefix(left, "// OpenClaw\n") {
		t.Fatalf("%q", left)
	}

	// An object model keeps its fallbacks.
	object := `{"agents": {"defaults": {"model": {"primary": "a/b", "fallbacks": ["c/d"]}}}}`
	plan = planWrite(t, OpenClaw, texts(object), connection(fields), nil)
	if got := at(jsonc(t, plan.Contents[0]), "agents", "defaults", "model"); !reflect.DeepEqual(got, decode(t, `{"primary": "astrlink/oc-route", "fallbacks": ["c/d"]}`)) {
		t.Fatalf("%v", got)
	}
	for _, broken := range []string{"{models: []}", "{a: 1 b: 2}"} {
		if _, err := cc.PlanWrite(OpenClaw, texts(broken), connection(fields), cc.Catalog{}, nil); err == nil {
			t.Fatal(broken)
		}
	}
}
