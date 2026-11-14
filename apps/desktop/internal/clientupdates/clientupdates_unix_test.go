//go:build !windows

package clientupdates

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/wsl"
)

// The stand-in clients below are shell scripts in the test's temporary
// directory; no test runs or updates a client of this computer.

func (f *fixture) script(rel, contents string) string {
	f.t.Helper()
	path := f.write(rel, contents)
	if err := os.Chmod(path, 0o755); err != nil {
		f.t.Fatal(err)
	}
	return path
}

func (f *fixture) symlink(target, rel string) {
	f.t.Helper()
	link := f.path(rel)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) read(rel string) string {
	f.t.Helper()
	data, err := os.ReadFile(f.path(rel))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}

func TestIgnoresUnrelatedPiCommands(t *testing.T) {
	f := newFixture(t)
	f.script("bin/pi", "#!/bin/sh\necho 3.14\n")
	if install := detect(f.environment(), ClientPi); install != nil {
		t.Fatal(install)
	}
	// Standalone builds ship their package.json beside the binary.
	f.write("bin/package.json", `{"name":"@earendil-works/pi-coding-agent"}`)
	install := detect(f.environment(), ClientPi)
	if install == nil || install.method != unknown {
		t.Fatal(install)
	}
}

func TestRunsCmdShimTargetsWithoutCmdExe(t *testing.T) {
	f := newFixture(t)
	node := f.script("bin/node", "#!/bin/sh\n")
	shim := f.path("npm/codex.cmd")
	target := f.path("npm/node_modules/@openai/codex/bin/codex.js")
	spec, failure := shimCommand(f.environment(), shim, target)
	if failure != nil || spec.program != node || !slices.Equal(spec.args, []string{target}) {
		t.Fatal(spec, failure)
	}
	binary := f.path("npm/node_modules/@anthropic-ai/claude-code/bin/claude.exe")
	if spec, failure := shimCommand(f.environment(), shim, binary); failure != nil || spec.program != binary || len(spec.args) != 0 {
		t.Fatal(spec, failure)
	}
	// A shim whose target could not be read is never passed to cmd.exe.
	if _, failure := shimCommand(f.environment(), shim, shim); failure == nil || failure.code != "unsupported" {
		t.Fatal(failure)
	}
}

func TestRunsTheNodeAShimNames(t *testing.T) {
	f := newFixture(t)
	env := f.environment()
	shim := f.write(".pi/agent/bin/pi.cmd", piInstallerCmd)
	local := f.path("local")
	variable := func(name string) (string, bool) { return local, name == "LOCALAPPDATA" }
	// Without its standalone Node.js, Pi's launcher runs `node` from PATH.
	if found, ok := shimNode(env, shim, variable); ok {
		t.Fatal(found)
	}
	node := f.script("local/pi-node/current/node.exe", "")
	if found, ok := shimNode(env, shim, variable); !ok || found.path != node {
		t.Fatal(found, ok)
	}

	// npm's shims name the node.exe beside them.
	npm := f.write("npm/codex.cmd", "IF EXIST \"%dp0%\\node.exe\" (\n  SET \"_prog=%dp0%\\node.exe\"\n)\n")
	none := func(string) (string, bool) { return "", false }
	if found, ok := shimNode(env, npm, none); ok {
		t.Fatal(found)
	}
	sibling := f.script("npm/node.exe", "")
	if found, ok := shimNode(env, npm, none); !ok || found.path != sibling {
		t.Fatal(found, ok)
	}
	script := f.path("npm/node_modules/@openai/codex/bin/codex.js")
	if spec, failure := shimCommand(env, npm, script); failure != nil || spec.program != sibling {
		t.Fatal(spec, failure)
	}
}

