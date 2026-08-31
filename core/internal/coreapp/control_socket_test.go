//go:build unix

package coreapp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/controlapi"
)

func TestListenControlSocketAcceptsSameUIDWithoutBearer(t *testing.T) {
	// macOS unix sockets reject paths longer than about 104 bytes.
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("al-cs-%d.sock", os.Getpid()))
	_ = os.Remove(socketPath)
	listener, err := listenControlSocket(socketPath)
	if err != nil {
		t.Fatalf("listenControlSocket: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})

	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %o, want 0600", info.Mode().Perm())
	}

	server := &http.Server{
		Handler: controlapi.LocalSocketHandler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if !controlapi.LocalSocketAuthenticated(request) {
				http.Error(writer, "missing socket auth", http.StatusUnauthorized)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
		})),
		ReadHeaderTimeout: time.Second,
	}
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(listener)
	}()

	client := &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 2 * time.Second,
	}
	response, err := client.Get("http://local-control/control/v1/secret")
	if err != nil {
		t.Fatalf("socket GET: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", response.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := <-done; err != nil && err != http.ErrServerClosed {
		t.Fatalf("Serve: %v", err)
	}
}
