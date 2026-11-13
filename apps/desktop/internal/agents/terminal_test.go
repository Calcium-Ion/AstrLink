package agents

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Nothing here starts a terminal or a client: the tests check the files a
// session writes and the command that would open it.

func terminalSession(client TerminalClient, model string) *TerminalSession {
	return &TerminalSession{
		Client:    client,
		Origin:    "http://127.0.0.1:18317",
		Token:     "astr_secret",
		ServiceID: "svc_claude_two",
		Model:     model,
		Directory: "/work/it's here",
		Banner:    "Claude Code here goes through \x1b]52;c;aGk=\x07Claude 2",
		Exited:    "Claude Code exited.",
	}
}

func TestClaudeSettingsRouteThroughTheProviderAndResetModels(t *testing.T) {
	defaults := claudeTerminalSettings(terminalSession(TerminalClaude, ""))
	env := defaults["env"].(map[string]any)
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:18317" || env["ANTHROPIC_AUTH_TOKEN"] != "astr_secret" ||
		env["ANTHROPIC_CUSTOM_HEADERS"] != "X-AstrLink-Service: svc_claude_two" {
		t.Fatal(env)
	}
	for _, key := range append(slices.Clone(claudeCleared), claudeModels...) {
		if value, ok := env[key]; !ok || value != "" {
			t.Errorf("%s = %v", key, value)
		}
	}
	// A blank model overrides one the user's settings name.
	if model, ok := defaults["model"]; !ok || model != "" {
		t.Fatal(model)
	}
	chosen := claudeTerminalSettings(terminalSession(TerminalClaude, "glm-5"))
	chosenEnv := chosen["env"].(map[string]any)
	for _, key := range claudeModels {
		if chosenEnv[key] != "glm-5" {
			t.Errorf("%s = %v", key, chosenEnv[key])
		}
	}
	if chosenEnv["CLAUDE_CODE_SUBAGENT_MODEL"] != "" || chosen["model"] != "glm-5" {
		t.Fatal(chosen)
	}
	// The file is pretty JSON with sorted keys, as serde_json wrote it.
	pretty := format.EncodePretty(chosen)
	if !strings.HasPrefix(pretty, "{\n  \"env\": {\n    \"ANTHROPIC_API_KEY\": \"\",\n") || !strings.HasSuffix(pretty, "\n  },\n  \"model\": \"glm-5\"\n}") {
		t.Fatal(pretty)
	}
}

func TestCodexArgsDefineAProviderOfTheirOwn(t *testing.T) {
	args := codexTerminalArgs(terminalSession(TerminalCodex, "gpt-5.5"))
	var overrides []string
	for i := 0; i+1 < len(args); i += 2 {
		if args[i] == "-c" {
			overrides = append(overrides, args[i+1])
		}
	}
	if want := []string{
		`model_provider="astrlink-terminal"`,
		`model_providers.astrlink-terminal.name="AstrLink"`,
		`model_providers.astrlink-terminal.base_url="http://127.0.0.1:18317/v1"`,
		`model_providers.astrlink-terminal.wire_api="responses"`,
		`model_providers.astrlink-terminal.env_key="ASTRLINK_TERMINAL_TOKEN"`,
		`model_providers.astrlink-terminal.http_headers.x-openai-actor-authorization="codex-imagegen"`,
		`model_providers.astrlink-terminal.http_headers.X-AstrLink-Service="svc_claude_two"`,
	}; !slices.Equal(overrides, want) {
		t.Fatal(overrides)
	}
	if !slices.Equal(args[len(args)-2:], []string{"-m", "gpt-5.5"}) {
		t.Fatal(args)
	}
	// The token never reaches the command line.
	if slices.ContainsFunc(args, func(arg string) bool { return strings.Contains(arg, "astr_secret") }) {
		t.Fatal("the token is an argument")
	}
	if slices.Contains(codexTerminalArgs(terminalSession(TerminalCodex, "")), "-m") {
		t.Fatal("a model without one chosen")
	}
}

