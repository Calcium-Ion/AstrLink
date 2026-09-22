package ingress

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/convo"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// usageScanner is a passive observer of client-facing response bytes. It never
// modifies the stream and never fails the response; overflow disables capture.
type usageScanner struct {
	complete         bool
	protocol         contract.ProtocolID
	streaming        bool
	disabled         bool
	buffer           bytes.Buffer
	carry            []byte
	usage            *contract.Usage
	contentEncoding  string
	encodingCaptured bool
	// Anthropic accumulates across message_start / message_delta events.
	anthropicInput        *int
	anthropicOutput       int
	anthropicCacheRead    *int
	anthropicCacheWrite   *int
	anthropicCacheWrite1h *int
	anthropicSeen         bool
	outputID              string
	// observer collects session cursors from the same decoded events the
	// usage parser sees, so the response is parsed exactly once. nil for
	// protocols without conversation history.
	observer *convo.ResponseObserver
	// observerFed guards the non-streaming path: Usage re-parses the buffered
	// body on every call but the observer must see it only once.
	observerFed bool
}

func newUsageScanner(protocol contract.ProtocolID, streaming bool) *usageScanner {
	scanner := &usageScanner{protocol: protocol, streaming: streaming}
	if convoProto, ok := convoProtocol(protocol); ok {
		scanner.observer = conversationPolicy.NewResponseObserver(convoProto, streaming)
	}
	return scanner
}

// reset clears attempt-local parsing state without changing the scanner's
// address. The client response writer is installed once per ingress request
// and retains this pointer across upstream retries.
func (scanner *usageScanner) reset(protocol contract.ProtocolID, streaming bool) {
	if scanner == nil {
		return
	}
	*scanner = *newUsageScanner(protocol, streaming)
}

// conversation returns what the response contributed to session identity.
// It flushes the parser first so a non-streaming body observed only through
// the buffer is accounted for.
func (scanner *usageScanner) conversation() convo.ResponseSummary {
	if scanner == nil || scanner.observer == nil {
		return convo.ResponseSummary{}
	}
	scanner.Usage()
	return scanner.observer.Summary()
}

func (scanner *usageScanner) setContentEncoding(encoding string) {
	if scanner == nil {
		return
	}
	scanner.encodingCaptured = true
	scanner.contentEncoding = strings.ToLower(strings.TrimSpace(encoding))
}

func (scanner *usageScanner) wrap(inner http.ResponseWriter) http.ResponseWriter {
	if scanner == nil {
		return inner
	}
	return &usageScanningWriter{ResponseWriter: inner, scanner: scanner}
}

func (scanner *usageScanner) OutputID() string {
	if scanner == nil {
		return ""
	}
	return scanner.outputID
}

func (scanner *usageScanner) noteOutputID(raw json.RawMessage) {
	if scanner == nil || scanner.outputID != "" || len(raw) == 0 || string(raw) == "null" {
		return
	}
	var id string
	if json.Unmarshal(raw, &id) != nil {
		return
	}
	scanner.outputID = clampCursor(id)
}

func (scanner *usageScanner) Usage() *contract.Usage {
	if scanner == nil || scanner.disabled {
		return nil
	}
	if !scanner.streaming {
		body, ok := scanner.decodeNonStreamingBody(scanner.buffer.Bytes())
		if !ok {
			return nil
		}
		scanner.parseNonStreaming(body)
	} else if len(scanner.carry) > 0 {
		scanner.observeSSELine(scanner.carry)
		scanner.carry = nil
	}
	if scanner.protocol == contract.ProtocolAnthropicMessages && scanner.anthropicSeen {
		scanner.usage = normalizeAnthropicUsage(
			scanner.anthropicInput,
			&scanner.anthropicOutput,
			scanner.anthropicCacheRead,
			scanner.anthropicCacheWrite,
		)
	}
	if scanner.usage != nil && scanner.protocol == contract.ProtocolAnthropicMessages {
		scanner.usage.CacheWrite1hTokens = scanner.anthropicCacheWrite1h
	}
	return scanner.usage
}

func (scanner *usageScanner) decodeNonStreamingBody(body []byte) ([]byte, bool) {
	decoded, err := transport.DecodeBody(body, scanner.contentEncoding, maxResponseInspectionBytes)
	return decoded, err == nil
}

