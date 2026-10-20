package migrate

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestAccessTokenSyncUpgradesPublishedSchema(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "token-sync-upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrations := DefaultMigrations()
	index := -1
	for i, migration := range migrations {
		if migration.Name == "access_token_sync" {
			index = i
		}
	}
	if index < 1 || migrations[index-1].Name != "session_cursor_lookup_order" {
		t.Fatal("token sync must follow the published cursor migration")
	}
	before, err := New(SQLDatabase{DB: db}, migrations[:index])
	if err != nil {
		t.Fatal(err)
	}
	if err := before.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	const config = `{"enabled":true,"scope":{"services":true,"credentials":true,"policies":true,"pricing":true}}`
	if _, err := db.Exec(`INSERT INTO sync_state (id, device_id, config_json) VALUES (1, 'device_existing', ?)`, config); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO local_access_tokens
(id, name, name_key, token_hash, token_hint, source, created_at)
VALUES ('access_token_existing', 'existing', 'EXISTING', zeroblob(32), 'astr_…AAAAAA', 'user', '2026-10-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	after, err := New(SQLDatabase{DB: db}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := after.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	var storedConfig string
	var generation int64
	if err := db.QueryRow(`SELECT config_json, force_push_generation FROM sync_state WHERE id = 1`).Scan(&storedConfig, &generation); err != nil {
		t.Fatal(err)
	}
	if storedConfig != config || generation != 0 {
		t.Fatal("migration modified existing configuration or armed a force push")
	}
	if _, err := db.Exec(`UPDATE local_access_tokens SET name = 'renamed', name_key = 'RENAMED' WHERE id = 'access_token_existing'`); err != nil {
		t.Fatal(err)
	}
	var changes int
	if err := db.QueryRow(`SELECT COUNT(*) FROM config_change_log WHERE entity_kind = 'access_token' AND entity_id = 'access_token_existing'`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if changes != 1 {
		t.Fatalf("existing token update produced %d changes, want 1", changes)
	}
}
