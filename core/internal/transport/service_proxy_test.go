package transport

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

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/gorilla/websocket"
)

func TestForwarderUsesInstanceProxyForHTTPAndSSE(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !r.URL.IsAbs() || r.URL.Host != "provider.invalid" {
				t.Error("not a proxy request")
			}
			if r.Header.Get("X-AstrLink-Trace") != "" || strings.Contains(strings.ToLower(r.UserAgent()), "astrlink") {
				t.Error("gateway identity leaked")
			}
			if streaming {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: proxied\n\n")
			} else {
				io.WriteString(w, `{"proxied":true}`)
			}
		}))
		target := Target{BaseURL: mustParseURL(t, "http://provider.invalid"), Service: contract.Service{ID: "service_forward", Proxy: &contract.ServiceProxy{Mode: "custom", URL: proxy.URL}}}
		forwarder := New(nil)
		request := httptest.NewRequest("POST", "http://localhost/v1/responses", strings.NewReader(`{"input":"hello"}`))
		request.Header.Set("X-AstrLink-Trace", "local")
		response := httptest.NewRecorder()
		if err := forwarder.Forward(response, request, target); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(response.Body.String(), "proxied") {
			t.Fatal(response.Body.String())
		}
		forwarder.proxyTransport.(interface{ CloseIdleConnections() }).CloseIdleConnections()
		proxy.Close()
	}
}

func TestResponsesSocketBindsProxyAndRequiresReconnectAfterChange(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var turns, tunnels atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials leaked")
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			turns.Add(1)
			_ = conn.WriteJSON(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_proxy", "status": "completed", "output": []any{}}})
		}
	}))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "provider.invalid:80" {
			t.Error("missing proxy tunnel")
			return
		}
		upstream, err := net.DialTimeout("tcp", strings.TrimPrefix(origin.URL, "http://"), time.Second)
		if err != nil {
			t.Error(err)
			return
		}
		defer upstream.Close()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		tunnels.Add(1)
		io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go io.Copy(upstream, conn)
		io.Copy(conn, upstream)
	}))
	defer proxy.Close()
	target := Target{BaseURL: mustParseURL(t, "http://provider.invalid"), Service: contract.Service{ID: "service_socket", Proxy: &contract.ServiceProxy{Mode: "custom", URL: proxy.URL}}}
	socket := &ResponsesSocket{}
	defer socket.Close()
	forward := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		request := httptest.NewRequest("POST", "http://localhost/v1/responses", strings.NewReader(`{"model":"test","input":"hello"}`)).WithContext(ctx)
		return socket.Forward(httptest.NewRecorder(), request, target, "account-binding", nil)
	}
	if err := forward(); err != nil {
		t.Fatal(err)
	}
	// Stopping the per-turn cancellation hook leaves the upstream connection reusable.
	target.Service.Proxy = &contract.ServiceProxy{Mode: "direct"}
	if err := forward(); err == nil {
		t.Fatal("existing socket accepted a different proxy")
	}
	if turns.Load() != 1 || tunnels.Load() != 1 {
		t.Fatalf("turns=%d tunnels=%d", turns.Load(), tunnels.Load())
	}
}