func TestRespectsPathOrderAndDeduplicatesSymlinks(t *testing.T) {
	f := newFixture(t)
	first := f.script("first/codex", "#!/bin/sh\necho 'codex-cli 1.0.0'\n")
	f.script("second/codex", "#!/bin/sh\necho 'codex-cli 2.0.0'\n")
	f.symlink(first, "alias/codex")
	var paths []string
	for _, found := range findPrograms(strings.Join([]string{f.path("first"), f.path("alias"), f.path("second")}, ":"), "codex") {
		paths = append(paths, found.path)
	}
	if !slices.Equal(paths, []string{first, f.path("second/codex")}) {
		t.Fatal(paths)
	}
}

// fakeCodex is a standalone Codex 1.0.0 whose update runs update.
func fakeCodex(t *testing.T, f *fixture, update string) *installation {
	t.Helper()
	f.write("package/codex-package.json", `{"variant":"codex","layoutVersion":1,"entrypoint":"bin/codex"}`)
	script := f.script("package/bin/codex", "#!/bin/sh\nif [ \"$1\" = '--version' ]; then cat version; else "+update+"; fi\n")
	f.write("version", "codex-cli 1.0.0\n")
	f.symlink(script, "bin/codex")
	install := detect(f.environment(), ClientCodex)
	if install == nil {
		t.Fatal("the stand-in Codex was not found")
	}
	return install
}

// fakeWSL stands in for wsl.exe: it runs the command after --exec from the
// fixture, which plays the distribution user's home.
func fakeWSL(f *fixture) *wslTarget {
	home := f.root
	launcher := f.script("wsl", "#!/bin/sh\ncd '"+home+"' || exit 1\nwhile [ \"$1\" != --exec ]; do shift; done\nshift\n"+
		"exec env SHELL=/bin/sh HOME='"+home+"' \"$@\"\n")
	return &wslTarget{distribution: "Ubuntu", launcher: launcher, share: "/", mountRoot: f.path("mnt")}
}

func TestReadsTheHomeAndLoginPathInWSL(t *testing.T) {
	f := newFixture(t)
	env, failure := wslEnvironment(fakeWSL(f), false)
	if failure != nil {
		t.Fatal(failure)
	}
	if env.home != f.root || !slices.Contains(strings.Split(env.path, ":"), f.path(".local/bin")) {
		t.Fatalf("home %q, path %q", env.home, env.path)
	}
}

func TestChecksAndUpdatesClientsInWSLApartFromWindowsDrives(t *testing.T) {
	f := newFixture(t)
	local := fakeCodex(t, f, "echo 'codex-cli 1.1.0' > version")
	// This computer's npm shim, which WSL appends to PATH.
	f.script("mnt/c/npm/codex", "#!/bin/sh\necho 'codex-cli 9.0.0'\n")
	cursor := f.script("my tools/cursor-agent", "#!/bin/sh\n")
	env := f.environment()
	env.path = f.searchPath(f.path("mnt/c/npm"), f.path("bin"), f.path("my tools"))
	env.wsl = fakeWSL(f)
	install := detect(env, ClientCodex)
	if install == nil || install.executable != f.path("bin/codex") || install.resolved != local.resolved || install.method != native || len(install.others) != 0 {
		t.Fatalf("install = %+v", install)
	}
	var found []string
	for _, p := range env.programs("cursor-agent") {
		found = append(found, p.path)
	}
	if !slices.Equal(found, []string{cursor}) {
		t.Fatal(found)
	}
	if !env.executable(f.path("bin/codex")) || env.executable(f.path("version")) || !env.isFile(f.path("version")) {
		t.Fatal("file checks in WSL")
	}
	result, failure := performUpdate(env, ClientCodex, install, version(t, ClientCodex, "1.1.0"))
	if failure != nil || result.String() != "1.1.0" {
		t.Fatal(result, failure)
	}
}

