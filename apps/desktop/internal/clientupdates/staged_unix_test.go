//go:build !windows

package clientupdates

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// guard turns on the Windows update guard for f's home until the test
// ends.
func guard(t *testing.T, f *fixture) {
	t.Helper()
	guardedHomes.Lock()
	guardedHomes.homes = append(guardedHomes.homes, f.root)
	guardedHomes.Unlock()
	t.Cleanup(func() { unguard(f) })
}

func unguard(f *fixture) {
	guardedHomes.Lock()
	defer guardedHomes.Unlock()
	guardedHomes.homes = slices.DeleteFunc(guardedHomes.homes, func(home string) bool { return home == f.root })
}

// npmFixture is an npm-installed Claude Code in <home>/node and a
// stand-in for npm that works like npm's reify: it renames the live
// package and shim aside first, then "downloads" (which its mode can make
// fail, stop, or skip the platform binary), writes the new files and
// finally deletes what it retired.
type npmFixture struct {
	*fixture
	prefix string
}

func newNpmFixture(t *testing.T, mode string) *npmFixture {
	t.Helper()
	f := newFixture(t)
	guard(t, f)
	root, ok := canonical(f.root)
	if !ok {
		t.Fatal("the fixture has no canonical path")
	}
	n := &npmFixture{f, filepath.Join(root, "node")}
	f.script("node/bin/node", "#!/bin/sh\nexec /bin/sh \"$@\"\n")
	n.installLive("2.1.321")
	// Arguments: npm-cli.js install --global --prefix <target> ... <pkg>@<v>
	f.write("node/lib/node_modules/npm/bin/npm-cli.js", "#!/bin/sh\nset -eu\nmode='"+mode+"'\nshift 3\ntarget=$1\n"+
		"for spec; do :; done\nversion=${spec##*@}\n"+
		"pkg=$target/lib/node_modules/@anthropic-ai/claude-code\n"+
		"shim=$target/bin/claude\n"+
		"[ -e \"$pkg\" ] && mv \"$pkg\" \"$target/lib/node_modules/@anthropic-ai/.claude-code-AbCd1234\"\n"+
		"{ [ -e \"$shim\" ] || [ -L \"$shim\" ]; } && mv \"$shim\" \"$target/bin/.claude-EfGh5678\"\n"+
		"echo \"$target\" >> npm-targets\n"+
		"case $mode in\n"+
		"  killed) kill -9 $$ ;;\n"+
		"  fail) echo 'npm error code ECONNRESET' >&2; exit 1 ;;\n"+
		"esac\n"+
		"mkdir -p \"$pkg/bin\" \"$target/bin\"\n"+
		"printf '{\"name\":\"@anthropic-ai/claude-code\",\"version\":\"%s\",\"bin\":{\"claude\":\"bin/claude.exe\"}}' \"$version\" > \"$pkg/package.json\"\n"+
		"if [ \"$mode\" = stub ]; then\n"+
		"  printf '#!/bin/sh\\necho \"Error: claude native binary not installed.\" >&2\\nexit 1\\n' > \"$pkg/bin/claude.exe\"\n"+
		"else\n"+
		"  printf '#!/bin/sh\\necho \"%s (Claude Code)\"\\n' \"$version\" > \"$pkg/bin/claude.exe\"\n"+
		"fi\n"+
		"chmod +x \"$pkg/bin/claude.exe\"\n"+
		"ln -s ../lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe \"$shim\"\n"+
		"rm -rf \"$target/lib/node_modules/@anthropic-ai/.claude-code-AbCd1234\" \"$target/bin/.claude-EfGh5678\"\n")
	return n
}

// setMode makes the stand-in npm behave as mode from now on.
func (n *npmFixture) setMode(from, to string) {
	n.t.Helper()
	path := filepath.Join(n.prefix, "lib/node_modules/npm/bin/npm-cli.js")
	data, err := os.ReadFile(path)
	if err != nil {
		n.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "mode='"+from+"'", "mode='"+to+"'", 1)), 0o644); err != nil {
		n.t.Fatal(err)
	}
}

