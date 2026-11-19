package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/convo"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/metadata"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/tidwall/gjson"
)

// beginPlugins starts the plugins' part in a request a client sent. The
// gateway's own calls, Claude Code's background requests and protocols
// without conversation turns run none.
func (handler *Handler) beginPlugins(request *http.Request, classified Request, session *recordSession) {
	if handler.plugins == nil || session == nil || len(classified.body) == 0 {
		return
	}
	ctx := request.Context()
	if builtinInternalFrom(ctx) != nil {
		return
	}
	switch classified.Protocol {
	case contract.ProtocolAnthropicMessages, contract.ProtocolOpenAIChat,
		contract.ProtocolOpenAIResponses, contract.ProtocolGoogleGenerateContent:
	default:
		return
	}
	if claudeCodeHaikuRequest(session.clientType, classified.Model) {
		return
	}
	turn := 0
	if session.turn != nil {
		turn = session.turn.Index
	}
	accessTokenID, _ := AccessTokenIDFromContext(ctx)
	// Only a client on this machine can reach the lookup outlet.
	lookupBase := ""
	if loopbackCaller(request.RemoteAddr) && request.Host != "" {
		lookupBase = "http://" + request.Host
	}
	run := handler.plugins.Begin(ctx, pluginhost.Input{
		RequestID: session.id,
		Protocol:  classified.Protocol, Model: classified.Model, Client: string(session.clientType),
		Stream: classified.Streaming, Token: accessTokenID, Header: request.Header,
		Session: string(session.sessionID), Turn: turn, Body: classified.body,
		Inspect:    handler.pluginInspector(session, classified, accessTokenID),
		Redacts:    handler.pluginRedacts(classified, accessTokenID),
		LookupBase: lookupBase,
	})
	if run == nil {
		return
	}
	session.plugins = run
	session.metadata = run.Metadata()
	session.scanner.watchMarkers(run.NewMarkerScanner)
}

// pluginInspector checks the turn's text against the privacy policy before
// it leaves for a plugin, as for an upstream nobody trusts: allow and warn
// pass the text, redact passes it redacted with the placeholders the
// upstream sees, block passes nothing. nil when no policy can apply.
func (handler *Handler) pluginInspector(session *recordSession, classified Request, accessTokenID contract.AccessTokenID) func(context.Context, []string) ([]string, error) {
	filter := handler.privacyFilter
	if filter == nil {
		return nil
	}
	scope := privacy.Scope{Protocol: classified.Protocol, Model: classified.Model, AccessTokenID: accessTokenID}
	conversation := string(session.sessionID)
	return func(ctx context.Context, texts []string) ([]string, error) {
		return inspectForPlugin(ctx, filter, scope, conversation, texts)
	}
}

// lookupInspector checks what a lookup asks as the turn's text was checked
// before it left for the plugin: under the policy of the session's requests
// and with its conversation, so a value the plugin keeps a placeholder of
// is asked by the same placeholder. nil when no policy can apply.
func (handler *Handler) lookupInspector() func(context.Context, pluginhost.LookupScope, string, []string) ([]string, error) {
	filter := handler.privacyFilter
	if filter == nil {
		return nil
	}
	return func(ctx context.Context, scope pluginhost.LookupScope, session string, texts []string) ([]string, error) {
		return inspectForPlugin(ctx, filter, privacy.Scope{Protocol: scope.Protocol, Model: scope.Model, AccessTokenID: scope.Token}, session, texts)
	}
}

// inspectForPlugin checks texts that leave for a plugin against the privacy
// policy of scope, as for an upstream nobody trusts.
func inspectForPlugin(ctx context.Context, filter privacy.Filter, scope privacy.Scope, conversation string, texts []string) ([]string, error) {
	policy, err := filter.ResolvePolicy(ctx, scope)
	if err != nil {
		return nil, err
	}
	if !policy.Enabled {
		return texts, nil
	}
	protocol := scope.Protocol
	if _, ok := convoProtocol(protocol); !ok {
		protocol = contract.ProtocolOpenAIChat
	}
	body, paths, err := pluginSampleBody(protocol, texts)
	if err != nil {
		return nil, err
	}
	result, err := filter.Inspect(privacy.WithConversation(ctx, conversation), policy, protocol, body)
	if err != nil {
		return nil, err
	}
	switch result.Decision {
	case privacy.DecisionAllow, privacy.DecisionWarn:
		return texts, nil
	case privacy.DecisionRedact:
		checked := make([]string, len(paths))
		for index, path := range paths {
			value := gjson.GetBytes(result.Body, path)
			if value.Type != gjson.String {
				return nil, errors.New("the redacted text lost its place")
			}
			checked[index] = value.Str
		}
		return checked, nil
	case privacy.DecisionBlock:
		return nil, pluginhost.ErrBlocked
	default:
		return nil, errors.New("unknown privacy decision")
	}
}

