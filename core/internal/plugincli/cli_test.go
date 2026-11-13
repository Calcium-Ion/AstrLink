package plugincli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/pluginbuild"
)

func run(t *testing.T, env Environment, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env.Stdout, env.Stderr = &stdout, &stderr
	code := Run(context.Background(), args, env)
	return code, stdout.String(), stderr.String()
}

func TestNewTestCheckAndBuildAProject(t *testing.T) {
	parent := t.TempDir()
	if code, _, stderr := run(t, Environment{}, "new", "turn-counter", "--dir", parent); code != 0 {
		t.Fatalf("new = %d: %s", code, stderr)
	}
	dir := filepath.Join(parent, "turn-counter")
	if code, stdout, stderr := run(t, Environment{}, "test", dir); code != 0 || !strings.Contains(stdout, `"passed": 2`) {
		t.Fatalf("test = %d: %s %s", code, stdout, stderr)
	}
	code, stdout, stderr := run(t, Environment{}, "check", dir)
	if code != 0 {
		t.Fatalf("check = %d: %s", code, stderr)
	}
	var report checkReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil || report.Key != "turn-counter" || report.APIVersion != 2 || strings.Join(report.Hooks, ",") != "describeStatus,observeTurn" {
		t.Fatalf("check report = %s (%v)", stdout, err)
	}
	if code, _, stderr := run(t, Environment{}, "build", dir); code != 0 {
		t.Fatalf("build = %d: %s", code, stderr)
	}
	for _, name := range []string{"plugin.js", "plugin.js.map"} {
		if _, err := os.Stat(filepath.Join(dir, "dist", name)); err != nil {
			t.Fatalf("dist/%s: %v", name, err)
		}
	}
	// A second project in the same place is refused.
	if code, _, stderr := run(t, Environment{}, "new", "turn-counter", "--dir", parent); code != 1 || !strings.Contains(stderr, "not empty") {
		t.Fatalf("new again = %d: %s", code, stderr)
	}
	if code, _, stderr := run(t, Environment{}, "new", "Bad_Key"); code != 2 || !strings.Contains(stderr, "cannot be a plugin key") {
		t.Fatalf("new with a bad key = %d: %s", code, stderr)
	}
}