func TestUpdatesAnIsolatedNativeClientAndChecksTheResult(t *testing.T) {
	f := newFixture(t)
	install := fakeCodex(t, f, "echo 'codex-cli 1.1.0' > version")
	if install.method != native {
		t.Fatal(install.method)
	}
	result, failure := performUpdate(f.environment(), ClientCodex, install, version(t, ClientCodex, "1.1.0"))
	if failure != nil || result.String() != "1.1.0" {
		t.Fatal(result, failure)
	}
	// Never downgrade even if the running check has stale metadata.
	result, failure = performUpdate(f.environment(), ClientCodex, install, version(t, ClientCodex, "1.0.0"))
	if failure != nil || result.String() != "1.1.0" {
		t.Fatal(result, failure)
	}
}

func TestReportsUpdateFailureAndSuccessWithoutVersionChange(t *testing.T) {
	for _, tc := range []struct{ script, code string }{
		{"echo update-refused >&2; exit 1", "command"},
		{"echo download-failed >&2; exit 0", "unchanged"},
	} {
		f := newFixture(t)
		install := fakeCodex(t, f, tc.script)
		_, failure := performUpdate(f.environment(), ClientCodex, install, version(t, ClientCodex, "1.1.0"))
		if failure == nil || failure.code != tc.code {
			t.Fatalf("%s: %v", tc.script, failure)
		}
		if tc.code == "unchanged" && !strings.Contains(failure.detail, "download-failed") {
			t.Fatal(failure.detail)
		}
		if current, failure := currentVersion(f.environment(), ClientCodex, install); failure != nil || current.String() != "1.0.0" {
			t.Fatal(current, failure)
		}
	}
}

func TestRefusesToUpdateWhenTheSelectedInstallationChanges(t *testing.T) {
	f := newFixture(t)
	install := fakeCodex(t, f, "echo should-not-run > marker")
	if err := os.Remove(install.executable); err != nil {
		t.Fatal(err)
	}
	f.script("bin/codex", "#!/bin/sh\necho 'codex-cli 1.0.0'\n")
	if _, failure := performUpdate(f.environment(), ClientCodex, install, version(t, ClientCodex, "1.1.0")); failure == nil || failure.code != "changed" {
		t.Fatal(failure)
	}
	if _, err := os.Stat(f.path("marker")); err == nil {
		t.Fatal("the update ran")
	}
}

func TestNpmUpdateTargetsTheDetectedPrefixAndAllowsThePackageScripts(t *testing.T) {
	f := newFixture(t)
	f.script("node/bin/node", "#!/bin/sh\nexit 0\n")
	npm := f.write("node/lib/node_modules/npm/bin/npm-cli.js", "")
	prefix := f.path("node")
	for _, tc := range []struct {
		id               ClientID
		latest, package_ string
	}{
		{ClientCodex, "0.158.0", "@openai/codex"},
		{ClientClaude, "2.1.299", "@anthropic-ai/claude-code"},
	} {
		install := &installation{executable: filepath.Join(prefix, "bin", tc.id.name()), method: npmAt(prefix)}
		spec, failure := updateCommand(f.environment(), tc.id, install, version(t, tc.id, tc.latest))
		if failure != nil || spec.program != filepath.Join(prefix, "bin", "node") {
			t.Fatal(spec, failure)
		}
		if want := []string{
			npm, "install", "--global", "--prefix", prefix, "--cache", f.path(".cache/astrlink/npm"),
			"--allow-scripts=" + tc.package_, tc.package_ + "@" + tc.latest,
		}; !slices.Equal(spec.args, want) {
			t.Fatalf("args = %q", spec.args)
		}
	}
	// Without npm beside the prefix or on PATH there is nothing to run.
	other := &installation{executable: "codex", method: npmAt(f.path("elsewhere"))}
	if _, failure := updateCommand(f.environment(), ClientCodex, other, version(t, ClientCodex, "0.158.0")); failure == nil || failure.code != "unsupported" {
		t.Fatal(failure)
	}
}

