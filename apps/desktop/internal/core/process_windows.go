package core

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// configureCommand keeps Core from opening a console window of its own.
func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}

func describeExit(state *os.ProcessState) string {
	return fmt.Sprintf("astrlink-core exited with code %d", state.ExitCode())
}
