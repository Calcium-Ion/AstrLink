package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

type memoryAuditSettings struct {
	settings contract.AuditSettings
}

func (store *memoryAuditSettings) GetAuditSettings(context.Context) (contract.AuditSettings, error) {
	return store.settings, nil
}

type memoryAuditBlobs struct {
	key   []byte
	blobs []storage.AuditBlob
	fail  bool
}

func (store *memoryAuditBlobs) GetOrCreateAuditKey(context.Context) ([]byte, error) {
	if store.key == nil {
		store.key = bytes.Repeat([]byte{9}, storage.AuditKeyBytes)
	}
	return append([]byte(nil), store.key...), nil
}

func (store *memoryAuditBlobs) InsertAuditBlob(_ context.Context, blob storage.AuditBlob) error {
	if store.fail {
		return io.ErrUnexpectedEOF
	}
	store.blobs = append(store.blobs, blob)
	return nil
}

func TestIngressAuditCaptureNonStreamingRoundTrip(t *testing.T) {
	const requestBody = `{"model":"m","input":"hello"}`
	const responseBody = `{"id":"r","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	records := &memoryRequestRecordStore{}
	blobs := &memoryAuditBlobs{}
	settings := &memoryAuditSettings{settings: contract.AuditSettings{
		RequestBodyEnabled: true, ResponseContentEnabled: true,
		RequestBodyMaxBytes: 1024, ResponseContentMaxBytes: 1024,
		MetadataRetentionDays: 30, ContentRetentionDays: 7,
	}}
	upstream := validEndpoint(contract.ProtocolOpenAIResponses, false)
	handler := NewWithDependencies(Dependencies{
		Resolver:       candidateResolver{candidates: []endpoint.Resolved{{Endpoint: upstream}}},
		RequestRecords: records,
		AuditSettings:  settings,
		AuditBlobs:     blobs,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != requestBody {
				t.Fatalf("upstream body=%q", body)
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, err = writer.Write([]byte(responseBody))
			return err
		}),
	})
	response := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK || response.Body.String() != responseBody {
		t.Fatalf("client response altered: %d %q", response.Code, response.Body.String())
	}
	if len(records.records) != 1 || !records.records[0].Audit.RequestBodyCaptured ||
		!records.records[0].Audit.ResponseContentCaptured {
		t.Fatalf("record=%#v", records.records)
	}
	if len(blobs.blobs) != 2 {
		t.Fatalf("blobs=%d", len(blobs.blobs))
	}
	for _, blob := range blobs.blobs {
		plain, err := storage.OpenAuditBlob(blobs.key, blob.Nonce, blob.Ciphertext)
		if err != nil {
			t.Fatal(err)
		}
		switch blob.Direction {
		case storage.AuditDirectionRequest:
			if string(plain) != requestBody {
				t.Fatalf("request plain=%q", plain)
			}
		case storage.AuditDirectionResponse:
			if string(plain) != responseBody {
				t.Fatalf("response plain=%q", plain)
			}
		}
	}
}

func TestIngressAuditCaptureSSEByteFidelity(t *testing.T) {
	want, err := os.ReadFile("testdata/responses-anomalous-complete.sse")
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" {
			t.Errorf("upstream path=%q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		for start := 0; start < len(want); {
			end := min(start+17, len(want))
			if _, writeErr := writer.Write(want[start:end]); writeErr != nil {
				return
			}
			writer.(http.Flusher).Flush()
			start = end
		}
	}))
	defer upstream.Close()

	records := &memoryRequestRecordStore{}
	blobs := &memoryAuditBlobs{}
	settings := &memoryAuditSettings{settings: contract.AuditSettings{
		ResponseContentEnabled: true, ResponseContentMaxBytes: 64 * 1024,
		RequestBodyMaxBytes: 1024, MetadataRetentionDays: 30, ContentRetentionDays: 7,
	}}
	resolved := validEndpoint(contract.ProtocolOpenAIResponses, true)
	resolved.BaseURL = upstream.URL
	resolved.Capabilities[0].Mode = contract.CapabilityModeDelegated
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint: resolved,
			Mode:     contract.CapabilityModeDelegated,
		}}},
		RequestRecords: records,
		AuditSettings:  settings,
		AuditBlobs:     blobs,
		Forwarder:      transport.New(http.DefaultTransport),
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"m","stream":true,"input":"draw a cat"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), want) {
		t.Fatalf("client bytes altered:\n got %q\nwant %q", response.Body.Bytes(), want)
	}
	if len(records.records) != 1 ||
		records.records[0].Status != contract.RequestStatusSucceeded ||
		!records.records[0].Audit.ResponseContentCaptured ||
		records.records[0].Audit.ResponseContentTruncated {
		t.Fatalf("record=%#v", records.records)
	}
	if records.records[0].Plan == nil ||
		records.records[0].Plan.Type != contract.PlanTypeDelegated {
		t.Fatalf("plan=%#v", records.records[0].Plan)
	}
	if records.records[0].Usage == nil ||
		records.records[0].Usage.InputTokens != 7 ||
		records.records[0].Usage.OutputTokens != 18 ||
		records.records[0].Usage.TotalTokens != 25 {
		t.Fatalf("usage=%#v", records.records[0].Usage)
	}
	if len(blobs.blobs) != 1 || blobs.blobs[0].Direction != storage.AuditDirectionResponse {
		t.Fatalf("blobs=%#v", blobs.blobs)
	}
	plain, err := storage.OpenAuditBlob(blobs.key, blobs.blobs[0].Nonce, blobs.blobs[0].Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, want) {
		t.Fatalf("stored stream != client bytes:\n got %q\nwant %q", plain, want)
	}
	if blobs.blobs[0].Truncated || blobs.blobs[0].CapturedBytes != len(want) {
		t.Fatalf("audit blob=%#v", blobs.blobs[0])
	}
}

func TestIngressAuditCaptureTruncationAndOff(t *testing.T) {
	t.Run("truncation", func(t *testing.T) {
		records := &memoryRequestRecordStore{}
		blobs := &memoryAuditBlobs{}
		settings := &memoryAuditSettings{settings: contract.AuditSettings{
			RequestBodyEnabled: true, ResponseContentEnabled: true,
			RequestBodyMaxBytes: 1024, ResponseContentMaxBytes: 16,
			MetadataRetentionDays: 30, ContentRetentionDays: 7,
		}}
		handler := NewWithDependencies(Dependencies{
			Resolver: candidateResolver{candidates: []endpoint.Resolved{{
				Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false),
			}}},
			RequestRecords: records,
			AuditSettings:  settings,
			AuditBlobs:     blobs,
			Forwarder: forwarderFunc(func(writer http.ResponseWriter, _ *http.Request, _ transport.Target) error {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusOK)
				_, err := writer.Write([]byte(`{"pad":"0123456789abcdefEXTRA"}`))
				return err
			}),
		})
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"m","input":"hi"}`)),
		)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d", response.Code)
		}
		if len(records.records) != 1 || !records.records[0].Audit.ResponseContentTruncated {
			t.Fatalf("record=%#v", records.records)
		}
		if len(blobs.blobs) == 0 || blobs.blobs[len(blobs.blobs)-1].CapturedBytes != 16 {
			t.Fatalf("blobs=%#v", blobs.blobs)
		}
	})

	t.Run("capture off", func(t *testing.T) {
		records := &memoryRequestRecordStore{}
		blobs := &memoryAuditBlobs{}
		defaults := contract.DefaultAuditSettings()
		defaults.HTTPMetaEnabled = false
		settings := &memoryAuditSettings{settings: defaults}
		handler := NewWithDependencies(Dependencies{
			Resolver: candidateResolver{candidates: []endpoint.Resolved{{
				Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false),
			}}},
			RequestRecords: records,
			AuditSettings:  settings,
			AuditBlobs:     blobs,
			Forwarder: forwarderFunc(func(writer http.ResponseWriter, _ *http.Request, _ transport.Target) error {
				writer.WriteHeader(http.StatusOK)
				_, err := writer.Write([]byte(`{"ok":true}`))
				return err
			}),
		})
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"m"}`)),
		)
		if len(blobs.blobs) != 0 {
			t.Fatalf("expected no blobs, got %#v", blobs.blobs)
		}
		if len(records.records) != 1 || records.records[0].Audit.RequestBodyCaptured ||
			records.records[0].Audit.ResponseContentCaptured {
			t.Fatalf("record=%#v", records.records)
		}
	})
}

// Regression guard for prepareAuditKey: the http_meta blob must persist even
// when both body captures are disabled (the default configuration).
func TestIngressHTTPMetaCaptureWithBodyCaptureOff(t *testing.T) {
	records := &memoryRequestRecordStore{}
	blobs := &memoryAuditBlobs{}
	settings := &memoryAuditSettings{settings: contract.DefaultAuditSettings()}
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false),
		}}},
		RequestRecords: records,
		AuditSettings:  settings,
		AuditBlobs:     blobs,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, _ *http.Request, _ transport.Target) error {
			writer.Header().Set("Content-Type", "application/json")
			writer.Header().Set("X-Request-Id", "req_upstream_1")
			writer.WriteHeader(http.StatusOK)
			_, err := writer.Write([]byte(`{"ok":true}`))
			return err
		}),
	})
	const credential = "sk-inbound-secret-123"
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses?stream=false",
		strings.NewReader(`{"model":"m","input":"hi"}`),
	)
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	if len(blobs.blobs) != 1 {
		t.Fatalf("blobs=%d, want exactly the http_meta blob", len(blobs.blobs))
	}
	blob := blobs.blobs[0]
	if blob.Direction != storage.AuditDirectionHTTPMeta {
		t.Fatalf("direction=%q", blob.Direction)
	}
	if blob.MediaType != "application/json" {
		t.Fatalf("media_type=%q", blob.MediaType)
	}
	plaintext, err := storage.OpenAuditBlob(blobs.key, blob.Nonce, blob.Ciphertext)
	if err != nil {
		t.Fatalf("decrypt http_meta: %v", err)
	}
	if bytes.Contains(plaintext, []byte(credential)) {
		t.Fatalf("http_meta blob leaks inbound credential: %s", plaintext)
	}
	var meta contract.AuditHTTPMeta
	if err := json.Unmarshal(plaintext, &meta); err != nil {
		t.Fatalf("decode http_meta: %v", err)
	}
	if meta.Method != http.MethodPost || !strings.Contains(meta.URL, "/v1/responses") {
		t.Fatalf("meta=%+v", meta)
	}
	if meta.ResponseStatus == nil || *meta.ResponseStatus != http.StatusOK {
		t.Fatalf("response_status=%v", meta.ResponseStatus)
	}
	foundAuth := false
	for _, header := range meta.RequestHeaders {
		if header.Name == "authorization" {
			foundAuth = true
			if !header.Redacted || !strings.HasPrefix(header.Value, "Bearer <redacted:") {
				t.Fatalf("authorization not masked: %+v", header)
			}
		}
	}
	if !foundAuth {
		t.Fatal("authorization header missing from capture")
	}
	foundRequestID := false
	for _, header := range meta.ResponseHeaders {
		if header.Name == "x-request-id" {
			foundRequestID = true
			if header.Value != "req_upstream_1" || header.Redacted {
				t.Fatalf("x-request-id altered: %+v", header)
			}
		}
	}
	if !foundRequestID {
		t.Fatal("x-request-id missing from response headers")
	}
	// Body summary flags stay false: only the envelope was captured.
	record := records.records[len(records.records)-1]
	if record.Audit.RequestBodyCaptured || record.Audit.ResponseContentCaptured {
		t.Fatalf("audit summary=%+v", record.Audit)
	}
}

func TestIngressAuditCaptureFailureDoesNotAlterClientResponse(t *testing.T) {
	const responseBody = `{"ok":true}`
	records := &memoryRequestRecordStore{}
	blobs := &memoryAuditBlobs{fail: true}
	settings := &memoryAuditSettings{settings: contract.AuditSettings{
		ResponseContentEnabled: true, ResponseContentMaxBytes: 1024,
		RequestBodyMaxBytes: 1024, MetadataRetentionDays: 30, ContentRetentionDays: 7,
	}}
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false),
		}}},
		RequestRecords: records,
		AuditSettings:  settings,
		AuditBlobs:     blobs,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, _ *http.Request, _ transport.Target) error {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, err := writer.Write([]byte(responseBody))
			return err
		}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"m"}`)),
	)
	if response.Code != http.StatusOK || response.Body.String() != responseBody {
		t.Fatalf("client response altered: %d %q", response.Code, response.Body.String())
	}
}
