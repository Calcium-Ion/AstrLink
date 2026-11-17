package clients

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func traePath(home agents.Home) string { return filepath.Join(home.Dir(), ".trae", "traecli.toml") }

func TestTraeCLIWritesAModelEntryPerModelAndPicksOne(t *testing.T) {
	home := scratchHome(t)
	mkdir(t, home.Dir(), ".trae", "cli")
	chosen := listed(models("model", "claude-sonnet-5"), "deepseek-v4", "gpt-5.5", "claude-sonnet-5")
	chosen.Protocols = map[string]string{"gpt-5.5": "openai.responses", "claude-sonnet-5": "anthropic.messages"}
	details := cc.Catalog{"claude-sonnet-5": {ContextWindow: cc.Some(uint64(200_000)), MaxTokens: cc.Some(uint64(64_000))}}
	if asked := writePlace(t, home, place(TraeCLI), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	want := "model = \"claude-sonnet-5\"\nmodel_provider = \"custom\"\n\n[[models]]\nname = \"deepseek-v4\"\n\n[models.open_ai]\nbase_url = \"" + origin + "/v1\"\napi_key = \"" + token + "\"\n\n[[models]]\nname = \"gpt-5.5\"\n\n[models.byted_gpt]\nbase_url = \"" + origin + "/v1\"\napi_key = \"" + token + "\"\n\n[[models]]\nname = \"claude-sonnet-5\"\ncontext_window = 200000\noutput_tokens_hard_limit = 64000\n\n[models.claude]\nbase_url = \"" + origin + "/v1\"\napi_key = \"" + token + "\"\nmax_tokens = 64000\n"
	if text, _ := readFile(t, traePath(home)); text != want {
		t.Fatalf("%s", text)
	}
	status := statusOf(t, home, TraeCLI, origin)
	if status.State != cc.Configured || *status.Models.Model != "claude-sonnet-5" {
		t.Fatalf("%+v", status)
	}

	syncAt(t, home, "http://127.0.0.1:9000/")
	if synced, _ := readFile(t, traePath(home)); strings.Count(synced, `base_url = "http://127.0.0.1:9000/v1"`) != 3 {
		t.Fatal(synced)
	}

	removeAt(t, home, TraeCLI)
	if _, exists := readFile(t, traePath(home)); exists {
		t.Fatal("file left")
	}
}

const traeUser = "# my trae\nmodel = \"doubao-seed\"\nmodel_provider = \"trae\"\n\n[[models]]\nname = \"my-model\"\n\n[models.open_ai]\nbase_url = \"https://api.example.com/v1\"\napi_key = \"${MY_API_KEY}\"\n"

func TestTraeCLIAddsItsModelsAfterTheUsersAndPutsTheirChoiceBack(t *testing.T) {
	home := scratchHome(t)
	writeFile(t, traePath(home), traeUser)
	chosen := connection(models("model", "deepseek-v4"))
	asked := writePlace(t, home, place(TraeCLI), chosen, cc.Catalog{}, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "model", "model_provider")
	writePlace(t, home, place(TraeCLI), chosen, cc.Catalog{}, true)
	written, _ := readFile(t, traePath(home))
	mine, ours := strings.Index(written, `name = "my-model"`), strings.Index(written, `name = "deepseek-v4"`)
	if !strings.HasPrefix(written, "# my trae\nmodel = \"deepseek-v4\"\nmodel_provider = \"custom\"\n") || mine < 0 || ours < mine {
		t.Fatal(written)
	}

	removeAt(t, home, TraeCLI)
	if text, _ := readFile(t, traePath(home)); text != traeUser {
		t.Fatalf("%q", text)
	}
}
