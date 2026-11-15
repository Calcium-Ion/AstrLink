package clients

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func onlyWrite(t *testing.T, removals []cc.Removal) string {
	t.Helper()
	if len(removals) != 1 || removals[0].Kind != cc.RemovalWrite {
		t.Fatalf("expected a rewrite, got %+v", removals)
	}
	return removals[0].Contents
}

func TestCodexConfigKeepsCommentsAndOtherTables(t *testing.T) {
	existing := "# user settings\nmodel = \"gpt-5\" # mine\napproval_policy = \"never\"\n\n[profiles.fast]\nmodel = \"o3\"\n\n[model_providers.other]\nname = \"Other\"\n"
	plan := planWrite(t, Codex, texts(existing), connection(codexModels()), nil)
	sameStrings(t, plan.Conflicts, "model")
	want := "# user settings\nmodel = \"gpt-route\" # mine\napproval_policy = \"never\"\nmodel_provider = \"astrlink\"\n\n[profiles.fast]\nmodel = \"o3\"\n\n[model_providers.other]\nname = \"Other\"\n\n[model_providers.astrlink]\nname = \"AstrLink\"\nbase_url = \"http://127.0.0.1:18317/v1\"\nwire_api = \"responses\"\nexperimental_bearer_token = \"astr_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA0\"\nhttp_headers = { x-openai-actor-authorization = \"codex-imagegen\" }\n"
	if plan.Contents[0] != want {
		t.Fatalf("%q", plan.Contents[0])
	}
	if plan.Record.BaseURL != "http://127.0.0.1:18317/v1" {
		t.Fatal(plan.Record.BaseURL)
	}
	// The model AstrLink replaced comes back.
	if removed := onlyWrite(t, planRemove(t, Codex, texts(plan.Contents[0]), plan.Record)); removed != existing {
		t.Fatalf("%q", removed)
	}
}

func TestCodexConfigIsCreatedWithoutAuthSettings(t *testing.T) {
	plan := planWrite(t, Codex, texts(missing), connection(codexModels()), nil)
	want := "model_provider = \"astrlink\"\nmodel = \"gpt-route\"\n\n[model_providers.astrlink]\nname = \"AstrLink\"\nbase_url = \"http://127.0.0.1:18317/v1\"\nwire_api = \"responses\"\nexperimental_bearer_token = \"astr_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA0\"\nhttp_headers = { x-openai-actor-authorization = \"codex-imagegen\" }\n"
	if plan.Contents[0] != want {
		t.Fatalf("%q", plan.Contents[0])
	}
	// The reserved provider ID or the name "OpenAI" would make Codex treat
	// this as its built-in provider.
	for _, forbidden := range []string{"env_key", "requires_openai_auth", `"OpenAI"`, `"openai"`, "model_providers.openai"} {
		if strings.Contains(plan.Contents[0], forbidden) {
			t.Fatal(forbidden)
		}
	}
	sameRemovals(t, planRemove(t, Codex, texts(plan.Contents[0]), plan.Record), deleted)
}

func TestCodexToolsHeaderSitsBesideTheUsersOwnHeaders(t *testing.T) {
	existing := "[model_providers.astrlink.http_headers]\nx-team = \"blue\"\n"
	plan := planWrite(t, Codex, texts(existing), connection(codexModels()), nil)
	sameStrings(t, plan.Conflicts)
	for _, want := range []string{`x-team = "blue"`, `x-openai-actor-authorization = "codex-imagegen"`} {
		if !strings.Contains(plan.Contents[0], want) {
			t.Fatalf("%q lacks %s", plan.Contents[0], want)
		}
	}
	removed := onlyWrite(t, planRemove(t, Codex, texts(plan.Contents[0]), plan.Record))
	if !strings.Contains(removed, `x-team = "blue"`) {
		t.Fatal(removed)
	}
	for _, gone := range []string{"x-openai-actor-authorization", "experimental_bearer_token", "model_provider ="} {
		if strings.Contains(removed, gone) {
			t.Fatalf("%q holds %s", removed, gone)
		}
	}

	// A value the user gave the header is theirs until they confirm.
	existing = "[model_providers.astrlink]\nhttp_headers = { X-OpenAI-Actor-Authorization = \"mine\", x-team = \"blue\" }\n"
	plan = planWrite(t, Codex, texts(existing), connection(codexModels()), nil)
	sameStrings(t, plan.Conflicts, codexHeadersKey)
	if !strings.Contains(plan.Contents[0], `http_headers = { X-OpenAI-Actor-Authorization = "codex-imagegen", x-team = "blue" }`) {
		t.Fatal(plan.Contents[0])
	}
}

