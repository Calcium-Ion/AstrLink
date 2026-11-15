//go:build !windows

package clientupdates

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// openClaim creates file naming this process; nil while a live process
// holds it. Other systems update clients in place and run this only in
// tests. A lock would pass to every child forked while it is held, so the
// claim names the process holding it instead.
func openClaim(file string) (*os.File, error) {
	for range 2 {
		handle, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			if _, err := fmt.Fprint(handle, os.Getpid()); err != nil {
				handle.Close()
				return nil, err
			}
			return handle, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		data, _ := os.ReadFile(file)
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			// Signal 0 only checks that the process exists.
			if err := syscall.Kill(pid, 0); err == nil || errors.Is(err, syscall.EPERM) {
				return nil, nil
			}
		}
		_ = os.Remove(file)
	}
	return nil, nil
}

// releaseClaim closes and removes the claim.
func releaseClaim(handle *os.File, file string) {
	_ = handle.Close()
	_ = os.Remove(file)
}

// renameBusy is Windows only.
func renameBusy(error) bool { return false }
