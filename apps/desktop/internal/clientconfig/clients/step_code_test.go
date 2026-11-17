package clients

import (
	"path/filepath"
	"reflect"
	"testing"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func TestStepCodeWritesAProviderPerProtocolAndTheStartupModel(t *testing.T) {
	home := scratchHome(t)
	dir := mkdir(t, home.Dir(), ".stepcode")
	chosen := listed(models("model", "claude-sonnet-5"), "claude-sonnet-5", "deepseek-v4")
	chosen.Protocols = map[string]string{"claude-sonnet-5": "anthropic.messages"}
	details := cc.Catalog{"claude-sonnet-5": {Reasoning: []string{"low", "high"}, HasReasoning: true, ContextWindow: cc.Some(uint64(200_000)), MaxTokens: cc.Some(uint64(64_000))}}
	if asked := writePlace(t, home, place(StepCode), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	want := `{"providers": {
	  "local-chat": {"baseUrl": "` + origin + `/v1", "api": "openai-completions", "apiKey": "` + token + `",
	    "models": [{"id": "deepseek-v4"}]},
	  "local-anthropic": {"baseUrl": "` + origin + `", "api": "anthropic-messages", "apiKey": "` + token + `",
	    "models": [{"id": "claude-sonnet-5", "reasoning": true, "contextWindow": 200000, "maxTokens": 64000}]}
	}}`
	if got := readJSON(t, filepath.Join(dir, "models.json")); !reflect.DeepEqual(got, decode(t, want)) {
		t.Fatalf("%v", got)
	}
	if text, _ := readFile(t, filepath.Join(dir, "config.toml")); text != "defaultProvider = \"local-anthropic\"\ndefaultModel = \"claude-sonnet-5\"\n" {
		t.Fatalf("%q", text)
	}
	status := statusOf(t, home, StepCode, origin)
	if status.State != cc.Configured || *status.Models.Model != "claude-sonnet-5" {
		t.Fatalf("%+v", status)
	}

	syncAt(t, home, "http://127.0.0.1:9000/")
	synced := readJSON(t, filepath.Join(dir, "models.json"))
	if at(synced, "providers", "local-chat", "baseUrl") != "http://127.0.0.1:9000/v1" || at(synced, "providers", "local-anthropic", "baseUrl") != "http://127.0.0.1:9000" {
		t.Fatalf("%v", synced)
	}

	removeAt(t, home, StepCode)
	for _, name := range []string{"models.json", "config.toml"} {
		if _, exists := readFile(t, filepath.Join(dir, name)); exists {
			t.Fatalf("%s left", name)
		}
	}
}

const (
	stepUserModels = "{\n  // local models\n  \"providers\": {\n    \"ollama\": {\"baseUrl\": \"http://localhost:11434/v1\", \"api\": \"openai-completions\", \"apiKey\": \"ollama\", \"models\": [{\"id\": \"llama3.1:8b\"}]},\n  },\n}\n"
	stepUserConfig = "# step preferences\ndefaultProvider = \"step\"\ndefaultModel = \"step-3.5\"\n"
)

func TestStepCodeKeepsTheUsersProvidersAndPutsTheirModelBack(t *testing.T) {
	home := scratchHome(t)
	dir := mkdir(t, home.Dir(), ".stepcode")
	writeFile(t, filepath.Join(dir, "models.json"), stepUserModels)
	writeFile(t, filepath.Join(dir, "config.toml"), stepUserConfig)
	chosen := connection(models("model", "deepseek-v4"))
	asked := writePlace(t, home, place(StepCode), chosen, cc.Catalog{}, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "defaultProvider", "defaultModel")
	writePlace(t, home, place(StepCode), chosen, cc.Catalog{}, true)
	text, _ := readFile(t, filepath.Join(dir, "models.json"))
	written, err := cc.ReadJSON(StepCode.Files()[0], text)
	if err != nil || at(written, "providers", "ollama", "apiKey") != "ollama" || at(written, "providers", "local-chat", "apiKey") != token {
		t.Fatalf("%s %v", text, err)
	}
	if text, _ := readFile(t, filepath.Join(dir, "config.toml")); text != "# step preferences\ndefaultProvider = \"local-chat\"\ndefaultModel = \"deepseek-v4\"\n" {
		t.Fatalf("%q", text)
	}

	removeAt(t, home, StepCode)
	if text, _ := readFile(t, filepath.Join(dir, "models.json")); text != stepUserModels {
		t.Fatalf("%q", text)
	}
	if text, _ := readFile(t, filepath.Join(dir, "config.toml")); text != stepUserConfig {
		t.Fatalf("%q", text)
	}
}
