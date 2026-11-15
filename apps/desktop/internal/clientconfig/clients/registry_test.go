package clients

import (
	"testing"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func TestTheCatalogListsEveryClientInTheDialogsOrder(t *testing.T) {
	var ids []string
	for _, client := range cc.All() {
		ids = append(ids, client.ID())
	}
	sameStrings(t, ids, "claude", "codex", "gemini", "opencode", "openclaw", "pi", "workbuddy", "codebuddy",
		"deepseek_harness", "qwen_code", "kimi_code", "grok_build", "droid", "copilot_cli", "vscode_copilot",
		"kilo_code", "crush", "cline", "zed", "goose", "mistral_vibe", "codewhale", "reasonix", "dimagent", "zcode",
		"minimax_code", "step_code", "aider", "chatbox", "qoder_cli", "trae_cli")
	if _, ok := cc.Find("pair"); !ok {
		t.Fatal("the test-only client is unknown")
	}
}
