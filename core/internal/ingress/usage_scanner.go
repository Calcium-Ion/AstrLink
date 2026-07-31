package ingress

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// usageScanner is a passive observer of client-facing response bytes. It never
// modifies the stream and never fails the response; overflow disables capture.
type usageScanner struct {
	protocol         contract.ProtocolID
	streaming        bool
	disabled         bool
	buffer           bytes.Buffer
	carry            []byte
	usage            *contract.Usage
	contentEncoding  string
	encodingCaptured bool
	// Anthropic accumulates output_tokens across message_delta events.
	anthropicInput  *int
	anthropicOutput int
	anthropicSeen   bool
}

func newUsageScanner(protocol contract.ProtocolID, streaming bool) *usageScanner {
	return &usageScanner{protocol: protocol, streaming: streaming}
}

// reset clears attempt-local parsing state without changing the scanner's
// address. The client response writer is installed once per ingress request
// and retains this pointer across upstream retries.
func (scanner *usageScanner) reset(protocol contract.ProtocolID, streaming bool) {
	if scanner == nil {
		return
	}
	*scanner = usageScanner{protocol: protocol, streaming: streaming}
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
		input := 0
		if scanner.anthropicInput != nil {
			input = *scanner.anthropicInput
		}
		total := input + scanner.anthropicOutput
		scanner.usage = &contract.Usage{
			InputTokens:  input,
			OutputTokens: scanner.anthropicOutput,
			TotalTokens:  total,
		}
	}
	return scanner.usage
}

func (scanner *usageScanner) decodeNonStreamingBody(body []byte) ([]byte, bool) {
	encoding := strings.ToLower(strings.TrimSpace(scanner.contentEncoding))
	switch encoding {
	case "", "identity":
		return body, true
	case "gzip":
		if len(body) == 0 {
			return nil, false
		}
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, false
		}
		defer reader.Close()
		decompressed, err := io.ReadAll(io.LimitReader(reader, int64(maxMetadataBytes)+1))
		if err != nil {
			return nil, false
		}
		if len(decompressed) > maxMetadataBytes {
			return nil, false
		}
		return decompressed, true
	default:
		return nil, false
	}
}

func (scanner *usageScanner) observe(chunk []byte) {
	if scanner == nil || scanner.disabled {
		return
	}
	if scanner.streaming {
		scanner.observeStreaming(chunk)
		return
	}
	if scanner.buffer.Len()+len(chunk) > maxMetadataBytes {
		scanner.disabled = true
		scanner.buffer.Reset()
		return
	}
	_, _ = scanner.buffer.Write(chunk)
}

func (scanner *usageScanner) observeStreaming(chunk []byte) {
	scanner.carry = append(scanner.carry, chunk...)
	if len(scanner.carry) > maxMetadataBytes {
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
}

func (scanner *usageScanner) parseEventJSON(payload []byte) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(payload, &document); err != nil || document == nil {
		return
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
				if usage := decodeOpenAIResponsesUsage(response["usage"]); usage != nil {
					scanner.usage = usage
				}
				return
			}
		}
	}
	if usage := decodeOpenAIResponsesUsage(document["usage"]); usage != nil {
		scanner.usage = usage
	}
}

func decodeOpenAIResponsesUsage(raw json.RawMessage) *contract.Usage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var payload struct {
		InputTokens       *int `json:"input_tokens"`
		OutputTokens      *int `json:"output_tokens"`
		TotalTokens       *int `json:"total_tokens"`
		CachedInputTokens *int `json:"cached_input_tokens"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	if payload.InputTokens == nil || payload.OutputTokens == nil || payload.TotalTokens == nil {
		return nil
	}
	usage := &contract.Usage{
		InputTokens:  *payload.InputTokens,
		OutputTokens: *payload.OutputTokens,
		TotalTokens:  *payload.TotalTokens,
	}
	if payload.CachedInputTokens != nil {
		usage.CachedInputTokens = payload.CachedInputTokens
	}
	return usage
}

func (scanner *usageScanner) parseOpenAIChat(document map[string]json.RawMessage) {
	raw, ok := document["usage"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return
	}
	var payload struct {
		PromptTokens     *int `json:"prompt_tokens"`
		CompletionTokens *int `json:"completion_tokens"`
		TotalTokens      *int `json:"total_tokens"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return
	}
	if payload.PromptTokens == nil || payload.CompletionTokens == nil || payload.TotalTokens == nil {
		return
	}
	scanner.usage = &contract.Usage{
		InputTokens:  *payload.PromptTokens,
		OutputTokens: *payload.CompletionTokens,
		TotalTokens:  *payload.TotalTokens,
	}
}

func (scanner *usageScanner) parseAnthropic(document map[string]json.RawMessage) {
	var eventType string
	if raw, ok := document["type"]; ok {
		_ = json.Unmarshal(raw, &eventType)
	}
	switch eventType {
	case "message_start":
		var message struct {
			Usage struct {
				InputTokens *int `json:"input_tokens"`
			} `json:"usage"`
		}
		if raw, ok := document["message"]; ok {
			if json.Unmarshal(raw, &message) == nil && message.Usage.InputTokens != nil {
				scanner.anthropicInput = message.Usage.InputTokens
				scanner.anthropicSeen = true
			}
		}
	case "message_delta":
		if raw, ok := document["usage"]; ok {
			var usage struct {
				OutputTokens *int `json:"output_tokens"`
			}
			if json.Unmarshal(raw, &usage) == nil && usage.OutputTokens != nil {
				scanner.anthropicOutput += *usage.OutputTokens
				scanner.anthropicSeen = true
			}
		}
	default:
		if !scanner.streaming {
			var usage struct {
				InputTokens  *int `json:"input_tokens"`
				OutputTokens *int `json:"output_tokens"`
			}
			if raw, ok := document["usage"]; ok && json.Unmarshal(raw, &usage) == nil {
				if usage.InputTokens != nil && usage.OutputTokens != nil {
					total := *usage.InputTokens + *usage.OutputTokens
					scanner.usage = &contract.Usage{
						InputTokens:  *usage.InputTokens,
						OutputTokens: *usage.OutputTokens,
						TotalTokens:  total,
					}
				}
			}
		}
	}
}

func (scanner *usageScanner) parseGemini(document map[string]json.RawMessage) {
	raw, ok := document["usageMetadata"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return
	}
	var payload struct {
		PromptTokenCount     *int `json:"promptTokenCount"`
		CandidatesTokenCount *int `json:"candidatesTokenCount"`
		TotalTokenCount      *int `json:"totalTokenCount"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return
	}
	if payload.PromptTokenCount == nil || payload.CandidatesTokenCount == nil || payload.TotalTokenCount == nil {
		return
	}
	scanner.usage = &contract.Usage{
		InputTokens:  *payload.PromptTokenCount,
		OutputTokens: *payload.CandidatesTokenCount,
		TotalTokens:  *payload.TotalTokenCount,
	}
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
