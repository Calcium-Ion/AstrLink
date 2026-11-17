package clients

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func qwenSettingsPath(home agents.Home) string {
	return filepath.Join(home.Dir(), ".qwen", "settings.json")
}

func readQwen(t *testing.T, home agents.Home) any {
	t.Helper()
	text, exists := readFile(t, qwenSettingsPath(home))
	if !exists {
		return nil
	}
	return jsonc(t, text)
}

func qwenEntryOf(id, url string) map[string]any {
	return map[string]any{"id": id, "name": id, "description": "AstrLink", "baseUrl": url, "envKey": "ASTRLINK_QWEN_API_KEY"}
}

func TestQwenWritesEachModelIntoItsProtocolsList(t *testing.T) {
	home := scratchHome(t)
	mkdir(t, home.Dir(), ".qwen")
	chosen := listed(models("model", "claude-sonnet-5"), "gpt-5.6-sol", "glm-5", "claude-sonnet-5", "gemini-3.6-pro")
	chosen.Protocols = map[string]string{
		"gpt-5.6-sol":     "openai.responses",
		"claude-sonnet-5": "anthropic.messages",
		"gemini-3.6-pro":  "google.generate_content",
	}
	details := cc.Catalog{"glm-5": {ContextWindow: cc.Some(uint64(200_000)), MaxTokens: cc.Some(uint64(32_000))}}
	if asked := writePlace(t, home, place(QwenCode), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	responses := qwenEntryOf("gpt-5.6-sol", origin+"/v1")
	responses["wireApi"] = "responses"
	chat := qwenEntryOf("glm-5", origin+"/v1")
	chat["generationConfig"] = map[string]any{"contextWindowSize": number(200_000), "samplingParams": map[string]any{"max_tokens": number(32_000)}}
	want := map[string]any{
		"$version": number(4),
		"modelProviders": map[string]any{
			"openai":    []any{responses, chat},
			"anthropic": []any{qwenEntryOf("claude-sonnet-5", origin)},
			"gemini":    []any{qwenEntryOf("gemini-3.6-pro", origin)},
		},
		"env":      map[string]any{"ASTRLINK_QWEN_API_KEY": token},
		"security": map[string]any{"auth": map[string]any{"selectedType": "anthropic"}},
		"model":    map[string]any{"name": "claude-sonnet-5", "baseUrl": origin},
	}
	if got := readQwen(t, home); !reflect.DeepEqual(got, want) {
		text, _ := readFile(t, qwenSettingsPath(home))
		t.Fatalf("%s", text)
	}
	status := statusOf(t, home, QwenCode, origin)
	if status.State != cc.Configured || *status.Models.Model != "claude-sonnet-5" {
		t.Fatalf("%+v", status)
	}
	sameStrings(t, status.Models.Models, "claude-sonnet-5", "gpt-5.6-sol", "glm-5", "gemini-3.6-pro")

	syncAt(t, home, "http://127.0.0.1:9000/")
	synced := readQwen(t, home)
	if at(synced, "modelProviders", "openai").([]any)[0].(map[string]any)["baseUrl"] != "http://127.0.0.1:9000/v1" ||
		at(synced, "modelProviders", "anthropic").([]any)[0].(map[string]any)["baseUrl"] != "http://127.0.0.1:9000" ||
		at(synced, "model", "baseUrl") != "http://127.0.0.1:9000" {
		t.Fatalf("%v", synced)
	}
	if state := statusOf(t, home, QwenCode, "http://127.0.0.1:9000").State; state != cc.Configured {
		t.Fatal(state)
	}

	removeAt(t, home, QwenCode)
	if _, err := os.Stat(qwenSettingsPath(home)); !os.IsNotExist(err) {
		t.Fatal("settings left")
	}
}

const qwenUser = `{
  "$version": 4,
  // my own DashScope setup
  "general": { "vimMode": true },
  "modelProviders": {
    "openai": [
      { "id": "qwen3-coder-plus", "name": "qwen3-coder-plus",
        "baseUrl": "https://dashscope.aliyuncs.com/compatible-mode/v1",
        "description": "Qwen3-Coder via Dashscope", "envKey": "DASHSCOPE_API_KEY" }
    ]
  },
  "env": { "DASHSCOPE_API_KEY": "sk-dash" },
  "security": { "auth": { "selectedType": "qwen-oauth" } },
  "model": { "name": "coder-model" }
}
`

func TestQwenLeadsTheUsersListsAndPutsTheirChoiceBack(t *testing.T) {
	home := scratchHome(t)
	writeFile(t, qwenSettingsPath(home), qwenUser)
	chosen := listed(models("model", "claude-sonnet-5"), "claude-sonnet-5", "glm-5")
	chosen.Protocols = map[string]string{"claude-sonnet-5": "anthropic.messages"}
	asked := writePlace(t, home, place(QwenCode), chosen, cc.Catalog{}, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "security.auth.selectedType", "model.name")
	writePlace(t, home, place(QwenCode), chosen, cc.Catalog{}, true)
	text, _ := readFile(t, qwenSettingsPath(home))
	if !strings.Contains(text, "// my own DashScope setup") {
		t.Fatal(text)
	}
	written := readQwen(t, home)
	openai := at(written, "modelProviders", "openai").([]any)
	if at(openai[0], "id") != "glm-5" || at(openai[1], "id") != "qwen3-coder-plus" ||
		at(written, "env", "DASHSCOPE_API_KEY") != "sk-dash" || at(written, "security", "auth", "selectedType") != "anthropic" ||
		!reflect.DeepEqual(at(written, "model"), map[string]any{"name": "claude-sonnet-5", "baseUrl": origin}) {
		t.Fatalf("%s", text)
	}

	// `/model` picks another of AstrLink's models.
	picked := strings.Replace(text, "\"name\": \"claude-sonnet-5\",\n    \"baseUrl\": \"http://127.0.0.1:18317\"",
		"\"name\": \"glm-5\",\n    \"baseUrl\": \"http://127.0.0.1:18317/v1\"", 1)
	if picked == text {
		t.Fatal(text)
	}
	writeFile(t, qwenSettingsPath(home), picked)
	status := statusOf(t, home, QwenCode, origin)
	if status.State != cc.Configured || *status.Models.Model != "glm-5" {
		t.Fatalf("%+v", status)
	}
	writeFile(t, qwenSettingsPath(home), text)

	// The values come back; a one-line object AstrLink added to stays on
	// several lines.
	removeAt(t, home, QwenCode)
	restored, _ := readFile(t, qwenSettingsPath(home))
	if !strings.Contains(restored, "// my own DashScope setup") || !reflect.DeepEqual(readQwen(t, home), jsonc(t, qwenUser)) {
		t.Fatalf("%s", restored)
	}
}
