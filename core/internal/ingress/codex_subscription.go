package ingress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/jsonedit"
	"github.com/tidwall/gjson"
)

// codexSubscriptionUnsupportedFields are rejected by the ChatGPT Codex backend.
// Repeatedly sending them from one subscription account is a malformed-request
// pattern, so they are dropped instead of being forwarded.
var codexSubscriptionUnsupportedFields = []string{
	"max_output_tokens", "max_completion_tokens", "temperature", "top_p",
	"frequency_penalty", "presence_penalty", "truncation", "prompt_cache_retention",
	"prompt_cache_options", "safety_identifier", "user", "metadata", "stream_options",
}

const codexEncryptedReasoningInclude = "reasoning.encrypted_content"

// codexRequestOptions selects the Routing protections of a Codex attempt.
type codexRequestOptions struct {
	// normalize applies every structural rule. Without it, only a converted
	// Responses body receives the fixes local conversion always needed.
	normalize bool
	converted bool
	// sessionScope maps forwarded session identifiers into one account's
	// namespace; empty forwards them unchanged.
	sessionScope contract.ServiceID
}

// prepareCodexSubscriptionRequest normalizes the structure of a Codex
// subscription request: storage stays disabled, instructions are present,
// encrypted reasoning is retained and unsupported fields are removed. HTTP
// Responses requests must stream upstream; forcedStream reports that a
// non-streaming request was switched so its SSE response must be aggregated.
// Every rule is a no-op for a compliant body, which is then forwarded
// byte-for-byte; a Codex User-Agent alone is not trusted because it is easily
// copied. Prompts, tools and continuation fields are never rewritten.
func prepareCodexSubscriptionRequest(
	request *http.Request,
	protocol contract.ProtocolID,
	options codexRequestOptions,
) (forcedStream bool, err error) {
	legacy := !options.normalize && options.converted && protocol == contract.ProtocolOpenAIResponses
	repair := options.normalize || legacy
	if !repair && options.sessionScope == "" {
		return false, nil
	}
	raw, err := io.ReadAll(request.Body)
	_ = request.Body.Close()
	if err != nil {
		return false, fmt.Errorf("Codex subscription request could not be prepared")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil || body == nil {
		if options.sessionScope != "" {
			scopeCodexSessions(request.Header, nil, options.sessionScope)
		}
		if !repair {
			// Session scoping alone never rejects a body the upstream judges.
			replaceRecoveryRequestBody(request, raw)
			return false, nil
		}
		return false, fmt.Errorf("invalid Codex subscription request")
	}
	changed := false
	switch {
	case options.normalize:
		changed, forcedStream = normalizeCodexSubscriptionBody(body, protocol)
	case legacy:
		changed = normalizeCodexConvertedBody(body)
	}
	if options.sessionScope != "" && scopeCodexSessions(request.Header, body, options.sessionScope) {
		changed = true
	}
	if options.sessionScope != "" && scopeCodexPromptCacheKey(body, options.sessionScope) {
		changed = true
	}
	if !changed {
		replaceRecoveryRequestBody(request, raw)
		return false, nil
	}
	updated, err := json.Marshal(body)
	if err != nil {
		return false, err
	}
	replaceRecoveryRequestBody(request, updated)
	return forcedStream, nil
}

func normalizeCodexSubscriptionBody(body map[string]json.RawMessage, protocol contract.ProtocolID) (changed, forcedStream bool) {
	if instructions, ok := body["instructions"]; !ok || isJSONNull(instructions) {
		body["instructions"] = json.RawMessage(`""`)
		changed = true
	}
	for _, name := range codexSubscriptionUnsupportedFields {
		if _, ok := body[name]; ok {
			delete(body, name)
			changed = true
		}
	}
	if protocol != contract.ProtocolOpenAIResponses {
		return changed, false
	}
	if input := bytes.TrimSpace(body["input"]); len(input) > 0 && input[0] == '"' {
		// Responses accepts shorthand text; the subscription backend requires
		// a message list. Wrap the JSON string without changing its text.
		body["input"] = json.RawMessage(`[{"role":"user","content":` + string(input) + `}]`)
		changed = true
	}
	if !bytes.Equal(bytes.TrimSpace(body["store"]), []byte("false")) {
		body["store"] = json.RawMessage("false")
		changed = true
	}
	if reasoning, ok := body["reasoning"]; ok && !isJSONNull(reasoning) {
		if include, added := withCodexEncryptedReasoning(body["include"]); added {
			body["include"] = include
			changed = true
		}
	}
	if !bytes.Equal(bytes.TrimSpace(body["stream"]), []byte("true")) {
		body["stream"] = json.RawMessage("true")
		changed, forcedStream = true, true
	}
	return changed, forcedStream
}

// normalizeCodexConvertedBody applies the fixes a locally converted Responses
// body needs even without request normalization: Codex rejects storage, a
// missing instructions field and max_output_tokens.
func normalizeCodexConvertedBody(body map[string]json.RawMessage) bool {
	changed := false
	if !bytes.Equal(bytes.TrimSpace(body["store"]), []byte("false")) {
		body["store"] = json.RawMessage("false")
		changed = true
	}
	if instructions, ok := body["instructions"]; !ok || isJSONNull(instructions) {
		body["instructions"] = json.RawMessage(`""`)
		changed = true
	}
	if _, ok := body["max_output_tokens"]; ok {
		delete(body, "max_output_tokens")
		changed = true
	}
	return changed
}

// codexSessionHeaders carry the Codex client's session and thread identity:
// hyphenated since 0.131, with underscores in 0.129 and 0.130, and
// Conversation_id in older clients. X-Client-Request-Id repeats the thread id
// and a spawned thread names its parent's.
var codexSessionHeaders = []string{
	"Session-Id", "Session_id", "Thread-Id", "Thread_id", "Conversation_id",
	"X-Client-Request-Id", codexParentThreadHeader,
}

// codexSessionCarriers repeat those ids inside larger values: the window id
// starts with the thread id and the turn metadata names the session, the
// thread and its parent. client_metadata in the body repeats all of them.
var codexSessionCarriers = []string{"X-Codex-Window-Id", codexTurnMetadataHeader}

// codexSessionIDFields name the ids inside client_metadata and the turn
// metadata.
var codexSessionIDFields = []string{
	"session_id", "thread_id", "parent_thread_id", "forked_from_thread_id", "x-codex-parent-thread-id",
}

// minCodexSessionIDLength keeps short ids, which could occur inside unrelated
// text, from being mapped where a value only contains them. Codex ids are
// UUIDs.
const minCodexSessionIDLength = 8

// scopeCodexSessions maps every session and thread id the caller sent, in the
// session headers, the values that repeat them and body's client_metadata,
// into scope's namespace with the function that maps prompt_cache_key, so a
// value repeated in several places stays the same everywhere. Absent ids are
// not invented. It reports whether body changed; a nil body leaves only the
// headers to map.
func scopeCodexSessions(header http.Header, body map[string]json.RawMessage, scope contract.ServiceID) bool {
	mapped := map[string]string{}
	note := func(value string) {
		if value = strings.TrimSpace(value); value != "" {
			mapped[value] = accountauth.ScopedSessionID(scope, value)
		}
	}
	noteMetadata := func(metadata gjson.Result) {
		for _, field := range codexSessionIDFields {
			if value := metadata.Get(field); value.Type == gjson.String {
				note(value.Str)
			}
		}
	}
	for _, name := range codexSessionHeaders {
		note(header.Get(name))
	}
	noteMetadata(gjson.Parse(header.Get(codexTurnMetadataHeader)))
	clientMetadata := gjson.ParseBytes(body["client_metadata"])
	if clientMetadata.IsObject() {
		noteMetadata(clientMetadata)
		noteMetadata(gjson.Parse(clientMetadata.Get("x-codex-turn-metadata").String()))
	}
	if len(mapped) == 0 {
		return false
	}
	// Longer ids first, so an id that contains another maps as a whole.
	ids := slices.SortedFunc(maps.Keys(mapped), func(left, right string) int { return len(right) - len(left) })
	scopeValue := func(value string) string {
		if scoped, ok := mapped[value]; ok {
			return scoped
		}
		for _, id := range ids {
			if len(id) >= minCodexSessionIDLength {
				value = strings.ReplaceAll(value, id, mapped[id])
			}
		}
		return value
	}
	for _, name := range append(slices.Clone(codexSessionHeaders), codexSessionCarriers...) {
		if values := header.Values(name); len(values) > 0 {
			header.Set(name, scopeValue(strings.TrimSpace(values[0])))
		}
	}
	if !clientMetadata.IsObject() {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body["client_metadata"], &fields) != nil {
		return false
	}
	changed := false
	for key, raw := range fields {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			continue
		}
		if scoped := scopeValue(value); scoped != value {
			encoded, err := json.Marshal(scoped)
			if err != nil {
				continue
			}
			fields[key] = encoded
			changed = true
		}
	}
	if !changed {
		return false
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return false
	}
	body["client_metadata"] = encoded
	return true
}

