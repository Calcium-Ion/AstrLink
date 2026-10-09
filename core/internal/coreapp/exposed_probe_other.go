//go:build !darwin

package coreapp

// probeExposedPort is a no-op where the wildcard bind itself fails when any
// process holds the port: Linux refuses the overlap, Windows binds exclusively.
func probeExposedPort(string) error {
	return nil
}
