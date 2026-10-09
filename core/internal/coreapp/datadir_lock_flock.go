//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package coreapp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// LockDataDirectory takes directory for this process, before anything in it
// is opened or replaced. The kernel drops the lock when the process exits,
// so a crash never leaves it held. Keep the closer until shutdown.
func LockDataDirectory(directory string) (io.Closer, error) {
	file, err := os.OpenFile(filepath.Join(directory, dataDirectoryLockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open data directory lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s", ErrDataDirectoryInUse, directory)
		}
		return nil, fmt.Errorf("lock data directory: %w", err)
	}
	return file, nil
}
