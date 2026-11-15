package clients

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

const kimiFactory = "# ~/.kimi-code/config.toml\n# Runtime settings for Kimi Code.\n# This file starts empty so built-in defaults can apply.\n# Login will populate managed Kimi provider and model entries.\n"

func kimiConfigPath(home agents.Home) string {
	return filepath.Join(home.Dir(), ".kimi-code", "config.toml")
}

func TestKimiWritesAProviderPerProtocolAndAnAliasPerModel(t *testing.T) {
	home := scratchHome(t)
	writeFile(t, kimiConfigPath(home), kimiFactory)
	chosen := listed(models("model", "claude-sonnet-5"), "claude-sonnet-5", "deepseek-v4-pro", "gpt-5.5")
	chosen.Protocols = map[string]string{"claude-sonnet-5": "anthropic.messages", "gpt-5.5": "openai.responses"}
	details := cc.Catalog{"gpt-5.5": {
		Reasoning: []string{"none", "low", "medium", "high", "xhigh"}, HasReasoning: true,
		ContextWindow: cc.Some(uint64(400_000)), MaxTokens: cc.Some(uint64(128_000)),
	}}
	if asked := writePlace(t, home, place(KimiCode), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	text, _ := readFile(t, kimiConfigPath(home))
	if !strings.HasPrefix(text, kimiFactory) || !strings.Contains(text, `[models."astrlink/gpt-5.5"]`) {
		t.Fatal(text)
	}
	written := tomlValue(t, text)
	providers := at(written, "providers")
	gpt := at(written, "models", "astrlink/gpt-5.5")
	checks := map[any]any{
		at(written, "default_model"):                                          "astrlink/claude-sonnet-5",
		at(providers, "astrlink-anthropic", "type"):                           "anthropic",
		at(providers, "astrlink-anthropic", "base_url"):                       origin,
		at(providers, "astrlink-anthropic", "api_key"):                        token,
		at(providers, "astrlink-chat", "base_url"):                            origin + "/v1",
		at(providers, "astrlink-responses", "type"):                           "openai_responses",
		at(gpt, "provider"):                                                   "astrlink-responses",
		at(gpt, "model"):                                                      "gpt-5.5",
		at(gpt, "max_context_size"):                                           number(400_000),
		at(gpt, "max_output_size"):                                            number(128_000),
		at(gpt, "off_effort"):                                                 "none",
		at(written, "models", "astrlink/deepseek-v4-pro", "max_context_size"): number(131_072),
	}
	for got, want := range checks {
		if got != want {
			t.Errorf("got %v, want %v", got, want)
		}
	}
	if at(providers, "astrlink-gemini") != nil || !reflect.DeepEqual(at(gpt, "support_efforts"), []any{"low", "medium", "high", "xhigh"}) {
		t.Fatal(text)
	}
	status := statusOf(t, home, KimiCode, origin)
	if status.State != cc.Configured || *status.Models.Model != "claude-sonnet-5" || len(status.Models.Models) != 3 {
		t.Fatalf("%+v", status)
	}

	syncAt(t, home, "http://127.0.0.1:9000/")
	text, _ = readFile(t, kimiConfigPath(home))
	synced := tomlValue(t, text)
	if at(synced, "providers", "astrlink-anthropic", "base_url") != "http://127.0.0.1:9000" ||
		at(synced, "providers", "astrlink-chat", "base_url") != "http://127.0.0.1:9000/v1" {
		t.Fatal(text)
	}

	removeAt(t, home, KimiCode)
	if text, _ := readFile(t, kimiConfigPath(home)); text != kimiFactory {
		t.Fatalf("%q", text)
	}
}

const kimiLoggedIn = `default_model = "kimi-code/k3"
default_permission_mode = "manual"
telemetry = true

[providers."managed:kimi-code"]
type = "kimi"
base_url = "https://api.kimi.com/coding/v1"
api_key = ""

[models."kimi-code/k3"]
provider = "managed:kimi-code"
model = "k3"
max_context_size = 1048576
capabilities = [ "thinking", "always_thinking", "image_in", "video_in", "tool_use" ]
support_efforts = [ "low", "high", "max" ]
default_effort = "max"

[thinking]
enabled = true
effort = "high"

[[permission.rules]]
decision = "allow"
pattern = "Read"
`

func TestKimiJoinsALoggedInConfigAndPutsItBack(t *testing.T) {
	home := scratchHome(t)
	writeFile(t, kimiConfigPath(home), kimiLoggedIn)
	chosen := connection(models("model", "deepseek-v4-pro"))
	asked := writeAt(t, home, KimiCode, chosen, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "default_model")
	writeAt(t, home, KimiCode, chosen, true)
	text, _ := readFile(t, kimiConfigPath(home))
	written := tomlValue(t, text)
	if at(written, "default_model") != "astrlink/deepseek-v4-pro" || at(written, "models", "kimi-code/k3", "model") != "k3" ||
		at(written, "models", "astrlink/deepseek-v4-pro", "provider") != "astrlink-chat" {
		t.Fatal(text)
	}

	// `/model` picks Kimi's own model again: AstrLink's config stays.
	picked := strings.Replace(text, `default_model = "astrlink/deepseek-v4-pro"`, `default_model = "kimi-code/k3"`, 1)
	writeFile(t, kimiConfigPath(home), picked)
	status := statusOf(t, home, KimiCode, origin)
	if status.State != cc.Configured || *status.Models.Model != "deepseek-v4-pro" {
		t.Fatalf("%+v", status)
	}
	removeAt(t, home, KimiCode)
	if text, _ := readFile(t, kimiConfigPath(home)); text != kimiLoggedIn {
		t.Fatalf("%q", text)
	}
}
