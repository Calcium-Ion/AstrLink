// Package transport forwards inference requests to a natively compatible
// upstream. It deliberately performs no protocol conversion and never retries
// a request after an upstream response has started.
package transport

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
)

const copyBufferSize = 32 * 1024
const localPolicyWarningHeader = "X-AstrLink-Policy-Warning"

// Target contains the already-resolved upstream URL and safe request headers.
// RequestHeaders may include authentication material prepared by a higher
// layer. Forwarder does not log or otherwise retain those values.
type Target struct {
	Service          contract.Service
	ProxyCredentials secretstore.SecretStore
	// HandleResponse owns the body when set, and decides before any client bytes.
	HandleResponse func(*http.Response) error
	BaseURL        *url.URL
	RequestHeaders http.Header
	// ObserveOutbound runs after the outbound request is fully constructed and
	// immediately before RoundTrip. It is skipped on TargetError. Callers may
	// tee the request body; the forwarder does not retain the request.
	ObserveOutbound func(*http.Request)
	// WrapResponseBody may tee the upstream response body before it is copied
	// to the client writer. It receives hop-by-hop-stripped response headers.
	WrapResponseBody func(status int, header http.Header, body io.ReadCloser) io.ReadCloser
}

// TargetError reports a target rejected before an upstream request was made.
type TargetError struct {
	err error
}

func (err *TargetError) Error() string {
	return "invalid upstream target"
}

func (err *TargetError) Unwrap() error {
	return err.err
}

// UpstreamError reports a RoundTrip failure before response headers were
// available. The caller may still write its own error response in this case.
type UpstreamError struct {
	err error
}

func NewUpstreamError(err error) error {
	if err == nil {
		return nil
	}
	return &UpstreamError{err: err}
}

func (err *UpstreamError) Error() string {
	return "upstream request failed before the response started"
}

func (err *UpstreamError) Unwrap() error {
	return err.err
}

// ResponseError reports a read, write, or flush failure after the upstream
// response status was committed. The caller must not retry the request or
// append a second response envelope.
type ResponseError struct {
	err error
}

func NewResponseError(err error) error {
	if err == nil {
		return nil
	}
	return &ResponseError{err: err}
}

func (err *ResponseError) Error() string {
	return "upstream response was interrupted after it started"
}

func (err *ResponseError) Unwrap() error {
	return err.err
}

// Forwarder performs one protocol-preserving upstream round trip.
type Forwarder struct {
	roundTripper   http.RoundTripper
	proxyTransport http.RoundTripper
}

func New(roundTripper http.RoundTripper) *Forwarder {
	return NewWithResponseHeaderTimeout(roundTripper, 0)
}

// NewWithResponseHeaderTimeout builds a Forwarder whose default transport
// waits headerTimeout for upstream response headers. Zero waits indefinitely
// so slow non-stream generations are not cut off before the first byte.
func NewWithResponseHeaderTimeout(roundTripper http.RoundTripper, headerTimeout time.Duration) *Forwarder {
	if roundTripper == nil {
		if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
			configured := defaultTransport.Clone()
			configured.DisableCompression = true
			configured.ResponseHeaderTimeout = headerTimeout
			roundTripper = configured
		} else {
			roundTripper = http.DefaultTransport
		}
	}
	return &Forwarder{roundTripper: roundTripper, proxyTransport: networkproxy.WrapTransport(roundTripper)}
}

