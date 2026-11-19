package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Caller sends one request to Core's control API. Package core implements
// it for the running Core; the command services and tests depend on the
// interface only.
type Caller interface {
	Call(ctx context.Context, req Request) (Response, error)
}

// Request is one control API call.
type Request struct {
	Method string
	// Path starts with /control/v1 and may carry a query string.
	Path string
	// Body is sent as JSON when it is not nil. A json.RawMessage is sent as
	// it is.
	Body any
	// IfMatch is sent as the If-Match header when it is not empty.
	IfMatch string
	// PluginApproved marks a change of a plugin the user confirmed in the
	// plugin confirmation window; Core refuses a change that grants a
	// plugin more without it.
	PluginApproved bool
}

// Response is what Core answered.
type Response struct {
	Status int
	// ETag is the ETag header, empty when Core sent none.
	ETag string
	Body json.RawMessage
}

// OK reports a 2xx status.
func (r Response) OK() bool { return r.Status >= 200 && r.Status < 300 }

// Client sends control requests to one Core with its operator token. It
// never uses a proxy and never follows a redirect.
type Client struct {
	http *http.Client
}

// maxIdleControlConns is how many idle connections to Core are kept for
// reuse.
const maxIdleControlConns = 16

// NewClient returns a client for Core's loopback control listener.
func NewClient() *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	// The pages poll several reads at once (sessions, a running session's
	// detail, call bodies, status); Go keeps only two idle connections per
	// host by default and closes the rest after each burst.
	transport.MaxIdleConnsPerHost = maxIdleControlConns
	return &Client{http: &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// transportError is a failure to reach Core or to read its answer in
// time, as opposed to an answer Core gave.
type transportError struct{ message string }

func (e *transportError) Error() string { return e.message }

// IsTransportError reports a failure to reach Core or to read its answer.
func IsTransportError(err error) bool {
	var transport *transportError
	return errors.As(err, &transport)
}

// Do sends req to the Core control listener at baseURL. A transport failure
// is sent once more unless the call must not repeat: a built-in tool test,
// a purge, or a raw proof.
func (c *Client) Do(ctx context.Context, baseURL, token string, req Request) (Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		if raw, ok := req.Body.(json.RawMessage); ok {
			body = raw
		} else if body, err = json.Marshal(req.Body); err != nil {
			return Response{}, fmt.Errorf("unable to encode control request: %w", err)
		}
	}
	url := strings.TrimRight(baseURL, "/") + req.Path
	for attempt := 0; ; attempt++ {
		response, err := c.dispatch(ctx, url, token, req, body)
		if err == nil {
			return response, nil
		}
		if attempt == 0 && retries(req.Method, req.Path) && IsTransportError(err) && ctx.Err() == nil {
			continue
		}
		fmt.Fprintf(os.Stderr, "astrlink: %s %s failed: %v\n", req.Method, req.Path, err)
		return Response{}, err
	}
}

func (c *Client) dispatch(ctx context.Context, url, token string, req Request, body []byte) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout(req.Method, req.Path))
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, req.Method, url, reader)
	if err != nil {
		return Response{}, fmt.Errorf("%s %s failed: %w", req.Method, req.Path, err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if req.IfMatch != "" {
		request.Header.Set("If-Match", req.IfMatch)
	}
	if req.PluginApproved {
		request.Header.Set(contract.PluginApprovalHeader, "confirmed")
	}
	if body != nil {
		contentType := "application/json"
		if req.Method == http.MethodPatch {
			contentType = "application/merge-patch+json"
		}
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return Response{}, sendError(req.Method, req.Path, err)
	}
	defer response.Body.Close()
	etag := response.Header.Get("ETag")
	if etag != "" && !isVisibleASCII(etag) {
		return Response{}, errors.New("Core returned a non-text ETag")
	}
	limit := BodyLimit(req.Path)
	if response.ContentLength > limit {
		return Response{}, fmt.Errorf("%s %s response is too large", req.Method, req.Path)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		if isRequestPurge(req.Method, req.Path) && isTimeout(err) {
			return Response{}, errors.New(RequestPurgeStillRunning)
		}
		message := fmt.Sprintf("%s %s body failed: %v", req.Method, req.Path, err)
		if isTimeout(err) {
			return Response{}, &transportError{message}
		}
		return Response{}, errors.New(message)
	}
	if int64(len(data)) > limit {
		return Response{}, fmt.Errorf("%s %s response is too large", req.Method, req.Path)
	}
	return Response{Status: response.StatusCode, ETag: etag, Body: data}, nil
}

func sendError(method, path string, err error) error {
	if isTimeout(err) && isRequestPurge(method, path) {
		return errors.New(RequestPurgeStillRunning)
	}
	return &transportError{fmt.Sprintf("%s %s failed: %v", method, path, err)}
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

func isVisibleASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] > 0x7e {
			if value[i] != '\t' {
				return false
			}
		}
	}
	return true
}

// StatusError is the message of a call Core refused, in the shape the
// frontend reads its error code from:
// `POST /path returned 409 Conflict: {"error":...}`.
func StatusError(method, path string, status int, body []byte) error {
	text := strings.ToValidUTF8(string(body), "�")
	if utf8.RuneCountInString(text) > maxErrorBody {
		text = string([]rune(text)[:maxErrorBody])
	}
	return fmt.Errorf("%s %s returned %s: %s", method, path, statusText(status), text)
}

func statusText(status int) string {
	if text := http.StatusText(status); text != "" {
		return fmt.Sprintf("%d %s", status, text)
	}
	return fmt.Sprintf("%d <unknown status code>", status)
}

// GetJSON reads an unauthenticated handshake endpoint (health, version,
// capabilities) into out.
func (c *Client) GetJSON(ctx context.Context, baseURL, path string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+path, nil)
	if err != nil {
		return fmt.Errorf("GET %s failed: %w", path, err)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return &transportError{fmt.Sprintf("GET %s failed: %v", path, err)}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxControlBody+1))
	if err != nil {
		return &transportError{fmt.Sprintf("GET %s body failed: %v", path, err)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return StatusError(http.MethodGet, path, response.StatusCode, data)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("GET %s returned invalid JSON: %w", path, err)
	}
	return nil
}

// Shutdown asks Core to stop gracefully. Core accepts with 202 and exits on
// its own.
func (c *Client) Shutdown(ctx context.Context, baseURL, token string) error {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/control/v1/shutdown", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxErrorBody))
	if response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("returned %s", statusText(response.StatusCode))
	}
	return nil
}

// Send is Call that turns a refusal into the StatusError the frontend
// reads.
func Send(ctx context.Context, caller Caller, req Request) (Response, error) {
	response, err := caller.Call(ctx, req)
	if err != nil {
		return Response{}, err
	}
	if !response.OK() {
		return Response{}, StatusError(req.Method, req.Path, response.Status, response.Body)
	}
	return response, nil
}
