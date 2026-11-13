package clients

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func crushConfigPath(home agents.Home) string {
	return filepath.Join(home.Dir(), ".config", "crush", "crush.json")
}

func crushRead(t *testing.T, text string) any {
	t.Helper()
	value, err := cc.ReadJSON(Crush.Files()[0], text)
	if err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return value
}

func crushFile(t *testing.T, home agents.Home) any {
	t.Helper()
	text, _ := readFile(t, crushConfigPath(home))
	return crushRead(t, text)
}

func TestCrushWritesAProviderPerProtocolAndTheLargeAndSmallModels(t *testing.T) {
	home := scratchHome(t)
	mkdir(t, filepath.Dir(crushDataFile(home)))
	chosen := listed(models("model", "claude-sonnet-5"), "claude-sonnet-5", "deepseek-v4")
	chosen.Protocols = map[string]string{"claude-sonnet-5": "anthropic.messages"}
	details := cc.Catalog{"claude-sonnet-5": {ContextWindow: cc.Some(uint64(200_000)), MaxTokens: cc.Some(uint64(64_000))}}
	if asked := writePlace(t, home, place(Crush), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	picked := map[string]any{"provider": "local-anthropic", "model": "claude-sonnet-5"}
	want := map[string]any{
		"$schema": "https://charm.land/crush.json",
		"providers": map[string]any{
			"local-chat": map[string]any{
				"name": "AstrLink", "type": "completions", "base_url": origin + "/v1", "api_key": token,
				"models": []any{map[string]any{"id": "deepseek-v4", "name": "deepseek-v4", "context_window": number(128_000), "default_max_tokens": number(16_000)}},
			},
			"local-anthropic": map[string]any{
				"name": "AstrLink", "type": "messages", "base_url": origin, "api_key": token,
				"models": []any{map[string]any{"id": "claude-sonnet-5", "name": "claude-sonnet-5", "context_window": number(200_000), "default_max_tokens": number(64_000)}},
			},
		},
		"models": map[string]any{"large": picked, "small": picked},
	}
	if got := crushFile(t, home); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	status := statusOf(t, home, Crush, origin)
	if status.State != cc.Configured || *status.Models.Model != "claude-sonnet-5" || len(status.Notes) != 0 {
		t.Fatalf("%+v", status)
	}

	writeFile(t, crushDataFile(home), `{"models": {"large": {"provider": "anthropic", "model": "claude-opus-5"}}}`)
	sameStrings(t, statusOf(t, home, Crush, origin).Notes, "overridden")

	syncAt(t, home, "http://127.0.0.1:9000/")
	synced := crushFile(t, home)
	if at(synced, "providers", "local-chat", "base_url") != "http://127.0.0.1:9000/v1" ||
		at(synced, "providers", "local-anthropic", "base_url") != "http://127.0.0.1:9000" {
		t.Fatalf("%v", synced)
	}

	removeAt(t, home, Crush)
	if _, exists := readFile(t, crushConfigPath(home)); exists {
		t.Fatal("crush.json left")
	}
}

const crushUser = `{
  "$schema": "https://charm.land/crush.json",
  "options": { "disable_provider_auto_update": true },
  "providers": {
    "deepseek": { "api_key": "sk-deep" }
  },
  "models": {
    "large": { "provider": "deepseek", "model": "deepseek-chat" }
  }
}
`

func TestCrushKeepsTheUsersProvidersAndPutsTheirModelsBack(t *testing.T) {
	home := scratchHome(t)
	writeFile(t, crushConfigPath(home), crushUser)
	chosen := connection(models("model", "deepseek-v4"))
	asked := writeAt(t, home, Crush, chosen, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "models.large")
	writeAt(t, home, Crush, chosen, true)
	written := crushFile(t, home)
	if at(written, "providers", "deepseek", "api_key") != "sk-deep" || at(written, "models", "large", "provider") != "local-chat" {
		t.Fatalf("%v", written)
	}

	removeAt(t, home, Crush)
	if got := crushFile(t, home); !reflect.DeepEqual(got, crushRead(t, crushUser)) {
		t.Fatalf("%v", got)
	}
}
