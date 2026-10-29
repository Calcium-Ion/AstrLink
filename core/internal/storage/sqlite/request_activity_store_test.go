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
	running.Output = &contract.OutputActivity{Kind: contract.OutputKindTool, Tools: []string{"Bash"}}
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
	if output := calls[2].Output; output == nil || output.Kind != contract.OutputKindTool || len(output.Tools) != 1 || output.Tools[0] != "Bash" {
		t.Fatalf("running call output = %+v", output)
	}
	if calls[0].Output != nil {
		t.Fatalf("call without output = %+v", calls[0].Output)
	}
	for index := range want {
		if !got[index].started.Equal(want[index].started) || got[index].ended != want[index].ended ||
			got[index].status != want[index].status || got[index].client != want[index].client {
			t.Fatalf("calls = %+v, want %+v", got, want)
		}
	}
}

func TestListRequestActivityKeepsCallsWhoseToolsMayStillRun(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "activity.db"))
	defer store.Close()
	ctx := context.Background()
	since := time.Date(2026, 10, 23, 6, 0, 0, 0, time.UTC)
	tool := &contract.OutputActivity{Kind: contract.OutputKindTool, Tools: []string{"Bash"}}
	call := func(session, id string, started, completed time.Duration, output *contract.OutputActivity) contract.RequestRecord {
		sessionID := contract.SessionID(session)
		return contract.RequestRecord{
			ID: contract.RequestID(id), SessionID: &sessionID,
			StartedAt: since.Add(started), CompletedAt: ptrTime(since.Add(completed)),
			Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses,
			Output: output, Audit: contract.NotCapturedAuditSummary(),
		}
	}
	waiting := call("session_waiting", "request_waiting", -10*time.Minute, -9*time.Minute, tool)
	// The client brought the results back and the conversation answered.
	answeredTools := call("session_answered", "request_answered_tools", -10*time.Minute, -9*time.Minute, tool)
	answer := call("session_answered", "request_answer", -8*time.Minute, -7*time.Minute,
		&contract.OutputActivity{Kind: contract.OutputKindText})
	// The results come back inside the window, so the wait before it counts.
	resumedTools := call("session_resumed", "request_resumed_tools", -10*time.Minute, -9*time.Minute, tool)
	resumed := call("session_resumed", "request_resumed", time.Minute, 2*time.Minute, tool)
	failed := call("session_failed", "request_failed", -10*time.Minute, -9*time.Minute, tool)
	failed.Status = contract.RequestStatusFailed
	rejected := call("session_rejected", "request_rejected", -10*time.Minute, -9*time.Minute, tool)
	rejected.HTTPStatus = ptrInt(502)
	unnamed := call("session_unnamed", "request_unnamed", -10*time.Minute, -9*time.Minute,
		&contract.OutputActivity{Kind: contract.OutputKindTool})
	tooOld := call("session_old", "request_old", -2*time.Hour, -119*time.Minute, tool)
	// The activity read leaves interrupted calls out, so one does not end the wait.
	interruptedTools := call("session_interrupted", "request_interrupted_tools", -10*time.Minute, -9*time.Minute, tool)
	interrupted := call("session_interrupted", "request_interrupted", -8*time.Minute, -time.Minute, nil)
	interrupted.Status = contract.RequestStatusFailed
	interrupted.Error = &contract.ErrorSummary{Category: "core", Code: "core_interrupted", Message: "core restarted"}
	for _, record := range []contract.RequestRecord{
		waiting, answeredTools, answer, resumedTools, resumed, failed, rejected, unnamed, tooOld, interruptedTools, interrupted,
	} {
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}

	calls, err := store.ListRequestActivity(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	type span struct {
		session contract.SessionID
		started time.Time
	}
	got := make([]span, 0, len(calls))
	for _, item := range calls {
		got = append(got, span{item.SessionID, item.StartedAt.UTC()})
	}
	// Newest first; equal starts order by descending id.
	want := []span{
		{"session_resumed", resumed.StartedAt},
		{"session_waiting", waiting.StartedAt},
		{"session_resumed", resumedTools.StartedAt},
		{"session_interrupted", interruptedTools.StartedAt},
	}
	if len(got) != len(want) {
		t.Fatalf("calls = %+v, want %+v", got, want)
	}
	for index := range want {
		if got[index].session != want[index].session || !got[index].started.Equal(want[index].started) {
			t.Fatalf("calls = %+v, want %+v", got, want)
		}
	}
	if output := calls[1].Output; output == nil || output.Kind != contract.OutputKindTool || len(output.Tools) != 1 {
		t.Fatalf("waiting call output = %+v", output)
	}
}

func TestListRequestActivityReadsFinalOutputTokens(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "activity.db"))
	defer store.Close()
	ctx := context.Background()
	since := time.Date(2026, 10, 23, 6, 0, 0, 0, time.UTC)
	call := func(id string, started time.Duration, usage *contract.Usage) contract.RequestRecord {
		session := contract.SessionID("session_" + id)
		return contract.RequestRecord{
			ID: contract.RequestID("request_" + id), SessionID: &session,
			StartedAt: since.Add(started), CompletedAt: ptrTime(since.Add(started + 10*time.Second)),
			Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolAnthropicMessages,
			Usage: usage, Audit: contract.NotCapturedAuditSummary(),
		}
	}
	final := call("final", 3*time.Minute, &contract.Usage{InputTokens: 900, OutputTokens: 120, TotalTokens: 1020})
	// The stream stopped before its last usage event; the count is partial.
	cut := call("cut", 2*time.Minute, &contract.Usage{InputTokens: 900, OutputTokens: 1, TotalTokens: 901, BillingIncomplete: true})
	cut.Status = contract.RequestStatusCancelled
	unreported := call("unreported", time.Minute, nil)
	for _, record := range []contract.RequestRecord{final, cut, unreported} {
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}

	calls, err := store.ListRequestActivity(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[contract.SessionID]int, len(calls))
	for _, item := range calls {
		got[item.SessionID] = item.OutputTokens
	}
	want := map[contract.SessionID]int{"session_final": 120, "session_cut": 0, "session_unreported": 0}
	if len(got) != len(want) {
		t.Fatalf("output tokens = %v, want %v", got, want)
	}
	for session, tokens := range want {
		if got[session] != tokens {
			t.Fatalf("output tokens = %v, want %v", got, want)
		}
	}
}
