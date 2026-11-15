//go:build !windows

package agentcli

import (
	"errors"
	"syscall"
)

// pidAlive reports whether a process with pid exists; a process another
// user owns counts.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// refused reports a connection nothing was listening for.
func refused(err error) bool { return errors.Is(err, syscall.ECONNREFUSED) }