func TestSyncAddsTheCodexToolsHeaderToReleasedConfigsOnce(t *testing.T) {
	plan := planWrite(t, Codex, texts(missing), connection(codexModels()), nil)
	// Released builds wrote no header and kept no fingerprint for it.
	released := strings.Replace(plan.Contents[0], "http_headers = { x-openai-actor-authorization = \"codex-imagegen\" }\n", "", 1)
	record := plan.Record.Clone()
	delete(record.Keys, codexHeadersKey)
	if state := inspect(Codex, texts(released), record, origin); state != cc.Configured {
		t.Fatal(state)
	}
	contents, next := planSync(t, Codex, texts(released), record, origin)
	if contents == nil || contents[0] != plan.Contents[0] || !reflect.DeepEqual(next.Keys, plan.Record.Keys) {
		t.Fatalf("%q %v", contents, next)
	}
	if again, _ := planSync(t, Codex, texts(contents[0]), next, origin); again != nil {
		t.Fatal("synced twice")
	}

	// A header the user removed after AstrLink wrote it stays removed.
	if again, _ := planSync(t, Codex, texts(released), plan.Record, origin); again != nil {
		t.Fatal("added a removed header")
	}
	// A changed connection is left alone.
	modified := strings.Replace(released, `wire_api = "responses"`, `wire_api = "chat"`, 1)
	if again, _ := planSync(t, Codex, texts(modified), record, origin); again != nil {
		t.Fatal("synced a modified config")
	}
	// A new port and the header arrive in one write.
	contents, next = planSync(t, Codex, texts(released), record, "http://127.0.0.1:9000")
	if contents == nil || !strings.Contains(contents[0], `base_url = "http://127.0.0.1:9000/v1"`) ||
		!strings.Contains(contents[0], `x-openai-actor-authorization = "codex-imagegen"`) || next.BaseURL != "http://127.0.0.1:9000/v1" {
		t.Fatalf("%q", contents)
	}
}

func TestCodexConflictsCoverAForeignProviderTable(t *testing.T) {
	existing := "model_provider = \"other\"\n\n[model_providers.astrlink]\nbase_url = \"https://secret.example/v1\"\nenv_key = \"SECRET_KEY\"\n"
	plan := planWrite(t, Codex, texts(existing), connection(codexModels()), nil)
	sameStrings(t, plan.Conflicts, "model_provider", "model_providers.astrlink.base_url", "model_providers.astrlink.env_key")
	if strings.Contains(plan.Contents[0], "env_key") || strings.Contains(plan.Contents[0], "secret.example") {
		t.Fatal(plan.Contents[0])
	}
	plan = planWrite(t, Codex, texts("model_providers = { astrlink = \"broken\" }\n"), connection(codexModels()), nil)
	sameStrings(t, plan.Conflicts, "model_providers.astrlink")
	if !strings.Contains(plan.Contents[0], "experimental_bearer_token") {
		t.Fatal(plan.Contents[0])
	}
}

func TestCodexCRLFFilesStayCRLF(t *testing.T) {
	existing := "# a\r\napproval_policy = \"never\"\r\n"
	plan := planWrite(t, Codex, texts(existing), connection(codexModels()), nil)
	if !strings.HasPrefix(plan.Contents[0], existing) || strings.Contains(strings.ReplaceAll(plan.Contents[0], "\r\n", ""), "\n") {
		t.Fatalf("%q", plan.Contents[0])
	}
	if removed := onlyWrite(t, planRemove(t, Codex, texts(plan.Contents[0]), plan.Record)); removed != existing {
		t.Fatalf("%q", removed)
	}
}

func TestAModelTheClientChangedDoesNotBlockSync(t *testing.T) {
	plan := planWrite(t, Codex, texts(missing), connection(codexModels()), nil)
	edited := strings.ReplaceAll(plan.Contents[0], "gpt-route", "picked-in-tui")
	if state := inspect(Codex, texts(edited), plan.Record, origin); state != cc.Configured {
		t.Fatal(state)
	}
	synced, record := planSync(t, Codex, texts(edited), plan.Record, "http://127.0.0.1:9000")
	if len(synced) != 1 || synced[0] != strings.ReplaceAll(edited, "127.0.0.1:18317", "127.0.0.1:9000") {
		t.Fatalf("%q", synced)
	}
	if record.BaseURL != "http://127.0.0.1:9000/v1" || record.TokenSHA256 != plan.Record.TokenSHA256 {
		t.Fatalf("%+v", record)
	}
	if state := inspect(Codex, texts(synced[0]), record, "http://127.0.0.1:9000"); state != cc.Configured {
		t.Fatal(state)
	}
	// The model the user picked is theirs now and survives removal.
	if removed := onlyWrite(t, planRemove(t, Codex, texts(synced[0]), record)); removed != "model = \"picked-in-tui\"\n" {
		t.Fatalf("%q", removed)
	}
}

func TestCodexApplyNeverTouchesAuthJSON(t *testing.T) {
	home := scratchHome(t)
	auth := filepath.Join(home.Dir(), ".codex", "auth.json")
	login := "{\"auth_mode\": \"chatgpt\", \"tokens\": {}}\n"
	writeFile(t, auth, login)
	writeAt(t, home, Codex, connection(codexModels()), false)
	syncAt(t, home, "http://127.0.0.1:9000/")
	config := filepath.Join(home.Dir(), ".codex", "config.toml")
	if text, _ := readFile(t, config); !strings.Contains(text, `base_url = "http://127.0.0.1:9000/v1"`) {
		t.Fatal(text)
	}
	removeAt(t, home, Codex)
	if _, exists := readFile(t, config); exists {
		t.Fatal("config left")
	}
	if text, _ := readFile(t, auth); text != login {
		t.Fatal(text)
	}
	entries, _ := os.ReadDir(filepath.Join(home.Dir(), ".codex"))
	if len(entries) != 1 || entries[0].Name() != "auth.json" {
		t.Fatalf("%v", entries)
	}
}

