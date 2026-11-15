//go:build unix

package clients

import (
	"errors"
	"syscall"
)

// alive reports whether a process exists; signal 0 only checks.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
