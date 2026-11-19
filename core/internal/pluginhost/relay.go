package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/jsonpatch"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// A provider of a new-api plugin's provider part relays requests over the
// plugin's meta.relay routes: the route that serves the client path and
// model says where the request goes and how the key is sent, and the
// plugin's prepareRequest and signRequest, when it exports them, adjust the
// request before it is sent. The gateway converts the request with
// relaykit when the route names a converter, as for any provider.

// relayHookTime bounds prepareRequest and signRequest.
const relayHookTime = 10 * time.Second

// Bounds of prepareRequest's effects.
const (
	maxRelayEffects    = 32
	maxRelayOperations = 512
	maxRelayHeaders    = 64
)

// relayProtectedHeaders are the headers prepareRequest may not touch, nor
// see: the credentials and the framing of the request.
var relayProtectedHeaders = []string{
	"Authorization", "Proxy-Authorization", "Cookie", "Host", "Content-Length", "Content-Type", "Content-Encoding",
	"Connection", "Keep-Alive", "Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
	"X-Api-Key", "X-Goog-Api-Key",
}

// relayBillingMembers are the members of a request body prepareRequest may
// not change: those the gateway bills by.
var relayBillingMembers = []string{
	"/model", "/stream", "/stream_options", "/n", "/generationConfig/candidateCount",
	"/size", "/quality", "/resolution", "/seconds", "/duration", "/service_tier",
}

// relayTarget is the plugin and route an attempt on a relay provider goes
// over.
type relayTarget struct {
	plugin *plugin.Plugin
	meta   *plugin.TaskMeta
	route  *plugin.RelayRoute
	config json.RawMessage
	// token is the exchangeKey token of the provider's key, nil without
	// one; credential is the credential the plugin logged the provider in
	// with, nil for a key.
	token      json.RawMessage
	credential *PluginCredential
}

// authFor sends what authenticates a request: the credential's headers and
// query, else key as the route says. It returns the headers and the header
// that holds the key.
func (relay *relayTarget) authFor(key string, query url.Values) (http.Header, string) {
	if relay.credential == nil && relay.route == nil {
		// A request outside a route, such as a balance, carries the key as
		// the plugin writes it.
		return http.Header{}, ""
	}
	if relay.credential == nil {
		return relayAuth(relay.route, key, query)
	}
	headers := http.Header{}
	for name, value := range relay.credential.Headers {
		headers.Set(name, value)
	}
	for name, value := range relay.credential.Query {
		query.Set(name, value)
	}
	return headers, ""
}

// authQueryNames are the query parameters authFor sets.
func (relay *relayTarget) authQueryNames() []string {
	var names []string
	if relay.credential != nil {
		for name := range relay.credential.Query {
			names = append(names, name)
		}
		return names
	}
	if relay.route == nil {
		return names
	}
	if auth := relay.route.Auth; auth != nil && auth.Type == "query" {
		names = append(names, auth.Name)
	}
	return names
}

// useCredential takes the provider's credential when its secret is one,
// refreshed first when it expires soon or force is set. It returns the key
// requests send, "" for a credential.
func (host *Host) useCredential(ctx context.Context, service contract.Service, relay *relayTarget, baseURL, key string, force bool) (string, error) {
	credential, ok := decodeCredential(key)
	if relay.credential != nil {
		credential, ok = *relay.credential, true
	}
	if !ok {
		return key, nil
	}
	live, err := host.liveCredential(ctx, service, relay, baseURL, credential, force)
	if err != nil {
		return "", err
	}
	relay.credential = &live
	return "", nil
}

// relayFor finds the route of the service's plugin that serves the attempt.
func (host *Host) relayFor(service contract.Service, attempt ProviderAttempt) (*relayTarget, error) {
	if host == nil || host.plugins == nil || service.Plugin == nil {
		return nil, errProviderUnavailable
	}
	target := host.plugins.Current().Plugin(service.Plugin.Key)
	if target == nil || !target.Enabled() {
		return nil, errProviderUnavailable
	}
	meta := target.NewAPI()
	if meta == nil || len(meta.Relay) == 0 {
		return nil, errProviderUnavailable
	}
	route := meta.RelayRouteFor(attempt.ClientPath, attempt.Model)
	switch {
	case route == nil:
		return nil, fmt.Errorf("the plugin's routes do not serve %s for %s", attempt.ClientPath, attempt.Model)
	case route.UpstreamProtocol() != attempt.Protocol:
		return nil, fmt.Errorf("the route of %s for %s sends %s, not %s", attempt.ClientPath, attempt.Model, route.UpstreamProtocol(), attempt.Protocol)
	}
	return &relayTarget{plugin: target, meta: meta, route: route, config: RelayConfig(meta, service.Plugin.Settings)}, nil
}

