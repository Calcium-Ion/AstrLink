//go:build unix

package coreapp

import (
	"fmt"
	"net"
	"os"
)

func listenControlSocket(path string) (net.Listener, error) {
	if path == "" {
		return nil, fmt.Errorf("control socket path is required")
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale control socket: %w", err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("restrict control socket mode: %w", err)
	}
	return &uidCheckedListener{Listener: listener}, nil
}

type uidCheckedListener struct {
	net.Listener
}

func (listener *uidCheckedListener) Accept() (net.Conn, error) {
	conn, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if err := requireSameUID(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}
