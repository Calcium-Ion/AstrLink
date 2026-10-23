package controlapi

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestRequestActivityControlAPI(t *testing.T) {
	store, err := openControlTestStore(t, context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
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
	started := time.Now().UTC().Add(-30 * time.Second)
	session := contract.SessionID("session_activity")
	if err := store.InsertRequestRecord(context.Background(), contract.RequestRecord{
		ID: "request_activity", SessionID: &session, StartedAt: started,
		Status: contract.RequestStatusPending, InputProtocol: contract.ProtocolOpenAIResponses,
		Audit: contract.NotCapturedAuditSummary(),
	}); err != nil {
		t.Fatal(err)
	}

	since := url.QueryEscape(time.Now().UTC().Add(-6 * time.Minute).Format(time.RFC3339Nano))
	response := requestRecordHTTP(t, handler, http.MethodGet, RequestActivityPath+"?since="+since, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("activity status=%d body=%s", response.Code, response.Body.String())
	}
	var activity contract.RequestActivity
	decode(t, response, &activity)
	if activity.Now.IsZero() || activity.Now.Before(started) {
		t.Fatalf("now = %v", activity.Now)
	}
	if len(activity.Calls) != 1 || activity.Calls[0].SessionID != session ||
		activity.Calls[0].CompletedAt != nil || activity.Calls[0].Status != contract.RequestStatusPending {
		t.Fatalf("calls = %+v", activity.Calls)
	}

	old := url.QueryEscape(time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano))
	for _, path := range []string{
		RequestActivityPath,
		RequestActivityPath + "?since=yesterday",
		RequestActivityPath + "?since=" + old,
		RequestActivityPath + "?since=" + since + "&since=" + since,
		RequestActivityPath + "?since=" + since + "&limit=5",
	} {
		response := requestRecordHTTP(t, handler, http.MethodGet, path, "", "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	response = requestRecordHTTP(t, handler, http.MethodPost, RequestActivityPath+"?since="+since, "", "")
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post status=%d", response.Code)
	}
}
