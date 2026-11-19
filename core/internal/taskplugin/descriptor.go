package taskplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// descriptor is a RequestDescriptor: the upstream request a hook
// describes.
type descriptor struct {
	ResponseType   string                     `json:"responseType"`
	URL            string                     `json:"url"`
	Method         string                     `json:"method"`
	Headers        map[string]json.RawMessage `json:"headers"`
	Body           json.RawMessage            `json:"body"`
	Credentialless bool                       `json:"credentialless"`
	Action         string                     `json:"action"`
	Model          string                     `json:"model"`
	RewriteModel   string                     `json:"rewriteModel"`
	BodyType       string                     `json:"bodyType"`
	Parts          []descriptorPart           `json:"parts"`
	// ContentPath, of buildContentRequest alone, is where the artifact's
	// bytes are in base64 in the JSON reply of a long-running operation;
	// MimeType is their media type.
	ContentPath []json.RawMessage `json:"contentPath"`
	MimeType    string            `json:"mimeType"`
}

type descriptorPart struct {
	Name     string          `json:"name"`
	Value    json.RawMessage `json:"value"`
	FileRef  string          `json:"fileRef"`
	Filename string          `json:"filename"`
}

// descriptorHeaders are the headers a descriptor may not set: the
// transport owns them.
var descriptorHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Transfer-Encoding": true, "Connection": true, "Keep-Alive": true,
	"Proxy-Connection": true, "Proxy-Authorization": true, "Te": true, "Trailer": true, "Upgrade": true,
	"Accept-Encoding": true,
}

// maxRedirects bounds the redirects a credentialless download follows.
const maxRedirects = 5

func parseDescriptor(hook string, raw json.RawMessage) (*descriptor, error) {
	var parsed descriptor
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, failure(http.StatusBadGateway, "plugin_error", "%s returned no request", hook)
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, failure(http.StatusBadGateway, "plugin_error", "%s returned a request that is not a request descriptor", hook)
	}
	if parsed.ResponseType != "" && parsed.ResponseType != "json" && parsed.ResponseType != "sse" {
		return nil, failure(http.StatusBadGateway, "plugin_error", "%s returned the responseType %q", hook, parsed.ResponseType)
	}
	if parsed.BodyType != "" && parsed.BodyType != "json" && parsed.BodyType != "multipart" {
		return nil, failure(http.StatusBadGateway, "plugin_error", "%s returned the bodyType %q", hook, parsed.BodyType)
	}
	return &parsed, nil
}

// headers are the descriptor's headers, each scalar as text.
func (desc *descriptor) headers() (http.Header, error) {
	headers := http.Header{}
	for name, raw := range desc.Headers {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if canonical == "" || descriptorHeaders[canonical] || strings.ContainsAny(canonical, " \t\r\n:") {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			var scalar any
			if json.Unmarshal(raw, &scalar) != nil {
				return nil, fmt.Errorf("the header %s is not text", canonical)
			}
			switch scalar.(type) {
			case nil:
				continue
			case map[string]any, []any:
				return nil, fmt.Errorf("the header %s is not text", canonical)
			}
			value = strings.TrimSpace(string(raw))
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("the header %s holds a line break", canonical)
		}
		headers.Set(canonical, value)
	}
	return headers, nil
}

// exchange is one upstream request a hook described, and the provider and
// plugin it goes through.
type exchange struct {
	engine      *Engine
	target      *plugin.Plugin
	service     contract.Service
	meta        *plugin.TaskMeta
	credentials credentials
	body        *Body
}

