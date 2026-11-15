package agents

// Opens a terminal in which Claude Code or Codex reaches the gateway
// through one API provider. The client starts with settings for that
// session only, so the user's own client configs stay as they are and
// other terminals keep routing as usual.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

const (
	// serviceHeader is the header Core routes a request to one provider
	// by: ingress.ServiceHeader. Core keeps it local.
	serviceHeader = "X-AstrLink-Service"
	// codexTerminalProvider is the provider Codex reads this session's
	// connection from, so the token config.toml holds for the usual
	// provider cannot win.
	codexTerminalProvider = "astrlink-terminal"
	// codexTokenVariable keeps the token off the command line. Codex
	// leaves variables named like tokens out of the commands it runs.
	codexTokenVariable = "ASTRLINK_TERMINAL_TOKEN"
	// Codex shows its own image generation and web search tools only for
	// a provider that sends this header; the gateway answers both tools.
	codexToolsHeader      = "x-openai-actor-authorization"
	codexToolsHeaderValue = "codex-imagegen"
)

// claudeCleared would send Claude Code elsewhere or under another login. A
// blank value counts as unset.
var claudeCleared = []string{
	"ANTHROPIC_API_KEY",
	"CLAUDE_CODE_OAUTH_TOKEN",
	"CLAUDE_CODE_USE_BEDROCK",
	"CLAUDE_CODE_USE_VERTEX",
	"CLAUDE_CODE_USE_FOUNDRY",
}

// claudeModels are Claude Code's model variables. A chosen model fills
// every one, so background tasks stay on the provider too; blanks fall
// back to Claude Code's own models.
var claudeModels = []string{
	"ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
	"ANTHROPIC_DEFAULT_FABLE_MODEL",
	"ANTHROPIC_SMALL_FAST_MODEL",
}

// TerminalClient is the client a terminal session starts.
type TerminalClient string

const (
	TerminalClaude TerminalClient = "claude"
	TerminalCodex  TerminalClient = "codex"
)

// UnmarshalJSON accepts only the two clients.
func (c *TerminalClient) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	if name != string(TerminalClaude) && name != string(TerminalCodex) {
		return fmt.Errorf("unknown terminal client %q", name)
	}
	*c = TerminalClient(name)
	return nil
}

// Label is the client's name in the terminal.
func (c TerminalClient) Label() string {
	if c == TerminalClaude {
		return "Claude Code"
	}
	return "Codex"
}

// TerminalApp is a terminal app a session can open in.
type TerminalApp string

const (
	TerminalAppTerminal          TerminalApp = "terminal"
	TerminalAppITerm2            TerminalApp = "iterm2"
	TerminalAppWarp              TerminalApp = "warp"
	TerminalAppGhostty           TerminalApp = "ghostty"
	TerminalAppWezTerm           TerminalApp = "wezterm"
	TerminalAppKitty             TerminalApp = "kitty"
	TerminalAppAlacritty         TerminalApp = "alacritty"
	TerminalAppGnomeTerminal     TerminalApp = "gnome_terminal"
	TerminalAppKonsole           TerminalApp = "konsole"
	TerminalAppXfce4Terminal     TerminalApp = "xfce4_terminal"
	TerminalAppXTerminalEmulator TerminalApp = "x_terminal_emulator"
	TerminalAppWindowsTerminal   TerminalApp = "windows_terminal"
	TerminalAppPowerShell        TerminalApp = "powershell"
)

var terminalNames = map[TerminalApp]string{
	TerminalAppTerminal:          "Terminal",
	TerminalAppITerm2:            "iTerm2",
	TerminalAppWarp:              "Warp",
	TerminalAppGhostty:           "Ghostty",
	TerminalAppWezTerm:           "WezTerm",
	TerminalAppKitty:             "kitty",
	TerminalAppAlacritty:         "Alacritty",
	TerminalAppGnomeTerminal:     "GNOME Terminal",
	TerminalAppKonsole:           "Konsole",
	TerminalAppXfce4Terminal:     "Xfce Terminal",
	TerminalAppXTerminalEmulator: "x-terminal-emulator",
	TerminalAppWindowsTerminal:   "Windows Terminal",
	TerminalAppPowerShell:        "PowerShell",
}

