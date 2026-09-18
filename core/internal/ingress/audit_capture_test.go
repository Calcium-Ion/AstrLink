package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

var requestScopedPlaceholderPattern = regexp.MustCompile(
	`<PRIVATE_(?:EMAIL|PHONE)_[0-9a-f]{16}>`,
)

type memoryAuditSettings struct {
	settings contract.AuditSettings
}

func (store *memoryAuditSettings) GetAuditSettings(context.Context) (contract.AuditSettings, error) {
	return store.settings, nil
}

type memoryAuditBlobs struct {
	key     []byte
	blobs   []storage.AuditBlob
	fail    bool
	records *memoryRequestRecordStore
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
	if store.records != nil {
		found := false
		for _, record := range store.records.records {
			if record.ID == blob.RequestID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("upsert audit blob: constraint failed: FOREIGN KEY constraint failed (787)")
		}
	}
	for index := range store.blobs {
		if store.blobs[index].RequestID == blob.RequestID &&
			store.blobs[index].Direction == blob.Direction {
			store.blobs[index] = blob
			return nil
		}
	}
	store.blobs = append(store.blobs, blob)
	return nil
}

func TestIngressAuditPersistsClientRequestWhilePending(t *testing.T) {
	const requestBody = `{"model":"m","input":"hello"}`
	const responseBody = `{"id":"r","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	records := &memoryRequestRecordStore{}
	blobs := &memoryAuditBlobs{records: records}
	settings := &memoryAuditSettings{settings: contract.AuditSettings{
		RequestBodyEnabled: true, ResponseContentEnabled: true,
		RequestBodyMaxBytes: 1024, ResponseContentMaxBytes: 1024,
		MetadataRetentionDays: 30, ContentRetentionDays: 7,
	}}
	started := make(chan struct{})
	release := make(chan struct{})
	handler := NewWithDependencies(Dependencies{
		Resolver:       candidateResolver{candidates: []endpoint.Resolved{{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false)}}},
		RequestRecords: records,
		AuditSettings:  settings,
		AuditBlobs:     blobs,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			close(started)
			<-release
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, err := writer.Write([]byte(responseBody))
			return err
		}),
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(requestBody))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("forwarder did not start")
	}
	if len(records.records) != 1 || records.records[0].Status != contract.RequestStatusPending {
		t.Fatalf("pending records=%#v", records.records)
	}
	if !records.records[0].Audit.RequestBodyCaptured {
		t.Fatalf("pending audit=%#v", records.records[0].Audit)
	}
	if records.records[0].Audit.ResponseContentCaptured {
		t.Fatal("response should still be uncaptured while the call is in flight")
	}
	var requestBlob *storage.AuditBlob
	for index := range blobs.blobs {
		if blobs.blobs[index].Direction == storage.AuditDirectionRequest {
			requestBlob = &blobs.blobs[index]
			break
		}
	}
	if requestBlob == nil {
		t.Fatalf("missing request blob among %#v", blobs.blobs)
	}
	plain, err := storage.OpenAuditBlob(blobs.key, requestBlob.Nonce, requestBlob.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != requestBody {
		t.Fatalf("pending request plain=%q", plain)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not finish")
	}
	record := records.records[len(records.records)-1]
	if record.Status != contract.RequestStatusSucceeded || !record.Audit.RequestBodyCaptured ||
		!record.Audit.ResponseContentCaptured {
		t.Fatalf("terminal record=%#v", record)
	}
	var sawRequest, sawResponse bool
	for _, blob := range blobs.blobs {
		switch blob.Direction {
		case storage.AuditDirectionRequest:
			sawRequest = true
		case storage.AuditDirectionResponse:
			sawResponse = true
		}
	}
	if !sawRequest || !sawResponse {
		t.Fatalf("terminal blobs=%#v", blobs.blobs)
	}
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
	var sawRequest, sawResponse bool
	for _, blob := range blobs.blobs {
		plain, err := storage.OpenAuditBlob(blobs.key, blob.Nonce, blob.Ciphertext)
		if err != nil {
			t.Fatal(err)
		}
		switch blob.Direction {
		case storage.AuditDirectionRequest:
			sawRequest = true
			if string(plain) != requestBody {
				t.Fatalf("request plain=%q", plain)
			}
		case storage.AuditDirectionResponse:
			sawResponse = true
			if string(plain) != responseBody {
				t.Fatalf("response plain=%q", plain)
			}
		}
	}
	if !sawRequest || !sawResponse {
		t.Fatalf("missing client blobs among %#v", blobs.blobs)
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
	var responseBlob *storage.AuditBlob
	for index := range blobs.blobs {
		if blobs.blobs[index].Direction == storage.AuditDirectionResponse {
			responseBlob = &blobs.blobs[index]
			break
		}
	}
	if responseBlob == nil {
		t.Fatalf("missing client response blob among %#v", blobs.blobs)
	}
	plain, err := storage.OpenAuditBlob(blobs.key, responseBlob.Nonce, responseBlob.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, want) {
		t.Fatalf("stored stream != client bytes:\n got %q\nwant %q", plain, want)
	}
	if responseBlob.Truncated || responseBlob.CapturedBytes != len(want) {
		t.Fatalf("audit blob=%#v", responseBlob)
	}
}

func TestIngressAuditCapturesRestoredCrossEventPrivacyResponse(t *testing.T) {
	const input = "邮箱：alice@example.com\n" +
		"备用邮箱：alice@example.com\n" +
		"另一邮箱：bob@example.com\n" +
		"电话A：+1-415-555-0001\n" +
		"电话B：+1-415-555-0002"
	requestBody, err := json.Marshal(map[string]any{
		"model":  "gpt-5",
		"stream": true,
		"messages": []any{map[string]any{
			"role":    "user",
			"content": input,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled:         true,
		Mode:            privacy.ModeRegex,
		Action:          privacy.ActionRedact,
		ResponseRestore: true,
	}, nil)
	records := &memoryRequestRecordStore{}
	blobs := &memoryAuditBlobs{}
	settings := &memoryAuditSettings{settings: contract.AuditSettings{
		ResponseContentEnabled:  true,
		ResponseContentMaxBytes: 64 * 1024,
		RequestBodyMaxBytes:     1024,
		MetadataRetentionDays:   30,
		ContentRetentionDays:    7,
	}}
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint: validEndpoint(contract.ProtocolOpenAIChat, true),
		}}},
		PrivacyFilter:  filter,
		RequestRecords: records,
		AuditSettings:  settings,
		AuditBlobs:     blobs,
		Forwarder: forwarderFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
			_ transport.Target,
		) error {
			redacted, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				return readErr
			}
			matches := requestScopedPlaceholderPattern.FindAllString(string(redacted), -1)
			if len(matches) != 5 || matches[0] != matches[1] {
				t.Fatalf("outbound placeholders=%#v body=%s", matches, redacted)
			}
			fragments := []string{
				"邮箱=" + matches[0][:8],
				matches[0][8:] + "\n备用邮箱=" + matches[1][:1],
				matches[1][1:] + "\n另一邮箱=" + matches[2][:12],
				matches[2][12:] + "\n电话A=" + matches[3][:8],
				matches[3][8:] + "\n电话B=" + matches[4][:14],
				matches[4][14:],
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.WriteHeader(http.StatusOK)
			for _, fragment := range fragments {
				event, marshalErr := json.Marshal(map[string]any{
					"choices": []any{map[string]any{
						"index": 0,
						"delta": map[string]any{"content": fragment},
					}},
				})
				if marshalErr != nil {
					return marshalErr
				}
				wire := append([]byte("data: "), event...)
				wire = append(wire, '\n', '\n')
				if _, writeErr := writer.Write(wire); writeErr != nil {
					return writeErr
				}
			}
			_, writeErr := writer.Write([]byte("data: [DONE]\n\n"))
			return writeErr
		}),
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader(requestBody),
	)
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)

	want := "邮箱=alice@example.com\n" +
		"备用邮箱=alice@example.com\n" +
		"另一邮箱=bob@example.com\n" +
		"电话A=+1-415-555-0001\n" +
		"电话B=+1-415-555-0002"
	if response.Code != http.StatusOK ||
		visibleSSEText(t, contract.ProtocolOpenAIChat, response.Body.Bytes()) != want ||
		strings.Contains(response.Body.String(), "<PRIVATE_") {
		t.Fatalf("client response=%d %s", response.Code, response.Body.String())
	}
	if len(records.records) != 1 || records.records[0].PrivacyRestore == nil {
		t.Fatalf("record=%#v", records.records)
	}
	restore := records.records[0].PrivacyRestore
	if !restore.Enabled ||
		restore.MappingCount != 4 ||
		restore.RestoredCount != 5 ||
		restore.FallbackCount != 0 {
		t.Fatalf("privacy restore=%#v", restore)
	}
	if len(restore.Hits) != 2 ||
		restore.Hits[0] != (contract.PrivacyHitCount{Kind: contract.CanonicalKindEmail, Count: 2}) ||
		restore.Hits[1] != (contract.PrivacyHitCount{Kind: contract.CanonicalKindPhone, Count: 2}) {
		t.Fatalf("privacy hits=%#v", restore.Hits)
	}
	if len(blobs.blobs) != 1 ||
		blobs.blobs[0].Direction != storage.AuditDirectionResponse {
		t.Fatalf("blobs=%#v", blobs.blobs)
	}
	plain, err := storage.OpenAuditBlob(
		blobs.key,
		blobs.blobs[0].Nonce,
		blobs.blobs[0].Ciphertext,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, response.Body.Bytes()) ||
		strings.Contains(string(plain), "<PRIVATE_") {
		t.Fatalf("captured response=%s", plain)
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
	var blob *storage.AuditBlob
	for index := range blobs.blobs {
		if blobs.blobs[index].Direction == storage.AuditDirectionHTTPMeta {
			blob = &blobs.blobs[index]
			break
		}
	}
	if blob == nil {
		t.Fatalf("missing client http_meta among %#v", blobs.blobs)
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
	for _, candidate := range blobs.blobs {
		plain, err := storage.OpenAuditBlob(blobs.key, candidate.Nonce, candidate.Ciphertext)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(plain, []byte(credential)) || bytes.Contains(plain, []byte("upstream.example")) {
			t.Fatalf("%s blob leaks sensitive material: %s", candidate.Direction, plain)
		}
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