// send sends what desc, which hook returned, describes and returns the
// upstream response, whose body the caller closes. clientHeaders are the
// client's Range and conditional headers of a download.
func (call *exchange) send(ctx context.Context, hook string, desc *descriptor, clientHeaders http.Header) (*http.Response, error) {
	target, err := url.Parse(strings.TrimSpace(desc.URL))
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, failure(http.StatusBadGateway, "plugin_error", "%s returned a url that is not an absolute http or https URL", hook)
	}
	target.Fragment = ""
	method := strings.ToUpper(strings.TrimSpace(desc.Method))
	hasBody := len(bytes.TrimSpace(desc.Body)) > 0 && !bytes.Equal(bytes.TrimSpace(desc.Body), []byte("null"))
	if method == "" {
		method = http.MethodGet
		if hasBody || desc.BodyType == "multipart" {
			method = http.MethodPost
		}
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil, failure(http.StatusBadGateway, "plugin_error", "%s returned the method %q", hook, desc.Method)
	}
	if desc.Credentialless {
		return call.download(ctx, hook, method, target, desc, hasBody, clientHeaders)
	}
	if !allowedHost(target, call.credentials.baseURL, call.meta) {
		return nil, failure(http.StatusBadGateway, "plugin_error", "%s addressed %s, which is neither the provider's host nor one the plugin declares", hook, target.Hostname())
	}
	headers, err := desc.headers()
	if err != nil {
		return nil, failure(http.StatusBadGateway, "plugin_error", "%s: %v", hook, err)
	}
	var payload io.Reader
	switch {
	case desc.BodyType == "multipart":
		data, contentType, err := multipartBody(desc.Parts, call.body)
		if err != nil {
			return nil, failure(http.StatusBadRequest, "invalid_request", "%v", err)
		}
		payload = bytes.NewReader(data)
		headers.Set("Content-Type", contentType)
	case hasBody:
		trimmed := bytes.TrimSpace(desc.Body)
		var text string
		if trimmed[0] == '"' && json.Unmarshal(trimmed, &text) == nil {
			payload = strings.NewReader(text)
		} else {
			inlined, err := inlineFiles(trimmed, call.body)
			if err != nil {
				return nil, failure(http.StatusBadRequest, "invalid_request", "%v", err)
			}
			if !call.meta.Has("json-order@1") || bytes.Contains(trimmed, []byte(`"__fileRef"`)) {
				// Without json-order@1 the host writes bodies sorted by key,
				// as Go's codec does; so is one with files inlined.
				inlined = sortedJSON(inlined)
			}
			payload = bytes.NewReader(inlined)
			if headers.Get("Content-Type") == "" {
				headers.Set("Content-Type", "application/json")
			}
		}
	}
	for name, values := range clientHeaders {
		headers[name] = values
	}
	return call.engine.roundTrip(ctx, call.service, method, target, headers, payload)
}

// roundTrip sends one request through the provider's proxy. The headers
// travel as the target's, which the transport keeps.
func (engine *Engine) roundTrip(ctx context.Context, service contract.Service, method string, target *url.URL, headers http.Header, payload io.Reader) (*http.Response, error) {
	path := &url.URL{Path: target.Path, RawPath: target.RawPath, RawQuery: target.RawQuery}
	if path.Path == "" {
		path.Path = "/"
	}
	request, err := http.NewRequestWithContext(ctx, method, path.String(), payload)
	if err != nil {
		return nil, failure(http.StatusBadGateway, "plugin_error", "the request to %s cannot be built", target.Hostname())
	}
	request.URL = path
	origin := &url.URL{Scheme: target.Scheme, Host: target.Host}
	response, err := engine.send(request, transport.Target{Service: service, ProxyCredentials: engine.proxyCredentials, BaseURL: origin, RequestHeaders: headers})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &Error{Status: http.StatusBadGateway, Code: "upstream_unavailable", Message: fmt.Sprintf("the request to %s failed", target.Hostname()), Retryable: true, err: err}
	}
	return response, nil
}

// download serves a credentialless descriptor: a GET or HEAD of a public
// address, a CDN link the vendor handed out, with no headers, body or key,
// and no private address reached on the way.
func (call *exchange) download(ctx context.Context, hook, method string, target *url.URL, desc *descriptor, hasBody bool, clientHeaders http.Header) (*http.Response, error) {
	if method != http.MethodGet && method != http.MethodHead {
		return nil, failure(http.StatusBadGateway, "plugin_error", "%s: a credentialless request is a GET or HEAD", hook)
	}
	if hasBody || desc.BodyType == "multipart" {
		return nil, failure(http.StatusBadGateway, "plugin_error", "%s: a credentialless request has no body", hook)
	}
	for range maxRedirects + 1 {
		if err := call.engine.checkAddress(ctx, target); err != nil {
			return nil, failure(http.StatusBadGateway, "plugin_error", "%s: %v", hook, err)
		}
		headers := http.Header{}
		for name, values := range clientHeaders {
			headers[name] = values
		}
		response, err := call.engine.roundTrip(ctx, call.service, method, target, headers, nil)
		if err != nil {
			return nil, err
		}
		switch response.StatusCode {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		default:
			return response, nil
		}
		location, err := target.Parse(response.Header.Get("Location"))
		response.Body.Close()
		if err != nil || (location.Scheme != "http" && location.Scheme != "https") {
			return nil, failure(http.StatusBadGateway, "upstream_error", "%s redirected to an address that is not http or https", target.Hostname())
		}
		target = location
	}
	return nil, failure(http.StatusBadGateway, "upstream_error", "the download redirected more than %d times", maxRedirects)
}

