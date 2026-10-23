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

func TestRequestOutputFollowsTheStreamUntilTheCallEnds(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "output.db"))
	defer store.Close()
	ctx := context.Background()
	start := time.Date(2026, 10, 23, 7, 0, 0, 0, time.UTC)
	session := contract.SessionID("session_output")
	earlier := contract.RequestRecord{
		ID: "request_earlier", SessionID: &session, StartedAt: start,
		CompletedAt: ptrTime(start.Add(time.Second)), LatencyMs: ptrInt(1000),
		Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolAnthropicMessages,
		Output: &contract.OutputActivity{Kind: contract.OutputKindTool, Tools: []string{"Read"}},
		Audit:  contract.NotCapturedAuditSummary(),
	}
	running := contract.RequestRecord{
		ID: "request_running", SessionID: &session, StartedAt: start.Add(2 * time.Second),
		Status: contract.RequestStatusPending, InputProtocol: contract.ProtocolAnthropicMessages,
		Audit: contract.NotCapturedAuditSummary(),
	}
	for _, record := range []contract.RequestRecord{earlier, running} {
		if err := store.UpsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	sessionOutput := func() *contract.OutputActivity {
		t.Helper()
		detail, err := store.GetRequestSession(ctx, string(session))
		if err != nil {
			t.Fatal(err)
		}
		return detail.Output
	}
	if output := sessionOutput(); output != nil {
		t.Fatalf("session output before the running call produced any = %+v", output)
	}

	thinking := contract.OutputActivity{Kind: contract.OutputKindReasoning}
	if err := store.UpdateRequestOutput(ctx, running.ID, thinking); err != nil {
		t.Fatal(err)
	}
	if output := sessionOutput(); !reflect.DeepEqual(output, &thinking) {
		t.Fatalf("session output while thinking = %+v", output)
	}

	finished := running
	finished.Status = contract.RequestStatusSucceeded
	finished.CompletedAt = ptrTime(running.StartedAt.Add(3 * time.Second))
	finished.LatencyMs = ptrInt(3000)
	finished.Output = &contract.OutputActivity{Kind: contract.OutputKindTool, Tools: []string{"Bash", "Edit"}}
	if err := store.UpsertRequestRecord(ctx, finished); err != nil {
		t.Fatal(err)
	}
	// A late update from the stream cannot replace what the call ended with.
	if err := store.UpdateRequestOutput(ctx, running.ID, contract.OutputActivity{Kind: contract.OutputKindText}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetRequestRecord(ctx, running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Output, finished.Output) {
		t.Fatalf("record output = %+v, want %+v", loaded.Output, finished.Output)
	}
	if output := sessionOutput(); !reflect.DeepEqual(output, finished.Output) {
		t.Fatalf("session output = %+v, want %+v", output, finished.Output)
	}
	page, err := store.ListRequestSessions(ctx, storagecontract.RequestSessionListOptions{})
	if err != nil || len(page.Items) != 1 || !reflect.DeepEqual(page.Items[0].Output, finished.Output) {
		t.Fatalf("listed sessions = %+v, err = %v", page.Items, err)
	}

	if err := store.UpdateRequestOutput(ctx, running.ID, contract.OutputActivity{Kind: "typing"}); err == nil {
		t.Fatal("an unknown output kind was accepted")
	}
}
