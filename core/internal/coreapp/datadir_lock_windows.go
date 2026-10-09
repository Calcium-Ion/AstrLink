//go:build windows

package coreapp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// LockDataDirectory takes directory for this process, before anything in it
// is opened or replaced. Windows drops the lock when the process exits, so a
// crash never leaves it held. Keep the closer until shutdown.
func LockDataDirectory(directory string) (io.Closer, error) {
	file, err := os.OpenFile(filepath.Join(directory, dataDirectoryLockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open data directory lock: %w", err)
	}
	var overlapped windows.Overlapped
	err = windows.LockFileEx(windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
	if err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, fmt.Errorf("%w: %s", ErrDataDirectoryInUse, directory)
		}
		return nil, fmt.Errorf("lock data directory: %w", err)
	}
	return file, nil
}
