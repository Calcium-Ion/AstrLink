package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

func TestSessionChannelBindingControlAuditAndRelease(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler, err := NewWithDependencies(contract.VersionResponse{CoreVersion: "test", ControlAPIVersion: "v1", ProtocolContractVersion: "v1"}, Dependencies{ServiceStore: store, RequestRecords: store, ControlToken: "control-token-123456"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	id := contract.SessionID("session_one")
	record := contract.RequestRecord{ID: "request_one", SessionID: &id, StartedAt: now, CompletedAt: &now, Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIChat, Audit: contract.NotCapturedAuditSummary()}
	if err := store.InsertRequestRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	scope := contract.ChannelBindingScope{SessionID: id, Principal: "token_one", Protocol: contract.ProtocolOpenAIChat, Model: "public"}
	if err := store.RememberChannelBinding(ctx, contract.ChannelBinding{ChannelBindingScope: scope, ServiceID: "service_a", Source: "explicit", RequestID: record.ID, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)}, now); err != nil {
		t.Fatal(err)
	}
	path := RequestSessionsPath + "/session_one/channel-bindings"
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodDelete, path, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized release %d", unauthorized.Code)
	}
	response := requestRecordHTTP(t, handler, http.MethodGet, path, "", "")
	var audit contract.ChannelBindingAudit
	decode(t, response, &audit)
	if response.Code != 200 || len(audit.Bindings) != 1 {
		t.Fatalf("get audit %d %+v", response.Code, audit)
	}
	response = requestRecordHTTP(t, handler, http.MethodDelete, path, "", "")
	decode(t, response, &audit)
	if response.Code != 200 || len(audit.Bindings) != 0 || audit.Events[0].Action != "released" || len(audit.Events) != 2 {
		t.Fatalf("release %d %+v", response.Code, audit)
	}
	if _, err := store.GetRequestRecord(ctx, record.ID); err != nil {
		t.Fatal("release removed request history")
	}
	for _, body := range []string{`{"channel_stickiness":{"enabled":true,"ttl_seconds":3600}}`, `{"channel_stickiness":{"enabled":false,"ttl_seconds":60}}`} {
		response = requestRecordHTTP(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", body)
		if response.Code != 200 {
			t.Fatalf("settings %d %s", response.Code, response.Body.String())
		}
	}
	for _, body := range []string{`{"channel_stickiness":null}`, `{"channel_stickiness":{"enabled":true}}`, `{"channel_stickiness":{"enabled":true,"ttl_seconds":0}}`, `{"channel_stickiness":{"enabled":true,"ttl_seconds":3600,"extra":true}}`} {
		response = requestRecordHTTP(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", body)
		if response.Code != 422 {
			t.Fatalf("invalid accepted %s: %d", body, response.Code)
		}
	}
}
