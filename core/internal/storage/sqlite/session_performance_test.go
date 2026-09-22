package sqlite

import (
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestSessionPerformancePersistsAndAggregatesEligibleCalls(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "performance.db"))
	defer store.Close()
	start := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	sid := contract.SessionID("session_performance")
	first := contract.RequestRecord{ID: "request_first", SessionID: &sid, StartedAt: start,
		CompletedAt: ptrTime(start.Add(2 * time.Second)), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, Streaming: true, TurnIndex: ptrInt(1),
		LatencyMs: ptrInt(2000), FirstTokenMs: ptrInt(500), Usage: &contract.Usage{OutputTokens: 150, TotalTokens: 150}}
	second := first
	second.ID, second.StartedAt = "request_second", start.Add(6*time.Second)
	second.CompletedAt, second.LatencyMs, second.FirstTokenMs = ptrTime(start.Add(10*time.Second)), ptrInt(4000), ptrInt(1000)
	second.Usage = &contract.Usage{OutputTokens: 90, TotalTokens: 90}
	retry := second
	retry.ID, retry.ParentRequestID, retry.SessionID = "request_retry", &second.ID, nil
	retry.StartedAt, retry.CompletedAt = start.Add(5*time.Second), ptrTime(start.Add(5500*time.Millisecond))
	retry.LatencyMs, retry.FirstTokenMs, retry.Usage, retry.Status = ptrInt(500), nil, nil, contract.RequestStatusFailed
	nextTurn := first
	nextTurn.ID, nextTurn.TurnIndex, nextTurn.Streaming, nextTurn.FirstTokenMs = "request_next_turn", ptrInt(2), false, nil
	nextTurn.StartedAt, nextTurn.CompletedAt = start.Add(24*time.Hour), ptrTime(start.Add(24*time.Hour+2*time.Second))
	for _, record := range []contract.RequestRecord{first, second, retry, nextTurn} {
		if err := store.InsertRequestRecord(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.UpsertRequestRecord(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	detail, err := store.GetRequestSession(t.Context(), string(sid))
	if err != nil {
		t.Fatal(err)
	}
	if detail.Turns[0].FirstTokenMs == nil || *detail.Turns[0].FirstTokenMs != 500 {
		t.Fatal("first token timing did not round trip")
	}
	if detail.DurationMs != 8500 || detail.ToolDurationMs == nil || *detail.ToolDurationMs != 3000 ||
		detail.AverageTTFTMs == nil || *detail.AverageTTFTMs != 750 ||
		detail.OutputTokensPerSecond == nil || math.Abs(*detail.OutputTokensPerSecond-240.0/4.5) > 1e-9 {
		t.Fatalf("unexpected statistics: %+v", detail.RequestSession)
	}
	page, err := store.ListRequestSessions(t.Context(), storagecontract.RequestSessionListOptions{From: &nextTurn.StartedAt})
	if err != nil || len(page.Items) != 1 || !reflect.DeepEqual(page.Items[0], detail.RequestSession) {
		t.Fatalf("list statistics differ: %+v, %v", page, err)
	}
}

func TestSessionPerformanceExcludesMissingAndIncompleteSamples(t *testing.T) {
	start := time.Now()
	stats := &sessionPerformance{}
	base := contract.RequestRecord{ID: "request_first", StartedAt: start, CompletedAt: ptrTime(start.Add(time.Second)),
		InputProtocol: contract.ProtocolOpenAIChat, Streaming: true, LatencyMs: ptrInt(1000), FirstTokenMs: ptrInt(200),
		Usage: &contract.Usage{OutputTokens: 100, BillingIncomplete: true}}
	stats.observe(string(base.ID), 0, base)
	zero := base
	zero.ID, zero.FirstTokenMs, zero.Usage = "request_zero", ptrInt(1000), &contract.Usage{OutputTokens: 100}
	stats.observe(string(zero.ID), 0, zero)
	var session contract.RequestSession
	stats.apply(&session)
	if session.ToolDurationMs != nil || session.OutputTokensPerSecond != nil || session.AverageTTFTMs == nil || *session.AverageTTFTMs != 600 {
		t.Fatalf("invalid samples were included: %+v", session)
	}
	legacy := &sessionPerformance{}
	base.FirstTokenMs = nil
	legacy.observe(string(base.ID), 0, base)
	session = contract.RequestSession{}
	legacy.apply(&session)
	if session.ToolDurationMs != nil || session.AverageTTFTMs != nil || session.OutputTokensPerSecond != nil {
		t.Fatal("legacy timing was invented")
	}
}

func TestSessionToolEstimateMergesOverlappingCalls(t *testing.T) {
	start := time.Now()
	stats := &sessionPerformance{}
	for i, interval := range [][2]int{{0, 10}, {2, 4}, {12, 14}} {
		id := contract.RequestID([]string{"request_a", "request_b", "request_c"}[i])
		record := contract.RequestRecord{ID: id, InputProtocol: contract.ProtocolOpenAIChat,
			StartedAt: start.Add(time.Duration(interval[0]) * time.Second), CompletedAt: ptrTime(start.Add(time.Duration(interval[1]) * time.Second))}
		stats.observe(string(id), 1, record)
	}
	var session contract.RequestSession
	stats.apply(&session)
	if session.ToolDurationMs == nil || *session.ToolDurationMs != 2000 {
		t.Fatalf("overlapping call counted twice: %+v", session)
	}
	stats.calls["request_a"].end = nil
	session = contract.RequestSession{}
	stats.apply(&session)
	if session.ToolDurationMs != nil {
		t.Fatal("unknown completion produced a tool estimate")
	}
}