// RelayUpstreamProtocol is the protocol the route of a relay provider's
// plugin that serves model on the client path sends upstream: the client's
// when the route sends the request as it came, a relaykit converter's
// upstream protocol otherwise. ok is false when no route the gateway runs
// serves it.
func (host *Host) RelayUpstreamProtocol(service contract.Service, clientPath, model string) (contract.ProtocolID, bool) {
	if host == nil || host.plugins == nil || service.Plugin == nil || service.Plugin.Provider != contract.PluginProviderRelay {
		return "", false
	}
	target := host.plugins.Current().Plugin(service.Plugin.Key)
	if target == nil || target.NewAPI() == nil {
		return "", false
	}
	route := target.NewAPI().RelayRouteFor(clientPath, model)
	if route == nil {
		return "", false
	}
	return route.UpstreamProtocol(), true
}

// RelayConfig is ctx.config of a relay provider's hooks: its settings over
// the defaults of the plugin's channel config, with only the fields the
// schema declares when it has one.
func RelayConfig(meta *plugin.TaskMeta, settings json.RawMessage) json.RawMessage {
	merged := meta.ChannelConfigDefaults()
	var stored map[string]json.RawMessage
	if len(settings) > 0 && json.Unmarshal(settings, &stored) == nil {
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		_ = json.Unmarshal(meta.ChannelConfig, &schema)
		for name, value := range stored {
			if _, declared := schema.Properties[name]; declared {
				merged[name] = value
			}
		}
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return json.RawMessage("{}")
	}
	return encoded
}

// serviceKey is the provider's key, as the gateway put it in the headers
// of the attempt.
func serviceKey(service contract.Service, headers http.Header) string {
	if service.HTTP == nil {
		return ""
	}
	switch service.HTTP.Auth.Scheme {
	case contract.AuthSchemeBearer:
		value, _ := strings.CutPrefix(headers.Get("Authorization"), "Bearer ")
		return value
	case contract.AuthSchemeAnthropicAPIKey:
		return headers.Get("X-Api-Key")
	case contract.AuthSchemeGoogleAPIKey:
		return headers.Get("X-Goog-Api-Key")
	case contract.AuthSchemeCustomHeader:
		return headers.Get(service.HTTP.Auth.HeaderName)
	}
	return ""
}

// relayAddress is where a route sends the request: its upstream path on the
// provider's address, or on the address of a url field of the config. The
// root it returns is what a hook's path replaces the route's path on.
func relayAddress(base *url.URL, route *plugin.RelayRoute, model string, stream bool, config json.RawMessage) (*url.URL, *url.URL, error) {
	upstream := strings.ReplaceAll(route.UpstreamPath, "{model}", url.PathEscape(model))
	if stream && route.UpstreamProtocol() == contract.ProtocolGoogleGenerateContent {
		upstream = strings.Replace(upstream, ":generateContent", ":streamGenerateContent", 1)
	}
	root := base
	if name, rest, found := strings.Cut(strings.TrimPrefix(upstream, "{config."), "}"); found && strings.HasPrefix(upstream, "{config.") {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(config, &fields)
		var address string
		_ = json.Unmarshal(fields[name], &address)
		if address = strings.TrimRight(address, "/"); address != "" {
			parsed, err := url.Parse(address)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
				return nil, nil, fmt.Errorf("the provider's setting %s is not an http or https address", name)
			}
			root = parsed
		}
		upstream = rest
		if upstream == "" {
			upstream = "/"
		}
	}
	if !strings.HasPrefix(upstream, "/") {
		parsed, err := url.Parse(upstream)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return nil, nil, fmt.Errorf("the route's upstream_path %q is not an address", route.UpstreamPath)
		}
		return parsed, &url.URL{Scheme: parsed.Scheme, Host: parsed.Host}, nil
	}
	if root == nil {
		return nil, nil, errors.New("the provider has no address")
	}
	address, err := joinPath(root, upstream)
	return address, root, err
}

