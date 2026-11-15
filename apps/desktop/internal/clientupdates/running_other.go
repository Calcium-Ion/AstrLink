//go:build !windows

package clientupdates

// findRunning is Windows only: other systems replace a running client's
// files in place.
func findRunning([]string) []runningProcess { return nil }
