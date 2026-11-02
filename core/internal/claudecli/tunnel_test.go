package claudecli

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
)

// echoServer answers every connection by echoing what it receives.
func echoServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return listener.Addr().String()
}

// upstreamProxy is an account's HTTP proxy. It requires its own credential,
// records each CONNECT and pipes it to origin.
type upstreamProxy struct {
	address string

	mu       sync.Mutex
	connects []string
	auth     []string
}

func (proxy *upstreamProxy) seen() ([]string, []string) {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	return append([]string(nil), proxy.connects...), append([]string(nil), proxy.auth...)
}

func newUpstreamProxy(t *testing.T, origin string) *upstreamProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	proxy := &upstreamProxy{address: listener.Addr().String()}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				request, err := http.ReadRequest(reader)
				if err != nil || request.Method != http.MethodConnect {
					return
				}
				proxy.mu.Lock()
				proxy.connects = append(proxy.connects, request.Host)
				proxy.auth = append(proxy.auth, request.Header.Get("Proxy-Authorization"))
				proxy.mu.Unlock()
				if request.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:secret")) {
					_, _ = io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\n\r\n")
					return
				}
				target, err := net.Dial("tcp", origin)
				if err != nil {
					_, _ = io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
					return
				}
				defer target.Close()
				_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
				go func() { _, _ = io.Copy(target, reader) }()
				_, _ = io.Copy(conn, target)
			}()
		}
	}()
	return proxy
}

func accountProxy(scheme, address string) Network {
	return func(ctx context.Context, id contract.ServiceID) (context.Context, error) {
		return networkproxy.BindConfig(ctx, id, &contract.ServiceProxy{Mode: "custom", URL: scheme + "://" + address},
			&contract.ProxyCredential{Username: "user", Password: "secret"})
	}
}

// connect opens a CONNECT through the tunnel the way the CLI does and
// returns the connection and the tunnel's status.
func connect(t *testing.T, proxyURL, target, authorization string) (net.Conn, *bufio.Reader, int) {
	t.Helper()
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	request := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if authorization != "" {
		request += "Proxy-Authorization: " + authorization + "\r\n"
	}
	if _, err := io.WriteString(conn, request+"\r\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	return conn, reader, response.StatusCode
}

func tunnelAuthorization(t *testing.T, tunnel *Tunnel) string {
	t.Helper()
	parsed, err := url.Parse(tunnel.URL())
	if err != nil {
		t.Fatal(err)
	}
	password, _ := parsed.User.Password()
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(parsed.User.Username()+":"+password))
}

func roundTrip(t *testing.T, conn net.Conn, reader *bufio.Reader) {
	t.Helper()
	if _, err := io.WriteString(conn, "client hello"); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("client hello"))
	if _, err := io.ReadFull(reader, got); err != nil || string(got) != "client hello" {
		t.Fatalf("relay returned %q, %v", got, err)
	}
}

func TestTunnelRelaysThroughTheAccountProxy(t *testing.T) {
	upstream := newUpstreamProxy(t, echoServer(t))
	tunnel, err := StartTunnel(accountProxy("http", upstream.address), "service_claude")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tunnel.Close() })

	parsed, _ := url.Parse(tunnel.URL())
	if parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.User.Username() == "" {
		t.Fatalf("tunnel URL = %s", tunnel.URL())
	}
	if password, _ := parsed.User.Password(); len(password) < 32 {
		t.Fatalf("tunnel credential is too short: %d", len(password))
	}
	env := tunnel.Env()
	if env["HTTPS_PROXY"] != tunnel.URL() || env["https_proxy"] != tunnel.URL() {
		t.Fatalf("env = %v", env)
	}
	for _, name := range []string{"NO_PROXY", "no_proxy"} {
		if env[name] != "127.0.0.1,localhost,::1" {
			t.Fatalf("%s = %q", name, env[name])
		}
	}

	conn, reader, status := connect(t, tunnel.URL(), "platform.claude.test:443", tunnelAuthorization(t, tunnel))
	if status != http.StatusOK {
		t.Fatalf("CONNECT status = %d", status)
	}
	roundTrip(t, conn, reader)
	connects, auth := upstream.seen()
	if len(connects) != 1 || connects[0] != "platform.claude.test:443" {
		t.Fatalf("account proxy saw %v", connects)
	}
	if auth[0] != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:secret")) {
		t.Fatalf("account proxy got %q, want its own credential and not the tunnel's", auth[0])
	}
}