func scopeCodexPromptCacheKey(body map[string]json.RawMessage, scope contract.ServiceID) bool {
	key, ok := claudeString(body["prompt_cache_key"])
	if !ok || strings.TrimSpace(key) == "" {
		return false
	}
	encoded, err := json.Marshal(accountauth.ScopedSessionID(scope, key))
	if err != nil {
		return false
	}
	body["prompt_cache_key"] = encoded
	return true
}

// filterCodexRelayKitHeaders drops client SDK fingerprints and Anthropic
// protocol headers from a request converted for the Codex backend.
func filterCodexRelayKitHeaders(header http.Header) {
	stripHeaderPrefixes(header, "x-stainless-", "anthropic-")
}

// Codex marks a Responses Lite request with this header, or with this
// client_metadata entry on a WebSocket turn. Lite takes no top-level tools; it
// declares them in additional_tools input items.
const (
	codexResponsesLiteHeader   = "X-Openai-Internal-Codex-Responses-Lite"
	codexResponsesLiteMetadata = "ws_request_header_x_openai_internal_codex_responses_lite"
)

// codexWebSearchHistoryTool searches cached results only.
var codexWebSearchHistoryTool = struct {
	Type              string `json:"type"`
	ExternalWebAccess bool   `json:"external_web_access"`
}{Type: "web_search"}

