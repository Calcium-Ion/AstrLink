package agents

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

var macTerminals = []struct {
	app    TerminalApp
	bundle string
}{
	{TerminalAppTerminal, "Terminal.app"},
	{TerminalAppITerm2, "iTerm.app"},
	{TerminalAppWarp, "Warp.app"},
	{TerminalAppGhostty, "Ghostty.app"},
	{TerminalAppWezTerm, "WezTerm.app"},
	{TerminalAppKitty, "kitty.app"},
	{TerminalAppAlacritty, "Alacritty.app"},
}

// realHome is the user's own home, where terminal apps keep their files.
// An isolated AstrLink keeps its clients in a home of its own instead.
func realHome() (string, error) {
	if home := os.Getenv("HOME"); home != "" {
		return home, nil
	}
	return "", errors.New("unable to resolve the user home directory")
}

func macBundle(app TerminalApp) (string, bool) {
	for _, terminal := range macTerminals {
		if terminal.app != app {
			continue
		}
		places := []string{"/Applications", "/System/Applications/Utilities", "/Applications/Utilities"}
		if home, err := realHome(); err == nil {
			places = append(places, filepath.Join(home, "Applications"))
		}
		for _, place := range places {
			if path := filepath.Join(place, terminal.bundle); isDir(path) {
				return path, true
			}
		}
	}
	return "", false
}

func installedTerminals() []TerminalApp {
	var apps []TerminalApp
	for _, terminal := range macTerminals {
		if _, ok := macBundle(terminal.app); ok {
			apps = append(apps, terminal.app)
		}
	}
	return apps
}

func platformTerminal(app TerminalApp, session *TerminalSession, dir string) (terminalLaunch, error) {
	// Warp runs a saved tab config, which the session removes once it
	// runs.
	var warpConfig, warpName string
	var cleanup []string
	if app == TerminalAppWarp {
		home, err := realHome()
		if err != nil {
			return terminalLaunch{}, err
		}
		configs := filepath.Join(home, ".warp", "tab_configs")
		if err := os.MkdirAll(configs, 0o755); err != nil {
			return terminalLaunch{}, err
		}
		warpName, _ = warpTabConfig(dir, "")
		warpConfig = filepath.Join(configs, warpName+".toml")
		cleanup = []string{warpConfig}
	}
	sessionPath, launchPath, err := writePOSIX(session, dir, userShell("/etc/passwd", os.Getuid()), cleanup)
	if err != nil {
		return terminalLaunch{}, err
	}
	typed := typedCommand(session.Directory, sessionPath)
	bundle, ok := macBundle(app)
	if !ok {
		return terminalLaunch{}, errors.New(app.Name() + " is not installed")
	}
	if app == TerminalAppWarp {
		_, contents := warpTabConfig(dir, typed)
		if err := writePrivate(warpConfig, []byte(contents), false); err != nil {
			return terminalLaunch{}, err
		}
		return terminalLaunch{
			program: "/usr/bin/open",
			args:    []string{"warp://tab_config/" + warpName},
			wait:    true,
			undo:    func() { _ = os.Remove(warpConfig) },
		}, nil
	}
	return macTerminalLaunch(app, bundle, typed, launchPath)
}

func hideWindow(*exec.Cmd) {}
