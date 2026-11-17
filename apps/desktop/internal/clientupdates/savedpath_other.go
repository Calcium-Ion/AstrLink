//go:build !windows

package clientupdates

// savedPath is Windows only.
func savedPath() (string, bool) { return "", false }
