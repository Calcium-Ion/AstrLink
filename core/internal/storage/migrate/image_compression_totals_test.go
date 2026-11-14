package migrate

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// An upgrade starts the image compression totals at zero from the upgrade
// itself: nothing is made up for the requests before it.
func TestImageCompressionTotalsStartAtTheUpgrade(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "image-totals-upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrations := DefaultMigrations()
	index := -1
	for i, migration := range migrations {
		if migration.Name == "image_compression_totals" {
			index = i
		}
	}
	if index < 1 || migrations[index-1].Name != "access_token_service_scope" {
		t.Fatal("image compression totals must follow the published token scope migration")
	}
	before, err := New(SQLDatabase{DB: db}, migrations[:index])
	if err != nil {
		t.Fatal(err)
	}
	if err := before.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	upgraded := time.Now().UTC().Truncate(time.Second)
	after, err := New(SQLDatabase{DB: db}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := after.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	var since string
	var requests, images, compressed, original, sent int64
	if err := db.QueryRow(`SELECT since, requests, images, compressed, original_bytes, sent_bytes
FROM image_compression_totals WHERE id = 1`).Scan(&since, &requests, &images, &compressed, &original, &sent); err != nil {
		t.Fatal(err)
	}
	started, err := time.Parse(time.RFC3339, since)
	if err != nil || started.Before(upgraded) || started.After(time.Now().UTC()) {
		t.Fatalf("since = %q, upgrade at %s", since, upgraded)
	}
	if requests != 0 || images != 0 || compressed != 0 || original != 0 || sent != 0 {
		t.Fatalf("totals did not start at zero: %d %d %d %d %d", requests, images, compressed, original, sent)
	}
	if _, err := db.Exec(`INSERT INTO image_compression_totals (id, since) VALUES (2, ?)`, since); err == nil {
		t.Fatal("a second totals row was accepted")
	}
}
