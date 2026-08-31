//go:build unix && !darwin && !linux

package coreapp

import (
	"net"
)

// Other Unix platforms rely on 0600 socket permissions only.
func requireSameUID(net.Conn) error {
	return nil
}
