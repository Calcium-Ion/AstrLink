package networkproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/gorilla/websocket"
)

func TestInstanceIsolationModesRotationAndNoFallback(t *testing.T) {
	var directHits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { directHits.Add(1); io.WriteString(w, "direct") }))
	defer origin.Close()
	proxy := func(label string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !r.URL.IsAbs() {
				t.Error("request did not reach an HTTP proxy")
			}
			if label != "global" && r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(label+":secret")) {
				t.Error("instance proxy authentication crossed accounts")
			}
			io.WriteString(w, label)
		}))
	}
	a, b, global := proxy("alpha"), proxy("beta"), proxy("global")
	defer a.Close()
	defer b.Close()
	defer global.Close()
	base := http.DefaultTransport.(*http.Transport).Clone()
	globalURL, _ := url.Parse(global.URL)
	base.Proxy = http.ProxyURL(globalURL)
	client := WrapClient(&http.Client{Transport: base, Timeout: time.Second})
	defer client.CloseIdleConnections()
	bound := func(id, mode, address, user string) context.Context {
		t.Helper()
		var credential *contract.ProxyCredential
		if user != "" {
			credential = &contract.ProxyCredential{Username: user, Password: "secret"}
		}
		ctx, err := BindConfig(context.Background(), contract.ServiceID(id), &contract.ServiceProxy{Mode: mode, URL: address}, credential)
		if err != nil {
			t.Fatal(err)
		}
		return ctx
	}
	contexts := []context.Context{bound("service_one", "custom", a.URL, "alpha"), bound("service_two", "custom", b.URL, "beta")}
	// An authorization refresh and the following inference must share one
	// snapshot even if the settings change between those two network calls.
	preserved, err := Bind(contexts[0], contract.Service{ID: "service_one", Proxy: &contract.ServiceProxy{Mode: "direct"}}, nil)
	if err != nil || Binding(preserved) != Binding(contexts[0]) {
		t.Fatal("operation lost its proxy snapshot")
	}
	fetch := func(ctx context.Context, want string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, "GET", origin.URL, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if string(body) != want {
			t.Errorf("exit = %s; want %s", body, want)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		for j, ctx := range contexts {
			wg.Add(1)
			go func() { defer wg.Done(); fetch(ctx, []string{"alpha", "beta"}[j]) }()
		}
	}
	wg.Wait()
	fetch(bound("service_one", "custom", b.URL, "beta"), "beta")
	// A pending operation retains the old snapshot, without affecting other instances.
	fetch(Copy(context.Background(), contexts[0]), "alpha")
	fetch(bound("service_inherit", "inherit", "", ""), "global")
	fetch(bound("service_direct", "direct", "", ""), "direct")
	a.Close()
	req, _ := http.NewRequestWithContext(contexts[0], "GET", origin.URL, nil)
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("broken instance proxy fell back")
	} else if strings.Contains(err.Error(), "secret") {
		t.Fatal("proxy error exposed authentication")
	}
	if directHits.Load() != 1 {
		t.Fatalf("unexpected direct requests: %d", directHits.Load())
	}
}

