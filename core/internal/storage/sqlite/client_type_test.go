package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/migrate"
)

func TestClientTypeRecordAndSessionRoundTrip(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "client.db"))
	defer store.Close()
	ctx := context.Background()
	sessionID := contract.SessionID("session_client")
	first := contract.RequestRecord{
		ID: "request_client_first", SessionID: &sessionID,
		StartedAt: time.Now().UTC(), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, ClientType: contract.ClientCodex,
	}
	if err := store.InsertRequestRecord(ctx, first); err != nil {
		t.Fatal(err)
	}
	latest := first
	latest.ID = "request_client_latest"
	latest.StartedAt = first.StartedAt.Add(time.Second)
	latest.Status = contract.RequestStatusPending
	latest.ClientType = contract.ClientPi
	if err := store.UpsertRequestRecord(ctx, latest); err != nil {
		t.Fatal(err)
	}
	latest.Status = contract.RequestStatusSucceeded
	if err := store.UpsertRequestRecord(ctx, latest); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListRequestRecords(ctx, storagecontract.RequestRecordListOptions{})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("records = %#v, %v", page, err)
	}
	if page.Items[0].ClientType != contract.ClientPi || page.Items[1].ClientType != contract.ClientCodex {
		t.Fatalf("record clients = %#v", page.Items)
	}
	sessions, err := store.ListRequestSessions(ctx, storagecontract.RequestSessionListOptions{})
	if err != nil || len(sessions.Items) != 1 || sessions.Items[0].ClientType != contract.ClientPi {
		t.Fatalf("session summary = %#v, %v", sessions, err)
	}
	detail, err := store.GetRequestSession(ctx, string(sessionID))
	if err != nil || detail.ClientType != contract.ClientPi || len(detail.Turns) != 2 || detail.Turns[0].ClientType != contract.ClientCodex {
		t.Fatalf("session detail = %#v, %v", detail, err)
	}
}

func TestClientTypeHistoricalRowsAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.db")
	ctx := context.Background()
	database, err := sql.Open(driverName, sqliteFileDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var previous []migrate.Migration
	for _, migration := range migrate.DefaultMigrations() {
		if migration.Name == "request_client_type" {
			break
		}
		previous = append(previous, migration)
	}
	runner, err := migrate.New(migrate.SQLDatabase{DB: database}, previous)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	// Upgrade an existing database containing a record without client metadata.
	_, err = database.Exec(`INSERT INTO request_records (id, started_at, status, input_protocol, streaming, audit_json, created_at)
VALUES ('request_legacy_client', '2026-09-25T00:00:00Z', 'succeeded', 'openai.responses', 0, '{}', '2026-09-25T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	store := openTestStore(t, path)
	defer store.Close()
	record, err := store.GetRequestRecord(ctx, "request_legacy_client")
	if err != nil || record.ClientType != "" {
		t.Fatalf("legacy = %#v, %v", record, err)
	}
	record.ClientType = "made_up"
	if err := store.UpsertRequestRecord(ctx, record); err == nil {
		t.Fatal("invalid client accepted")
	}
	record.ClientType = contract.ClientUnknown
	if err := store.UpsertRequestRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
}
