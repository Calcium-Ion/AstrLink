package ingress

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

const thinkingSignatureRecoveryReason = "thinking_signature_repair"

// Model mapping must be resolved first: Anthropic-compatible providers such
// as DeepSeek and Kimi require thinking to be passed back unchanged. Unknown
// models are deliberately excluded, even when they accept the Messages API.
func supportsThinkingSignatureRecovery(plan contract.ExecutionPlan, model string) bool {
	if plan.Type == contract.PlanTypeRelayKit || plan.InputProtocol != contract.ProtocolAnthropicMessages || plan.UpstreamProtocol != contract.ProtocolAnthropicMessages {
		return false
	}
	model = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(model)), "anthropic/")
	for _, prefix := range []string{"claude-", "opus-", "sonnet-", "haiku-"} {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

func isThinkingSignatureError(body []byte) bool {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return false
	}
	message := envelope.Error.Message
	if message == "" {
		message = envelope.Message
	}
	message = strings.ToLower(message)
	thinking := strings.Contains(message, "thinking")
	// A bare authentication/request signature error is unrelated. Some
	// validators identify the history block only through its JSON field path.
	if strings.Contains(message, "signature") && (thinking || (strings.Contains(message, "messages.") && strings.Contains(message, ".signature"))) {
		return true
	}
	return thinking && ((strings.Contains(message, "expected") && strings.Contains(message, "found")) || strings.Contains(message, "cannot be modified") || strings.Contains(message, "thinking block must contain"))
}

// Repair only the assistant's history and the thinking-dependent settings.
// Tool calls, results, user content and unknown fields retain their semantics;
// UseNumber prevents corrupting large integers in tool inputs or metadata.
func rectifyThinkingSignature(body []byte) ([]byte, bool) {
	if !json.Valid(body) {
		return nil, false
	}
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&document) != nil {
		return nil, false
	}
	messages, ok := document["messages"].([]any)
	if !ok || len(messages) == 0 {
		return nil, false
	}
	changed := false
	for _, entry := range messages {
		message, ok := entry.(map[string]any)
		if !ok || message["role"] != "assistant" {
			continue
		}
		content, ok := message["content"].([]any)
		if !ok {
			continue
		}
		repaired := make([]any, 0, len(content))
		modified := false
		for _, entry := range content {
			block, ok := entry.(map[string]any)
			if ok {
				switch block["type"] {
				case "thinking":
					modified = true
					if text, _ := block["thinking"].(string); strings.TrimSpace(text) != "" {
						repaired = append(repaired, map[string]any{"type": "text", "text": text})
					}
					continue
				case "redacted_thinking":
					modified = true
					continue
				}
			}
			repaired = append(repaired, entry)
		}
		if modified {
			if len(repaired) == 0 {
				repaired = append(repaired, map[string]any{"type": "text", "text": "(thinking omitted)"})
			}
			message["content"] = repaired
			changed = true
		}
	}
	if _, exists := document["thinking"]; exists {
		delete(document, "thinking")
		changed = true
	}
	if !changed {
		return nil, false
	}
	if management, ok := document["context_management"].(map[string]any); ok {
		if edits, ok := management["edits"].([]any); ok {
			kept := make([]any, 0, len(edits))
			for _, entry := range edits {
				edit, _ := entry.(map[string]any)
				kind, _ := edit["type"].(string)
				if !strings.HasPrefix(kind, "clear_thinking_") {
					kept = append(kept, entry)
				}
			}
			if len(kept) != len(edits) {
				if len(kept) == 0 {
					delete(management, "edits")
				} else {
					management["edits"] = kept
				}
				if len(management) == 0 {
					delete(document, "context_management")
				}
			}
		}
	}
	result, err := json.Marshal(document)
	return result, err == nil
}

func replaceRecoveryRequestBody(request *http.Request, body []byte) {
	_ = request.Body.Close()
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	request.ContentLength = int64(len(body))
	request.Header.Del("Content-Length")
	request.TransferEncoding = nil
}

func prepareReasoningRecovery(source *requestBodySource, repair func([]byte) ([]byte, bool)) []byte {
	body, err := source.factory()
	if err != nil {
		return nil
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maxMetadataBytes+1))
	if err != nil || len(data) > maxMetadataBytes {
		return nil
	}
	repaired, changed := repair(data)
	if !changed {
		return nil
	}
	return repaired
}

// Inspect only a bounded, complete error. Preserve all bytes for the final
// response (including an oversized body) when no repair is selected.
func inspectRecoveryError(response *http.Response) ([]byte, bool) {
	original := response.Body
	timer := time.AfterFunc(time.Second, func() { _ = original.Close() })
	data, err := io.ReadAll(io.LimitReader(original, 64*1024+1))
	withinLimit := timer.Stop()
	if err != nil || !withinLimit {
		_ = original.Close()
		// The inspected error is incomplete. Sending partial JSON through a
		// buffered response adapter could turn this HTTP 400 into a conversion
		// failure and accidentally schedule a network retry. Keep its status and
		// send one complete error envelope instead.
		const fallback = `{"type":"error","error":{"type":"api_error","message":"upstream returned an incomplete error response"}}`
		response.Body = io.NopCloser(strings.NewReader(fallback))
		response.ContentLength = int64(len(fallback))
		if response.Header == nil {
			response.Header = make(http.Header)
		}
		response.Header.Del("Content-Length")
		response.Header.Del("Content-Encoding")
		response.Header.Set("Content-Type", "application/json")
		return nil, false
	}
	response.Body = &joinedReadCloser{Reader: io.MultiReader(bytes.NewReader(data), original), closer: original}
	return data, len(data) <= 64*1024
}
