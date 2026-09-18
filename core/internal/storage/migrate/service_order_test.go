package migrate

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

func TestServiceOrderMigrationPreservesPoliciesAndInitializesExistingOrder(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrations := DefaultMigrations()
	old, _ := New(SQLDatabase{DB: db}, migrations[:26])
	if err := old.Up(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"service_z", "service_a", "service_m"} {
		if _, err := db.Exec(`INSERT INTO services(id, document_json, created_at, updated_at) VALUES (?, '{}', 'now', 'now')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE routing_settings SET document_json = json_set(document_json, '$.allow_unmatched_failover', json('true'), '$.max_attempts', 17, '$.default_failure_policy.max_retries', 4)`); err != nil {
		t.Fatal(err)
	}
	latest, _ := New(SQLDatabase{DB: db}, migrations)
	if err := latest.Up(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT id FROM services ORDER BY sort_position`)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []string{"service_a", "service_m", "service_z"}) {
		t.Fatal(ids)
	}
	var strategy string
	var enabled, attempts, retries int
	err = db.QueryRow(`SELECT json_extract(document_json, '$.strategy'), json_extract(document_json, '$.allow_unmatched_failover'), json_extract(document_json, '$.max_attempts'), json_extract(document_json, '$.default_failure_policy.max_retries') FROM routing_settings`).Scan(&strategy, &enabled, &attempts, &retries)
	if err != nil || strategy != "failover_only" || enabled != 1 || attempts != 17 || retries != 4 {
		t.Fatalf("settings changed: %s %d %d %d %v", strategy, enabled, attempts, retries, err)
	}
}