// declareCodexWebSearchHistory reads the attempt body and declares the
// web_search tool its history needs, see withCodexWebSearchHistoryTool. A
// compressed body is forwarded as it is.
func declareCodexWebSearchHistory(request *http.Request) error {
	if request.Body == nil || request.Body == http.NoBody {
		return nil
	}
	if encoding := strings.ToLower(strings.TrimSpace(request.Header.Get("Content-Encoding"))); encoding != "" && encoding != "identity" {
		return nil
	}
	raw, err := io.ReadAll(request.Body)
	_ = request.Body.Close()
	if err != nil {
		return fmt.Errorf("Codex subscription request could not be prepared")
	}
	body, _ := withCodexWebSearchHistoryTool(raw, request.Header)
	replaceRecoveryRequestBody(request, body)
	return nil
}

// withCodexWebSearchHistoryTool declares a cached-only web_search tool when the
// Responses input replays a hosted web_search_call item but no web search tool
// is declared. The Codex backend rejects that history ("response protection is
// unavailable"), and Codex's local compaction sends exactly that: the whole
// history with tools:[]. Responses Lite gets the declaration in its first
// additional_tools item, or in a new one kept before a trailing
// compaction_trigger. When the caller offered no tools, tool_choice becomes
// "none" so the declared tool cannot run. Only the added values are encoded;
// a body that needs nothing, or cannot be edited in place, is returned as is.
func withCodexWebSearchHistoryTool(body []byte, header http.Header) ([]byte, bool) {
	if !bytes.Contains(body, []byte(`"web_search_call"`)) {
		return body, false
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, false
	}
	items := input.Array()
	replayed, offered, additional := false, false, -1
	for index, item := range items {
		switch item.Get("type").String() {
		case "web_search_call":
			replayed = true
		case "additional_tools":
			tools := item.Get("tools")
			if codexDeclaresWebSearch(tools) {
				return body, false
			}
			offered = offered || tools.IsArray() && len(tools.Array()) > 0
			if additional < 0 {
				additional = index
			}
		}
	}
	tools := gjson.GetBytes(body, "tools")
	if !replayed || codexDeclaresWebSearch(tools) {
		return body, false
	}
	offered = offered || tools.IsArray() && len(tools.Array()) > 0

	path := "tools"
	lite := codexResponsesLite(header, body)
	if lite && additional >= 0 {
		path = "input." + strconv.Itoa(additional) + ".tools"
		tools = items[additional].Get("tools")
	}
	var declared []byte
	var err error
	switch {
	case lite && additional < 0:
		at := len(items)
		if at > 0 && items[at-1].Get("type").String() == "compaction_trigger" {
			at--
		}
		item := struct {
			Type  string `json:"type"`
			Role  string `json:"role"`
			Tools []any  `json:"tools"`
		}{"additional_tools", "developer", []any{codexWebSearchHistoryTool}}
		declared, err = jsonedit.Insert(body, "input", at, item)
	case tools.IsArray():
		declared, err = jsonedit.Insert(body, path, len(tools.Array()), codexWebSearchHistoryTool)
	case !tools.Exists() || tools.Type == gjson.Null:
		declared, err = jsonedit.Set(body, path, []any{codexWebSearchHistoryTool})
	default:
		// A malformed tools value is left for the upstream to judge.
		return body, false
	}
	if err != nil {
		return body, false
	}
	if !offered {
		// Only a choice among no tools is pinned; any other is the caller's.
		choice := gjson.GetBytes(declared, "tool_choice")
		mode := strings.ToLower(strings.TrimSpace(choice.Str))
		if !choice.Exists() || choice.Type == gjson.Null || choice.Type == gjson.String && (mode == "" || mode == "auto" || mode == "none") {
			if declared, err = jsonedit.Set(declared, "tool_choice", "none"); err != nil {
				return body, false
			}
		}
	}
	return declared, true
}

