//go:build !windows

package core

import "io"

// attachJobObject is Windows only: elsewhere Core watches its parent PID.
func attachJobObject(int) (io.Closer, error) { return nil, nil }
