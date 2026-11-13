package plugin

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

const testMeta = `export const meta = { apiVersion: 1, key: "demo", name: "Demo", version: "1.0.0", capabilities: [] };
`

// compileChecked compiles text and checks it as load checks an apiVersion 1
// plugin, which testMeta declares.
func compileChecked(file, text string) error {
	_, parsed, err := compile(file, text)
	if err != nil {
		return err
	}
	return checkSynchronous(file, parsed)
}

func TestCompileRejectsWhatSynchronousPluginsCannotUse(t *testing.T) {
	for _, test := range []struct {
		name, file, body, code string
		line                   int
	}{
		{"async function", "plugin.js", "\nexport async function prepareTurn() {}", CodeAsync, 3},
		{"async arrow", "plugin.js", "export const prepareTurn = async () => 1;", CodeAsync, 2},
		{"async method", "plugin.js", "class Recall { async run() {} }", CodeAsync, 2},
		{"async object method", "plugin.ts", "const recall = { async run(): Promise<void> {} };", CodeAsync, 2},
		{"top-level await", "plugin.js", "await 1;", CodeAwait, 2},
		{"for await", "plugin.js", "for await (const item of []) {}", CodeAwait, 2},
		{"import", "plugin.js", `import { helper } from "./helper.js";`, CodeImport, 2},
		{"dynamic import", "plugin.js", `export function prepareTurn() { return import("./later.js"); }`, CodeImport, 2},
		{"export from", "plugin.js", `export { helper } from "./helper.js";`, CodeReexport, 2},
		{"export star", "plugin.js", `export * from "./helper.js";`, CodeReexport, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := compileChecked(test.file, testMeta+test.body)
			var loadErr *LoadError
			if !errors.As(err, &loadErr) {
				t.Fatalf("compile error = %v, want a LoadError", err)
			}
			if loadErr.Code != test.code || loadErr.Line != test.line || loadErr.File != test.file {
				t.Fatalf("LoadError = %+v, want code %s at %s:%d", loadErr, test.code, test.file, test.line)
			}
		})
	}
}

func TestCompileLinksTheModulesOfNode(t *testing.T) {
	source := testMeta + `import fs from "node:fs";
import { join } from "path";
import * as crypto from "node:crypto";
export { promisify } from "node:util";
export function prepareTurn() { return [typeof fs.readFileSync, join("a", "b"), typeof crypto.createHash, import.meta.url]; }
`
	if err := compileChecked("plugin.js", source); err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, test := range []struct{ body, message string }{
		{`import x from "node:nope";`, `"node:nope"`},
		{`import x from "sqlite";`, `"sqlite"`},
		{`import { nope } from "node:path";`, "nope"},
	} {
		err := compileChecked("plugin.js", testMeta+test.body)
		var loadErr *LoadError
		if !errors.As(err, &loadErr) || loadErr.Code != CodeImport || !strings.Contains(loadErr.Message, test.message) {
			t.Errorf("%s: compile error = %v, want an import error naming %s", test.body, err, test.message)
		}
	}
}

func TestCompileTellsCodeFromTextThatMentionsIt(t *testing.T) {
	source := testMeta + `
// async functions, await and import are mentioned here only in a comment.
/* import x from "y"; await z; */
const words = ["async", "await", "import", ` + "`import ${\"x\"} async`" + `];
const pattern = /import|await|async/;
const asyncValue = 1, awaitCount = 2;
const table = { import: 1, await: 2, async: 3 };
export function prepareTurn() { return [words, pattern.test("import"), asyncValue + awaitCount, table.import]; }
`
	if err := compileChecked("plugin.js", source); err != nil {
		t.Fatalf("compile: %v", err)
	}
}

func TestCompileAcceptsTypeScriptWithTypeOnlyImports(t *testing.T) {
	source := `import type { TurnView } from "astrlink";
interface Config { serverUrl: string; quotas?: Record<string, number> }
export const meta = { apiVersion: 1, key: "demo", name: "Demo", version: "1.0.0", capabilities: [] as string[] };
export function prepareTurn(turn: TurnView): { skip: string } {
  const config = astrlink.config.get() as Config;
  return { skip: config.serverUrl ?? "none" };
}
`
	if err := compileChecked("plugin.ts", source); err != nil {
		t.Fatalf("compile: %v", err)
	}
}

func TestCompileReportsSyntaxErrorsWithPosition(t *testing.T) {
	err := compileChecked("plugin.ts", testMeta+"export function prepareTurn() {\n  return {;\n}\n")
	var loadErr *LoadError
	if !errors.As(err, &loadErr) || loadErr.Code != CodeSyntax || loadErr.Line != 3 {
		t.Fatalf("compile error = %#v, want a syntax error on line 3", err)
	}
	if !strings.HasPrefix(loadErr.Error(), "plugin.ts:3:") {
		t.Fatalf("Error() = %q", loadErr.Error())
	}
}