func (n *npmFixture) installLive(version string) {
	n.t.Helper()
	pkg := "node/lib/node_modules/@anthropic-ai/claude-code"
	n.write(pkg+"/package.json", `{"name":"@anthropic-ai/claude-code","version":"`+version+`","bin":{"claude":"bin/claude.exe"}}`)
	binary := n.script(pkg+"/bin/claude.exe", "#!/bin/sh\necho '"+version+" (Claude Code)'\n")
	shim := filepath.Join(n.prefix, "bin", "claude")
	_ = os.Remove(shim)
	if err := os.Symlink(binary, shim); err != nil {
		n.t.Fatal(err)
	}
}

func (n *npmFixture) env() *Environment {
	return &Environment{home: n.root, path: n.npmPath(filepath.Join(n.prefix, "bin"))}
}

// npmPath is bins, then links to the system tools the stand-in npm uses,
// so that no real client is on it.
func (n *npmFixture) npmPath(bins string) string {
	n.t.Helper()
	path := n.searchPath(bins)
	tools := n.path("tools")
	for _, name := range []string{"mv", "chmod", "ln", "rm"} {
		if exists(filepath.Join(tools, name)) {
			continue
		}
		found, err := exec.LookPath(name)
		if err != nil {
			n.t.Fatal(err)
		}
		if err := os.Symlink(found, filepath.Join(tools, name)); err != nil {
			n.t.Fatal(err)
		}
	}
	return path
}

func (n *npmFixture) installed() string {
	env := n.env()
	install := detect(env, ClientClaude)
	if install == nil {
		return ""
	}
	v, failure := currentVersion(env, ClientClaude, install)
	if failure != nil {
		return ""
	}
	return v.String()
}

func (n *npmFixture) update() (clientVersion, *clientError) {
	n.t.Helper()
	env := n.env()
	install := detect(env, ClientClaude)
	if install == nil {
		n.t.Fatal("the stand-in Claude Code was not found")
	}
	return performUpdate(env, ClientClaude, install, version(n.t, ClientClaude, "2.1.323"))
}

// updateInPlace is what the old updater did: npm straight into the live
// prefix.
func (n *npmFixture) updateInPlace() {
	n.t.Helper()
	env := n.env()
	install := detect(env, ClientClaude)
	if install == nil {
		n.t.Fatal("the stand-in Claude Code was not found")
	}
	spec, failure := updateCommand(env, ClientClaude, install, version(n.t, ClientClaude, "2.1.323"))
	if failure != nil {
		n.t.Fatal(failure)
	}
	_, _ = run(env, spec, 10*time.Second)
}

func (n *npmFixture) leftovers() []string {
	n.t.Helper()
	found := []string{}
	for _, directory := range []string{"bin", "lib/node_modules/@anthropic-ai"} {
		entries, err := os.ReadDir(filepath.Join(n.prefix, directory))
		if err != nil {
			n.t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") {
				found = append(found, directory+"/"+entry.Name())
			}
		}
	}
	if exists(filepath.Join(n.prefix, stagedWork)) {
		found = append(found, stagedWork)
	}
	return found
}

func (n *npmFixture) targets() []string {
	return strings.Fields(n.read("npm-targets"))
}

