package ingress

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

func TestRestoringWriterRecomputesContentLength(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(recorder, []privacy.Redaction{
		{Placeholder: "<PRIVATE_EMAIL>", Kind: privacy.KindEmail, Value: "alice@example.com"},
	}, false, contract.ProtocolOpenAIChat)
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Content-Length", "12")
	writer.WriteHeader(http.StatusOK)
	if _, err := writer.Write([]byte(
		`{"choices":[{"index":0,"message":{"content":"<PRIVATE_EMAIL>"}}]}`,
	)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	body := recorder.Body.String()
	if body != `{"choices":[{"index":0,"message":{"content":"alice@example.com"}}]}` {
		t.Fatalf("body=%s", body)
	}
	if recorder.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Fatalf("content-length=%q body=%d", recorder.Header().Get("Content-Length"), len(body))
	}
}

func TestRestoringWriterDoesNotCommitBufferedResponseOnTransportFlush(t *testing.T) {
	recorder := httptest.NewRecorder()
	downstream := newCommitTrackingWriter(recorder)
	writer := newRestoringResponseWriter(downstream, []privacy.Redaction{
		{Placeholder: "<PRIVATE_EMAIL>", Kind: privacy.KindEmail, Value: "alice@example.com"},
	}, false, contract.ProtocolOpenAIChat)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusCreated)
	writer.Flush()
	if downstream.Committed() {
		t.Fatal("non-streaming transport flush committed the buffered response")
	}
	if _, err := writer.Write([]byte(
		`{"choices":[{"index":0,"message":{"content":"<PRIVATE_EMAIL>"}}]}`,
	)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusCreated ||
		recorder.Body.String() !=
			`{"choices":[{"index":0,"message":{"content":"alice@example.com"}}]}` {
		t.Fatalf("response=%d %s", recorder.Code, recorder.Body.String())
	}
}

func TestRestoringWriterFailsClosedOnGzip(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(recorder, []privacy.Redaction{
		{Placeholder: "<PRIVATE_EMAIL>", Kind: privacy.KindEmail, Value: "alice@example.com"},
	}, false, contract.ProtocolOpenAIChat)
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Content-Encoding", "gzip")
	writer.WriteHeader(http.StatusOK)
	if _, err := writer.Write([]byte("not-restored")); err == nil {
		t.Fatal("expected write to fail after encoding rejection")
	}
	if recorder.Code != http.StatusBadGateway ||
		!strings.Contains(recorder.Body.String(), "upstream_content_encoding") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "alice@example.com") {
		t.Fatalf("plaintext leaked: %s", recorder.Body.String())
	}
}

