package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

func TestAuditSettingsGETDefaultsAndPATCHAckMatrix(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler, err := NewWithDependencies(contract.VersionResponse{
		CoreVersion: "0.0.0-test", ControlAPIVersion: "v1", ProtocolContractVersion: "v1",
	}, Dependencies{
		EndpointStore:  store,
		RequestRecords: store,
		AuditSettings:  store,
		AuditKeys:      store,
		AuditBlobs:     store,
		ControlToken:   "control-token-123456",
	})
	if err != nil {
		t.Fatal(err)
	}

	response := auditSettingsHTTP(t, handler, http.MethodGet, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", response.Code, response.Body.String())
	}
	var settings contract.AuditSettings
	decode(t, response, &settings)
	if settings.RequestBodyEnabled || settings.ResponseContentEnabled {
		t.Fatalf("defaults enabled: %#v", settings)
	}
	if strings.Contains(response.Body.String(), "audit_risk_acknowledged") {
		t.Fatal("ack must never be echoed")
	}

	response = auditSettingsHTTP(
		t, handler, http.MethodPatch, "application/merge-patch+json",
		`{"request_body_enabled":true}`,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("enable without ack status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope errorEnvelope
	decode(t, response, &envelope)
	if envelope.Error.Code != "validation_failed" || len(envelope.Error.Details) == 0 ||
		envelope.Error.Details[0].Field != "audit_risk_acknowledged" {
		t.Fatalf("envelope=%#v", envelope)
	}

	response = auditSettingsHTTP(
		t, handler, http.MethodPatch, "application/merge-patch+json",
		`{"request_body_enabled":true,"response_content_enabled":true,"audit_risk_acknowledged":true}`,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("enable with ack status=%d body=%s", response.Code, response.Body.String())
	}
	decode(t, response, &settings)
	if !settings.RequestBodyEnabled || !settings.ResponseContentEnabled {
		t.Fatalf("settings=%#v", settings)
	}
	if strings.Contains(response.Body.String(), "audit_risk_acknowledged") {
		t.Fatal("ack must never be echoed after enable")
	}
	key, err := store.GetAuditKey(context.Background())
	if err != nil || len(key) != storage.AuditKeyBytes {
		t.Fatalf("key should exist after enable: %v", err)
	}

	response = auditSettingsHTTP(
		t, handler, http.MethodPatch, "application/merge-patch+json",
		`{"request_body_enabled":false,"response_content_enabled":false}`,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("disable without ack status=%d body=%s", response.Code, response.Body.String())
	}
	decode(t, response, &settings)
	if settings.RequestBodyEnabled || settings.ResponseContentEnabled {
		t.Fatalf("disable failed: %#v", settings)
	}

	response = auditSettingsHTTP(
		t, handler, http.MethodPatch, "application/merge-patch+json",
		`{"request_body_max_bytes":2048}`,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("range change status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAuditContentRoundTripAndLeakBoundary(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler, err := NewWithDependencies(contract.VersionResponse{
		CoreVersion: "0.0.0-test", ControlAPIVersion: "v1", ProtocolContractVersion: "v1",
	}, Dependencies{
		EndpointStore:  store,
		RequestRecords: store,
		AuditSettings:  store,
		AuditKeys:      store,
		AuditBlobs:     store,
		ControlToken:   "control-token-123456",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key, err := store.GetOrCreateAuditKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const requestBody = `{"model":"m","input":"secret-request"}`
	const responseBody = `{"id":"r","output":"secret-response"}`
	start := time.Now().UTC()
	record := contract.RequestRecord{
		ID: "request_audit", StartedAt: start, Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses,
		Audit: contract.AuditRecordSummary{
			RequestBodyCaptured: true, ResponseContentCaptured: true,
		},
	}
	if err := store.InsertRequestRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		direction storage.AuditDirection
		plain     string
		media     string
	}{
		{storage.AuditDirectionRequest, requestBody, "application/json"},
		{storage.AuditDirectionResponse, responseBody, "application/json"},
	} {
		nonce, ciphertext, err := storage.SealAuditBlob(key, []byte(item.plain))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.InsertAuditBlob(ctx, storage.AuditBlob{
			RequestID: "request_audit", Direction: item.direction, MediaType: item.media,
			Nonce: nonce, Ciphertext: ciphertext, CapturedBytes: len(item.plain),
		}); err != nil {
			t.Fatal(err)
		}
	}

	response := requestRecordHTTP(t, handler, http.MethodGet, RequestsPath+"/request_audit/audit", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("audit get status=%d body=%s", response.Code, response.Body.String())
	}
	var content contract.AuditContent
	decode(t, response, &content)
	if content.RequestBody == nil || content.RequestBody.Content != requestBody {
		t.Fatalf("request part=%#v", content.RequestBody)
	}
	if content.ResponseContent == nil || content.ResponseContent.Content != responseBody {
		t.Fatalf("response part=%#v", content.ResponseContent)
	}

	list := requestRecordHTTP(t, handler, http.MethodGet, RequestsPath, "", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d", list.Code)
	}
	if strings.Contains(list.Body.String(), "secret-request") ||
		strings.Contains(list.Body.String(), "secret-response") ||
		strings.Contains(list.Body.String(), string(key)) {
		t.Fatalf("list leaked content/key: %s", list.Body.String())
	}
	get := requestRecordHTTP(t, handler, http.MethodGet, RequestsPath+"/request_audit", "", "")
	if get.Code != http.StatusOK {
		t.Fatalf("get status=%d", get.Code)
	}
	if strings.Contains(get.Body.String(), "secret-request") ||
		strings.Contains(get.Body.String(), "secret-response") {
		t.Fatalf("get leaked content: %s", get.Body.String())
	}

	missing := requestRecordHTTP(t, handler, http.MethodGet, RequestsPath+"/request_missing/audit", "", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing audit status=%d", missing.Code)
	}

	emptyID := "request_empty"
	if err := store.InsertRequestRecord(ctx, contract.RequestRecord{
		ID: contract.RequestID(emptyID), StartedAt: start, Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIChat, Audit: contract.NotCapturedAuditSummary(),
	}); err != nil {
		t.Fatal(err)
	}
	empty := requestRecordHTTP(t, handler, http.MethodGet, RequestsPath+"/"+emptyID+"/audit", "", "")
	if empty.Code != http.StatusOK {
		t.Fatalf("empty audit status=%d", empty.Code)
	}
	var emptyContent contract.AuditContent
	decode(t, empty, &emptyContent)
	if emptyContent.RequestBody != nil || emptyContent.ResponseContent != nil {
		t.Fatalf("expected null parts: %#v", emptyContent)
	}
}

func TestAuditContentConflictWhenKeyMissing(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler, err := NewWithDependencies(contract.VersionResponse{
		CoreVersion: "0.0.0-test", ControlAPIVersion: "v1", ProtocolContractVersion: "v1",
	}, Dependencies{
		EndpointStore:  store,
		RequestRecords: store,
		AuditSettings:  store,
		AuditKeys:      store,
		AuditBlobs:     store,
		ControlToken:   "control-token-123456",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := bytesRepeat(32, 7)
	nonce, ciphertext, err := storage.SealAuditBlob(key, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	record := contract.RequestRecord{
		ID: "request_orphan_blob", StartedAt: time.Now().UTC(),
		Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses,
		Audit: contract.AuditRecordSummary{RequestBodyCaptured: true},
	}
	if err := store.InsertRequestRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertAuditBlob(ctx, storage.AuditBlob{
		RequestID: "request_orphan_blob", Direction: storage.AuditDirectionRequest,
		MediaType: "application/json", Nonce: nonce, Ciphertext: ciphertext, CapturedBytes: 2,
	}); err != nil {
		t.Fatal(err)
	}
	response := requestRecordHTTP(t, handler, http.MethodGet, RequestsPath+"/request_orphan_blob/audit", "", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func auditSettingsHTTP(t *testing.T, handler *Handler, method, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, AuditSettingsPath, nil)
	} else {
		request = httptest.NewRequest(method, AuditSettingsPath, strings.NewReader(body))
		request.Header.Set("Content-Type", contentType)
	}
	request.Header.Set("Authorization", "Bearer control-token-123456")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func bytesRepeat(n, value byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = value
	}
	return out
}
