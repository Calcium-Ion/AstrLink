package claudecli

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
)

const (
	tunnelHandshakeTimeout = 30 * time.Second
	tunnelDialTimeout      = 30 * time.Second
	tunnelUser             = "claude"
	// tunnelNoProxy keeps the CLI's model calls to AstrLink's loopback base
	// URL off the tunnel; the CLI otherwise sends those through it too.
	tunnelNoProxy = "127.0.0.1,localhost,::1"
)

// Network resolves the network route of an account: the context an outgoing
// request of that account is bound to (see networkproxy.Resolver).
type Network func(ctx context.Context, id contract.ServiceID) (context.Context, error)

// Tunnel is the HTTPS proxy of one claude process: a loopback listener that
// accepts only CONNECT with its own credential and relays the bytes over the
// account's network route. Login, token refresh and anything else the CLI
// sends besides model calls thus leave the way AstrLink's own requests do,
// SOCKS included, which the CLI cannot speak. TLS stays end to end: the
// tunnel never terminates, reads or logs what it relays.
type Tunnel struct {
	listener net.Listener
	secret   string
	dial     func(context.Context, string) (net.Conn, error)
	ctx      context.Context
	cancel   context.CancelFunc

	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
	wg     sync.WaitGroup
}

// StartTunnel opens the tunnel of one claude process of account id. A nil
// network dials over the gateway's own route.
func StartTunnel(network Network, id contract.ServiceID) (*Tunnel, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	tunnel := &Tunnel{
		listener: listener, secret: hex.EncodeToString(raw[:]), ctx: ctx, cancel: cancel,
		conns: make(map[net.Conn]struct{}),
		dial: func(ctx context.Context, address string) (net.Conn, error) {
			if network != nil {
				bound, err := network(ctx, id)
				if err != nil {
					return nil, err
				}
				ctx = bound
			}
			return networkproxy.Dial(ctx, address)
		},
	}
	tunnel.wg.Add(1)
	go tunnel.serve()
	return tunnel, nil
}

// URL is the proxy URL the CLI uses, credential included.
func (tunnel *Tunnel) URL() string {
	return (&url.URL{Scheme: "http", User: url.UserPassword(tunnelUser, tunnel.secret), Host: tunnel.listener.Addr().String()}).String()
}

// Env is the proxy environment of the claude process.
func (tunnel *Tunnel) Env() map[string]string {
	proxy := tunnel.URL()
	return map[string]string{
		"HTTPS_PROXY": proxy, "https_proxy": proxy,
		"NO_PROXY": tunnelNoProxy, "no_proxy": tunnelNoProxy,
	}
}

// Close stops accepting, cuts every relay and waits for them to end.
func (tunnel *Tunnel) Close() error {
	tunnel.mu.Lock()
	if tunnel.closed {
		tunnel.mu.Unlock()
		return nil
	}
	tunnel.closed = true
	for conn := range tunnel.conns {
		_ = conn.Close()
	}
	tunnel.mu.Unlock()
	tunnel.cancel()
	err := tunnel.listener.Close()
	tunnel.wg.Wait()
	return err
}

// track registers a connection so Close can cut it; false once closed.
func (tunnel *Tunnel) track(conn net.Conn) bool {
	tunnel.mu.Lock()
	defer tunnel.mu.Unlock()
	if tunnel.closed {
		return false
	}
	tunnel.conns[conn] = struct{}{}
	return true
}

func (tunnel *Tunnel) untrack(conn net.Conn) {
	tunnel.mu.Lock()
	delete(tunnel.conns, conn)
	tunnel.mu.Unlock()
}

func (tunnel *Tunnel) serve() {
	defer tunnel.wg.Done()
	for {
		conn, err := tunnel.listener.Accept()
		if err != nil {
			return
		}
		if !tunnel.track(conn) {
			_ = conn.Close()
			return
		}
		tunnel.wg.Add(1)
		go func() {
			defer tunnel.wg.Done()
			defer tunnel.untrack(conn)
			defer conn.Close()
			tunnel.handle(conn)
		}()
	}
}

func (tunnel *Tunnel) handle(client net.Conn) {
	_ = client.SetDeadline(time.Now().Add(tunnelHandshakeTimeout))
	reader := bufio.NewReader(client)
	request, err := http.ReadRequest(reader)
	if err != nil {
		return
	}
	if request.Method != http.MethodConnect {
		reply(client, http.StatusMethodNotAllowed, nil)
		return
	}
	if !tunnel.authorized(request.Header.Get("Proxy-Authorization")) {
		reply(client, http.StatusProxyAuthRequired, http.Header{"Proxy-Authenticate": {`Basic realm="proxy"`}})
		return
	}
	address := request.Host
	if _, port, err := net.SplitHostPort(address); err != nil || port == "" {
		reply(client, http.StatusBadRequest, nil)
		return
	}
	ctx, cancel := context.WithTimeout(tunnel.ctx, tunnelDialTimeout)
	upstream, err := tunnel.dial(ctx, address)
	cancel()
	if err != nil {
		reply(client, http.StatusBadGateway, nil)
		return
	}
	defer upstream.Close()
	if !tunnel.track(upstream) {
		return
	}
	defer tunnel.untrack(upstream)
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	_ = client.SetDeadline(time.Time{})
	done := make(chan struct{}, 2)
	go func() {
		// Bytes the client sent after its CONNECT are already buffered.
		_, _ = io.Copy(upstream, reader)
		closeWrite(upstream)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		closeWrite(client)
		done <- struct{}{}
	}()
	<-done
	<-done
}

func (tunnel *Tunnel) authorized(header string) bool {
	encoded, ok := strings.CutPrefix(header, "Basic ")
	if !ok {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return false
	}
	want := tunnelUser + ":" + tunnel.secret
	return subtle.ConstantTimeCompare(decoded, []byte(want)) == 1
}

func reply(conn net.Conn, status int, header http.Header) {
	response := &http.Response{StatusCode: status, ProtoMajor: 1, ProtoMinor: 1, Header: header, Close: true}
	if response.Header == nil {
		response.Header = http.Header{}
	}
	_ = response.Write(conn)
}

// closeWrite ends one direction of a relay, so the peer sees EOF while the
// other direction drains.
func closeWrite(conn net.Conn) {
	type writeCloser interface{ CloseWrite() error }
	if closer, ok := conn.(writeCloser); ok && closer.CloseWrite() == nil {
		return
	}
	_ = conn.Close()
}
