package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestSessionLinkOrderedBranchesKeepNextEligibleRoot(t *testing.T) {
	store, _ := writerTestStore(t)
	start := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	token := contract.AccessTokenID("token_match")
	other := contract.AccessTokenID("token_other")
	session := contract.SessionID("session_match")
	root := contract.RequestRecord{
		ID: "request_root", StartedAt: start, Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
		SessionID: &session, LocalAccessTokenID: &token,
		Cursors: []contract.SessionCursor{
			{Kind: contract.SessionCursorEchoID, Direction: contract.SessionCursorOut, Value: "shared"},
			{Kind: contract.SessionCursorExplicit, Direction: contract.SessionCursorOut, Value: "shared"},
		},
	}
	if err := store.InsertRequestRecord(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		newer := root
		newer.ID = contract.RequestID(fmt.Sprintf("request_new_%02d", i))
		newer.StartedAt = start.Add(time.Duration(i+1) * time.Minute)
		switch i % 3 {
		case 0:
			newer.LocalAccessTokenID = &other
		case 1:
			newer.ParentRequestID = &root.ID
			newer.AttemptIndex = 1
		case 2:
			newer.SessionID = nil
		}
		if err := store.InsertRequestRecord(t.Context(), newer); err != nil {
			t.Fatal(err)
		}
	}
	scope := storagecontract.SessionCursorScope{SamePrincipal: true, LocalAccessTokenID: &token}
	match, found, err := store.FindSessionLink(t.Context(), contract.SessionCursorEchoID, []string{"absent", "shared"}, scope)
	if err != nil || !found || match.SessionID != session {
		t.Fatalf("next eligible root = %+v/%v/%v", match, found, err)
	}
	scope.NotBefore = start.Add(time.Second)
	if _, found, err := store.FindSessionLink(t.Context(), contract.SessionCursorEchoID, []string{"shared"}, scope); err != nil || found {
		t.Fatalf("expired eligible root returned: found=%v err=%v", found, err)
	}
	// A NULL principal is an explicit scope, not an unrestricted lookup.
	scope.LocalAccessTokenID = nil
	scope.NotBefore = time.Time{}
	if _, found, err := store.FindSessionLink(t.Context(), contract.SessionCursorEchoID, []string{"shared"}, scope); err != nil || found {
		t.Fatalf("NULL principal matched another token: found=%v err=%v", found, err)
	}
	// Exercise SQLite's compound-query/parameter limits at the public input
	// bound, with the only eligible value in the last branch.
	values := make([]string, maxSessionLinkValues)
	for i := range values {
		values[i] = fmt.Sprintf("missing_%d", i)
	}
	values[len(values)-1] = "shared"
	scope.LocalAccessTokenID = &token
	scope.NotBefore = start
	if match, found, err := store.FindSessionLink(t.Context(), contract.SessionCursorExplicit, values, scope); err != nil || !found || match.SessionID != session {
		t.Fatalf("maximum input lookup = %+v/%v/%v", match, found, err)
	}
}

func TestSessionLinkOnlySortsBoundedBranchResults(t *testing.T) {
	store, _ := writerTestStore(t)
	token := contract.AccessTokenID("token_plan")
	for _, scope := range []storagecontract.SessionCursorScope{{}, {SamePrincipal: true, LocalAccessTokenID: &token, NotBefore: time.Now().UTC()}} {
		query, args := sessionLinkQuery(contract.SessionCursorExplicit, []string{"same"}, scope)
		rows, err := store.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(plan, "\n")
		if strings.Count(joined, "USE TEMP B-TREE FOR ORDER BY") != 1 {
			t.Fatalf("a branch sorts unbounded history instead of seeking its newest root:\n%s", joined)
		}
		index := "request_record_cursors_latest_idx"
		if scope.SamePrincipal {
			index = "request_record_cursors_principal_latest_idx"
		}
		legacy := []string{"request_records_output_latest_idx", "request_records_previous_latest_idx"}
		if scope.SamePrincipal {
			legacy = []string{"request_records_output_principal_latest_idx", "request_records_previous_principal_latest_idx"}
		}
		for _, expected := range append(legacy, index) {
			if !strings.Contains(joined, "INDEX "+expected+" ") {
				t.Fatalf("missing ordered lookup through %s:\n%s", expected, joined)
			}
		}
	}
}

func BenchmarkSessionLinkRepeatedCursor(b *testing.B) {
	for _, count := range []int{100, 1000, 10000, 50000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			store, err := Open(b.Context(), filepath.Join(b.TempDir(), "session-lookup.db"), WithLocalKey(make([]byte, 32)))
			if err != nil {
				b.Fatal(err)
			}
			defer store.Close()
			tx, err := store.db.BeginTx(b.Context(), nil)
			if err != nil {
				b.Fatal(err)
			}
			defer tx.Rollback()
			record, err := tx.Prepare(`INSERT INTO request_records
(id, started_at, status, input_protocol, streaming, audit_json, created_at, session_id, previous_response_id)
VALUES (?, ?, 'succeeded', 'openai.responses', 0, '{}', ?, 'session_bench', 'same')`)
			if err != nil {
				b.Fatal(err)
			}
			defer record.Close()
			cursor, err := tx.Prepare(`INSERT INTO request_record_cursors(request_id, kind, direction, value, started_at)
VALUES (?, 'explicit', 'in', 'same', ?)`)
			if err != nil {
				b.Fatal(err)
			}
			defer cursor.Close()
			for i := range count {
				id := fmt.Sprintf("request_%08d", i)
				stamp := time.Unix(int64(i), 0).UTC().Format(time.RFC3339Nano)
				if _, err := record.Exec(id, stamp, stamp); err != nil {
					b.Fatal(err)
				}
				if _, err := cursor.Exec(id, stamp); err != nil {
					b.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_, found, err := store.FindSessionLink(context.Background(), contract.SessionCursorExplicit, []string{"same"}, storagecontract.SessionCursorScope{})
				if err != nil || !found {
					b.Fatalf("found=%v err=%v", found, err)
				}
			}
			b.StopTimer()
		})
	}
}
