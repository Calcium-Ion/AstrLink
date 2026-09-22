package agentmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const requestTimeout = 15 * time.Second

// userAgent must keep the `astrlink-mcp` prefix the Control API classifies
// agent-side observers by.
const userAgent = "astrlink-mcp/1"

// DialOptions selects how the MCP process reaches the local Control API.
// Production Unix uses Socket. Tests may use ControlURL plus ControlToken.
type DialOptions struct {
	Socket       string
	ControlURL   string
	ControlToken string
	SessionPath  string
}

type Client struct {
	http       *http.Client
	baseURL    string
	token      string
	socketAuth bool
}

func Dial(options DialOptions) (*Client, error) {
	resolved, err := resolveDial(options)
	if err != nil {
		return nil, err
	}
	if resolved.socket != "" {
		return &Client{
			http:       unixHTTPClient(resolved.socket),
			baseURL:    "http://local-control",
			socketAuth: true,
		}, nil
	}
	base := strings.TrimRight(resolved.url, "/")
	if base == "" {
		return nil, fmt.Errorf("control URL is required when no control socket is available")
	}
	return &Client{
		http:    &http.Client{Timeout: requestTimeout},
		baseURL: base,
		token:   resolved.token,
	}, nil
}

type resolvedDial struct {
	socket string
	url    string
	token  string
}

func resolveDial(options DialOptions) (resolvedDial, error) {
	if socket := strings.TrimSpace(options.Socket); socket != "" {
		return resolvedDial{socket: socket}, nil
	}
	if socket := strings.TrimSpace(os.Getenv("ASTRLINK_CONTROL_SOCKET")); socket != "" {
		return resolvedDial{socket: socket}, nil
	}
	if controlURL := strings.TrimSpace(options.ControlURL); controlURL != "" {
		return resolvedDial{url: controlURL, token: options.ControlToken}, nil
	}
	sessionPath := strings.TrimSpace(options.SessionPath)
	if sessionPath == "" {
		var err error
		sessionPath, err = DefaultSessionPath()
		if err != nil {
			return resolvedDial{}, err
		}
	}
	session, err := LoadSessionFile(sessionPath)
	if err != nil {
		return resolvedDial{}, fmt.Errorf("AstrLink control session unavailable (is the desktop gateway running?): %w", err)
	}
	if socket := strings.TrimSpace(session.ControlSocket); socket != "" {
		return resolvedDial{socket: socket}, nil
	}
	return resolvedDial{
		url:   strings.TrimSpace(session.ControlURL),
		token: session.ControlToken,
	}, nil
}

func unixHTTPClient(socket string) *http.Client {
	dialer := &net.Dialer{Timeout: requestTimeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		},
	}
	return &http.Client{Timeout: requestTimeout, Transport: transport}
}

func (client *Client) get(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	target := client.baseURL + path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	// Names the MCP bridge on the loopback fallback too, so the desktop can
	// show "an agent is reading" regardless of transport.
	request.Header.Set("User-Agent", userAgent)
	if !client.socketAuth && client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("control API request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read control API response: %w", err)
	}
	if response.StatusCode >= 300 {
		return nil, &APIError{Status: response.StatusCode, Body: json.RawMessage(body)}
	}
	if len(body) == 0 {
		return json.RawMessage("null"), nil
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("control API returned non-JSON")
	}
	return json.RawMessage(body), nil
}

// APIError is a non-2xx Control API response.
type APIError struct {
	Status int
	Body   json.RawMessage
}

func (err *APIError) Error() string {
	if len(err.Body) > 0 {
		return fmt.Sprintf("control API HTTP %d: %s", err.Status, string(err.Body))
	}
	return fmt.Sprintf("control API HTTP %d", err.Status)
}
