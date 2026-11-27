package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/wsl"
)

// One upstream declaration, an official-only model and an ID-only model exercise
// all three paths through the same writer used by the desktop button.
func pickerFixture() (*cc.Connection, cc.Catalog) {
	return listed(models("model", "alias", "reasoningEffort", "low"), "alias", "official", "unknown"), cc.Catalog{
		"alias":    {CodexCatalog: json.RawMessage(`{"slug":"upstream","display_name":"Original","visibility":"hide","supported_in_api":false,"default_reasoning_level":"high","supported_reasoning_levels":[{"effort":"low","description":"Quick"},{"effort":"high","description":"Thorough"}],"context_window":32000,"future_field":42}`)},
		"official": {Reasoning: []string{"low", "max", "unrecognized"}, ContextWindow: cc.Some(uint64(64000))},
	}
}

func TestCodexCatalogWriteReopenRestore(t *testing.T) {
	for _, test := range []struct {
		name     string
		original *string
		edit     bool
	}{
		{"new file", nil, false}, {"previous file", cc.Some(" existing catalog bytes\r\n"), false}, {"empty file", cc.Some(""), false}, {"user edits", nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := scratchHome(t)
			// A moved CODEX_HOME must also carry the companion to its new location.
			home.Dirs.Codex = filepath.Join(home.Dir(), "codex config")
			if err := os.MkdirAll(home.CodexDir(), 0700); err != nil {
				t.Fatal(err)
			}
			paths := Codex.ConfigPaths(home, "", nil)
			oldPointer := filepath.Join(home.Dir(), "my-models.json")
			oldConfig := fmt.Sprintf("model_catalog_json = %q\n# keep this comment\n", oldPointer)
			writeFile(t, paths[0], oldConfig)
			writeFile(t, oldPointer, "my separate catalog")
			if test.original != nil {
				writeFile(t, paths[1], *test.original)
			}
			c, catalog := pickerFixture()
			preview, previewErr := cc.PreviewPlace(home, place(Codex), origin, &cc.PreviewWrite{Selection: &cc.Selection{Fields: c.Models, List: c.List}, Secret: cc.Secret{ID: c.TokenID, Token: c.Token}, Hint: "test-hint", Details: catalog})
			if previewErr != nil || len(preview.Files) != 2 {
				t.Fatalf("preview: %v", previewErr)
			}
			screen, marshalErr := json.Marshal(preview.Files)
			if marshalErr != nil || strings.Contains(string(screen), c.Token) || !strings.Contains(string(screen), "supported_reasoning_levels") {
				t.Fatal("preview did not show catalog changes with the token masked")
			}

			if ok, ask, err := cc.WriteWith(home, place(Codex), c, catalog, tokens(), false); ok || ask == nil || err != nil {
				t.Fatalf("expected confirmation before writing: %t %+v %v", ok, ask, err)
			}
			if got, _ := readFile(t, paths[0]); got != oldConfig {
				t.Fatal("confirmation changed config")
			}
			if ok, ask, err := cc.WriteWith(home, place(Codex), c, catalog, tokens(), true); !ok || ask != nil || err != nil {
				t.Fatalf("write: %v", err)
			}
			config, _ := readFile(t, paths[0])
			toml, err := ReadCodexConfig(config)
			if err != nil {
				t.Fatal(err)
			}
			if ptr, _, _ := toml.Get("model_catalog_json"); ptr != home.Shown(paths[1]) {
				t.Fatalf("wrong catalog path %q", ptr)
			}
			if effort, _, _ := toml.Get(codexReasoningKey); effort != "low" {
				t.Fatalf("wrong explicit effort %q", effort)
			}
			if got := statusOf(t, home, Codex, origin).Models.Models; !slices.Equal(got, c.List) {
				t.Fatalf("reopened with %v", got)
			}
			raw, _ := readFile(t, paths[1])
			var fields struct {
				Models []struct {
					Slug string
					contract.CodexReasoning
					ContextWindow int `json:"context_window"`
					Future        int `json:"future_field"`
				}
			}
			if err := json.Unmarshal([]byte(raw), &fields); err != nil {
				t.Fatal(err)
			}
			if len(fields.Models) != 3 {
				t.Fatalf("wrong catalog length %d", len(fields.Models))
			}
			a, b, u := fields.Models[0], fields.Models[1], fields.Models[2]
			if a.Slug != "alias" || a.Default == nil || *a.Default != "high" || a.Supported[0].Description != "Quick" || a.Future != 42 || b.Supported[1].Effort != "max" || b.Default != nil || b.ContextWindow != 64000 || u.Supported == nil || len(u.Supported) != 0 || u.Default != nil {
				t.Fatalf("wrong per-model choices: %s", raw)
			}
			if test.edit {
				writeFile(t, paths[1], "changed after setup\n")
			}
			removeAt(t, home, Codex)
			if now, _ := readFile(t, paths[0]); now != oldConfig {
				t.Fatalf("restored config = %q", now)
			}
			now, exists := readFile(t, paths[1])
			switch {
			case test.edit:
				if !exists || now != "changed after setup\n" {
					t.Fatal("overwrote later edit")
				}
			case test.original != nil:
				if !exists || now != *test.original {
					t.Fatalf("original bytes lost %q (%t)", now, exists)
				}
			default:
				if exists {
					t.Fatal("created catalog survived undo")
				}
			}
			if now, _ := readFile(t, oldPointer); now != "my separate catalog" {
				t.Fatal("modified the user's catalog")
			}
		})
	}
}

