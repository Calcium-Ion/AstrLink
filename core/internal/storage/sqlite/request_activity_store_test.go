package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestListRequestActivityReadsCallsOverlappingTheWindow(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "activity.db"))
	defer store.Close()
	ctx := context.Background()
	since := time.Date(2026, 10, 23, 6, 0, 0, 0, time.UTC)
	session := contract.SessionID("session_live")
	call := func(id string, started time.Time, completed *time.Time) contract.RequestRecord {
		status := contract.RequestStatusSucceeded
		if completed == nil {
			status = contract.RequestStatusPending
		}
		return contract.RequestRecord{
			ID: contract.RequestID(id), SessionID: &session, StartedAt: started, CompletedAt: completed,
			Status: status, InputProtocol: contract.ProtocolAnthropicMessages, ClientType: contract.ClientType("claude_code"),
			Audit: contract.NotCapturedAuditSummary(),
		}
	}
	running := call("request_running", since.Add(-50*time.Minute), nil)
	endedInside := call("request_inside", since.Add(-time.Minute), ptrTime(since.Add(10*time.Second)))
	rejected := call("request_rejected", since.Add(time.Minute), ptrTime(since.Add(61*time.Second)))
	rejected.HTTPStatus = ptrInt(502)
	endedInside.ClientType = ""
	endedBefore := call("request_before", since.Add(-2*time.Minute), ptrTime(since.Add(-time.Second)))
	tooOld := call("request_too_old", since.Add(-2*time.Hour), nil)
	discovery := call("request_models", since.Add(time.Second), ptrTime(since.Add(2*time.Second)))
	discovery.SessionID = nil
	discovery.InputProtocol = contract.ProtocolOpenAIModels
	interrupted := call("request_interrupted", since.Add(-10*time.Minute), ptrTime(since.Add(time.Minute)))
	interrupted.Status = contract.RequestStatusFailed
	interrupted.Error = &contract.ErrorSummary{Category: "core", Code: "core_interrupted", Message: "core restarted"}
	retry := call("request_retry", since.Add(2*time.Minute), ptrTime(since.Add(3*time.Minute)))
	retry.ParentRequestID = &rejected.ID
	retry.SessionID = nil
	retry.AttemptIndex = 1
	for _, record := range []contract.RequestRecord{running, endedInside, rejected, endedBefore, tooOld, discovery, interrupted, retry} {
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}

	calls, err := store.ListRequestActivity(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	type span struct {
		started time.Time
		ended   bool
		status  contract.RequestStatus
		client  contract.ClientType
	}
	got := make([]span, 0, len(calls))
	for _, item := range calls {
		if item.SessionID != session {
			t.Fatalf("session = %q", item.SessionID)
		}
		got = append(got, span{item.StartedAt, item.CompletedAt != nil, item.Status, item.ClientType})
	}
	want := []span{
		{rejected.StartedAt, true, contract.RequestStatusFailed, "claude_code"},
		{endedInside.StartedAt, true, contract.RequestStatusSucceeded, ""},
		{running.StartedAt, false, contract.RequestStatusPending, "claude_code"},
	}
	if len(got) != len(want) {
		t.Fatalf("calls = %+v, want %+v", got, want)
	}
	for index := range want {
		if !got[index].started.Equal(want[index].started) || got[index].ended != want[index].ended ||
			got[index].status != want[index].status || got[index].client != want[index].client {
			t.Fatalf("calls = %+v, want %+v", got, want)
		}
	}
}
