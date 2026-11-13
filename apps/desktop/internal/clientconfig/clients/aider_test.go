package clients

import (
	"path/filepath"
	"strings"
	"testing"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func TestAiderWritesTheAddressTwiceAndTheModelForEveryRole(t *testing.T) {
	home := scratchHome(t)
	mkdir(t, home.Dir(), ".aider")
	path := filepath.Join(home.Dir(), ".aider.conf.yml")
	if asked := writeAt(t, home, Aider, connection(models("model", "deepseek-v4")), false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	want := "openai-api-base: " + origin + "/v1\nset-env:\n- OPENAI_BASE_URL=" + origin + "/v1\nopenai-api-key: " + token + "\nmodel: openai/deepseek-v4\nweak-model: openai/deepseek-v4\neditor-model: openai/deepseek-v4\n"
	if text, _ := readFile(t, path); text != want {
		t.Fatalf("%s", text)
	}
	status := statusOf(t, home, Aider, origin)
	if status.State != cc.Configured || *status.Models.Model != "deepseek-v4" {
		t.Fatalf("%+v", status)
	}

	syncAt(t, home, "http://127.0.0.1:9000/")
	synced, _ := readFile(t, path)
	if !strings.Contains(synced, "openai-api-base: http://127.0.0.1:9000/v1\n") || !strings.Contains(synced, "- OPENAI_BASE_URL=http://127.0.0.1:9000/v1\n") {
		t.Fatal(synced)
	}

	removeAt(t, home, Aider)
	if _, exists := readFile(t, path); exists {
		t.Fatal("file left")
	}
}

const aiderUser = "# my aider\nmodel: anthropic/claude-sonnet-4-5\nset-env:\n  - OPENAI_BASE_URL=https://api.openai.com/v1\n  - LITELLM_LOG=ERROR\ndark-mode: true\n"

func TestAiderReplacesTheUsersVariableAndPutsItBack(t *testing.T) {
	home := scratchHome(t)
	path := filepath.Join(home.Dir(), ".aider.conf.yml")
	writeFile(t, path, aiderUser)
	chosen := connection(models("model", "deepseek-v4"))
	asked := writeAt(t, home, Aider, chosen, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "set-env.OPENAI_BASE_URL", "model")
	writeAt(t, home, Aider, chosen, true)
	written, _ := readFile(t, path)
	for _, part := range []string{"  - LITELLM_LOG=ERROR\n", "  - OPENAI_BASE_URL=" + origin + "/v1\n", "dark-mode: true\n"} {
		if !strings.Contains(written, part) {
			t.Fatalf("%s lacks %s", written, part)
		}
	}
	if strings.Contains(written, "api.openai.com") {
		t.Fatal(written)
	}

	removeAt(t, home, Aider)
	restored, _ := readFile(t, path)
	if !strings.Contains(restored, "  - OPENAI_BASE_URL=https://api.openai.com/v1\n") ||
		!strings.HasPrefix(restored, "# my aider\nmodel: anthropic/claude-sonnet-4-5\n") || strings.Contains(restored, "openai-api-key") {
		t.Fatal(restored)
	}
}