// joinPath puts path, which may have a query, after the path of base.
func joinPath(base *url.URL, path string) (*url.URL, error) {
	relative, err := url.Parse(path)
	if err != nil || relative.IsAbs() || relative.Host != "" || relative.Fragment != "" {
		return nil, fmt.Errorf("%q is not a path", path)
	}
	for _, segment := range strings.Split(relative.Path, "/") {
		if segment == ".." {
			return nil, fmt.Errorf("%q leaves the provider's path", path)
		}
	}
	joined := *base
	joined.Path = strings.TrimSuffix(base.Path, "/") + relative.Path
	joined.RawPath = ""
	joined.RawQuery = relative.RawQuery
	joined.Fragment = ""
	return &joined, nil
}

// relayEffect is one effect prepareRequest returns.
type relayEffect struct {
	Type   string                `json:"type"`
	Ops    []jsonpatch.Operation `json:"ops"`
	Set    map[string]string     `json:"set"`
	Remove []string              `json:"remove"`
}

// relayPrepared is what prepareRequest returns.
type relayPrepared struct {
	Effects []relayEffect `json:"effects"`
	Path    string        `json:"path"`
	URL     string        `json:"url"`
}

// relayRefusal is a request the plugin's effects cannot apply to: the
// gateway answers it without trying another provider.
type relayRefusal struct{ message string }

func (refusal *relayRefusal) Error() string { return refusal.message }

// prepare runs prepareRequest and applies its effects to body and header,
// all or none, and returns where the request goes when the plugin moved it.
func (relay *relayTarget) prepare(ctx context.Context, baseURL, clientPath, model string, stream bool, address, root *url.URL, body []byte, header http.Header, authHeader string) ([]byte, *url.URL, error) {
	document, err := jsonpatch.Parse(body)
	if err != nil {
		// prepareRequest runs on JSON bodies only.
		return body, address, nil
	}
	visible := map[string]string{}
	for name, values := range header {
		canonical := http.CanonicalHeaderKey(name)
		if relayProtected(canonical, authHeader) || strings.HasPrefix(canonical, "X-Astrlink-") || len(values) == 0 {
			continue
		}
		visible[canonical] = values[0]
	}
	view := map[string]any{"baseUrl": baseURL, "path": clientPath, "config": relay.config}
	if relay.token != nil {
		view["token"] = relay.token
	}
	request := map[string]any{
		"path": clientPath, "upstreamPath": address.Path, "model": model, "stream": stream,
		"body": json.RawMessage(body), "headers": visible,
	}
	raw, err := relay.call(ctx, "prepareRequest", view, request)
	if err != nil {
		return nil, nil, fmt.Errorf("prepareRequest: %w", err)
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return body, address, nil
	}
	var prepared relayPrepared
	if err := json.Unmarshal(raw, &prepared); err != nil {
		return nil, nil, errors.New("prepareRequest returned neither null nor {effects, path, url}")
	}
	if len(prepared.Effects) > maxRelayEffects {
		return nil, nil, &relayRefusal{fmt.Sprintf("prepareRequest returned more than %d effects", maxRelayEffects)}
	}
	before := map[string]string{}
	for _, member := range relayBillingMembers {
		if value, ok := document.Get(member); ok {
			before[member] = string(value)
		}
	}
	changed := header.Clone()
	operations := 0
	patched := false
	for index, effect := range prepared.Effects {
		switch effect.Type {
		case "json.patch":
			operations += len(effect.Ops)
			if operations > maxRelayOperations {
				return nil, nil, &relayRefusal{fmt.Sprintf("prepareRequest's patches hold more than %d operations", maxRelayOperations)}
			}
			if err := document.Apply(effect.Ops); err != nil {
				return nil, nil, &relayRefusal{fmt.Sprintf("prepareRequest's effect %d: %v", index, err)}
			}
			patched = true
		case "header.patch":
			if len(effect.Set)+len(effect.Remove) > maxRelayHeaders {
				return nil, nil, &relayRefusal{fmt.Sprintf("prepareRequest's effect %d changes more than %d headers", index, maxRelayHeaders)}
			}
			for _, name := range effect.Remove {
				if relayProtected(http.CanonicalHeaderKey(name), authHeader) {
					return nil, nil, &relayRefusal{fmt.Sprintf("prepareRequest may not remove the header %s", name)}
				}
				changed.Del(name)
			}
			for name, value := range effect.Set {
				if relayProtected(http.CanonicalHeaderKey(name), authHeader) || !validHeader(name, value) {
					return nil, nil, &relayRefusal{fmt.Sprintf("prepareRequest may not set the header %s", name)}
				}
				changed.Set(name, value)
			}
		default:
			return nil, nil, &relayRefusal{fmt.Sprintf("prepareRequest's effect %d has the type %q, not json.patch or header.patch", index, effect.Type)}
		}
	}
	for _, member := range relayBillingMembers {
		value, ok := document.Get(member)
		if was, had := before[member]; ok != had || (ok && string(value) != was) {
			return nil, nil, &relayRefusal{fmt.Sprintf("prepareRequest may not change %s, which the gateway bills by", strings.TrimPrefix(member, "/"))}
		}
	}
	moved := address
	switch {
	case prepared.Path != "" && prepared.URL != "":
		return nil, nil, errors.New("prepareRequest returned both path and url")
	case prepared.Path != "":
		if !strings.HasPrefix(prepared.Path, "/") || strings.HasPrefix(prepared.Path, "//") {
			return nil, nil, fmt.Errorf("prepareRequest's path %q does not start with a single /", prepared.Path)
		}
		moved, err = joinPath(root, prepared.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("prepareRequest's path: %w", err)
		}
	case prepared.URL != "":
		parsed, err := url.Parse(prepared.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
			return nil, nil, fmt.Errorf("prepareRequest's url %q is not an http or https address", prepared.URL)
		}
		moved = parsed
	}
	for name := range header {
		delete(header, name)
	}
	for name, values := range changed {
		header[name] = values
	}
	if !patched {
		return body, moved, nil
	}
	encoded, err := document.Bytes()
	if err != nil {
		return nil, nil, err
	}
	return encoded, moved, nil
}

