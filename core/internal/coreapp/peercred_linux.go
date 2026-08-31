//go:build linux

package coreapp

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func requireSameUID(conn net.Conn) error {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("control socket requires a unix connection")
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return fmt.Errorf("control socket syscall conn: %w", err)
	}
	var cred *unix.Ucred
	var ctrlErr error
	if err := raw.Control(func(fd uintptr) {
		cred, ctrlErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return fmt.Errorf("control socket peer credentials: %w", err)
	}
	if ctrlErr != nil {
		return fmt.Errorf("control socket peer credentials: %w", ctrlErr)
	}
	if int(cred.Uid) != os.Getuid() {
		return fmt.Errorf("control socket peer uid mismatch")
	}
	return nil
}
