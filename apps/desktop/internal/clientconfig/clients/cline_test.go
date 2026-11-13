package clients

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func clineFile(home agents.Home) string {
	return filepath.Join(home.Dir(), ".cline", "data", "settings", "providers.json")
}

func clineRead(t *testing.T, text string) any {
	t.Helper()
	value, err := cc.ReadJSON(Cline.Files()[0], text)
	if err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return value
}

func clineFileValue(t *testing.T, home agents.Home) map[string]any {
	t.Helper()
	text, _ := readFile(t, clineFile(home))
	return clineRead(t, text).(map[string]any)
}

func TestClineWritesTheProviderTheDefaultModelTakes(t *testing.T) {
	home := scratchHome(t)
	mkdir(t, home.Dir(), ".cline", "data")
	chosen := connection(models("model", "claude-sonnet-5"))
	chosen.Protocols = map[string]string{"claude-sonnet-5": "anthropic.messages"}
	details := cc.Catalog{"claude-sonnet-5": {ContextWindow: cc.Some(uint64(200_000)), MaxTokens: cc.Some(uint64(64_000))}}
	if asked := writePlace(t, home, place(Cline), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	written := clineFileValue(t, home)
	anthropic := at(written, "providers", "anthropic").(map[string]any)
	if stamp, _ := anthropic["updatedAt"].(string); !strings.HasSuffix(stamp, "Z") {
		t.Fatal(anthropic["updatedAt"])
	}
	anthropic["updatedAt"] = nil
	want := map[string]any{
		"version": number(1),
		"modes":   map[string]any{},
		"providers": map[string]any{"anthropic": map[string]any{
			"settings": map[string]any{
				"provider": "anthropic", "apiKey": token, "baseUrl": origin + "/v1",
				"model": "claude-sonnet-5", "contextWindow": number(200_000), "maxTokens": number(64_000),
			},
			"updatedAt":   nil,
			"tokenSource": "manual",
		}},
		"lastUsedProvider": "anthropic",
	}
	if !reflect.DeepEqual(written, want) {
		t.Fatalf("%v", written)
	}
	status := statusOf(t, home, Cline, origin)
	if status.State != cc.Configured || *status.Models.Model != "claude-sonnet-5" || len(status.Notes) != 0 {
		t.Fatalf("%+v", status)
	}

	// A chat model moves the choice to the compatible provider.
	writeAt(t, home, Cline, connection(models("model", "deepseek-v4")), false)
	written = clineFileValue(t, home)
	if at(written, "providers", "anthropic") != nil || written["lastUsedProvider"] != "openai-compatible" {
		t.Fatalf("%v", written)
	}

	writeFile(t, filepath.Join(home.Dir(), ".cline", "data", "globalState.json"), `{"actModeApiProvider": "openrouter"}`)
	sameStrings(t, statusOf(t, home, Cline, origin).Notes, "legacy")

	syncAt(t, home, "http://127.0.0.1:9000/")
	if got := at(clineFileValue(t, home), "providers", "openai-compatible", "settings", "baseUrl"); got != "http://127.0.0.1:9000/v1" {
		t.Fatal(got)
	}

	removeAt(t, home, Cline)
	if _, exists := readFile(t, clineFile(home)); exists {
		t.Fatal("providers.json left")
	}
}

const clineUser = `{
  "version": 1,
  "lastUsedProvider": "openai-compatible",
  "modes": {},
  "providers": {
    "openai-compatible": {
      "settings": {
        "provider": "openai-compatible",
        "apiKey": "sk-mine",
        "model": "qwen3-coder",
        "baseUrl": "http://192.168.1.9:8000/v1"
      },
      "updatedAt": "2026-10-30T08:00:00.000Z",
      "tokenSource": "manual"
    }
  }
}
`

func TestClinePutsTheUsersProviderBack(t *testing.T) {
	home := scratchHome(t)
	writeFile(t, clineFile(home), clineUser)
	chosen := connection(models("model", "deepseek-v4"))
	asked := writeAt(t, home, Cline, chosen, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "providers.openai-compatible.settings")
	writeAt(t, home, Cline, chosen, true)
	written := clineFileValue(t, home)
	if at(written, "providers", "openai-compatible", "settings", "apiKey") != token ||
		at(written, "providers", "openai-compatible", "updatedAt") != "2026-10-30T08:00:00.000Z" {
		t.Fatalf("%v", written)
	}

	removeAt(t, home, Cline)
	if got := clineFileValue(t, home); !reflect.DeepEqual(any(got), clineRead(t, clineUser)) {
		t.Fatalf("%v", got)
	}
}
