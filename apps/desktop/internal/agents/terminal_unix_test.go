//go:build !darwin && !windows

package agents

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPreparingASessionWritesItsFilesWithoutStartingAnything(t *testing.T) {
	path := stubPrograms(t, "x-terminal-emulator")
	t.Setenv("PATH", path)
	t.Setenv("SHELL", "/bin/sh")
	if got := InstalledTerminals(); len(got) != 1 || got[0] != (TerminalChoice{TerminalAppXTerminalEmulator, "x-terminal-emulator"}) {
		t.Fatal(got)
	}
	for _, client := range []TerminalClient{TerminalClaude, TerminalCodex} {
		dir := t.TempDir()
		session := terminalSession(client, "glm-5")
		launch, err := prepareTerminal(TerminalAppXTerminalEmulator, session, dir)
		if err != nil {
			t.Fatal(err)
		}
		launcher := filepath.Join(dir, "launch.sh")
		if launch.program != filepath.Join(path, "x-terminal-emulator") || !slices.Equal(launch.args, []string{"-e", "/bin/sh", launcher}) {
			t.Fatal(launch)
		}
		if got := mustRead(t, launcher); got != launchScript("/bin/sh", session.Directory, filepath.Join(dir, "session.sh")) {
			t.Fatal(got)
		}
		script := mustRead(t, filepath.Join(dir, "session.sh"))
		if script != sessionScript(session, dir, nil) {
			t.Fatal(script)
		}
		settings := filepath.Join(dir, "settings.json")
		if client == TerminalCodex {
			if pathExists(settings) {
				t.Fatal("Codex got Claude Code's settings")
			}
			continue
		}
		info, err := os.Stat(settings)
		if err != nil || info.Mode().Perm() != 0o600 || !strings.Contains(mustRead(t, settings), `"ANTHROPIC_AUTH_TOKEN": "astr_secret"`) {
			t.Fatal(info, err)
		}
	}
	if _, err := prepareTerminal(TerminalAppKonsole, terminalSession(TerminalCodex, ""), t.TempDir()); err == nil || err.Error() != "Konsole is not installed" {
		t.Fatal(err)
	}
	if err := OpenTerminal(TerminalAppKitty, terminalSession(TerminalCodex, "")); err == nil || err.Error() != "kitty is not installed" {
		t.Fatal(err)
	}
}
