package pluginhost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// A route with converter plugin has the plugin's hooks convert it:
// convertRequest turns the request in the route's protocol into the
// vendor's, convertResponse and convertStreamEvent turn the vendor's answer
// back. convertResponse may send one more request instead of answering, for
// a vendor that takes several: an upload first, or a job polled until it
// is done. convertRequest may open a WebSocket session instead, whose
// frames convertStreamEvent reads.

// Bounds of a converted route.
const (
	maxConvertedRequests = 32
	maxConvertedDelay    = 10 * time.Second
)

// convertedRequest is a request the hooks describe: convertRequest's
// result, or the next of a chain.
type convertedRequest struct {
	Body           json.RawMessage   `json:"body"`
	ContentType    string            `json:"contentType"`
	Path           string            `json:"path"`
	URL            string            `json:"url"`
	Method         string            `json:"method"`
	Headers        map[string]string `json:"headers"`
	Stream         *bool             `json:"stream"`
	State          json.RawMessage   `json:"state"`
	NativeResponse bool              `json:"nativeResponse"`
	DelayMs        int               `json:"delayMs"`
	Websocket      json.RawMessage   `json:"websocket"`
	Form           json.RawMessage   `json:"form"`
	File           json.RawMessage   `json:"file"`
}

// convertedAnswer is what convertResponse returns.
type convertedAnswer struct {
	Body        json.RawMessage   `json:"body"`
	BodyBase64  *string           `json:"bodyBase64"`
	ContentType string            `json:"contentType"`
	Next        *convertedRequest `json:"next"`
	State       json.RawMessage   `json:"state"`
	Error       *convertError     `json:"error"`
}

type convertError struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// relaySend is how a converted route sends each of its requests: on the
// route's address, with the key or credential, the token and signRequest.
type relaySend struct {
	host      *Host
	relay     *relayTarget
	target    transport.Target
	roundTrip RoundTrip
	root      *url.URL
	address   *url.URL
	base      *url.URL
	baseURL   string
	key       string
	// query is the client's query, without the route's auth query.
	query      url.Values
	header     http.Header
	authHeader string
	form       *clientForm
	// renewed is set once the credential or token was renewed for the
	// client request.
	renewed bool
}

// view is ctx of the convert hooks.
func (send *relaySend) view(clientPath string) map[string]any {
	view := map[string]any{"baseUrl": send.baseURL, "path": clientPath, "config": send.relay.config}
	if send.relay.token != nil {
		view["token"] = send.relay.token
	}
	return view
}

// renewable reports whether a rejection may still renew the credential or
// the token.
func (send *relaySend) renewable() bool {
	if send.renewed {
		return false
	}
	return (send.relay.credential != nil && send.relay.plugin.HasTaskHook("refresh")) || send.relay.plugin.HasTaskHook("exchangeKey")
}

// renew refreshes the credential or exchanges the key again, once per
// client request. A failed exchange may return the plugin's answer.
func (send *relaySend) renew(ctx context.Context) (*http.Response, error) {
	send.renewed = true
	if send.relay.credential != nil && send.relay.plugin.HasTaskHook("refresh") {
		_, err := send.host.useCredential(ctx, send.target.Service, send.relay, send.baseURL, "", true)
		return nil, err
	}
	return send.host.exchange(ctx, send.target.Service, send.relay, send.base, send.key, true)
}

// addressOf is where a request the hooks describe goes: a path on the
// route's address, an absolute url, or the route's own address.
func (send *relaySend) addressOf(path, rawURL string) (url.URL, error) {
	switch {
	case path != "" && rawURL != "":
		return url.URL{}, errors.New("a converted request has a path or a url, not both")
	case path != "":
		if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
			return url.URL{}, fmt.Errorf("the path %q does not start with a single /", path)
		}
		joined, err := joinPath(send.root, path)
		if err != nil {
			return url.URL{}, err
		}
		return *joined, nil
	case rawURL != "":
		parsed, err := url.Parse(rawURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
			return url.URL{}, fmt.Errorf("the url %q is not an http or https address", rawURL)
		}
		return *parsed, nil
	}
	return *send.address, nil
}