func relayProtected(canonical, authHeader string) bool {
	return slices.Contains(relayProtectedHeaders, canonical) || (authHeader != "" && canonical == http.CanonicalHeaderKey(authHeader))
}

func validHeader(name, value string) bool {
	return name != "" && len(name) <= 128 && len(value) <= 8<<10 && !strings.ContainsAny(name, " \r\n:") && !strings.ContainsAny(value, "\r\n")
}

// sign runs signRequest on the final request and returns the headers and
// query it adds.
func (relay *relayTarget) sign(ctx context.Context, baseURL, key, method string, address *url.URL, header http.Header, body []byte) (map[string]string, map[string]string, error) {
	sum := sha256.Sum256(body)
	headers := map[string]string{}
	for name, values := range header {
		if len(values) > 0 {
			headers[http.CanonicalHeaderKey(name)] = values[0]
		}
	}
	var credential any
	if relay.credential != nil {
		credential = jsonOrNull(relay.credential.Data)
	}
	view := map[string]any{"baseUrl": baseURL, "credential": credential, "apiKey": key, "config": relay.config}
	if relay.token != nil {
		view["token"] = relay.token
	}
	request := map[string]any{"method": method, "url": address.String(), "headers": headers, "bodySha256": hex.EncodeToString(sum[:])}
	raw, err := relay.call(ctx, "signRequest", view, request)
	if err != nil {
		return nil, nil, fmt.Errorf("signRequest: %w", err)
	}
	var signed struct {
		Headers map[string]string `json:"headers"`
		Query   map[string]string `json:"query"`
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		if err := json.Unmarshal(raw, &signed); err != nil {
			return nil, nil, errors.New("signRequest returned neither {headers, query} nor nothing")
		}
	}
	for name, value := range signed.Headers {
		if !validHeader(name, value) || http.CanonicalHeaderKey(name) == "Host" || http.CanonicalHeaderKey(name) == "Content-Length" {
			return nil, nil, fmt.Errorf("signRequest may not set the header %s", name)
		}
	}
	return signed.Headers, signed.Query, nil
}

// call runs one of the plugin's provider hooks.
func (relay *relayTarget) call(ctx context.Context, hook string, args ...any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, relayHookTime)
	defer cancel()
	result, _, err := relay.plugin.CallTask(ctx, hook, args, plugin.CallOptions{})
	return result, err
}

