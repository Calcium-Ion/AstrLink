package transport

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// proxyChainHeaders is what Caddy, nginx, Traefik and Cloudflare add in front
// of a self-hosted gateway, in the spellings clients actually send.
var proxyChainHeaders = map[string]string{
	"Forwarded":                `for=203.0.113.7;proto=https;host=gateway.example`,
	"X-Forwarded-For":          "203.0.113.7, 10.0.0.2",
	"X-Forwarded-Host":         "gateway.example",
	"X-Forwarded-Proto":        "https",
	"X-Forwarded-Port":         "443",
	"x-forwarded-prefix":       "/llm",
	"X-Real-IP":                "203.0.113.7",
	"Via":                      "1.1 caddy",
	"True-Client-IP":           "203.0.113.7",
	"CF-Connecting-IP":         "203.0.113.7",
	"cf-ipcountry":             "NL",
	"CF-Ray":                   "8c1f2e3d4c5b6a79-AMS",
	"CF-Visitor":               `{"scheme":"https"}`,
	"CDN-Loop":                 "cloudflare",
	"X-Client-IP":              "203.0.113.7",
	"X-Original-Forwarded-For": "203.0.113.7",
}

func setProxyChainHeaders(header http.Header) {
	for name, value := range proxyChainHeaders {
		// Raw map writes keep non-canonical spellings, as an HTTP/2 hop would.
		header[name] = []string{value}
	}
}

func assertNoProxyChainHeaders(t *testing.T, header http.Header) {
	t.Helper()
	for name := range proxyChainHeaders {
		for received := range header {
			if strings.EqualFold(received, name) {
				t.Errorf("proxy chain header reached upstream: %s: %q", received, header[received])
			}
		}
	}
}

func TestForwardDropsProxyChainHeadersEvenFromTargetOverlays(t *testing.T) {
	received := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received <- request.Header.Clone()
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{}`)
	}))
	defer upstream.Close()

	overlay := http.Header{}
	overlay.Set("X-Forwarded-For", "198.51.100.9")
	overlay.Set("Via", "1.1 adapter")
	overlay.Set("X-Provider-Keep", "kept")
	target := Target{BaseURL: mustParseURL(t, upstream.URL), RequestHeaders: overlay}
	request := httptest.NewRequest(http.MethodPost, "http://gateway.example/v1/responses", strings.NewReader(`{"input":"hello"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Client-Keep", "kept")
	setProxyChainHeaders(request.Header)

	forwarder := New(nil)
	defer forwarder.proxyTransport.(interface{ CloseIdleConnections() }).CloseIdleConnections()
	response := httptest.NewRecorder()
	if err := forwarder.Forward(response, request, target); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	header := <-received
	assertNoProxyChainHeaders(t, header)
	if header.Get("X-Client-Keep") != "kept" || header.Get("X-Provider-Keep") != "kept" {
		t.Fatalf("ordinary headers were dropped: %v", header)
	}
}

func TestResponsesSocketDropsProxyChainHeaders(t *testing.T) {
	received := make(chan http.Header, 1)
	upgrader := websocket.Upgrader{}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received <- request.Header.Clone()
		conn, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteJSON(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_chain", "status": "completed", "output": []any{}}})
		// Hold the connection until the client closes it.
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()

	overlay := http.Header{}
	overlay.Set("X-Real-IP", "198.51.100.9")
	target := Target{BaseURL: mustParseURL(t, upstream.URL), RequestHeaders: overlay}
	request := httptest.NewRequest(http.MethodPost, "http://gateway.example/v1/responses", strings.NewReader(`{"model":"test","input":"hello"}`))
	request.Header.Set("X-Client-Keep", "kept")
	setProxyChainHeaders(request.Header)

	socket := &ResponsesSocket{}
	defer socket.Close()
	if err := socket.Forward(httptest.NewRecorder(), request, target, "binding", nil); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	header := <-received
	assertNoProxyChainHeaders(t, header)
	if header.Get("X-Client-Keep") != "kept" {
		t.Fatalf("ordinary headers were dropped: %v", header)
	}
}
