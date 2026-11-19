package pluginhost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// RoundTrip sends one upstream request as the gateway sends every attempt:
// through the provider's proxy, without the client's credentials or the
// gateway's headers, with the provider's key.
type RoundTrip = func(*http.Request, transport.Target) (*http.Response, error)

// ProviderAttempt is what an attempt on a provider of kind plugin asks for:
// the protocol its body speaks, which the gateway converted it into when the
// provider declares convert.
type ProviderAttempt struct {
	Protocol contract.ProtocolID
	Model    string
	Stream   bool
	// Index counts the attempts of the request, from 0.
	Index int
	// ClientPath is the path the client asked, which picks the route of a
	// new-api plugin's provider part.
	ClientPath string
}

// Bounds of what a provider adapter reads of an upstream answer.
const (
	maxProviderResponseBytes = 64 << 20
	maxProviderErrorBytes    = 1 << 20
	// maxProviderEventBytes bounds one upstream stream event.
	maxProviderEventBytes = 4 << 20
)

// providerHeaders are the client headers a provider adapter sees: what
// tells the protocol's version and features, never credentials.
var providerHeaders = []string{"anthropic-version", "anthropic-beta", "openai-beta"}

// errProviderUnavailable is a provider whose plugin cannot serve it now.
var errProviderUnavailable = errors.New("the plugin does not serve this provider now")

// adapter is one attempt's plugin and provider.
type adapter struct {
	host     *Host
	target   *plugin.Plugin
	provider *plugin.Provider
	service  contract.Service
}

// providerFor finds the plugin and the provider a service of kind plugin
// names, while the plugin is enabled with provider granted.
func (host *Host) providerFor(service contract.Service) (*adapter, error) {
	if host == nil || host.plugins == nil || service.Plugin == nil {
		return nil, errProviderUnavailable
	}
	target := host.plugins.Current().Plugin(service.Plugin.Key)
	if target == nil || !target.Enabled() || !target.Can(plugin.CapabilityProvider) {
		return nil, errProviderUnavailable
	}
	provider := target.Meta.Provider(service.Plugin.Provider)
	if provider == nil {
		return nil, errProviderUnavailable
	}
	return &adapter{host: host, target: target, provider: provider, service: service}, nil
}

// call runs one of the adapter's hooks within limit, the network allowed
// when granted.
func (adapter *adapter) call(ctx context.Context, hook, limit string, args ...any) (json.RawMessage, error) {
	bound := adapter.target.Limit(limit)
	ctx, cancel := context.WithTimeoutCause(ctx, bound, errHookLimit)
	defer cancel()
	options := plugin.CallOptions{}
	if adapter.target.Can(plugin.CapabilityNetwork) {
		options.NetworkBudget = bound
	}
	result, _, err := adapter.target.CallArgs(ctx, hook, args, options)
	return result, err
}

// context is what every hook of the attempt gets, the body only where
// asked for.
func (adapter *adapter) context(attempt ProviderAttempt, purpose string, header http.Header, body json.RawMessage) map[string]any {
	view := map[string]any{
		"purpose":  purpose,
		"provider": adapter.provider.ID,
		"settings": adapter.target.Config(),
		"service":  map[string]any{"id": adapter.service.ID, "name": adapter.service.Name},
	}
	if adapter.service.HTTP != nil {
		view["baseUrl"] = adapter.service.HTTP.BaseURL
	}
	if purpose == "chat" {
		view["protocol"] = attempt.Protocol
		view["model"] = attempt.Model
		view["stream"] = attempt.Stream
		view["attempt"] = attempt.Index
		headers := map[string]string{}
		for _, name := range providerHeaders {
			if value := header.Get(name); value != "" {
				headers[name] = value
			}
		}
		view["headers"] = headers
	}
	if body != nil {
		view["body"] = body
	}
	return view
}

// providerRequest is what buildRequest returns.
type providerRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
	Stream  *bool             `json:"stream"`
}