func TestAStoppedOrFailedNpmUpdateKeepsTheInstalledClient(t *testing.T) {
	for _, tc := range []struct{ mode, code string }{
		// npm stopped midway: the update deadline or quitting AstrLink.
		{"killed", "command"},
		{"fail", "network"},
		// The platform binary did not download; npm finishes anyway.
		{"stub", "incomplete"},
	} {
		n := newNpmFixture(t, tc.mode)
		_, failure := n.update()
		if failure == nil || failure.code != tc.code {
			t.Fatalf("%s: %+v", tc.mode, failure)
		}
		if got := n.installed(); got != "2.1.321" {
			t.Fatalf("%s: installed %q", tc.mode, got)
		}
		if got := n.leftovers(); len(got) != 0 {
			t.Fatalf("%s: %q", tc.mode, got)
		}
		// npm never ran in the live prefix.
		if slices.Contains(n.targets(), n.prefix) {
			t.Fatalf("%s: %q", tc.mode, n.targets())
		}
	}
}

func TestAVerifiedNpmUpdateReplacesTheInstalledClient(t *testing.T) {
	n := newNpmFixture(t, "ok")
	updated, failure := n.update()
	if failure != nil || !sameVersion(updated, version(t, ClientClaude, "2.1.323")) {
		t.Fatal(updated, failure)
	}
	if got := n.installed(); got != "2.1.323" {
		t.Fatal(got)
	}
	if got := n.leftovers(); len(got) != 0 {
		t.Fatal(got)
	}
	install := detect(n.env(), ClientClaude)
	if install.executable != filepath.Join(n.prefix, "bin", "claude") || install.method != npmAt(n.prefix) {
		t.Fatalf("%+v", install)
	}
}

// offline is a client for version checks that fails at once instead of
// reaching the release services.
func offline() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, os.ErrDeadlineExceeded
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReportsAndRepairsAClientNpmLeftBroken(t *testing.T) {
	// The old updater ran npm in the live prefix: stopped midway it leaves
	// only npm's retired copy; with a missing download, only a stub.
	for _, tc := range []struct{ mode, repair, previous string }{
		{"killed", "restore", "2.1.321"},
		{"stub", "reinstall", ""},
	} {
		n := newNpmFixture(t, tc.mode)
		n.updateInPlace()
		if got := n.installed(); got != "" {
			t.Fatalf("%s: %q still starts", tc.mode, got)
		}
		env := n.env()
		status, _, _ := inspect(context.Background(), env, offline(), ClientClaude)
		if status.Phase != "damaged" || value(status.Repair) != tc.repair || status.InstallMethod != "npm" {
			t.Fatalf("%s: %+v", tc.mode, status)
		}
		if previous := value(status.PreviousVersion); tc.previous != "" && previous != tc.previous || tc.previous == "" && status.PreviousVersion != nil {
			t.Fatalf("%s: previous %s", tc.mode, previous)
		}

		// The repair itself must not fail the same way.
		n.setMode(tc.mode, "ok")
		latest := version(t, ClientClaude, "2.1.323")
		repaired, failure := performRepair(env, ClientClaude, &latest)
		expected := tc.previous
		if expected == "" {
			expected = "2.1.323"
		}
		if failure != nil || repaired.String() != expected {
			t.Fatalf("%s: %v %+v", tc.mode, repaired, failure)
		}
		if got := n.installed(); got != expected {
			t.Fatalf("%s: installed %q", tc.mode, got)
		}
		if got := n.leftovers(); len(got) != 0 {
			t.Fatalf("%s: %q", tc.mode, got)
		}
	}
}

