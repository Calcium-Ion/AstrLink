package ingress

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func intPtr(value int) *int { return &value }

func TestUsageScannerProtocols(t *testing.T) {
	tests := []struct {
		name      string
		protocol  contract.ProtocolID
		streaming bool
		chunks    []string
		want      *contract.Usage
	}{
		{
			name:     "openai responses non-streaming",
			protocol: contract.ProtocolOpenAIResponses,
			chunks:   []string{`{"id":"resp_out","usage":{"input_tokens":3,"output_tokens":5,"total_tokens":8}}`},
			want:     &contract.Usage{InputTokens: 3, OutputTokens: 5, TotalTokens: 8},
		},
		{
			name:     "openai responses input_tokens_details cached",
			protocol: contract.ProtocolOpenAIResponses,
			chunks: []string{
				`{"id":"r","usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12,"input_tokens_details":{"cached_tokens":4}}}`,
			},
			want: &contract.Usage{
				InputTokens: 10, OutputTokens: 2, TotalTokens: 12,
				CacheReadTokens: intPtr(4),
			},
		},
		{
			name:     "openai responses top-level cached_input_tokens fallback",
			protocol: contract.ProtocolOpenAIResponses,
			chunks: []string{
				`{"id":"r","usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12,"cached_input_tokens":3}}`,
			},
			want: &contract.Usage{
				InputTokens: 10, OutputTokens: 2, TotalTokens: 12,
				CacheReadTokens: intPtr(3),
			},
		},
		{
			name:      "openai responses sse completed event",
			protocol:  contract.ProtocolOpenAIResponses,
			streaming: true,
			chunks: []string{
				"event: response.created\ndata: {\"type\":\"response.created\"}\n\n",
				"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"total_tokens\":3}}}\n",
			},
			want: &contract.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
		},
		{
			name:      "chat sse last usage-bearing chunk wins",
			protocol:  contract.ProtocolOpenAIChat,
			streaming: true,
			chunks: []string{
				"data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n",
				"data: {\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":6,\"total_tokens\":10}}\n",
				"data: [DONE]\n",
			},
			want: &contract.Usage{InputTokens: 4, OutputTokens: 6, TotalTokens: 10},
		},
		{
			name:     "chat prompt_tokens_details cached",
			protocol: contract.ProtocolOpenAIChat,
			chunks: []string{
				`{"id":"c","usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25,"prompt_tokens_details":{"cached_tokens":8}}}`,
			},
			want: &contract.Usage{
				InputTokens: 20, OutputTokens: 5, TotalTokens: 25,
				CacheReadTokens: intPtr(8),
			},
		},
		{
			name:      "anthropic input plus output deltas",
			protocol:  contract.ProtocolAnthropicMessages,
			streaming: true,
			chunks: []string{
				"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":11}}}\n",
				"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":2}}\n",
				"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3}}\n",
			},
			want: &contract.Usage{InputTokens: 11, OutputTokens: 5, TotalTokens: 16},
		},
		{
			name:      "anthropic stream normalizes cache into input",
			protocol:  contract.ProtocolAnthropicMessages,
			streaming: true,
			chunks: []string{
				"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":50,\"cache_read_input_tokens\":100,\"cache_creation_input_tokens\":20}}}\n",
				"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":7}}\n",
			},
			want: &contract.Usage{
				InputTokens: 170, OutputTokens: 7, TotalTokens: 177,
				CacheReadTokens:  intPtr(100),
				CacheWriteTokens: intPtr(20),
			},
		},
		{
			name:     "anthropic non-stream normalizes cache into input",
			protocol: contract.ProtocolAnthropicMessages,
			chunks: []string{
				`{"id":"m","type":"message","usage":{"input_tokens":5,"output_tokens":2,"cache_read_input_tokens":1,"cache_creation_input_tokens":1}}`,
			},
			want: &contract.Usage{
				InputTokens: 7, OutputTokens: 2, TotalTokens: 9,
				CacheReadTokens:  intPtr(1),
				CacheWriteTokens: intPtr(1),
			},
		},
		{
			name:      "gemini last usageMetadata wins",
			protocol:  contract.ProtocolGoogleGenerateContent,
			streaming: true,
			chunks: []string{
				"data: {\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1,\"totalTokenCount\":2}}\n",
				"data: {\"usageMetadata\":{\"promptTokenCount\":9,\"candidatesTokenCount\":7,\"totalTokenCount\":16}}\n",
			},
			want: &contract.Usage{InputTokens: 9, OutputTokens: 7, TotalTokens: 16},
		},
		{
			name:     "gemini cachedContentTokenCount",
			protocol: contract.ProtocolGoogleGenerateContent,
			chunks: []string{
				`{"usageMetadata":{"promptTokenCount":30,"candidatesTokenCount":4,"totalTokenCount":34,"cachedContentTokenCount":12}}`,
			},
			want: &contract.Usage{
				InputTokens: 30, OutputTokens: 4, TotalTokens: 34,
				CacheReadTokens: intPtr(12),
			},
		},
		{
			name:     "absent usage yields null",
			protocol: contract.ProtocolOpenAIChat,
			chunks:   []string{`{"id":"c","choices":[]}`},
			want:     nil,
		},
		{
			name:      "sse split across chunks mid-token",
			protocol:  contract.ProtocolOpenAIChat,
			streaming: true,
			chunks: []string{
				"data: {\"usage\":{\"prompt_tokens\":2,\"comple",
				"tion_tokens\":3,\"total_tok",
				"ens\":5}}\n",
			},
			want: &contract.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
		},
	}
	t.Run("extracts responses output id", func(t *testing.T) {
		scanner := newUsageScanner(contract.ProtocolOpenAIResponses, false)
		recorder := httptest.NewRecorder()
		writer := scanner.wrap(recorder)
		if _, err := writer.Write([]byte(`{"id":"resp_out","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)); err != nil {
			t.Fatal(err)
		}
		_ = scanner.Usage()
		if scanner.OutputID() != "resp_out" {
			t.Fatalf("output id = %q", scanner.OutputID())
		}
	})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scanner := newUsageScanner(test.protocol, test.streaming)
			recorder := httptest.NewRecorder()
			writer := scanner.wrap(recorder)
			for _, chunk := range test.chunks {
				if _, err := writer.Write([]byte(chunk)); err != nil {
					t.Fatal(err)
				}
			}
			got := scanner.Usage()
			if test.want == nil {
				if got != nil {
					t.Fatalf("usage = %#v, want nil", got)
				}
				return
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("usage = %#v, want %#v", got, test.want)
			}
			if recorder.Body.String() != strings.Join(test.chunks, "") {
				t.Fatalf("scanner altered response bytes")
			}
		})
	}
}

func TestNormalizeAnthropicUsage(t *testing.T) {
	got := normalizeAnthropicUsage(intPtr(50), intPtr(7), intPtr(100), intPtr(20))
	want := &contract.Usage{
		InputTokens: 170, OutputTokens: 7, TotalTokens: 177,
		CacheReadTokens:  intPtr(100),
		CacheWriteTokens: intPtr(20),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalize = %#v, want %#v", got, want)
	}
	if got.CacheReadTokens == nil || *got.CacheReadTokens > got.InputTokens {
		t.Fatal("cache_read must be a subset of normalized input")
	}
}

func TestUsageScannerOverflowDisablesCapture(t *testing.T) {
	scanner := newUsageScanner(contract.ProtocolOpenAIResponses, false)
	writer := scanner.wrap(httptest.NewRecorder())
	writer.WriteHeader(http.StatusOK)
	huge := strings.Repeat("x", maxMetadataBytes+1)
	_, _ = writer.Write([]byte(huge))
	if scanner.Usage() != nil {
		t.Fatal("overflow should disable usage")
	}
}

func TestUsageScannerGzipNonStreaming(t *testing.T) {
	const plain = `{"id":"r","usage":{"input_tokens":3,"output_tokens":5,"total_tokens":8}}`
	compressed := gzipBytes(t, []byte(plain))

	t.Run("parses usage from gzip body", func(t *testing.T) {
		scanner := newUsageScanner(contract.ProtocolOpenAIResponses, false)
		recorder := httptest.NewRecorder()
		writer := scanner.wrap(recorder)
		writer.Header().Set("Content-Encoding", "gzip")
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		if _, err := writer.Write(compressed); err != nil {
			t.Fatal(err)
		}
		got := scanner.Usage()
		want := &contract.Usage{InputTokens: 3, OutputTokens: 5, TotalTokens: 8}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("usage = %#v, want %#v", got, want)
		}
		if !bytes.Equal(recorder.Body.Bytes(), compressed) {
			t.Fatal("scanner altered gzip response bytes")
		}
	})

	t.Run("over-cap decompressed payload yields null", func(t *testing.T) {
		// Highly compressible payload expands past the metadata cap.
		hugePlain := `{"pad":"` + strings.Repeat("a", maxMetadataBytes+1) + `","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
		scanner := newUsageScanner(contract.ProtocolOpenAIResponses, false)
		writer := scanner.wrap(httptest.NewRecorder())
		writer.Header().Set("Content-Encoding", "gzip")
		writer.WriteHeader(http.StatusOK)
		if _, err := writer.Write(gzipBytes(t, []byte(hugePlain))); err != nil {
			t.Fatal(err)
		}
		if scanner.Usage() != nil {
			t.Fatal("over-cap decompressed body should yield null usage")
		}
	})

	t.Run("unknown encoding yields null", func(t *testing.T) {
		scanner := newUsageScanner(contract.ProtocolOpenAIResponses, false)
		writer := scanner.wrap(httptest.NewRecorder())
		writer.Header().Set("Content-Encoding", "br")
		writer.WriteHeader(http.StatusOK)
		if _, err := writer.Write([]byte(plain)); err != nil {
			t.Fatal(err)
		}
		if scanner.Usage() != nil {
			t.Fatal("unknown content-encoding should yield null usage")
		}
	})
}

func gzipBytes(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
