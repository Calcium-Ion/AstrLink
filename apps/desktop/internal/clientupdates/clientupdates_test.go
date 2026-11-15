package clientupdates

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type fixture struct {
	t    *testing.T
	root string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return &fixture{t: t, root: t.TempDir()}
}

func (f *fixture) path(rel string) string { return filepath.Join(f.root, filepath.FromSlash(rel)) }

func (f *fixture) write(rel, contents string) string {
	f.t.Helper()
	path := f.path(rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return path
}

func (f *fixture) mkdir(rel string) string {
	f.t.Helper()
	path := f.path(rel)
	if err := os.MkdirAll(path, 0o755); err != nil {
		f.t.Fatal(err)
	}
	return path
}

// environment is a computer whose PATH holds the fixture's bin and the
// few system tools the stand-in scripts use, and no real client.
func (f *fixture) environment() *Environment {
	return &Environment{home: f.root, path: f.searchPath(f.path("bin"))}
}

// searchPath is dirs, then a directory of links to the few system tools
// the stand-in scripts use, so that no real client is on it.
func (f *fixture) searchPath(dirs ...string) string {
	f.t.Helper()
	tools := f.path("tools")
	if _, err := os.Stat(tools); err != nil {
		f.mkdir("tools")
		if runtime.GOOS != "windows" {
			for _, name := range []string{"cat", "readlink", "mkdir"} {
				found, err := exec.LookPath(name)
				if err != nil {
					f.t.Fatal(err)
				}
				if err := os.Symlink(found, filepath.Join(tools, name)); err != nil {
					f.t.Fatal(err)
				}
			}
		}
	}
	return strings.Join(append(dirs, tools), string(os.PathListSeparator))
}

func version(t *testing.T, id ClientID, raw string) clientVersion {
	t.Helper()
	v, ok := parseClientVersion(id, raw)
	if !ok {
		t.Fatalf("%s: %q does not parse", id, raw)
	}
	return v
}

func TestReadsVersionOutputAndComparesSemverWithoutDowngrading(t *testing.T) {
	codex, failure := parseVersion(ClientCodex, "codex-cli 0.153.4\n")
	if failure != nil || !sameVersion(codex, version(t, ClientCodex, "0.153.4")) {
		t.Fatal(codex, failure)
	}
	claude, failure := parseVersion(ClientClaude, "2.1.280 (Claude Code)\n")
	if failure != nil || claude.order.compare(semver{major: 2, minor: 1, patch: 280}) != 0 {
		t.Fatal(claude, failure)
	}
	for _, tc := range []struct {
		a, b  string
		newer bool
	}{
		{"v2.10.0", "2.9.9", true},
		{"2.9.9", "2.9.9", false},
		{"0.160.0-beta.1", "0.159.0", true},
		{"1.0.0", "1.0.0-rc.1", true},
		{"1.0.0-rc.1", "1.0.0", false},
		{"1.0.0-alpha.2", "1.0.0-alpha.10", false},
		{"1.0.0-beta", "1.0.0-alpha.10", true},
	} {
		if got := version(t, ClientPi, tc.a).newerThan(version(t, ClientPi, tc.b)); got != tc.newer {
			t.Errorf("%s newer than %s = %v", tc.a, tc.b, got)
		}
	}
	if _, failure := parseVersion(ClientCodex, "not installed"); failure == nil || failure.code != "version" {
		t.Fatal(failure)
	}
	for _, invalid := range []string{"01.2.3", "1.2", "1.2.3-", "1.2.3-01", "1.2.3+", "1.2.3-a..b", "1.2.3-a_b"} {
		if _, ok := parseClientVersion(ClientCodex, invalid); ok {
			t.Errorf("%q parsed", invalid)
		}
	}
	if v := version(t, ClientCodex, "(v1.2.3+build.01)"); v.String() != "1.2.3+build.01" {
		t.Fatal(v)
	}
}

func TestOrdersDatedCursorBuilds(t *testing.T) {
	current, failure := parseVersion(ClientCursor, "2026.08.11-e8db854\n")
	if failure != nil || current.String() != "2026.08.11-e8db854" {
		t.Fatal(current, failure)
	}
	latest := version(t, ClientCursor, "2026.09.28-64d2043")
	if !latest.newerThan(current) || current.newerThan(latest) || latest.newerThan(latest) {
		t.Fatal("dated order")
	}
	// Like Cursor's updater, another build from the same day is an update.
	if !version(t, ClientCursor, "2026.09.28-1a2b3c4").newerThan(latest) {
		t.Fatal("same-day build")
	}
	for _, invalid := range []string{"2026.9.28-64d2043", "1.2.3", "2026.09.28-$(touch)"} {
		if _, ok := parseClientVersion(ClientCursor, invalid); ok {
			t.Errorf("%q parsed", invalid)
		}
	}
}

func TestIdentifiesNpmAndBrewWithoutConfusingNativeInstallations(t *testing.T) {
	f := newFixture(t)
	env := f.environment()
	npm := f.write("node/lib/node_modules/@openai/codex/package.json", `{"name":"@openai/codex"}`)
	binary := filepath.Join(filepath.Dir(npm), "bin", "codex.js")
	for _, tc := range []struct {
		id       ClientID
		resolved string
		want     installMethod
	}{
		{ClientCodex, binary, npmAt(f.path("node"))},
		{ClientClaude, filepath.FromSlash("/opt/homebrew/Caskroom/claude-code@latest/2.1.280/claude"), brew(filepath.FromSlash("/opt/homebrew"), "claude-code@latest", true)},
		{ClientCodex, filepath.FromSlash("/usr/local/Cellar/codex/1.0/bin/codex"), brew(filepath.FromSlash("/usr/local"), "codex", false)},
		{ClientClaude, f.path(".local/share/claude/versions/2.1.0"), native},
		{ClientCodex, f.path("random/codex"), unknown},
	} {
		if got := installationMethod(env, tc.id, tc.resolved); got != tc.want {
			t.Errorf("%s %s: %+v, want %+v", tc.id, tc.resolved, got, tc.want)
		}
	}
}

func TestRecognizesAStandaloneCodexPackageByItsEntrypoint(t *testing.T) {
	f := newFixture(t)
	// The Windows package names bin/codex.exe.
	f.write("release/codex-package.json", `{"variant":"codex","layoutVersion":1,"entrypoint":"bin/codex.exe"}`)
	binary := f.write("release/bin/codex.exe", "")
	env := f.environment()
	if got := installationMethod(env, ClientCodex, binary); got != native {
		t.Fatal(got)
	}
	if got := installationMethod(env, ClientCodex, f.write("release/bin/codex", "")); got != unknown {
		t.Fatal(got)
	}
	// A layout version written as a float is not 1, as in serde_json.
	f.write("release/codex-package.json", `{"variant":"codex","layoutVersion":1.0,"entrypoint":"bin/codex.exe"}`)
	if got := installationMethod(env, ClientCodex, binary); got != unknown {
		t.Fatal(got)
	}
}

func TestExcludesOtherPackageManagersAndProjectNodeModules(t *testing.T) {
	f := newFixture(t)
	env := f.environment()
	pkg := f.write(".bun/install/global/node_modules/@openai/codex/package.json", `{"name":"@openai/codex"}`)
	if got := installationMethod(env, ClientCodex, filepath.Join(filepath.Dir(pkg), "bin", "codex.js")); got != unknown {
		t.Fatal(got)
	}
	if !env.windows() {
		project := f.write("project/node_modules/@openai/codex/package.json", `{"name":"@openai/codex"}`)
		if got := installationMethod(env, ClientCodex, filepath.Join(filepath.Dir(project), "bin", "codex.js")); got != unknown {
			t.Fatal(got)
		}
	}
	// A package whose manifest names another package is not the client.
	other := f.write("node/lib/node_modules/@openai/codex/package.json", `{"name":"@evil/codex"}`)
	if got := installationMethod(env, ClientCodex, filepath.Join(filepath.Dir(other), "bin", "codex.js")); got != unknown {
		t.Fatal(got)
	}
	if found := findPrograms(".:relative", "codex"); len(found) != 0 {
		t.Fatal(found)
	}
}

func TestIdentifiesCursorAndPiInstallations(t *testing.T) {
	f := newFixture(t)
	env := f.environment()
	formula := f.write("brew/Cellar/pi-coding-agent/0.87.1/libexec/lib/node_modules/@earendil-works/pi-coding-agent/package.json",
		`{"name":"@earendil-works/pi-coding-agent"}`)
	legacy := f.write("node/lib/node_modules/@mariozechner/pi-coding-agent/package.json", `{"name":"@mariozechner/pi-coding-agent"}`)
	bun := f.write(".bun/install/global/node_modules/@earendil-works/pi-coding-agent/package.json", `{"name":"@earendil-works/pi-coding-agent"}`)
	f.write(".pi/agent/install/managed-install.json", `{"kind":"pi-managed-install","schemaVersion":1,"layout":"releases-v1"}`)
	cases := []struct {
		id       ClientID
		resolved string
		want     installMethod
	}{
		{ClientCursor, f.path(".local/share/cursor-agent/versions/2026.09.28-64d2043/cursor-agent"), native},
		// The Homebrew formula wraps an npm layout inside the Cellar.
		{ClientPi, filepath.Join(filepath.Dir(formula), "dist", "cli.js"), brew(f.path("brew"), "pi-coding-agent", false)},
		{ClientPi, filepath.Join(filepath.Dir(legacy), "dist", "cli.js"), npmAt(f.path("node"))},
		{ClientPi, filepath.Join(filepath.Dir(bun), "dist", "cli.js"), packageManager("bun")},
		{ClientPi, f.path(".pi/agent/bin/pi"), native},
	}
	cases = append(cases, struct {
		id       ClientID
		resolved string
		want     installMethod
	}{ClientCursor, filepath.FromSlash("/opt/homebrew/Caskroom/cursor-cli/2026.09.28-64d2043/dist-package/cursor-agent"), brew(filepath.FromSlash("/opt/homebrew"), "cursor-cli", true)})
	for _, tc := range cases {
		if got := installationMethod(env, tc.id, tc.resolved); got != tc.want {
			t.Errorf("%s %s: %+v, want %+v", tc.id, tc.resolved, got, tc.want)
		}
	}
	// Windows PowerShell 5.1 writes the marker with a BOM.
	f.write(".pi/agent/install/managed-install.json", "\uFEFF"+`{"kind":"pi-managed-install","schemaVersion":1,"layout":"releases-v1"}`)
	if got := installationMethod(env, ClientPi, f.path(".pi/agent/bin/pi-launcher.js")); got != native {
		t.Fatalf("with a BOM: %+v", got)
	}
}

// piInstallerCmd is the pi.cmd that Pi's install.ps1 writes when it
// installed a standalone Node.js.
const piInstallerCmd = "@ECHO off\r\n" +
	"SETLOCAL\r\n" +
	"SET \"node=node\"\r\n" +
	"IF EXIST \"%LOCALAPPDATA%\\pi-node\\current\\node.exe\" SET \"node=%LOCALAPPDATA%\\pi-node\\current\\node.exe\"\r\n" +
	"\"%node%\" \"%~dp0pi-launcher.js\" %*\r\n" +
	"SET \"pi_exit_code=%ERRORLEVEL%\"\r\n" +
	"IF \"%pi_exit_code%\"==\"9009\" (\r\n" +
	"  IF \"%node%\"==\"node\" (\r\n" +
	"    \"%SystemRoot%\\System32\\where.exe\" /Q node\r\n" +
	"    IF ERRORLEVEL 1 GOTO node_missing\r\n" +
	"  ) ELSE IF NOT EXIST \"%node%\" GOTO node_missing\r\n" +
	")\r\n" +
	"EXIT /B %pi_exit_code%\r\n" +
	":node_missing\r\n" +
	"ECHO Pi could not find Node.js. Install Node.js 22.19.0 or newer, or rerun the installer at https://pi.dev. 1>&2\r\n" +
	"EXIT /B %pi_exit_code%\r\n"

func TestReadsNpmCmdShimTargetsInsideTheShimDirectory(t *testing.T) {
	f := newFixture(t)
	codex := f.path("npm/node_modules/@openai/codex/bin/codex.js")
	for _, shim := range []string{
		// npm 7 and later
		"@ECHO off\r\nGOTO start\r\n:find_dp0\r\nSET dp0=%~dp0\r\nEXIT /b\r\n:start\r\nSETLOCAL\r\nCALL :find_dp0\r\n\r\n" +
			"IF EXIST \"%dp0%\\node.exe\" (\r\n  SET \"_prog=%dp0%\\node.exe\"\r\n) ELSE (\r\n  SET \"_prog=node\"\r\n  SET PATHEXT=%PATHEXT:;.JS;=;%\r\n)\r\n\r\n" +
			"endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & \"%_prog%\"  \"%dp0%\\node_modules\\@openai\\codex\\bin\\codex.js\" %*\r\n",
		// npm 6
		"@IF EXIST \"%~dp0\\node.exe\" (\n  \"%~dp0\\node.exe\"  \"%~dp0\\node_modules\\@openai\\codex\\bin\\codex.js\" %*\n) ELSE (\n" +
			"  @SETLOCAL\n  @SET PATHEXT=%PATHEXT:;.JS;=;%\n  node  \"%~dp0\\node_modules\\@openai\\codex\\bin\\codex.js\" %*\n)\n",
	} {
		if got, ok := cmdShimTarget(f.write("npm/codex.cmd", shim)); !ok || got != codex {
			t.Fatalf("target = %q, %v", got, ok)
		}
	}
	// A native binary has no interpreter, as in Claude Code 2.1.113 and
	// later.
	claude := f.write("npm/claude.cmd", "CALL :find_dp0\n\"%dp0%\\node_modules\\@anthropic-ai\\claude-code\\bin\\claude.exe\"   %*\n")
	if got, ok := cmdShimTarget(claude); !ok || got != f.path("npm/node_modules/@anthropic-ai/claude-code/bin/claude.exe") {
		t.Fatalf("target = %q, %v", got, ok)
	}
	// Pi's installer writes `%~dp0` without a backslash, which it ends in.
	if got, ok := cmdShimTarget(f.write(".pi/agent/bin/pi.cmd", piInstallerCmd)); !ok || got != f.path(".pi/agent/bin/pi-launcher.js") {
		t.Fatalf("pi target = %q, %v", got, ok)
	}
	for _, invalid := range []string{
		`"%dp0%\..\@openai\codex\bin\codex.js" %*`,
		`"%dp0%\C:\other\codex.js" %*`,
		`"%~dp0..\codex.js" %*`,
		"@echo off\ncodex.exe %*\n",
		// The shim's own directory is not a target.
		`cd /d "%~dp0" %*`,
	} {
		if got, ok := cmdShimTarget(f.write("npm/codex.cmd", invalid)); ok {
			t.Errorf("%q: %q", invalid, got)
		}
	}
}

func TestRecognizesWindowsProgramsByTheirPEHeader(t *testing.T) {
	f := newFixture(t)
	// Claude Code's npm stub, left when its postinstall did not run.
	stub := f.write("npm/node_modules/@anthropic-ai/claude-code/bin/claude.exe", "echo \"Error: claude native binary not installed.\" >&2\nexit 1\n")
	if image, err := windowsImage(stub); err != nil || image {
		t.Fatal(image, err)
	}
	image := make([]byte, 0x84)
	copy(image, "MZ")
	image[60] = 0x80
	copy(image[0x80:], "PE\x00\x00")
	path := f.path("program.exe")
	if err := os.WriteFile(path, image, 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := windowsImage(path); err != nil || !ok {
		t.Fatal(ok, err)
	}
	dos := slices.Clone(image)
	copy(dos[0x80:], "NE\x00\x00")
	// Empty, DOS header only, cut-off signature, and a 16-bit NE image.
	for _, data := range [][]byte{{}, image[:64], image[:0x82], dos} {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if ok, err := windowsImage(path); err != nil || ok {
			t.Fatalf("%d bytes: %v, %v", len(data), ok, err)
		}
	}
	if _, err := windowsImage(f.path("missing.exe")); err == nil {
		t.Fatal("a missing file is an image")
	}
}

func TestParsesLatestSourcesAndRejectsInvalidVersions(t *testing.T) {
	for _, tc := range []struct {
		body   string
		method installMethod
		id     ClientID
		want   string
	}{
		{`{"version":"0.158.0"}`, native, ClientCodex, "0.158.0"},
		{"2.1.283\n", native, ClientClaude, "2.1.283"},
		{`{"url":"https://downloads.cursor.com/x.tar.gz","version":"2026.09.28-64d2043"}`, native, ClientCursor, "2026.09.28-64d2043"},
		{`{"versions":{"stable":"0.158.0"}}`, brew("/opt/homebrew", "codex", false), ClientCodex, "0.158.0"},
		{`{"version":"0.158.0"}`, brew("/opt/homebrew", "codex", true), ClientCodex, "0.158.0"},
	} {
		got, failure := parseLatest(tc.body, tc.method, tc.id)
		if failure != nil || got.String() != tc.want {
			t.Errorf("%s: %v, %v", tc.body, got, failure)
		}
	}
	for _, invalid := range []string{`{"version":"$(touch evil)"}`, `{"version":"1.0.0-beta.1"}`, "<html>error</html>", `{}`, `[]`} {
		if _, failure := parseLatest(invalid, unknown, ClientCodex); failure == nil || failure.code != "version" {
			t.Errorf("%s: %v", invalid, failure)
		}
	}
}

func TestClaudeVersionCheckRespectsTheSelectedNativeChannel(t *testing.T) {
	f := newFixture(t)
	install := &installation{executable: "claude", resolved: "claude", method: native}
	if url := latestSourceOf(f.environment(), ClientClaude, install).url; !strings.HasSuffix(url, "/latest") {
		t.Fatal(url)
	}
	f.write(".claude/settings.json", `{"autoUpdatesChannel":"stable"}`)
	if url := latestSourceOf(f.environment(), ClientClaude, install).url; !strings.HasSuffix(url, "/stable") {
		t.Fatal(url)
	}
	install.method = npmAt(f.path("node"))
	if url := latestSourceOf(f.environment(), ClientClaude, install).url; url != "https://registry.npmjs.org/@anthropic-ai/claude-code/latest" {
		t.Fatal(url)
	}
}

func TestCursorAndPiVersionChecksUseTheirReleaseServices(t *testing.T) {
	f := newFixture(t)
	install := &installation{executable: "cursor-agent", resolved: "cursor-agent", method: native}
	if pi := latestSourceOf(f.environment(), ClientPi, install); pi.url != piLatestURL || pi.body != nil {
		t.Fatal(pi)
	}
	cursor := latestSourceOf(f.environment(), ClientCursor, install)
	if cursor.url != cursorLatestURL || cursor.body["channel"] != "prod" {
		t.Fatal(cursor)
	}
	f.write(".cursor/cli-config.json", `{"channel":"lab"}`)
	if body := latestSourceOf(f.environment(), ClientCursor, install).body; body["channel"] != "lab" {
		t.Fatal(body)
	}
	f.write(".cursor/cli-config.json", `{"channel":"prod-stable-internal"}`)
	if body := latestSourceOf(f.environment(), ClientCursor, install).body; body["channel"] != "prod" {
		t.Fatal(body)
	}
	install.method = brew("/opt/homebrew", "cursor-cli", true)
	if source := latestSourceOf(f.environment(), ClientCursor, install); source.url != "https://formulae.brew.sh/api/cask/cursor-cli.json" || source.body != nil {
		t.Fatal(source)
	}
}

func TestSerializesOperationsAndRejectsUnknownClientIDs(t *testing.T) {
	manager := NewManager(func() bool { return false }, nil)
	if !manager.operation.TryLock() {
		t.Fatal("the first operation did not start")
	}
	if manager.operation.TryLock() {
		t.Fatal("a second operation started")
	}
	manager.operation.Unlock()
	if !manager.operation.TryLock() {
		t.Fatal("the lock was not released")
	}
	manager.operation.Unlock()
	var id ClientID
	if err := json.Unmarshal([]byte(`"codex; rm -rf anything"`), &id); err == nil {
		t.Fatal("an unknown client was accepted")
	}
	var target ClientTarget
	if err := json.Unmarshal([]byte(`{"id":"cursor","wsl":"Ubuntu"}`), &target); err != nil || target.ID != ClientCursor || *target.WSL != "Ubuntu" {
		t.Fatal(target, err)
	}
	if got := len(manager.Snapshot().Clients); got != len(allClients) {
		t.Fatal(got)
	}
	for _, targets := range [][]ClientTarget{nil, {{ID: ClientCodex}, {ID: ClientCodex}}, {{ID: ClientCodex, WSL: text("A")}, {ID: ClientCodex, WSL: text("A")}}} {
		if _, err := manager.Update(targets); err == nil || err.Error() != "Invalid client selection" {
			t.Errorf("%v: %v", targets, err)
		}
		if _, err := manager.Repair(targets); err == nil || err.Error() != "Invalid client selection" {
			t.Errorf("repair %v: %v", targets, err)
		}
	}
}

func TestSnapshotsSerializeAsTheTauriShellDid(t *testing.T) {
	data, err := json.Marshal(initialSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	status := `{"id":"%s","wsl":null,"phase":"idle","current_version":null,"latest_version":null,"install_method":"unknown",` +
		`"executable":null,"other_installations":[],"can_update":false,"checked_at":null,"error_code":null,"error_detail":null,` +
		`"repair":null,"previous_version":null,"kept_previous":false}`
	var clients []string
	for _, id := range allClients {
		clients = append(clients, strings.Replace(status, "%s", string(id), 1))
	}
	if want := `{"revision":0,"busy":false,"clients":[` + strings.Join(clients, ",") + `],"wsl_unchecked":[]}`; string(data) != want {
		t.Fatalf("%s\nwant %s", data, want)
	}
}

func TestReportsMaskedCurlFailuresAsNetworkErrorsAndKeepsTheErrorTail(t *testing.T) {
	output := strings.Repeat("log\n", 2000) + "Update ran successfully!\ncurl: (35) LibreSSL SSL_connect: SSL_ERROR_SYSCALL"
	failure := newError("unchanged", output)
	if failure.code != "network" || !strings.Contains(failure.detail, "SSL_ERROR_SYSCALL") || len([]rune(failure.detail)) > detailLimit {
		t.Fatal(failure.code, len(failure.detail))
	}
	if !strings.HasSuffix(output, failure.detail) {
		t.Fatal("the end of the details was not kept")
	}
}

func TestReportsCacheAndInstallationPermissionFailuresWithTheOriginalDetails(t *testing.T) {
	for _, detail := range []string{
		"wsl: Failed to mount G:\\, see dmesg for more details.\nnpm error code EACCES\nnpm error syscall open\n" +
			"npm error path /home/ada/.npm/_cacache/tmp/file\nnpm error Your cache folder contains root-owned files",
		"npm ERR! code EACCES\nnpm ERR! syscall rename\nnpm ERR! path /usr/lib/node_modules/@openai/codex",
		"npm error code EPERM\nnpm error syscall unlink",
		"Permission denied (os error 13)",
		"Access is denied. (os error 5)",
		"Operation not permitted (os error 1)",
	} {
		for _, code := range []string{"command", "unchanged"} {
			if failure := newError(code, detail); failure.code != "permission" || failure.detail != detail {
				t.Errorf("%s %q: %s", code, detail, failure.code)
			}
		}
	}
	if failure := newError("command", `wsl: Failed to mount G:\`); failure.code != "command" {
		t.Fatal(failure.code)
	}
	if failure := newError("version", "EACCES"); failure.code != "version" {
		t.Fatal(failure.code)
	}
}

func direct() *http.Client { return &http.Client{Transport: &http.Transport{}} }

func TestHandlesHTTPFailureRateLimitAndInvalidMetadata(t *testing.T) {
	install := &installation{executable: "codex", resolved: "codex", method: native}
	for _, tc := range []struct {
		status int
		body   string
		code   string
	}{
		{http.StatusTooManyRequests, "", "rate_limit"},
		{http.StatusServiceUnavailable, "", "network"},
		{http.StatusOK, "not-json", "version"},
		{http.StatusOK, strings.Repeat(" ", maxLatestBytes+1), "version"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}))
		_, failure := fetchLatest(context.Background(), direct(), latestSource{url: server.URL + "/latest"}, install, ClientCodex)
		server.Close()
		if failure == nil || failure.code != tc.code {
			t.Errorf("%d: %v", tc.status, failure)
		}
	}
}

func TestPostsTheCursorChannelToTheReleaseService(t *testing.T) {
	type request struct {
		method, path, protocol, contentType, userAgent, body string
		hasUserAgent                                         bool
	}
	seen := make(chan request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, hasUserAgent := r.Header["User-Agent"]
		seen <- request{r.Method, r.URL.Path, r.Header.Get("Connect-Protocol-Version"), r.Header.Get("Content-Type"), r.Header.Get("User-Agent"), string(body), hasUserAgent}
		_, _ = io.WriteString(w, `{"url":"https://downloads.cursor.com/x","version":"2026.09.28-64d2043"}`)
	}))
	defer server.Close()
	install := &installation{executable: "cursor-agent", resolved: "cursor-agent", method: native}
	source := latestSource{url: server.URL + "/latest", body: map[string]string{"channel": "prod"}}
	latest, failure := fetchLatest(context.Background(), direct(), source, install, ClientCursor)
	if failure != nil || latest.String() != "2026.09.28-64d2043" {
		t.Fatal(latest, failure)
	}
	got := <-seen
	if got.method != http.MethodPost || got.path != "/latest" || got.protocol != "1" || got.contentType != "application/json" ||
		got.body != `{"channel":"prod"}` {
		t.Fatalf("request = %+v", got)
	}
	// The request names no client, as the Tauri shell's did not.
	if got.hasUserAgent {
		t.Fatalf("User-Agent = %q", got.userAgent)
	}
}

func TestExportsSystemConnectProxyAndIgnoresScopedDictionaries(t *testing.T) {
	raw := "<dictionary> {\n HTTPSProxy : 127.0.0.1\n HTTPSPort : 6152\n HTTPSEnable : 1\n ExceptionsList : <array> {\n 0 : *.local\n }\n" +
		" __SCOPED__ : <dictionary> {\n HTTPSProxy : wrong.example\n HTTPSPort : 123\n }\n}"
	env := proxyMap(t, raw)
	if env["HTTPS_PROXY"] != "http://127.0.0.1:6152" || env["https_proxy"] != env["HTTPS_PROXY"] ||
		env["npm_config_https_proxy"] != env["HTTPS_PROXY"] || env["HTTP_PROXY"] != "" || !strings.Contains(env["NO_PROXY"], ".local") {
		t.Fatal(env)
	}
	if !strings.HasPrefix(env["NO_PROXY"], "localhost,127.0.0.1,::1,") {
		t.Fatal(env["NO_PROXY"])
	}
}

func proxyMap(t *testing.T, raw string) map[string]string {
	t.Helper()
	variables, failure := macEnvironment(raw)
	if failure != nil {
		t.Fatal(failure)
	}
	if len(variables) != len(proxyVariables) {
		t.Fatal(variables)
	}
	env := map[string]string{}
	for _, variable := range variables {
		env[variable.name] = variable.value
	}
	return env
}

func TestHandlesDisabledProxiesSocksIPv6AndInvalidConfiguration(t *testing.T) {
	if env := proxyMap(t, "<dictionary> {\n}"); env["HTTPS_PROXY"] != "" {
		t.Fatal(env)
	}
	socks := proxyMap(t, "<dictionary> {\n SOCKSEnable : 1\n SOCKSProxy : ::1\n SOCKSPort : 1080\n}")
	if socks["HTTPS_PROXY"] != "socks5h://[::1]:1080" || socks["ALL_PROXY"] != socks["HTTPS_PROXY"] {
		t.Fatal(socks)
	}
	for _, raw := range []string{
		"garbage",
		"<dictionary> {\n ProxyAutoConfigEnable : 1\n}",
		"<dictionary> {\n HTTPSEnable : 1\n HTTPSProxy : proxy@example.com\n HTTPSPort : 6152\n}",
		"<dictionary> {\n HTTPSEnable : 1\n HTTPSProxy : localhost\n HTTPSPort : 99999\n}",
		"<dictionary> {\n HTTPSEnable : 1\n HTTPSProxy : localhost\n",
	} {
		if _, failure := macEnvironment(raw); failure == nil || failure.code != "proxy" {
			t.Errorf("%q: %v", raw, failure)
		}
	}
}

func TestLookupsSkipWindowsDrivesAndRepeatedDirectories(t *testing.T) {
	path := "/home/ada/.local/bin:/usr/bin/:relative::/mnt/c/Users/ada/AppData/Roaming/npm:" +
		"/mnt/d:/mnt/wsl/bin:/mntc/bin:/usr/bin"
	if got := candidates(path, "/mnt", "codex"); !slices.Equal(got, []string{
		"/home/ada/.local/bin/codex", "/usr/bin/codex", "/mnt/wsl/bin/codex", "/mntc/bin/codex",
	}) {
		t.Fatal(got)
	}
	// Drives mount at the root when wsl.conf sets `root = /`.
	if got := candidates("/c/tools:/usr/bin:/", "", "pi"); !slices.Equal(got, []string{"/usr/bin/pi", "/pi"}) {
		t.Fatal(got)
	}
}

func TestProbeOutputSurvivesStartUpNoise(t *testing.T) {
	for _, tc := range []struct {
		output, home, path string
		ok                 bool
	}{
		{"__ASTRLINK_PATH__\n/usr/bin:/bin\nWelcome to Ubuntu\n__ASTRLINK_PATH__\n" +
			"/home/ada/.nvm/versions/node/v22.0.0/bin:/usr/bin\n__ASTRLINK_HOME__\n/home/ada\n" +
			"bash: no job control in this shell", "/home/ada", "/home/ada/.nvm/versions/node/v22.0.0/bin:/usr/bin", true},
		// A login shell that exits early leaves the plain PATH.
		{"__ASTRLINK_PATH__\n/usr/bin\n__ASTRLINK_PATH__\n__ASTRLINK_HOME__\n/root\n", "/root", "/usr/bin", true},
		{"__ASTRLINK_PATH__\r\n/usr/bin\r\n__ASTRLINK_HOME__\r\n/root\r\n", "/root", "/usr/bin", true},
		{"__ASTRLINK_PATH__\n/usr/bin\n", "", "", false},
	} {
		home, path, ok := parseProbe(tc.output)
		if home != tc.home || path != tc.path || ok != tc.ok {
			t.Errorf("%q: %q %q %v", tc.output, home, path, ok)
		}
	}
}

func TestLinuxPathsKeepForwardSlashesAfterJoins(t *testing.T) {
	prefix := "/home/ada/.nvm/versions/node/v22.0.0"
	if got := linux(filepath.Join(prefix, "lib", "node_modules", "npm", "bin", "npm-cli.js")); got != prefix+"/lib/node_modules/npm/bin/npm-cli.js" {
		t.Fatal(got)
	}
	if got := linux("/"); got != "/" {
		t.Fatal(got)
	}
	share := t.TempDir()
	w := &wslTarget{distribution: "Ubuntu", launcher: "wsl.exe", share: share, mountRoot: "/mnt"}
	if got, ok := w.shared(filepath.Join("/home/ada", ".claude", "settings.json")); !ok || got != filepath.Join(share, "home", "ada", ".claude", "settings.json") {
		t.Fatal(got, ok)
	}
	for _, path := range []string{"/home/../etc/shadow", "home/ada"} {
		if got, ok := w.shared(path); ok {
			t.Errorf("%s: %s", path, got)
		}
	}
}

func TestCommandsRunThroughWSLFromTheHomeWithTheLocalEnvironment(t *testing.T) {
	w := &wslTarget{distribution: "Ubuntu-24.04", launcher: "wsl.exe", share: "share", mountRoot: "/mnt"}
	env := &Environment{home: "/home/ada", path: "/home/ada/.local/bin:/mnt/c/Program Files/nodejs", wsl: w}
	spec := command(filepath.Join("/home/ada", ".local", "bin", "claude"), "update")
	wrapped := wslCommand(env, w, spec)
	if wrapped.program != "wsl.exe" {
		t.Fatal(wrapped.program)
	}
	if want := []string{"--distribution", "Ubuntu-24.04", "--cd", "~", "--exec", "env", "-u", "HTTP_PROXY"}; !slices.Equal(wrapped.args[:8], want) {
		t.Fatal(wrapped.args)
	}
	assignments := slices.IndexFunc(wrapped.args, func(arg string) bool { return strings.Contains(arg, "=") })
	if want := []string{
		"PATH=/home/ada/.local/bin:/mnt/c/Program Files/nodejs",
		"NO_COLOR=1",
		"CI=1",
		"DISABLE_AUTOUPDATER=1",
		"HOMEBREW_NO_INSTALL_CLEANUP=1",
		"NO_PROXY=*",
		"no_proxy=*",
		"npm_config_proxy=",
		"npm_config_https_proxy=",
		"/home/ada/.local/bin/claude",
		"update",
	}; !slices.Equal(wrapped.args[assignments:], want) {
		t.Fatal(wrapped.args[assignments:])
	}
	// With the system proxy, the distribution keeps its own proxy
	// settings.
	system := *env
	system.useSystemProxy, system.path = true, ""
	for _, arg := range wslCommand(&system, w, spec).args {
		if arg == "-u" || strings.HasPrefix(arg, "PATH=") {
			t.Fatal(arg)
		}
	}
}
