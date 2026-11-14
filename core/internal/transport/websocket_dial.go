package transport

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
)

// webSocketHandshakeTimeout bounds the opening handshake of an upstream
// WebSocket session.
const webSocketHandshakeTimeout = 30 * time.Second

// DialWebSocket opens a WebSocket session with the upstream at address, an
// http or https address dialed as ws or wss: through the provider's proxy
// and TLS policy, with header and the target's request headers, as
// RoundTrip sends a request. A handshake the upstream refuses returns its
// response.
func DialWebSocket(ctx context.Context, target Target, address *url.URL, header http.Header, protocols []string, readLimit int64) (*websocket.Conn, *http.Response, error) {
	target.BaseURL = &url.URL{Scheme: address.Scheme, Host: address.Host}
	if err := validateTarget(target); err != nil {
		return nil, nil, &TargetError{err: err}
	}
	ctx, err := networkproxy.Bind(ctx, target.Service, target.ProxyCredentials)
	if err != nil {
		return nil, nil, &TargetError{err: err}
	}
	header = header.Clone()
	removeHopByHopHeaders(header)
	removeInboundCredentials(header)
	overlayHeaders(header, target.RequestHeaders)
	removeHopByHopHeaders(header)
	removeGatewayHeaders(header)
	removeForwardingHeaders(header)
	for name := range header {
		if strings.HasPrefix(strings.ToLower(name), "sec-websocket-") {
			header.Del(name)
		}
	}
	for _, name := range []string{"Content-Type", "Content-Length", "Accept-Encoding", "Accept"} {
		header.Del(name)
	}
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = webSocketHandshakeTimeout
	dialer.Subprotocols = protocols
	if err := networkproxy.ConfigureWebSocket(ctx, &dialer, address); err != nil {
		return nil, nil, NewUpstreamError(err)
	}
	dialed := *address
	if dialed.Scheme == "https" {
		dialed.Scheme = "wss"
	} else {
		dialed.Scheme = "ws"
	}
	conn, response, err := dialer.DialContext(ctx, dialed.String(), header)
	if err != nil {
		if response != nil && response.Body != nil {
			return nil, response, err
		}
		return nil, nil, NewUpstreamError(err)
	}
	conn.SetReadLimit(readLimit)
	return conn, response, nil
}