// forwardRelay sends an attempt on a provider of a new-api plugin's
// provider part.
func (host *Host) forwardRelay(writer http.ResponseWriter, request *http.Request, target transport.Target, attempt ProviderAttempt, roundTrip RoundTrip) error {
	relay, err := host.relayFor(target.Service, attempt)
	if err != nil {
		return transport.NewUpstreamError(err)
	}
	if resting, notice := host.resting(target.Service.ID); resting {
		// The plugin said the account cannot serve now: another provider
		// takes the request until it says it can.
		return transport.NewUpstreamError(fmt.Errorf("the plugin rests this provider: %s", notice))
	}
	base := target.BaseURL
	if target.Service.HTTP != nil {
		if configured, err := url.Parse(target.Service.HTTP.BaseURL); err == nil && configured.Host != "" {
			base = configured
		}
	}
	if base == nil && relay.meta.BaseURL != "" {
		base, _ = url.Parse(relay.meta.BaseURL)
	}
	address, root, err := relayAddress(base, relay.route, attempt.Model, attempt.Stream, relay.config)
	if err != nil {
		return transport.NewUpstreamError(err)
	}
	// The client's query goes along, without a key it may carry.
	query := address.Query()
	for name, values := range request.URL.Query() {
		if name == "key" || query.Has(name) {
			continue
		}
		query[name] = values
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return transport.NewUpstreamError(err)
	}
	baseURL := ""
	if base != nil {
		baseURL = strings.TrimRight(base.String(), "/")
	}
	ctx := request.Context()
	key, err := host.useCredential(ctx, target.Service, relay, baseURL, serviceKey(target.Service, target.RequestHeaders), false)
	if err != nil {
		return transport.NewUpstreamError(err)
	}
	_, authHeader := relay.authFor(key, query)
	header := request.Header.Clone()
	if failed, err := host.exchange(ctx, target.Service, relay, base, key, false); failed != nil || err != nil {
		if err != nil {
			return transport.NewUpstreamError(err)
		}
		return answerRelay(writer, target, failed)
	}
	if relay.route.Plugin() {
		header.Del("Authorization")
		plain := url.Values{}
		for name, values := range query {
			if !slices.Contains(relay.authQueryNames(), name) {
				plain[name] = slices.Clone(values)
			}
		}
		send := &relaySend{
			host: host, relay: relay, target: target, roundTrip: roundTrip, root: root, address: address, base: base,
			baseURL: baseURL, key: key, query: plain, header: header, authHeader: authHeader,
		}
		return host.forwardConverted(ctx, writer, target, attempt, send, request.Method, body, attempt.Stream)
	}
	if relay.plugin.HasTaskHook("prepareRequest") && len(body) > 0 {
		address.RawQuery = query.Encode()
		prepared, moved, err := relay.prepare(ctx, baseURL, attempt.ClientPath, attempt.Model, attempt.Stream, address, root, body, header, authHeader)
		var refusal *relayRefusal
		if errors.As(err, &refusal) {
			return writeRelayRefusal(writer, target, refusal)
		}
		if err != nil {
			return transport.NewUpstreamError(err)
		}
		if moved != address {
			movedQuery := moved.Query()
			for name, values := range query {
				if !movedQuery.Has(name) {
					movedQuery[name] = values
				}
			}
			query = movedQuery
		}
		body, address = prepared, moved
	}
	header.Del("Authorization")
	unauthenticated := url.Values{}
	for name, values := range query {
		if !slices.Contains(relay.authQueryNames(), name) {
			unauthenticated[name] = slices.Clone(values)
		}
	}
	for resend := false; ; resend = true {
		query := url.Values{}
		for name, values := range unauthenticated {
			query[name] = slices.Clone(values)
		}
		sentCredentials, _ := relay.authFor(key, query)
		address.RawQuery = query.Encode()
		if relay.plugin.HasTaskHook("signRequest") {
			final := header.Clone()
			for name, values := range sentCredentials {
				final[name] = values
			}
			headers, extra, err := relay.sign(ctx, baseURL, key, request.Method, address, final, body)
			if err != nil {
				return transport.NewUpstreamError(err)
			}
			for name, value := range headers {
				sentCredentials.Set(name, value)
			}
			for name, value := range extra {
				query.Set(name, value)
			}
			address.RawQuery = query.Encode()
		}
		outbound, err := http.NewRequestWithContext(ctx, request.Method, (&url.URL{Path: address.Path, RawQuery: address.RawQuery}).String(), bytes.NewReader(body))
		if err != nil {
			return transport.NewUpstreamError(err)
		}
		outbound.Header = header.Clone()
		outbound.Header.Set("Content-Length", strconv.Itoa(len(body)))
		outbound.ContentLength = int64(len(body))
		sent := target
		sent.BaseURL = &url.URL{Scheme: address.Scheme, Host: address.Host}
		sent.RequestHeaders = sentCredentials
		response, err := roundTrip(outbound, sent)
		if err != nil {
			return err
		}
		refreshes := relay.credential != nil && relay.plugin.HasTaskHook("refresh")
		if !resend && (relay.plugin.HasTaskHook("exchangeKey") || refreshes) {
			rejected, kept := relay.rejected(ctx, baseURL, attempt.ClientPath, response)
			response = kept
			if rejected {
				// The credential or token is no longer good: it is
				// renewed and the same request sent once more.
				_ = response.Body.Close()
				if refreshes {
					if _, err := host.useCredential(ctx, target.Service, relay, baseURL, "", true); err != nil {
						return transport.NewUpstreamError(err)
					}
					continue
				}
				if failed, err := host.exchange(ctx, target.Service, relay, base, key, true); failed != nil || err != nil {
					if err != nil {
						return transport.NewUpstreamError(err)
					}
					return answerRelay(writer, target, failed)
				}
				continue
			}
		}
		return answerRelay(writer, target, response)
	}
}

