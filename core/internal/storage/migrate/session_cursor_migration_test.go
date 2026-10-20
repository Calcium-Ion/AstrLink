package migrate

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSessionCursorOrderingBackfillsAndTracksRecordChanges(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "cursor-order.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrations := DefaultMigrations()
	index := -1
	for i, migration := range migrations {
		if migration.Name == "session_cursor_lookup_order" {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("missing session cursor migration")
	}
	before, err := New(SQLDatabase{DB: db}, migrations[:index])
	if err != nil {
		t.Fatal(err)
	}
	if err := before.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO request_records
(id, started_at, status, input_protocol, streaming, audit_json, created_at, session_id, local_access_token_id)
VALUES ('request_old', '2026-10-01T00:00:00Z', 'succeeded', 'openai.responses', 0, '{}', '2026-10-01T00:00:00Z', 'session_old', 'token_old')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO request_record_cursors(request_id, kind, direction, value)
VALUES ('request_old', 'explicit', 'in', 'old')`); err != nil {
		t.Fatal(err)
	}
	after, err := New(SQLDatabase{DB: db}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := after.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	check := func(value, wantStart, wantToken string) {
		t.Helper()
		var started, token string
		if err := db.QueryRow(`SELECT started_at, local_access_token_id FROM request_record_cursors WHERE value = ?`, value).Scan(&started, &token); err != nil {
			t.Fatal(err)
		}
		if started != wantStart || token != wantToken {
			t.Fatalf("cursor %q ordering = %q/%q, want %q/%q", value, started, token, wantStart, wantToken)
		}
	}
	check("old", "2026-10-01T00:00:00Z", "token_old")
	if _, err := db.Exec(`INSERT INTO request_record_cursors(request_id, kind, direction, value)
VALUES ('request_old', 'explicit', 'out', 'new')`); err != nil {
		t.Fatal(err)
	}
	check("new", "2026-10-01T00:00:00Z", "token_old")
	if _, err := db.Exec(`UPDATE request_records SET started_at = '2026-10-02T00:00:00Z', local_access_token_id = 'token_new' WHERE id = 'request_old'`); err != nil {
		t.Fatal(err)
	}
	check("old", "2026-10-02T00:00:00Z", "token_new")
	check("new", "2026-10-02T00:00:00Z", "token_new")
}
