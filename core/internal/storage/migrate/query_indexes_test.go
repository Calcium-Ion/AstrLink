package migrate

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestQueryIndexesUpgradeAndAvoidHistoryScans(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	all := DefaultMigrations()
	old, _ := New(SQLDatabase{DB: db}, all[:27])
	if err := old.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO request_records (id, started_at, status, input_protocol, streaming, audit_json, created_at) VALUES ('request_old', '2026-09-19T00:00:00Z', 'succeeded', 'openai.responses', 0, '{}', '2026-09-19T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	latest, _ := New(SQLDatabase{DB: db}, all)
	if err := latest.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM request_records`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	for _, test := range []struct{ query, index string }{
		{`SELECT id FROM request_records WHERE parent_request_id IS NULL ORDER BY started_at DESC, id DESC LIMIT 51`, "request_records_root_started_idx"},
		{`SELECT id FROM request_records WHERE parent_request_id IS NULL AND COALESCE(session_id, id) = 'session_missing' ORDER BY started_at, id`, "request_records_session_turns_idx"},
		{`SELECT id FROM request_records WHERE parent_request_id IS NULL AND previous_response_id IN ('missing') AND session_id IS NOT NULL`, "request_records_previous_response_idx"},
		{`SELECT id FROM request_records WHERE parent_request_id IS NULL AND output_response_id IN ('missing') AND session_id IS NOT NULL`, "request_records_output_response_id_idx"},
		{`SELECT rowid FROM response_affinities ORDER BY created_at DESC, rowid DESC LIMIT -1 OFFSET 10000`, "response_affinities_created_idx"},
	} {
		rows, err := db.Query("EXPLAIN QUERY PLAN " + test.query)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if !strings.Contains(plan.String(), test.index) || strings.Contains(plan.String(), "TEMP B-TREE") {
			t.Fatalf("query=%s plan=%s", test.query, plan.String())
		}
	}
}