// answerRelay hands an attempt's answer to the gateway.
func answerRelay(writer http.ResponseWriter, target transport.Target, response *http.Response) error {
	if target.HandleResponse != nil {
		return target.HandleResponse(response)
	}
	return transport.WriteResponse(writer, response)
}

// rejected reports whether the upstream rejected the token: a 401, or
// what credentialRejected says of another error. The response it returns
// still has its whole body.
func (relay *relayTarget) rejected(ctx context.Context, baseURL, clientPath string, response *http.Response) (bool, *http.Response) {
	if response.StatusCode == http.StatusUnauthorized {
		return true, response
	}
	if response.StatusCode < http.StatusBadRequest || !relay.plugin.HasTaskHook("credentialRejected") {
		return false, response
	}
	head, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	response.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), response.Body), response.Body}
	headers := map[string]string{}
	for name, values := range response.Header {
		if len(values) > 0 {
			headers[http.CanonicalHeaderKey(name)] = values[0]
		}
	}
	view := map[string]any{"baseUrl": baseURL, "path": clientPath, "config": relay.config}
	if relay.token != nil {
		view["token"] = relay.token
	}
	raw, err := relay.call(ctx, "credentialRejected", view, map[string]any{"status": response.StatusCode, "headers": headers, "body": strings.ToValidUTF8(string(head), "\uFFFD")})
	return err == nil && bytes.Equal(bytes.TrimSpace(raw), []byte("true")), response
}

// relayTokens keeps the exchangeKey tokens of relay providers in memory,
// by provider, key, plugin version, config and address.
type relayTokens struct {
	mutex  sync.Mutex
	tokens map[string]relayToken
}

type relayToken struct {
	token   json.RawMessage
	expires time.Time
}

// relayTokenMargin renews a token this long before it expires.
const relayTokenMargin = 30 * time.Second