func TestSessionScriptQuotesPathsAndStripsTerminalControls(t *testing.T) {
	dir := "/tmp/astrlink-terminal-ab"
	warp := "/home/u/.warp/tab_configs/astrlink_terminal_ab.toml"
	script := sessionScript(terminalSession(TerminalClaude, ""), dir, []string{warp})
	for _, want := range []string{
		"dir='/tmp/astrlink-terminal-ab'\n",
		"rm -f -- '/home/u/.warp/tab_configs/astrlink_terminal_ab.toml'\n",
		"cd '/work/it'\\''s here' || exit 1\n",
		"claude --settings \"$dir/settings.json\"\n",
		// The OSC 52 sequence in the name loses its escape and bell, so
		// the terminal prints it instead of writing the clipboard.
		"printf '%s\\n\\n' 'Claude Code here goes through ]52;c;aGk=Claude 2'\n",
		"printf '\\n%s\\n' 'Claude Code exited.'\n",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the script lacks %q:\n%s", want, script)
		}
	}
	if strings.ContainsAny(script, "\x1b\x07") {
		t.Fatal("control characters reached the script")
	}
	codex := sessionScript(terminalSession(TerminalCodex, "gpt-5.5"), dir, nil)
	if !strings.Contains(codex, `ASTRLINK_TERMINAL_TOKEN='astr_secret' codex '-c' 'model_provider="astrlink-terminal"'`) ||
		!strings.Contains(codex, " '-m' 'gpt-5.5'\n") {
		t.Fatal(codex)
	}
}

func TestLaunchScriptEntersTheLoginShell(t *testing.T) {
	script := launchScript("/bin/zsh", "/work/it's here", "/tmp/d/session.sh")
	// The shell left after the client exits starts in the directory too.
	if !strings.HasPrefix(script, "#!/bin/sh\ncd '/work/it'\\''s here'\nSHELL='/bin/zsh'\nASTRLINK_TERMINAL_SESSION='/tmp/d/session.sh'\n") ||
		!strings.Contains(script, `exec "$SHELL" -l -i -c '/bin/sh "$ASTRLINK_TERMINAL_SESSION"; exec "$SHELL" -l -i'`) {
		t.Fatal(script)
	}
}

func TestTypedCommandLeavesTheShellInTheDirectory(t *testing.T) {
	if got := typedCommand("/work/it's here", "/tmp/d/session.sh"); got != ` cd '/work/it'\''s here'; /bin/sh '/tmp/d/session.sh'` {
		t.Fatal(got)
	}
}

func TestPowerShellScriptQuotesAndCleansUp(t *testing.T) {
	script := powershellScript(terminalSession(TerminalCodex, ""), `C:\Temp\astrlink-terminal-ab`)
	for _, want := range []string{
		`$dir = 'C:\Temp\astrlink-terminal-ab'`,
		"Set-Location -LiteralPath '/work/it''s here'",
		"$env:ASTRLINK_TERMINAL_TOKEN = 'astr_secret'",
		`& codex '-c' 'model_provider="astrlink-terminal"'`,
		"Remove-Item -LiteralPath $dir -Recurse -Force",
		"Remove-Item Env:ASTRLINK_TERMINAL_TOKEN -ErrorAction SilentlyContinue",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the script lacks %q", want)
		}
	}
	if strings.Contains(script, "\x1b") {
		t.Fatal("control characters reached the script")
	}
	claude := powershellScript(terminalSession(TerminalClaude, ""), `C:\Temp\d`)
	if !strings.Contains(claude, "    & claude --settings (Join-Path $dir 'settings.json')\n") || strings.Contains(claude, "astr_secret") {
		t.Fatal(claude)
	}
}

func TestPowerShellQuotesHoldTypographicQuotes(t *testing.T) {
	if got := psQuote("Bob’s ‘key’"); got != "'Bob’’s ‘‘key’’'" {
		t.Fatal(got)
	}
	if got := psQuote("it's"); got != "'it''s'" {
		t.Fatal(got)
	}
}

func TestTerminalInputsAreChecked(t *testing.T) {
	for _, tc := range []struct {
		id string
		ok bool
	}{{"svc_claude-2", true}, {"Svc", false}, {"svc claude", false}, {"sv", false}, {"s" + strings.Repeat("v", 96), false}} {
		if ValidServiceID(tc.id) != tc.ok {
			t.Errorf("%q", tc.id)
		}
	}
	for _, tc := range []struct {
		token string
		ok    bool
	}{{"astr_AbC-_9", true}, {"astr_a'b", false}, {"sk_abc", false}, {"astr_" + strings.Repeat("a", 124), false}} {
		if ValidTerminalToken(tc.token) != tc.ok {
			t.Errorf("%q", tc.token)
		}
	}
	for _, tc := range []struct {
		model *string
		want  string
		ok    bool
	}{{nil, "", true}, {ptr("  "), "", true}, {ptr(" glm-5 "), "glm-5", true}, {ptr("a\nb"), "", false}, {ptr(strings.Repeat("m", 257)), "", false}} {
		got, err := ChosenModel(tc.model)
		if got != tc.want || (err == nil) != tc.ok {
			t.Errorf("%v: %q, %v", tc.model, got, err)
		}
	}
	var client TerminalClient
	if err := client.UnmarshalJSON([]byte(`"gemini"`)); err == nil {
		t.Fatal("an unknown client was accepted")
	}
	var app TerminalApp
	if err := app.UnmarshalJSON([]byte(`"gnome_terminal"`)); err != nil || app.Name() != "GNOME Terminal" {
		t.Fatal(app, err)
	}
	if err := app.UnmarshalJSON([]byte(`"xterm; rm"`)); err == nil {
		t.Fatal("an unknown app was accepted")
	}
}

