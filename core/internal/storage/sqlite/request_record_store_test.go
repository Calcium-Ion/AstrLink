package sqlite

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestRequestRecordStoreInsertListFiltersAndPurge(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "astrlink.db")
	store := openTestStore(t, databasePath)
	defer store.Close()
	ctx := context.Background()

	start := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
	model := "public-alias"
	statusOK := 200
	latency := 15
	endpointID := contract.ServiceID("endpoint_a")
	records := []contract.RequestRecord{
		{
			ID: "request_a", StartedAt: start, CompletedAt: ptrTime(start.Add(time.Second)),
			Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses,
			RequestedModel: &model, Streaming: false, ServiceID: &endpointID,
			HTTPStatus: &statusOK, LatencyMs: &latency, Audit: contract.NotCapturedAuditSummary(),
			Usage: &contract.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
			PrivacyRestore: &contract.PrivacyRestoreSummary{
				Enabled: true, MappingCount: 4, RestoredCount: 5, FallbackCount: 0,
			},
		},
		{
			ID: "request_b", StartedAt: start.Add(time.Minute), CompletedAt: ptrTime(start.Add(2 * time.Minute)),
			Status: contract.RequestStatusFailed, InputProtocol: contract.ProtocolOpenAIChat,
			Streaming: true, Audit: contract.NotCapturedAuditSummary(),
			Error: &contract.ErrorSummary{Category: "upstream", Code: "upstream_unavailable", Message: "unavailable", Retryable: true},
		},
		{
			ID: "request_c", StartedAt: start.Add(2 * time.Minute), CompletedAt: ptrTime(start.Add(3 * time.Minute)),
			Status: contract.RequestStatusBlocked, InputProtocol: contract.ProtocolOpenAIResponses,
			RequestedModel: &model, Audit: contract.NotCapturedAuditSummary(),
			Error: &contract.ErrorSummary{Category: "privacy", Code: "policy_blocked", Message: "blocked", Retryable: false},
		},
	}
	for _, record := range records {
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatalf("InsertRequestRecord(%s): %v", record.ID, err)
		}
	}

	page, err := store.ListRequestRecords(ctx, storagecontract.RequestRecordListOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != "request_c" || page.Items[1].ID != "request_b" || page.NextCursor == "" {
		t.Fatalf("page = %#v", page)
	}
	page, err = store.ListRequestRecords(ctx, storagecontract.RequestRecordListOptions{Limit: 2, Cursor: page.NextCursor})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "request_a" || page.NextCursor != "" {
		t.Fatalf("second page = %#v err=%v", page, err)
	}

	status := contract.RequestStatusSucceeded
	protocol := contract.ProtocolOpenAIResponses
	from := start
	to := start.Add(90 * time.Second)
	filtered, err := store.ListRequestRecords(ctx, storagecontract.RequestRecordListOptions{
		Status: &status, Protocol: &protocol, ServiceID: &endpointID, From: &from, To: &to,
	})
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].ID != "request_a" {
		t.Fatalf("filtered = %#v err=%v", filtered, err)
	}
	if filtered.Items[0].Usage == nil || filtered.Items[0].Usage.TotalTokens != 3 {
		t.Fatalf("usage = %#v", filtered.Items[0].Usage)
	}
	if filtered.Items[0].PrivacyRestore == nil ||
		filtered.Items[0].PrivacyRestore.MappingCount != 4 ||
		filtered.Items[0].PrivacyRestore.RestoredCount != 5 {
		t.Fatalf("privacy restore = %#v", filtered.Items[0].PrivacyRestore)
	}

	tokenA := contract.AccessTokenID("token_alpha")
	tokenB := contract.AccessTokenID("token_beta")
	withToken := records[0]
	withToken.ID = "request_token_a"
	withToken.StartedAt = start.Add(3 * time.Minute)
	withToken.LocalAccessTokenID = &tokenA
	otherToken := records[0]
	otherToken.ID = "request_token_b"
	otherToken.StartedAt = start.Add(4 * time.Minute)
	otherToken.LocalAccessTokenID = &tokenB
	for _, record := range []contract.RequestRecord{withToken, otherToken} {
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatalf("InsertRequestRecord(%s): %v", record.ID, err)
		}
	}
	tokenFiltered, err := store.ListRequestRecords(ctx, storagecontract.RequestRecordListOptions{
		LocalAccessTokenID: &tokenA,
	})
	if err != nil || len(tokenFiltered.Items) != 1 || tokenFiltered.Items[0].ID != "request_token_a" {
		t.Fatalf("token filtered = %#v err=%v", tokenFiltered, err)
	}

	got, err := store.GetRequestRecord(ctx, "request_a")
	if err != nil || got.ID != "request_a" {
		t.Fatalf("GetRequestRecord: %#v %v", got, err)
	}
	if err := store.DeleteRequestRecord(ctx, "request_b"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRequestRecord(ctx, "request_b"); !errors.Is(err, storagecontract.ErrNotFound) {
		t.Fatalf("deleted get error = %v", err)
	}

	before := start.Add(90 * time.Second)
	result, err := store.PurgeRequestRecords(ctx, contract.PurgeRequest{
		Scope: contract.PurgeScopeBefore, Before: &before, Confirm: true,
	})
	if err != nil || result.DeletedRecords != 1 || result.DeletedAuditBlobs != 0 {
		t.Fatalf("purge before = %#v err=%v", result, err)
	}
	result, err = store.PurgeRequestRecords(ctx, contract.PurgeRequest{Scope: contract.PurgeScopeAll, Confirm: true})
	if err != nil || result.DeletedRecords != 3 {
		t.Fatalf("purge all = %#v err=%v", result, err)
	}

	reopened, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("reopen after migration: %v", err)
	}
	defer reopened.Close()
	var tableCount int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='request_records'`).Scan(&tableCount); err != nil || tableCount != 1 {
		t.Fatalf("request_records missing after reopen: count=%d err=%v", tableCount, err)
	}
}

func TestRequestRecordStoreLiveUpsertAndStartupRecovery(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	started := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
	model := "gpt-live"
	pending := contract.RequestRecord{
		ID:             "request_live",
		StartedAt:      started,
		Status:         contract.RequestStatusPending,
		InputProtocol:  contract.ProtocolOpenAIResponses,
		RequestedModel: &model,
		Streaming:      true,
		Audit:          contract.NotCapturedAuditSummary(),
	}
	if err := store.UpsertRequestRecord(ctx, pending); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRequestRecord(ctx, pending.ID)
	if err != nil || got.Status != contract.RequestStatusPending || got.CompletedAt != nil {
		t.Fatalf("pending=%#v err=%v", got, err)
	}

	completed := started.Add(2 * time.Second)
	latency := 2000
	status := http.StatusOK
	terminal := pending
	terminal.Status = contract.RequestStatusSucceeded
	terminal.CompletedAt = &completed
	terminal.LatencyMs = &latency
	terminal.HTTPStatus = &status
	if err := store.UpsertRequestRecord(ctx, terminal); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRequestRecord(ctx, pending); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetRequestRecord(ctx, pending.ID)
	if err != nil || got.Status != contract.RequestStatusSucceeded || got.CompletedAt == nil {
		t.Fatalf("late pending downgraded terminal=%#v err=%v", got, err)
	}

	interrupted := pending
	interrupted.ID = "request_interrupted"
	if err := store.UpsertRequestRecord(ctx, interrupted); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.RecoverPendingRequestRecords(ctx)
	if err != nil || recovered != 1 {
		t.Fatalf("recover count=%d err=%v", recovered, err)
	}
	got, err = store.GetRequestRecord(ctx, interrupted.ID)
	if err != nil || got.Status != contract.RequestStatusFailed || got.Error == nil ||
		got.Error.Code != "core_interrupted" || got.CompletedAt == nil {
		t.Fatalf("recovered=%#v err=%v", got, err)
	}
}

func TestRequestSessionStoreGroupsTurnsAndLinksCursors(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "sessions.db"))
	defer store.Close()
	ctx := context.Background()
	start := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	sessionID := contract.SessionID("session_linked")
	previous := "resp_one"
	output := "resp_two"
	preview := "创建快捷方式"
	first := contract.RequestRecord{
		ID: "request_turn_a", StartedAt: start, Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
		SessionID: &sessionID, OutputResponseID: &previous, InputPreview: &preview,
	}
	second := first
	second.ID = "request_turn_b"
	second.StartedAt = start.Add(time.Minute)
	secondCompleted := start.Add(90 * time.Second)
	second.CompletedAt = &secondCompleted
	second.PreviousResponseID = &previous
	second.OutputResponseID = &output
	legacy := first
	legacy.ID = "request_legacy"
	legacy.StartedAt = start.Add(2 * time.Minute)
	legacy.SessionID = nil
	legacy.OutputResponseID = nil
	legacy.InputPreview = nil
	legacy.RequestedModel = ptrString("solo-model")
	for _, record := range []contract.RequestRecord{first, second, legacy} {
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}

	// Legacy columns still anchor explicit lookups.
	linked, ok, err := store.FindSessionLink(ctx, contract.SessionCursorExplicit, []string{"resp_one"}, storagecontract.SessionCursorScope{})
	if err != nil || !ok || linked.SessionID != sessionID || linked.Value != "resp_one" {
		t.Fatalf("link=%+v ok=%v err=%v", linked, ok, err)
	}

	page, err := store.ListRequestSessions(ctx, storagecontract.RequestSessionListOptions{Limit: 10})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("sessions=%#v err=%v", page, err)
	}
	if page.Items[0].ID != contract.SessionID("request_legacy") || page.Items[1].ID != sessionID {
		t.Fatalf("order=%#v", page.Items)
	}
	if page.Items[1].TurnCount != 2 || page.Items[1].Title != preview {
		t.Fatalf("linked session=%#v", page.Items[1])
	}
	if page.Items[1].CompletedAt == nil || !page.Items[1].CompletedAt.Equal(secondCompleted) {
		t.Fatalf("linked session completed_at=%v", page.Items[1].CompletedAt)
	}

	detail, err := store.GetRequestSession(ctx, string(sessionID))
	if err != nil || len(detail.Turns) != 2 || detail.Turns[0].ID != "request_turn_a" {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
	if detail.CompletedAt == nil || !detail.CompletedAt.Equal(secondCompleted) {
		t.Fatalf("detail completed_at=%v", detail.CompletedAt)
	}
	legacyDetail, err := store.GetRequestSession(ctx, "request_legacy")
	if err != nil || legacyDetail.ID != "request_legacy" || len(legacyDetail.Turns) != 1 {
		t.Fatalf("legacy=%#v err=%v", legacyDetail, err)
	}
}

func TestRequestSessionStoreTreatsLegacyHTTPErrorAsFailed(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "sessions-http-error.db"))
	defer store.Close()
	ctx := context.Background()
	start := time.Date(2026, 9, 3, 2, 7, 29, 0, time.UTC)
	completed := start.Add(63 * time.Second)
	status := http.StatusBadGateway
	sessionID := contract.SessionID("session_http_error")
	record := contract.RequestRecord{
		ID: "request_a979607ff60139b9386dcb11", StartedAt: start, CompletedAt: &completed,
		Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolAnthropicMessages,
		HTTPStatus: &status, Audit: contract.NotCapturedAuditSummary(), SessionID: &sessionID,
	}
	if err := store.InsertRequestRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListRequestSessions(ctx, storagecontract.RequestSessionListOptions{Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("sessions=%#v err=%v", page, err)
	}
	if page.Items[0].Status != contract.RequestStatusFailed {
		t.Fatalf("list status=%q", page.Items[0].Status)
	}
	detail, err := store.GetRequestSession(ctx, string(sessionID))
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != contract.RequestStatusFailed {
		t.Fatalf("detail status=%q", detail.Status)
	}
	if detail.Turns[0].Status != contract.RequestStatusSucceeded {
		t.Fatalf("stored turn status should stay succeeded, got %q", detail.Turns[0].Status)
	}
}

func TestListRequestSessionsHonorsSQLLimitWithManyRoots(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "sessions-limit.db"))
	defer store.Close()
	ctx := context.Background()
	start := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	for index := 0; index < 180; index++ {
		record := contract.RequestRecord{
			ID:            contract.RequestID(fmt.Sprintf("request_lim_%03d", index)),
			StartedAt:     start.Add(time.Duration(index) * time.Second),
			Status:        contract.RequestStatusSucceeded,
			InputProtocol: contract.ProtocolOpenAIResponses,
			Audit:         contract.NotCapturedAuditSummary(),
		}
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatalf("InsertRequestRecord(%s): %v", record.ID, err)
		}
		child := record
		child.ID = contract.RequestID(fmt.Sprintf("request_lim_%03d_c", index))
		child.ParentRequestID = &record.ID
		child.AttemptIndex = 1
		if err := store.InsertRequestRecord(ctx, child); err != nil {
			t.Fatalf("InsertRequestRecord(%s): %v", child.ID, err)
		}
	}

	page, err := store.ListRequestSessions(ctx, storagecontract.RequestSessionListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 10 || page.NextCursor == "" {
		t.Fatalf("first page = %#v", page)
	}
	if page.Items[0].ID != "request_lim_179" || page.Items[0].CallCount != 2 {
		t.Fatalf("newest session = %#v", page.Items[0])
	}
	second, err := store.ListRequestSessions(ctx, storagecontract.RequestSessionListOptions{
		Limit: 10, Cursor: page.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 10 || second.Items[0].ID == page.Items[0].ID {
		t.Fatalf("second page = %#v", second)
	}
}

func TestRequestRecordCursorsRoundTripScopeAndCascade(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "cursors.db"))
	defer store.Close()
	ctx := context.Background()
	start := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	tokenA := contract.AccessTokenID("token_a")
	tokenB := contract.AccessTokenID("token_b")
	sessionA := contract.SessionID("session_a")
	turnOne := 1

	pending := contract.RequestRecord{
		ID: "request_cur_a", StartedAt: start, Status: contract.RequestStatusPending,
		InputProtocol: contract.ProtocolOpenAIChat, Audit: contract.NotCapturedAuditSummary(),
		LocalAccessTokenID: &tokenA, SessionID: &sessionA, TurnIndex: &turnOne,
		Cursors: []contract.SessionCursor{
			{Kind: contract.SessionCursorExplicit, Direction: contract.SessionCursorIn, Value: "conv_shared"},
		},
	}
	if err := store.UpsertRequestRecord(ctx, pending); err != nil {
		t.Fatal(err)
	}
	terminal := pending
	terminal.Status = contract.RequestStatusSucceeded
	terminal.Cursors = []contract.SessionCursor{
		{Kind: contract.SessionCursorExplicit, Direction: contract.SessionCursorIn, Value: "conv_shared"},
		{Kind: contract.SessionCursorExplicit, Direction: contract.SessionCursorOut, Value: "chatcmpl-a"},
		{Kind: contract.SessionCursorEchoID, Direction: contract.SessionCursorOut, Value: "call_7f3a9c2e1b4d4e8fa1c2"},
		{Kind: contract.SessionCursorFingerprint, Direction: contract.SessionCursorOut, Value: "fp1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{Kind: contract.SessionCursorFingerprint, Direction: contract.SessionCursorOut, Value: "fp1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}
	if err := store.UpsertRequestRecord(ctx, terminal); err != nil {
		t.Fatal(err)
	}
	// A late pending snapshot must neither downgrade the row nor its cursors.
	if err := store.UpsertRequestRecord(ctx, pending); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRequestRecord(ctx, pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Cursors) != 4 || got.TurnIndex == nil || *got.TurnIndex != 1 {
		t.Fatalf("stored record = %+v", got)
	}

	scopedA := storagecontract.SessionCursorScope{SamePrincipal: true, LocalAccessTokenID: &tokenA}
	scopedB := storagecontract.SessionCursorScope{SamePrincipal: true, LocalAccessTokenID: &tokenB}
	match, ok, err := store.FindSessionLink(ctx, contract.SessionCursorEchoID, []string{"call_missing", "call_7f3a9c2e1b4d4e8fa1c2"}, scopedA)
	if err != nil || !ok || match.SessionID != sessionA || match.Value != "call_7f3a9c2e1b4d4e8fa1c2" || match.TurnIndex == nil || *match.TurnIndex != 1 {
		t.Fatalf("echo match = %+v ok=%v err=%v", match, ok, err)
	}
	if _, ok, err := store.FindSessionLink(ctx, contract.SessionCursorEchoID, []string{"call_7f3a9c2e1b4d4e8fa1c2"}, scopedB); err != nil || ok {
		t.Fatalf("other token must not match: ok=%v err=%v", ok, err)
	}
	late := storagecontract.SessionCursorScope{SamePrincipal: true, LocalAccessTokenID: &tokenA, NotBefore: start.Add(time.Second)}
	if _, ok, err := store.FindSessionLink(ctx, contract.SessionCursorEchoID, []string{"call_7f3a9c2e1b4d4e8fa1c2"}, late); err != nil || ok {
		t.Fatalf("window must exclude older roots: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := store.FindSessionLink(ctx, contract.SessionCursorExplicit, []string{"conv_shared"}, storagecontract.SessionCursorScope{}); !ok {
		t.Fatal("explicit inbound cursors must match")
	}
	if _, ok, _ := store.FindSessionLink(ctx, contract.SessionCursorFingerprint, []string{"fp1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, scopedA); !ok {
		t.Fatal("fingerprint out cursor must match")
	}
	if _, ok, _ := store.FindSessionLink(ctx, contract.SessionCursorFingerprint, []string{"conv_shared"}, scopedA); ok {
		t.Fatal("kind mismatch must not match")
	}
	if _, ok, _ := store.FindSessionLink(ctx, contract.SessionCursorEchoID, nil, scopedA); ok {
		t.Fatal("empty values must not match")
	}

	// Children never anchor a session, and the newest root wins.
	child := terminal
	child.ID = "request_cur_a_child"
	child.ParentRequestID = &terminal.ID
	child.AttemptIndex = 1
	child.StartedAt = start.Add(time.Minute)
	child.Cursors = []contract.SessionCursor{{Kind: contract.SessionCursorEchoID, Direction: contract.SessionCursorOut, Value: "call_child_only_9a8b7c6d"}}
	if err := store.InsertRequestRecord(ctx, child); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.FindSessionLink(ctx, contract.SessionCursorEchoID, []string{"call_child_only_9a8b7c6d"}, scopedA); ok {
		t.Fatal("child cursors must not anchor sessions")
	}
	sessionB := contract.SessionID("session_b")
	newer := terminal
	newer.ID = "request_cur_b"
	newer.StartedAt = start.Add(2 * time.Minute)
	newer.SessionID = &sessionB
	if err := store.InsertRequestRecord(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if match, ok, _ := store.FindSessionLink(ctx, contract.SessionCursorEchoID, []string{"call_7f3a9c2e1b4d4e8fa1c2"}, scopedA); !ok || match.SessionID != sessionB {
		t.Fatalf("newest root must win: %+v", match)
	}

	// A newer root that merely consumed an explicit cursor must not outrank
	// the root that produced it: the producer carries the turn the
	// continuation builds on. This holds for the cursor table and for the
	// legacy previous_response_id / output_response_id columns alike.
	sessionC := contract.SessionID("session_c")
	turnFive := 5
	previous := "chatcmpl-a"
	consumer := contract.RequestRecord{
		ID: "request_cur_c", StartedAt: start.Add(3 * time.Minute), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIChat, Audit: contract.NotCapturedAuditSummary(),
		LocalAccessTokenID: &tokenA, SessionID: &sessionC, TurnIndex: &turnFive,
		PreviousResponseID: &previous,
		Cursors: []contract.SessionCursor{
			{Kind: contract.SessionCursorExplicit, Direction: contract.SessionCursorIn, Value: "chatcmpl-a"},
		},
	}
	if err := store.InsertRequestRecord(ctx, consumer); err != nil {
		t.Fatal(err)
	}
	if match, ok, _ := store.FindSessionLink(ctx, contract.SessionCursorExplicit, []string{"chatcmpl-a"}, storagecontract.SessionCursorScope{}); !ok || match.SessionID != sessionB || match.TurnIndex == nil || *match.TurnIndex != 1 {
		t.Fatalf("producer must outrank a newer consumer: %+v", match)
	}
	legacyOutput := "resp_legacy_out"
	legacyProducer := contract.RequestRecord{
		ID: "request_legacy_producer", StartedAt: start.Add(4 * time.Minute), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
		SessionID: &sessionA, TurnIndex: &turnOne, OutputResponseID: &legacyOutput,
	}
	legacyConsumer := contract.RequestRecord{
		ID: "request_legacy_consumer", StartedAt: start.Add(5 * time.Minute), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
		SessionID: &sessionC, TurnIndex: &turnFive, PreviousResponseID: &legacyOutput,
	}
	for _, record := range []contract.RequestRecord{legacyProducer, legacyConsumer} {
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	if match, ok, _ := store.FindSessionLink(ctx, contract.SessionCursorExplicit, []string{legacyOutput}, storagecontract.SessionCursorScope{}); !ok || match.SessionID != sessionA || match.TurnIndex == nil || *match.TurnIndex != 1 {
		t.Fatalf("legacy output column must outrank a newer previous_response_id: %+v", match)
	}
	if err := store.DeleteRequestRecord(ctx, consumer.ID); err != nil {
		t.Fatal(err)
	}

	// Deleting the root removes its and its children's cursor rows.
	if err := store.DeleteRequestRecord(ctx, terminal.ID); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM request_record_cursors WHERE request_id IN ('request_cur_a', 'request_cur_a_child')`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("cursor rows after delete = %d err=%v", remaining, err)
	}
	if _, err := store.PurgeRequestRecords(ctx, contract.PurgeRequest{Scope: contract.PurgeScopeAll, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM request_record_cursors`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("cursor rows after purge = %d err=%v", remaining, err)
	}
}

func TestSessionTurnCountGroupsByTurnIndex(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "turns.db"))
	defer store.Close()
	ctx := context.Background()
	start := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	sessionID := contract.SessionID("session_loop")
	turns := []*int{ptrInt(1), ptrInt(1), ptrInt(1), ptrInt(2), nil, ptrInt(1)}
	for index, turn := range turns {
		record := contract.RequestRecord{
			ID: contract.RequestID(fmt.Sprintf("request_loop_%d", index)), StartedAt: start.Add(time.Duration(index) * time.Second),
			Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIChat,
			Audit: contract.NotCapturedAuditSummary(), SessionID: &sessionID, TurnIndex: turn,
		}
		if index > 0 {
			record.SessionLink = &contract.SessionLink{Kind: contract.SessionCursorEchoID, Value: "call_7f3a9c2e1b4d4e8fa1c2"}
		}
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	detail, err := store.GetRequestSession(ctx, string(sessionID))
	if err != nil {
		t.Fatal(err)
	}
	// 1,1,1 → one turn; 2 → second; nil → third; 1 (fell back) → fourth.
	if detail.TurnCount != 4 || detail.CallCount != 6 {
		t.Fatalf("turn_count=%d call_count=%d", detail.TurnCount, detail.CallCount)
	}
	if detail.Turns[1].SessionLink == nil || detail.Turns[1].SessionLink.Kind != contract.SessionCursorEchoID {
		t.Fatalf("session_link not persisted: %+v", detail.Turns[1])
	}
}

func ptrInt(value int) *int { return &value }

func ptrTime(value time.Time) *time.Time { return &value }

func ptrString(value string) *string { return &value }