// loopbackCaller reports whether a request came from this machine.
func loopbackCaller(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

// serveLookup serves the lookup outlet, GET or POST
// /tools/{plugin}/{tool}: the queries plugins offer the model and the
// reports they take from it, which it runs with its shell. A GET carries the
// parameters in its query; a POST, as a JSON object or as the form curl
// posts. Only a caller on this machine reaches it, and no browser; the
// session's credential stands in for an access token. Answers are plain
// text, for the model to read as a command's output.
func (handler *Handler) serveLookup(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	reply := func(status int, text string) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.WriteHeader(status)
		_, _ = io.WriteString(writer, text+"\n")
	}
	if handler.plugins == nil {
		reply(http.StatusNotFound, "no lookups")
		return
	}
	if !handler.hostAllowed(request.Host) {
		reply(http.StatusMisdirectedRequest, "request Host does not match the inference listener")
		return
	}
	if request.Header.Get("Origin") != "" || !loopbackCaller(request.RemoteAddr) {
		reply(http.StatusForbidden, "lookups answer this machine only")
		return
	}
	pluginKey, tool, ok := strings.Cut(strings.TrimPrefix(request.URL.Path, pluginhost.LookupPath), "/")
	if !ok || !contract.ValidPluginKey(pluginKey) || tool == "" || strings.Contains(tool, "/") {
		reply(http.StatusNotFound, "no such lookup")
		return
	}
	token, found := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	if !found || strings.TrimSpace(token) == "" {
		writer.Header().Set("WWW-Authenticate", "Bearer")
		reply(http.StatusUnauthorized, "a lookup needs the credential in the usage")
		return
	}
	// The first value of each parameter of a query string or a form.
	firstValues := func(given url.Values) []byte {
		values := map[string]string{}
		for key, all := range given {
			if len(all) > 0 {
				values[key] = all[0]
			}
		}
		encoded, _ := json.Marshal(values)
		return encoded
	}
	var input []byte
	switch request.Method {
	case http.MethodGet:
		if len(request.URL.RawQuery) > pluginhost.MaxLookupBody {
			reply(http.StatusRequestEntityTooLarge, "the lookup is too large")
			return
		}
		input = firstValues(request.URL.Query())
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(request.Body, pluginhost.MaxLookupBody+1))
		if err != nil {
			reply(http.StatusBadRequest, "the lookup could not be read")
			return
		}
		if len(body) > pluginhost.MaxLookupBody {
			reply(http.StatusRequestEntityTooLarge, "the lookup is too large")
			return
		}
		input = body
		if media, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type")); media == "application/x-www-form-urlencoded" {
			form, err := url.ParseQuery(string(body))
			if err != nil {
				reply(http.StatusBadRequest, "the lookup could not be read")
				return
			}
			input = firstValues(form)
		}
	default:
		writer.Header().Set("Allow", "GET, POST")
		reply(http.StatusMethodNotAllowed, "use GET or POST")
		return
	}
	result := handler.plugins.Lookup(request.Context(), pluginhost.LookupRequest{
		Plugin: pluginKey, Tool: tool, Token: strings.TrimSpace(token), Input: input, Inspect: handler.lookupInspector(),
	})
	reply(result.Status, result.Text)
}

// pluginRedacts reports whether the privacy policy that applies to the
// request replaces what it finds with placeholders. nil when no policy can
// apply.
func (handler *Handler) pluginRedacts(classified Request, accessTokenID contract.AccessTokenID) func(context.Context) bool {
	filter := handler.privacyFilter
	if filter == nil {
		return nil
	}
	scope := privacy.Scope{Protocol: classified.Protocol, Model: classified.Model, AccessTokenID: accessTokenID}
	return func(ctx context.Context) bool {
		policy, err := filter.ResolvePolicy(ctx, scope)
		return err == nil && policy.Enabled && policy.Action == privacy.ActionRedact
	}
}

// pluginSampleBody wraps texts as user messages of a request in protocol,
// the smallest body the privacy policy inspects, and returns where each
// text sits in it.
func pluginSampleBody(protocol contract.ProtocolID, texts []string) ([]byte, []string, error) {
	messages := make([]any, len(texts))
	paths := make([]string, len(texts))
	field := "messages"
	for index, text := range texts {
		at := strconv.Itoa(index)
		switch protocol {
		case contract.ProtocolGoogleGenerateContent:
			field = "contents"
			messages[index] = map[string]any{"role": "user", "parts": []map[string]string{{"text": text}}}
			paths[index] = "contents." + at + ".parts.0.text"
		case contract.ProtocolOpenAIResponses:
			field = "input"
			messages[index] = map[string]string{"role": "user", "content": text}
			paths[index] = "input." + at + ".content"
		default:
			messages[index] = map[string]string{"role": "user", "content": text}
			paths[index] = "messages." + at + ".content"
		}
	}
	body, err := json.Marshal(map[string]any{field: messages})
	return body, paths, err
}

// injectPlugins puts the plugins' blocks into the body every attempt sends,
// before the attempt loop, so retries and failover send the same bytes.
func (session *recordSession) injectPlugins(body *requestBodySource) {
	if session == nil || session.plugins == nil {
		return
	}
	if body.Replayable() {
		if data, err := body.bytes(); err == nil {
			if injected := session.plugins.Inject(data); len(injected) != len(data) {
				body.rewrite(injected)
			}
		}
	}
	session.notePlugins(session.plugins.Report())
}