// Opt in with the path of the Codex binary shipped with the desktop app.
// model/list reads the generated files without sending an inference request.
func TestCodexStartupCatalogInstalledClient(t *testing.T) {
	binary := os.Getenv("ASTRLINK_TEST_CODEX_BIN")
	if binary == "" {
		t.Skip("set ASTRLINK_TEST_CODEX_BIN to test the installed client")
	}
	home := scratchHome(t)
	c, details := pickerFixture()
	// No running gateway or credentials are needed to load a startup catalog.
	c.Origin = "http://127.0.0.1:1"
	if ok, _, err := cc.WriteWith(home, place(Codex), c, details, nil, true); !ok || err != nil {
		t.Fatalf("write: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "app-server", "--listen", "stdio://")
	cmd.Dir = home.Dir()
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home.Dir(), "CODEX_HOME=" + home.CodexDir(), "TMPDIR=" + t.TempDir(), "RUST_LOG=warn"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
		if t.Failed() {
			t.Log(stderr.String())
		}
	}()
	decoder := json.NewDecoder(stdout)
	rpc := func(id int, message string) json.RawMessage {
		t.Helper()
		if _, err := fmt.Fprintln(stdin, message); err != nil {
			t.Fatal(err)
		}
		for {
			var reply struct {
				ID            *int
				Result, Error json.RawMessage
			}
			if err := decoder.Decode(&reply); err != nil {
				t.Fatal(err)
			}
			if reply.ID != nil && *reply.ID == id {
				if len(reply.Error) != 0 {
					t.Fatalf("RPC error: %s", reply.Error)
				}
				return reply.Result
			}
		}
	}
	rpc(1, `{"id":1,"method":"initialize","params":{"clientInfo":{"name":"catalog_test","version":"1.0"}}}`)
	raw := rpc(2, "{\"method\":\"initialized\"}\n"+`{"id":2,"method":"model/list","params":{}}`)
	var result struct {
		Data []struct {
			ID                        string
			DefaultReasoningEffort    string
			SupportedReasoningEfforts []struct{ ReasoningEffort string }
		}
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	choices := map[string][]string{}
	defaults := map[string]string{}
	for _, model := range result.Data {
		choices[model.ID] = []string{}
		for _, level := range model.SupportedReasoningEfforts {
			choices[model.ID] = append(choices[model.ID], level.ReasoningEffort)
		}
		defaults[model.ID] = model.DefaultReasoningEffort
	}
	if len(choices) != 3 || !slices.Equal(choices["alias"], []string{"low", "high"}) || defaults["alias"] != "high" || !slices.Equal(choices["official"], []string{"low", "max"}) || choices["unknown"] == nil || len(choices["unknown"]) != 0 {
		t.Fatalf("unexpected Codex model/list: %v (%v)", choices, defaults)
	}
	t.Logf("Codex model/list: %s", strings.Join(c.List, ", "))
}

func TestCodexCatalogSnippetUsesWSLPath(t *testing.T) {
	guest := agents.InWSL(t.TempDir(), &agents.WSLHome{Home: wsl.Home{Distribution: "Ubuntu", Share: `\\wsl.localhost\Ubuntu\home\ada`, MountRoot: "/mnt"}})
	c, details := pickerFixture()
	files, err := cc.Snippet(Codex, guest, c, details)
	if err != nil || len(files) != 2 {
		t.Fatalf("snippet: %v", err)
	}
	toml, err := ReadCodexConfig(files[0].Contents)
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := toml.Get("model_catalog_json"); got != "/home/ada/.codex/"+CodexCatalogFilename {
		t.Fatalf("WSL pointer %q", got)
	}
}

func TestCodexUpgradesAndRestoresPreCatalogRecords(t *testing.T) {
	c := connection(codexModels())
	plan := planWrite(t, Codex, texts(missing, missing), c, nil)
	// Released versions owned only the gateway and model keys in config.toml.
	document, err := ReadCodexConfig(plan.Contents[0])
	if err != nil {
		t.Fatal(err)
	}
	document.Remove("model_catalog_json")
	before := texts(document.Render(), missing)
	record := plan.Record.Clone()
	delete(record.Keys, "model_catalog_json")
	delete(record.Keys, CodexCatalogFilename)
	record.CreatedCompanions = nil
	if contents, _ := planSync(t, Codex, before, record, origin); contents != nil {
		t.Fatal("background sync unexpectedly installed a catalog")
	}
	sameRemovals(t, planRemove(t, Codex, before, record), deleted, unchanged)
	updated := planWrite(t, Codex, before, c, record)
	if len(updated.Conflicts) != 0 || !slices.Contains(updated.Record.CreatedCompanions, 1) {
		t.Fatal("explicit write failed to upgrade legacy record")
	}
	sameRemovals(t, planRemove(t, Codex, texts(updated.Contents...), updated.Record), deleted, deleted)
}
