package migrate

import (
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
	if err := old.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO request_records (id, started_at, status, input_protocol, streaming, audit_json, created_at) VALUES ('request_old', '2026-09-19T00:00:00Z', 'succeeded', 'openai.responses', 0, '{}', '2026-09-19T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	latest, _ := New(SQLDatabase{DB: db}, all)
	if err := latest.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM request_records`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	var billingTokenColumnCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('billing_ledger') WHERE name = 'local_access_token_id'`).Scan(&billingTokenColumnCount); err != nil || billingTokenColumnCount != 1 {
		t.Fatalf("billing token column count=%d err=%v", billingTokenColumnCount, err)
	}
	// SQLite may prefer the time index on a tiny database to avoid a sort.
	// Give the planner representative cardinality: a two-token filter selects
	// 2% of the roots. Do not force INDEXED BY or forbid a cross-token sort.
	if _, err := db.Exec(`WITH RECURSIVE records(n) AS (
    VALUES(0) UNION ALL SELECT n+1 FROM records WHERE n<9999
)
INSERT INTO request_records (id, started_at, status, input_protocol, streaming, audit_json, created_at, local_access_token_id)
SELECT printf('request_plan_%05d',n), '2026-09-19T00:00:00Z', 'succeeded', 'openai.responses', 0, '{}', '2026-09-19T00:00:00Z', printf('token_%02d',n%100)
FROM records`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ANALYZE`); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		query          string
		index          string
		allowTempBTree bool
	}{
		{query: `SELECT id FROM request_records WHERE parent_request_id IS NULL ORDER BY started_at DESC, id DESC LIMIT 51`, index: "request_records_root_started_idx"},
		{query: `SELECT id FROM request_records WHERE parent_request_id IS NULL AND local_access_token_id = 'token_missing' ORDER BY started_at DESC, id DESC`, index: "request_records_root_token_time_idx"},
		// IN scans multiple token ranges; merging their time order may use a temp B-tree.
		{query: `SELECT id FROM request_records WHERE parent_request_id IS NULL AND local_access_token_id IN ('token_01', 'token_02') ORDER BY started_at DESC, id DESC`, index: "request_records_root_token_time_idx", allowTempBTree: true},
		{query: `SELECT id FROM request_records WHERE parent_request_id IS NULL AND local_access_token_id IN ('token_01', 'token_02') AND started_at >= '2026-09-01T00:00:00Z' AND started_at < '2026-10-01T00:00:00Z' ORDER BY started_at DESC, id DESC LIMIT 51`, index: "request_records_root_token_time_idx", allowTempBTree: true},
		{query: `SELECT id FROM request_records WHERE parent_request_id IS NULL AND COALESCE(session_id, id) = 'session_missing' ORDER BY started_at, id`, index: "request_records_session_turns_idx"},
		{query: `SELECT id FROM request_records WHERE parent_request_id IS NULL AND previous_response_id IN ('missing') AND session_id IS NOT NULL`, index: "request_records_previous_response_idx"},
		{query: `SELECT id FROM request_records WHERE parent_request_id IS NULL AND output_response_id IN ('missing') AND session_id IS NOT NULL`, index: "request_records_output_response_id_idx"},
		{query: `SELECT rowid FROM response_affinities ORDER BY created_at DESC, rowid DESC LIMIT -1 OFFSET 10000`, index: "response_affinities_created_idx"},
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
		if !strings.Contains(plan.String(), test.index) || (!test.allowTempBTree && strings.Contains(plan.String(), "TEMP B-TREE")) {
			t.Fatalf("query=%s plan=%s", test.query, plan.String())
		}
	}
}
