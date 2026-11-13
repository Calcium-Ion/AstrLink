package clients

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func kiloRead(t *testing.T, path string) any {
	t.Helper()
	text, _ := readFile(t, path)
	value, err := cc.ReadJSON(KiloCode.Files()[0], text)
	if err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return value
}

func TestKiloWritesOneProviderWithEachModelsSDK(t *testing.T) {
	home := scratchHome(t)
	dir := mkdir(t, home.Dir(), ".config", "kilo")
	chosen := listed(models("model", "gpt-5.5"), "claude-sonnet-5", "deepseek-v4", "gpt-5.5")
	chosen.Protocols = map[string]string{"gpt-5.5": "openai.responses", "claude-sonnet-5": "anthropic.messages"}
	details := cc.Catalog{"gpt-5.5": {
		Reasoning: []string{"low", "medium", "high"}, HasReasoning: true,
		ContextWindow: cc.Some(uint64(400_000)), MaxTokens: cc.Some(uint64(128_000)),
	}}
	if asked := writePlace(t, home, place(KiloCode), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	want := map[string]any{
		"$schema": "https://app.kilo.ai/config.json",
		"model":   "local-gateway/gpt-5.5",
		"provider": map[string]any{"local-gateway": map[string]any{
			"npm":     "@ai-sdk/openai-compatible",
			"name":    "AstrLink",
			"options": map[string]any{"baseURL": origin + "/v1", "apiKey": token},
			"models": map[string]any{
				"claude-sonnet-5": map[string]any{"name": "claude-sonnet-5", "provider": map[string]any{"npm": "@ai-sdk/anthropic"}},
				"deepseek-v4":     map[string]any{"name": "deepseek-v4"},
				"gpt-5.5": map[string]any{"name": "gpt-5.5", "provider": map[string]any{"npm": "@ai-sdk/openai"}, "reasoning": true,
					"limit": map[string]any{"context": number(400_000), "output": number(128_000)}},
			},
		}},
	}
	if got := kiloRead(t, filepath.Join(dir, "kilo.jsonc")); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	status := statusOf(t, home, KiloCode, origin)
	if status.State != cc.Configured || *status.Models.Model != "gpt-5.5" || len(status.Notes) != 0 {
		t.Fatalf("%+v", status)
	}

	writeFile(t, filepath.Join(dir, "opencode.json"), `{"model": "kilo/kilo-auto/free"}`)
	sameStrings(t, statusOf(t, home, KiloCode, origin).Notes, "overridden")
	if err := os.Remove(filepath.Join(dir, "opencode.json")); err != nil {
		t.Fatal(err)
	}

	syncAt(t, home, "http://127.0.0.1:9000/")
	if got := at(kiloRead(t, filepath.Join(dir, "kilo.jsonc")), "provider", "local-gateway", "options", "baseURL"); got != "http://127.0.0.1:9000/v1" {
		t.Fatal(got)
	}

	removeAt(t, home, KiloCode)
	if _, exists := readFile(t, filepath.Join(dir, "kilo.jsonc")); exists {
		t.Fatal("kilo.jsonc left")
	}
}

const kiloUser = "{\n  \"$schema\": \"https://app.kilo.ai/config.json\",\n  // My Kilo settings\n  \"model\": \"openrouter/qwen3-coder\",\n  \"permission\": {\n    \"bash\": \"allow\",\n  },\n  \"provider\": {\n    \"openrouter\": {\n      \"options\": { \"apiKey\": \"sk-or-mine\" },\n    },\n  },\n}\n"

func TestKiloWritesTheFileKiloWritesAndPutsTheModelBack(t *testing.T) {
	home := scratchHome(t)
	dir := mkdir(t, home.Dir(), ".config", "kilo")
	writeFile(t, filepath.Join(dir, "opencode.json"), kiloUser)
	chosen := connection(models("model", "deepseek-v4"))
	asked := writeAt(t, home, KiloCode, chosen, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "model")
	writeAt(t, home, KiloCode, chosen, true)
	if _, exists := readFile(t, filepath.Join(dir, "kilo.jsonc")); exists {
		t.Fatal("wrote kilo.jsonc")
	}
	written := kiloRead(t, filepath.Join(dir, "opencode.json"))
	if at(written, "model") != "local-gateway/deepseek-v4" || at(written, "provider", "openrouter", "options", "apiKey") != "sk-or-mine" {
		t.Fatalf("%v", written)
	}

	removeAt(t, home, KiloCode)
	restored, _ := readFile(t, filepath.Join(dir, "opencode.json"))
	user, _ := cc.ReadJSON(KiloCode.Files()[0], kiloUser)
	if !strings.Contains(restored, "// My Kilo settings") || !reflect.DeepEqual(kiloRead(t, filepath.Join(dir, "opencode.json")), user) {
		t.Fatal(restored)
	}
}
