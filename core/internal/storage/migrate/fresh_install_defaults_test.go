package migrate

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// migrateBeforeFreshInstallDefaults opens a database at the published schema
// just before fresh_install_defaults and returns it with the full list.
func migrateBeforeFreshInstallDefaults(t *testing.T) (*sql.DB, []Migration) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "fresh-install-defaults.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migrations := DefaultMigrations()
	index := -1
	for i, migration := range migrations {
		if migration.Name == "fresh_install_defaults" {
			index = i
		}
	}
	if index < 1 || migrations[index-1].Name != "request_output_activity" {
		t.Fatal("fresh_install_defaults must follow the published output activity migration")
	}
	before, err := New(SQLDatabase{DB: db}, migrations[:index])
	if err != nil {
		t.Fatal(err)
	}
	if err := before.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db, migrations
}

func migrateFully(t *testing.T, db *sql.DB, migrations []Migration) {
	t.Helper()
	after, err := New(SQLDatabase{DB: db}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := after.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapTokenSourceMarksOnlyTheTokenTheBootstrapIssued(t *testing.T) {
	db, migrations := migrateBeforeFreshInstallDefaults(t)
	// What a store before this fix wrote: the bootstrap token and its state
	// share one timestamp, and every token is stored as a user's.
	const issued = "2026-10-01T08:00:00.123456789Z"
	if _, err := db.Exec(`INSERT INTO local_access_token_bootstrap_state (singleton, completed_at) VALUES (1, ?)`, issued); err != nil {
		t.Fatal(err)
	}
	for _, token := range []struct{ id, name, createdAt string }{
		{"access_token_bootstrap", "默认令牌", issued},
		{"access_token_created", "laptop", "2026-10-02T09:00:00Z"},
	} {
		if _, err := db.Exec(`INSERT INTO local_access_tokens
(id, name, name_key, token_hash, token_hint, source, created_at)
VALUES (?, ?, ?, randomblob(32), 'astr_…AAAAAA', 'user', ?)`, token.id, token.name, token.name, token.createdAt); err != nil {
			t.Fatal(err)
		}
	}
	migrateFully(t, db, migrations)
	for id, want := range map[string]string{
		"access_token_bootstrap": "system_default",
		"access_token_created":   "user",
	} {
		var source string
		if err := db.QueryRow(`SELECT source FROM local_access_tokens WHERE id = ?`, id).Scan(&source); err != nil {
			t.Fatal(err)
		}
		if source != want {
			t.Errorf("%s source = %q, want %q", id, source, want)
		}
	}
}
