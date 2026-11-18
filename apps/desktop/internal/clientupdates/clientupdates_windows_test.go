package clientupdates

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestReadsTheSavedWindowsPath(t *testing.T) {
	path, ok := savedPath()
	if !ok {
		t.Fatal("no saved Path")
	}
	// The system Path holds System32, usually as %SystemRoot%\system32.
	for _, directory := range filepath.SplitList(path) {
		if isFile(filepath.Join(directory, "cmd.exe")) {
			return
		}
	}
	t.Fatal(path)
}

func TestFollowsNpmShimsOnWindowsWithoutVerbatimPaths(t *testing.T) {
	f := newFixture(t)
	root, ok := canonical(f.root)
	if !ok || strings.HasPrefix(root, `\\?\`) {
		t.Fatal(root)
	}
	npm := filepath.Join(root, "npm")
	nodejs := filepath.Join(root, "nodejs")
	for _, entry := range []struct{ name, file string }{
		{"@earendil-works/pi-coding-agent", "dist/bundle/cli.js"},
		{"@anthropic-ai/claude-code", "bin/claude.exe"},
	} {
		f.write("npm/node_modules/"+entry.name+"/package.json", `{"name":"`+entry.name+`"}`)
		f.write("npm/node_modules/"+entry.name+"/"+entry.file, "")
	}
	f.write("npm/pi.cmd", `"%_prog%"  "%dp0%\node_modules\@earendil-works\pi-coding-agent\dist\bundle\cli.js" %*`)
	// npm's sh shim for Git Bash is the same installation.
	f.write("npm/pi", "#!/bin/sh\n")
	f.write("npm/claude.cmd", `"%dp0%\node_modules\@anthropic-ai\claude-code\bin\claude.exe"   %*`)
	f.write("npm/node.exe", "")
	f.write("nodejs/npm.cmd", "")
	f.write("nodejs/node_modules/npm/bin/npm-cli.js", "")
	env := &Environment{home: root, path: npm + ";" + nodejs}

	pi := detect(env, ClientPi)
	if pi == nil || pi.method != npmAt(npm) || len(pi.others) != 0 {
		t.Fatalf("pi = %+v", pi)
	}
	spec, failure := clientCommand(env, pi)
	if failure != nil || spec.program != filepath.Join(npm, "node.exe") ||
		len(spec.args) != 1 || spec.args[0] != filepath.Join(npm, `node_modules\@earendil-works\pi-coding-agent\dist\bundle\cli.js`) {
		t.Fatal(spec, failure)
	}

	claude := detect(env, ClientClaude)
	if claude == nil || claude.method != npmAt(npm) {
		t.Fatalf("claude = %+v", claude)
	}
	binary := filepath.Join(npm, `node_modules\@anthropic-ai\claude-code\bin\claude.exe`)
	if spec, failure := clientCommand(env, claude); failure != nil || spec.program != binary || len(spec.args) != 0 {
		t.Fatal(spec, failure)
	}
	// Node's own npm.cmd runs the npm-cli.js beside it.
	spec, failure = updateCommand(env, ClientClaude, claude, version(t, ClientClaude, "2.1.286"))
	if failure != nil || spec.args[0] != filepath.Join(nodejs, `node_modules\npm\bin\npm-cli.js`) || spec.args[4] != npm {
		t.Fatal(spec, failure)
	}
}

func TestFollowsThePiInstallerLauncherOnWindows(t *testing.T) {
	f := newFixture(t)
	root, ok := canonical(f.root)
	if !ok {
		t.Fatal(root)
	}
	bin := filepath.Join(root, `.pi\agent\bin`)
	nodejs := filepath.Join(root, "nodejs")
	for _, file := range []struct{ name, contents string }{
		{"pi.cmd", piInstallerCmd},
		{"pi.ps1", ""},
		{"pi", "#!/bin/sh\n"},
		{"pi-launcher.js", ""},
	} {
		f.write(".pi/agent/bin/"+file.name, file.contents)
	}
	f.write(".pi/agent/install/managed-install.json", "\uFEFF"+`{"kind":"pi-managed-install","schemaVersion":1,"layout":"releases-v1"}`)
	f.write("nodejs/node.exe", "")
	env := &Environment{home: root, path: bin + ";" + nodejs}

	pi := detect(env, ClientPi)
	if pi == nil || pi.method != native || pi.executable != filepath.Join(bin, "pi.cmd") ||
		pi.resolved != filepath.Join(bin, "pi-launcher.js") || len(pi.others) != 0 {
		t.Fatalf("pi = %+v", pi)
	}
	spec, failure := updateCommand(env, ClientPi, pi, version(t, ClientPi, "99.0.0"))
	if failure != nil {
		t.Fatal(failure)
	}
	node := filepath.Join(nodejs, "node.exe")
	if found, ok := shimNode(env, pi.executable, os.LookupEnv); ok {
		node = found.path
	}
	if spec.program != node || !slices.Equal(spec.args, []string{filepath.Join(bin, "pi-launcher.js"), "update", "--self"}) {
		t.Fatal(spec)
	}
}

func TestNeverStartsAFileThatIsNotAWindowsProgram(t *testing.T) {
	f := newFixture(t)
	env := &Environment{home: f.root, path: os.Getenv("PATH")}
	stub := f.write("claude.exe", "exit 1\n")
	// CreateProcess itself would fail with "command" after showing the
	// dialog.
	if _, failure := run(env, command(stub, "--version"), probeTimeout); failure == nil || failure.code != "damaged" {
		t.Fatal(failure)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if image, err := windowsImage(self); err != nil || !image {
		t.Fatal(image, err)
	}
	output, failure := run(env, command(os.Getenv("ComSpec"), "/d", "/c", "echo ok"), probeTimeout)
	if failure != nil || strings.TrimSpace(output) != "ok" {
		t.Fatal(output, failure)
	}
}

// The standalone Codex installer puts Programs\OpenAI\Codex\bin on PATH,
// a junction to .codex\packages\standalone\current\bin, where current is
// a junction to the release. filepath.EvalSymlinks fails on that path.
func TestDetectsStandaloneCodexBehindTwoJunctions(t *testing.T) {
	f := newFixture(t)
	root, ok := canonical(f.root)
	if !ok {
		t.Fatal(root)
	}
	release := `.codex/packages/standalone/releases/0.169.0-x86_64-pc-windows-msvc`
	f.write(release+"/codex-package.json", `{"variant":"codex","layoutVersion":1,"entrypoint":"bin/codex.exe"}`)
	f.write(release+"/bin/codex.exe", "")
	f.mkdir("Programs/OpenAI/Codex")
	current := filepath.Join(root, `.codex\packages\standalone\current`)
	bin := filepath.Join(root, `Programs\OpenAI\Codex\bin`)
	for _, link := range [][2]string{
		{current, filepath.Join(root, filepath.FromSlash(release))},
		{bin, filepath.Join(current, "bin")},
	} {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link[0], link[1]).CombinedOutput(); err != nil {
			t.Fatalf("mklink /J: %v: %s", err, out)
		}
	}
	env := &Environment{home: root, path: bin}

	codex := detect(env, ClientCodex)
	want := filepath.Join(root, filepath.FromSlash(release), `bin\codex.exe`)
	if codex == nil || codex.method != native || codex.executable != filepath.Join(bin, "codex.exe") ||
		!strings.EqualFold(codex.resolved, want) {
		t.Fatalf("codex = %+v, want %s", codex, want)
	}
}
