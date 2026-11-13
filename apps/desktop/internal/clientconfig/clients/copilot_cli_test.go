package clients

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func copilotInstalled(t *testing.T, home agents.Home) {
	t.Helper()
	writeFile(t, filepath.Join(home.Dir(), ".copilot", "config.json"),
		"// This file is managed automatically.\n{\"firstLaunchAt\": \"2026-11-03T00:00:00Z\"}\n")
}

func copilotPath(home agents.Home, file string) string {
	return filepath.Join(home.Dir(), ".copilot", file)
}

func copilotRead(t *testing.T, home agents.Home, file string) any {
	t.Helper()
	text, _ := readFile(t, copilotPath(home, file))
	return jsonc(t, text)
}

func TestCopilotWritesAProviderPerProtocolAndPicksTheDefault(t *testing.T) {
	home := scratchHome(t)
	mkdir(t, home.Dir(), ".copilot")
	if CopilotCLI.Detected(home, "") {
		t.Fatal("detected without config.json")
	}
	copilotInstalled(t, home)
	if !CopilotCLI.Detected(home, "") {
		t.Fatal("not detected")
	}
	chosen := listed(models("model", "gpt-5.5"), "deepseek-v4", "gpt-5.5", "claude-sonnet-5")
	chosen.Protocols = map[string]string{"gpt-5.5": "openai.responses", "claude-sonnet-5": "anthropic.messages"}
	details := cc.Catalog{"claude-sonnet-5": {ContextWindow: cc.Some(uint64(200_000)), MaxTokens: cc.Some(uint64(64_000))}}
	if asked := writePlace(t, home, place(CopilotCLI), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	v1 := origin + "/v1"
	want := map[string]any{
		"providers": []any{
			map[string]any{"name": "local-chat", "type": "openai", "wireApi": "completions", "baseUrl": v1, "apiKey": token},
			map[string]any{"name": "local-responses", "type": "openai", "wireApi": "responses", "baseUrl": v1, "apiKey": token},
			map[string]any{"name": "local-anthropic", "type": "anthropic", "baseUrl": origin, "apiKey": token},
		},
		"models": []any{
			map[string]any{"id": "deepseek-v4", "provider": "local-chat"},
			map[string]any{"id": "gpt-5.5", "provider": "local-responses"},
			map[string]any{"id": "claude-sonnet-5", "provider": "local-anthropic",
				"maxContextWindowTokens": number(200_000), "maxPromptTokens": number(136_000), "maxOutputTokens": number(64_000)},
		},
	}
	if got := copilotRead(t, home, "providers.json"); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	if got := copilotRead(t, home, "settings.json"); !reflect.DeepEqual(got, map[string]any{"model": "local-responses/gpt-5.5"}) {
		t.Fatalf("%v", got)
	}
	status := statusOf(t, home, CopilotCLI, origin)
	if status.State != cc.Configured || *status.Models.Model != "gpt-5.5" || len(status.Models.Models) != 3 {
		t.Fatalf("%+v", status)
	}

	syncAt(t, home, "http://127.0.0.1:9000/")
	providers := at(copilotRead(t, home, "providers.json"), "providers").([]any)
	if at(providers[0], "baseUrl") != "http://127.0.0.1:9000/v1" || at(providers[2], "baseUrl") != "http://127.0.0.1:9000" {
		t.Fatalf("%v", providers)
	}

	removeAt(t, home, CopilotCLI)
	for _, file := range []string{"providers.json", "settings.json"} {
		if _, exists := readFile(t, copilotPath(home, file)); exists {
			t.Fatalf("%s left", file)
		}
	}
}

const copilotUserProviders = `{
  "providers": [
    {
      "name": "ollama",
      "type": "openai",
      "baseUrl": "http://127.0.0.1:11434/v1"
    }
  ],
  "models": [
    {
      "id": "qwen3:8b",
      "provider": "ollama",
      "maxPromptTokens": 6144,
      "maxOutputTokens": 2048
    }
  ]
}
`

const copilotUserSettings = "{\n  // mine\n  \"banner\": \"never\",\n  \"model\": \"ollama/qwen3:8b\",\n}\n"

func TestCopilotKeepsTheUsersProvidersAndPutsTheirModelBack(t *testing.T) {
	home := scratchHome(t)
	copilotInstalled(t, home)
	writeFile(t, copilotPath(home, "providers.json"), copilotUserProviders)
	writeFile(t, copilotPath(home, "settings.json"), copilotUserSettings)
	chosen := connection(models("model", "claude-sonnet-5"))
	chosen.Protocols = map[string]string{"claude-sonnet-5": "anthropic.messages"}
	asked := writeAt(t, home, CopilotCLI, chosen, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "model")
	writeAt(t, home, CopilotCLI, chosen, true)
	written := copilotRead(t, home, "providers.json")
	providers, models := at(written, "providers").([]any), at(written, "models").([]any)
	if at(providers[0], "name") != "local-anthropic" || at(providers[1], "name") != "ollama" || at(models[1], "id") != "qwen3:8b" {
		t.Fatalf("%v", written)
	}
	settings, _ := readFile(t, copilotPath(home, "settings.json"))
	if !strings.Contains(settings, "// mine") || !strings.Contains(settings, `"model": "local-anthropic/claude-sonnet-5"`) {
		t.Fatal(settings)
	}

	removeAt(t, home, CopilotCLI)
	if text, _ := readFile(t, copilotPath(home, "providers.json")); text != copilotUserProviders {
		t.Fatalf("%q", text)
	}
	if text, _ := readFile(t, copilotPath(home, "settings.json")); text != copilotUserSettings {
		t.Fatalf("%q", text)
	}
}