// publicAddress refuses a target on this machine or a private network: a
// literal address or a name that resolves to one.
func publicAddress(ctx context.Context, target *url.URL) error {
	host := target.Hostname()
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return errors.New("a credentialless request may not reach this machine")
	}
	addresses := []net.IP{net.ParseIP(host)}
	if addresses[0] == nil {
		resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			// The proxy, when the provider has one, resolves the name.
			return nil
		}
		addresses = addresses[:0]
		for _, address := range resolved {
			addresses = append(addresses, address.IP)
		}
	}
	for _, address := range addresses {
		if address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
			address.IsUnspecified() || address.IsMulticast() || address.IsInterfaceLocalMulticast() {
			return fmt.Errorf("a credentialless request may not reach the private address of %s", host)
		}
		if v4 := address.To4(); v4 != nil && (v4[0] == 100 && v4[1]&0xc0 == 64 || v4[0] == 0) {
			return fmt.Errorf("a credentialless request may not reach the private address of %s", host)
		}
	}
	return nil
}

// multipartBody builds a multipart request from parts: values as text,
// files from the client's uploads.
func multipartBody(parts []descriptorPart, body *Body) ([]byte, string, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for _, part := range parts {
		if part.Name == "" {
			return nil, "", errors.New("a multipart part has no name")
		}
		if part.FileRef != "" {
			file, ok := body.upload(part.FileRef)
			if !ok {
				return nil, "", fmt.Errorf("the part %s names %q, which the request did not upload", part.Name, part.FileRef)
			}
			filename := part.Filename
			if filename == "" {
				filename = file.Filename
			}
			header := textproto.MIMEHeader{}
			header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%s; filename=%s`, strconv.Quote(part.Name), strconv.Quote(filename)))
			mimeType := file.MimeType
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			header.Set("Content-Type", mimeType)
			target, err := writer.CreatePart(header)
			if err != nil {
				return nil, "", err
			}
			if _, err := target.Write(file.Data); err != nil {
				return nil, "", err
			}
			continue
		}
		trimmed := bytes.TrimSpace(part.Value)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			continue
		}
		value := string(trimmed)
		var text string
		if trimmed[0] == '"' && json.Unmarshal(trimmed, &text) == nil {
			value = text
		}
		if err := writer.WriteField(part.Name, value); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), writer.FormDataContentType(), nil
}

// upstreamResponse is the UpstreamResponse a parse hook reads.
type upstreamResponse struct {
	StatusCode int                 `json:"statusCode"`
	Headers    map[string][]string `json:"headers"`
	Body       json.RawMessage     `json:"body"`
}

// hookResponse is the HookHTTPResponse the poll hooks read beside the body.
type hookResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
}

func firstHeaders(header http.Header) map[string]string {
	headers := map[string]string{}
	for name, values := range header {
		if len(values) > 0 {
			headers[http.CanonicalHeaderKey(name)] = values[0]
		}
	}
	return headers
}

func allHeaders(header http.Header) map[string][]string {
	headers := map[string][]string{}
	for name, values := range header {
		headers[http.CanonicalHeaderKey(name)] = values
	}
	return headers
}

// readAnswer reads an upstream body up to limit, as JSON when it parses,
// else as text.
func readAnswer(response *http.Response, limit int64) (json.RawMessage, []byte, error) {
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, nil, &Error{Status: http.StatusBadGateway, Code: "upstream_unavailable", Message: "the upstream answer could not be read", Retryable: true, err: err}
	}
	if int64(len(data)) > limit {
		return nil, nil, failure(http.StatusBadGateway, "upstream_error", "the upstream answer is larger than %d MiB", limit>>20)
	}
	return answerValue(data), data, nil
}

// answerValue is a body as the hooks read it: parsed JSON, else the text.
func answerValue(data []byte) json.RawMessage {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && json.Valid(trimmed) {
		return trimmed
	}
	text, _ := json.Marshal(strings.ToValidUTF8(string(data), "�"))
	return text
}
