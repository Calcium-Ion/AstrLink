//go:build windows

package coreapp

import (
	"fmt"
	"net"
)

func listenControlSocket(path string) (net.Listener, error) {
	return nil, fmt.Errorf("unix control socket is not supported on windows: %s", path)
}
