package sqlite

import (
	"context"
	"fmt"
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
	if detail.RunningSince != nil {
		t.Fatalf("idle session started a running block: %v", detail.RunningSince)
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
	assertRuntime := func(wantMs int64, wantStarts []time.Time, wantRunningSince *time.Time) {
		t.Helper()
		page, err := store.ListRequestSessions(ctx, storagecontract.RequestSessionListOptions{})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("page=%+v err=%v", page, err)
		}
		detail, err := store.GetRequestSession(ctx, string(sessionID))
		if err != nil || !reflect.DeepEqual(page.Items[0], detail.RequestSession) {
			t.Fatalf("list=%+v detail=%+v err=%v", page.Items[0], detail.RequestSession, err)
		}
		if detail.DurationMs != wantMs || !reflect.DeepEqual(detail.ActiveRequestStarts, wantStarts) ||
			!reflect.DeepEqual(detail.RunningSince, wantRunningSince) {
			t.Fatalf("runtime=%d active=%v running_since=%v; want %d %v %v",
				detail.DurationMs, detail.ActiveRequestStarts, detail.RunningSince, wantMs, wantStarts, wantRunningSince)
		}
	}
	// The finished call overlaps both running calls, so its runtime joins the
	// one open block instead of ticking twice.
	assertRuntime(0, []time.Time{active.StartedAt, concurrent.StartedAt}, &start)
	for _, record := range []contract.RequestRecord{active, concurrent} {
		record.Status = contract.RequestStatusCancelled
		record.LatencyMs = ptrInt(2500)
		record.CompletedAt = ptrTime(record.StartedAt.Add(2500 * time.Millisecond))
		if err := store.UpsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	// [start, start+2.5s] and [start+1s, start+3.5s] merge to 3.5 s.
	assertRuntime(3500, []time.Time{}, nil)
}

func TestSessionRuntimeCountsOverlappingCallsOnce(t *testing.T) {
	start := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	at := func(seconds int) time.Time { return start.Add(time.Duration(seconds) * time.Second) }
	type call struct {
		offset  int // seconds from the session start
		runtime int // seconds; ignored for a running call
		running bool
		retry   bool
	}
	cases := []struct {
		name      string
		calls     []call
		wantMs    int64
		wantSince *time.Time
	}{
		{
			name:   "fully overlapping calls count once",
			calls:  []call{{offset: 0, runtime: 10}, {offset: 2, runtime: 10}},
			wantMs: 12_000,
		},
		{
			name:   "a call nested in another counts once",
			calls:  []call{{offset: 0, runtime: 10}, {offset: 2, runtime: 1}},
			wantMs: 10_000,
		},
		{
			name:   "partial overlap counts the shared time once",
			calls:  []call{{offset: 0, runtime: 10}, {offset: 5, runtime: 3}},
			wantMs: 10_000,
		},
		{
			name:   "a gap between calls stays excluded",
			calls:  []call{{offset: 0, runtime: 10}, {offset: 20, runtime: 5}},
			wantMs: 15_000,
		},
		{
			name:   "retry children add their sequential runtime",
			calls:  []call{{offset: 0, runtime: 5}, {offset: -1, runtime: 1, retry: true}},
			wantMs: 6_000,
		},
		{
			// Finished [0 s, 10 s] and running since 5 s merge to one block from
			// 0 s, so the client adds the time since 0 s, not 10 s plus 15 s.
			name:      "a running call absorbs the finished call it overlaps",
			calls:     []call{{offset: 0, runtime: 10}, {offset: 5, running: true}},
			wantSince: ptrTime(at(0)),
		},
		{
			name:      "every running call joins one block",
			calls:     []call{{offset: 0, runtime: 4}, {offset: 5, running: true}, {offset: 7, running: true}},
			wantMs:    4_000,
			wantSince: ptrTime(at(5)),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t, filepath.Join(t.TempDir(), "runtime.db"))
			defer store.Close()
			ctx := context.Background()
			sessionID := contract.SessionID("session_overlap")
			rootID := contract.RequestID("request_0")
			for index, item := range test.calls {
				record := contract.RequestRecord{
					ID: contract.RequestID(fmt.Sprintf("request_%d", index)), SessionID: &sessionID,
					StartedAt: at(item.offset), Status: contract.RequestStatusSucceeded,
					InputProtocol: contract.ProtocolOpenAIResponses,
					Audit:         contract.NotCapturedAuditSummary(),
				}
				if item.retry {
					record.SessionID = nil
					record.ParentRequestID = &rootID
				}
				if item.running {
					record.Status = contract.RequestStatusPending
				} else {
					record.LatencyMs = ptrInt(item.runtime * 1000)
					record.CompletedAt = ptrTime(at(item.offset + item.runtime))
				}
				if err := store.InsertRequestRecord(ctx, record); err != nil {
					t.Fatal(err)
				}
			}
			page, err := store.ListRequestSessions(ctx, storagecontract.RequestSessionListOptions{})
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("page=%+v err=%v", page, err)
			}
			session := page.Items[0]
			if session.DurationMs != test.wantMs || !reflect.DeepEqual(session.RunningSince, test.wantSince) {
				t.Fatalf("duration=%d running_since=%v; want %d %v",
					session.DurationMs, session.RunningSince, test.wantMs, test.wantSince)
			}
		})
	}
}