// headers are the client's headers with those the hooks add, which may
// not be protected ones.
func (send *relaySend) headers(extra map[string]string) (http.Header, error) {
	header := send.header.Clone()
	for name, value := range extra {
		canonical := http.CanonicalHeaderKey(name)
		if relayProtected(canonical, send.authHeader) || !validHeader(name, value) {
			return nil, fmt.Errorf("a converted request may not set the header %s", name)
		}
		header.Set(canonical, value)
	}
	return header, nil
}

// signed is the final address and credential headers of a request: the
// client's query, the auth query, and what signRequest adds.
func (send *relaySend) signed(ctx context.Context, method string, address url.URL, header http.Header, body []byte) (url.URL, http.Header, error) {
	query := address.Query()
	for name, values := range send.query {
		if !query.Has(name) {
			query[name] = slices.Clone(values)
		}
	}
	credentials, _ := send.relay.authFor(send.key, query)
	address.RawQuery = query.Encode()
	if send.relay.plugin.HasTaskHook("signRequest") {
		final := header.Clone()
		maps.Copy(final, credentials)
		headers, extra, err := send.relay.sign(ctx, send.baseURL, send.key, method, &address, final, body)
		if err != nil {
			return url.URL{}, nil, err
		}
		for name, value := range headers {
			credentials.Set(name, value)
		}
		for name, value := range extra {
			query.Set(name, value)
		}
		address.RawQuery = query.Encode()
	}
	return address, credentials, nil
}

// send sends one request the hooks described.
func (send *relaySend) send(ctx context.Context, method string, request convertedRequest, body []byte, contentType string) (*http.Response, error) {
	address, err := send.addressOf(request.Path, request.URL)
	if err != nil {
		return nil, err
	}
	header, err := send.headers(request.Headers)
	if err != nil {
		return nil, err
	}
	if len(body) > 0 {
		if contentType == "" {
			contentType = "application/json"
		}
		header.Set("Content-Type", contentType)
	} else {
		header.Del("Content-Type")
	}
	address, credentials, err := send.signed(ctx, method, address, header, body)
	if err != nil {
		return nil, err
	}
	outbound, err := http.NewRequestWithContext(ctx, method, (&url.URL{Path: address.Path, RawQuery: address.RawQuery}).String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	outbound.Header = header
	outbound.Header.Set("Content-Length", strconv.Itoa(len(body)))
	outbound.ContentLength = int64(len(body))
	sent := send.target
	sent.BaseURL = &url.URL{Scheme: address.Scheme, Host: address.Host}
	sent.RequestHeaders = credentials
	return send.roundTrip(outbound, sent)
}

// convertedBody is the bytes a converted request sends: a string as it
// is, anything else as JSON.
func convertedBody(raw json.RawMessage) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var text string
	if trimmed[0] == '"' && json.Unmarshal(trimmed, &text) == nil {
		return []byte(text)
	}
	return trimmed
}

