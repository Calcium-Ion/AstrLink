package servicetest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

type responseFailure struct{ message string }

var errResponseTooLarge = errors.New("Provider response exceeded the test size limit.")

type responseReadFailure struct{ error }

func (err *responseReadFailure) Unwrap() error { return err.error }

func (err *responseFailure) Error() string { return err.message }

type textBlock struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Thought bool   `json:"thought"`
}
type envelope struct {
	Type   string          `json:"type"`
	Status string          `json:"status"`
	Error  json.RawMessage `json:"error"`
	// Anthropic message_start carries an object here; error envelopes can carry
	// a string. Leave it opaque until extracting an actual failure message.
	Message      json.RawMessage `json:"message"`
	Delta        json.RawMessage `json:"delta"`
	Response     *envelope       `json:"response"`
	ContentBlock *textBlock      `json:"content_block"`
	Content      []textBlock     `json:"content"`
	Output       []struct {
		Content []textBlock `json:"content"`
	} `json:"output"`
	Choices []struct {
		Text    string `json:"text"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Candidates []struct {
		Content struct {
			Parts []textBlock `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
}

func upstreamError(raw []byte) string {
	var value struct {
		Error   json.RawMessage `json:"error"`
		Message json.RawMessage `json:"message"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	if len(value.Error) != 0 && string(value.Error) != "null" {
		var message string
		if json.Unmarshal(value.Error, &message) == nil {
			return message
		}
		var detail struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(value.Error, &detail) == nil && detail.Message != "" {
			return detail.Message
		}
		return "Provider returned an error."
	}
	var message string
	_ = json.Unmarshal(value.Message, &message)
	return message
}

func decodeResponse(body io.Reader, protocol contract.ProtocolID, stream bool, _ string, onText func()) (string, error) {
	limited := &io.LimitedReader{R: body, N: maxResponseBytes + 1}
	if !stream {
		raw, err := io.ReadAll(limited)
		if err != nil {
			return "", &responseReadFailure{err}
		}
		if limited.N == 0 {
			return "", errResponseTooLarge
		}
		var value envelope
		if json.Unmarshal(raw, &value) != nil {
			return "", fmt.Errorf("Provider did not return a valid JSON inference response.")
		}
		if err := checkFailure(value, raw); err != nil {
			return "", err
		}
		output := responseText(value, protocol, false)
		if strings.TrimSpace(output) == "" {
			return "", fmt.Errorf("Provider returned no text output for the selected protocol.")
		}
		return output, nil
	}
	// Do not gate on Content-Type here. The official Codex client parses
	// the response body as SSE without requiring text/event-stream, and some
	// upstreams return a valid event stream with a missing or non-standard MIME
	// type.
	var output strings.Builder
	appendText := func(text string) {
		if text == "" {
			return
		}
		if output.Len() == 0 && onText != nil {
			onText()
		}
		output.WriteString(text)
	}
	completed := false
	var data []string
	consume := func() error {
		if len(data) == 0 {
			return nil
		}
		payload := strings.Join(data, "\n")
		data = nil
		if payload == "[DONE]" {
			if protocol == contract.ProtocolOpenAIChat || protocol == contract.ProtocolOpenAICompletions {
				completed = true
			}
			return nil
		}
		var value envelope
		if json.Unmarshal([]byte(payload), &value) != nil {
			return fmt.Errorf("Provider returned malformed stream data.")
		}
		if err := checkFailure(value, []byte(payload)); err != nil {
			return err
		}
		switch protocol {
		case contract.ProtocolOpenAIResponses:
			if value.Type == "response.output_text.delta" {
				var delta string
				if json.Unmarshal(value.Delta, &delta) != nil {
					return fmt.Errorf("Provider returned an invalid text delta.")
				}
				appendText(delta)
			}
			if value.Type == "response.completed" {
				completed = true
				if output.Len() == 0 && value.Response != nil {
					appendText(responseText(*value.Response, protocol, false))
				}
			}
		case contract.ProtocolAnthropicMessages:
			if value.Type == "content_block_start" && value.ContentBlock != nil && value.ContentBlock.Type == "text" {
				appendText(value.ContentBlock.Text)
			}
			if value.Type == "content_block_delta" {
				var delta textBlock
				if json.Unmarshal(value.Delta, &delta) == nil && delta.Type == "text_delta" {
					appendText(delta.Text)
				}
			}
			if value.Type == "message_stop" {
				completed = true
			}
		default:
			appendText(responseText(value, protocol, true))
			for _, choice := range value.Choices {
				if choice.FinishReason != nil && *choice.FinishReason != "" {
					completed = true
				}
			}
			for _, candidate := range value.Candidates {
				if candidate.FinishReason != "" {
					completed = true
				}
			}
		}
		return nil
	}
	// Decode events as the body arrives, not after buffering the whole stream.
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), maxResponseBytes+1)
	for scanner.Scan() {
		if limited.N == 0 {
			return output.String(), errResponseTooLarge
		}
		line := scanner.Text()
		if line == "" {
			if err := consume(); err != nil {
				return output.String(), err
			}
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		if limited.N == 0 {
			return output.String(), errResponseTooLarge
		}
		return output.String(), &responseReadFailure{err}
	}
	if limited.N == 0 {
		return output.String(), errResponseTooLarge
	}
	if err := consume(); err != nil {
		return output.String(), err
	}
	if !completed {
		return output.String(), fmt.Errorf("Provider stream ended before a completion event.")
	}
	if strings.TrimSpace(output.String()) == "" {
		return "", fmt.Errorf("Provider stream completed without text output.")
	}
	return output.String(), nil
}

func checkFailure(value envelope, raw []byte) error {
	if (len(value.Error) > 0 && string(value.Error) != "null") || value.Type == "error" || value.Type == "response.failed" || value.Type == "response.incomplete" || value.Status == "failed" || value.Status == "incomplete" {
		message := upstreamError(raw)
		if value.Response != nil {
			encoded, _ := json.Marshal(value.Response)
			if detail := upstreamError(encoded); detail != "" {
				message = detail
			}
		}
		if message == "" {
			message = "Provider could not complete the test response."
		}
		return &responseFailure{message: message}
	}
	return nil
}

func responseText(value envelope, protocol contract.ProtocolID, stream bool) string {
	var output strings.Builder
	switch protocol {
	case contract.ProtocolOpenAIResponses:
		for _, item := range value.Output {
			for _, block := range item.Content {
				if block.Type == "output_text" {
					output.WriteString(block.Text)
				}
			}
		}
	case contract.ProtocolAnthropicMessages:
		for _, block := range value.Content {
			if block.Type == "text" {
				output.WriteString(block.Text)
			}
		}
	case contract.ProtocolGoogleGenerateContent:
		for _, candidate := range value.Candidates {
			for _, part := range candidate.Content.Parts {
				if !part.Thought {
					output.WriteString(part.Text)
				}
			}
		}
	default:
		for _, choice := range value.Choices {
			if protocol == contract.ProtocolOpenAICompletions {
				output.WriteString(choice.Text)
			} else if stream {
				output.WriteString(choice.Delta.Content)
			} else {
				output.WriteString(choice.Message.Content)
			}
		}
	}
	return output.String()
}