// outbound turns what buildRequest returned into the request the gateway
// sends, and the target it sends it to: the provider's address alone, which
// the URL must stay on.
func (adapter *adapter) outbound(ctx context.Context, built providerRequest, target transport.Target) (*http.Request, transport.Target, error) {
	base := target.BaseURL
	if adapter.service.HTTP != nil {
		if configured, err := url.Parse(adapter.service.HTTP.BaseURL); err == nil && configured.Host != "" {
			base = configured
		}
	}
	if base == nil {
		return nil, target, errors.New("the provider has no address")
	}
	method := strings.ToUpper(strings.TrimSpace(built.Method))
	switch method {
	case "":
		method = http.MethodPost
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil, target, fmt.Errorf("buildRequest returned the method %q", built.Method)
	}
	address, err := url.Parse(built.URL)
	if err != nil || built.URL == "" {
		return nil, target, fmt.Errorf("buildRequest returned no usable url")
	}
	switch {
	case address.IsAbs():
		if !strings.EqualFold(address.Scheme, base.Scheme) || !strings.EqualFold(address.Host, base.Host) || address.User != nil {
			return nil, target, fmt.Errorf("buildRequest addressed %s, outside the provider's address %s://%s", address.Host, base.Scheme, base.Host)
		}
	case strings.HasPrefix(built.URL, "/") && !strings.HasPrefix(built.URL, "//"):
		joined := *base
		joined.Path = strings.TrimSuffix(base.Path, "/") + address.Path
		joined.RawPath = ""
		joined.RawQuery = address.RawQuery
		address = &joined
	default:
		return nil, target, fmt.Errorf("buildRequest must return a url that starts with / or an address on the provider's own host")
	}
	address.Fragment = ""
	var body io.Reader
	contentType := ""
	if trimmed := bytes.TrimSpace(built.Body); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		var text string
		if trimmed[0] == '"' && json.Unmarshal(trimmed, &text) == nil {
			body = strings.NewReader(text)
		} else {
			body = bytes.NewReader(trimmed)
			contentType = "application/json"
		}
	}
	// The target holds the origin alone: the path is the request's.
	origin := &url.URL{Scheme: address.Scheme, Host: address.Host}
	path := &url.URL{Path: address.Path, RawQuery: address.RawQuery}
	request, err := http.NewRequestWithContext(ctx, method, path.String(), body)
	if err != nil {
		return nil, target, err
	}
	request.URL = path
	for name, value := range built.Headers {
		if http.CanonicalHeaderKey(name) == "Host" || strings.ContainsAny(name+value, "\r\n") {
			continue
		}
		request.Header.Set(name, value)
	}
	if contentType != "" && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", contentType)
	}
	sent := target
	sent.BaseURL = origin
	return request, sent, nil
}

// upstreamView is an upstream response as parseResponse and parseError see
// it.
func upstreamView(response *http.Response, body []byte) map[string]any {
	headers := map[string]string{}
	for name, values := range response.Header {
		headers[strings.ToLower(name)] = strings.Join(values, ", ")
	}
	view := map[string]any{"status": response.StatusCode, "headers": headers, "body": string(body)}
	if trimmed := bytes.TrimSpace(body); len(trimmed) > 0 && json.Valid(trimmed) {
		view["json"] = json.RawMessage(trimmed)
	}
	return view
}

// ForwardProvider serves an attempt on a provider of kind plugin. The
// plugin's buildRequest describes the upstream request, which the gateway
// sends through its transport with the provider's key; parseResponse, or
// parseStreamEvent for each upstream event, turns the answer into the
// response the attempt's protocol expects. What follows the attempt, the
// conversion back to the client's protocol, the privacy restore, the
// records and the price, sees that response as any upstream's.
func (host *Host) ForwardProvider(writer http.ResponseWriter, request *http.Request, target transport.Target, attempt ProviderAttempt, roundTrip RoundTrip) error {
	if target.Service.Plugin != nil && target.Service.Plugin.Provider == contract.PluginProviderRelay {
		return host.forwardRelay(writer, request, target, attempt, roundTrip)
	}
	adapter, err := host.providerFor(target.Service)
	if err != nil {
		return transport.NewUpstreamError(err)
	}
	modes, served := adapter.provider.Protocols[attempt.Protocol]
	if !served || (attempt.Stream && !modes.Stream) || (!attempt.Stream && !modes.Synchronous()) {
		return transport.NewUpstreamError(fmt.Errorf("the plugin's provider does not serve %s %s", attempt.Protocol, map[bool]string{true: "streams", false: "whole responses"}[attempt.Stream]))
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return transport.NewUpstreamError(err)
	}
	if !json.Valid(body) {
		return transport.NewUpstreamError(errors.New("the request body is not JSON"))
	}
	ctx := request.Context()
	view := adapter.context(attempt, "chat", request.Header, body)
	raw, err := adapter.call(ctx, plugin.HookBuildRequest, contract.PluginHookProvider, view)
	if err != nil {
		return transport.NewUpstreamError(fmt.Errorf("buildRequest: %w", err))
	}
	var built providerRequest
	if err := json.Unmarshal(raw, &built); err != nil {
		return transport.NewUpstreamError(errors.New("buildRequest returned no request"))
	}
	stream := attempt.Stream
	if built.Stream != nil {
		stream = *built.Stream
	}
	outbound, sent, err := adapter.outbound(ctx, built, target)
	if err != nil {
		return transport.NewUpstreamError(err)
	}
	// The hooks that read the answer do without the request body.
	delete(view, "body")
	response, err := roundTrip(outbound, sent)
	if err != nil {
		return err
	}
	answer, err := adapter.answer(ctx, view, attempt, stream, response)
	if err != nil {
		return err
	}
	if target.HandleResponse != nil {
		return target.HandleResponse(answer)
	}
	return transport.WriteResponse(writer, answer)
}