func TestNpmUpdatesUseASeparateCacheLocallyAndInWSL(t *testing.T) {
	for _, inWSL := range []bool{false, true} {
		f := newFixture(t)
		// Include spaces to exercise argument passing through the WSL
		// launcher.
		home := f.path("user home")
		prefix := filepath.Join(home, "node")
		cache := f.mkdir("user home/.npm")
		if err := os.Chmod(cache, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(cache, 0o755) })
		f.write("user home/node/lib/node_modules/@openai/codex/package.json", `{"name":"@openai/codex"}`)
		codex := f.script("user home/node/lib/node_modules/@openai/codex/bin/codex.js", "#!/bin/sh\ncat version\n")
		f.write("user home/version", "codex-cli 1.0.0\n")
		f.script("user home/node/bin/node", "#!/bin/sh\nexec /bin/sh \"$@\"\n")
		f.symlink(codex, "user home/node/bin/codex")
		// Stand in for npm: reject the default cache, otherwise write
		// download data to the requested cache before updating the
		// installed version.
		f.write("user home/node/lib/node_modules/npm/bin/npm-cli.js", "#!/bin/sh\nset -eu\ncache=.npm\n"+
			"while [ $# -gt 0 ]; do\n  if [ \"$1\" = --cache ]; then shift; cache=$1; fi\n  shift\ndone\n"+
			"if [ \"$cache\" = .npm ]; then echo 'npm error code EACCES' >&2; exit 1; fi\n"+
			"mkdir -p \"$cache\"\necho download > \"$cache/download\"\necho 'codex-cli 1.1.0' > version\n")
		env := &Environment{home: home, path: f.searchPath(filepath.Join(prefix, "bin"))}
		if inWSL {
			w := fakeWSL(f)
			w.launcher = f.script("wsl-with-home", "#!/bin/sh\ncd '"+home+"' || exit 1\n"+
				"while [ \"$1\" != --exec ]; do shift; done\nshift\nexec \"$@\"\n")
			env.wsl = w
		}
		install := detect(env, ClientCodex)
		// detect resolves symlinks, and macOS keeps temporary directories
		// behind one (/var is /private/var).
		resolvedPrefix, err := filepath.EvalSymlinks(prefix)
		if err != nil {
			t.Fatal(err)
		}
		if install == nil || install.method != npmAt(resolvedPrefix) {
			t.Fatalf("WSL %v: %+v", inWSL, install)
		}
		result, failure := performUpdate(env, ClientCodex, install, version(t, ClientCodex, "1.1.0"))
		entries, err := os.ReadDir(cache)
		if err != nil || len(entries) != 0 {
			t.Fatal("the default cache was written", entries, err)
		}
		if failure != nil || result.String() != "1.1.0" {
			t.Fatalf("WSL %v: %v, %v", inWSL, result, failure)
		}
		if got := f.read("user home/.cache/astrlink/npm/download"); got != "download\n" {
			t.Fatal(got)
		}
	}
}

func TestPiDelegatesUpdatesAndCursorRespectsTheStaticChannel(t *testing.T) {
	f := newFixture(t)
	for _, method := range []installMethod{native, packageManager("pnpm")} {
		install := &installation{executable: f.path("bin/pi"), method: method}
		spec, failure := updateCommand(f.environment(), ClientPi, install, version(t, ClientPi, "0.87.1"))
		if failure != nil || spec.program != install.executable || !slices.Equal(spec.args, []string{"update", "--self"}) {
			t.Fatal(spec, failure)
		}
	}
	cursor := &installation{executable: f.path("bin/cursor-agent"), method: native}
	latest := version(t, ClientCursor, "2026.09.28-64d2043")
	if spec, failure := updateCommand(f.environment(), ClientCursor, cursor, latest); failure != nil || !slices.Equal(spec.args, []string{"update"}) {
		t.Fatal(spec, failure)
	}
	f.write(".cursor/cli-config.json", `{"channel":"static"}`)
	if _, failure := updateCommand(f.environment(), ClientCursor, cursor, latest); failure == nil || failure.code != "unsupported" {
		t.Fatal(failure)
	}
	// Homebrew installations upgrade through their own brew, if it runs.
	formula := &installation{executable: "codex", method: brew(f.path("brew"), "codex", false)}
	if _, failure := updateCommand(f.environment(), ClientCodex, formula, version(t, ClientCodex, "1.0.0")); failure == nil || failure.code != "unsupported" {
		t.Fatal(failure)
	}
	brewProgram := f.script("brew/bin/brew", "#!/bin/sh\n")
	spec, failure := updateCommand(f.environment(), ClientCodex, formula, version(t, ClientCodex, "1.0.0"))
	if failure != nil || spec.program != brewProgram || !slices.Equal(spec.args, []string{"upgrade", "--formula", "codex"}) {
		t.Fatal(spec, failure)
	}
	for _, method := range []installMethod{unknown, packageManager("bun")} {
		install := &installation{executable: "codex", method: method}
		if _, failure := updateCommand(f.environment(), ClientCodex, install, version(t, ClientCodex, "1.0.0")); failure == nil || failure.code != "unsupported" {
			t.Fatal(method, failure)
		}
	}
}