func (scanner *usageScanner) observe(chunk []byte) {
	if scanner == nil || scanner.disabled {
		return
	}
	if scanner.streaming {
		scanner.observeStreaming(chunk)
		return
	}
	if scanner.buffer.Len()+len(chunk) > maxResponseInspectionBytes {
		scanner.disabled = true
		scanner.buffer.Reset()
		return
	}
	_, _ = scanner.buffer.Write(chunk)
}

func (scanner *usageScanner) observeStreaming(chunk []byte) {
	scanner.carry = append(scanner.carry, chunk...)
	if len(scanner.carry) > maxResponseInspectionBytes {
		scanner.disabled = true
		scanner.carry = nil
		return
	}
	for {
		newline := bytes.IndexByte(scanner.carry, '\n')
		if newline < 0 {
			return
		}
		line := scanner.carry[:newline]
		scanner.carry = append([]byte(nil), scanner.carry[newline+1:]...)
		scanner.observeSSELine(line)
		if scanner.disabled {
			return
		}
	}
}

func (scanner *usageScanner) observeSSELine(line []byte) {
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	payload := line[len("data:"):]
	if len(payload) > 0 && payload[0] == ' ' {
		payload = payload[1:]
	}
	if len(payload) == 0 || string(payload) == "[DONE]" {
		return
	}
	scanner.parseEventJSON(payload)
}

func (scanner *usageScanner) parseNonStreaming(body []byte) {
	if len(body) == 0 {
		return
	}
	scanner.parseEventJSON(body)
	scanner.complete = scanner.usage != nil
}

func (scanner *usageScanner) parseEventJSON(payload []byte) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(payload, &document); err != nil || document == nil {
		return
	}
	if scanner.observer != nil && (scanner.streaming || !scanner.observerFed) {
		scanner.observer.ObserveEvent(document)
		scanner.observerFed = true
	}
	switch scanner.protocol {
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		scanner.parseOpenAIResponses(document)
	case contract.ProtocolOpenAIChat, contract.ProtocolOpenAICompletions:
		scanner.parseOpenAIChat(document)
	case contract.ProtocolAnthropicMessages:
		scanner.parseAnthropic(document)
	case contract.ProtocolGoogleGenerateContent:
		scanner.parseGemini(document)
	}
}

func (scanner *usageScanner) parseOpenAIResponses(document map[string]json.RawMessage) {
	if scanner.streaming {
		var eventType string
		if raw, ok := document["type"]; ok {
			_ = json.Unmarshal(raw, &eventType)
		}
		if eventType != "" && eventType != "response.completed" {
			return
		}
		if rawResponse, ok := document["response"]; ok {
			var response map[string]json.RawMessage
			if json.Unmarshal(rawResponse, &response) == nil {
				scanner.noteOutputID(response["id"])
				if usage := decodeOpenAIResponsesUsage(response["usage"]); usage != nil {
					scanner.complete = true
					scanner.usage = usage
				}
				return
			}
		}
	}
	scanner.noteOutputID(document["id"])
	if usage := decodeOpenAIResponsesUsage(document["usage"]); usage != nil {
		scanner.usage = usage
	}
}

