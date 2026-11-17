package clients

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func droidSettingsPath(home agents.Home) string {
	return filepath.Join(home.Dir(), ".factory", "settings.json")
}

func droidRead(t *testing.T, home agents.Home) any {
	t.Helper()
	text, _ := readFile(t, droidSettingsPath(home))
	return jsonc(t, text)
}

func droidEntryOf(id, url, provider string) map[string]any {
	return map[string]any{"id": "custom:" + id, "model": id, "displayName": id, "baseUrl": url, "apiKey": token, "provider": provider}
}

func TestDroidWritesAnEntryPerModelWithItsProvider(t *testing.T) {
	home := scratchHome(t)
	mkdir(t, home.Dir(), ".factory")
	chosen := listed(models("model", "claude-sonnet-5"), "claude-sonnet-5", "gpt-5.6", "glm-5")
	chosen.Protocols = map[string]string{"claude-sonnet-5": "anthropic.messages", "gpt-5.6": "openai.responses"}
	details := cc.Catalog{"claude-sonnet-5": {ContextWindow: cc.Some(uint64(200_000)), MaxTokens: cc.Some(uint64(64_000))}}
	if asked := writePlace(t, home, place(Droid), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	claude := droidEntryOf("claude-sonnet-5", origin, "anthropic")
	claude["maxContextLimit"], claude["maxOutputTokens"] = number(200_000), number(64_000)
	want := map[string]any{
		"customModels": []any{
			claude,
			droidEntryOf("gpt-5.6", origin+"/v1", "openai"),
			droidEntryOf("glm-5", origin+"/v1", "generic-chat-completion-api"),
		},
		"sessionDefaultSettings": map[string]any{"model": "custom:claude-sonnet-5"},
	}
	if got := droidRead(t, home); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	status := statusOf(t, home, Droid, origin)
	if status.State != cc.Configured || *status.Models.Model != "claude-sonnet-5" || len(status.Notes) != 0 {
		t.Fatalf("%+v", status)
	}

	syncAt(t, home, "http://127.0.0.1:9000/")
	synced := at(droidRead(t, home), "customModels").([]any)
	if at(synced[0], "baseUrl") != "http://127.0.0.1:9000" || at(synced[2], "baseUrl") != "http://127.0.0.1:9000/v1" {
		t.Fatalf("%v", synced)
	}

	writeFile(t, filepath.Join(home.Dir(), ".factory", "settings.local.json"), `{"sessionDefaultSettings": {"model": "gpt-5.5"}}`)
	status = statusOf(t, home, Droid, "http://127.0.0.1:9000")
	if status.State != cc.Configured {
		t.Fatal(status.State)
	}
	sameStrings(t, status.Notes, "overridden")

	removeAt(t, home, Droid)
	if _, exists := readFile(t, droidSettingsPath(home)); exists {
		t.Fatal("settings left")
	}
}

const droidUser = `{
  "customModels": [
    { "model": "kimi-k3", "displayName": "Kimi", "baseUrl": "https://api.moonshot.cn/v1",
      "apiKey": "sk-moon", "provider": "generic-chat-completion-api" }
  ],
  "sessionDefaultSettings": { "model": "custom:Kimi-0", "autonomyMode": "auto-low" },
  "cloudSessionSync": false
}
`

func TestDroidLeadsTheUsersModelsAndPutsTheirDefaultBack(t *testing.T) {
	home := scratchHome(t)
	writeFile(t, droidSettingsPath(home), droidUser)
	chosen := connection(models("model", "glm-5"))
	asked := writeAt(t, home, Droid, chosen, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "sessionDefaultSettings.model")
	writeAt(t, home, Droid, chosen, true)
	written := droidRead(t, home)
	list := at(written, "customModels").([]any)
	if at(list[0], "id") != "custom:glm-5" || at(list[1], "apiKey") != "sk-moon" ||
		at(written, "sessionDefaultSettings", "autonomyMode") != "auto-low" || at(written, "cloudSessionSync") != false {
		t.Fatalf("%v", written)
	}

	removeAt(t, home, Droid)
	if got := droidRead(t, home); !reflect.DeepEqual(got, jsonc(t, droidUser)) {
		t.Fatalf("%v", got)
	}
}
