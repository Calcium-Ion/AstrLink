package ingress

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{Endpoint: upstream}}},
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
	chunks := []string{
		"event: response.created\n",
		"data: {\"type\":\"response.created\"}\n\n",
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n",
	}
	want := strings.Join(chunks, "")
	records := &memoryRequestRecordStore{}
	blobs := &memoryAuditBlobs{}
	settings := &memoryAuditSettings{settings: contract.AuditSettings{
		ResponseContentEnabled: true, ResponseContentMaxBytes: 4096,
		RequestBodyMaxBytes: 1024, MetadataRetentionDays: 30, ContentRetentionDays: 7,
	}}
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, true),
		}}},
		RequestRecords: records,
		AuditSettings:  settings,
		AuditBlobs:     blobs,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, _ *http.Request, _ transport.Target) error {
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.WriteHeader(http.StatusOK)
			for _, chunk := range chunks {
				if _, err := writer.Write([]byte(chunk)); err != nil {
					return err
				}
			}
			return nil
		}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"m","stream":true}`)),
	)
	if response.Body.String() != want {
		t.Fatalf("client bytes altered:\n got %q\nwant %q", response.Body.String(), want)
	}
	if len(blobs.blobs) != 1 || blobs.blobs[0].Direction != storage.AuditDirectionResponse {
		t.Fatalf("blobs=%#v", blobs.blobs)
	}
	plain, err := storage.OpenAuditBlob(blobs.key, blobs.blobs[0].Nonce, blobs.blobs[0].Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != want {
		t.Fatalf("stored stream != client bytes:\n got %q\nwant %q", plain, want)
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
		settings := &memoryAuditSettings{settings: contract.DefaultAuditSettings()}
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