func TestUndoesAnInterruptedSwapBeforeTheNextCheck(t *testing.T) {
	n := newNpmFixture(t, "ok")
	// AstrLink quit after moving the live client aside, before moving the
	// new one in.
	runDir := filepath.Join(n.prefix, stagedWork, "claude", "1")
	saved := filepath.Join(runDir, "previous")
	if err := os.MkdirAll(saved, 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(n.prefix, "lib/node_modules/@anthropic-ai/claude-code")
	shim := filepath.Join(n.prefix, "bin", "claude")
	if err := os.Rename(pkg, filepath.Join(saved, "package-0")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(shim, filepath.Join(saved, "claude")); err != nil {
		t.Fatal(err)
	}
	j := journal{
		Run:     runDir,
		Retired: []move{{pkg, filepath.Join(saved, "package-0")}, {shim, filepath.Join(saved, "claude")}},
		Placed:  []move{{filepath.Join(runDir, "stage/pkg"), pkg}, {filepath.Join(runDir, "stage/claude"), shim}},
	}
	if _, failure := beginSwap(filepath.Join(n.prefix, stagedWork, "claude"), j); failure != nil {
		t.Fatal(failure)
	}
	if got := n.installed(); got != "" {
		t.Fatal(got)
	}
	status, _, _ := inspect(context.Background(), n.env(), offline(), ClientClaude)
	if value(status.CurrentVersion) != "2.1.321" || n.installed() != "2.1.321" {
		t.Fatalf("%+v", status)
	}
	// The run AstrLink left behind goes too.
	if got := n.leftovers(); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestLeavesOtherSystemsOnThePreviousUpdatePath(t *testing.T) {
	n := newNpmFixture(t, "ok")
	unguard(n.fixture)
	if _, failure := n.update(); failure != nil {
		t.Fatal(failure)
	}
	// npm ran in the live prefix, as before.
	if got := n.targets(); !slices.Equal(got, []string{n.prefix}) {
		t.Fatal(got)
	}
}

func TestASlowFirstStartIsNotDamage(t *testing.T) {
	n := newNpmFixture(t, "ok")
	env := n.env()
	found := func(failure *clientError) *damage { return findDamageIn(env, ClientClaude, n.prefix, failure) }
	// Antivirus can hold a fresh program past the version probe's
	// deadline; an old Node.js fails a script client the same before and
	// after a reinstall.
	if d := found(newError("timeout", "Command timed out")); d != nil {
		t.Fatal(d.detail)
	}
	if d := found(newError("command", "SyntaxError: Unexpected token '?'\n    at wrapSafe (node:internal/modules/cjs/loader:1001:15)")); d != nil {
		t.Fatal(d.detail)
	}
	d := found(newError("command", "Error: claude native binary not installed."))
	if d == nil || !strings.Contains(d.detail, "not installed") {
		t.Fatal(d)
	}

	// Uninstalled, with only the copy npm once could not delete because
	// the client was running: not an installation.
	scope := filepath.Join(n.prefix, "lib/node_modules/@anthropic-ai")
	if err := os.Rename(filepath.Join(scope, "claude-code"), filepath.Join(scope, ".claude-code-AbCd1234")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(n.prefix, "bin", "claude")); err != nil {
		t.Fatal(err)
	}
	if d := findDamage(env, ClientClaude); d != nil {
		t.Fatal(d.detail)
	}
}

// nvm-windows puts a link to the active Node.js folder on PATH; detection
// resolves it, and so must a repair.
func TestRepairsAClientFoundThroughALinkedFolderOnPath(t *testing.T) {
	for _, tc := range []struct{ mode, expected string }{{"killed", "2.1.321"}, {"stub", "2.1.323"}} {
		n := newNpmFixture(t, tc.mode)
		n.updateInPlace()
		n.setMode(tc.mode, "ok")
		link := n.path("current")
		if err := os.Symlink(n.prefix, link); err != nil {
			t.Fatal(err)
		}
		env := n.env()
		env.path = n.npmPath(filepath.Join(link, "bin"))
		latest := version(t, ClientClaude, "2.1.323")
		repaired, failure := performRepair(env, ClientClaude, &latest)
		if failure != nil || repaired.String() != tc.expected {
			t.Fatalf("%s: %v %+v", tc.mode, repaired, failure)
		}
		if got := n.installed(); got != tc.expected {
			t.Fatalf("%s: installed %q", tc.mode, got)
		}
	}
}

// An undo that could not move a new entry out tries again later and never
// takes the restored old one for it.
func TestAnUndoThatStoppedHalfwayCanRunAgain(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	s := newSwapped(t).apply()
	stage := filepath.Join(s.runDir, "stage")
	if err := os.Chmod(stage, 0o555); err != nil {
		t.Fatal(err)
	}
	failure := recoverWork(s.work)
	if err := os.Chmod(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	if failure == nil || failure.code != "damaged" || !exists(filepath.Join(s.work, stagedJournal)) {
		t.Fatal(failure)
	}
	if failure := recoverWork(s.work); failure != nil {
		t.Fatal(failure)
	}
	if got := s.live(); !slices.Equal(got, oldFiles) || exists(filepath.Join(s.work, stagedJournal)) {
		t.Fatal(got)
	}
}

func TestStagedPiGetsItsOwnUpdaterOptionsAndTheInstallationNpmrc(t *testing.T) {
	f := newFixture(t)
	f.script("node/bin/node", "#!/bin/sh\nexit 0\n")
	f.write("node/lib/node_modules/npm/bin/npm-cli.js", "")
	prefix, stage := f.path("node"), f.path("stage")
	spec, failure := npmCommand(f.environment(), ClientPi, prefix, stage, version(t, ClientPi, "1.5.2"))
	if failure != nil {
		t.Fatal(failure)
	}
	if want := []string{
		"--prefix", stage, "--globalconfig", filepath.Join(prefix, "etc/npmrc"), "--cache", f.path(".cache/astrlink/npm"),
		"--ignore-scripts", "--min-release-age=0", "@earendil-works/pi-coding-agent@1.5.2",
	}; !slices.Equal(spec.args[3:], want) {
		t.Fatalf("args = %q", spec.args)
	}
}

const claudeLatest = "https://registry.npmjs.org/@anthropic-ai/claude-code/latest"

func TestTheManagerKeepsReportsAndRepairsAnNpmClient(t *testing.T) {
	service := &fakeService{bodies: map[string]string{claudeLatest: `{"version":"2.1.323"}`}}
	// A failed update keeps the version from before, and says so.
	n := newNpmFixture(t, "fail")
	m, events := testManager(t, n.fixture, n.env(), service)
	if _, err := m.Update([]ClientTarget{{ID: ClientClaude}}); err != nil {
		t.Fatal(err)
	}
	claude := statusOf(t, finished(events), ClientClaude, nil)
	if claude.Phase != "error" || value(claude.ErrorCode) != "network" || !claude.KeptPrevious || value(claude.CurrentVersion) != "2.1.321" {
		t.Fatalf("%+v", claude)
	}

	// A client the old updater left broken is reported with its repair,
	// which installs the latest again.
	n = newNpmFixture(t, "stub")
	n.updateInPlace()
	n.setMode("stub", "ok")
	m, events = testManager(t, n.fixture, n.env(), service)
	if _, err := m.Refresh(); err != nil {
		t.Fatal(err)
	}
	claude = statusOf(t, finished(events), ClientClaude, nil)
	if claude.Phase != "damaged" || value(claude.Repair) != "reinstall" || claude.CanUpdate {
		t.Fatalf("%+v", claude)
	}
	initial, err := m.Repair([]ClientTarget{{ID: ClientClaude}})
	if err != nil || statusOf(t, initial, ClientClaude, nil).Phase != "repairing" {
		t.Fatal(initial, err)
	}
	claude = statusOf(t, finished(events), ClientClaude, nil)
	if claude.Phase != "repaired" || value(claude.CurrentVersion) != "2.1.323" || claude.ErrorDetail != nil || n.installed() != "2.1.323" {
		t.Fatalf("%+v", claude)
	}
	// A repair leaves clients that are not damaged alone.
	if _, err := m.Repair([]ClientTarget{{ID: ClientClaude}}); err != nil {
		t.Fatal(err)
	}
	if claude = statusOf(t, finished(events), ClientClaude, nil); claude.Phase != "up_to_date" {
		t.Fatalf("%+v", claude)
	}
}
