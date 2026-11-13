package clients

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func TestOpenCodeGetsAProviderWithEveryListedModel(t *testing.T) {
	fields := models("model", "oc-route")
	details := cc.Catalog{"oc-route": {ContextWindow: cc.Some(uint64(400_000)), MaxTokens: cc.Some(uint64(64_000))}}
	plan, err := cc.PlanWrite(OpenCode, texts(missing), listed(fields, "oc-route", "oc-mini"), details, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
		"$schema": "https://opencode.ai/config.json",
		"model": "astrlink/oc-route",
		"provider": {"astrlink": {
			"npm": "@ai-sdk/openai-compatible",
			"name": "AstrLink",
			"options": {"baseURL": "http://127.0.0.1:18317/v1", "apiKey": "` + token + `"},
			"models": {
				"oc-route": {"name": "oc-route", "limit": {"context": 400000, "output": 64000}},
				"oc-mini": {"name": "oc-mini"}
			}
		}}
	}`
	if got := jsonc(t, plan.Contents[0]); !reflect.DeepEqual(got, decode(t, want)) {
		t.Fatalf("%s", plan.Contents[0])
	}
	if _, ok := plan.Record.Keys["$schema"]; ok {
		t.Fatal("recorded the schema")
	}
	written := texts(plan.Contents[0])
	current := cc.CurrentModelsOf(OpenCode, written)
	if !reflect.DeepEqual(current, cc.CurrentModels{Model: some("oc-route"), Models: []string{"oc-route", "oc-mini"}}) {
		t.Fatalf("%+v", current)
	}
	// The created file holds nothing else and goes.
	sameRemovals(t, planRemove(t, OpenCode, written, plan.Record), deleted)

	existing := "{\n  // mine\n  \"model\": \"anthropic/claude-sonnet\",\n  \"provider\": {\"other\": {\"options\": {\"apiKey\": \"sk-other\"}}},\n}\n"
	plan = planWrite(t, OpenCode, texts(existing), connection(fields), nil)
	sameStrings(t, plan.Conflicts, "model")
	if strings.Contains(plan.Contents[0], "$schema") {
		t.Fatal(plan.Contents[0])
	}
	left := onlyWrite(t, planRemove(t, OpenCode, texts(plan.Contents[0]), plan.Record))
	if !strings.Contains(left, "// mine") || !reflect.DeepEqual(jsonc(t, left), jsonc(t, existing)) {
		t.Fatalf("%q", left)
	}
}

func TestOpenCodeWritesTheFileItReadsFirstAndKeepsToIt(t *testing.T) {
	home := scratchHome(t)
	dir := mkdir(t, home.Dir(), ".config", "opencode")
	if got := openCodeFile(home, nil); got != filepath.Join(dir, "opencode.jsonc") {
		t.Fatal(got)
	}
	writeFile(t, filepath.Join(dir, "config.json"), "{}")
	if got := openCodeFile(home, nil); got != filepath.Join(dir, "config.json") {
		t.Fatal(got)
	}
	writeFile(t, filepath.Join(dir, "opencode.json"), "{}")
	if got := openCodeFile(home, nil); got != filepath.Join(dir, "opencode.json") {
		t.Fatal(got)
	}
	writeAt(t, home, OpenCode, connection(codexModels()), false)
	if text, _ := readFile(t, filepath.Join(dir, "opencode.json")); !strings.Contains(text, token) {
		t.Fatal(text)
	}
	// A file made later does not move AstrLink's config.
	writeFile(t, filepath.Join(dir, "opencode.jsonc"), "{}")
	status := statusOf(t, home, OpenCode, "http://127.0.0.1:18317/")
	if !reflect.DeepEqual(status.Paths, []string{filepath.Join(dir, "opencode.json")}) || status.State != cc.Configured {
		t.Fatalf("%+v", status)
	}
	removeAt(t, home, OpenCode)
	if text, _ := readFile(t, filepath.Join(dir, "opencode.json")); !reflect.DeepEqual(jsonc(t, text), map[string]any{}) {
		t.Fatal(text)
	}
}
