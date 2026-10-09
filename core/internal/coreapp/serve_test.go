package coreapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServeConfigAcceptsNetworkAddresses(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8317", "[::]:8318", ":8317", "192.168.1.20:8317", "astrlink:8317"} {
		if err := validateListenAddress(address); err != nil {
			t.Errorf("%q: %v", address, err)
		}
	}
	for _, address := range []string{"", "8317", "0.0.0.0", "0.0.0.0:http", "0.0.0.0:70000"} {
		if err := validateListenAddress(address); err == nil {
			t.Errorf("%q was accepted", address)
		}
	}
	// The desktop path accepts only 127.0.0.1 or every interface, never a
	// single named address or an IPv6-only wildcard.
	for _, address := range []string{"192.168.1.20:18317", "[::]:18317", "astrlink:18317"} {
		desktop := DefaultConfig("0.1.0-test", "abc1234")
		desktop.InferenceListen = address
		if err := desktop.Validate(); err == nil {
			t.Errorf("desktop config accepted inference address %q", address)
		}
	}
}

// pathConsole owns /console/ paths, like the real console owns its API.
type pathConsole struct{ http.Handler }

func (pathConsole) Owns(request *http.Request) bool {
	return strings.HasPrefix(request.URL.Path, "/console/")
}

func TestRunServeSharesOneListenerAndServesTheSocketUntilCancelled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no control socket on Windows")
	}
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("al-sv-%d.sock", os.Getpid()))
	_ = os.Remove(socketPath)
	plane := func(name string) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(writer, name)
		})
	}
	listening := make(chan net.Addr, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RunServe(ctx, ServeConfig{Listen: "127.0.0.1:0", ControlSocketPath: socketPath}, ServeDependencies{
			InferenceHandler: plane("inference"),
			Console:          pathConsole{plane("console")},
			ControlHandler:   plane("socket"),
			Listening:        func(address net.Addr) { listening <- address },
		})
	}()
	var address net.Addr
	select {
	case address = <-listening:
	case err := <-done:
		t.Fatalf("RunServe: %v", err)
	}
	get := func(client *http.Client, url, want string) {
		t.Helper()
		response, err := client.Get(url)
		if err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if string(body) != want {
			t.Fatalf("GET %s = %q, want %q", url, body, want)
		}
	}
	direct := &http.Client{Transport: &http.Transport{DisableKeepAlives: true, Proxy: nil}}
	get(direct, "http://"+address.String()+"/console/v1/status", "console")
	get(direct, "http://"+address.String()+"/v1/models", "inference")
	get(direct, "http://"+address.String()+"/", "inference")
	socket := &http.Client{Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}}
	get(socket, "http://local-control/", "socket")

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunServe after cancel: %v", err)
	}
	if _, err := os.Stat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("control socket left behind: %v", err)
	}
}

func TestLockDataDirectoryAdmitsOneHolder(t *testing.T) {
	directory := t.TempDir()
	first, err := LockDataDirectory(directory)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if _, err := LockDataDirectory(directory); !errors.Is(err, ErrDataDirectoryInUse) {
		t.Fatalf("second lock = %v, want ErrDataDirectoryInUse", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := LockDataDirectory(directory)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	_ = again.Close()
	info, err := os.Stat(filepath.Join(directory, dataDirectoryLockName))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("lock file mode = %o", info.Mode().Perm())
	}
}