// Forward sends request to target and copies the response as it arrives.
// request.Context is inherited by the outbound request, so cancellation closes
// the upstream round trip. Forward never retries.
func (forwarder *Forwarder) RoundTrip(request *http.Request, target Target) (*http.Response, error) {
	if err := validateTarget(target); err != nil {
		return nil, &TargetError{err: err}
	}

	ctx, err := networkproxy.Bind(request.Context(), target.Service, target.ProxyCredentials)
	if err != nil {
		return nil, &TargetError{err: err}
	}
	outbound := request.Clone(ctx)
	outbound.URL = joinTargetURL(target.BaseURL, request.URL)
	outbound.Host = ""
	outbound.RequestURI = ""
	outbound.Close = false
	outbound.TransferEncoding = nil
	outbound.Trailer = nil
	removeHopByHopHeaders(outbound.Header)
	removeInboundCredentials(outbound.Header)
	overlayHeaders(outbound.Header, target.RequestHeaders)
	removeHopByHopHeaders(outbound.Header)
	removeGatewayHeaders(outbound.Header)

	if target.ObserveOutbound != nil {
		target.ObserveOutbound(outbound)
	}

	response, err := forwarder.proxyTransport.RoundTrip(outbound)
	if err != nil {
		return nil, &UpstreamError{err: err}
	}
	if response == nil {
		return nil, &UpstreamError{err: errors.New("round trip returned a nil response")}
	}
	if response.Body == nil {
		return nil, &UpstreamError{err: errors.New("round trip returned a response with a nil body")}
	}

	responseHeaders := response.Header.Clone()
	removeHopByHopHeaders(responseHeaders)
	removeCORSHeaders(responseHeaders)
	removeHeaderFold(responseHeaders, localPolicyWarningHeader)
	body := io.ReadCloser(response.Body)
	if target.WrapResponseBody != nil {
		body = target.WrapResponseBody(response.StatusCode, responseHeaders.Clone(), response.Body)
		if body == nil {
			_ = response.Body.Close()
			return nil, &UpstreamError{err: errors.New("response body wrapper returned nil")}
		}
	}
	response.Header = responseHeaders
	response.Body = body
	return response, nil
}

func (forwarder *Forwarder) Forward(writer http.ResponseWriter, request *http.Request, target Target) error {
	response, err := forwarder.RoundTrip(request, target)
	if err != nil {
		return err
	}
	if target.HandleResponse != nil {
		return target.HandleResponse(response)
	}
	return WriteResponse(writer, response)
}

// WriteResponse consumes and closes one response. Recovery belongs to the caller.
func WriteResponse(writer http.ResponseWriter, response *http.Response) error {
	defer response.Body.Close()
	copyHeaders(writer.Header(), response.Header)
	writer.WriteHeader(response.StatusCode)
	if err := flush(writer); err != nil {
		return &ResponseError{err: err}
	}
	if err := copyStreaming(writer, response.Body); err != nil {
		return &ResponseError{err: err}
	}
	return nil
}

// Inbound inference credentials authenticate the local caller and must never
// become upstream credentials. The Endpoint authorizer may add the selected
// upstream scheme again through Target.RequestHeaders.
func removeInboundCredentials(header http.Header) {
	for _, name := range []string{
		"Authorization",
		"Proxy-Authorization",
		"Cookie",
		"X-Api-Key",
		"X-Goog-Api-Key",
		localPolicyWarningHeader,
	} {
		header.Del(name)
	}
}

// Gateway diagnostics belong to the local connection. Apply this after target
// overlays as well, so a provider adapter cannot reintroduce gateway branding.
func removeGatewayHeaders(header http.Header) {
	for name := range header {
		if strings.HasPrefix(strings.ToLower(name), "x-astrlink-") {
			delete(header, name)
		}
	}
}

// The inference listener is not a browser API. Never let permissive upstream
// CORS policy turn loopback into a cross-origin credentialed relay.
func removeCORSHeaders(header http.Header) {
	for name := range header {
		canonical := http.CanonicalHeaderKey(name)
		if strings.HasPrefix(canonical, "Access-Control-") || canonical == "Timing-Allow-Origin" {
			delete(header, name)
		}
	}
}

func removeHeaderFold(header http.Header, name string) {
	for existing := range header {
		if strings.EqualFold(existing, name) {
			delete(header, existing)
		}
	}
}

func validateTarget(target Target) error {
	if target.BaseURL == nil {
		return errors.New("base URL is nil")
	}
	if target.BaseURL.Scheme != "http" && target.BaseURL.Scheme != "https" {
		return fmt.Errorf("unsupported base URL scheme %q", target.BaseURL.Scheme)
	}
	if target.BaseURL.Host == "" {
		return errors.New("base URL host is empty")
	}
	if target.BaseURL.User != nil {
		return errors.New("base URL contains credentials")
	}
	if target.BaseURL.RawQuery != "" || target.BaseURL.Fragment != "" {
		return errors.New("base URL contains a query or fragment")
	}
	return nil
}

