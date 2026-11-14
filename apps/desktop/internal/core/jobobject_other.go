//go:build !windows

package core

// attachJobObject is Windows only: elsewhere Core watches its parent PID.
func attachJobObject(int) (processTree, error) { return nil, nil }
