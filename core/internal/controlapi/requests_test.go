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
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

func TestRequestRecordControlAPI(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler, err := NewWithDependencies(contract.VersionResponse{
		CoreVersion: "0.0.0-test", ControlAPIVersion: "v1", ProtocolContractVersion: "v1",
	}, Dependencies{
		ServiceStore:   store,
		RequestRecords: store,
		ControlToken:   "control-token-123456",
	})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	completed := start.Add(time.Second)
	model := "public-alias"
	status := 200
	latency := 20
	record := contract.RequestRecord{
		ID: "request_ctrl", StartedAt: start, CompletedAt: &completed,
		Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses,
		RequestedModel: &model, HTTPStatus: &status, LatencyMs: &latency,
		Audit: contract.NotCapturedAuditSummary(),
		Usage: &contract.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
		PrivacyRestore: &contract.PrivacyRestoreSummary{
			Enabled: true, MappingCount: 1, RestoredCount: 2,
		},
	}
	if err := store.InsertRequestRecord(context.Background(), record); err != nil {
		t.Fatal(err)
	}

	response := requestRecordHTTP(t, handler, http.MethodGet, RequestsPath+"?status=succeeded&limit=10", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	var page requestRecordPageResponse
	decode(t, response, &page)
	if len(page.Items) != 1 || page.Items[0].ID != "request_ctrl" {
		t.Fatalf("page=%#v", page)
	}

	response = requestRecordHTTP(t, handler, http.MethodGet, RequestsPath+"/request_ctrl", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", response.Code, response.Body.String())
	}
	var loaded contract.RequestRecord
	decode(t, response, &loaded)
	if loaded.RequestedModel == nil || *loaded.RequestedModel != "public-alias" {
		t.Fatalf("loaded=%#v", loaded)
	}
	if loaded.PrivacyRestore == nil ||
		loaded.PrivacyRestore.MappingCount != 1 ||
		loaded.PrivacyRestore.RestoredCount != 2 {
		t.Fatalf("privacy restore=%#v", loaded.PrivacyRestore)
	}

	response = requestRecordHTTP(
		t, handler, http.MethodPost, RequestsPurgePath,
		"application/json", `{"scope":"all","confirm":true}`,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("purge status=%d body=%s", response.Code, response.Body.String())
	}
	var purge contract.PurgeResult
	decode(t, response, &purge)
	if purge.DeletedRecords != 1 || purge.DeletedAuditBlobs != 0 {
		t.Fatalf("purge=%#v", purge)
	}

	response = requestRecordHTTP(t, handler, http.MethodDelete, RequestsPath+"/request_ctrl", "", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("delete missing status=%d", response.Code)
	}
}

func TestRequestSessionControlAPI(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler, err := NewWithDependencies(contract.VersionResponse{
		CoreVersion: "0.0.0-test", ControlAPIVersion: "v1", ProtocolContractVersion: "v1",
	}, Dependencies{
		ServiceStore:   store,
		RequestRecords: store,
		ControlToken:   "control-token-123456",
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	sessionID := contract.SessionID("session_ctrl")
	preview := "会话标题"
	record := contract.RequestRecord{
		ID: "request_sess", StartedAt: start, Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
		SessionID: &sessionID, InputPreview: &preview,
	}
	if err := store.InsertRequestRecord(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	response := requestRecordHTTP(t, handler, http.MethodGet, RequestSessionsPath, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list sessions status=%d body=%s", response.Code, response.Body.String())
	}
	var page requestSessionPageResponse
	decode(t, response, &page)
	if len(page.Items) != 1 || page.Items[0].ID != sessionID || page.Items[0].Title != preview {
		t.Fatalf("page=%#v", page)
	}
	response = requestRecordHTTP(t, handler, http.MethodGet, RequestSessionsPath+"/session_ctrl", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("get session status=%d body=%s", response.Code, response.Body.String())
	}
	var detail contract.RequestSessionDetail
	decode(t, response, &detail)
	if len(detail.Turns) != 1 || detail.Turns[0].ID != "request_sess" {
		t.Fatalf("detail=%#v", detail)
	}
	for _, protocol := range []contract.ProtocolID{contract.ProtocolOpenAIModels, contract.ProtocolGoogleModels} {
		discovery := record
		discovery.ID = contract.RequestID("request_discovery_" + strings.Split(string(protocol), ".")[0])
		discovery.SessionID = nil
		discovery.InputProtocol = protocol
		if err := store.InsertRequestRecord(context.Background(), discovery); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		kind  string
		count int
	}{{"inference", 1}, {"discovery", 2}, {"", 3}} {
		response := requestRecordHTTP(t, handler, http.MethodGet, RequestSessionsPath+"?kind="+test.kind, "", "")
		if response.Code != http.StatusOK {
			t.Fatalf("kind=%s status=%d body=%s", test.kind, response.Code, response.Body.String())
		}
		var page requestSessionPageResponse
		decode(t, response, &page)
		if len(page.Items) != test.count {
			t.Fatalf("kind=%s page=%#v", test.kind, page)
		}
	}
	response = requestRecordHTTP(t, handler, http.MethodGet, RequestSessionsPath+"?kind=invalid", "", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid kind status=%d", response.Code)
	}
}

func requestRecordHTTP(
	t *testing.T,
	handler *Handler,
	method, path, contentType, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, path, nil)
	} else {
		request = httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", contentType)
	}
	request.Header.Set("Authorization", "Bearer control-token-123456")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
