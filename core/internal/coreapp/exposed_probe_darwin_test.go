package coreapp

import (
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
)

func TestExposedProbeIgnoresConnectionsAStoppedListenerClosed(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	client, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Fatal(err)
	}
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	// The serving side closes first, as a stopping Core does, so its end of the
	// connection stays in TIME_WAIT after the client closes too.
	_ = server.Close()
	_ = listener.Close()
	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("client read = %v, want EOF", err)
	}
	_ = client.Close()

	if err := bindWithoutReuse(port); !errors.Is(err, addressInUse) {
		t.Fatalf("bind without SO_REUSEADDR = %v, want the closed connection to refuse it", err)
	}
	if err := probeExposedPort(port); err != nil {
		t.Fatalf("probe = %v, want the port free once nothing listens on it", err)
	}
}

func TestExposedProbeReportsALoopbackListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)

	if err := probeExposedPort(port); !errors.Is(err, addressInUse) {
		t.Fatalf("probe = %v, want the 127.0.0.1 listener reported", err)
	}
}

func TestExposedProbeReportsAListenerOnAnInterfaceAddress(t *testing.T) {
	var host string
	for _, candidate := range localHosts() {
		ip := net.ParseIP(candidate)
		if ip != nil && ip.To4() != nil && !ip.IsLoopback() {
			host = candidate
			break
		}
	}
	if host == "" {
		t.Skip("no IPv4 interface address besides loopback")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)

	if err := probeExposedPort(port); !errors.Is(err, addressInUse) {
		t.Fatalf("probe = %v, want the %s listener reported", err, host)
	}
}
