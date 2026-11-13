//go:build !darwin && !windows

package proxycheck

// withSystem adds nothing: hyper-util reads only the environment here.
func withSystem(*settings) {}
