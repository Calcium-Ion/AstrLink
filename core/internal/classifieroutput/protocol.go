package classifieroutput

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Layer 1 errors. Callers map them onto their fail-open fallback codes.
var (
	// ErrNoAssistantText covers tool-only replies, empty content, and
	// responses whose output carries no visible text.
	ErrNoAssistantText = errors.New("classifier response has no assistant text")
	// ErrRefusal marks a refusal annotation instead of an answer.
	ErrRefusal = errors.New("classifier response is a refusal")
	// ErrIncompleteOutput marks truncated or otherwise unfinished replies.
	ErrIncompleteOutput = errors.New("classifier response is incomplete")
	// ErrAmbiguousText marks replies with more than one assistant text block;
	// concatenating them could stitch a fabricated answer.
	ErrAmbiguousText = errors.New("classifier response has more than one assistant text")
)

// AssistantText unpacks a gateway-native OpenAI Responses object into the one
// assistant-visible text. Reasoning parts, tool calls, and refusal
// annotations never substitute for visible text; an incomplete status is not
// an answer. body is the final client-facing response document, not a raw
// upstream frame.
func AssistantText(body []byte) (string, error) {
	if len(body) > MaxResponseBytes {
		return "", fmt.Errorf("classifier response exceeds %d bytes", MaxResponseBytes)
	}
	var response struct {
		Status     string `json:"status"`
		Incomplete *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Output []struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Role    string `json:"role"`
			Content []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("classifier response is not a Responses object: %w", err)
	}
	if response.Status == "incomplete" || response.Incomplete != nil {
		return "", ErrIncompleteOutput
	}
	var texts []string
	for _, output := range response.Output {
		if output.Type != "message" || (output.Status != "" && output.Status != "completed") {
			continue
		}
		for _, content := range output.Content {
			if content.Refusal != "" {
				return "", ErrRefusal
			}
			if content.Type != "output_text" {
				continue
			}
			text := strings.TrimSpace(content.Text)
			if text != "" {
				texts = append(texts, text)
			}
		}
	}
	switch len(texts) {
	case 1:
		return texts[0], nil
	case 0:
		return "", ErrNoAssistantText
	default:
		return "", ErrAmbiguousText
	}
}
