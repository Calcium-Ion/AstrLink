package realpath

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// junction links link to the directory target as mklink /J does.
func junction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s", link, target, err, out)
	}
}

// The standalone Codex installer reaches codex.exe through two junctions:
// Programs\OpenAI\Codex\bin -> packages\standalone\current\bin and
// current -> releases\<version>.
func TestResolvesThroughTwoJunctions(t *testing.T) {
	root := t.TempDir()
	release := filepath.Join(root, "releases", "0.169.0-x86_64-pc-windows-msvc")
	if err := os.MkdirAll(filepath.Join(release, "bin"), 0o777); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(release, "bin", "codex.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "packages", "standalone"), 0o777); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, "packages", "standalone", "current")
	junction(t, current, release)
	if err := os.MkdirAll(filepath.Join(root, "Programs", "Codex"), 0o777); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "Programs", "Codex", "bin")
	junction(t, bin, filepath.Join(current, "bin"))

	want, ok := finalPath(exe)
	if !ok {
		t.Fatal("unable to resolve the release itself")
	}
	got, err := Resolve(filepath.Join(bin, "codex.exe"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.EqualFold(got, want) {
		t.Fatalf("Resolve = %q, want %q", got, want)
	}
	if strings.HasPrefix(got, `\\?\`) {
		t.Fatalf("Resolve kept the verbatim prefix: %q", got)
	}
	if dir, err := Resolve(bin); err != nil || !strings.EqualFold(dir, filepath.Dir(want)) {
		t.Fatalf("Resolve(bin) = %q, %v, want %q", dir, err, filepath.Dir(want))
	}
}

func TestTrimsTheVerbatimPrefix(t *testing.T) {
	for in, want := range map[string]string{
		`\\?\C:\Users\x`:         `C:\Users\x`,
		`\\?\UNC\server\share\x`: `\\server\share\x`,
		`C:\already`:             `C:\already`,
	} {
		if got := trimVerbatim(in); got != want {
			t.Errorf("trimVerbatim(%q) = %q, want %q", in, got, want)
		}
	}
}