// exchange gives relay the exchangeKey token of key, exchanging the key
// when no kept token is good or renew is set. It returns the answer of a
// key the plugin refused, or the error of an exchange that failed.
func (host *Host) exchange(ctx context.Context, service contract.Service, relay *relayTarget, base *url.URL, key string, renew bool) (*http.Response, error) {
	if !relay.plugin.HasTaskHook("exchangeKey") || key == "" {
		return nil, nil
	}
	sum := sha256.Sum256([]byte(key + "\x00" + string(relay.config)))
	baseURL := ""
	if base != nil {
		baseURL = strings.TrimRight(base.String(), "/")
	}
	cacheKey := strings.Join([]string{string(service.ID), relay.meta.Version, hex.EncodeToString(sum[:]), baseURL}, "\x00")
	host.relayTokens.mutex.Lock()
	defer host.relayTokens.mutex.Unlock()
	if kept, ok := host.relayTokens.tokens[cacheKey]; ok && !renew && (kept.expires.IsZero() || time.Until(kept.expires) > relayTokenMargin) {
		relay.token = kept.token
		return nil, nil
	}
	allowed := slices.Clone(relay.meta.AllowedHosts)
	if base != nil {
		allowed = append(allowed, strings.ToLower(base.Host))
	}
	call, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	raw, _, err := relay.plugin.CallTask(call, "exchangeKey", []any{map[string]any{"baseUrl": baseURL, "config": relay.config, "apiKey": key}}, plugin.CallOptions{
		NetworkBudget: time.Minute,
		TaskFetch: &plugin.TaskFetch{
			Allowed: func(target *url.URL) bool { return slices.Contains(allowed, strings.ToLower(target.Host)) },
			Client:  &http.Client{}, Post: true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("exchangeKey: %w", err)
	}
	var result struct {
		Token     json.RawMessage `json:"token"`
		ExpiresAt int64           `json:"expiresAt"`
		Error     *struct {
			Status  int    `json:"status"`
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, errors.New("exchangeKey returned neither {token} nor {error}")
	}
	if result.Error != nil {
		status := result.Error.Status
		if status < 400 || status > 599 {
			status = http.StatusInternalServerError
		}
		data, _ := json.Marshal(map[string]any{"error": map[string]any{"message": truncateRunes(result.Error.Message, 4096), "type": result.Error.Type, "code": result.Error.Code}})
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}, nil
	}
	if len(result.Token) == 0 || len(result.Token) > 64<<10 {
		return nil, errors.New("exchangeKey returned no token, or one larger than 64 KiB")
	}
	kept := relayToken{token: result.Token}
	if result.ExpiresAt > 0 {
		kept.expires = time.Unix(result.ExpiresAt, 0)
	}
	if host.relayTokens.tokens == nil {
		host.relayTokens.tokens = map[string]relayToken{}
	}
	host.relayTokens.tokens[cacheKey] = kept
	relay.token = kept.token
	return nil, nil
}

// relayAuth sends key as the route says: the headers it goes in, and the
// header that holds it, or into query.
func relayAuth(route *plugin.RelayRoute, key string, query url.Values) (http.Header, string) {
	credentials := http.Header{}
	switch auth := route.Auth; {
	case auth == nil:
		if key != "" {
			credentials.Set("Authorization", "Bearer "+key)
		}
		return credentials, "Authorization"
	case auth.Type == "header":
		credentials.Set(auth.Name, strings.ReplaceAll(auth.Value, "{api_key}", key))
		return credentials, auth.Name
	case auth.Type == "query":
		query.Set(auth.Name, strings.ReplaceAll(auth.Value, "{api_key}", key))
	}
	return credentials, ""
}

// relayModels lists the models of a relay provider: from its /v1/models
// route, else those the plugin lists. ok is false when it has neither.
func (host *Host) relayModels(ctx context.Context, service contract.Service, target transport.Target, roundTrip RoundTrip) ([]string, bool, error) {
	if host == nil || host.plugins == nil || service.Plugin == nil {
		return nil, false, errProviderUnavailable
	}
	found := host.plugins.Current().Plugin(service.Plugin.Key)
	if found == nil || !found.Enabled() || found.NewAPI() == nil {
		return nil, false, errProviderUnavailable
	}
	meta := found.NewAPI()
	var route *plugin.RelayRoute
	for index := range meta.Relay {
		if meta.Relay[index].IncomingPath == plugin.RelayModelsPath {
			route = &meta.Relay[index]
		}
	}
	if route == nil {
		return slices.Clone(meta.Models), len(meta.Models) > 0, nil
	}
	relay := &relayTarget{plugin: found, meta: meta, route: route, config: RelayConfig(meta, service.Plugin.Settings)}
	var state json.RawMessage
	var base *url.URL
	if service.HTTP != nil {
		base, _ = url.Parse(service.HTTP.BaseURL)
	}
	if base == nil || base.Host == "" {
		base, _ = url.Parse(meta.BaseURL)
	}
	address, _, err := relayAddress(base, route, "", false, relay.config)
	if err != nil {
		return nil, false, err
	}
	query := address.Query()
	key, err := host.useCredential(ctx, service, relay, strings.TrimRight(base.String(), "/"), serviceKey(service, target.RequestHeaders), false)
	if err != nil {
		return nil, false, err
	}
	if failed, err := host.exchange(ctx, service, relay, base, key, false); failed != nil || err != nil {
		if err == nil {
			_ = failed.Body.Close()
			err = fmt.Errorf("the plugin refused the key with %d", failed.StatusCode)
		}
		return nil, false, err
	}
	credentials, _ := relay.authFor(key, query)
	address.RawQuery = query.Encode()
	if found.HasTaskHook("signRequest") {
		headers, extra, err := relay.sign(ctx, strings.TrimRight(base.String(), "/"), key, http.MethodGet, address, credentials.Clone(), nil)
		if err != nil {
			return nil, false, err
		}
		for name, value := range headers {
			credentials.Set(name, value)
		}
		for name, value := range extra {
			query.Set(name, value)
		}
		address.RawQuery = query.Encode()
	}
	outbound, err := http.NewRequestWithContext(ctx, http.MethodGet, (&url.URL{Path: address.Path, RawQuery: address.RawQuery}).String(), nil)
	if err != nil {
		return nil, false, err
	}
	sent := target
	sent.BaseURL = &url.URL{Scheme: address.Scheme, Host: address.Host}
	sent.RequestHeaders = credentials
	response, err := roundTrip(outbound, sent)
	if err != nil {
		return nil, false, err
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxProviderErrorBytes*8))
	_ = response.Body.Close()
	if err != nil {
		return nil, false, err
	}
	// A list the plugin converts may take several pages.
	for page := 0; route.Plugin(); page++ {
		if page > maxConvertedRequests {
			return nil, false, fmt.Errorf("the model list took more than %d requests", maxConvertedRequests)
		}
		view := map[string]any{"baseUrl": strings.TrimRight(base.String(), "/"), "path": plugin.RelayModelsPath, "config": relay.config, "state": jsonOrNull(state)}
		if relay.token != nil {
			view["token"] = relay.token
		}
		input := map[string]any{"status": response.StatusCode, "headers": firstHeaders(response.Header), "body": strings.ToValidUTF8(string(body), "\uFFFD")}
		raw, err := relay.call(ctx, "convertResponse", view, input)
		if err != nil {
			return nil, false, fmt.Errorf("convertResponse: %w", err)
		}
		var answer convertedAnswer
		if err := json.Unmarshal(raw, &answer); err != nil {
			return nil, false, errors.New("convertResponse returned no model list")
		}
		switch {
		case answer.Error != nil:
			return nil, false, errors.New(truncateRunes(answer.Error.Message, 512))
		case answer.Next == nil:
			body = convertedBody(answer.Body)
			response.StatusCode = http.StatusOK
		default:
			if len(answer.State) > 0 {
				state = answer.State
			}
			send := &relaySend{host: host, relay: relay, target: target, roundTrip: roundTrip, root: base, address: address, base: base, baseURL: strings.TrimRight(base.String(), "/"),
				key: key, query: url.Values{}, header: http.Header{}}
			next := *answer.Next
			payload, contentType, err := payloadOf(next, convertedBody(next.Body), nil)
			if err != nil {
				return nil, false, err
			}
			method := strings.ToUpper(next.Method)
			if method == "" {
				method = http.MethodGet
				if len(payload) > 0 {
					method = http.MethodPost
				}
			}
			response, err = send.send(ctx, method, next, payload, contentType)
			if err != nil {
				return nil, false, err
			}
			body, err = io.ReadAll(io.LimitReader(response.Body, maxProviderErrorBytes*8))
			_ = response.Body.Close()
			if err != nil {
				return nil, false, err
			}
			continue
		}
		break
	}
	if response.StatusCode >= 400 {
		return nil, false, fmt.Errorf("the provider answered %d", response.StatusCode)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, false, errors.New("the provider's model list is not an OpenAI model list")
	}
	models := make([]string, 0, len(list.Data))
	for _, model := range list.Data {
		if model.ID != "" {
			models = append(models, model.ID)
		}
	}
	return models, true, nil
}

// writeRelayRefusal answers a request the plugin's effects refused with
// 400, which no other provider would answer differently.
func writeRelayRefusal(writer http.ResponseWriter, target transport.Target, refusal *relayRefusal) error {
	body, _ := json.Marshal(map[string]any{"error": map[string]any{"message": refusal.message, "type": "invalid_request_error", "code": "plugin_refused"}})
	response := &http.Response{
		StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)),
	}
	if target.HandleResponse != nil {
		return target.HandleResponse(response)
	}
	return transport.WriteResponse(writer, response)
}
