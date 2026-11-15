//go:build !windows

package core

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func configureCommand(*exec.Cmd) {}

func describeExit(state *os.ProcessState) string {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return fmt.Sprintf("astrlink-core exited after signal %d", int(status.Signal()))
	}
	if code := state.ExitCode(); code >= 0 {
		return fmt.Sprintf("astrlink-core exited with code %d", code)
	}
	return "astrlink-core exited unexpectedly"
}
