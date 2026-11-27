package pluginhost

import (
	"bytes"
	"context"
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
	"time"

	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"github.com/QuantumNous/new-api/pluginkit"
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
// result, or the next of a chain, as pluginkit read it.
type convertedRequest = pluginkit.ConvertedRequest

// convertError is a vendor error a convert hook made.
type convertError = pluginkit.UpstreamError

// convertedAnswer is what convertResponse returns: the reply or the next
// request, or the vendor error the plugin made of the response.
type convertedAnswer struct {
	pluginkit.ConvertedResponse
	failed *convertError
}

// convertResponse runs convertResponse on an upstream response.
func convertResponse(ctx context.Context, relay *relayTarget, hookCtx pluginkit.HookContext, status int, headers map[string]string, body []byte) (convertedAnswer, error) {
	return callProvider(ctx, relay, "convertResponse", func(ctx context.Context, loaded *plugin.NewAPIPlugin) (convertedAnswer, error) {
		converted, failed, err := loaded.CallConvertResponse(ctx, hookCtx, status, headers, body)
		return convertedAnswer{converted, failed}, err
	})
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

// hookContext is ctx of the convert hooks.
func (send *relaySend) hookContext(clientPath string, state json.RawMessage) pluginkit.HookContext {
	return send.relay.hookContext(send.baseURL, clientPath, state)
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
	case path != "":
		joined, err := joinPath(send.root, path)
		if err != nil {
			return url.URL{}, err
		}
		return *joined, nil
	case rawURL != "":
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return url.URL{}, fmt.Errorf("the url %q is not an address", rawURL)
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

// forwardConverted sends an attempt over a route the plugin's hooks
// convert, and answers in the route's protocol.
func (host *Host) forwardConverted(ctx context.Context, writer http.ResponseWriter, target transport.Target, attempt ProviderAttempt, send *relaySend, method string, body []byte, stream bool) error {
	relay := send.relay
	form, err := readClientForm(send.header.Get("Content-Type"), body)
	if err != nil {
		return transport.NewUpstreamError(err)
	}
	send.form = form
	input := pluginkit.ConvertInput{Path: attempt.ClientPath, Model: attempt.Model, Stream: stream}
	if form != nil {
		input.Form = form.input(attempt.Model)
	} else {
		input.Body = body
	}
	hookCtx := send.hookContext(attempt.ClientPath, nil)
	request, err := callProvider(ctx, relay, "convertRequest", func(ctx context.Context, loaded *plugin.NewAPIPlugin) (convertedRequest, error) {
		return loaded.CallConvertRequest(ctx, hookCtx, input)
	})
	if err != nil {
		return transport.NewUpstreamError(fmt.Errorf("convertRequest: %w", err))
	}
	state := request.State
	if request.WebSocket != nil {
		return host.converseWebSocket(ctx, writer, target, attempt, send, *request.WebSocket, state)
	}
	payload := request.Body
	// prepareRequest runs on a JSON body, its $file markers still in it.
	if relay.plugin.HasTaskHook("prepareRequest") && request.Body != nil && json.Valid(payload) {
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
		requestMethod := request.Method
		switch {
		case requestMethod == "" && first:
			requestMethod = method
		case requestMethod == "" && len(payload) > 0:
			requestMethod = http.MethodPost
		case requestMethod == "":
			requestMethod = http.MethodGet
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
		answer, err := convertResponse(ctx, relay, send.hookContext(attempt.ClientPath, state), response.StatusCode, firstHeaders(response.Header), whole)
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
		case answer.failed != nil && answer.failed.Status == http.StatusUnauthorized && send.renewable():
			if failed, err := send.renew(ctx); failed != nil {
				return answerRelay(writer, target, failed)
			} else if err == nil {
				sent--
				continue
			}
			return answerRelay(writer, target, convertedFailure(*answer.failed, response.StatusCode))
		case answer.failed != nil:
			return answerRelay(writer, target, convertedFailure(*answer.failed, response.StatusCode))
		case answer.Next != nil:
			if answer.State != nil {
				state = answer.State
			}
			request = *answer.Next
			payload, contentType, err = payloadOf(request, request.Body, form)
			if err != nil {
				return transport.NewUpstreamError(err)
			}
			if delay := min(request.Delay, maxConvertedDelay); delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			first = false
			continue
		case !success:
			return answerRelay(writer, target, original())
		}
		data, contentType := answer.Body, answer.ContentType
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
