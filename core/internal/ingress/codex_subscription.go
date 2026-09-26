package ingress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

// codexSubscriptionUnsupportedFields are rejected by the ChatGPT Codex backend.
// Repeatedly sending them from one subscription account is a malformed-request
// pattern, so they are dropped instead of being forwarded.
var codexSubscriptionUnsupportedFields = []string{
	"max_output_tokens", "max_completion_tokens", "temperature", "top_p",
	"frequency_penalty", "presence_penalty", "truncation", "prompt_cache_retention",
	"safety_identifier", "user", "metadata", "stream_options",
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
	if options.sessionScope != "" {
		scopeCodexSessionHeaders(request.Header, options.sessionScope)
	}
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

// codexSessionHeaders carry the Codex client's conversation identity.
var codexSessionHeaders = []string{"Session_id", "Conversation_id"}

// scopeCodexSessionHeaders maps session identifiers the caller sent; absent
// ones are not invented.
func scopeCodexSessionHeaders(header http.Header, scope contract.ServiceID) {
	for _, name := range codexSessionHeaders {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			header.Set(name, accountauth.ScopedSessionID(scope, value))
		}
	}
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
	writer.failureCode = detail.Code
	if writer.failureCode == "" {
		writer.failureCode = detail.Type
	}
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
		writer.ResponseWriter.WriteHeader(codexFailureStatus(writer.failureCode))
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

func codexFailureStatus(code string) int {
	switch code {
	case "rate_limit_exceeded", "usage_limit_reached", "usage_not_included", "insufficient_quota":
		return http.StatusTooManyRequests
	case "context_length_exceeded", "invalid_prompt", "invalid_request_error", "invalid_value", "bad_request":
		return http.StatusBadRequest
	default:
		return http.StatusBadGateway
	}
}
