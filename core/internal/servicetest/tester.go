// Package servicetest sends one bounded inference request to a saved provider
// through gateway privacy and recording, without routing, retries or failover.
package servicetest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

const Timeout = 60 * time.Second
const maxResponseBytes = 1 << 20
const maxRawResponseCharacters = 64 << 10

type Tester struct {
	gateway             *ingress.Handler
	identities          *accountauth.IdentityRegistry
	subscriptionBaseURL func(contract.SubscriptionProvider) string
}

func New(authorizer endpoint.Authorizer, forwarder *transport.Forwarder, subscriptionBaseURL func(contract.SubscriptionProvider) string) *Tester {
	dependencies := ingress.Dependencies{Authorizer: authorizer}
	if forwarder != nil {
		dependencies.Forwarder = forwarder
	}
	return NewWithDependencies(dependencies, subscriptionBaseURL)
}

// NewWithDependencies shares the gateway's privacy, audit and provider
// dependencies. Its handler is reachable only from the control-plane tester.
func NewWithDependencies(dependencies ingress.Dependencies, subscriptionBaseURL func(contract.SubscriptionProvider) string) *Tester {
	return &Tester{gateway: ingress.NewWithDependencies(dependencies), identities: dependencies.Identities, subscriptionBaseURL: subscriptionBaseURL}
}