func TestATaskPluginRunsNewAPIFixturesAsItIs(t *testing.T) {
	dir := t.TempDir()
	source, err := os.ReadFile(filepath.Join("..", "plugin", "testdata", "newapi", "sora", "plugin.js"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.js"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	golden := `[
		{"name": "queued", "hook": "parseTaskResult", "args": [{}, {"status": "queued"}], "expected": {"status": "QUEUED"}},
		{"hook": "parseTaskResult", "args": [{}, {"status": "in_progress", "progress": 40}], "expected": {"status": "IN_PROGRESS", "progress": "40%"}},
		{"name": "needs a prompt", "hook": "buildSubmitRequest", "args": [{"requestBody": {}, "apiKey": "k", "baseUrl": "https://api.openai.com"}], "expectedError": "field prompt is required"},
		{"name": "decodes Responses", "hook": "protocols", "member": "openai_responses.decodeRequest", "args": [{"body": {"kind": "json", "value": {"model": "sora-2", "prompt": "a cat"}}}],
		 "expected": {"kind": "submit", "model": "sora-2", "action": "text_to_video", "requestBody": {"model": "sora-2", "prompt": "a cat"}}}
	]`
	if err := os.WriteFile(filepath.Join(dir, "golden.json"), []byte(golden), 0o644); err != nil {
		t.Fatal(err)
	}
	// As new-api runs them: the plugin's file and the fixture's.
	code, stdout, stderr := run(t, Environment{}, "test", filepath.Join(dir, "plugin.js"), "--fixture", filepath.Join(dir, "golden.json"))
	if code != 0 || !strings.Contains(stdout, `"passed": 4`) {
		t.Fatalf("test = %d: %s %s", code, stdout, stderr)
	}
	// A wrong expectation says what the hook returned.
	failing := `{"name": "no video yet", "hook": "listArtifacts", "args": [{"status": "SUCCESS"}], "expected": []}`
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests", "artifacts.json"), []byte(failing), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = run(t, Environment{}, "test", dir)
	if code != 1 || !strings.Contains(stderr, `returned [{"key":"video","type":"video"}]`) {
		t.Fatalf("failing test = %d: %s", code, stderr)
	}
	code, stdout, stderr = run(t, Environment{}, "check", dir)
	var report checkReport
	if code != 0 || json.Unmarshal([]byte(stdout), &report) != nil || report.Key != "sora" || report.APIVersion != 1 ||
		!slices.Contains(report.Hooks, "protocols.openai_responses.decodeRequest") || report.PluginBytes != len(source) {
		t.Fatalf("check = %d: %s %s", code, stdout, stderr)
	}
}

func TestANewProjectInstallsOnTheDevelopmentBuilds(t *testing.T) {
	var manifest pluginbuild.Manifest
	if err := json.Unmarshal([]byte(projectFiles("demo", templates()["basic"])["package.json"]), &manifest); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"0.1.0-dev.1", "0.1.0", "0.2.3"} {
		current, _ := pluginbuild.ParseVersion(version)
		if compatible, err := pluginbuild.Satisfies(current, manifest.EngineRange()); err != nil || !compatible {
			t.Errorf("AstrLink %s is outside engines.astrlink %q (%v)", version, manifest.EngineRange(), err)
		}
	}
}

func TestTestReportsWhatFailed(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"plugin.ts": `export const meta = { apiVersion: 2, key: "fetcher", name: "Fetcher", version: "1.0.0", capabilities: ["network"], network: { purpose: "test" } };
export async function describeStatus() {
  const response = await fetch("https://api.example/status");
  const body = await response.json();
  console.info("status", body.state);
  return [{ type: "text", text: body.state }];
}
export async function testConnection() {
  await fetch("https://api.example/other");
  return { status: "ok", summary: "fine" };
}
`,
		"tests/cases.json": `[
  { "name": "reads the status", "hook": "describeStatus",
    "fetch": [{ "url": "https://api.example/status", "json": { "state": "green" } }],
    "expect": { "result": [{ "type": "text", "text": "green" }] } },
  { "name": "wrong expectation", "hook": "describeStatus",
    "fetch": [{ "url": "https://api.example/*", "json": { "state": "red" } }],
    "expect": { "result": [{ "type": "text", "text": "green" }] } },
  { "name": "unexpected request", "hook": "testConnection",
    "fetch": [{ "url": "https://api.example/status", "json": {} }],
    "expect": { "match": { "status": "ok" } } }
]`,
	}
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, stdout, stderr := run(t, Environment{}, "test", dir)
	if code != 1 || !strings.Contains(stdout, `"passed": 1`) || !strings.Contains(stdout, `"failed": 2`) {
		t.Fatalf("test = %d: %s %s", code, stdout, stderr)
	}
	for _, want := range []string{"PASS  reads the status", "FAIL  wrong expectation", `returned [{"type":"text","text":"red"}]`, "info: status red", "made a request no fixture answers: GET https://api.example/other"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if code, _, stderr := run(t, Environment{}, "test", dir, "--run", "reads"); code != 0 {
		t.Fatalf("test --run = %d: %s", code, stderr)
	}
}

func TestCasesShareASetupAndScriptTheServer(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"plugin.ts": `export const meta = {
  apiVersion: 2, key: "scripted", name: "Scripted", version: "1.0.0",
  capabilities: ["network", "storage", "request.read"], network: { purpose: "test" },
  metadata: [{ key: "project", label: "Project", pattern: "^[a-z]+$" }],
};
export async function describeStatus() {
  const first = await astrlink.http.fetch({ method: "POST", url: "https://api.example/items", json: { name: "a", version: 0 } });
  const second = first.status === 409
    ? await astrlink.http.fetch({ method: "POST", url: "https://api.example/items", json: { name: "a", version: 1 } })
    : first;
  const projects = await astrlink.meta.values("project");
  await astrlink.store.delete("stale");
  const now = astrlink.clock.now();
  const local = new Date(now + astrlink.clock.offset(now) * 60000).toISOString().slice(0, 16);
  return [{ type: "text", text: [first.status, second.status, second.took_ms, projects.map((p) => p.value).join(","), local].join(" ") }];
}
`,
		"tests/server.json": `{
  "now": "2026-10-21T03:04:00Z",
  "timeZone": "America/New_York",
  "metaValues": { "project": [{ "value": "paseo", "sessions": 3, "lastUsedAt": 1 }, { "value": "notes", "sessions": 1, "lastUsedAt": 0 }] },
  "store": { "stale": true },
  "fetch": [{ "method": "POST", "url": "https://api.example/items", "body": { "version": 1 }, "status": 201, "delayMs": 3, "json": {} }],
  "cases": [
    { "name": "a conflict, then the shared answer", "hook": "describeStatus",
      "fetch": [{ "method": "POST", "url": "https://api.example/items", "body": { "version": 0 }, "times": 1, "status": 409, "json": {} }],
      "expect": { "result": [{ "type": "text", "text": "409 201 3 paseo,notes 2026-10-20T23:04" }], "store": { "stale": null } } },
    { "name": "its own clock and values", "hook": "describeStatus", "timeZone": "Asia/Shanghai", "metaValues": {},
      "fetch": [{ "method": "POST", "url": "https://api.example/items", "times": 1, "status": 409, "json": {} }],
      "expect": { "match": [{ "text": "409 201 3  2026-10-21T11:04" }] } },
    { "name": "fewer requests than the times", "hook": "describeStatus",
      "fetch": [{ "method": "POST", "url": "https://api.example/items", "body": { "version": 0 }, "times": 2, "status": 409, "json": {} }],
      "expect": { "match": [{ "type": "text" }] } },
    { "name": "a body no response takes", "hook": "describeStatus",
      "expect": { "match": [{ "type": "text" }] } }
  ]
}`,
	}
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, stdout, stderr := run(t, Environment{}, "test", dir)
	if code != 1 || !strings.Contains(stdout, `"passed": 2`) || !strings.Contains(stdout, `"failed": 2`) {
		t.Fatalf("test = %d: %s %s", code, stdout, stderr)
	}
	for _, want := range []string{
		"PASS  a conflict, then the shared answer",
		"PASS  its own clock and values",
		"FAIL  fewer requests than the times\n      POST https://api.example/items answered 1 of the 2 requests it expects",
		`FAIL  a body no response takes` + "\n" + `      made a request no fixture answers: POST https://api.example/items {"name":"a","version":0}`,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}

type fakeGateway struct {
	directory string
	paths     []string
}

func (gateway *fakeGateway) Get(_ context.Context, path string, query url.Values) (json.RawMessage, error) {
	gateway.paths = append(gateway.paths, path+"?"+query.Encode())
	if path == "/control/v1/plugins" {
		return json.Marshal(map[string]any{"directory": gateway.directory, "plugins": []any{}})
	}
	return json.RawMessage(`{"entries":[],"timings":[],"latest":7}`), nil
}

func TestDevInstallsADevelopmentBuild(t *testing.T) {
	parent := t.TempDir()
	if code, _, stderr := run(t, Environment{}, "new", "notes", "--dir", parent); code != 0 {
		t.Fatalf("new = %d: %s", code, stderr)
	}
	project := filepath.Join(parent, "notes")
	gateway := &fakeGateway{directory: t.TempDir()}
	env := Environment{Dial: func() (Gateway, error) { return gateway, nil }}
	if code, _, stderr := run(t, env, "dev", project, "--once"); code != 0 || !strings.Contains(stderr, "notes built and installed") {
		t.Fatalf("dev = %d: %s", code, stderr)
	}
	installed := filepath.Join(gateway.directory, "notes")
	for _, name := range []string{"plugin.js", "plugin.js.map", plugin.DevelopmentMarker} {
		if _, err := os.Stat(filepath.Join(installed, name)); err != nil {
			t.Fatalf("installed %s: %v", name, err)
		}
	}
	marker, _ := os.ReadFile(filepath.Join(installed, plugin.DevelopmentMarker))
	if !strings.Contains(string(marker), filepath.ToSlash(project)) && !strings.Contains(string(marker), project) {
		t.Fatalf("marker = %s", marker)
	}
	// A plugin installed otherwise is not replaced without --force.
	if err := os.Remove(filepath.Join(installed, plugin.DevelopmentMarker)); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := run(t, env, "dev", project, "--once"); code != 1 || !strings.Contains(stderr, "--force") {
		t.Fatalf("dev over an installed plugin = %d: %s", code, stderr)
	}
	if code, _, stderr := run(t, env, "dev", project, "--once", "--force"); code != 0 {
		t.Fatalf("dev --force = %d: %s", code, stderr)
	}
}

func TestEveryTemplateBuildsAndPassesItsTests(t *testing.T) {
	for _, name := range templateNames() {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			key := "demo-" + name
			if code, _, stderr := run(t, Environment{}, "new", key, "--template", name, "--dir", parent); code != 0 {
				t.Fatalf("new = %d: %s", code, stderr)
			}
			dir := filepath.Join(parent, key)
			if code, stdout, stderr := run(t, Environment{}, "test", dir); code != 0 {
				t.Fatalf("test = %d: %s %s", code, stdout, stderr)
			}
			if code, _, stderr := run(t, Environment{}, "check", dir); code != 0 {
				t.Fatalf("check = %d: %s", code, stderr)
			}
		})
	}
}
