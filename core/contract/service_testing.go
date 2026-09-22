package contract

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ServiceTestRequest always targets one saved service, without routing or failover.
type ServiceTestRequest struct {
	Protocol ProtocolID `json:"protocol"`
	Model    string     `json:"model"`
	Stream   bool       `json:"stream"`
	Prompt   string     `json:"prompt,omitempty"`
}

func (input ServiceTestRequest) Validate(service Service) error {
	if !SupportsServiceTest(input.Protocol) {
		return fmt.Errorf("unsupported test protocol")
	}
	if strings.TrimSpace(input.Model) != input.Model || input.Model == "" || len(input.Model) > 256 || !utf8.ValidString(input.Model) || strings.ContainsFunc(input.Model, unicode.IsControl) {
		return fmt.Errorf("model must contain 1 to 256 bytes without surrounding whitespace or control characters")
	}
	if !utf8.ValidString(input.Prompt) || utf8.RuneCountInString(input.Prompt) > 2000 {
		return fmt.Errorf("prompt must contain at most 2000 characters")
	}
	if service.Kind == ServiceKindCodexSubscription && !input.Stream {
		return fmt.Errorf("Codex subscription tests require streaming")
	}
	for _, capability := range service.Capabilities {
		if capability.Protocol == input.Protocol && capability.ConvertTo == "" && (!input.Stream || capability.Streaming) {
			return nil
		}
	}
	return fmt.Errorf("service does not provide the selected protocol and streaming mode directly")
}

func SupportsServiceTest(protocol ProtocolID) bool {
	switch protocol {
	case ProtocolOpenAIResponses, ProtocolOpenAIChat, ProtocolOpenAICompletions, ProtocolAnthropicMessages, ProtocolGoogleGenerateContent:
		return true
	}
	return false
}

type ServiceTestResult struct {
	ServiceID  ServiceID  `json:"service_id"`
	Protocol   ProtocolID `json:"protocol"`
	Model      string     `json:"model"`
	Stream     bool       `json:"stream"`
	OK         bool       `json:"ok"`
	StatusCode int        `json:"status_code"`
	DurationMS int64      `json:"duration_ms"`
	// Upstream timings exclude credential preparation and request privacy processing.
	ResponseHeadersMS *int64 `json:"response_headers_ms"`
	// FirstTokenMS is time to the first non-empty visible text after gateway processing.
	// Nil for non-streaming responses, or when no text has arrived.
	FirstTokenMS         *int64 `json:"first_token_ms"`
	Output               string `json:"output"`
	RawResponse          string `json:"raw_response"`
	RawResponseTruncated bool   `json:"raw_response_truncated"`
	ResponseContentType  string `json:"response_content_type,omitempty"`
	ErrorCode            string `json:"error_code,omitempty"`
	Message              string `json:"message,omitempty"`
}