// UnmarshalJSON accepts only the known apps.
func (a *TerminalApp) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	if _, ok := terminalNames[TerminalApp(name)]; !ok {
		return fmt.Errorf("unknown terminal app %q", name)
	}
	*a = TerminalApp(name)
	return nil
}

// Name is the app's name as the user knows it.
func (a TerminalApp) Name() string { return terminalNames[a] }

// TerminalChoice is a terminal app the dialog offers.
type TerminalChoice struct {
	ID   TerminalApp `json:"id"`
	Name string      `json:"name"`
}

// TerminalSession is one session's connection and the text the terminal
// shows around it.
type TerminalSession struct {
	Client TerminalClient
	// Origin is the gateway's root, such as http://127.0.0.1:18317.
	Origin    string
	Token     string
	ServiceID string
	// Model is "" for the client's own.
	Model     string
	Directory string
	Banner    string
	Exited    string
}

// shQuote is POSIX single quotes; a quote inside closes, escapes and
// reopens them.
func shQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// psQuote is PowerShell single quotes, which double a quote inside.
// PowerShell reads the typographic single quotes as quotes too, such as
// one in "Bob’s".
func psQuote(value string) string {
	var quoted strings.Builder
	quoted.WriteByte('\'')
	for _, c := range value {
		switch c {
		case '\'', '‘', '’', '‚', '‛':
			quoted.WriteRune(c)
		}
		quoted.WriteRune(c)
	}
	quoted.WriteByte('\'')
	return quoted.String()
}

// printable is text shown in the terminal. A provider name is the
// user's, and control characters in it could drive the terminal, such as
// writing the clipboard.
func printable(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
}