func TestPiNpmUpdatesRunUnderNodeWhenBunAnswersToNode(t *testing.T) {
	f := newFixture(t)
	bun := f.script(".bun/bin/bun", "#!/bin/sh\n")
	f.mkdir("prefix/bin")
	f.symlink(bun, "bin/node")
	entry := f.write("prefix/lib/node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js", "")
	pi := &installation{executable: f.path("prefix/bin/pi"), resolved: entry, method: npmAt(f.path("prefix"))}
	latest := version(t, ClientPi, "1.2.0")
	env := f.environment()
	env.path = f.path("bin")
	if _, failure := updateCommand(env, ClientPi, pi, latest); failure == nil || failure.code != "unsupported" {
		t.Fatal(failure)
	}
	// npm itself runs under Bun when nothing else answers to `node`.
	f.write("prefix/lib/node_modules/npm/bin/npm-cli.js", "")
	codex := &installation{executable: f.path("prefix/bin/codex"), method: pi.method}
	if spec, failure := updateCommand(env, ClientCodex, codex, version(t, ClientCodex, "0.158.0")); failure != nil || spec.program != f.path("bin/node") {
		t.Fatal(spec, failure)
	}

	node := f.script("node/bin/node", "#!/bin/sh\n")
	env.path = f.path("bin") + ":" + f.path("node/bin")
	spec, failure := updateCommand(env, ClientPi, pi, latest)
	if failure != nil || spec.program != node || !slices.Equal(spec.args, []string{entry, "update", "--self"}) {
		t.Fatal(spec, failure)
	}
	// Node.js beside the prefix comes before PATH, unless it is Bun.
	f.symlink(bun, "prefix/bin/node")
	if spec, failure := updateCommand(env, ClientPi, pi, latest); failure != nil || spec.program != node {
		t.Fatal(spec, failure)
	}
	if err := os.Remove(f.path("prefix/bin/node")); err != nil {
		t.Fatal(err)
	}
	sibling := f.script("prefix/bin/node", "#!/bin/sh\n")
	if spec, failure := updateCommand(env, ClientPi, pi, latest); failure != nil || spec.program != sibling {
		t.Fatal(spec, failure)
	}
}

func systemEnvironment(t *testing.T) *Environment {
	return &Environment{home: t.TempDir(), path: "/usr/bin:/bin"}
}

func TestBoundsOutputAndReportsNonzeroExit(t *testing.T) {
	output, failure := run(systemEnvironment(t), command("/bin/sh", "-c", "head -c 30000 /dev/zero"), 3*time.Second)
	if failure != nil || len(output) != outputLimit {
		t.Fatal(len(output), failure)
	}
	_, failure = run(systemEnvironment(t), command("/bin/sh", "-c", "echo denied >&2; exit 1"), 3*time.Second)
	if failure == nil || failure.code != "command" || !strings.Contains(failure.detail, "denied") {
		t.Fatal(failure)
	}
	if _, failure := run(systemEnvironment(t), command(filepath.Join(t.TempDir(), "missing")), 3*time.Second); failure == nil || failure.code != "command" {
		t.Fatal(failure)
	}
}

