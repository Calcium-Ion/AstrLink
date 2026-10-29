package sqlite

import (
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

// subagentRoot is a finished root call of session, optionally a subagent of
// parent, naming cursor as its explicit identity.
func subagentRoot(id string, session string, parent string, started time.Time, turn, users int, fingerprint string, cursor string) contract.RequestRecord {
	sessionID := contract.SessionID(session)
	completed := started.Add(time.Second)
	model := "model-a"
	record := contract.RequestRecord{
		ID: contract.RequestID(id), StartedAt: started, CompletedAt: &completed,
		Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolAnthropicMessages,
		RequestedModel: &model, Audit: contract.NotCapturedAuditSummary(), SessionID: &sessionID,
		TurnIndex: &turn, TurnUserMessages: &users,
	}
	if fingerprint != "" {
		record.TurnUserFingerprint = &fingerprint
	}
	if parent != "" {
		parentID := contract.SessionID(parent)
		record.ParentSessionID = &parentID
	}
	if cursor != "" {
		record.Cursors = []contract.SessionCursor{{Kind: contract.SessionCursorExplicit, Direction: contract.SessionCursorIn, Value: cursor}}
	}
	return record
}

func TestSessionLinkContinuesSkipsCallsOffTheLine(t *testing.T) {
	store, _ := writerTestStore(t)
	start := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	records := []contract.RequestRecord{
		subagentRoot("request_main", "session_main", "", start, 1, 1, "fp_a", "thread"),
		subagentRoot("request_fork", "session_main", "", start.Add(time.Minute), 2, 2, "fp_fork", "thread"),
	}
	for _, record := range records {
		if err := store.InsertRequestRecord(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	latest, found, err := store.FindSessionLink(t.Context(), contract.SessionCursorExplicit, []string{"thread"}, storagecontract.SessionCursorScope{})
	if err != nil || !found || latest.TurnUserFingerprint != "fp_fork" {
		t.Fatalf("latest = %+v %v %v", latest, found, err)
	}
	scope := storagecontract.SessionCursorScope{Continues: []storagecontract.SessionTurnPoint{
		{UserMessages: 1, Fingerprint: "fp_a"}, {UserMessages: 2, Fingerprint: "fp_next"},
	}}
	continued, found, err := store.FindSessionLink(t.Context(), contract.SessionCursorExplicit, []string{"thread"}, scope)
	if err != nil || !found || continued.TurnUserFingerprint != "fp_a" || continued.TurnIndex == nil || *continued.TurnIndex != 1 {
		t.Fatalf("continued = %+v %v %v", continued, found, err)
	}
	scope.Continues = []storagecontract.SessionTurnPoint{{UserMessages: 3, Fingerprint: "fp_none"}}
	if _, found, err := store.FindSessionLink(t.Context(), contract.SessionCursorExplicit, []string{"thread"}, scope); err != nil || found {
		t.Fatalf("a history continuing no call matched: %v %v", found, err)
	}
}

func TestSubagentSessionsLinkFoldAndPurge(t *testing.T) {
	store, _ := writerTestStore(t)
	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	records := []contract.RequestRecord{
		subagentRoot("request_parent_1", "session_parent", "", start, 1, 1, "fp_p", "parent-thread"),
		subagentRoot("request_child_a1", "session_child_a", "session_parent", start.Add(time.Second), 1, 1, "fp_a", "child-a"),
		subagentRoot("request_child_b1", "session_child_b", "session_parent", start.Add(2*time.Second), 1, 1, "fp_b", "child-b"),
		subagentRoot("request_child_a2", "session_child_a", "session_parent", start.Add(3*time.Second), 1, 1, "fp_a", "child-a"),
		subagentRoot("request_grandchild", "session_grandchild", "session_child_b", start.Add(4*time.Second), 1, 1, "fp_g", "grandchild"),
		subagentRoot("request_parent_2", "session_parent", "", start.Add(5*time.Second), 1, 1, "fp_p", "parent-thread"),
		subagentRoot("request_other", "session_other", "", start.Add(6*time.Second), 1, 1, "fp_o", "other"),
	}
	for _, record := range records {
		if err := store.InsertRequestRecord(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}

	match, found, err := store.FindSessionLink(t.Context(), contract.SessionCursorExplicit, []string{"child-a"}, storagecontract.SessionCursorScope{})
	if err != nil || !found || match.ParentSessionID == nil || *match.ParentSessionID != "session_parent" {
		t.Fatalf("child link = %+v %v %v", match, found, err)
	}
	turn, found, err := store.FindSessionTurn(t.Context(), "session_parent")
	if err != nil || !found || turn.TurnIndex == nil || turn.ParentSessionID != nil || turn.Status != contract.RequestStatusSucceeded {
		t.Fatalf("parent turn = %+v %v %v", turn, found, err)
	}
	if _, found, err := store.FindSessionTurn(t.Context(), "session_missing"); err != nil || found {
		t.Fatalf("unknown session = %v %v", found, err)
	}

	page, err := store.ListRequestSessions(t.Context(), storagecontract.RequestSessionListOptions{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[contract.SessionID]contract.RequestSession{}
	for _, session := range page.Items {
		byID[session.ID] = session
	}
	if parent := byID["session_parent"]; parent.SubagentCount != 2 || parent.ParentSessionID != nil {
		t.Fatalf("parent summary = %+v", parent)
	}
	if child := byID["session_child_a"]; child.ParentSessionID == nil || *child.ParentSessionID != "session_parent" || child.CallCount != 2 {
		t.Fatalf("child summary = %+v", child)
	}
	if child := byID["session_child_b"]; child.SubagentCount != 1 {
		t.Fatalf("nested parent summary = %+v", child)
	}

	detail, err := store.GetRequestSession(t.Context(), "session_parent")
	if err != nil {
		t.Fatal(err)
	}
	if detail.SubagentCount != 2 || len(detail.Subagents) != 2 || detail.Subagents[0].ID != "session_child_a" ||
		detail.Subagents[1].ID != "session_child_b" || detail.Subagents[0].CallCount != 2 {
		t.Fatalf("parent detail subagents = %+v", detail.Subagents)
	}
	if err := detail.Validate(); err != nil {
		t.Fatal(err)
	}
	child, err := store.GetRequestSession(t.Context(), "session_child_b")
	if err != nil || child.ParentSessionID == nil || *child.ParentSessionID != "session_parent" || len(child.Subagents) != 1 {
		t.Fatalf("child detail = %+v %v", child.RequestSession, err)
	}

	calls, err := store.ListRequestActivity(t.Context(), start)
	if err != nil {
		t.Fatal(err)
	}
	parents := map[contract.SessionID]string{}
	for _, call := range calls {
		if call.ParentSessionID != nil {
			parents[call.SessionID] = string(*call.ParentSessionID)
		}
	}
	if parents["session_child_a"] != "session_parent" || parents["session_grandchild"] != "session_child_b" || parents["session_parent"] != "" {
		t.Fatalf("activity parents = %v", parents)
	}

	// Deleting a conversation takes the subagents it started with it, at
	// every level, and leaves the others.
	result, err := store.PurgeRequestRecords(t.Context(), contract.PurgeRequest{
		Scope: contract.PurgeScopeSessions, SessionIDs: []contract.SessionID{"session_parent"}, Confirm: true,
	})
	if err != nil || result.DeletedRecords != 6 {
		t.Fatalf("purge = %+v %v", result, err)
	}
	page, err = store.ListRequestSessions(t.Context(), storagecontract.RequestSessionListOptions{Limit: 20})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "session_other" {
		t.Fatalf("sessions after purge = %+v %v", page.Items, err)
	}
}
