//go:build !darwin && !windows

package agents

import (
	"os"
	"os/exec"
)

func installedTerminals() []TerminalApp { return linuxInstalled(os.Getenv("PATH")) }

func platformTerminal(app TerminalApp, session *TerminalSession, dir string) (terminalLaunch, error) {
	path := os.Getenv("PATH")
	if _, err := linuxTerminalLaunch(app, path, ""); err != nil {
		return terminalLaunch{}, err
	}
	_, launch, err := writePOSIX(session, dir, userShell("/etc/passwd", os.Getuid()), nil)
	if err != nil {
		return terminalLaunch{}, err
	}
	return linuxTerminalLaunch(app, path, launch)
}

func hideWindow(*exec.Cmd) {}