func TestTimeoutStopsDescendantsAndReapsTheProcess(t *testing.T) {
	started := time.Now()
	_, failure := run(systemEnvironment(t), command("/bin/sh", "-c", "sleep 30 & wait"), 100*time.Millisecond)
	if failure == nil || failure.code != "timeout" {
		t.Fatal(failure)
	}
	if elapsed := time.Since(started); elapsed >= 3*time.Second {
		t.Fatal(elapsed)
	}
}

func TestAFinishedCommandLeavesNoChildBehind(t *testing.T) {
	// The child keeps the output open; the command still returns once its
	// own process exits, and the child goes with it.
	output, failure := run(systemEnvironment(t), command("/bin/sh", "-c", "sleep 30 & echo $!; echo done"), 20*time.Second)
	if failure != nil || !strings.HasSuffix(output, "done\n") {
		t.Fatal(output, failure)
	}
}

func TestPassesArgumentsLiterallyWithoutAShell(t *testing.T) {
	printf, err := exec.LookPath("printf")
	if err != nil {
		t.Skip("printf is unavailable")
	}
	dir := t.TempDir()
	env := &Environment{home: dir, path: "/usr/bin:/bin"}
	output, failure := run(env, command(printf, "%s", "$(touch should-not-exist); &"), 3*time.Second)
	if failure != nil || output != "$(touch should-not-exist); &" {
		t.Fatal(output, failure)
	}
	if _, err := os.Stat(filepath.Join(dir, "should-not-exist")); err == nil {
		t.Fatal("a shell ran the argument")
	}
}

func TestPassesProxyToInstallerChildrenAndHonorsDirectMode(t *testing.T) {
	proxyEnv, failure := macEnvironment("<dictionary> {\n HTTPSEnable : 1\n HTTPSProxy : 127.0.0.1\n HTTPSPort : 6152\n}")
	if failure != nil {
		t.Fatal(failure)
	}
	env := systemEnvironment(t)
	env.useSystemProxy, env.proxyEnv = true, proxyEnv
	spec := command("/bin/sh", "-c", `printf '%s|%s|%s' "$HTTPS_PROXY" "$npm_config_https_proxy" "$NO_PROXY"`)
	output, failure := run(env, spec, 3*time.Second)
	if failure != nil || !strings.HasPrefix(output, "http://127.0.0.1:6152|http://127.0.0.1:6152|") {
		t.Fatal(output, failure)
	}
	env.useSystemProxy = false
	if output, failure := run(env, spec, 3*time.Second); failure != nil || output != "||*" {
		t.Fatal(output, failure)
	}
	// With the system proxy and no settings to apply, children keep
	// their own.
	t.Setenv("HTTPS_PROXY", "http://inherited.invalid:1")
	env.useSystemProxy, env.proxyEnv = true, nil
	if output, failure := run(env, spec, 3*time.Second); failure != nil || !strings.HasPrefix(output, "http://inherited.invalid:1|") {
		t.Fatal(output, failure)
	}
}

// fakeService answers version checks from bodies by URL, with 404 for any
// other.
type fakeService struct {
	mu     sync.Mutex
	bodies map[string]string
	asked  []string
}

func (s *fakeService) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.asked = append(s.asked, r.URL.String())
	s.mu.Unlock()
	body, ok := s.bodies[r.URL.String()]
	status := http.StatusOK
	if !ok {
		status = http.StatusNotFound
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}, nil
}

const codexLatest = "https://registry.npmjs.org/@openai/codex/latest"

// testManager checks f's clients: its local environment is env, WSL
// distributions are none, and versions come from service.
func testManager(t *testing.T, f *fixture, env *Environment, service *fakeService) (*Manager, chan ClientSnapshot) {
	t.Helper()
	events := make(chan ClientSnapshot, 256)
	m := NewManager(func() bool { return false }, func(s ClientSnapshot) { events <- s })
	m.home = func() (string, error) { return f.root, nil }
	m.client = func(bool) *http.Client { return &http.Client{Transport: service} }
	m.loadEnvironment = func(string, bool) (*Environment, *clientError) { return env, nil }
	m.distributions = func() []wsl.Distribution { return nil }
	m.wslEnvironment = func(name string, _ bool) (*Environment, *clientError) {
		return nil, newErrorf("command", "unexpected WSL %s", name)
	}
	return m, events
}