// answer turns the upstream response into the attempt's protocol.
func (adapter *adapter) answer(ctx context.Context, view map[string]any, attempt ProviderAttempt, stream bool, response *http.Response) (*http.Response, error) {
	if response.StatusCode >= 400 {
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxProviderErrorBytes))
		return adapter.failed(ctx, view, attempt.Protocol, response, body), nil
	}
	if !attempt.Stream {
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes+1))
		if err != nil {
			return nil, transport.NewUpstreamError(err)
		}
		if len(body) > maxProviderResponseBytes {
			return nil, transport.NewUpstreamError(errors.New("the upstream response is larger than the adapter reads"))
		}
		raw, err := adapter.call(ctx, plugin.HookParseResponse, contract.PluginHookProvider, view, upstreamView(response, body))
		if err != nil {
			return nil, transport.NewUpstreamError(fmt.Errorf("parseResponse: %w", err))
		}
		var parsed struct {
			Status int             `json:"status"`
			JSON   json.RawMessage `json:"json"`
		}
		if json.Unmarshal(raw, &parsed) != nil || len(bytes.TrimSpace(parsed.JSON)) == 0 {
			return nil, transport.NewUpstreamError(errors.New("parseResponse returned no json"))
		}
		status := http.StatusOK
		if parsed.Status >= 200 && parsed.Status < 600 {
			status = parsed.Status
		}
		return synthesized(status, "application/json", io.NopCloser(bytes.NewReader(parsed.JSON))), nil
	}
	reader, writer := io.Pipe()
	go adapter.relay(ctx, view, stream, response, writer)
	answer := synthesized(http.StatusOK, "text/event-stream; charset=utf-8", reader)
	answer.Header.Set("Cache-Control", "no-cache")
	return answer, nil
}

func synthesized(status int, contentType string, body io.ReadCloser) *http.Response {
	header := http.Header{}
	header.Set("Content-Type", contentType)
	return &http.Response{StatusCode: status, Status: strconv.Itoa(status) + " " + http.StatusText(status), Header: header, Body: body, ProtoMajor: 1, ProtoMinor: 1}
}

// failed is an upstream error in the attempt's protocol: what parseError
// says of it, else its own status with the upstream's message. A retryable
// error keeps a status the gateway fails over on, and one that is not keeps
// a status it does not.
func (adapter *adapter) failed(ctx context.Context, view map[string]any, protocol contract.ProtocolID, response *http.Response, body []byte) *http.Response {
	status, code, message := response.StatusCode, "upstream_error", strings.TrimSpace(string(body))
	if adapter.target.Has(plugin.HookParseError) {
		if raw, err := adapter.call(ctx, plugin.HookParseError, contract.PluginHookProvider, view, upstreamView(response, body)); err == nil {
			var parsed struct {
				Status    int    `json:"status"`
				Code      string `json:"code"`
				Message   string `json:"message"`
				Retryable *bool  `json:"retryable"`
			}
			if json.Unmarshal(raw, &parsed) == nil {
				if parsed.Status >= 400 && parsed.Status < 600 {
					status = parsed.Status
				}
				if parsed.Code != "" {
					code = parsed.Code
				}
				if parsed.Message != "" {
					message = parsed.Message
				}
				if parsed.Retryable != nil {
					switch retryable := *parsed.Retryable; {
					case retryable && status < 500 && status != http.StatusTooManyRequests:
						status = http.StatusServiceUnavailable
					case !retryable && (status >= 500 || status == http.StatusTooManyRequests):
						status = http.StatusBadRequest
					}
				}
			}
		}
	}
	if len(message) > 2000 {
		message = message[:2000]
	}
	answer := synthesized(status, "application/json", io.NopCloser(bytes.NewReader(protocolError(protocol, status, code, message))))
	if retryAfter := response.Header.Get("Retry-After"); retryAfter != "" {
		answer.Header.Set("Retry-After", retryAfter)
	}
	return answer
}