func TestCompileRejectsTypeScriptWithRuntimeSemantics(t *testing.T) {
	_, _, err := compile("plugin.ts", testMeta+"enum Family { Memory }\n")
	var loadErr *LoadError
	if !errors.As(err, &loadErr) || loadErr.Code != CodeSyntax {
		t.Fatalf("compile error = %#v, want a syntax error for enum", err)
	}
}

func TestCompileBoundsTheSource(t *testing.T) {
	large := testMeta + "//" + strings.Repeat("x", MaxSourceBytes)
	var loadErr *LoadError
	if _, _, err := compile("plugin.js", large); !errors.As(err, &loadErr) || loadErr.Code != CodeTooLarge {
		t.Fatalf("compile of %d bytes = %v, want too_large", len(large), err)
	}
	if _, _, err := compile("plugin.js", testMeta+"const text = \"\xff\";"); !errors.As(err, &loadErr) || loadErr.Code != CodeEncoding {
		t.Fatalf("compile of invalid UTF-8 = %v, want encoding", err)
	}
}

func TestDiscoverLetsTheDirectoryOverrideBuiltins(t *testing.T) {
	builtin := fstest.MapFS{
		"alpha/plugin.ts":     {Data: []byte("builtin alpha")},
		"beta/plugin.ts":      {Data: []byte("builtin beta")},
		"Not-A-Key/plugin.ts": {Data: []byte("ignored")},
	}
	dir := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("beta/plugin.js", "directory beta")
	write("gamma/plugin.ts", "directory gamma")
	write("gamma/plugin.js", "second gamma")
	write("delta/README.md", "no source")
	write("notes.txt", "not a plugin")
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "plugin.js"), []byte("linked epsilon"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(dir, "epsilon")); err != nil {
		t.Fatal(err)
	}

	sources, err := Discover(builtin, dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	var got []string
	for _, source := range sources {
		entry := source.Key + " " + string(source.Origin) + " " + source.File + " " + source.Text
		if source.Err != nil {
			entry = source.Key + " " + string(source.Origin) + " error " + source.Err.Code
		}
		got = append(got, entry)
	}
	want := []string{
		"alpha builtin plugin.ts builtin alpha",
		"beta override plugin.js directory beta",
		"delta directory error not_found",
		"epsilon directory plugin.js linked epsilon",
		"gamma directory error ambiguous",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Discover =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestDiscoverReadsTheIconBesideTheSource(t *testing.T) {
	png := "\x89PNG\r\n\x1a\nimage"
	svg := `<?xml version="1.0"?><!-- mark --><svg xmlns="http://www.w3.org/2000/svg"/>`
	builtin := fstest.MapFS{
		"png/plugin.ts":        {Data: []byte("png")},
		"png/icon.png":         {Data: []byte(png)},
		"svg/plugin.ts":        {Data: []byte("svg")},
		"svg/icon.svg":         {Data: []byte("\xef\xbb\xbf" + svg)},
		"svg/icon.png":         {Data: []byte(png)},
		"script/plugin.ts":     {Data: []byte("script")},
		"script/icon.svg":      {Data: []byte(`<svg><script>alert(1)</script></svg>`)},
		"script/icon.png":      {Data: []byte(png)},
		"html/plugin.ts":       {Data: []byte("html")},
		"html/icon.svg":        {Data: []byte(`<html><svg/></html>`)},
		"large/plugin.ts":      {Data: []byte("large")},
		"large/icon.png":       {Data: []byte(png + strings.Repeat("x", MaxIconBytes))},
		"mislabeled/plugin.ts": {Data: []byte("mislabeled")},
		"mislabeled/icon.png":  {Data: []byte("GIF89a")},
		"broken/icon.png":      {Data: []byte(png)},
	}
	sources, err := Discover(builtin, "")
	if err != nil {
		t.Fatal(err)
	}
	icons := map[string]string{}
	for _, source := range sources {
		icons[source.Key] = source.Icon
	}
	pngURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(png))
	want := map[string]string{
		"png":        pngURL,
		"svg":        "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("\xef\xbb\xbf"+svg)),
		"script":     pngURL,
		"html":       "",
		"large":      "",
		"mislabeled": "",
		// A plugin that does not load still shows its icon.
		"broken": pngURL,
	}
	for key, icon := range want {
		if icons[key] != icon {
			t.Errorf("%s icon = %.60q, want %.60q", key, icons[key], icon)
		}
	}
}

func TestDiscoverWithoutAPluginDirectory(t *testing.T) {
	sources, err := Discover(nil, filepath.Join(t.TempDir(), "missing"))
	if err != nil || len(sources) != 0 {
		t.Fatalf("Discover = %v, %v; want no plugins", sources, err)
	}
}
