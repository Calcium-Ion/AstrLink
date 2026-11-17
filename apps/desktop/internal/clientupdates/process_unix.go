//go:build !windows

package clientupdates

import (
	"os"
	"os/exec"
	"syscall"
)

// configure starts the command in a session of its own: it leads its own
// process group, and an interactive login shell finds no terminal to stop
// on when the app was started from one.
func configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// contain returns what kills the process and everything it started.
func contain(process *os.Process) (func(), error) {
	pid := process.Pid
	return func() { _ = syscall.Kill(-pid, syscall.SIGKILL) }, nil
}

// checkProgram is Windows only.
func checkProgram(string) *clientError { return nil }
