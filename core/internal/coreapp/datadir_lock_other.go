//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows)

package coreapp

import (
	"fmt"
	"io"
	"runtime"
)

func LockDataDirectory(string) (io.Closer, error) {
	return nil, fmt.Errorf("data directory locking is not supported on %s", runtime.GOOS)
}