func joinTargetURL(base, incoming *url.URL) *url.URL {
	joined := *base
	joined.Path, joined.RawPath = joinURLPath(base, incoming)
	joined.RawQuery = incoming.RawQuery
	joined.ForceQuery = incoming.ForceQuery
	return &joined
}

// JoinTargetURL applies the same reverse-proxy-prefix and API-version joining
// rules used by Forwarder. Control-plane probes use it to address the exact
// upstream model-discovery endpoint without duplicating URL policy.
func JoinTargetURL(base, incoming *url.URL) *url.URL {
	return joinTargetURL(base, incoming)
}

func joinURLPath(base, incoming *url.URL) (path string, rawPath string) {
	if base.RawPath == "" && incoming.RawPath == "" {
		return singleJoiningSlash(base.Path, trimDuplicateProtocolVersion(base.Path, incoming.Path)), ""
	}

	basePath := base.EscapedPath()
	incomingPath := trimDuplicateProtocolVersion(basePath, incoming.EscapedPath())
	decodedIncomingPath := trimDuplicateProtocolVersion(base.Path, incoming.Path)
	if incomingPath == "" {
		return strings.TrimSuffix(base.Path, "/"), strings.TrimSuffix(basePath, "/")
	}
	baseSlash := strings.HasSuffix(basePath, "/")
	incomingSlash := strings.HasPrefix(incomingPath, "/")
	switch {
	case baseSlash && incomingSlash:
		return base.Path + decodedIncomingPath[1:], basePath + incomingPath[1:]
	case !baseSlash && !incomingSlash:
		return base.Path + "/" + decodedIncomingPath, basePath + "/" + incomingPath
	default:
		return base.Path + decodedIncomingPath, basePath + incomingPath
	}
}

// Endpoint BaseURL accepts an origin or reverse-proxy prefix. Users commonly
// paste vendor SDK base URLs ending in /v1 or /v1beta; avoid producing
// /v1/v1/... while preserving every other prefix segment exactly.
func trimDuplicateProtocolVersion(basePath, incomingPath string) string {
	trimmedBase := strings.TrimSuffix(basePath, "/")
	for _, version := range []string{"/v1", "/v1beta"} {
		if trimmedBase != version && !strings.HasSuffix(trimmedBase, version) {
			continue
		}
		if incomingPath == version {
			return ""
		}
		if strings.HasPrefix(incomingPath, version+"/") {
			return strings.TrimPrefix(incomingPath, version)
		}
	}
	return incomingPath
}

func singleJoiningSlash(left, right string) string {
	if right == "" {
		return strings.TrimSuffix(left, "/")
	}
	leftSlash := strings.HasSuffix(left, "/")
	rightSlash := strings.HasPrefix(right, "/")
	switch {
	case leftSlash && rightSlash:
		return left + right[1:]
	case !leftSlash && !rightSlash:
		return left + "/" + right
	default:
		return left + right
	}
}

func overlayHeaders(destination, source http.Header) {
	for name, values := range source {
		destination.Del(name)
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}

func copyHeaders(destination, source http.Header) {
	for name, values := range source {
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}

func removeHopByHopHeaders(header http.Header) {
	for _, connection := range header.Values("Connection") {
		for _, name := range strings.Split(connection, ",") {
			if name = strings.TrimSpace(name); name != "" {
				header.Del(name)
			}
		}
	}

	for _, name := range []string{
		"Connection",
		"Proxy-Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
	} {
		header.Del(name)
	}
}

func copyStreaming(writer http.ResponseWriter, reader io.Reader) error {
	buffer := make([]byte, copyBufferSize)
	for {
		read, readErr := reader.Read(buffer)
		if read > 0 {
			written, writeErr := writer.Write(buffer[:read])
			if writeErr != nil {
				return writeErr
			}
			if written != read {
				return io.ErrShortWrite
			}
			if err := flush(writer); err != nil {
				return err
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

func flush(writer http.ResponseWriter) error {
	err := http.NewResponseController(writer).Flush()
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}