func TestHTTPSProxyForHTTPAndWebSocket(t *testing.T) {
	upgrader := websocket.Upgrader{}
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credential reached provider")
		}
		if r.URL.Path == "/socket" {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			_ = conn.WriteMessage(websocket.TextMessage, []byte("connected"))
			return
		}
		io.WriteString(w, "ok")
	}))
	defer origin.Close()
	var connects atomic.Int32
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:password")) {
			t.Error("missing authenticated CONNECT")
			w.WriteHeader(407)
			return
		}
		connects.Add(1)
		upstream, err := net.DialTimeout("tcp", r.Host, time.Second)
		if err != nil {
			t.Error(err)
			return
		}
		downstream, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			t.Error(err)
			return
		}
		defer downstream.Close()
		defer upstream.Close()
		io.WriteString(downstream, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go io.Copy(upstream, downstream)
		io.Copy(downstream, upstream)
	}))
	defer proxy.Close()
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	roots.AddCert(proxy.Certificate())
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	previous := http.DefaultTransport
	http.DefaultTransport = base
	defer func() { http.DefaultTransport = previous; base.CloseIdleConnections() }()
	ctx, err := BindConfig(context.Background(), "service_tls", &contract.ServiceProxy{Mode: "custom", URL: proxy.URL}, &contract.ProxyCredential{Username: "user", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	client := WrapClient(&http.Client{Transport: base, Timeout: 3 * time.Second})
	defer client.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "GET", origin.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	dialer := *websocket.DefaultDialer
	target, _ := url.Parse(origin.URL + "/socket")
	if err := ConfigureWebSocket(ctx, &dialer, target); err != nil {
		t.Fatal(err)
	}
	conn, _, err := dialer.DialContext(ctx, "wss"+strings.TrimPrefix(target.String(), "https"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, message, err := conn.ReadMessage()
	if err != nil || string(message) != "connected" {
		t.Fatalf("WebSocket: %s %v", message, err)
	}
	if connects.Load() != 2 {
		t.Fatalf("CONNECT count=%d", connects.Load())
	}
}

func TestSOCKS5AuthenticationAndRemoteDNSForHTTPAndWebSocket(t *testing.T) {
	upgrader := websocket.Upgrader{}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/socket" {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			_ = conn.WriteMessage(websocket.TextMessage, []byte("socks"))
			return
		}
		io.WriteString(w, "socks")
	}))
	defer origin.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var connected atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				read := func(n int) []byte {
					b := make([]byte, n)
					if _, err := io.ReadFull(conn, b); err != nil {
						return nil
					}
					return b
				}
				header := read(2)
				if len(header) != 2 || header[0] != 5 {
					return
				}
				if read(int(header[1])) == nil {
					return
				}
				conn.Write([]byte{5, 2})
				auth := read(2)
				if len(auth) != 2 || auth[0] != 1 {
					return
				}
				user := read(int(auth[1]))
				length := read(1)
				if len(length) != 1 {
					return
				}
				password := read(int(length[0]))
				if string(user) != "user" || string(password) != "secret" {
					t.Error("SOCKS authentication lost")
					return
				}
				conn.Write([]byte{1, 0})
				request := read(5)
				if len(request) != 5 || request[0] != 5 || request[1] != 1 || request[3] != 3 {
					t.Error("SOCKS did not use remote DNS")
					return
				}
				host := read(int(request[4]))
				if read(2) == nil {
					return
				}
				if string(host) != "provider.invalid" {
					t.Error("unexpected SOCKS target")
					return
				}
				upstream, err := net.DialTimeout("tcp", strings.TrimPrefix(origin.URL, "http://"), time.Second)
				if err != nil {
					t.Error(err)
					return
				}
				defer upstream.Close()
				connected.Add(1)
				conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
				go io.Copy(upstream, conn)
				io.Copy(conn, upstream)
			}()
		}
	}()
	ctx, err := BindConfig(context.Background(), "service_socks", &contract.ServiceProxy{Mode: "custom", URL: "socks5://" + listener.Addr().String()}, &contract.ProxyCredential{Username: "user", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	client := WrapClient(&http.Client{Timeout: 3 * time.Second})
	defer client.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://provider.invalid", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	dialer := *websocket.DefaultDialer
	target, _ := url.Parse("http://provider.invalid/socket")
	if err := ConfigureWebSocket(ctx, &dialer, target); err != nil {
		t.Fatal(err)
	}
	conn, _, err := dialer.DialContext(ctx, "ws://provider.invalid/socket", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, body, err := conn.ReadMessage()
	if err != nil || string(body) != "socks" {
		t.Fatalf("SOCKS WebSocket %s %v", body, err)
	}
	if connected.Load() != 2 {
		t.Fatal("missing proxied connection")
	}
}
