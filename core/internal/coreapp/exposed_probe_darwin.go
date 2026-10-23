package coreapp

import (
	"context"
	"errors"
	"net"
	"syscall"
	"time"
)

// acceptProbeTimeout bounds each connection attempt that looks for a
// listener. Addresses of this machine accept or refuse at once; the bound only
// covers an interface that drops the attempt.
const acceptProbeTimeout = 300 * time.Millisecond

// probeExposedPort reports addressInUse when another process already holds
// the port on a specific address such as 127.0.0.1. macOS lets a wildcard
// bind coexist with such a socket once SO_REUSEADDR is set, which Go does by
// default, so the wildcard bind alone would succeed while local programs kept
// reaching the other process. Binding without SO_REUSEADDR refuses any
// overlap; the probe socket is closed before the real listener binds.
//
// That bind is also refused while connections the previous Core closed wait
// in TIME_WAIT, for 30 seconds or longer after a restart or an update. A
// refusal therefore counts only while something still accepts connections on
// the port.
func probeExposedPort(port string) error {
	err := bindWithoutReuse(port)
	if errors.Is(err, addressInUse) && !portAccepting(port) {
		return nil
	}
	return err
}

// bindWithoutReuse binds the every-interface port with SO_REUSEADDR off and
// releases it again.
func bindWithoutReuse(port string) error {
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

// portAccepting reports whether a listener answers on port at any address of
// this machine.
func portAccepting(port string) bool {
	hosts := localHosts()
	results := make(chan bool, len(hosts))
	for _, host := range hosts {
		go func() {
			connection, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), acceptProbeTimeout)
			if err == nil {
				_ = connection.Close()
			}
			results <- err == nil
		}()
	}
	for range hosts {
		if <-results {
			return true
		}
	}
	return false
}

// localHosts lists the addresses of the interfaces that are up, loopback
// included; IPv6 link-local addresses carry their zone.
func localHosts() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return []string{"127.0.0.1", "::1"}
	}
	var hosts []string
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 {
			continue
		}
		addresses, err := networkInterface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			network, ok := address.(*net.IPNet)
			if !ok {
				continue
			}
			host := network.IP.String()
			if network.IP.To4() == nil && network.IP.IsLinkLocalUnicast() {
				host += "%" + networkInterface.Name
			}
			hosts = append(hosts, host)
		}
	}
	return hosts
}
