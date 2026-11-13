package clients

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func codeWhaleFile(home agents.Home) string {
	return filepath.Join(home.Dir(), ".codewhale", "config.toml")
}

func TestCodeWhaleWritesOneTableWithTheDefaultModelsWire(t *testing.T) {
	home := scratchHome(t)
	mkdir(t, home.Dir(), ".codewhale")
	chosen := connection(models("model", "gpt-5.5"))
	chosen.Protocols = map[string]string{"gpt-5.5": "openai.responses"}
	details := cc.Catalog{"gpt-5.5": {ContextWindow: cc.Some(uint64(400_000))}}
	if asked := writePlace(t, home, place(CodeWhale), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	want := `provider = "local-gateway"

[providers.local-gateway]
kind = "openai-compatible"
base_url = "` + origin + `/v1"
api_key = "` + token + `"
model = "gpt-5.5"
wire = "responses"
model_context_windows = { "gpt-5.5" = 400000 }
`
	if text, _ := readFile(t, codeWhaleFile(home)); text != want {
		t.Fatalf("%s", text)
	}
	status := statusOf(t, home, CodeWhale, origin)
	if status.State != cc.Configured || *status.Models.Model != "gpt-5.5" {
		t.Fatalf("%+v", status)
	}

	// A chat model drops the wire and the window it has no size for.
	writeAt(t, home, CodeWhale, connection(models("model", "deepseek-v4")), false)
	written, _ := readFile(t, codeWhaleFile(home))
	if strings.Contains(written, "wire") || strings.Contains(written, "model_context_windows") {
		t.Fatal(written)
	}

	syncAt(t, home, "http://127.0.0.1:9000/")
	if synced, _ := readFile(t, codeWhaleFile(home)); !strings.Contains(synced, `base_url = "http://127.0.0.1:9000/v1"`) {
		t.Fatal(synced)
	}

	removeAt(t, home, CodeWhale)
	if _, exists := readFile(t, codeWhaleFile(home)); exists {
		t.Fatal("config left")
	}
}

const codeWhaleUser = "# my whale\nprovider = \"deepseek\"\nmodel = \"deepseek-v4-pro\"\n\n[providers.deepseek]\napi_key = \"sk-deep\"\n"

func TestCodeWhaleKeepsTheUsersProviderAndPutsItBack(t *testing.T) {
	home := scratchHome(t)
	writeFile(t, codeWhaleFile(home), codeWhaleUser)
	chosen := connection(models("model", "deepseek-v4"))
	asked := writeAt(t, home, CodeWhale, chosen, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "provider")
	writeAt(t, home, CodeWhale, chosen, true)
	written, _ := readFile(t, codeWhaleFile(home))
	if !strings.HasPrefix(written, "# my whale\nprovider = \"local-gateway\"\n") ||
		!strings.Contains(written, "[providers.deepseek]\napi_key = \"sk-deep\"\n") || !strings.Contains(written, "[providers.local-gateway]\n") {
		t.Fatal(written)
	}

	removeAt(t, home, CodeWhale)
	if text, _ := readFile(t, codeWhaleFile(home)); text != codeWhaleUser {
		t.Fatalf("%q", text)
	}
}