// ValidServiceID reports a provider id as Core makes them.
func ValidServiceID(id string) bool {
	if len(id) < 3 || len(id) > 96 || id[0] < 'a' || id[0] > 'z' {
		return false
	}
	for i := 1; i < len(id); i++ {
		if c := id[i]; !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// ValidTerminalToken reports an access token that can go into a script
// unchanged.
func ValidTerminalToken(token string) bool {
	rest, ok := strings.CutPrefix(token, "astr_")
	if !ok || len(token) > 128 {
		return false
	}
	for i := 0; i < len(rest); i++ {
		if c := rest[i]; !(isASCIIAlpha(c) || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// ChosenModel is a model name as Codex's -m and Claude Code's settings
// take it; "" keeps the client's own.
func ChosenModel(model *string) (string, error) {
	if model == nil {
		return "", nil
	}
	chosen := strings.TrimFunc(*model, isRustWhitespace)
	if utf8.RuneCountInString(chosen) > 256 || strings.ContainsFunc(chosen, unicode.IsControl) {
		return "", errors.New("invalid model name")
	}
	return chosen, nil
}

// claudeTerminalSettings is the settings file Claude Code starts with. It
// sits above the user's and project settings, so it fixes everything that
// routes a request. Keys it leaves out still come from the user's
// settings.
func claudeTerminalSettings(session *TerminalSession) map[string]any {
	env := map[string]any{
		"ANTHROPIC_BASE_URL": session.Origin,
		// AUTH_TOKEN, not API_KEY: an API key asks for approval first.
		"ANTHROPIC_AUTH_TOKEN":     session.Token,
		"ANTHROPIC_CUSTOM_HEADERS": serviceHeader + ": " + session.ServiceID,
	}
	for _, key := range claudeCleared {
		env[key] = ""
	}
	for _, key := range claudeModels {
		env[key] = session.Model
	}
	// Subagents then follow the models above.
	env["CLAUDE_CODE_SUBAGENT_MODEL"] = ""
	// A blank model also overrides one the user's settings name.
	return map[string]any{"env": env, "model": session.Model}
}

// codexTerminalArgs are Codex's flags for this session. Values stay plain
// words without spaces, which Codex takes as text when they are not TOML,
// so a shell that strips the quotes passes them unchanged.
func codexTerminalArgs(session *TerminalSession) []string {
	provider := "model_providers." + codexTerminalProvider
	var args []string
	for _, value := range []string{
		`model_provider="` + codexTerminalProvider + `"`,
		// The display name is local UI.
		provider + `.name="AstrLink"`,
		provider + `.base_url="` + session.Origin + `/v1"`,
		provider + `.wire_api="responses"`,
		provider + `.env_key="` + codexTokenVariable + `"`,
		provider + ".http_headers." + codexToolsHeader + `="` + codexToolsHeaderValue + `"`,
		provider + ".http_headers." + serviceHeader + `="` + session.ServiceID + `"`,
	} {
		args = append(args, "-c", value)
	}
	if session.Model != "" {
		args = append(args, "-m", session.Model)
	}
	return args
}

// sessionScript runs one session in dir: it starts the client in the
// chosen directory and removes dir once the client exits or the window
// closes. A Ctrl-C belongs to the client, so the script outlives it.
func sessionScript(session *TerminalSession, dir string, cleanup []string) string {
	var script strings.Builder
	script.WriteString("#!/bin/sh\n" +
		"# AstrLink opened this terminal for one session. This folder is removed\n" +
		"# when the client exits.\n")
	script.WriteString("dir=" + shQuote(dir) + "\n")
	script.WriteString("rm -f -- \"$0\"\n" +
		"trap 'rm -rf \"$dir\"' EXIT\n" +
		"trap 'exit 129' HUP\n" +
		"trap 'exit 143' TERM\n" +
		"trap ':' INT QUIT\n")
	for _, path := range cleanup {
		script.WriteString("rm -f -- " + shQuote(path) + "\n")
	}
	script.WriteString("cd " + shQuote(session.Directory) + " || exit 1\n")
	script.WriteString(`printf '%s\n\n' ` + shQuote(printable(session.Banner)) + "\n")
	switch session.Client {
	case TerminalClaude:
		script.WriteString("claude --settings \"$dir/settings.json\"\n")
	case TerminalCodex:
		var args []string
		for _, arg := range codexTerminalArgs(session) {
			args = append(args, shQuote(arg))
		}
		script.WriteString(codexTokenVariable + "=" + shQuote(session.Token) + " codex " + strings.Join(args, " ") + "\n")
	}
	script.WriteString(`printf '\n%s\n' ` + shQuote(printable(session.Exited)) + "\n")
	return script.String()
}

// launchScript runs the session inside the user's login shell, which sets
// up PATH as a new window would, then stays in that shell in the
// session's directory. Shells outside the list run the session from
// /bin/sh instead.
func launchScript(shell, directory, sessionPath string) string {
	return "#!/bin/sh\n" +
		"cd " + shQuote(directory) + "\n" +
		"SHELL=" + shQuote(shell) + "\n" +
		"ASTRLINK_TERMINAL_SESSION=" + shQuote(sessionPath) + "\n" +
		"export SHELL ASTRLINK_TERMINAL_SESSION\n" +
		"case \"${SHELL##*/}\" in\n" +
		"bash | zsh | fish | ksh | mksh)\n" +
		"\texec \"$SHELL\" -l -i -c '/bin/sh \"$ASTRLINK_TERMINAL_SESSION\"; exec \"$SHELL\" -l -i'\n" +
		"\t;;\n" +
		"esac\n" +
		"/bin/sh \"$ASTRLINK_TERMINAL_SESSION\"\n" +
		"exec \"$SHELL\"\n"
}

// typedCommand is what a terminal types into the user's own shell, which
// already set up the environment and stays, in the session's directory,
// once the client exits. The leading space keeps the line out of most
// shells' history.
func typedCommand(directory, sessionPath string) string {
	return " cd " + shQuote(directory) + "; /bin/sh " + shQuote(sessionPath)
}

// powershellScript is the Windows session: PowerShell reloads PATH, since
// the terminal inherits the one AstrLink started with, and removes dir
// once the client exits.
func powershellScript(session *TerminalSession, dir string) string {
	var script strings.Builder
	script.WriteString("# AstrLink opened this terminal for one session. This folder is removed\n" +
		"# when the client exits.\n")
	script.WriteString("$dir = " + psQuote(dir) + "\n" +
		"Remove-Item -LiteralPath $PSCommandPath -Force -ErrorAction SilentlyContinue\n" +
		"$env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User')\n" +
		"Set-Location -LiteralPath " + psQuote(session.Directory) + "\n" +
		"Write-Host " + psQuote(printable(session.Banner)) + "\n" +
		"Write-Host ''\n" +
		"try {\n")
	switch session.Client {
	case TerminalClaude:
		script.WriteString("    & claude --settings (Join-Path $dir 'settings.json')\n")
	case TerminalCodex:
		var args []string
		for _, arg := range codexTerminalArgs(session) {
			args = append(args, psQuote(arg))
		}
		script.WriteString("    $env:" + codexTokenVariable + " = " + psQuote(session.Token) + "\n" +
			"    & codex " + strings.Join(args, " ") + "\n")
	}
	script.WriteString("} finally {\n" +
		"    Remove-Item Env:" + codexTokenVariable + " -ErrorAction SilentlyContinue\n" +
		"    Remove-Item -LiteralPath $dir -Recurse -Force -ErrorAction SilentlyContinue\n" +
		"}\n" +
		"Write-Host ''\n" +
		"Write-Host " + psQuote(printable(session.Exited)) + "\n")
	return script.String()
}

// privateDir makes a new folder in parent only this user can read, for
// one session's files.
func privateDir(parent string) (string, error) {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	dir := filepath.Join(parent, "astrlink-terminal-"+hex.EncodeToString(random[:]))
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", fmt.Errorf("unable to create %s: %w", dir, err)
	}
	return dir, nil
}

// writePrivate writes a new file only this user can read, and with
// executable set, run.
func writePrivate(path string, contents []byte, executable bool) error {
	mode := os.FileMode(0o600)
	if executable {
		mode = 0o700
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err == nil {
		_, err = file.Write(contents)
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		return fmt.Errorf("unable to write %s: %w", path, err)
	}
	return nil
}

// terminalLaunch is how a terminal app is started for a session.
type terminalLaunch struct {
	program string
	args    []string
	// wait runs the program to its end and reports its error output;
	// otherwise it is started and reaped in the background, since some
	// terminals stay in the foreground until their window closes.
	wait bool
	// hidden starts it without a console window (Windows).
	hidden bool
	// undo removes what the launch needs if it fails.
	undo func()
}

// InstalledTerminals is the terminal apps installed here, the platform's
// own first.
func InstalledTerminals() []TerminalChoice {
	choices := []TerminalChoice{}
	for _, app := range installedTerminals() {
		choices = append(choices, TerminalChoice{ID: app, Name: app.Name()})
	}
	return choices
}

// OpenTerminal writes the session's files and opens app on them. The
// files go away if the terminal cannot be opened.
func OpenTerminal(app TerminalApp, session *TerminalSession) error {
	if !slices.Contains(installedTerminals(), app) {
		return fmt.Errorf("%s is not installed", app.Name())
	}
	dir, err := privateDir(os.TempDir())
	if err != nil {
		return err
	}
	launch, err := prepareTerminal(app, session, dir)
	if err == nil {
		err = startTerminal(app, launch)
		if err != nil && launch.undo != nil {
			launch.undo()
		}
	}
	if err != nil {
		_ = os.RemoveAll(dir)
	}
	return err
}

// startTerminal starts a prepared launch.
func startTerminal(app TerminalApp, launch terminalLaunch) error {
	cmd := exec.Command(launch.program, launch.args...)
	if launch.hidden {
		hideWindow(cmd)
	}
	if !launch.wait {
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("unable to start %s: %w", app.Name(), err)
		}
		go func() { _ = cmd.Wait() }()
		return nil
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil
	case !errors.As(err, &exit):
		return fmt.Errorf("unable to start %s: %w", app.Name(), err)
	}
	if detail := strings.TrimSpace(strings.ToValidUTF8(stderr.String(), "\uFFFD")); detail != "" {
		return fmt.Errorf("%s failed: %s", app.Name(), detail)
	}
	return fmt.Errorf("%s failed with %s", app.Name(), exit.ProcessState)
}

// prepareTerminal writes the session's files into dir and returns how to
// start app on them.
func prepareTerminal(app TerminalApp, session *TerminalSession, dir string) (terminalLaunch, error) {
	if session.Client == TerminalClaude {
		settings := format.EncodePretty(claudeTerminalSettings(session))
		if err := writePrivate(filepath.Join(dir, "settings.json"), []byte(settings), false); err != nil {
			return terminalLaunch{}, err
		}
	}
	return platformTerminal(app, session, dir)
}

// writePOSIX writes the POSIX scripts and returns the session script's
// path and the launcher's.
func writePOSIX(session *TerminalSession, dir, shell string, cleanup []string) (sessionPath, launchPath string, err error) {
	sessionPath = filepath.Join(dir, "session.sh")
	if err = writePrivate(sessionPath, []byte(sessionScript(session, dir, cleanup)), true); err != nil {
		return "", "", err
	}
	launchPath = filepath.Join(dir, "launch.sh")
	if err = writePrivate(launchPath, []byte(launchScript(shell, session.Directory, sessionPath)), true); err != nil {
		return "", "", err
	}
	return sessionPath, launchPath, nil
}

// usableShell reports an absolute path to a file the user may run.
func usableShell(path string) bool {
	info, err := os.Stat(path)
	return filepath.IsAbs(path) && err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// userShell is $SHELL, else the login shell the user database names, else
// /bin/sh.
func userShell(passwd string, uid int) string {
	if shell := os.Getenv("SHELL"); usableShell(shell) {
		return shell
	}
	if data, err := os.ReadFile(passwd); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Split(line, ":")
			if len(fields) == 7 && fields[2] == fmt.Sprint(uid) && usableShell(fields[6]) {
				return fields[6]
			}
		}
	}
	return "/bin/sh"
}

// terminalOnPath is the first file named name in a directory of path.
func terminalOnPath(path, name string) (string, bool) {
	for _, dir := range filepath.SplitList(path) {
		if file := filepath.Join(dir, name); isFile(file) {
			return file, true
		}
	}
	return "", false
}

// applescriptString is value as an AppleScript string literal.
func applescriptString(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

// macTerminalLaunch starts app, found at bundle, on the session: through
// AppleScript, which types into the user's own shell, or with the
// launcher as the app's program.
func macTerminalLaunch(app TerminalApp, bundle, typed, launch string) (terminalLaunch, error) {
	osascript := func(script string) terminalLaunch {
		return terminalLaunch{program: "/usr/bin/osascript", args: []string{"-e", script}, wait: true}
	}
	open := func(args ...string) terminalLaunch {
		return terminalLaunch{program: "/usr/bin/open", args: append([]string{"-na", bundle, "--args"}, args...), wait: true}
	}
	switch app {
	case TerminalAppTerminal:
		// A cold `activate` opens a window of its own first.
		return osascript("set command to " + applescriptString(typed) + "\n" +
			"set wasRunning to application \"Terminal\" is running\n" +
			"tell application \"Terminal\"\n" +
			"\tif not wasRunning then launch\n" +
			"\tdo script command\n" +
			"\tactivate\n" +
			"end tell"), nil
	case TerminalAppITerm2:
		// A cold start opens a window; the session goes there.
		return osascript("set command to " + applescriptString(typed) + "\n" +
			"set wasRunning to application \"iTerm\" is running\n" +
			"tell application \"iTerm\"\n" +
			"\tactivate\n" +
			"\tif wasRunning and (count of windows) > 0 then\n" +
			"\t\ttell current window to create tab with default profile\n" +
			"\telse\n" +
			"\t\trepeat 30 times\n" +
			"\t\t\tif (count of windows) > 0 then exit repeat\n" +
			"\t\t\tdelay 0.1\n" +
			"\t\tend repeat\n" +
			"\t\tif (count of windows) = 0 then create window with default profile\n" +
			"\tend if\n" +
			"\ttell current session of current window to write text command\n" +
			"end tell"), nil
	// Ghostty, kitty and Alacritty run the script as their program and
	// quit with the last window, so each session gets its own process.
	case TerminalAppGhostty, TerminalAppAlacritty:
		return open("-e", "/bin/sh", launch), nil
	case TerminalAppKitty:
		return open("/bin/sh", launch), nil
	case TerminalAppWezTerm:
		return open("start", "--", "/bin/sh", launch), nil
	}
	return terminalLaunch{}, fmt.Errorf("%s is not available on macOS", app.Name())
}

// warpTabConfig is the saved tab config Warp runs for a session in dir:
// its name and contents. The session removes it once it runs.
func warpTabConfig(dir, typed string) (name, contents string) {
	name = "astrlink_terminal_" + strings.TrimPrefix(filepath.Base(dir), "astrlink-terminal-")
	contents = "name = \"AstrLink\"\n\n" +
		"[[panes]]\n" +
		"id = \"session\"\n" +
		"type = \"terminal\"\n" +
		"commands = [" + tomlBasicString(typed) + "]\n"
	return name, contents
}

// tomlBasicString is value as a TOML basic string.
func tomlBasicString(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		switch {
		case r == '"':
			out.WriteString(`\"`)
		case r == '\\':
			out.WriteString(`\\`)
		case r == '\n':
			out.WriteString(`\n`)
		case r == '\t':
			out.WriteString(`\t`)
		case r == '\r':
			out.WriteString(`\r`)
		case r == '\b':
			out.WriteString(`\b`)
		case r == '\f':
			out.WriteString(`\f`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&out, `\u%04X`, r)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}

// linuxTerminals are each Linux app, its program, and the arguments that
// make it run one.
var linuxTerminals = []struct {
	app     TerminalApp
	program string
	args    []string
}{
	{TerminalAppXTerminalEmulator, "x-terminal-emulator", []string{"-e"}},
	{TerminalAppGnomeTerminal, "gnome-terminal", []string{"--"}},
	{TerminalAppKonsole, "konsole", []string{"-e"}},
	{TerminalAppXfce4Terminal, "xfce4-terminal", []string{"-x"}},
	{TerminalAppKitty, "kitty", nil},
	{TerminalAppAlacritty, "alacritty", []string{"-e"}},
	{TerminalAppWezTerm, "wezterm", []string{"start", "--"}},
	{TerminalAppGhostty, "ghostty", []string{"-e"}},
}

func linuxInstalled(path string) []TerminalApp {
	var apps []TerminalApp
	for _, terminal := range linuxTerminals {
		if _, ok := terminalOnPath(path, terminal.program); ok {
			apps = append(apps, terminal.app)
		}
	}
	return apps
}

// linuxTerminalLaunch starts app from path on the launcher.
func linuxTerminalLaunch(app TerminalApp, path, launch string) (terminalLaunch, error) {
	for _, terminal := range linuxTerminals {
		if terminal.app != app {
			continue
		}
		program, ok := terminalOnPath(path, terminal.program)
		if !ok {
			return terminalLaunch{}, fmt.Errorf("%s is not installed", app.Name())
		}
		args := append(slices.Clone(terminal.args), "/bin/sh", launch)
		return terminalLaunch{program: program, args: args}, nil
	}
	return terminalLaunch{}, fmt.Errorf("%s is not available on Linux", app.Name())
}

func windowsInstalled(path string) []TerminalApp {
	var apps []TerminalApp
	if _, ok := terminalOnPath(path, "wt.exe"); ok {
		apps = append(apps, TerminalAppWindowsTerminal)
	}
	return append(apps, TerminalAppPowerShell)
}

// windowsTerminalLaunch starts app from path on the PowerShell script.
func windowsTerminalLaunch(app TerminalApp, path, script string) (terminalLaunch, error) {
	powershell := []string{"powershell.exe", "-NoLogo", "-NoExit", "-ExecutionPolicy", "Bypass", "-File", script}
	switch app {
	case TerminalAppWindowsTerminal:
		program, ok := terminalOnPath(path, "wt.exe")
		if !ok {
			return terminalLaunch{}, errors.New("Windows Terminal is not installed")
		}
		// `wt` may stay until the tab hands off; it is reaped whenever it
		// ends.
		return terminalLaunch{program: program, args: append([]string{"-w", "0", "new-tab"}, powershell...)}, nil
	case TerminalAppPowerShell:
		// `start` gives PowerShell a console window of its own. Started
		// straight from this windowless app, it would get none to use.
		// The blank argument is the window title `start` reads first.
		return terminalLaunch{program: "cmd.exe", args: append([]string{"/D", "/C", "start", ""}, powershell...), wait: true, hidden: true}, nil
	}
	return terminalLaunch{}, fmt.Errorf("%s is not available on Windows", app.Name())
}