func TestPrivateFilesBelongToTheUserAlone(t *testing.T) {
	parent := t.TempDir()
	dir, err := privateDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(filepath.Join(dir, "settings.json"), []byte("{}"), false); err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(filepath.Join(dir, "session.sh"), []byte("#!/bin/sh\n"), true); err != nil {
		t.Fatal(err)
	}
	// A file is never written over.
	if err := writePrivate(filepath.Join(dir, "session.sh"), []byte("other"), true); err == nil {
		t.Fatal("an existing file was replaced")
	}
	if runtime.GOOS != "windows" {
		mode := func(path string) os.FileMode {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			return info.Mode().Perm()
		}
		if got := []os.FileMode{mode(dir), mode(filepath.Join(dir, "settings.json")), mode(filepath.Join(dir, "session.sh"))}; !slices.Equal(got, []os.FileMode{0o700, 0o600, 0o700}) {
			t.Fatalf("modes = %v", got)
		}
	}
	// A second session never reuses a folder.
	other, err := privateDir(parent)
	if err != nil || other == dir || !strings.HasPrefix(filepath.Base(other), "astrlink-terminal-") {
		t.Fatal(other, err)
	}
}

func TestMacTerminalsTypeIntoTheShellOrRunTheLauncher(t *testing.T) {
	typed := typedCommand(`/work/"quoted"\dir`, "/tmp/d/session.sh")
	for _, app := range []TerminalApp{TerminalAppTerminal, TerminalAppITerm2} {
		launch, err := macTerminalLaunch(app, "/Applications/X.app", typed, "/tmp/d/launch.sh")
		if err != nil || launch.program != "/usr/bin/osascript" || len(launch.args) != 2 || launch.args[0] != "-e" || !launch.wait {
			t.Fatal(app, launch, err)
		}
		if !strings.HasPrefix(launch.args[1], `set command to " cd '/work/\"quoted\"\\dir'; /bin/sh '/tmp/d/session.sh'"`+"\n") {
			t.Fatal(launch.args[1])
		}
	}
	for _, tc := range []struct {
		app  TerminalApp
		args []string
	}{
		{TerminalAppGhostty, []string{"-na", "/Applications/X.app", "--args", "-e", "/bin/sh", "/tmp/d/launch.sh"}},
		{TerminalAppAlacritty, []string{"-na", "/Applications/X.app", "--args", "-e", "/bin/sh", "/tmp/d/launch.sh"}},
		{TerminalAppKitty, []string{"-na", "/Applications/X.app", "--args", "/bin/sh", "/tmp/d/launch.sh"}},
		{TerminalAppWezTerm, []string{"-na", "/Applications/X.app", "--args", "start", "--", "/bin/sh", "/tmp/d/launch.sh"}},
	} {
		launch, err := macTerminalLaunch(tc.app, "/Applications/X.app", typed, "/tmp/d/launch.sh")
		if err != nil || launch.program != "/usr/bin/open" || !slices.Equal(launch.args, tc.args) {
			t.Fatal(tc.app, launch, err)
		}
	}
	if _, err := macTerminalLaunch(TerminalAppKonsole, "", typed, ""); err == nil || err.Error() != "Konsole is not available on macOS" {
		t.Fatal(err)
	}
	name, contents := warpTabConfig("/tmp/astrlink-terminal-ab12", typed)
	if name != "astrlink_terminal_ab12" || contents != "name = \"AstrLink\"\n\n[[panes]]\nid = \"session\"\ntype = \"terminal\"\n"+
		`commands = [" cd '/work/\"quoted\"\\dir'; /bin/sh '/tmp/d/session.sh'"]`+"\n" {
		t.Fatal(name, contents)
	}
	parsed, err := format.ParseTOML(contents)
	if err != nil || parsed == nil {
		t.Fatal(err)
	}
}

