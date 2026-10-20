//go:build darwin

package privacymodel

import "golang.org/x/sys/unix"

// macOSVersion returns the product version ("26.0.1") and the Darwin kernel
// release ("25.0.0"); either is empty when it cannot be read.
func macOSVersion() (string, string) {
	product, _ := unix.Sysctl("kern.osproductversion")
	darwin, _ := unix.Sysctl("kern.osrelease")
	return product, darwin
}
