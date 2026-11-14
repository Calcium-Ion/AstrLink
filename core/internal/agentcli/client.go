package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/controlapi"
)

const (
	requestTimeout          = 15 * time.Second
	maxControlResponseBytes = 8 << 20
	// An audit read carries up to four captured bodies (client and upstream
	// request and response). JSON escaping and base64 can grow them, so allow
	// twice the largest capture the audit settings permit.
	maxAuditResponseBytes = 2 * (2*contract.MaxRequestBodyMaxBytes + 2*contract.MaxResponseContentMaxBytes)
	// auditReadTimeout matches the desktop's audit reads: Core decrypts every
	// captured part before it answers.
	auditReadTimeout = 60 * time.Second
)

// userAgent must keep the `astrlink-cli` prefix the Control API classifies
// agent-side observers by.
const userAgent = "astrlink-cli/1"

// DialOptions selects how the CLI reaches the local Control API.
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
	// Agent is the agent's self-reported name, shown to the user beside a
	// raw access request. It authorizes nothing.
	Agent string
	// Progress receives status lines while a command waits on the user.
	Progress io.Writer
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
	// Each request sets its own deadline in do; audit reads need longer.
	return &Client{
		http:    &http.Client{},
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
	return &http.Client{Transport: transport}
}

// isAuditReadPath reports whether path reads one request's audit bodies,
// which may far exceed every other Control API response.
func isAuditReadPath(path string) bool {
	return strings.HasPrefix(path, controlapi.RequestsPath+"/") && strings.HasSuffix(path, "/audit")
}

func (client *Client) get(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	return client.do(ctx, http.MethodGet, path, query, nil, nil)
}

func (client *Client) post(ctx context.Context, path string, body any) (json.RawMessage, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return client.do(ctx, http.MethodPost, path, nil, raw, nil)
}

func (client *Client) do(
	ctx context.Context, method, path string, query url.Values, body []byte, header http.Header,
) (json.RawMessage, error) {
	limit, timeout := int64(maxControlResponseBytes), requestTimeout
	if isAuditReadPath(path) {
		limit, timeout = maxAuditResponseBytes, auditReadTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	target := client.baseURL + path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	for name, values := range header {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	// Names the CLI on the loopback fallback too, so the desktop can show
	// "an agent is reading" regardless of transport.
	request.Header.Set("User-Agent", userAgent)
	if !client.socketAuth && client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("control API request failed: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read control API response: %w", err)
	}
	if int64(len(payload)) > limit {
		return nil, fmt.Errorf("control API response is larger than %d MiB", limit>>20)
	}
	if response.StatusCode >= 300 {
		return nil, &APIError{Status: response.StatusCode, Body: json.RawMessage(payload)}
	}
	if len(payload) == 0 {
		return json.RawMessage("null"), nil
	}
	if !json.Valid(payload) {
		return nil, fmt.Errorf("control API returned non-JSON")
	}
	return json.RawMessage(payload), nil
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

// apiErrorCode is the Control API error code carried by err, or "" when err
// is not an API error or its body names none.
func apiErrorCode(err error) string {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return ""
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(apiErr.Body, &payload) != nil {
		return ""
	}
	return payload.Error.Code
}
