package servicetest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

type eventReader struct {
	chunks []string
	reads  int
	before func(int)
	err    error
}

func (reader *eventReader) Read(buffer []byte) (int, error) {
	if reader.reads == len(reader.chunks) {
		if reader.err != nil {
			return 0, reader.err
		}
		return 0, io.EOF
	}
	if reader.before != nil {
		reader.before(reader.reads)
	}
	chunk := reader.chunks[reader.reads]
	n := copy(buffer, chunk)
	if n < len(chunk) {
		reader.chunks[reader.reads] = chunk[n:]
	} else {
		reader.reads++
	}
	return n, nil
}

func TestFirstTextIsObservedWhileReadingEveryStreamProtocol(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol contract.ProtocolID
		chunks   []string
	}{
		{"responses", contract.ProtocolOpenAIResponses, []string{
			"data: {\"type\":\"response.created\"}\n\n",
			"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"thinking\"}\n\n",
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"\"}\n\n",
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n",
			"data: {\"type\":\"response.completed\"}\n\n",
		}},
		{"chat", contract.ProtocolOpenAIChat, []string{
			": heartbeat\n\n",
			"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n",
			"data: {\"choices\":[{\"delta\":{\"content\":\"\"}}]}\n\n",
			"data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n",
			"data: [DONE]\n\n",
		}},
		{"completions", contract.ProtocolOpenAICompletions, []string{
			": heartbeat\n\n", ": another heartbeat\n\n",
			"data: {\"choices\":[{\"text\":\"\"}]}\n\n",
			"data: {\"choices\":[{\"text\":\"OK\"}]}\n\n",
			"data: [DONE]\n\n",
		}},
		{"anthropic", contract.ProtocolAnthropicMessages, []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-5\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":12,\"output_tokens\":1}}}\n\n",
			"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"thinking\"}}\n\n",
			"data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
			"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\n",
			"data: {\"type\":\"message_stop\"}\n\n",
		}},
		{"gemini", contract.ProtocolGoogleGenerateContent, []string{
			": heartbeat\n\n",
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"thought\":true,\"text\":\"thinking\"}]}}]}\n\n",
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"\"}]}}]}\n\n",
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"OK\"}]}}]}\n\n",
			"data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &eventReader{chunks: test.chunks}
			calls := 0
			output, err := decodeResponse(reader, test.protocol, true, "text/event-stream", func() {
				calls++
				if reader.reads != 4 {
					t.Errorf("first text observed after read %d, want 4 before completion", reader.reads)
				}
			})
			if err != nil || output != "OK" || calls != 1 {
				t.Fatalf("output=%q error=%v calls=%d", output, err, calls)
			}
		})
	}
}

type timingTransport func(*http.Request) (*http.Response, error)

func (f timingTransport) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestTimingsSeparateHeadersFirstTextAndCompletion(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "interrupted"}[interrupted], func(t *testing.T) {
			reader := &eventReader{chunks: []string{
				": heartbeat\n\n",
				"data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n",
				"data: [DONE]\n\n",
			}, before: func(int) { time.Sleep(20 * time.Millisecond) }}
			if interrupted {
				reader.err = io.ErrUnexpectedEOF
			}
			forwarder := transport.New(timingTransport(func(*http.Request) (*http.Response, error) {
				time.Sleep(20 * time.Millisecond)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(reader)}, nil
			}))
			service := timingService()
			result := New(endpoint.NewServiceAuthorizer(nil, nil), forwarder, nil).Test(context.Background(), service, contract.ServiceTestRequest{Protocol: contract.ProtocolOpenAIChat, Model: "test", Stream: true})
			if result.OK == interrupted || result.ResponseHeadersMS == nil || result.FirstTokenMS == nil || result.Output != "OK" {
				t.Fatalf("result=%+v", result)
			}
			if *result.ResponseHeadersMS < 20 || *result.FirstTokenMS-*result.ResponseHeadersMS < 40 || result.DurationMS-*result.FirstTokenMS < 20 {
				t.Fatalf("headers=%d first=%d total=%d", *result.ResponseHeadersMS, *result.FirstTokenMS, result.DurationMS)
			}
			if interrupted && result.ErrorCode != "interrupted" {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func timingService() contract.Service {
	return contract.Service{ID: "service_test", Name: "Timing test", Kind: contract.ServiceKindOpenAI, HTTP: &contract.HTTPConnection{BaseURL: "https://example.test/v1", Auth: contract.EndpointAuth{Scheme: contract.AuthSchemeNone}}, Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true}}}
}

func TestMissingTimingIsNotZeroAndNonStreamingDoesNotInventFirstText(t *testing.T) {
	for _, test := range []struct {
		name            string
		status          int
		stream          bool
		body            string
		connectionError bool
		headers         bool
	}{
		{"network error", 0, true, "", true, false},
		{"HTTP error", 401, true, `{"error":{"message":"bad key"}}`, false, true},
		{"no text", 200, true, "data: [DONE]\n\n", false, true},
		{"non-streaming", 200, false, `{"choices":[{"message":{"content":"OK"}}]}`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			forwarder := transport.New(timingTransport(func(*http.Request) (*http.Response, error) {
				if test.connectionError {
					return nil, errors.New("connection failed")
				}
				return &http.Response{StatusCode: test.status, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			}))
			result := New(endpoint.NewServiceAuthorizer(nil, nil), forwarder, nil).Test(context.Background(), timingService(), contract.ServiceTestRequest{Protocol: contract.ProtocolOpenAIChat, Model: "test", Stream: test.stream})
			if (result.ResponseHeadersMS != nil) != test.headers || result.FirstTokenMS != nil {
				t.Fatalf("result=%+v", result)
			}
			if !test.stream && !result.OK {
				t.Fatalf("non-streaming result=%+v", result)
			}
		})
	}
}

func TestStreamingSizeLimitAndPartialEvents(t *testing.T) {
	reader := &eventReader{chunks: []string{"data: {\"choices\":[{\"delta\":{\"content\":", "\"OK\"}}]}\r\n", "\r\n", "data: [DONE]\r\n\r\n"}}
	_, err := decodeResponse(reader, contract.ProtocolOpenAIChat, true, "text/event-stream", func() {
		if reader.reads != 3 {
			t.Errorf("partial event counted as first text at read %d", reader.reads)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = decodeResponse(strings.NewReader(strings.Repeat(": heartbeat\n\n", maxResponseBytes/10)), contract.ProtocolOpenAIChat, true, "text/event-stream", nil)
	if !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("unbounded stream error=%v", err)
	}
}
