package clients

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// json5File is a JSON5 file, as OpenClaw reads it.
type json5File struct{}

func (json5File) Format() format.Format { return format.JSONFormat{Syntax: &format.JSON5} }

// jsonc reads a JSON5 file, comments and trailing commas included, as a
// value.
func jsonc(t *testing.T, text string) any {
	t.Helper()
	value, err := cc.ReadJSON(json5File{}, text)
	if err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return value
}

func TestGeminiEnvLinesChangeInPlaceAndComeBackOnRemoval(t *testing.T) {
	env := "# keys\nexport GEMINI_API_KEY=\"old-key-123456\"\nHTTP_PROXY=http://proxy # mine\nGEMINI_MODEL=gemini-2.5-pro\n"
	settings := "{\n  // mine\n  \"ui\": {\"theme\": \"dark\"},\n  \"security\": {\"auth\": {\"selectedType\": \"oauth-personal\"}}\n}\n"
	plan := planWrite(t, Gemini, texts(env, settings), connection(geminiModels()), nil)
	sameStrings(t, plan.Conflicts, "GEMINI_API_KEY", "GEMINI_MODEL", "security.auth.selectedType")
	if want := "# keys\nexport GEMINI_API_KEY=" + token + "\nHTTP_PROXY=http://proxy # mine\nGEMINI_MODEL=gemini-route\nGOOGLE_GEMINI_BASE_URL=" + origin + "\n"; plan.Contents[0] != want {
		t.Fatalf("%q", plan.Contents[0])
	}
	if !strings.Contains(plan.Contents[1], "// mine") {
		t.Fatal(plan.Contents[1])
	}
	if got, want := jsonc(t, plan.Contents[1]), decode(t, `{"ui": {"theme": "dark"}, "security": {"auth": {"selectedType": "gemini-api-key"}}}`); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	if plan.Record.BaseURL != origin || plan.Record.CreatedFile {
		t.Fatalf("%+v", plan.Record)
	}
	written := texts(plan.Contents[0], plan.Contents[1])
	if state := inspect(Gemini, written, plan.Record, origin); state != cc.Configured {
		t.Fatal(state)
	}
	if model := cc.CurrentModelsOf(Gemini, written).Model; model == nil || *model != "gemini-route" {
		t.Fatal(model)
	}
	// Signing in with Google again is a change to the connection.
	login := strings.ReplaceAll(plan.Contents[1], "gemini-api-key", "oauth-personal")
	if state := inspect(Gemini, texts(plan.Contents[0], login), plan.Record, origin); state != cc.Modified {
		t.Fatal(state)
	}

	removals, restored, err := cc.PlanRestore(Gemini, written, plan.Record)
	if err != nil || restored != 3 || len(removals) != 2 || removals[0].Kind != cc.RemovalWrite || removals[1].Kind != cc.RemovalWrite {
		t.Fatalf("%+v %d %v", removals, restored, err)
	}
	if want := "# keys\nexport GEMINI_API_KEY=old-key-123456\nHTTP_PROXY=http://proxy # mine\nGEMINI_MODEL=gemini-2.5-pro\n"; removals[0].Contents != want {
		t.Fatalf("%q", removals[0].Contents)
	}
	if !reflect.DeepEqual(jsonc(t, removals[1].Contents), jsonc(t, settings)) {
		t.Fatal(removals[1].Contents)
	}
}

func TestANewGeminiConfigIsCreatedAndRemovedWhole(t *testing.T) {
	home := scratchHome(t)
	mkdir(t, home.Dir(), ".gemini")
	writeAt(t, home, Gemini, connection(geminiModels()), false)
	env := filepath.Join(home.Dir(), ".gemini", ".env")
	settings := filepath.Join(home.Dir(), ".gemini", "settings.json")
	if text, _ := readFile(t, env); !strings.Contains(text, token) {
		t.Fatal(text)
	}
	syncAt(t, home, "http://127.0.0.1:9000/")
	if text, _ := readFile(t, env); !strings.Contains(text, "GOOGLE_GEMINI_BASE_URL=http://127.0.0.1:9000\n") {
		t.Fatal(text)
	}
	status := statusOf(t, home, Gemini, "http://127.0.0.1:9000/")
	if status.State != cc.Configured || !reflect.DeepEqual(status.Exists, []bool{true, true}) {
		t.Fatalf("%+v", status)
	}
	removeAt(t, home, Gemini)
	for _, path := range []string{env, settings} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal(path)
		}
	}
}

func TestGeminiTakesNoSlashButTheAutomaticRoute(t *testing.T) {
	if Gemini.CheckModel("a/b") == nil || Gemini.CheckModel(autoModel) != nil || Gemini.CheckModel("gemini-2.5-pro") != nil {
		t.Fatal("check")
	}
}