func (tester *Tester) Test(ctx context.Context, service contract.Service, input contract.ServiceTestRequest) (result contract.ServiceTestResult) {
	started := time.Now()
	result = contract.ServiceTestResult{ServiceID: service.ID, Protocol: input.Protocol, Model: input.Model, Stream: input.Stream}
	defer func() { result.DurationMS = time.Since(started).Milliseconds() }()
	fail := func(code, message string) contract.ServiceTestResult {
		result.ErrorCode, result.Message = code, message
		return result
	}
	if err := input.Validate(service); err != nil {
		return fail("invalid_test", err.Error())
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	path, payload := testPayload(service.Kind, input)
	body, _ := json.Marshal(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return fail("invalid_configuration", "Could not build the provider test request.")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if input.Stream {
		request.Header.Set("Accept", "text/event-stream")
	}
	if input.Protocol == contract.ProtocolAnthropicMessages {
		request.Header.Set("Anthropic-Version", "2023-06-01")
	}
	if service.Kind == contract.ServiceKindClaudeSubscription {
		request.Header.Set("User-Agent", tester.identities.ClaudeIdentityFor(ctx).UserAgent)
	}
	exchange, err := tester.execute(request, service, input)
	if err != nil {
		if ctx.Err() != nil {
			return fail("timeout", "Provider test timed out or was cancelled.")
		}
		return fail("connection_failed", "Could not connect to the provider. Check its URL, network and TLS configuration.")
	}
	response := exchange.response
	defer response.Body.Close()
	headers := exchange.credentials
	// Only credential-redacted, bounded output crosses the control boundary.
	defer func() {
		result.Output = redact(result.Output, headers, 4096)
		result.Message = redact(result.Message, headers, 1000)
	}()
	result.StatusCode = exchange.upstreamStatus
	result.ResponseHeadersMS = exchange.headersMS
	result.ResponseContentType = redact(response.Header.Get("Content-Type"), headers, 256)
	var captured bytes.Buffer
	// Capture the actual bytes consumed by the parser, including SSE framing,
	// error envelopes and malformed data. Never reconstruct raw data from text.
	responseBody := io.TeeReader(io.LimitReader(response.Body, maxResponseBytes+1), &captured)
	readFailed := false
	defer func() {
		// A parser can reject an early event or the content type. Still retain
		// the bounded body for diagnosis; the request deadline also bounds this read.
		_, drainErr := io.Copy(io.Discard, responseBody)
		redacted := redact(captured.String(), headers, maxResponseBytes+1)
		result.RawResponseTruncated = readFailed || drainErr != nil || captured.Len() > maxResponseBytes || len([]rune(redacted)) > maxRawResponseCharacters
		result.RawResponse = redact(redacted, nil, maxRawResponseCharacters)
	}()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, readErr := io.ReadAll(responseBody)
		if readErr != nil {
			readFailed = true
			if errors.Is(readErr, errResponseTooLarge) {
				return fail("response_too_large", errResponseTooLarge.Error())
			}
			if ctx.Err() != nil {
				return fail("timeout", "Provider test timed out or was cancelled.")
			}
			return fail("interrupted", "Provider response was interrupted.")
		}
		if len(raw) > maxResponseBytes {
			return fail("response_too_large", errResponseTooLarge.Error())
		}
		message := upstreamError(raw)
		if message == "" {
			message = "Provider returned HTTP " + response.Status + "."
		}
		code := "upstream_error"
		if exchange.upstreamStatus == 0 || response.StatusCode != exchange.upstreamStatus {
			var envelope struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal(raw, &envelope) == nil && envelope.Error.Code != "" {
				code = envelope.Error.Code
			}
			switch code {
			case "upstream_unavailable":
				code = "connection_failed"
			case "upstream_timeout":
				code = "timeout"
			case "invalid_endpoint_configuration":
				code = "invalid_configuration"
			}
		}
		return fail(code, message)
	}
	result.Output, err = decodeResponse(responseBody, input.Protocol, input.Stream, response.Header.Get("Content-Type"), func() {
		firstTokenMS := time.Since(exchange.sentAt).Milliseconds()
		result.FirstTokenMS = &firstTokenMS
	})
	if err != nil {
		if errors.Is(err, errResponseTooLarge) {
			return fail("response_too_large", err.Error())
		}
		var readErr *responseReadFailure
		if errors.As(err, &readErr) {
			readFailed = true
			if ctx.Err() != nil {
				return fail("timeout", "Provider test timed out or was cancelled.")
			}
			return fail("interrupted", "Provider response was interrupted.")
		}
		var upstream *responseFailure
		if errors.As(err, &upstream) {
			return fail("upstream_error", upstream.message)
		}
		return fail("invalid_response", err.Error())
	}
	result.OK = true
	return result
}

func testPayload(kind contract.ServiceKind, input contract.ServiceTestRequest) (string, map[string]any) {
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		prompt = "Reply with OK."
	}
	body := map[string]any{"model": input.Model, "stream": input.Stream}
	switch input.Protocol {
	case contract.ProtocolOpenAIResponses:
		body["input"] = []any{map[string]any{"role": "user", "content": []any{map[string]string{"type": "input_text", "text": prompt}}}}
		body["store"] = false
		body["instructions"] = "This is a connection test. Reply briefly."
		if kind == contract.ServiceKindCodexSubscription {
			return "/v1/responses", body
		}
		body["max_output_tokens"] = 1024
		return "/v1/responses", body
	case contract.ProtocolOpenAIChat, contract.ProtocolAnthropicMessages:
		body["messages"] = []any{map[string]string{"role": "user", "content": prompt}}
		if input.Protocol == contract.ProtocolAnthropicMessages {
			body["max_tokens"] = 1024
			if kind == contract.ServiceKindClaudeSubscription {
				body["system"] = "You are Claude Code, Anthropic's official CLI for Claude."
			}
			return "/v1/messages", body
		}
		// Modern OpenAI reasoning models reject max_tokens; other compatible
		// providers commonly still use it.
		model := strings.ToLower(input.Model)
		if kind == contract.ServiceKindOpenAI || strings.HasPrefix(model, "gpt-5") || strings.HasPrefix(model, "o1") || strings.HasPrefix(model, "o3") || strings.HasPrefix(model, "o4") {
			body["max_completion_tokens"] = 1024
		} else {
			body["max_tokens"] = 1024
		}
		return "/v1/chat/completions", body
	case contract.ProtocolOpenAICompletions:
		body["prompt"], body["max_tokens"] = prompt, 128
		return "/v1/completions", body
	default:
		body = map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]string{"text": prompt}}}}, "generationConfig": map[string]any{"maxOutputTokens": 1024}}
		action := ":generateContent"
		if input.Stream {
			action = ":streamGenerateContent?alt=sse"
		}
		return "/v1beta/models/" + url.PathEscape(strings.TrimPrefix(input.Model, "models/")) + action, body
	}
}

func redact(value string, headers http.Header, limit int) string {
	for _, values := range headers {
		for _, secret := range values {
			if secret == "" {
				continue
			}
			value = strings.ReplaceAll(value, secret, "[redacted]")
			if strings.HasPrefix(secret, "Bearer ") {
				value = strings.ReplaceAll(value, strings.TrimPrefix(secret, "Bearer "), "[redacted]")
			}
		}
	}
	runes := []rune(strings.ToValidUTF8(value, "�"))
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return string(runes)
}