// forwardConverted sends an attempt over a route the plugin's hooks
// convert, and answers in the route's protocol.
func (host *Host) forwardConverted(ctx context.Context, writer http.ResponseWriter, target transport.Target, attempt ProviderAttempt, send *relaySend, method string, body []byte, stream bool) error {
	relay := send.relay
	form, err := readClientForm(send.header.Get("Content-Type"), body)
	if err != nil {
		return transport.NewUpstreamError(err)
	}
	send.form = form
	input := map[string]any{"model": attempt.Model, "stream": stream}
	if form != nil {
		input["form"] = form.view(attempt.Model)
	} else {
		input["body"] = json.RawMessage(body)
	}
	raw, err := relay.call(ctx, "convertRequest", send.view(attempt.ClientPath), input)
	if err != nil {
		return transport.NewUpstreamError(fmt.Errorf("convertRequest: %w", err))
	}
	var request convertedRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return transport.NewUpstreamError(errors.New("convertRequest returned no request"))
	}
	state := request.State
	if present(request.Websocket) {
		if present(request.Body) || present(request.Form) || present(request.File) {
			return transport.NewUpstreamError(errors.New("a converted request has more than one of body, form, file and websocket"))
		}
		return host.converseWebSocket(ctx, writer, target, attempt, send, request.Websocket, state)
	}
	payload := convertedBody(request.Body)
	// prepareRequest runs on a JSON body, its $file markers still in it.
	if relay.plugin.HasTaskHook("prepareRequest") && present(request.Body) && json.Valid(payload) {
		prepared, moved, err := relay.prepare(ctx, send.baseURL, attempt.ClientPath, attempt.Model, stream, send.address, send.root, payload, send.header, send.authHeader)
		var refusal *relayRefusal
		if errors.As(err, &refusal) {
			return writeRelayRefusal(writer, target, refusal)
		}
		if err != nil {
			return transport.NewUpstreamError(err)
		}
		payload = prepared
		if moved != send.address && request.Path == "" && request.URL == "" {
			request.URL = moved.String()
		}
	}
	payload, contentType, err := payloadOf(request, payload, form)
	if err != nil {
		return transport.NewUpstreamError(err)
	}
	first := true
	for sent := 0; ; sent++ {
		if sent > maxConvertedRequests {
			return transport.NewUpstreamError(fmt.Errorf("the conversion sent more than %d requests", maxConvertedRequests))
		}
		requestMethod := strings.ToUpper(request.Method)
		switch {
		case requestMethod == "" && first:
			requestMethod = method
		case requestMethod == "" && len(payload) > 0:
			requestMethod = http.MethodPost
		case requestMethod == "":
			requestMethod = http.MethodGet
		}
		if !slices.Contains([]string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions}, requestMethod) {
			return transport.NewUpstreamError(fmt.Errorf("the method %q is not one a converted request takes", request.Method))
		}
		if requestMethod == http.MethodGet || requestMethod == http.MethodHead {
			payload = nil
		}
		response, err := send.send(ctx, requestMethod, request, payload, contentType)
		if err != nil {
			if _, upstream := err.(*transport.UpstreamError); upstream {
				return err
			}
			return transport.NewUpstreamError(err)
		}
		if send.renewable() {
			// The upstream rejected the credential or token: it is renewed
			// and the same request sent once more.
			rejected, kept := relay.rejected(ctx, send.baseURL, attempt.ClientPath, response)
			response = kept
			if rejected {
				failed, err := send.renew(ctx)
				if failed != nil {
					_ = response.Body.Close()
					return answerRelay(writer, target, failed)
				}
				if err == nil {
					_ = response.Body.Close()
					sent--
					continue
				}
			}
		}
		readsStream := (request.Stream != nil && *request.Stream) || (request.Stream == nil && first && stream) || streamedType(response.Header.Get("Content-Type"))
		if request.Stream != nil && !*request.Stream {
			readsStream = false
		}
		success := response.StatusCode >= 200 && response.StatusCode < 300
		if success && request.NativeResponse {
			return answerRelay(writer, target, response)
		}
		if success && readsStream {
			conversion := host.newConversion(ctx, send, attempt, state)
			start := conversion.run(httpEvents(response), func() { _ = response.Body.Close() })
			switch {
			case start.err != nil:
				return transport.NewUpstreamError(start.err)
			case start.failure != nil && start.failure.Status == http.StatusUnauthorized && send.renewable():
				if failed, err := send.renew(ctx); failed != nil {
					return answerRelay(writer, target, failed)
				} else if err == nil {
					sent--
					continue
				}
				return answerRelay(writer, target, convertedFailure(*start.failure, response.StatusCode))
			case start.failure != nil:
				return answerRelay(writer, target, convertedFailure(*start.failure, response.StatusCode))
			}
			return answerRelay(writer, target, start.response)
		}
		whole, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes+1))
		_ = response.Body.Close()
		if err != nil || len(whole) > maxProviderResponseBytes {
			return transport.NewUpstreamError(errors.New("the upstream answer could not be read whole"))
		}
		answerInput := map[string]any{"status": response.StatusCode, "headers": firstHeaders(response.Header)}
		if textual(response.Header.Get("Content-Type"), whole) {
			answerInput["body"] = string(whole)
		} else {
			answerInput["bodyBase64"] = base64.StdEncoding.EncodeToString(whole)
		}
		stateView := send.view(attempt.ClientPath)
		stateView["state"] = jsonOrNull(state)
		raw, err := relay.call(ctx, "convertResponse", stateView, answerInput)
		var answer convertedAnswer
		if err == nil {
			err = json.Unmarshal(raw, &answer)
		}
		original := func() *http.Response {
			response.Body = io.NopCloser(bytes.NewReader(whole))
			response.ContentLength = int64(len(whole))
			return response
		}
		switch {
		case err != nil && success:
			return transport.NewUpstreamError(fmt.Errorf("convertResponse: %w", err))
		case err != nil:
			return answerRelay(writer, target, original())
		case answer.Next != nil:
			if len(answer.State) > 0 {
				state = answer.State
			}
			request = *answer.Next
			if present(request.Websocket) {
				return transport.NewUpstreamError(errors.New("the next request of a chain cannot open a WebSocket session"))
			}
			payload, contentType, err = payloadOf(request, convertedBody(request.Body), form)
			if err != nil {
				return transport.NewUpstreamError(err)
			}
			if delay := time.Duration(min(max(request.DelayMs, 0), int(maxConvertedDelay/time.Millisecond))) * time.Millisecond; delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			first = false
			continue
		case answer.Error != nil && answer.Error.Status == http.StatusUnauthorized && send.renewable():
			if failed, err := send.renew(ctx); failed != nil {
				return answerRelay(writer, target, failed)
			} else if err == nil {
				sent--
				continue
			}
			return answerRelay(writer, target, convertedFailure(*answer.Error, response.StatusCode))
		case answer.Error != nil:
			return answerRelay(writer, target, convertedFailure(*answer.Error, response.StatusCode))
		case !success:
			return answerRelay(writer, target, original())
		}
		data := convertedBody(answer.Body)
		if answer.BodyBase64 != nil {
			decoded, err := base64.StdEncoding.DecodeString(*answer.BodyBase64)
			if err != nil {
				return transport.NewUpstreamError(errors.New("convertResponse returned bodyBase64 that is not base64"))
			}
			data = decoded
		}
		contentType := answer.ContentType
		if contentType == "" {
			contentType = "application/json"
		}
		return answerRelay(writer, target, &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}},
			Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)),
		})
	}
}

// convertedFailure answers a vendor error a hook returned: its status, or
// the upstream's when it is an error, else 500.
func convertedFailure(failure convertError, upstream int) *http.Response {
	status := failure.Status
	if status < 400 || status > 599 {
		status = upstream
		if status < 400 {
			status = http.StatusInternalServerError
		}
	}
	data, _ := json.Marshal(map[string]any{"error": map[string]any{"message": truncateRunes(failure.Message, 4096), "type": truncateRunes(failure.Type, 256), "code": truncateRunes(failure.Code, 256)}})
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}
}

func streamedType(contentType string) bool {
	media, _, _ := mime.ParseMediaType(contentType)
	return media == "text/event-stream" || media == "application/vnd.amazon.eventstream"
}

// textual reports whether a response comes to the hooks as text.
func textual(contentType string, body []byte) bool {
	media, _, _ := mime.ParseMediaType(contentType)
	switch {
	case media == "":
		return utf8.Valid(body)
	case strings.HasPrefix(media, "text/"), strings.HasSuffix(media, "json"), strings.HasSuffix(media, "xml"),
		strings.Contains(media, "ndjson"), media == "application/x-www-form-urlencoded":
		return true
	}
	return false
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

func jsonOrNull(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage("null")
	}
	return raw
}
