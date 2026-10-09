package coreapp

import (
	"context"
	"net"
	"syscall"

	"golang.org/x/sys/windows"
)

// soExclusiveAddrUse is Winsock's SO_EXCLUSIVEADDRUSE, the bitwise complement
// of SO_REUSEADDR; x/sys/windows does not define it.
const soExclusiveAddrUse = ^windows.SO_REUSEADDR

// probeExposedPort reports addressInUse when another process already holds
// the port on an IPv4 address such as 127.0.0.1. Windows lets the dual-stack
// [::] listener of an exposed plane bind beside such an IPv4 socket, so the
// bind alone would succeed while local programs kept reaching the other
// process. The probe binds the IPv4 wildcard for exclusive use, which refuses
// any overlap; it is closed before the real listener binds.
func probeExposedPort(port string) error {
	config := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var inner error
		if err := raw.Control(func(fd uintptr) {
			inner = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, soExclusiveAddrUse, 1)
		}); err != nil {
			return err
		}
		return inner
	}}
	listener, err := config.Listen(context.Background(), "tcp4", "0.0.0.0:"+port)
	if err != nil {
		return err
	}
	return listener.Close()
}