func TestAProviderAnotherToolSwitchesToIsAChangeWritingTakesBack(t *testing.T) {
	home := scratchHome(t)
	config := filepath.Join(home.Dir(), ".codex", "config.toml")
	writeAt(t, home, Codex, connection(codexModels()), false)
	written, _ := readFile(t, config)
	// A tool like CC Switch takes over the route and model keys and adds its
	// own provider table; AstrLink's table stays behind.
	switched := strings.NewReplacer(`model_provider = "astrlink"`, `model_provider = "custom"`, "model = \"gpt-route\"\n", "model = \"gpt-route\"\nmodel_reasoning_effort = \"high\"\n").Replace(written) +
		"\n[model_providers.custom]\nname = \"AstrLink · VS Code\"\nbase_url = \"" + origin + "/v1\"\nwire_api = \"responses\"\nexperimental_bearer_token = \"" + token + "\"\n"
	writeFile(t, config, switched)
	if state := statusOf(t, home, Codex, "http://127.0.0.1:18317/").State; state != cc.Modified {
		t.Fatal(state)
	}
	syncAt(t, home, "http://127.0.0.1:9000/")
	if text, _ := readFile(t, config); text != switched {
		t.Fatal(text)
	}

	// Writing takes it back, asking only about the route the tool chose.
	confirmation := writeAt(t, home, Codex, connection(codexModels()), false)
	if confirmation == nil || confirmation.Gateway != nil {
		t.Fatalf("%+v", confirmation)
	}
	sameStrings(t, askedKeys(confirmation), "model_provider")
	writeAt(t, home, Codex, connection(codexModels()), true)
	rewritten, _ := readFile(t, config)
	for _, want := range []string{`model_provider = "astrlink"`, `model_reasoning_effort = "high"`, "[model_providers.custom]"} {
		if !strings.Contains(rewritten, want) {
			t.Fatalf("%q lacks %s", rewritten, want)
		}
	}
	if state := statusOf(t, home, Codex, "http://127.0.0.1:18317/").State; state != cc.Configured {
		t.Fatal(state)
	}
	// Removal gives the tool its route back.
	removeAt(t, home, Codex)
	if text, _ := readFile(t, config); !strings.Contains(text, `model_provider = "custom"`) {
		t.Fatal(text)
	}
}

func TestCommentsAboveRemovedCodexKeysStay(t *testing.T) {
	for _, existing := range []string{
		"# header\n",
		"# header\nmodel = \"old\"\n",
		"approval_policy = \"never\"\n# pick one\nmodel = \"old\"\n\n[tui]\nx = 1\n",
	} {
		plan := planWrite(t, Codex, texts(existing), connection(codexModels()), nil)
		if removed := onlyWrite(t, planRemove(t, Codex, texts(plan.Contents[0]), plan.Record)); removed != existing {
			t.Fatalf("%q", removed)
		}
	}
}

func TestCodexEffortAndClaudeSubagentsAreWrittenAndGivenBack(t *testing.T) {
	existing := "model_reasoning_effort = \"high\"\n"
	plan := planWrite(t, Codex, texts(existing), connection(models("model", "gpt-route", "reasoningEffort", "low")), nil)
	sameStrings(t, plan.Conflicts, codexReasoningKey)
	if !strings.Contains(plan.Contents[0], `model_reasoning_effort = "low"`) {
		t.Fatal(plan.Contents[0])
	}
	if effort := cc.CurrentModelsOf(Codex, texts(plan.Contents[0])).ReasoningEffort; effort == nil || *effort != "low" {
		t.Fatal(effort)
	}
	// Clearing the effort gives back the one it replaced.
	cleared := planWrite(t, Codex, texts(plan.Contents[0]), connection(codexModels()), plan.Record)
	sameStrings(t, cleared.Conflicts)
	if !strings.HasPrefix(cleared.Contents[0], existing) || cleared.Record.Keys[codexReasoningKey] != "" || len(cleared.Record.Backup) != 0 {
		t.Fatalf("%q %+v", cleared.Contents[0], cleared.Record)
	}

	plan = planWrite(t, Claude, texts(missing), connection(models("subagentModel", "sub-route")), nil)
	if env := decode(t, plan.Contents[0]).(map[string]any)["env"].(map[string]any); env["CLAUDE_CODE_SUBAGENT_MODEL"] != "sub-route" {
		t.Fatal(env)
	}
	if current := cc.CurrentModelsOf(Claude, texts(plan.Contents[0])); !reflect.DeepEqual(current, cc.CurrentModels{SubagentModel: some("sub-route")}) {
		t.Fatalf("%+v", current)
	}
}