func TestRestoringWriterStreamingCarryAndDropsContentLength(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(recorder, []privacy.Redaction{
		{Placeholder: "<PRIVATE_EMAIL>", Kind: privacy.KindEmail, Value: "alice@example.com"},
	}, true, contract.ProtocolOpenAIChat)
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Content-Length", "999")
	writer.WriteHeader(http.StatusOK)
	if recorder.Header().Get("Content-Length") != "" {
		t.Fatalf("streaming must delete content-length, got %q", recorder.Header().Get("Content-Length"))
	}
	if _, err := writer.Write([]byte(
		`data: {"choices":[{"index":0,"delta":{"content":"<PRIV`,
	)); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(`ATE_EMAIL>"}}]}` + "\n\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	if got := recorder.Body.String(); !strings.Contains(got, "alice@example.com") ||
		strings.Contains(got, "<PRIVATE_EMAIL>") {
		t.Fatalf("stream body=%q", got)
	}
}

func TestRestoringWriterPassthroughUnknownContentType(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(recorder, []privacy.Redaction{
		{Placeholder: "<PRIVATE_EMAIL>", Kind: privacy.KindEmail, Value: "alice@example.com"},
	}, false, contract.ProtocolOpenAIChat)
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.WriteHeader(http.StatusOK)
	payload := []byte(`opaque <PRIVATE_EMAIL>`)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	if recorder.Body.String() != string(payload) {
		t.Fatalf("passthrough body=%s", recorder.Body.String())
	}
}

func TestRestoringWriterRestoresRealChatPlaceholderAcrossSSEEvents(t *testing.T) {
	redactions := []privacy.Redaction{
		{
			Placeholder: "<PRIVATE_EMAIL_b76ad3c71b07c2e5>",
			Kind:        privacy.KindEmail,
			Value:       "alice@example.com",
		},
		{
			Placeholder: "<PRIVATE_EMAIL_b76ad3c71b07c2e5>",
			Kind:        privacy.KindEmail,
			Value:       "alice@example.com",
		},
		{
			Placeholder: "<PRIVATE_EMAIL_6fa24f340e5a1fd9>",
			Kind:        privacy.KindEmail,
			Value:       "bob@example.com",
		},
		{
			Placeholder: "<PRIVATE_PHONE_1c6ca32f9db4f7ec>",
			Kind:        privacy.KindPhone,
			Value:       "+1-415-555-0001",
		},
		{
			Placeholder: "<PRIVATE_PHONE_d573447ca9d701b5>",
			Kind:        privacy.KindPhone,
			Value:       "+1-415-555-0002",
		},
	}
	fragments := []string{
		"邮箱=<PRIVATE",
		"_EMAIL_b76ad3c71b07c2e5>\n备用邮箱=<",
		"PRIVATE_EMAIL_b76ad3c71b07c2e5>\n另一邮箱=<",
		"PRIVATE_EMAIL_6fa24f340e5a1fd9>\n电话A=<PRIVATE",
		"_PHONE_1c6ca32f9db4f7ec>\n电话B=<PRIVATE_PHONE",
		"_d573447ca9d701b5>",
	}

	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(
		recorder,
		redactions,
		true,
		contract.ProtocolOpenAIChat,
	)
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(http.StatusOK)
	for _, fragment := range fragments {
		event, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{"content": fragment},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		wire := append([]byte("data: "), event...)
		wire = append(wire, '\n', '\n')
		middle := len(wire) / 2
		if _, err := writer.Write(wire[:middle]); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(wire[middle:]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Write([]byte("data: [DONE]\n\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}

	want := "邮箱=alice@example.com\n" +
		"备用邮箱=alice@example.com\n" +
		"另一邮箱=bob@example.com\n" +
		"电话A=+1-415-555-0001\n" +
		"电话B=+1-415-555-0002"
	if got := visibleSSEText(t, contract.ProtocolOpenAIChat, recorder.Body.Bytes()); got != want {
		t.Fatalf("visible text:\n%s\nwant:\n%s\nwire:\n%s", got, want, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "<PRIVATE_") {
		t.Fatalf("placeholder reached the client: %s", recorder.Body.String())
	}
	if writer.mappingCount() != 4 ||
		writer.restoredCount() != 5 ||
		writer.fallbackCount() != 0 {
		t.Fatalf(
			"stats mapping=%d restored=%d fallback=%d",
			writer.mappingCount(),
			writer.restoredCount(),
			writer.fallbackCount(),
		)
	}
}

func TestRestoringWriterRestoresVisibleFieldsAcrossAlphaSSEProtocols(t *testing.T) {
	const placeholder = "<PRIVATE_EMAIL_7f3a91c04d28be56>"
	redactions := []privacy.Redaction{{
		Placeholder: placeholder,
		Kind:        privacy.KindEmail,
		Value:       "alice@example.com",
	}}
	tests := []struct {
		name     string
		protocol contract.ProtocolID
		event    func(string) map[string]any
	}{
		{
			name:     "chat",
			protocol: contract.ProtocolOpenAIChat,
			event: func(fragment string) map[string]any {
				return map[string]any{
					"choices": []any{map[string]any{
						"index": 0,
						"delta": map[string]any{"content": fragment},
					}},
				}
			},
		},
		{
			name:     "completions",
			protocol: contract.ProtocolOpenAICompletions,
			event: func(fragment string) map[string]any {
				return map[string]any{
					"choices": []any{map[string]any{"index": 0, "text": fragment}},
				}
			},
		},
		{
			name:     "responses",
			protocol: contract.ProtocolOpenAIResponses,
			event: func(fragment string) map[string]any {
				return map[string]any{
					"type":          "response.output_text.delta",
					"item_id":       "item_1",
					"content_index": 0,
					"delta":         fragment,
				}
			},
		},
		{
			name:     "anthropic",
			protocol: contract.ProtocolAnthropicMessages,
			event: func(fragment string) map[string]any {
				return map[string]any{
					"type":  "content_block_delta",
					"index": 0,
					"delta": map[string]any{
						"type": "text_delta",
						"text": fragment,
					},
				}
			},
		},
		{
			name:     "gemini",
			protocol: contract.ProtocolGoogleGenerateContent,
			event: func(fragment string) map[string]any {
				return map[string]any{
					"candidates": []any{map[string]any{
						"index": 0,
						"content": map[string]any{
							"parts": []any{map[string]any{"text": fragment}},
						},
					}},
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writer := newRestoringResponseWriter(recorder, redactions, true, test.protocol)
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.WriteHeader(http.StatusOK)
			split := len(placeholder) / 2
			for _, fragment := range []string{placeholder[:split], placeholder[split:]} {
				encoded, err := json.Marshal(test.event(fragment))
				if err != nil {
					t.Fatal(err)
				}
				wire := append([]byte("data: "), encoded...)
				wire = append(wire, '\n', '\n')
				if _, err := writer.Write(wire); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Finish(); err != nil {
				t.Fatal(err)
			}
			if got := visibleSSEText(t, test.protocol, recorder.Body.Bytes()); got != "alice@example.com" {
				t.Fatalf("visible=%q wire=%s", got, recorder.Body.String())
			}
			if writer.restoredCount() != 1 || writer.fallbackCount() != 0 {
				t.Fatalf(
					"restored=%d fallback=%d",
					writer.restoredCount(),
					writer.fallbackCount(),
				)
			}
		})
	}
}

func TestRestoringWriterLeavesReasoningThinkingAndToolArgumentsRedacted(t *testing.T) {
	const placeholder = "<PRIVATE_EMAIL_7f3a91c04d28be56>"
	redactions := []privacy.Redaction{{
		Placeholder: placeholder,
		Kind:        privacy.KindEmail,
		Value:       "alice@example.com",
	}}
	body := `{"choices":[{"index":0,"message":{` +
		`"content":"` + placeholder + `",` +
		`"reasoning_content":"` + placeholder + `",` +
		`"tool_calls":[{"function":{"arguments":"{\"email\":\"` + placeholder + `\"}"}}]` +
		`}}]}`
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(
		recorder,
		redactions,
		false,
		contract.ProtocolOpenAIChat,
	)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	if _, err := writer.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					Function struct {
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	message := decoded.Choices[0].Message
	if message.Content != "alice@example.com" ||
		message.ReasoningContent != placeholder ||
		!strings.Contains(message.ToolCalls[0].Function.Arguments, placeholder) {
		t.Fatalf("unexpected response: %s", recorder.Body.String())
	}
}

func TestRestoringWriterLeavesGeminiThoughtTextRedacted(t *testing.T) {
	const placeholder = "<PRIVATE_EMAIL_7f3a91c04d28be56>"
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(
		recorder,
		[]privacy.Redaction{{
			Placeholder: placeholder,
			Kind:        privacy.KindEmail,
			Value:       "alice@example.com",
		}},
		false,
		contract.ProtocolGoogleGenerateContent,
	)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	body := `{"candidates":[{"content":{"parts":[` +
		`{"thought":true,"text":"` + placeholder + `"},` +
		`{"text":"` + placeholder + `"}` +
		`]}}]}`
	if _, err := writer.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	parts := decoded.Candidates[0].Content.Parts
	if len(parts) != 2 ||
		parts[0].Text != placeholder ||
		parts[1].Text != "alice@example.com" {
		t.Fatalf("body=%s", recorder.Body.String())
	}
}

func TestRestoringWriterNonStreamingEscapesOriginalValueSafely(t *testing.T) {
	const placeholder = "<PRIVATE_PERSON_7f3a91c04d28be56>"
	value := "Ann \"Quote\"\nC:\\Users\\Ann"
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(
		recorder,
		[]privacy.Redaction{{
			Placeholder: placeholder,
			Kind:        privacy.KindPerson,
			Value:       value,
		}},
		false,
		contract.ProtocolOpenAIChat,
	)
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Content-Length", "999")
	writer.WriteHeader(http.StatusOK)
	if _, err := writer.Write([]byte(
		`{"choices":[{"index":0,"message":{"content":"` + placeholder + `"}}]}`,
	)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("invalid restored JSON: %v body=%s", err, recorder.Body.String())
	}
	if decoded.Choices[0].Message.Content != value {
		t.Fatalf("content=%q want=%q", decoded.Choices[0].Message.Content, value)
	}
	if recorder.Header().Get("Content-Length") != strconv.Itoa(recorder.Body.Len()) {
		t.Fatalf("content-length=%s size=%d", recorder.Header().Get("Content-Length"), recorder.Body.Len())
	}
}

func TestRestoringWriterNonStreamingVisibleFieldsAcrossAlphaProtocols(t *testing.T) {
	const placeholder = "<PRIVATE_EMAIL_7f3a91c04d28be56>"
	tests := []struct {
		name     string
		protocol contract.ProtocolID
		body     string
	}{
		{
			name:     "chat",
			protocol: contract.ProtocolOpenAIChat,
			body:     `{"choices":[{"message":{"content":"` + placeholder + `"}}]}`,
		},
		{
			name:     "completions",
			protocol: contract.ProtocolOpenAICompletions,
			body:     `{"choices":[{"text":"` + placeholder + `"}]}`,
		},
		{
			name:     "responses",
			protocol: contract.ProtocolOpenAIResponses,
			body: `{"output":[{"type":"message","content":[` +
				`{"type":"output_text","text":"` + placeholder + `"}]}]}`,
		},
		{
			name:     "responses compact",
			protocol: contract.ProtocolOpenAIResponsesCompact,
			body: `{"output":[{"type":"message","content":[` +
				`{"type":"refusal","refusal":"` + placeholder + `"}]}]}`,
		},
		{
			name:     "anthropic",
			protocol: contract.ProtocolAnthropicMessages,
			body: `{"content":[{"type":"text","text":"` +
				placeholder + `"}]}`,
		},
		{
			name:     "gemini",
			protocol: contract.ProtocolGoogleGenerateContent,
			body: `{"candidates":[{"content":{"parts":[{"text":"` +
				placeholder + `"}]}}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writer := newRestoringResponseWriter(
				recorder,
				[]privacy.Redaction{{
					Placeholder: placeholder,
					Kind:        privacy.KindEmail,
					Value:       "alice@example.com",
				}},
				false,
				test.protocol,
			)
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			if _, err := writer.Write([]byte(test.body)); err != nil {
				t.Fatal(err)
			}
			if err := writer.Finish(); err != nil {
				t.Fatal(err)
			}
			if !json.Valid(recorder.Body.Bytes()) ||
				!strings.Contains(recorder.Body.String(), "alice@example.com") ||
				strings.Contains(recorder.Body.String(), placeholder) ||
				writer.restoredCount() != 1 {
				t.Fatalf(
					"body=%s restored=%d fallback=%d",
					recorder.Body.String(),
					writer.restoredCount(),
					writer.fallbackCount(),
				)
			}
		})
	}
}

func TestRestoringWriterGeminiJSONArrayAndGracefulFallback(t *testing.T) {
	const placeholder = "<PRIVATE_EMAIL_7f3a91c04d28be56>"
	redactions := []privacy.Redaction{{
		Placeholder: placeholder,
		Kind:        privacy.KindEmail,
		Value:       "alice@example.com",
	}}
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(
		recorder,
		redactions,
		true,
		contract.ProtocolGoogleGenerateContent,
	)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	split := len(placeholder) / 2
	first := `[{"candidates":[{"index":0,"content":{"parts":[{"text":"` +
		placeholder[:split] + `"}]}}]},`
	second := `{"candidates":[{"index":0,"content":{"parts":[{"text":"` +
		placeholder[split:] + `"}]}}]}]`
	if _, err := writer.Write([]byte(first)); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(second)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	var values []map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &values); err != nil {
		t.Fatalf("invalid JSON array: %v body=%s", err, recorder.Body.String())
	}
	if strings.Count(recorder.Body.String(), "alice@example.com") != 1 ||
		strings.Contains(recorder.Body.String(), placeholder) {
		t.Fatalf("body=%s", recorder.Body.String())
	}
	if writer.restoredCount() != 1 || writer.fallbackCount() != 0 {
		t.Fatalf("restored=%d fallback=%d", writer.restoredCount(), writer.fallbackCount())
	}

	fallbackRecorder := httptest.NewRecorder()
	fallback := newRestoringResponseWriter(
		fallbackRecorder,
		redactions,
		true,
		contract.ProtocolOpenAIChat,
	)
	fallback.Header().Set("Content-Type", "text/event-stream")
	fallback.WriteHeader(http.StatusOK)
	raw := "data: {malformed}\n\ndata: [DONE]\n\n"
	if _, err := fallback.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	if err := fallback.Finish(); err != nil {
		t.Fatal(err)
	}
	if fallbackRecorder.Body.String() != raw || fallback.fallbackCount() != 1 {
		t.Fatalf(
			"fallback body=%q count=%d",
			fallbackRecorder.Body.String(),
			fallback.fallbackCount(),
		)
	}
}

func TestRestoringWriterNeverCombinesDifferentChoices(t *testing.T) {
	const placeholder = "<PRIVATE_EMAIL_7f3a91c04d28be56>"
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(
		recorder,
		[]privacy.Redaction{{
			Placeholder: placeholder,
			Kind:        privacy.KindEmail,
			Value:       "alice@example.com",
		}},
		true,
		contract.ProtocolOpenAIChat,
	)
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(http.StatusOK)
	split := len(placeholder) / 2
	for index, fragment := range []string{placeholder[:split], placeholder[split:]} {
		event, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{
				"index": index,
				"delta": map[string]any{"content": fragment},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		wire := append([]byte("data: "), event...)
		wire = append(wire, '\n', '\n')
		if _, err := writer.Write(wire); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Write([]byte("data: [DONE]\n\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(recorder.Body.String(), "alice@example.com") {
		t.Fatalf("different choices were combined: %s", recorder.Body.String())
	}
	if visibleSSEText(t, contract.ProtocolOpenAIChat, recorder.Body.Bytes()) != placeholder ||
		writer.restoredCount() != 0 ||
		writer.fallbackCount() != 2 {
		t.Fatalf(
			"body=%s restored=%d fallback=%d",
			recorder.Body.String(),
			writer.restoredCount(),
			writer.fallbackCount(),
		)
	}
}

func TestRestoringWriterDoesNotRestoreLegacyFixedPlaceholder(t *testing.T) {
	const randomPlaceholder = "<PRIVATE_EMAIL_7f3a91c04d28be56>"
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(
		recorder,
		[]privacy.Redaction{{
			Placeholder: randomPlaceholder,
			Kind:        privacy.KindEmail,
			Value:       "alice@example.com",
		}},
		false,
		contract.ProtocolOpenAIChat,
	)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	body := `{"choices":[{"index":0,"message":{"content":"<PRIVATE_EMAIL>"}}]}`
	if _, err := writer.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	if recorder.Body.String() != body || writer.restoredCount() != 0 {
		t.Fatalf("legacy placeholder changed: %s", recorder.Body.String())
	}
}

func visibleSSEText(
	t *testing.T,
	protocol contract.ProtocolID,
	wire []byte,
) string {
	t.Helper()
	var text strings.Builder
	for len(wire) > 0 {
		end := nextSSEFrameEnd(wire)
		if end == 0 {
			break
		}
		frame := wire[:end]
		wire = wire[end:]
		data := parseSSEData(frame)
		if data == nil || bytesEqualFoldSpace(data, []byte("[DONE]")) {
			continue
		}
		root, err := decodeJSONValue(data)
		if err != nil {
			t.Fatalf("decode SSE data: %v data=%s", err, data)
		}
		for _, ref := range visibleResponseTextRefs(protocol, root, 0) {
			if ref.channel == "error:message" {
				continue
			}
			text.WriteString(ref.value)
		}
	}
	return text.String()
}

func bytesEqualFoldSpace(left, right []byte) bool {
	return strings.TrimSpace(string(left)) == string(right)
}