func decodeOpenAIResponsesUsage(raw json.RawMessage) *contract.Usage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var payload struct {
		InputTokens         *int            `json:"input_tokens"`
		OutputTokens        *int            `json:"output_tokens"`
		TotalTokens         *int            `json:"total_tokens"`
		CachedInputTokens   *int            `json:"cached_input_tokens"`
		InputTokensDetails  json.RawMessage `json:"input_tokens_details"`
		OutputTokensDetails json.RawMessage `json:"output_tokens_details"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	if payload.InputTokens == nil || payload.OutputTokens == nil || payload.TotalTokens == nil {
		return nil
	}
	cacheRead := cachedTokensFromDetails(payload.InputTokensDetails)
	if cacheRead == nil {
		cacheRead = payload.CachedInputTokens
	}
	usage := normalizeOpenAIStyleUsage(
		*payload.InputTokens,
		*payload.OutputTokens,
		*payload.TotalTokens,
		cacheRead,
	)
	usage.InputAudioTokens = audioTokensFromDetails(payload.InputTokensDetails)
	usage.OutputAudioTokens = audioTokensFromDetails(payload.OutputTokensDetails)
	return usage
}

func (scanner *usageScanner) parseOpenAIChat(document map[string]json.RawMessage) {
	scanner.noteOutputID(document["id"])
	raw, ok := document["usage"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return
	}
	var payload struct {
		PromptTokens            *int            `json:"prompt_tokens"`
		CompletionTokens        *int            `json:"completion_tokens"`
		TotalTokens             *int            `json:"total_tokens"`
		PromptTokensDetails     json.RawMessage `json:"prompt_tokens_details"`
		CompletionTokensDetails json.RawMessage `json:"completion_tokens_details"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return
	}
	if payload.PromptTokens == nil || payload.CompletionTokens == nil || payload.TotalTokens == nil {
		return
	}
	scanner.usage = normalizeOpenAIStyleUsage(
		*payload.PromptTokens,
		*payload.CompletionTokens,
		*payload.TotalTokens,
		cachedTokensFromDetails(payload.PromptTokensDetails),
	)
	scanner.usage.InputAudioTokens = audioTokensFromDetails(payload.PromptTokensDetails)
	scanner.usage.OutputAudioTokens = audioTokensFromDetails(payload.CompletionTokensDetails)
	scanner.complete = true
}

func (scanner *usageScanner) parseAnthropic(document map[string]json.RawMessage) {
	var eventType string
	if raw, ok := document["type"]; ok {
		_ = json.Unmarshal(raw, &eventType)
	}
	switch eventType {
	case "message_stop":
		scanner.complete = true
	case "message_start":
		var message struct {
			ID    string                `json:"id"`
			Usage anthropicUsagePayload `json:"usage"`
		}
		if raw, ok := document["message"]; ok {
			if json.Unmarshal(raw, &message) == nil {
				if message.ID != "" {
					scanner.outputID = clampCursor(message.ID)
				}
				scanner.applyAnthropicUsage(message.Usage, false)
			}
		}
	case "message_delta":
		var delta struct {
			StopReason *string `json:"stop_reason"`
		}
		if json.Unmarshal(document["delta"], &delta) == nil && delta.StopReason != nil {
			scanner.complete = true
		}
		if raw, ok := document["usage"]; ok {
			var usage anthropicUsagePayload
			if json.Unmarshal(raw, &usage) == nil {
				scanner.applyAnthropicUsage(usage, true)
			}
		}
	default:
		if !scanner.streaming {
			var usage anthropicUsagePayload
			if raw, ok := document["usage"]; ok && json.Unmarshal(raw, &usage) == nil {
				if usage.InputTokens != nil && usage.OutputTokens != nil {
					if usage.CacheCreation != nil {
						scanner.anthropicCacheWrite1h = usage.CacheCreation.OneHour
					}
					scanner.usage = normalizeAnthropicUsage(
						usage.InputTokens,
						usage.OutputTokens,
						usage.CacheReadInputTokens,
						usage.CacheCreationInputTokens,
					)
				}
			}
		}
	}
}

