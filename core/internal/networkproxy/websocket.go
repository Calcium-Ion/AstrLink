package networkproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/MoeForge/moego/base64"
	"github.com/gorilla/websocket"
	xproxy "golang.org/x/net/proxy"
)

// ConfigureWebSocket shares the HTTP instance policy, including TLS to HTTPS
// proxies, which Gorilla's built-in proxy adapter does not implement.
func ConfigureWebSocket(ctx context.Context, dialer *websocket.Dialer, target *url.URL) error {
	base, selected, dial, err := route(ctx, target)
	if err != nil {
		return err
	}
	dialer.Proxy = nil
	dialer.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if base.TLSClientConfig != nil {
		dialer.TLSClientConfig = base.TLSClientConfig.Clone()
	}
	dialer.TLSClientConfig.NextProtos = []string{"http/1.1"}
	dialer.NetDialContext = dial
	if selected == nil {
		return nil
	}
	dialer.NetDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dialProxy(ctx, network, address, selected, dial, base.TLSClientConfig)
		if err != nil {
			return nil, &connectionError{err}
		}
		return conn, nil
	}
	return nil
}

// route picks the proxy an HTTP request to target takes under ctx's instance
// policy, with the base transport's dialer.
func route(ctx context.Context, target *url.URL) (*http.Transport, *url.URL, func(context.Context, string, string) (net.Conn, error), error) {
	base, ok := BaseTransport(http.DefaultTransport)
	if !ok {
		return nil, nil, nil, errors.New("proxy routing requires an HTTP transport")
	}
	selectProxy := base.Proxy
	if s, ok := ctx.Value(snapshotKey{}).(snapshot); ok {
		switch s.mode {
		case "direct":
			selectProxy = nil
		case "custom":
			selectProxy = http.ProxyURL(s.proxy)
		}
	}
	var selected *url.URL
	if selectProxy != nil {
		var err error
		selected, err = selectProxy(&http.Request{URL: target})
		if err != nil {
			return nil, nil, nil, &connectionError{err}
		}
	}
	dial := base.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	return base, selected, dial, nil
}

// Dial opens a TCP connection to address, the host:port of an HTTPS origin,
// over the route an HTTPS request bound to ctx takes: directly, or through the
// instance's or the gateway's HTTP, HTTPS or SOCKS5 proxy.
func Dial(ctx context.Context, address string) (net.Conn, error) {
	base, selected, dial, err := route(ctx, &url.URL{Scheme: "https", Host: address})
	if err != nil {
		return nil, err
	}
	if selected == nil {
		return dial(ctx, "tcp", address)
	}
	conn, err := dialProxy(ctx, "tcp", address, selected, dial, base.TLSClientConfig)
	if err != nil {
		return nil, &connectionError{err}
	}
	return conn, nil
}

func dialProxy(ctx context.Context, network, address string, proxyURL *url.URL, dial func(context.Context, string, string) (net.Conn, error), tlsConfig *tls.Config) (net.Conn, error) {
	port := proxyURL.Port()
	if port == "" {
		switch proxyURL.Scheme {
		case "https":
			port = "443"
		case "socks5":
			port = "1080"
		default:
			port = "80"
		}
	}
	proxyAddress := net.JoinHostPort(proxyURL.Hostname(), port)
	if proxyURL.Scheme == "socks5" {
		var auth *xproxy.Auth
		if proxyURL.User != nil {
			password, _ := proxyURL.User.Password()
			auth = &xproxy.Auth{User: proxyURL.User.Username(), Password: password}
		}
		d, err := xproxy.SOCKS5(network, proxyAddress, auth, contextDialer{dial})
		if err != nil {
			return nil, err
		}
		return d.(xproxy.ContextDialer).DialContext(ctx, network, address)
	}
	if proxyURL.Scheme != "http" && proxyURL.Scheme != "https" {
		return nil, errors.New("unsupported proxy scheme")
	}
	conn, err := dial(ctx, network, proxyAddress)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			conn.Close()
		}
	}()
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { rawConn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if proxyURL.Scheme == "https" {
		config := &tls.Config{MinVersion: tls.VersionTLS12}
		if tlsConfig != nil {
			config = tlsConfig.Clone()
		}
		config.ServerName = proxyURL.Hostname()
		config.NextProtos = []string{"http/1.1"}
		secured := tls.Client(conn, config)
		if err := secured.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		conn = secured
	}
	header := make(http.Header)
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(proxyURL.User.Username()+":"+password)))
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: address}, Host: address, Header: header}
	if err := req.Write(conn); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("proxy CONNECT rejected")
	}
	if !stop() && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	_ = conn.SetDeadline(time.Time{})
	success = true
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

type contextDialer struct {
	dial func(context.Context, string, string) (net.Conn, error)
}

func (d contextDialer) Dial(network, address string) (net.Conn, error) {
	return d.dial(context.Background(), network, address)
}
func (d contextDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d.dial(ctx, network, address)
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
