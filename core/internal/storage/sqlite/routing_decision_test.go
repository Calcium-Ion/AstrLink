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

func TestRequestRoutingDecisionRoundTripsThroughInsertUpsertGetAndList(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "routing-decision-records.db"))
	defer store.Close()
	ctx := context.Background()
	started := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	decision := &contract.RequestRoutingDecision{
		Selected: contract.RoutingSelectionPriority,
		Skipped: []contract.RoutingSkip{
			{ServiceID: "mly", Reason: contract.RoutingSkipModelNotListed},
			{ServiceID: "newapi-codex", Reason: contract.RoutingSkipDisabled},
		},
	}
	serviceID := contract.ServiceID("newapi")
	inserted := contract.RequestRecord{
		ID: "request_decision_inserted", StartedAt: started, CompletedAt: ptrTime(started.Add(time.Second)),
		Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses,
		RequestedModel: ptrString("gpt-5.6-luna"), ServiceID: &serviceID, RoutingDecision: decision,
		Audit: contract.NotCapturedAuditSummary(),
	}
	if err := store.InsertRequestRecord(ctx, inserted); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRequestRecord(ctx, inserted.ID)
	if err != nil || !reflect.DeepEqual(got.RoutingDecision, decision) {
		t.Fatalf("inserted=%#v err=%v", got.RoutingDecision, err)
	}

	// The live row learns its decision once a provider is chosen, and a late
	// pending snapshot must not replace the final one.
	pending := contract.RequestRecord{
		ID: "request_decision_live", StartedAt: started.Add(time.Minute), Status: contract.RequestStatusPending,
		InputProtocol: contract.ProtocolOpenAIResponses, RequestedModel: ptrString("gpt-5.6-luna"),
		Streaming: true, Audit: contract.NotCapturedAuditSummary(),
	}
	if err := store.UpsertRequestRecord(ctx, pending); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetRequestRecord(ctx, pending.ID)
	if err != nil || got.RoutingDecision != nil {
		t.Fatalf("pending=%#v err=%v", got.RoutingDecision, err)
	}
	failover := &contract.RequestRoutingDecision{
		Selected: contract.RoutingSelectionFailover,
		Skipped:  []contract.RoutingSkip{{ServiceID: "mly", Reason: contract.RoutingSkipCircuitOpen}},
	}
	status := http.StatusOK
	final := pending
	final.Status = contract.RequestStatusSucceeded
	final.CompletedAt = ptrTime(started.Add(2 * time.Minute))
	final.HTTPStatus = &status
	final.ServiceID = &serviceID
	final.RoutingDecision = failover
	if err := store.UpsertRequestRecord(ctx, final); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRequestRecord(ctx, pending); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetRequestRecord(ctx, pending.ID)
	if err != nil || got.Status != contract.RequestStatusSucceeded || !reflect.DeepEqual(got.RoutingDecision, failover) {
		t.Fatalf("final=%#v err=%v", got.RoutingDecision, err)
	}

	// No provider was eligible: the decision has no selection and keeps an
	// empty skipped list as an array.
	none := &contract.RequestRoutingDecision{Skipped: []contract.RoutingSkip{}}
	failed := contract.RequestRecord{
		ID: "request_decision_none", StartedAt: started.Add(2 * time.Minute), CompletedAt: ptrTime(started.Add(3 * time.Minute)),
		Status: contract.RequestStatusFailed, InputProtocol: contract.ProtocolOpenAIChat, RequestedModel: ptrString("gpt-4.1"),
		RoutingDecision: none, Audit: contract.NotCapturedAuditSummary(),
		Error: &contract.ErrorSummary{Category: "routing", Code: "no_eligible_endpoint", Message: "no provider"},
	}
	if err := store.InsertRequestRecord(ctx, failed); err != nil {
		t.Fatal(err)
	}
	var raw sql.NullString
	if err := store.db.QueryRowContext(ctx, `SELECT routing_decision_json FROM request_records WHERE id = ?`, string(failed.ID)).Scan(&raw); err != nil || raw.String != `{"skipped":[]}` {
		t.Fatalf("record without selection stored %#v err=%v", raw, err)
	}

	plain := contract.RequestRecord{
		ID: "request_decision_plain", StartedAt: started.Add(3 * time.Minute), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIChat, RequestedModel: ptrString("gpt-4.1"), Audit: contract.NotCapturedAuditSummary(),
	}
	if err := store.InsertRequestRecord(ctx, plain); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT routing_decision_json FROM request_records WHERE id = ?`, string(plain.ID)).Scan(&raw); err != nil || raw.Valid {
		t.Fatalf("record without decision stored %#v err=%v", raw, err)
	}
	page, err := store.ListRequestRecords(ctx, storagecontract.RequestRecordListOptions{Limit: 10})
	if err != nil || len(page.Items) != 4 {
		t.Fatalf("list=%#v err=%v", page, err)
	}
	want := map[contract.RequestID]*contract.RequestRoutingDecision{
		inserted.ID: decision,
		pending.ID:  failover,
		failed.ID:   none,
		plain.ID:    nil,
	}
	for _, item := range page.Items {
		if !reflect.DeepEqual(item.RoutingDecision, want[item.ID]) {
			t.Fatalf("list item %s decision=%#v", item.ID, item.RoutingDecision)
		}
	}
}

func TestLegacyRequestRowsHaveNoRoutingDecision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-routing-decision.db")
	database, err := sql.Open(driverName, sqliteFileDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	migrations := migrate.DefaultMigrations()
	var beforeDecision []migrate.Migration
	for _, migration := range migrations {
		if migration.Name == "request_routing_decision" {
			break
		}
		beforeDecision = append(beforeDecision, migration)
	}
	if len(beforeDecision) == len(migrations) {
		t.Fatal("request_routing_decision migration is missing")
	}
	runner, err := migrate.New(migrate.SQLDatabase{DB: database}, beforeDecision)
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
VALUES ('request_legacy_routing', '2026-09-19T00:00:00Z', 'succeeded', 'openai.chat', 'gpt-4o', 0, ?, '2026-09-19T00:00:00Z')`, string(audit)); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	store := openTestStore(t, path)
	defer store.Close()
	ctx := context.Background()
	got, err := store.GetRequestRecord(ctx, "request_legacy_routing")
	if err != nil || got.RoutingDecision != nil {
		t.Fatalf("legacy=%#v err=%v", got, err)
	}

	// Persisted decisions are validated like every other record field.
	for _, document := range []string{
		`{"selected":"priority"`,
		`{"selected":"lucky","skipped":[]}`,
		`{"selected":"priority","skipped":[{"service_id":"mly","reason":"tired"}]}`,
		`{"selected":"priority","skipped":[{"service_id":"","reason":"disabled"}]}`,
	} {
		if _, err := store.db.ExecContext(ctx, `UPDATE request_records SET routing_decision_json = ? WHERE id = 'request_legacy_routing'`, document); err != nil {
			t.Fatal(err)
		}
		if _, err := store.GetRequestRecord(ctx, "request_legacy_routing"); !errors.Is(err, storagecontract.ErrInvalidRecord) {
			t.Fatalf("%s loaded with err=%v", document, err)
		}
	}
}