// finishPlugins binds the session to the marker lines of the reply, queues
// the captures that waited for it and keeps the plugins' records with the
// request.
func (session *recordSession) finishPlugins(ctx context.Context) {
	if session == nil {
		return
	}
	session.notePlugins(session.requestPlugins.Records(), nil)
	session.finishExtensions()
	if session.plugins == nil {
		return
	}
	succeeded := session.status == contract.RequestStatusSucceeded && (!session.hasHTTPStatus || session.httpStatus < http.StatusBadRequest)
	events := session.plugins.Finish(ctx, session.scanner.markerHits(), succeeded)
	if len(events) > 0 {
		session.metadata.Events = append(session.metadata.Events, events...)
		if len(session.metadata.Events) > contract.MaxRequestMetadataEvents {
			session.metadata.Events = session.metadata.Events[:contract.MaxRequestMetadataEvents]
		}
	}
	captures := session.plugins.Captures()
	for index, record := range session.pluginRecords {
		if state, ok := captures[record.Plugin]; ok && record.Observe != "" {
			session.pluginRecords[index].Observe = state
		}
	}
	session.notePlugins(session.plugins.Report())
}

// pluginsPersisted tells the plugins that the request record is written.
func (session *recordSession) pluginsPersisted() {
	if session == nil {
		return
	}
	if session.plugins != nil {
		session.plugins.Persisted()
	}
	session.extensions.Persisted()
}

// finishExtensions keeps the records of the response hooks with the
// request, and hands the response's summary to the hooks that follow it.
func (session *recordSession) finishExtensions() {
	if session.extensions == nil {
		return
	}
	session.notePlugins(session.extensions.Records(), nil)
	summary := pluginhost.ResponseSummary{
		Status: string(session.status), HTTPStatus: session.httpStatus,
		Model: session.classified.routingModel(), ElapsedMs: time.Since(session.startedAt).Milliseconds(),
		Text: session.replyPlugins.Text(),
	}
	if session.recovery != nil && session.recovery.UpstreamModel != "" {
		summary.Model = session.recovery.UpstreamModel
	}
	if session.plan != nil {
		summary.Provider = session.servedName
	}
	if usage := session.scanner.Usage(); usage != nil {
		summary.Usage = &pluginhost.ResponseUsage{Input: int64(usage.InputTokens), Output: int64(usage.OutputTokens)}
		if usage.CacheReadTokens != nil {
			summary.Usage.CacheRead = int64(*usage.CacheReadTokens)
		}
		if usage.CacheWriteTokens != nil {
			summary.Usage.CacheWrite = int64(*usage.CacheWriteTokens)
		}
	}
	if session.errorSummary != nil {
		summary.Error = session.errorSummary.Code
	}
	session.extensions.Finish(summary)
}

// notePlugins keeps the plugins' records and their events in the gateway
// lane.
func (session *recordSession) notePlugins(records []contract.RequestPluginRecord, events []contract.RequestEvent) {
	session.pluginRecords = append(session.pluginRecords, records...)
	if len(session.pluginRecords) > contract.MaxRequestPluginRecords {
		session.pluginRecords = session.pluginRecords[:contract.MaxRequestPluginRecords]
	}
	for _, event := range events {
		event.AttemptIndex = session.attemptIndex
		session.events = append(session.events, event)
	}
}

// replyMarkers reads the marker lines in the visible text of a reply.
type replyMarkers struct {
	newScanner func() *metadata.MarkerScanner
	scanner    *metadata.MarkerScanner
	parser     convo.ResponseParser
}

// watchMarkers makes the scanner read marker lines in the reply, afresh on
// each attempt.
func (scanner *usageScanner) watchMarkers(newScanner func() *metadata.MarkerScanner) {
	if scanner == nil || newScanner == nil {
		return
	}
	convoProto, ok := convoProtocol(scanner.protocol)
	if !ok {
		return
	}
	adapter, ok := convo.DefaultRegistry().Lookup(convoProto)
	if !ok {
		return
	}
	markers := &replyMarkers{newScanner: newScanner, scanner: newScanner(), parser: adapter.NewResponseParser(scanner.streaming)}
	if markers.scanner == nil {
		return
	}
	scanner.markers = markers
}

// markerHits returns the marker lines of the reply.
func (scanner *usageScanner) markerHits() []metadata.MarkerHit {
	if scanner == nil || scanner.markers == nil {
		return nil
	}
	// A whole body is parsed on demand.
	scanner.Usage()
	return scanner.markers.scanner.Close()
}

// observe feeds the reply's visible text to the marker scanner.
func (markers *replyMarkers) observe(document map[string]json.RawMessage) {
	markers.parser.Observe(document, markerSink{markers.scanner})
}

// markerSink passes a reply's visible text to a marker scanner. A new
// message starts a new line.
type markerSink struct{ scanner *metadata.MarkerScanner }

func (markerSink) OutputID(string) {}

func (markerSink) EchoID(string) {}

func (sink markerSink) AssistantText(text string) { sink.scanner.Write(text) }

func (sink markerSink) ResetAssistantText() { sink.scanner.Write("\n") }
