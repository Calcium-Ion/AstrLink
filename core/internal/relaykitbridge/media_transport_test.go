package relaykitbridge

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMediaResolverReusesConnectionsAndChecksEveryURL(t *testing.T) {
	var dials atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/redirect" {
			http.Redirect(writer, request, "https://127.0.0.1/private.png", http.StatusFound)
			return
		}
		writer.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(writer, "test image")
	}))
	t.Cleanup(server.Close)
	client := newMediaHTTPClient()
	transport := client.Transport.(*http.Transport)
	transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	t.Cleanup(transport.CloseIdleConnections)
	previous := mediaHTTPClient
	mediaHTTPClient = client
	t.Cleanup(func() { mediaHTTPClient = previous })

	// A public numeric host avoids DNS; the test dialer routes it only to the
	// local TLS server. URL checks and the real pooled transport still run.
	for range 2 {
		data, mime, err := ResolveImageData(context.Background(), "https://93.184.216.34/image.png")
		if err != nil || data == "" || mime != "image/png" {
			t.Fatalf("data=%q mime=%q error=%v", data, mime, err)
		}
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("two media requests opened %d connections, want 1", got)
	}
	for _, raw := range []string{"https://127.0.0.1/private.png", "https://93.184.216.34/redirect"} {
		if _, _, err := ResolveImageData(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "forbidden") {
			t.Fatalf("private destination %s error=%v", raw, err)
		}
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("rejected private destinations opened another connection: dials=%d", got)
	}
}

func TestMediaTransportClosesIdleConnections(t *testing.T) {
	closed := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "image")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	client := newMediaHTTPClient()
	transport := client.Transport.(*http.Transport)
	if transport.IdleConnTimeout <= 0 || transport.MaxIdleConns <= 0 {
		t.Fatalf("unbounded media pool: timeout=%v max=%d", transport.IdleConnTimeout, transport.MaxIdleConns)
	}
	// Exercise the configured idle-expiry mechanism without a 90-second test.
	transport.IdleConnTimeout = time.Millisecond
	transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	t.Cleanup(transport.CloseIdleConnections)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("idle media connection was not reclaimed")
	}
}
