package pluginbuild

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

type settings map[string]plugin.Settings

func (all settings) PluginSettings(context.Context) (map[string]plugin.Settings, error) {
	return all, nil
}

func TestBuildBundlesAProjectThatLoadsInTheHost(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"package.json": `{
  "name": "astrlink-plugin-demo",
  "version": "1.2.0",
  "engines": { "astrlink": ">=0.1.0" },
  "astrlink": { "plugin": "src/plugin.ts" },
  "dependencies": { "left-pad": "1.3.0" }
}`,
		"src/plugin.ts": `import leftPad from "left-pad";
import { greet, Mood } from "./greet";

export const meta = { apiVersion: 2, key: "demo", name: "Demo", version: "1.2.0", capabilities: [] };

export async function prepareTurn(input: { name: string }) {
  await null;
  return { text: greet(input.name, Mood.Happy), padded: leftPad("7", 3, "0") };
}

export function describeStatus() {
  return explode();
}

function explode(): never {
  throw new Error("status is broken");
}
`,
		"src/greet.ts": `export enum Mood { Happy = "happy", Sad = "sad" }
export function greet(name: string, mood: Mood): string {
  return ` + "`hello ${name}, you look ${mood}`" + `;
}
`,
		"node_modules/left-pad/package.json": `{ "name": "left-pad", "version": "1.3.0", "main": "index.js" }`,
		"node_modules/left-pad/index.js": `module.exports = function leftPad(text, length, char) {
  text = String(text);
  while (text.length < length) text = (char || " ") + text;
  return text;
};
`,
	})
	result, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if result.Entries.Plugin != "src/plugin.ts" || result.Client != nil || result.Manifest.EngineRange() != ">=0.1.0" {
		t.Fatalf("result = %+v", result)
	}
	if strings.Join(result.Inputs, ",") != "src/greet.ts,src/plugin.ts" {
		t.Fatalf("inputs = %v", result.Inputs)
	}
	manager := plugin.NewManager(plugin.ManagerOptions{
		Builtin: fstest.MapFS{
			"demo/plugin.js":     &fstest.MapFile{Data: result.Plugin},
			"demo/plugin.js.map": &fstest.MapFile{Data: result.SourceMap},
		},
		Settings: settings{"demo": {Enabled: true}},
	})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	loaded := manager.Current().Plugin("demo")
	if loaded == nil || !loaded.Loaded() {
		t.Fatalf("plugin = %+v", loaded.Err)
	}
	output, _, err := loaded.Call(context.Background(), plugin.HookPrepareTurn, map[string]string{"name": "Ada"}, plugin.CallOptions{})
	if err != nil || string(output) != `{"text":"hello Ada, you look happy","padded":"007"}` {
		t.Fatalf("prepareTurn = %s, %v", output, err)
	}
	_, _, err = loaded.Call(context.Background(), plugin.HookDescribeStatus, nil, plugin.CallOptions{})
	var failure *plugin.CallError
	if !errors.As(err, &failure) || !strings.Contains(failure.Message, "status is broken") {
		t.Fatalf("describeStatus = %v", err)
	}
	entries, _ := loaded.Journal(0)
	if len(entries) != 1 || !strings.Contains(entries[0].Stack, "src/plugin.ts:16:") {
		t.Fatalf("journal = %+v", entries)
	}
}

func TestBuildLeavesTheModulesOfNodeToTheHost(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"package.json": `{ "name": "demo", "version": "1.0.0", "dependencies": { "spawner": "1.0.0" } }`,
		"plugin.ts": `import { createHash } from "crypto";
import path from "node:path";
import spawner from "spawner";
export const meta = { apiVersion: 2, key: "demo", name: "Demo", version: "1.0.0", capabilities: [] };
export async function prepareTurn() {
  return { digest: createHash("sha1").update("abc").digest("hex"), joined: path.join("a", "b"), spawner: spawner() };
}
`,
		"node_modules/spawner/package.json": `{ "name": "spawner", "version": "1.0.0", "main": "index.js" }`,
		"node_modules/spawner/index.js": `const { EventEmitter } = require("events");
module.exports = function spawner() {
  if (typeof EventEmitter !== "function") return "no events";
  try { require("child_process").spawn("ls"); return "spawned"; } catch (error) { return error.code; }
};
`,
	})
	result, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	code := string(result.Plugin)
	if !strings.Contains(code, `from "node:crypto"`) || !strings.Contains(code, `from "node:path"`) || !strings.Contains(code, `"node:child_process"`) || strings.Contains(code, "function EventEmitter") {
		t.Fatalf("plugin.js leaves Node's modules out:\n%s", code)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0].Text, "node:child_process is not available to plugins") || !strings.HasPrefix(result.Warnings[0].File, "node_modules/spawner/") {
		t.Fatalf("warnings = %+v", result.Warnings)
	}
	manager := plugin.NewManager(plugin.ManagerOptions{
		Builtin:  fstest.MapFS{"demo/plugin.js": &fstest.MapFile{Data: result.Plugin}},
		Settings: settings{"demo": {Enabled: true}},
	})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	loaded := manager.Current().Plugin("demo")
	if loaded == nil || !loaded.Loaded() {
		t.Fatalf("plugin = %+v", loaded.Err)
	}
	output, _, err := loaded.Call(context.Background(), plugin.HookPrepareTurn, nil, plugin.CallOptions{})
	if err != nil || string(output) != `{"digest":"a9993e364706816aba3e25717850c26c9cd0d89d","joined":"a/b","spawner":"ERR_ACCESS_DENIED"}` {
		t.Fatalf("prepareTurn = %s, %v", output, err)
	}
}

