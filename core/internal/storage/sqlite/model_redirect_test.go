package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/migrate"
)

func TestRequestModelRedirectRoundTripsThroughInsertUpsertGetAndList(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "redirect-records.db"))
	defer store.Close()
	ctx := context.Background()
	started := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	redirect := &contract.RequestModelRedirect{From: "gpt-4o", To: "gpt-4.1"}
	inserted := contract.RequestRecord{
		ID: "request_redirect_inserted", StartedAt: started, CompletedAt: ptrTime(started.Add(time.Second)),
		Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIChat,
		RequestedModel: ptrString("gpt-4o"), ModelRedirect: redirect, Audit: contract.NotCapturedAuditSummary(),
		Recovery: &contract.RequestRecovery{UpstreamModel: "gpt-4.1"},
	}
	if err := store.InsertRequestRecord(ctx, inserted); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRequestRecord(ctx, inserted.ID)
	if err != nil || !reflect.DeepEqual(got.ModelRedirect, redirect) || *got.RequestedModel != "gpt-4o" {
		t.Fatalf("inserted=%#v err=%v", got, err)
	}

	// A pending row without a redirect must pick one up when the final
	// snapshot replaces it, and a late pending snapshot must not erase it.
	pending := contract.RequestRecord{
		ID: "request_redirect_live", StartedAt: started.Add(time.Minute), Status: contract.RequestStatusPending,
		InputProtocol: contract.ProtocolOpenAIResponses, RequestedModel: ptrString("astrlink/auto"),
		Streaming: true, Audit: contract.NotCapturedAuditSummary(),
	}
	if err := store.UpsertRequestRecord(ctx, pending); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetRequestRecord(ctx, pending.ID)
	if err != nil || got.ModelRedirect != nil {
		t.Fatalf("pending=%#v err=%v", got, err)
	}
	liveRedirect := &contract.RequestModelRedirect{From: "astrlink/auto", To: "gpt-4.1-mini"}
	live := pending
	live.ModelRedirect = liveRedirect
	if err := store.UpsertRequestRecord(ctx, live); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetRequestRecord(ctx, pending.ID)
	if err != nil || !reflect.DeepEqual(got.ModelRedirect, liveRedirect) {
		t.Fatalf("pending with redirect=%#v err=%v", got, err)
	}
	status := http.StatusOK
	final := live
	final.Status = contract.RequestStatusSucceeded
	final.CompletedAt = ptrTime(started.Add(2 * time.Minute))
	final.HTTPStatus = &status
	if err := store.UpsertRequestRecord(ctx, final); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRequestRecord(ctx, pending); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetRequestRecord(ctx, pending.ID)
	if err != nil || got.Status != contract.RequestStatusSucceeded || !reflect.DeepEqual(got.ModelRedirect, liveRedirect) {
		t.Fatalf("final=%#v err=%v", got, err)
	}

	plain := contract.RequestRecord{
		ID: "request_plain", StartedAt: started.Add(2 * time.Minute), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIChat, RequestedModel: ptrString("gpt-4.1"), Audit: contract.NotCapturedAuditSummary(),
	}
	if err := store.InsertRequestRecord(ctx, plain); err != nil {
		t.Fatal(err)
	}
	var raw sql.NullString
	if err := store.db.QueryRowContext(ctx, `SELECT model_redirect_json FROM request_records WHERE id = ?`, string(plain.ID)).Scan(&raw); err != nil || raw.Valid {
		t.Fatalf("record without redirect stored %#v err=%v", raw, err)
	}
	page, err := store.ListRequestRecords(ctx, storagecontract.RequestRecordListOptions{Limit: 10})
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("list=%#v err=%v", page, err)
	}
	want := map[contract.RequestID]*contract.RequestModelRedirect{
		inserted.ID: redirect,
		pending.ID:  liveRedirect,
		plain.ID:    nil,
	}
	for _, item := range page.Items {
		if !reflect.DeepEqual(item.ModelRedirect, want[item.ID]) {
			t.Fatalf("list item %s redirect=%#v", item.ID, item.ModelRedirect)
		}
	}
}

