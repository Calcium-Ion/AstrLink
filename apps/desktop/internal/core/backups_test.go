package core

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

func TestReportsBackupFilesWithoutTouchingThem(t *testing.T) {
	directory := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("astrlink.db", "live")
	write("astrlink.db-wal", "wal")
	write("desktop-preferences.json", "{}")
	write("astrlink.db.bak-20260924-migration-34-repair", "0123456789")
	write("notes.BAK", "xy")
	if err := os.Mkdir(filepath.Join(directory, "old.bak.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(directory, "astrlink.db"), filepath.Join(directory, "link.db.bak")); err != nil {
			t.Fatal(err)
		}
	}

	files := ScanBackupFiles(directory)
	var names []string
	for _, file := range files {
		names = append(names, file.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"astrlink.db.bak-20260924-migration-34-repair", "notes.BAK"}) {
		t.Fatalf("names = %v", names)
	}
	for _, file := range files {
		if file.Name == "astrlink.db.bak-20260924-migration-34-repair" && (file.SizeBytes != 10 || file.ModifiedUnix == nil) {
			t.Fatalf("repair copy = %+v", file)
		}
	}
	if _, err := os.Stat(filepath.Join(directory, "astrlink.db.bak-20260924-migration-34-repair")); err != nil {
		t.Fatal(err)
	}
}

func TestBackupsAreNewestFirstAndBounded(t *testing.T) {
	directory := t.TempDir()
	for i := range maxReportedBackups + 5 {
		path := filepath.Join(directory, "copy.bak"+string(rune('a'+i%26))+string(rune('a'+i/26)))
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	newest := filepath.Join(directory, "zz.bak")
	if err := os.WriteFile(newest, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	future := mustTime(t, "2030-01-01T00:00:00Z")
	if err := os.Chtimes(newest, future, future); err != nil {
		t.Fatal(err)
	}
	files := ScanBackupFiles(directory)
	if len(files) != maxReportedBackups || files[0].Name != "zz.bak" {
		t.Fatalf("got %d files, first %q", len(files), files[0].Name)
	}
}

func TestAMissingDirectoryReportsNothing(t *testing.T) {
	if files := ScanBackupFiles(filepath.Join(t.TempDir(), "absent")); files == nil || len(files) != 0 {
		t.Fatalf("files = %v", files)
	}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
