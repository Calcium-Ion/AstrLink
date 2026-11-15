package clients

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func gooseDir(home agents.Home) string { return Goose.Dir(home, "") }

func gooseProviderValue(t *testing.T, home agents.Home, name string) any {
	t.Helper()
	text, _ := readFile(t, filepath.Join(gooseDir(home), "custom_providers", name+".json"))
	return decode(t, text)
}

func gooseShared(name, engine, display, url string, models []any) map[string]any {
	return map[string]any{
		"name": name, "engine": engine, "display_name": display, "base_url": url,
		"api_key_env": "LOCAL_GATEWAY_API_KEY", "requires_auth": true, "supports_streaming": true,
		"dynamic_models": false, "models": models,
	}
}

func TestGooseWritesAProviderPerEngineAndTheActiveOne(t *testing.T) {
	home := scratchHome(t)
	mkdir(t, gooseDir(home))
	protocols := map[string]string{"gpt-5.5": "openai.responses", "claude-sonnet-5": "anthropic.messages"}
	chosen := listed(models("model", "claude-sonnet-5"), "claude-sonnet-5", "gpt-5.5", "deepseek-v4")
	chosen.Protocols = protocols
	details := cc.Catalog{"gpt-5.5": {
		Reasoning: []string{"low", "high"}, HasReasoning: true,
		ContextWindow: cc.Some(uint64(400_000)), MaxTokens: cc.Some(uint64(128_000)),
	}}
	if asked := writePlace(t, home, place(Goose), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	openai := gooseShared("local_gateway", "openai", "AstrLink", origin+"/v1", []any{
		map[string]any{"name": "gpt-5.5", "context_limit": number(400_000), "reasoning": true},
		map[string]any{"name": "deepseek-v4", "context_limit": number(128_000)},
	})
	if got := gooseProviderValue(t, home, "local_gateway"); !reflect.DeepEqual(got, any(openai)) {
		t.Fatalf("%v", got)
	}
	anthropic := gooseShared("local_gateway_anthropic", "anthropic", "AstrLink Anthropic", origin, []any{
		map[string]any{"name": "claude-sonnet-5", "context_limit": number(128_000)},
	})
	if got := gooseProviderValue(t, home, "local_gateway_anthropic"); !reflect.DeepEqual(got, any(anthropic)) {
		t.Fatalf("%v", got)
	}
	configPath := filepath.Join(gooseDir(home), "config.yaml")
	if text, _ := readFile(t, configPath); text != "active_provider: local_gateway_anthropic\nproviders:\n  local_gateway_anthropic:\n    enabled: true\n    model: claude-sonnet-5\n    configured: true\n" {
		t.Fatalf("%q", text)
	}
	status := statusOf(t, home, Goose, origin)
	if status.State != cc.Configured || *status.Models.Model != "claude-sonnet-5" || status.PasteSinceUnix == nil {
		t.Fatalf("%+v", status)
	}
	sameStrings(t, status.PasteInto, "AstrLink")
	since := *status.PasteSinceUnix

	// The keychain entry does not name the port.
	syncAt(t, home, "http://127.0.0.1:9000/")
	if at(gooseProviderValue(t, home, "local_gateway"), "base_url") != "http://127.0.0.1:9000/v1" ||
		at(gooseProviderValue(t, home, "local_gateway_anthropic"), "base_url") != "http://127.0.0.1:9000" {
		t.Fatal("not synced")
	}

	// Without a Claude model, its provider file goes; an empty one would
	// keep Goose from loading any.
	fewer := listed(models("model", "deepseek-v4"), "gpt-5.5", "deepseek-v4")
	fewer.Origin, fewer.Protocols = "http://127.0.0.1:9000", protocols
	if _, _, err := cc.WriteWith(home, place(Goose), fewer, details, tokens(), false); err != nil {
		t.Fatal(err)
	}
	if _, exists := readFile(t, filepath.Join(gooseDir(home), "custom_providers", "local_gateway_anthropic.json")); exists {
		t.Fatal("anthropic provider left")
	}
	if text, _ := readFile(t, configPath); text != "active_provider: local_gateway\nproviders:\n  local_gateway:\n    enabled: true\n    model: deepseek-v4\n    configured: true\n" {
		t.Fatalf("%q", text)
	}
	status = statusOf(t, home, Goose, "http://127.0.0.1:9000")
	if status.State != cc.Configured || status.PasteSinceUnix == nil || *status.PasteSinceUnix != since {
		t.Fatalf("%+v", status)
	}

	removeAt(t, home, Goose)
	for _, path := range []string{"config.yaml", "custom_providers/local_gateway.json", "custom_providers/local_gateway_anthropic.json"} {
		if _, exists := readFile(t, filepath.Join(gooseDir(home), path)); exists {
			t.Fatalf("%s left", path)
		}
	}
}

const gooseUser = "# my goose\nactive_provider: openai\nproviders:\n  openai:\n    enabled: true\n    model: gpt-5\n    configured: true\nextensions:\n  developer:\n    enabled: true\n"

func TestGooseAddsItsProviderBesideTheUsersAndPutsTheirsBack(t *testing.T) {
	home := scratchHome(t)
	configPath := filepath.Join(gooseDir(home), "config.yaml")
	writeFile(t, configPath, gooseUser)
	chosen := connection(models("model", "deepseek-v4"))
	asked := writeAt(t, home, Goose, chosen, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "active_provider")
	writeAt(t, home, Goose, chosen, true)
	written, _ := readFile(t, configPath)
	if !strings.HasPrefix(written, "# my goose\nactive_provider: local_gateway\n") || !strings.Contains(written, "  local_gateway:\n    enabled: true\n") {
		t.Fatal(written)
	}

	removeAt(t, home, Goose)
	if text, _ := readFile(t, configPath); text != gooseUser {
		t.Fatalf("%q", text)
	}
	if _, exists := readFile(t, filepath.Join(gooseDir(home), "custom_providers", "local_gateway.json")); exists {
		t.Fatal("provider left")
	}
}
