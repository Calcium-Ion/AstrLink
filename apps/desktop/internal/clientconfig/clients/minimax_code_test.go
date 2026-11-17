package clients

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func miniMaxPath(home agents.Home) string {
	return filepath.Join(home.Dir(), ".minimax", "config.yaml")
}

func TestMiniMaxCodeWritesAProviderPerProtocolAndTheDefault(t *testing.T) {
	home := scratchHome(t)
	mkdir(t, home.Dir(), ".minimax")
	chosen := listed(models("model", "gpt-5.5"), "gpt-5.5", "claude-sonnet-5", "deepseek-v4")
	chosen.Protocols = map[string]string{"gpt-5.5": "openai.responses", "claude-sonnet-5": "anthropic.messages"}
	details := cc.Catalog{"gpt-5.5": {Reasoning: []string{"low", "high"}, HasReasoning: true, ContextWindow: cc.Some(uint64(400_000))}}
	if asked := writePlace(t, home, place(MiniMaxCode), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	want := `custom_provider:
  local-chat:
    name: AstrLink
    kind: custom
    enabled: true
    api: openai-completions
    options:
      baseURL: ` + origin + `/v1
      apiKey: ` + token + `
    models:
      deepseek-v4:
        name: deepseek-v4
        enabled: true
  local-responses:
    name: "AstrLink Responses"
    kind: custom
    enabled: true
    api: openai-responses
    options:
      baseURL: ` + origin + `/v1
      apiKey: ` + token + `
    models:
      gpt-5.5:
        name: gpt-5.5
        enabled: true
        reasoning: true
        thinking:
          effortOptions:
            - low
            - high
        limit:
          context: 400000
          output: 16384
  local-anthropic:
    name: "AstrLink Anthropic"
    kind: custom
    enabled: true
    api: anthropic-messages
    options:
      baseURL: ` + origin + `
      apiKey: ` + token + `
    models:
      claude-sonnet-5:
        name: claude-sonnet-5
        enabled: true
defaultModel: custom_provider:local-responses/gpt-5.5
`
	if text, _ := readFile(t, miniMaxPath(home)); text != want {
		t.Fatalf("%s", text)
	}
	status := statusOf(t, home, MiniMaxCode, origin)
	if status.State != cc.Configured || *status.Models.Model != "gpt-5.5" {
		t.Fatalf("%+v", status)
	}

	syncAt(t, home, "http://127.0.0.1:9000/")
	synced, _ := readFile(t, miniMaxPath(home))
	if strings.Count(synced, "baseURL: http://127.0.0.1:9000/v1\n") != 2 || !strings.Contains(synced, "baseURL: http://127.0.0.1:9000\n") {
		t.Fatal(synced)
	}

	removeAt(t, home, MiniMaxCode)
	if _, exists := readFile(t, miniMaxPath(home)); exists {
		t.Fatal("file left")
	}
}

const miniMaxUser = "# my mcode\ndefaultModel: minimax/MiniMax-M3\ncustom_provider:\n  my-gateway:\n    name: My Gateway\n    kind: custom\n    api: openai-completions\n    options:\n      baseURL: https://example.invalid/v1\n      apiKey: sk-mine\n    models:\n      glm-5:\n        name: GLM 5\n"

func TestMiniMaxCodeAddsItsProviderBesideTheUsersAndPutsTheirDefaultBack(t *testing.T) {
	home := scratchHome(t)
	writeFile(t, miniMaxPath(home), miniMaxUser)
	chosen := connection(models("model", "deepseek-v4"))
	asked := writePlace(t, home, place(MiniMaxCode), chosen, cc.Catalog{}, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "defaultModel")
	writePlace(t, home, place(MiniMaxCode), chosen, cc.Catalog{}, true)
	written, _ := readFile(t, miniMaxPath(home))
	if !strings.HasPrefix(written, "# my mcode\ndefaultModel: custom_provider:local-chat/deepseek-v4\n") ||
		!strings.Contains(written, "  my-gateway:\n") || !strings.Contains(written, "  local-chat:\n") {
		t.Fatal(written)
	}

	removeAt(t, home, MiniMaxCode)
	if text, _ := readFile(t, miniMaxPath(home)); text != miniMaxUser {
		t.Fatalf("%q", text)
	}
}
