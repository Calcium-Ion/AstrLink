package migrate

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// A database an early test build of the plugins ran on has its two tables
// without a migration record; the plugin migration replaces them.
func TestThePluginMigrationReplacesTablesAnEarlyBuildLeft(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "plugin-leftovers.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrations := DefaultMigrations()
	index := -1
	for i, migration := range migrations {
		if migration.Name == "plugins_request_metadata_extension_points_and_tasks" {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("no plugin migration")
	}
	before, err := New(SQLDatabase{DB: db}, migrations[:index])
	if err != nil {
		t.Fatal(err)
	}
	if err := before.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE plugin_settings (plugin_key TEXT PRIMARY KEY, enabled INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE plugin_credentials (id TEXT PRIMARY KEY, plugin_key TEXT NOT NULL REFERENCES plugin_settings(plugin_key), credential_value BLOB)`,
		`INSERT INTO plugin_settings (plugin_key, enabled) VALUES ('old', 1)`,
		`INSERT INTO plugin_credentials (id, plugin_key, credential_value) VALUES ('c1', 'old', x'00')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	after, err := New(SQLDatabase{DB: db}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := after.Up(t.Context()); err != nil {
		t.Fatalf("the plugin migration over an early build's tables: %v", err)
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM plugin_settings`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("plugin_settings = %d rows, %v", rows, err)
	}
	var config string
	if err := db.QueryRow(`SELECT name FROM pragma_table_info('plugin_settings') WHERE name = 'config_json'`).Scan(&config); err != nil {
		t.Fatalf("plugin_settings is not the migration's: %v", err)
	}
}