type anthropicUsagePayload struct {
	InputTokens              *int `json:"input_tokens"`
	OutputTokens             *int `json:"output_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
	CacheCreation            *struct {
		OneHour *int `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

func (scanner *usageScanner) applyAnthropicUsage(usage anthropicUsagePayload, accumulateOutput bool) {
	if usage.CacheCreation != nil {
		scanner.anthropicCacheWrite1h = usage.CacheCreation.OneHour
	}
	if usage.InputTokens != nil {
		scanner.anthropicInput = usage.InputTokens
		scanner.anthropicSeen = true
	}
	if usage.CacheReadInputTokens != nil {
		scanner.anthropicCacheRead = usage.CacheReadInputTokens
		scanner.anthropicSeen = true
	}
	if usage.CacheCreationInputTokens != nil {
		scanner.anthropicCacheWrite = usage.CacheCreationInputTokens
		scanner.anthropicSeen = true
	}
	if usage.OutputTokens != nil {
		if accumulateOutput {
			scanner.anthropicOutput = max(scanner.anthropicOutput, *usage.OutputTokens)
		} else {
			scanner.anthropicOutput = *usage.OutputTokens
		}
		scanner.anthropicSeen = true
	}
}

func (scanner *usageScanner) parseGemini(document map[string]json.RawMessage) {
	scanner.noteOutputID(document["responseId"])
	var candidates []struct {
		FinishReason string `json:"finishReason"`
	}
	if json.Unmarshal(document["candidates"], &candidates) == nil {
		for _, c := range candidates {
			if c.FinishReason != "" {
				scanner.complete = true
			}
		}
	}
	raw, ok := document["usageMetadata"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return
	}
	var payload struct {
		PromptTokenCount        *int `json:"promptTokenCount"`
		CandidatesTokenCount    *int `json:"candidatesTokenCount"`
		TotalTokenCount         *int `json:"totalTokenCount"`
		CachedContentTokenCount *int `json:"cachedContentTokenCount"`
		ThoughtsTokenCount      int  `json:"thoughtsTokenCount"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return
	}
	if payload.PromptTokenCount == nil || payload.CandidatesTokenCount == nil || payload.TotalTokenCount == nil {
		return
	}
	scanner.usage = normalizeOpenAIStyleUsage(
		*payload.PromptTokenCount,
		*payload.CandidatesTokenCount+payload.ThoughtsTokenCount,
		*payload.TotalTokenCount,
		payload.CachedContentTokenCount,
	)
}

// normalizeOpenAIStyleUsage keeps provider input as-is (already includes cache
// hits and multimodal tokens) and attaches optional cache_read.
func normalizeOpenAIStyleUsage(input, output, total int, cacheRead *int) *contract.Usage {
	usage := &contract.Usage{
		InputTokens:  input,
		OutputTokens: output,
		TotalTokens:  total,
	}
	if cacheRead != nil {
		value := *cacheRead
		usage.CacheReadTokens = &value
	}
	return usage
}

// normalizeAnthropicUsage converts Anthropic's disjoint input partition into
// OpenAI-style input_tokens that include cache read and cache creation.
func normalizeAnthropicUsage(rawInput, rawOutput, cacheRead, cacheWrite *int) *contract.Usage {
	if rawInput == nil || rawOutput == nil {
		return nil
	}
	input := *rawInput
	read := 0
	write := 0
	if cacheRead != nil {
		read = *cacheRead
		input += read
	}
	if cacheWrite != nil {
		write = *cacheWrite
		input += write
	}
	usage := &contract.Usage{
		InputTokens:  input,
		OutputTokens: *rawOutput,
		TotalTokens:  input + *rawOutput,
	}
	if cacheRead != nil {
		value := read
		usage.CacheReadTokens = &value
	}
	if cacheWrite != nil {
		value := write
		usage.CacheWriteTokens = &value
	}
	return usage
}

func cachedTokensFromDetails(raw json.RawMessage) *int {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var details struct {
		CachedTokens *int `json:"cached_tokens"`
	}
	if json.Unmarshal(raw, &details) != nil || details.CachedTokens == nil {
		return nil
	}
	value := *details.CachedTokens
	return &value
}

type usageScanningWriter struct {
	http.ResponseWriter
	scanner *usageScanner
}

func (writer *usageScanningWriter) WriteHeader(status int) {
	writer.captureEncoding()
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *usageScanningWriter) Write(chunk []byte) (int, error) {
	writer.captureEncoding()
	writer.scanner.observe(chunk)
	return writer.ResponseWriter.Write(chunk)
}

func (writer *usageScanningWriter) captureEncoding() {
	if writer.scanner == nil || writer.scanner.encodingCaptured {
		return
	}
	writer.scanner.setContentEncoding(writer.Header().Get("Content-Encoding"))
}

func (writer *usageScanningWriter) Flush() {
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (writer *usageScanningWriter) FlushError() error {
	if controller, ok := writer.ResponseWriter.(interface{ FlushError() error }); ok {
		return controller.FlushError()
	}
	writer.Flush()
	return nil
}

func (writer *usageScanningWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func audioTokensFromDetails(raw json.RawMessage) *int {
	var details struct {
		Audio *int `json:"audio_tokens"`
	}
	if json.Unmarshal(raw, &details) != nil {
		return nil
	}
	return details.Audio
}