// codexDeclaresWebSearch reports a web_search tool of any version in tools.
func codexDeclaresWebSearch(tools gjson.Result) bool {
	if !tools.IsArray() {
		return false
	}
	for _, tool := range tools.Array() {
		if strings.HasPrefix(strings.TrimSpace(tool.Get("type").String()), "web_search") {
			return true
		}
	}
	return false
}

func codexResponsesLite(header http.Header, body []byte) bool {
	return strings.EqualFold(strings.TrimSpace(header.Get(codexResponsesLiteHeader)), "true") ||
		strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "client_metadata."+codexResponsesLiteMetadata).String()), "true")
}

func isJSONNull(value json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

// withCodexEncryptedReasoning appends the encrypted reasoning include. Stateless
// (store=false) turns otherwise lose reasoning continuity. A malformed include
// is left for the upstream to reject rather than being rewritten.
func withCodexEncryptedReasoning(raw json.RawMessage) (json.RawMessage, bool) {
	var include []string
	if len(raw) > 0 && !isJSONNull(raw) {
		if json.Unmarshal(raw, &include) != nil {
			return raw, false
		}
	}
	for _, value := range include {
		if value == codexEncryptedReasoningInclude {
			return raw, false
		}
	}
	updated, err := json.Marshal(append(include, codexEncryptedReasoningInclude))
	if err != nil {
		return raw, false
	}
	return updated, true
}

// codexAggregateWriter turns the SSE stream of a request whose streaming was
// forced upstream back into the single Responses JSON object the client asked
// for. Non-2xx or non-SSE responses pass through unchanged.
type codexAggregateWriter struct {
	http.ResponseWriter
	status      int
	passthrough bool
	carry       []byte
	items       map[int]json.RawMessage
	completed   json.RawMessage
	failure     json.RawMessage
	failureCode string
	failureType string
}

func newCodexAggregateWriter(writer http.ResponseWriter) *codexAggregateWriter {
	return &codexAggregateWriter{ResponseWriter: writer, items: make(map[int]json.RawMessage)}
}

func (writer *codexAggregateWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.passthrough = status < 200 || status >= 300 ||
		!strings.HasPrefix(strings.ToLower(writer.Header().Get("Content-Type")), "text/event-stream")
	if writer.passthrough {
		writer.ResponseWriter.WriteHeader(status)
	}
}

func (writer *codexAggregateWriter) Write(value []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	if writer.passthrough {
		return writer.ResponseWriter.Write(value)
	}
	writer.carry = append(writer.carry, value...)
	writer.consumeFrames(false)
	return len(value), nil
}

func (writer *codexAggregateWriter) Flush()                      {}
func (writer *codexAggregateWriter) FlushError() error           { return nil }
func (writer *codexAggregateWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

func (writer *codexAggregateWriter) consumeFrames(final bool) {
	normalized := bytes.ReplaceAll(writer.carry, []byte("\r\n"), []byte("\n"))
	frames := bytes.Split(normalized, []byte("\n\n"))
	if !final {
		writer.carry = append(writer.carry[:0], frames[len(frames)-1]...)
		frames = frames[:len(frames)-1]
	} else {
		writer.carry = nil
	}
	for _, frame := range frames {
		if writer.completed != nil || writer.failure != nil {
			return
		}
		_, data := parseSSEFrame(frame)
		if len(data) == 0 {
			continue
		}
		var event struct {
			Type        string          `json:"type"`
			OutputIndex *int            `json:"output_index"`
			Item        json.RawMessage `json:"item"`
			Response    json.RawMessage `json:"response"`
			Error       json.RawMessage `json:"error"`
			Code        string          `json:"code"`
			Message     string          `json:"message"`
			Param       json.RawMessage `json:"param"`
		}
		if json.Unmarshal(data, &event) != nil {
			continue
		}
		switch event.Type {
		case "response.output_item.done":
			if event.OutputIndex != nil && len(event.Item) > 0 {
				writer.items[*event.OutputIndex] = event.Item
			}
		case "response.completed", "response.incomplete":
			if len(event.Response) > 0 && !isJSONNull(event.Response) {
				writer.completed = event.Response
			}
		case "response.failed":
			var response struct {
				Error json.RawMessage `json:"error"`
			}
			_ = json.Unmarshal(event.Response, &response)
			writer.setFailure(response.Error)
		case "error":
			if len(event.Error) > 0 && !isJSONNull(event.Error) {
				writer.setFailure(event.Error)
				continue
			}
			flat, _ := json.Marshal(map[string]any{"code": event.Code, "message": event.Message, "param": event.Param})
			writer.setFailure(flat)
		}
	}
}

func (writer *codexAggregateWriter) setFailure(raw json.RawMessage) {
	var detail struct {
		Code string `json:"code"`
		Type string `json:"type"`
	}
	if len(raw) == 0 || isJSONNull(raw) || json.Unmarshal(raw, &detail) != nil {
		raw = json.RawMessage(`{"message":"upstream response failed","type":"server_error"}`)
	}
	writer.failure = raw
	writer.failureCode, writer.failureType = detail.Code, detail.Type
}

// Finish writes the aggregated response. It returns an error, before anything
// is committed, when the stream ended without a terminal event.
func (writer *codexAggregateWriter) Finish() error {
	if writer.passthrough {
		return nil
	}
	writer.consumeFrames(true)
	writer.Header().Del("Content-Length")
	writer.Header().Set("Content-Type", "application/json")
	if writer.failure != nil {
		body, err := json.Marshal(map[string]json.RawMessage{"error": writer.failure})
		if err != nil {
			return err
		}
		writer.ResponseWriter.WriteHeader(responsesFailureStatus(writer.failureCode, writer.failureType))
		_, err = writer.ResponseWriter.Write(body)
		return err
	}
	if writer.completed == nil {
		return fmt.Errorf("Codex response stream ended before completion")
	}
	body, err := writer.completedWithOutput()
	if err != nil {
		return err
	}
	writer.ResponseWriter.WriteHeader(http.StatusOK)
	_, err = writer.ResponseWriter.Write(body)
	return err
}

// completedWithOutput restores output items delivered only through
// output_item.done events when the terminal response carries an empty output.
func (writer *codexAggregateWriter) completedWithOutput() ([]byte, error) {
	if len(writer.items) == 0 {
		return writer.completed, nil
	}
	var response map[string]json.RawMessage
	if json.Unmarshal(writer.completed, &response) != nil {
		return writer.completed, nil
	}
	var output []json.RawMessage
	if raw, ok := response["output"]; ok && !isJSONNull(raw) && (json.Unmarshal(raw, &output) != nil || len(output) > 0) {
		return writer.completed, nil
	}
	indexes := slices.Sorted(maps.Keys(writer.items))
	output = make([]json.RawMessage, 0, len(indexes))
	for _, index := range indexes {
		output = append(output, writer.items[index])
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return nil, err
	}
	response["output"] = encoded
	return json.Marshal(response)
}
