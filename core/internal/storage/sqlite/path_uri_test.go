package sqlite

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSQLiteURIEscapesSpecialPathsForReadWriteAndReadOnly(t *testing.T) {
	root := t.TempDir()
	names := []string{"capture#one", "capture#two", "capture%one", "capture%23one"}
	if runtime.GOOS != "windows" {
		// Question marks are valid Unix filenames but forbidden by Windows.
		names = append(names, "capture?one", "capture?two")
	}
	key := make([]byte, 32)
	for i, name := range names {
		path := filepath.Join(root, name, "path-isolation.db")
		store, err := Open(t.Context(), path, WithLocalKey(key))
		if err != nil {
			t.Fatalf("open %q: %v", name, err)
		}
		if _, err := store.db.ExecContext(t.Context(), `CREATE TABLE path_probe (value INTEGER)`); err != nil {
			store.Close()
			t.Fatal(err)
		}
		if _, err := store.db.ExecContext(t.Context(), `INSERT INTO path_probe(value) VALUES (?)`, i); err != nil {
			store.Close()
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			t.Fatalf("database was not written to the exact special path %q: %v", path, err)
		}
	}
	for i, name := range names {
		path := filepath.Join(root, name, "path-isolation.db")
		store, err := Open(t.Context(), path, WithLocalKey(key), WithReadOnly())
		if err != nil {
			t.Fatalf("reopen read-only %q: %v", name, err)
		}
		var got int
		err = store.db.QueryRowContext(t.Context(), `SELECT value FROM path_probe`).Scan(&got)
		store.Close()
		if err != nil || got != i {
			t.Fatalf("path %q read another path's data: got %d want %d err=%v", name, got, i, err)
		}
	}
}
