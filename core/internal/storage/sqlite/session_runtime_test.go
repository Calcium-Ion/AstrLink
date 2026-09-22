package sqlite

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestSessionRuntimeExcludesIdleGapsAndIncludesRetryCalls(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "runtime.db"))
	defer store.Close()
	ctx := context.Background()
	start := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	sessionID := contract.SessionID("session_runtime")
	first := contract.RequestRecord{
		ID: "request_first", SessionID: &sessionID, StartedAt: start,
		CompletedAt: ptrTime(start.Add(2 * time.Second)), LatencyMs: ptrInt(1500),
		Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses,
		Audit: contract.NotCapturedAuditSummary(),
	}
	second := first
	second.ID = "request_second"
	second.StartedAt = start.Add(39 * time.Hour)
	second.CompletedAt = ptrTime(second.StartedAt.Add(3 * time.Second))
	second.LatencyMs = nil // Historical calls can use their own completion timestamp.
	child := second
	child.ID = "request_retry"
	child.ParentRequestID = &second.ID
	child.SessionID = nil // The root determines membership, including legacy children.
	child.AttemptIndex = 1
	child.StartedAt = second.StartedAt.Add(-time.Minute)
	child.CompletedAt = ptrTime(child.StartedAt.Add(500 * time.Millisecond))
	child.LatencyMs = ptrInt(500)
	child.Status = contract.RequestStatusFailed
	for _, record := range []contract.RequestRecord{first, second, child} {
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}

	// Filtering finds a session but runtime still includes its complete history.
	from := second.StartedAt
	page, err := store.ListRequestSessions(ctx, storagecontract.RequestSessionListOptions{From: &from})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	detail, err := store.GetRequestSession(ctx, string(sessionID))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(page.Items[0], detail.RequestSession) {
		t.Fatalf("list=%+v detail=%+v", page.Items[0], detail.RequestSession)
	}
	if detail.DurationMs != 5000 || detail.CallCount != 3 || len(detail.ActiveRequestStarts) != 0 {
		t.Fatalf("runtime=%+v", detail.RequestSession)
	}
}

func TestSessionRuntimeTracksAllActiveCallsAndFreezesTerminalRecords(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "active-runtime.db"))
	defer store.Close()
	ctx := context.Background()
	start := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	sessionID := contract.SessionID("session_active")
	active := contract.RequestRecord{
		ID: "request_active", SessionID: &sessionID, StartedAt: start,
		Status: contract.RequestStatusPending, InputProtocol: contract.ProtocolOpenAIResponses,
		Audit: contract.NotCapturedAuditSummary(),
	}
	concurrent := active
	concurrent.ID = "request_concurrent"
	concurrent.StartedAt = start.Add(time.Second)
	finished := active
	finished.ID = "request_finished"
	finished.StartedAt = start.Add(2 * time.Second)
	finished.Status = contract.RequestStatusSucceeded
	finished.LatencyMs = ptrInt(120)
	finished.CompletedAt = ptrTime(finished.StartedAt.Add(120 * time.Millisecond))
	unknown := active
	unknown.ID = "request_unknown"
	unknown.Status = contract.RequestStatusFailed // A terminal row without timing must not tick.
	recovered := unknown
	recovered.ID = "request_recovered"
	recovered.CompletedAt = ptrTime(start.Add(39 * time.Hour))
	recovered.Error = &contract.ErrorSummary{Category: "runtime", Code: "core_interrupted", Message: "interrupted"}
	for _, record := range []contract.RequestRecord{active, concurrent, finished, unknown, recovered} {
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	assertRuntime := func(wantMs int64, wantStarts []time.Time) {
		t.Helper()
		page, err := store.ListRequestSessions(ctx, storagecontract.RequestSessionListOptions{})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("page=%+v err=%v", page, err)
		}
		detail, err := store.GetRequestSession(ctx, string(sessionID))
		if err != nil || !reflect.DeepEqual(page.Items[0], detail.RequestSession) {
			t.Fatalf("list=%+v detail=%+v err=%v", page.Items[0], detail.RequestSession, err)
		}
		if detail.DurationMs != wantMs || !reflect.DeepEqual(detail.ActiveRequestStarts, wantStarts) {
			t.Fatalf("runtime=%d active=%v; want %d %v", detail.DurationMs, detail.ActiveRequestStarts, wantMs, wantStarts)
		}
	}
	assertRuntime(120, []time.Time{active.StartedAt, concurrent.StartedAt})
	for _, record := range []contract.RequestRecord{active, concurrent} {
		record.Status = contract.RequestStatusCancelled
		record.LatencyMs = ptrInt(2500)
		record.CompletedAt = ptrTime(record.StartedAt.Add(2500 * time.Millisecond))
		if err := store.UpsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	assertRuntime(5120, []time.Time{})
}
