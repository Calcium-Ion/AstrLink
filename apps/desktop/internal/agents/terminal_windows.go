package agents

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

func installedTerminals() []TerminalApp { return windowsInstalled(os.Getenv("PATH")) }

func platformTerminal(app TerminalApp, session *TerminalSession, dir string) (terminalLaunch, error) {
	script := filepath.Join(dir, "session.ps1")
	// A byte order mark makes Windows PowerShell read the file as UTF-8.
	contents := append([]byte{0xEF, 0xBB, 0xBF}, powershellScript(session, dir)...)
	if err := writePrivate(script, contents, false); err != nil {
		return terminalLaunch{}, err
	}
	return windowsTerminalLaunch(app, os.Getenv("PATH"), script)
}

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}