func TestTunnelRefusesCallersWithoutItsCredential(t *testing.T) {
	upstream := newUpstreamProxy(t, echoServer(t))
	tunnel, err := StartTunnel(accountProxy("http", upstream.address), "service_claude")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tunnel.Close() })
	other, err := StartTunnel(nil, "service_other")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })

	for name, authorization := range map[string]string{
		"none":         "",
		"another":      tunnelAuthorization(t, other),
		"wrong scheme": strings.Replace(tunnelAuthorization(t, tunnel), "Basic", "Bearer", 1),
	} {
		if _, _, status := connect(t, tunnel.URL(), "platform.claude.test:443", authorization); status != http.StatusProxyAuthRequired {
			t.Fatalf("%s credential: status = %d", name, status)
		}
	}
	parsed, _ := url.Parse(tunnel.URL())
	conn, err := net.Dial("tcp", parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "GET http://platform.claude.test/ HTTP/1.1\r\nHost: platform.claude.test\r\nProxy-Authorization: "+tunnelAuthorization(t, tunnel)+"\r\n\r\n")
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil || response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("plain proxy request = %v, %v", response, err)
	}
	if connects, _ := upstream.seen(); len(connects) != 0 {
		t.Fatalf("refused callers reached the account proxy: %v", connects)
	}
}

func TestTunnelDialsSOCKSAccountProxies(t *testing.T) {
	origin := echoServer(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	targets := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		read := func(n int) []byte {
			buffer := make([]byte, n)
			if _, err := io.ReadFull(conn, buffer); err != nil {
				return nil
			}
			return buffer
		}
		greeting := read(2)
		if greeting == nil || read(int(greeting[1])) == nil {
			return
		}
		_, _ = conn.Write([]byte{5, 2})
		auth := read(2)
		if auth == nil {
			return
		}
		user := read(int(auth[1]))
		length := read(1)
		if length == nil {
			return
		}
		password := read(int(length[0]))
		if string(user) != "user" || string(password) != "secret" {
			_, _ = conn.Write([]byte{1, 1})
			return
		}
		_, _ = conn.Write([]byte{1, 0})
		request := read(5)
		if request == nil || request[3] != 3 {
			return
		}
		host := read(int(request[4]))
		port := read(2)
		if host == nil || port == nil {
			return
		}
		targets <- string(host)
		target, err := net.Dial("tcp", origin)
		if err != nil {
			return
		}
		defer target.Close()
		_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
		go func() { _, _ = io.Copy(target, conn) }()
		_, _ = io.Copy(conn, target)
	}()
	tunnel, err := StartTunnel(accountProxy("socks5", listener.Addr().String()), "service_claude")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tunnel.Close() })
	conn, reader, status := connect(t, tunnel.URL(), "api.claude.test:443", tunnelAuthorization(t, tunnel))
	if status != http.StatusOK {
		t.Fatalf("CONNECT status = %d", status)
	}
	roundTrip(t, conn, reader)
	if host := <-targets; host != "api.claude.test" {
		t.Fatalf("SOCKS target = %q, want the host name for remote DNS", host)
	}
}

func TestTunnelCloseCutsOpenRelays(t *testing.T) {
	upstream := newUpstreamProxy(t, echoServer(t))
	tunnel, err := StartTunnel(accountProxy("http", upstream.address), "service_claude")
	if err != nil {
		t.Fatal(err)
	}
	conn, reader, status := connect(t, tunnel.URL(), "api.claude.test:443", tunnelAuthorization(t, tunnel))
	if status != http.StatusOK {
		t.Fatalf("CONNECT status = %d", status)
	}
	roundTrip(t, conn, reader)
	// Close waits for every relay, so it returns only once this one is cut.
	if err := tunnel.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("the relay stayed open after Close")
	}
	parsed, _ := url.Parse(tunnel.URL())
	if conn, err := net.Dial("tcp", parsed.Host); err == nil {
		_ = conn.Close()
		t.Fatal("the tunnel still accepts after Close")
	}
}