func TestBuildExplainsWhatPluginsCannotUse(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"plugin.ts": `import { spawn } from "child_process";
export const meta = { apiVersion: 2, key: "demo", name: "Demo", version: "1.0.0", capabilities: [] };
export function prepareTurn() { return spawn("ls"); }
`,
	})
	_, err := Build(dir)
	var failure *Error
	if !errors.As(err, &failure) || len(failure.Messages) != 1 {
		t.Fatalf("Build = %v", err)
	}
	message := failure.Messages[0]
	if message.File != "plugin.ts" || message.Line != 1 || !strings.Contains(message.Text, "node:child_process is not available to plugins: plugins cannot start processes") {
		t.Fatalf("message = %+v", message)
	}
}

func TestBuildFindsEntriesAndBuildsAnInterface(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"plugin.js": `export const meta = { apiVersion: 2, key: "demo", name: "Demo", version: "1.0.0", capabilities: [] };`,
		"src/client.tsx": `import { registerSlot } from "@astrlink/plugin/client";
import { useState } from "react";
function Counter() {
  const [count, setCount] = useState(0);
  return <button onClick={() => setCount(count + 1)}>{count}</button>;
}
registerSlot("plugin.tab", { title: "Counter", render: Counter });
`,
	})
	result, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if result.Entries.Plugin != "plugin.js" || result.Entries.Client != "src/client.tsx" {
		t.Fatalf("entries = %+v", result.Entries)
	}
	client := string(result.Client)
	for _, want := range []string{`__astrlinkClient.module("react")`, `__astrlinkClient.module("react/jsx-runtime")`, `__astrlinkClient.module("@astrlink/plugin/client")`} {
		if !strings.Contains(client, want) {
			t.Fatalf("client.js lacks %s:\n%s", want, client)
		}
	}
	if !strings.Contains(client, "import_react.useState") || len(result.ClientMap) == 0 {
		t.Fatalf("client.js = %s", client)
	}
}

func TestResolveEntriesRefusesWhatIsOutsideTheProject(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"package.json": `{"astrlink":{"plugin":"../elsewhere.ts"}}`})
	manifest, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveEntries(dir, manifest); err == nil || !strings.Contains(err.Error(), "outside the project") {
		t.Fatalf("ResolveEntries = %v", err)
	}
	empty := t.TempDir()
	if _, err := ResolveEntries(empty, nil); err == nil || !strings.Contains(err.Error(), "no plugin entry") {
		t.Fatalf("ResolveEntries = %v", err)
	}
}

func TestSatisfies(t *testing.T) {
	for _, test := range []struct {
		version, rangeText string
		want               bool
	}{
		{"1.2.3", "", true},
		{"1.2.3", "*", true},
		{"1.2.3", ">=1.2.0", true},
		{"1.1.9", ">=1.2.0", false},
		{"1.9.0", "^1.2", true},
		{"2.0.0", "^1.2", false},
		{"0.2.5", "^0.2.3", true},
		{"0.3.0", "^0.2.3", false},
		{"0.0.4", "^0.0.3", false},
		{"1.2.9", "~1.2.3", true},
		{"1.3.0", "~1.2.3", false},
		{"1.4.0", "1.x", true},
		{"2.0.0", "1.x", false},
		{"3.1.0", "^1.0.0 || >=3", true},
		{"1.5.0", ">=1.2.0 <1.5.0", false},
		{"1.5.0", "1.2 - 1.5", true},
		{"1.6.0", "1.2 - 1.5", false},
		{"1.0.0-beta.2", ">=1.0.0-beta.1", true},
		{"1.0.0-beta.2", ">=0.9.0", false},
		{"0.1.0-dev.1", ">=0.1.0-dev.0", true},
	} {
		version, ok := ParseVersion(test.version)
		if !ok {
			t.Fatalf("ParseVersion(%q) failed", test.version)
		}
		got, err := Satisfies(version, test.rangeText)
		if err != nil || got != test.want {
			t.Errorf("Satisfies(%s, %q) = %v, %v; want %v", test.version, test.rangeText, got, err, test.want)
		}
	}
	if _, err := Satisfies(Version{Major: 1}, ">=one"); err == nil {
		t.Fatal("Satisfies accepted a range that is not one")
	}
}
