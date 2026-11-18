package realpath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvesLikeEvalSymlinks(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve(file); err != nil || got != want {
		t.Fatalf("Resolve = %q, %v, want %q", got, err, want)
	}
	if _, err := Resolve(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("Resolve accepted a missing path")
	}
}
