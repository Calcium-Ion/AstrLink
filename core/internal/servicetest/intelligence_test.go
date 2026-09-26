package servicetest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
)

func TestIntelligenceSendsImageAndKeepsLongAnswer(t *testing.T) {
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	answer := strings.Repeat("x", 5000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		raw, _ := json.Marshal(body)
		if !strings.Contains(string(raw), "image_url") || !strings.Contains(string(raw), "data:image/png;base64,") || body["max_tokens"] != nil || body["max_completion_tokens"] != nil {
			t.Errorf("image missing or output capped: %s", raw)
		}
		if strings.Contains(string(raw), "connection test") {
			t.Error("connection prompt leaked into intelligence test")
		}
		if strings.Contains(strings.ToLower(r.Header.Get("User-Agent")), "astrlink") || r.Header.Get("X-AstrLink-Test") != "" {
			t.Error("gateway identity forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	service := timingService()
	service.Kind = contract.ServiceKindOpenAICompatible
	service.HTTP.BaseURL = server.URL
	result := New(endpoint.NewServiceAuthorizer(nil, nil), nil, nil).TestIntelligence(context.Background(), service, contract.IntelligenceRequest{
		ServiceTestRequest: contract.ServiceTestRequest{Protocol: contract.ProtocolOpenAIChat, Model: "test", Prompt: "Evaluate this picture."}, TimeoutSeconds: 120,
		Images: []string{"data:image/png;base64," + base64.StdEncoding.EncodeToString(picture.Bytes())},
	})
	if !result.OK || result.Output != answer {
		t.Fatalf("long output lost: ok=%v len=%d error=%s", result.OK, len(result.Output), result.Message)
	}
}

func TestIncompleteAnswerIsNotSuccessful(t *testing.T) {
	_, err := decodeResponse(strings.NewReader(`{"choices":[{"message":{"content":"21"},"finish_reason":"length"}]}`), contract.ProtocolOpenAIChat, false, "application/json", func() {})
	if err == nil {
		t.Fatal("truncated answer accepted")
	}
	_, err = decodeResponse(io.NopCloser(strings.NewReader(`{"content":[{"type":"text","text":"21"}],"stop_reason":"max_tokens"}`)), contract.ProtocolAnthropicMessages, false, "application/json", func() {})
	if err == nil {
		t.Fatal("truncated Anthropic answer accepted")
	}
}

func TestIntelligenceAcceptsLongReasoningStreamWithoutExpandingConnectionLimit(t *testing.T) {
	// The visible answer is tiny, but reasoning events exceed the connection
	// test's wire limit before the answer arrives (as with DeepSeek streams).
	reasoning := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"" + strings.Repeat("r", 2048) + "\"}}]}\n\n"
	stream := strings.Repeat(reasoning, maxResponseBytes/len(reasoning)+1) +
		"data: {\"choices\":[{\"delta\":{\"content\":\"21\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}))
	defer server.Close()
	service := timingService()
	service.Kind = contract.ServiceKindOpenAICompatible
	service.HTTP.BaseURL = server.URL
	tester := New(endpoint.NewServiceAuthorizer(nil, nil), nil, nil)
	request := contract.ServiceTestRequest{Protocol: contract.ProtocolOpenAIChat, Model: "test", Prompt: "Solve the question.", Stream: true}
	result := tester.TestIntelligence(context.Background(), service, contract.IntelligenceRequest{
		ServiceTestRequest: request, TimeoutSeconds: 120,
	})
	if !result.OK || result.Output != "21" || !result.RawResponseTruncated || len([]rune(result.RawResponse)) > maxRawResponseCharacters+1 {
		t.Fatalf("reasoning stream: ok=%v output=%q truncated=%v error=%s", result.OK, result.Output, result.RawResponseTruncated, result.Message)
	}
	connection := tester.Test(context.Background(), service, request)
	if connection.OK || connection.ErrorCode != "response_too_large" {
		t.Fatalf("connection limit changed: ok=%v error=%s", connection.OK, connection.ErrorCode)
	}
}

func TestIntelligenceLeavesOptionalOutputLimitsToProvider(t *testing.T) {
	for _, protocol := range []contract.ProtocolID{contract.ProtocolOpenAIChat, contract.ProtocolOpenAIResponses, contract.ProtocolGoogleGenerateContent} {
		t.Run(string(protocol), func(t *testing.T) {
			request := contract.ServiceTestRequest{Protocol: protocol, Model: "gpt-6-astra", Prompt: "Solve the question."}
			_, body := intelligencePayload(contract.ServiceKindOpenAI, contract.IntelligenceRequest{ServiceTestRequest: request, TimeoutSeconds: 120})
			for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "generationConfig"} {
				if _, ok := body[key]; ok {
					t.Fatalf("unexpected output cap %s: %v", key, body[key])
				}
			}
			_, connection := testPayload(contract.ServiceKindOpenAI, request)
			if connection["max_completion_tokens"] == nil && connection["max_output_tokens"] == nil && connection["generationConfig"] == nil {
				t.Fatal("connection-test budget was removed")
			}
		})
	}
	_, anthropic := intelligencePayload(contract.ServiceKindAnthropic, contract.IntelligenceRequest{ServiceTestRequest: contract.ServiceTestRequest{Protocol: contract.ProtocolAnthropicMessages, Model: "claude", Prompt: "Solve the question."}, TimeoutSeconds: 120})
	if anthropic["max_tokens"] != 16384 {
		t.Fatal("Anthropic requires max_tokens")
	}
}