// protocolError is an error body in the shape a protocol's clients read.
func protocolError(protocol contract.ProtocolID, status int, code, message string) []byte {
	var body any
	switch protocol {
	case contract.ProtocolAnthropicMessages:
		body = map[string]any{"type": "error", "error": map[string]any{"type": code, "message": message}}
	case contract.ProtocolGoogleGenerateContent:
		body = map[string]any{"error": map[string]any{"code": status, "message": message, "status": code}}
	default:
		body = map[string]any{"error": map[string]any{"message": message, "type": code, "code": code}}
	}
	data, _ := json.Marshal(body)
	return data
}

// streamStep is what parseStreamEvent and finishStream return.
type streamStep struct {
	Events []struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	} `json:"events"`
	State json.RawMessage `json:"state"`
	Done  bool            `json:"done"`
}

// relay reads the upstream stream event by event, has parseStreamEvent turn
// each into the protocol's events, and writes them as server-sent events.
// A failure ends the stream with it.
func (adapter *adapter) relay(ctx context.Context, view map[string]any, sse bool, response *http.Response, out *io.PipeWriter) {
	defer response.Body.Close()
	state := json.RawMessage("null")
	write := func(step streamStep) error {
		if len(bytes.TrimSpace(step.State)) > 0 {
			state = step.State
		}
		for _, event := range step.Events {
			if err := writeServerSentEvent(out, event.Event, event.Data); err != nil {
				return err
			}
		}
		return nil
	}
	finish := func() error {
		if !adapter.target.Has(plugin.HookFinishStream) {
			return nil
		}
		raw, err := adapter.call(ctx, plugin.HookFinishStream, contract.PluginHookProvider, view, state)
		if err != nil {
			return fmt.Errorf("finishStream: %w", err)
		}
		var step streamStep
		if len(bytes.TrimSpace(raw)) > 0 && json.Unmarshal(raw, &step) != nil {
			return errors.New("finishStream returned no events")
		}
		return write(step)
	}
	mode := streamMode(response.Header.Get("Content-Type"), sse)
	err := readEvents(response.Body, mode, func(event map[string]any) (bool, error) {
		raw, err := adapter.call(ctx, plugin.HookParseStreamEvent, contract.PluginHookProviderEvent, view, event, state)
		if err != nil {
			return false, fmt.Errorf("parseStreamEvent: %w", err)
		}
		var step streamStep
		if len(bytes.TrimSpace(raw)) > 0 && json.Unmarshal(raw, &step) != nil {
			return false, errors.New("parseStreamEvent returned no events")
		}
		if err := write(step); err != nil {
			return false, err
		}
		return step.Done, nil
	})
	if err == nil {
		err = finish()
	}
	if err != nil {
		_ = out.CloseWithError(err)
		return
	}
	_ = out.Close()
}

// How an upstream answer to a streamed attempt comes.
const (
	// streamSSE is server-sent events.
	streamSSE = iota
	// streamLines is one JSON value a line.
	streamLines
	// streamWhole is a whole response, one event: the plugin asked the
	// upstream for no stream.
	streamWhole
)

// streamMode reads how an upstream answer comes from its type, and from
// whether the plugin asked for a stream when the type says nothing.
func streamMode(contentType string, asked bool) int {
	contentType = strings.ToLower(contentType)
	switch {
	case strings.Contains(contentType, "event-stream"):
		return streamSSE
	case strings.Contains(contentType, "ndjson") || strings.Contains(contentType, "jsonl") || strings.Contains(contentType, "json-seq"):
		return streamLines
	case asked:
		return streamSSE
	}
	return streamWhole
}

