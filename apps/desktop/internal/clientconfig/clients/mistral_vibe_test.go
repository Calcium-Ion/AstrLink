package clients

import (
	"path/filepath"
	"strings"
	"testing"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func TestVibeWritesAProviderPerProtocolAndTheTokenToEnv(t *testing.T) {
	home := scratchHome(t)
	dir := mkdir(t, home.Dir(), ".vibe")
	chosen := listed(models("model", "gpt-5.5"), "claude-sonnet-5", "gpt-5.5", "deepseek-v4")
	chosen.Protocols = map[string]string{"gpt-5.5": "openai.responses", "claude-sonnet-5": "anthropic.messages"}
	details := cc.Catalog{"gpt-5.5": {ContextWindow: cc.Some(uint64(400_000)), MaxTokens: cc.Some(uint64(128_000))}}
	if asked := writePlace(t, home, place(MistralVibe), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	want := `active_model = "gpt-5.5"

[[providers]]
name = "local-chat"
api_base = "` + origin + `/v1"
api_key_env_var = "LOCAL_GATEWAY_API_KEY"
api_style = "openai"
backend = "generic"

[[providers]]
name = "local-responses"
api_base = "` + origin + `/v1"
api_key_env_var = "LOCAL_GATEWAY_API_KEY"
api_style = "openai-responses"
backend = "generic"

[[providers]]
name = "local-anthropic"
api_base = "` + origin + `"
api_key_env_var = "LOCAL_GATEWAY_API_KEY"
api_style = "anthropic"
backend = "generic"

[[models]]
name = "deepseek-v4"
provider = "local-chat"
alias = "deepseek-v4"

[[models]]
name = "gpt-5.5"
provider = "local-responses"
alias = "gpt-5.5"
max_context_length = 400000
max_output_tokens = 128000

[[models]]
name = "claude-sonnet-5"
provider = "local-anthropic"
alias = "claude-sonnet-5"
`
	if text, _ := readFile(t, filepath.Join(dir, "config.toml")); text != want {
		t.Fatalf("%s", text)
	}
	if text, _ := readFile(t, filepath.Join(dir, ".env")); text != "LOCAL_GATEWAY_API_KEY="+token+"\n" {
		t.Fatalf("%q", text)
	}
	status := statusOf(t, home, MistralVibe, origin)
	if status.State != cc.Configured || *status.Models.Model != "gpt-5.5" {
		t.Fatalf("%+v", status)
	}

	syncAt(t, home, "http://127.0.0.1:9000/")
	synced, _ := readFile(t, filepath.Join(dir, "config.toml"))
	if !strings.Contains(synced, `api_base = "http://127.0.0.1:9000/v1"`) || !strings.Contains(synced, "api_base = \"http://127.0.0.1:9000\"\n") {
		t.Fatal(synced)
	}

	removeAt(t, home, MistralVibe)
	for _, name := range []string{"config.toml", ".env"} {
		if _, exists := readFile(t, filepath.Join(dir, name)); exists {
			t.Fatalf("%s left", name)
		}
	}
}

const (
	vibeUserConfig = "# my vibe\nactive_model = \"devstral\"\n\n[[providers]]\nname = \"mistral\"\napi_base = \"https://api.mistral.ai/v1\"\napi_key_env_var = \"MISTRAL_API_KEY\"\n\n[[models]]\nname = \"devstral-2\"\nprovider = \"mistral\"\nalias = \"devstral\"\n"
	vibeUserEnv    = "MISTRAL_API_KEY=sk-mistral\n"
)

func TestVibeKeepsTheUsersProvidersAndPutsTheirModelBack(t *testing.T) {
	home := scratchHome(t)
	dir := filepath.Join(home.Dir(), ".vibe")
	writeFile(t, filepath.Join(dir, "config.toml"), vibeUserConfig)
	writeFile(t, filepath.Join(dir, ".env"), vibeUserEnv)
	chosen := connection(models("model", "deepseek-v4"))
	asked := writeAt(t, home, MistralVibe, chosen, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "active_model")
	writeAt(t, home, MistralVibe, chosen, true)
	written, _ := readFile(t, filepath.Join(dir, "config.toml"))
	if !strings.HasPrefix(written, "# my vibe\nactive_model = \"deepseek-v4\"\n") ||
		!strings.Contains(written, `name = "mistral"`) || !strings.Contains(written, `name = "local-chat"`) {
		t.Fatal(written)
	}
	if env, _ := readFile(t, filepath.Join(dir, ".env")); env != vibeUserEnv+"LOCAL_GATEWAY_API_KEY="+token+"\n" {
		t.Fatalf("%q", env)
	}

	removeAt(t, home, MistralVibe)
	if text, _ := readFile(t, filepath.Join(dir, "config.toml")); text != vibeUserConfig {
		t.Fatalf("%q", text)
	}
	if text, _ := readFile(t, filepath.Join(dir, ".env")); text != vibeUserEnv {
		t.Fatalf("%q", text)
	}
}