// finished is the snapshot an operation publishes when it ends.
func finished(events chan ClientSnapshot) ClientSnapshot {
	for {
		if s := <-events; !s.Busy {
			return s
		}
	}
}

func statusOf(t *testing.T, s ClientSnapshot, id ClientID, distribution *string) ClientStatus {
	t.Helper()
	for _, status := range s.Clients {
		if status.ID == id && sameDistribution(status.WSL, distribution) {
			return status
		}
	}
	t.Fatalf("no %s in %+v", id, s.Clients)
	return ClientStatus{}
}

func value(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestRefreshChecksEveryClientInTheBackground(t *testing.T) {
	f := newFixture(t)
	fakeCodex(t, f, "echo 'codex-cli 1.1.0' > version")
	service := &fakeService{bodies: map[string]string{codexLatest: `{"version":"1.1.0"}`}}
	m, events := testManager(t, f, f.environment(), service)
	initial, err := m.Refresh()
	if err != nil || !initial.Busy {
		t.Fatal(initial, err)
	}
	for _, status := range initial.Clients {
		if status.Phase != "checking" {
			t.Fatalf("%s: %s", status.ID, status.Phase)
		}
	}
	done := finished(events)
	codex := statusOf(t, done, ClientCodex, nil)
	if codex.Phase != "available" || !codex.CanUpdate || value(codex.CurrentVersion) != "1.0.0" || value(codex.LatestVersion) != "1.1.0" ||
		codex.InstallMethod != "native" || value(codex.Executable) != f.path("bin/codex") || codex.CheckedAt == nil {
		t.Fatalf("codex = %+v", codex)
	}
	for _, id := range []ClientID{ClientClaude, ClientCursor, ClientPi} {
		if status := statusOf(t, done, id, nil); status.Phase != "not_installed" {
			t.Fatalf("%s = %+v", id, status)
		}
	}
	if !slices.Equal(service.asked, []string{codexLatest}) || f.read("version") != "codex-cli 1.0.0\n" {
		t.Fatal("a refresh updated or asked elsewhere", service.asked)
	}
	if done.Revision <= initial.Revision || m.Snapshot().Revision != done.Revision {
		t.Fatal("revisions")
	}
	// The operation is over, so another may start.
	if again, err := m.Refresh(); err != nil || !again.Busy {
		t.Fatal(again, err)
	}
	finished(events)
}

func TestUpdateUpdatesOnlyTheChosenClient(t *testing.T) {
	f := newFixture(t)
	fakeCodex(t, f, "echo 'codex-cli 1.1.0' > version")
	service := &fakeService{bodies: map[string]string{codexLatest: `{"version":"1.1.0"}`}}
	m, events := testManager(t, f, f.environment(), service)
	initial, err := m.Update([]ClientTarget{{ID: ClientCodex}})
	if err != nil || statusOf(t, initial, ClientCodex, nil).Phase != "updating" || statusOf(t, initial, ClientClaude, nil).Phase != "idle" {
		t.Fatal(initial, err)
	}
	done := finished(events)
	codex := statusOf(t, done, ClientCodex, nil)
	if codex.Phase != "updated" || value(codex.CurrentVersion) != "1.1.0" || codex.CanUpdate || codex.ErrorCode != nil {
		t.Fatalf("codex = %+v", codex)
	}
	if statusOf(t, done, ClientClaude, nil).Phase != "idle" || f.read("version") != "codex-cli 1.1.0\n" {
		t.Fatal("the update reached another client or did not run")
	}
}

func TestAnUpdateThatChangesNothingIsReported(t *testing.T) {
	f := newFixture(t)
	fakeCodex(t, f, "echo download-failed >&2")
	service := &fakeService{bodies: map[string]string{codexLatest: `{"version":"1.1.0"}`}}
	m, events := testManager(t, f, f.environment(), service)
	if _, err := m.Update([]ClientTarget{{ID: ClientCodex}}); err != nil {
		t.Fatal(err)
	}
	codex := statusOf(t, finished(events), ClientCodex, nil)
	if codex.Phase != "error" || value(codex.ErrorCode) != "unchanged" || !strings.Contains(value(codex.ErrorDetail), "download-failed") {
		t.Fatalf("codex = %+v", codex)
	}
}

func TestAStartDuringAnOperationReturnsItsSnapshot(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	m, events := testManager(t, f, f.environment(), &fakeService{})
	m.loadEnvironment = func(string, bool) (*Environment, *clientError) {
		<-release
		return nil, newError("command", "no environment")
	}
	m.distributions = func() []wsl.Distribution {
		<-release
		return nil
	}
	first, err := m.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	<-events
	second, err := m.Update([]ClientTarget{{ID: ClientCodex}})
	if err != nil || second.Revision != first.Revision || !second.Busy || len(events) != 0 {
		t.Fatal(second, err)
	}
	close(release)
	done := finished(events)
	for _, status := range done.Clients {
		if status.Phase != "error" || value(status.ErrorCode) != "command" {
			t.Fatalf("%s = %+v", status.ID, status)
		}
	}
}

func TestRefreshListsRunningDistributionsAndSkipsStoppedOnes(t *testing.T) {
	f := newFixture(t)
	fakeCodex(t, f, "echo 'codex-cli 1.1.0' > version")
	service := &fakeService{bodies: map[string]string{codexLatest: `{"version":"1.1.0"}`}}
	m, events := testManager(t, f, &Environment{home: f.root}, service)
	m.distributions = func() []wsl.Distribution {
		return []wsl.Distribution{{Name: "Ubuntu", Running: true}, {Name: "Debian"}, {Name: "Broken", Running: true}}
	}
	var started []string
	m.wslEnvironment = func(name string, _ bool) (*Environment, *clientError) {
		started = append(started, name)
		if name == "Broken" {
			return nil, newError("command", "WSL did not answer")
		}
		env := f.environment()
		env.wsl = fakeWSL(f)
		return env, nil
	}
	if _, err := m.Refresh(); err != nil {
		t.Fatal(err)
	}
	done := finished(events)
	ubuntu := text("Ubuntu")
	codex := statusOf(t, done, ClientCodex, ubuntu)
	if codex.Phase != "available" || value(codex.CurrentVersion) != "1.0.0" || value(codex.Executable) != f.path("bin/codex") {
		t.Fatalf("codex in WSL = %+v", codex)
	}
	if len(done.Clients) != len(allClients)+1 || statusOf(t, done, ClientCodex, nil).Phase != "not_installed" {
		t.Fatalf("clients = %+v", done.Clients)
	}
	if !slices.Equal(done.WSLUnchecked, []string{"Debian", "Broken"}) || !slices.Equal(started, []string{"Ubuntu", "Broken"}) {
		t.Fatal(done.WSLUnchecked, started)
	}

	// An update reaches the distribution the user chose.
	if _, err := m.Update([]ClientTarget{{ID: ClientCodex, WSL: ubuntu}, {ID: ClientPi, WSL: text("Gone")}}); err != nil {
		t.Fatal(err)
	}
	done = finished(events)
	if codex := statusOf(t, done, ClientCodex, ubuntu); codex.Phase != "updated" || value(codex.CurrentVersion) != "1.1.0" {
		t.Fatalf("codex in WSL = %+v", codex)
	}
	if statusOf(t, done, ClientCodex, nil).Phase != "not_installed" || f.read("version") != "codex-cli 1.1.0\n" {
		t.Fatal("the update did not run in WSL alone")
	}
}
