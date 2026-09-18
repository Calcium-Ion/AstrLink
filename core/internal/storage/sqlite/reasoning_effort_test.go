package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestReasoningEffortRoundTripAndLatestSessionValue(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "reasoning.db"))
	defer store.Close()
	ctx := context.Background()
	sessionID := contract.SessionID("session_effort")
	first := contract.RequestRecord{
		ID: "request_first", SessionID: &sessionID, StartedAt: time.Now().UTC(),
		InputProtocol: contract.ProtocolOpenAIChat, Status: contract.RequestStatusSucceeded,
		RequestedModel: ptrString("model-a"), ReasoningEffort: ptrString("low"),
	}
	if err := store.InsertRequestRecord(ctx, first); err != nil {
		t.Fatal(err)
	}
	latest := first
	latest.ID = "request_latest"
	latest.StartedAt = first.StartedAt.Add(time.Second)
	latest.Status = contract.RequestStatusPending
	latest.RequestedModel = ptrString("model-b")
	latest.ReasoningEffort = ptrString("high")
	if err := store.UpsertRequestRecord(ctx, latest); err != nil {
		t.Fatal(err)
	}
	read, err := store.GetRequestRecord(ctx, latest.ID)
	if err != nil || read.ReasoningEffort == nil || *read.ReasoningEffort != "high" {
		t.Fatalf("record=%#v err=%v", read, err)
	}
	page, err := store.ListRequestSessions(ctx, storagecontract.RequestSessionListOptions{})
	if err != nil || len(page.Items) != 1 || page.Items[0].ReasoningEffort == nil || *page.Items[0].ReasoningEffort != "high" || *page.Items[0].RequestedModel != "model-b" {
		t.Fatalf("session=%#v err=%v", page, err)
	}
	// A later snapshot without a specified effort must clear the old badge,
	// not inherit the first call's level or the previous pending snapshot.
	latest.ReasoningEffort = nil
	latest.Status = contract.RequestStatusSucceeded
	if err := store.UpsertRequestRecord(ctx, latest); err != nil {
		t.Fatal(err)
	}
	detail, err := store.GetRequestSession(ctx, string(sessionID))
	if err != nil || detail.ReasoningEffort != nil || len(detail.Turns) != 2 {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
	if detail.Turns[0].ReasoningEffort == nil || *detail.Turns[0].ReasoningEffort != "low" || detail.Turns[1].ReasoningEffort != nil {
		t.Fatalf("per-call levels lost: %#v", detail.Turns)
	}
}