// readEvents calls each with the upstream's events until it says done or
// the stream ends.
func readEvents(body io.Reader, mode int, each func(map[string]any) (bool, error)) error {
	if mode == streamWhole {
		whole, err := io.ReadAll(io.LimitReader(body, maxProviderResponseBytes+1))
		if err != nil {
			return err
		}
		if len(whole) > maxProviderResponseBytes {
			return errors.New("the upstream response is larger than the adapter reads")
		}
		event := map[string]any{"data": string(whole)}
		if trimmed := bytes.TrimSpace(whole); len(trimmed) > 0 && json.Valid(trimmed) {
			event["json"] = json.RawMessage(trimmed)
		}
		_, err = each(event)
		return err
	}
	sse := mode == streamSSE
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), maxProviderEventBytes)
	var name, id string
	var data []string
	dispatch := func() (bool, error) {
		if len(data) == 0 {
			name, id = "", ""
			return false, nil
		}
		joined := strings.Join(data, "\n")
		event := map[string]any{"data": joined}
		if name != "" {
			event["event"] = name
		}
		if id != "" {
			event["id"] = id
		}
		if trimmed := strings.TrimSpace(joined); trimmed != "" && json.Valid([]byte(trimmed)) {
			event["json"] = json.RawMessage(trimmed)
		}
		name, id, data = "", "", nil
		return each(event)
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if !sse {
			if strings.TrimSpace(line) == "" {
				continue
			}
			data = []string{line}
			if done, err := dispatch(); err != nil || done {
				return err
			}
			continue
		}
		switch {
		case line == "":
			if done, err := dispatch(); err != nil || done {
				return err
			}
		case strings.HasPrefix(line, ":"):
		default:
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				name = value
			case "data":
				data = append(data, value)
			case "id":
				id = value
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if _, err := dispatch(); err != nil {
		return err
	}
	return nil
}

// writeServerSentEvent writes one event: its name when set, and its data,
// a string as it is and anything else as JSON.
func writeServerSentEvent(out io.Writer, name string, data json.RawMessage) error {
	var text string
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		if json.Unmarshal(trimmed, &text) != nil {
			return errors.New("an event's data is not JSON")
		}
	} else {
		text = string(trimmed)
	}
	var frame strings.Builder
	if name = strings.TrimSpace(name); name != "" && !strings.ContainsAny(name, "\r\n") {
		frame.WriteString("event: " + name + "\n")
	}
	for line := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		frame.WriteString("data: " + line + "\n")
	}
	frame.WriteString("\n")
	_, err := io.WriteString(out, frame.String())
	return err
}

// ProviderModels asks the plugin for the models of a provider of kind
// plugin: buildRequest describes the model list's request, purpose
// "models", and parseResponse returns { models }. ok is false when the
// plugin lists none.
func (host *Host) ProviderModels(ctx context.Context, service contract.Service, target transport.Target, roundTrip RoundTrip) (models []string, ok bool, err error) {
	if service.Plugin != nil && service.Plugin.Provider == contract.PluginProviderRelay {
		return host.relayModels(ctx, service, target, roundTrip)
	}
	adapter, err := host.providerFor(service)
	if err != nil {
		return nil, false, err
	}
	view := adapter.context(ProviderAttempt{}, "models", nil, nil)
	raw, err := adapter.call(ctx, plugin.HookBuildRequest, contract.PluginHookProvider, view)
	if err != nil {
		return nil, false, fmt.Errorf("buildRequest: %w", err)
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, false, nil
	}
	var built providerRequest
	if err := json.Unmarshal(raw, &built); err != nil {
		return nil, false, errors.New("buildRequest returned no request")
	}
	if built.Method == "" {
		built.Method = http.MethodGet
	}
	outbound, sent, err := adapter.outbound(ctx, built, target)
	if err != nil {
		return nil, false, err
	}
	response, err := roundTrip(outbound, sent)
	if err != nil {
		return nil, false, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxProviderErrorBytes*8))
	if err != nil {
		return nil, false, err
	}
	if response.StatusCode >= 400 {
		return nil, false, fmt.Errorf("the provider answered %d", response.StatusCode)
	}
	raw, err = adapter.call(ctx, plugin.HookParseResponse, contract.PluginHookProvider, view, upstreamView(response, body))
	if err != nil {
		return nil, false, fmt.Errorf("parseResponse: %w", err)
	}
	var parsed struct {
		Models []string `json:"models"`
	}
	if json.Unmarshal(raw, &parsed) != nil {
		return nil, false, errors.New("parseResponse returned no models")
	}
	return parsed.Models, true, nil
}