func TestRequestSessionExposesLatestTurnModelRedirect(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "redirect-sessions.db"))
	defer store.Close()
	ctx := context.Background()
	started := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	sessionID := contract.SessionID("session_redirect")
	first := contract.RequestRecord{
		ID: "request_redirect_first", SessionID: &sessionID, StartedAt: started,
		CompletedAt: ptrTime(started.Add(time.Second)), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIChat, RequestedModel: ptrString("gpt-4o"),
		ModelRedirect: &contract.RequestModelRedirect{From: "gpt-4o", To: "gpt-4.1"},
		Audit:         contract.NotCapturedAuditSummary(),
	}
	latest := first
	latest.ID = "request_redirect_latest"
	latest.StartedAt = started.Add(time.Minute)
	latest.CompletedAt = ptrTime(started.Add(61 * time.Second))
	latest.RequestedModel = ptrString("claude-old")
	latest.ModelRedirect = &contract.RequestModelRedirect{From: "claude-old", To: "claude-new"}
	for _, record := range []contract.RequestRecord{first, latest} {
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.ListRequestSessions(ctx, storagecontract.RequestSessionListOptions{Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("sessions=%#v err=%v", page, err)
	}
	if !reflect.DeepEqual(page.Items[0].ModelRedirect, latest.ModelRedirect) || *page.Items[0].RequestedModel != "claude-old" {
		t.Fatalf("list session redirect=%#v model=%v", page.Items[0].ModelRedirect, page.Items[0].RequestedModel)
	}
	detail, err := store.GetRequestSession(ctx, string(sessionID))
	if err != nil || len(detail.Turns) != 2 || !reflect.DeepEqual(detail.ModelRedirect, latest.ModelRedirect) {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
	if !reflect.DeepEqual(detail.Turns[0].ModelRedirect, first.ModelRedirect) || !reflect.DeepEqual(detail.Turns[1].ModelRedirect, latest.ModelRedirect) {
		t.Fatalf("per-turn redirects lost: %#v", detail.Turns)
	}

	// A later call without a redirect clears the session badge rather than
	// inheriting an earlier call's redirect.
	next := latest
	next.ID = "request_redirect_none"
	next.StartedAt = started.Add(2 * time.Minute)
	next.CompletedAt = ptrTime(started.Add(121 * time.Second))
	next.RequestedModel = ptrString("claude-new")
	next.ModelRedirect = nil
	if err := store.InsertRequestRecord(ctx, next); err != nil {
		t.Fatal(err)
	}
	page, err = store.ListRequestSessions(ctx, storagecontract.RequestSessionListOptions{Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].ModelRedirect != nil {
		t.Fatalf("list after plain call=%#v err=%v", page, err)
	}
	detail, err = store.GetRequestSession(ctx, string(sessionID))
	if err != nil || detail.ModelRedirect != nil || len(detail.Turns) != 3 {
		t.Fatalf("detail after plain call=%#v err=%v", detail, err)
	}
}

func TestLegacyRequestRowsHaveNoModelRedirect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-redirect.db")
	database, err := sql.Open(driverName, sqliteFileDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	migrations := migrate.DefaultMigrations()
	var beforeRedirect []migrate.Migration
	for _, migration := range migrations {
		if migration.Name == "request_model_redirect" {
			break
		}
		beforeRedirect = append(beforeRedirect, migration)
	}
	if len(beforeRedirect) == len(migrations) {
		t.Fatal("request_model_redirect migration is missing")
	}
	runner, err := migrate.New(migrate.SQLDatabase{DB: database}, beforeRedirect)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	audit, err := json.Marshal(contract.NotCapturedAuditSummary())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO request_records (id, started_at, status, input_protocol, requested_model, streaming, audit_json, created_at)
VALUES ('request_legacy_model', '2026-09-19T00:00:00Z', 'succeeded', 'openai.chat', 'gpt-4o', 0, ?, '2026-09-19T00:00:00Z')`, string(audit)); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	store := openTestStore(t, path)
	defer store.Close()
	ctx := context.Background()
	got, err := store.GetRequestRecord(ctx, "request_legacy_model")
	if err != nil || got.ModelRedirect != nil || got.RequestedModel == nil || *got.RequestedModel != "gpt-4o" {
		t.Fatalf("legacy=%#v err=%v", got, err)
	}
	detail, err := store.GetRequestSession(ctx, "request_legacy_model")
	if err != nil || detail.ModelRedirect != nil {
		t.Fatalf("legacy session=%#v err=%v", detail, err)
	}

	// Persisted redirects are validated like every other record field.
	for _, document := range []string{`{"from":"gpt-4o"`, `{"from":"gpt-4o","to":"gpt-4o"}`, `{"from":"","to":"gpt-4.1"}`} {
		if _, err := store.db.ExecContext(ctx, `UPDATE request_records SET model_redirect_json = ? WHERE id = 'request_legacy_model'`, document); err != nil {
			t.Fatal(err)
		}
		if _, err := store.GetRequestRecord(ctx, "request_legacy_model"); !errors.Is(err, storagecontract.ErrInvalidRecord) {
			t.Fatalf("%s loaded with err=%v", document, err)
		}
	}
}
