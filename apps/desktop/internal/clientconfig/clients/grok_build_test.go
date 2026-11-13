package clients

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

const grokUser = "# Grok Build settings\n[models]\ndefault = \"grok-4.6\"\n\n[model.\"glm-5\"]\nmodel = \"glm-5\"\nbase_url = \"https://open.bigmodel.cn/api/paas/v4\"\napi_key = \"zk-own\"\n\n[ui]\nscreen_mode = \"minimal\"\n"

func grokConfigPath(home agents.Home) string {
	return filepath.Join(home.Dir(), ".grok", "config.toml")
}

func grokRead(t *testing.T, home agents.Home) any {
	t.Helper()
	text, _ := readFile(t, grokConfigPath(home))
	return tomlValue(t, text)
}

func TestGrokWritesATablePerModelWithItsOwnKey(t *testing.T) {
	home := scratchHome(t)
	mkdir(t, home.Dir(), ".grok")
	chosen := listed(models("model", "claude-sonnet-5"), "claude-sonnet-5", "gpt-5.6", "glm-5")
	chosen.Protocols = map[string]string{"claude-sonnet-5": "anthropic.messages", "gpt-5.6": "openai.responses"}
	details := cc.Catalog{"gpt-5.6": {ContextWindow: cc.Some(uint64(400_000)), MaxTokens: cc.Some(uint64(128_000))}}
	if asked := writePlace(t, home, place(GrokBuild), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	written := grokRead(t, home)
	claude, gpt, glm := at(written, "model", "claude-sonnet-5"), at(written, "model", "gpt-5.6"), at(written, "model", "glm-5")
	checks := []struct{ got, want any }{
		{at(written, "models", "default"), "claude-sonnet-5"},
		{at(written, "models", "session_summary"), "claude-sonnet-5"},
		{at(claude, "description"), "AstrLink"},
		{at(claude, "api_backend"), "messages"},
		{at(claude, "base_url"), origin + "/v1"},
		{at(claude, "api_key"), token},
		{at(claude, "max_completion_tokens"), number(32_000)},
		{at(gpt, "api_backend"), "responses"},
		{at(gpt, "context_window"), number(400_000)},
		{at(gpt, "max_completion_tokens"), number(128_000)},
		{at(glm, "api_backend"), "chat_completions"},
		{at(glm, "max_completion_tokens"), nil},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("got %v, want %v", check.got, check.want)
		}
	}
	status := statusOf(t, home, GrokBuild, origin)
	if status.State != cc.Configured || *status.Models.Model != "claude-sonnet-5" || len(status.Models.Models) != 3 {
		t.Fatalf("%+v", status)
	}

	syncAt(t, home, "http://127.0.0.1:9000/")
	if got := at(grokRead(t, home), "model", "glm-5", "base_url"); got != "http://127.0.0.1:9000/v1" {
		t.Fatal(got)
	}
	if state := statusOf(t, home, GrokBuild, "http://127.0.0.1:9000").State; state != cc.Configured {
		t.Fatal(state)
	}

	removeAt(t, home, GrokBuild)
	if text, exists := readFile(t, grokConfigPath(home)); exists {
		t.Fatalf("%q", text)
	}
}

func TestGrokAsksBeforeReplacingTheUsersModelAndDefaultAndPutsThemBack(t *testing.T) {
	home := scratchHome(t)
	writeFile(t, grokConfigPath(home), grokUser)
	chosen := connection(models("model", "glm-5"))
	asked := writeAt(t, home, GrokBuild, chosen, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "model.glm-5", "models.default")
	writeAt(t, home, GrokBuild, chosen, true)
	text, _ := readFile(t, grokConfigPath(home))
	if !strings.HasPrefix(text, "# Grok Build settings\n[models]\ndefault = \"glm-5\"") {
		t.Fatal(text)
	}
	written := grokRead(t, home)
	if at(written, "model", "glm-5", "api_key") != token || at(written, "model", "glm-5", "description") != "AstrLink" ||
		at(written, "ui", "screen_mode") != "minimal" {
		t.Fatal(text)
	}

	removeAt(t, home, GrokBuild)
	if text, _ := readFile(t, grokConfigPath(home)); text != grokUser {
		t.Fatalf("%q", text)
	}
}

func TestTheCommunityGrokCLIAloneIsNotGrokBuild(t *testing.T) {
	home := scratchHome(t)
	writeFile(t, filepath.Join(home.Dir(), ".grok", "user-settings.json"), `{"apiKey":"x"}`)
	if GrokBuild.Detected(home, "") {
		t.Fatal("community grok-cli detected")
	}
	if err := os.Mkdir(filepath.Join(home.Dir(), ".grok", "downloads"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !GrokBuild.Detected(home, "") {
		t.Fatal("Grok Build not detected")
	}
}
