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
	return &uidCheckedListener{Listener: listener, check: requireSameUID}, nil
}

type uidCheckedListener struct {
	net.Listener
	check func(net.Conn) error
}

// Accept drops a connection from another user and keeps listening.
// http.Server.Serve stops on a non-temporary Accept error, so returning the
// rejection would let any root process (sudo astrlink, docker exec -u root)
// shut Core down by connecting.
func (listener *uidCheckedListener) Accept() (net.Conn, error) {
	for {
		conn, err := listener.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if err := listener.check(conn); err != nil {
			_ = conn.Close()
			continue
		}
		return conn, nil
	}
}
