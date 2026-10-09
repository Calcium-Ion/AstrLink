package coreapp

import (
	"context"
	"net"
	"syscall"
)

// probeExposedPort reports addressInUse when another process already holds
// the port on a specific address such as 127.0.0.1. macOS lets a wildcard
// bind coexist with such a socket once SO_REUSEADDR is set, which Go does by
// default, so the wildcard bind alone would succeed while local programs kept
// reaching the other process. Binding without SO_REUSEADDR refuses any
// overlap; the probe socket is closed before the real listener binds.
func probeExposedPort(port string) error {
	config := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var inner error
		if err := raw.Control(func(fd uintptr) {
			inner = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 0)
		}); err != nil {
			return err
		}
		return inner
	}}
	listener, err := config.Listen(context.Background(), "tcp", ":"+port)
	if err != nil {
		return err
	}
	return listener.Close()
}
