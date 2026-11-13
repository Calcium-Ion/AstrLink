package clients

import (
	"path/filepath"
	"strings"
	"testing"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func TestReasonixWritesAProviderPerProtocolAndTheTokenToEnv(t *testing.T) {
	home := scratchHome(t)
	dir := mkdir(t, Reasonix.Dir(home, ""))
	chosen := listed(models("model", "gpt-5.5"), "claude-sonnet-5", "gpt-5.5", "deepseek-v4")
	chosen.Protocols = map[string]string{"gpt-5.5": "openai.responses", "claude-sonnet-5": "anthropic.messages"}
	details := cc.Catalog{"gpt-5.5": {ContextWindow: cc.Some(uint64(400_000)), MaxTokens: cc.Some(uint64(128_000))}}
	if asked := writePlace(t, home, place(Reasonix), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	want := `default_model = "local-responses/gpt-5.5"

[[providers]]
name = "local-chat"
kind = "openai"
base_url = "` + origin + `/v1"
models = ["deepseek-v4"]
default = "deepseek-v4"
api_key_env = "LOCAL_GATEWAY_API_KEY"

[[providers]]
name = "local-responses"
kind = "responses"
base_url = "` + origin + `/v1"
models = ["gpt-5.5"]
default = "gpt-5.5"
api_key_env = "LOCAL_GATEWAY_API_KEY"
model_overrides = { "gpt-5.5" = { context_window = 400000, max_output_tokens = 128000 } }

[[providers]]
name = "local-anthropic"
kind = "anthropic"
base_url = "` + origin + `/v1"
models = ["claude-sonnet-5"]
default = "claude-sonnet-5"
api_key_env = "LOCAL_GATEWAY_API_KEY"
`
	path := filepath.Join(dir, "config.toml")
	if text, _ := readFile(t, path); text != want {
		t.Fatalf("%s", text)
	}
	if text, _ := readFile(t, filepath.Join(dir, ".env")); text != "LOCAL_GATEWAY_API_KEY="+token+"\n" {
		t.Fatalf("%q", text)
	}
	status := statusOf(t, home, Reasonix, origin)
	if status.State != cc.Configured || *status.Models.Model != "gpt-5.5" {
		t.Fatalf("%+v", status)
	}

	// Reasonix stamps its version on the first run.
	text, _ := readFile(t, path)
	writeFile(t, path, strings.Replace(text, "\n\n", "\nconfig_version = 6   # schema marker for diagnostics; old versions may ignore it\n\n", 1))
	if state := statusOf(t, home, Reasonix, origin).State; state != cc.Configured {
		t.Fatal(state)
	}

	syncAt(t, home, "http://127.0.0.1:9000/")
	if synced, _ := readFile(t, path); strings.Count(synced, `base_url = "http://127.0.0.1:9000/v1"`) != 3 {
		t.Fatal(synced)
	}

	removeAt(t, home, Reasonix)
	for _, name := range []string{"config.toml", ".env"} {
		if _, exists := readFile(t, filepath.Join(dir, name)); exists {
			t.Fatalf("%s left", name)
		}
	}
}

const (
	reasonixUserConfig = "# my reasonix\ndefault_model = \"deepseek/deepseek-v4-pro\"\n\n[desktop]\nprovider_access = [\"deepseek\"]\n\n[[providers]]\nname = \"deepseek\"\nkind = \"openai\"\nbase_url = \"https://api.deepseek.com/v1\"\nmodels = [\"deepseek-v4-pro\"]\napi_key_env = \"DEEPSEEK_API_KEY\"\n"
	reasonixUserEnv    = "DEEPSEEK_API_KEY=sk-deep\n"
)

func TestReasonixJoinsTheDesktopListAndPutsTheUsersChoiceBack(t *testing.T) {
	home := scratchHome(t)
	dir := Reasonix.Dir(home, "")
	writeFile(t, filepath.Join(dir, "config.toml"), reasonixUserConfig)
	writeFile(t, filepath.Join(dir, ".env"), reasonixUserEnv)
	chosen := connection(models("model", "deepseek-v4"))
	asked := writeAt(t, home, Reasonix, chosen, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "default_model")
	writeAt(t, home, Reasonix, chosen, true)
	written, _ := readFile(t, filepath.Join(dir, "config.toml"))
	if !strings.HasPrefix(written, "# my reasonix\ndefault_model = \"local-chat/deepseek-v4\"\n") ||
		!strings.Contains(written, `provider_access = ["deepseek", "local-chat"]`) || !strings.Contains(written, `name = "deepseek"`) {
		t.Fatal(written)
	}
	if state := statusOf(t, home, Reasonix, origin).State; state != cc.Configured {
		t.Fatal(state)
	}
	if env, _ := readFile(t, filepath.Join(dir, ".env")); env != reasonixUserEnv+"LOCAL_GATEWAY_API_KEY="+token+"\n" {
		t.Fatalf("%q", env)
	}

	removeAt(t, home, Reasonix)
	if text, _ := readFile(t, filepath.Join(dir, "config.toml")); text != reasonixUserConfig {
		t.Fatalf("%q", text)
	}
	if text, _ := readFile(t, filepath.Join(dir, ".env")); text != reasonixUserEnv {
		t.Fatalf("%q", text)
	}
}