func stubPrograms(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		mustWrite(t, filepath.Join(dir, name), "")
	}
	return dir
}

func TestLinuxTerminalsRunTheLauncherFromPath(t *testing.T) {
	path := stubPrograms(t, "konsole", "gnome-terminal", "wezterm") + string(os.PathListSeparator) + filepath.Join(t.TempDir(), "missing")
	if got := linuxInstalled(path); !slices.Equal(got, []TerminalApp{TerminalAppGnomeTerminal, TerminalAppKonsole, TerminalAppWezTerm}) {
		t.Fatal(got)
	}
	for _, tc := range []struct {
		app     TerminalApp
		program string
		args    []string
	}{
		{TerminalAppGnomeTerminal, "gnome-terminal", []string{"--", "/bin/sh", "/tmp/d/launch.sh"}},
		{TerminalAppKonsole, "konsole", []string{"-e", "/bin/sh", "/tmp/d/launch.sh"}},
		{TerminalAppWezTerm, "wezterm", []string{"start", "--", "/bin/sh", "/tmp/d/launch.sh"}},
	} {
		launch, err := linuxTerminalLaunch(tc.app, path, "/tmp/d/launch.sh")
		if err != nil || filepath.Base(launch.program) != tc.program || !slices.Equal(launch.args, tc.args) || launch.wait {
			t.Fatal(tc.app, launch, err)
		}
	}
	if _, err := linuxTerminalLaunch(TerminalAppKitty, path, ""); err == nil || err.Error() != "kitty is not installed" {
		t.Fatal(err)
	}
	if _, err := linuxTerminalLaunch(TerminalAppITerm2, path, ""); err == nil || err.Error() != "iTerm2 is not available on Linux" {
		t.Fatal(err)
	}
}

func TestWindowsTerminalsRunTheSessionScriptInPowerShell(t *testing.T) {
	powershell := []string{"powershell.exe", "-NoLogo", "-NoExit", "-ExecutionPolicy", "Bypass", "-File", `C:\T\session.ps1`}
	if got := windowsInstalled(stubPrograms(t)); !slices.Equal(got, []TerminalApp{TerminalAppPowerShell}) {
		t.Fatal(got)
	}
	path := stubPrograms(t, "wt.exe")
	if got := windowsInstalled(path); !slices.Equal(got, []TerminalApp{TerminalAppWindowsTerminal, TerminalAppPowerShell}) {
		t.Fatal(got)
	}
	wt, err := windowsTerminalLaunch(TerminalAppWindowsTerminal, path, `C:\T\session.ps1`)
	if err != nil || wt.program != filepath.Join(path, "wt.exe") || !slices.Equal(wt.args, append([]string{"-w", "0", "new-tab"}, powershell...)) || wt.wait {
		t.Fatal(wt, err)
	}
	start, err := windowsTerminalLaunch(TerminalAppPowerShell, path, `C:\T\session.ps1`)
	if err != nil || start.program != "cmd.exe" || !slices.Equal(start.args, append([]string{"/D", "/C", "start", ""}, powershell...)) || !start.wait || !start.hidden {
		t.Fatal(start, err)
	}
	if _, err := windowsTerminalLaunch(TerminalAppWarp, path, ""); err == nil {
		t.Fatal("Warp on Windows")
	}
}

func TestUserShellFallsBackToTheUserDatabase(t *testing.T) {
	shell := filepath.Join(t.TempDir(), "fish")
	mustWrite(t, shell, "")
	if err := os.Chmod(shell, 0o755); err != nil {
		t.Fatal(err)
	}
	passwd := filepath.Join(t.TempDir(), "passwd")
	mustWrite(t, passwd, "root:x:0:0:root:/root:/bin/sh\nada:x:1000:1000::/home/ada:"+shell+"\n")
	t.Setenv("SHELL", "relative/zsh")
	if runtime.GOOS != "windows" {
		if got := userShell(passwd, 1000); got != shell {
			t.Fatal(got)
		}
	}
	if got := userShell(passwd, 1001); got != "/bin/sh" {
		t.Fatal(got)
	}
	if runtime.GOOS != "windows" {
		t.Setenv("SHELL", shell)
		if got := userShell(filepath.Join(t.TempDir(), "missing"), 0); got != shell {
			t.Fatal(got)
		}
	}
}
