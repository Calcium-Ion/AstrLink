//go:build !darwin && !windows

package coreapp

// probeExposedPort is a no-op where the wildcard bind itself fails when any
// process holds the port, as Linux refuses the overlap.
func probeExposedPort(string) error {
	return nil
}
