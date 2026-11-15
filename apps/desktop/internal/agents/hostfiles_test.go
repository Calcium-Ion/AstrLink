package agents

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestASecretFileIsOwnerOnlyEvenWhenItExisted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix modes")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteText(path, "a = 2\n", WriteOptions{Secret: true}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	data, _ := os.ReadFile(path)
	if info.Mode().Perm() != 0o600 || string(data) != "a = 2\n" {
		t.Fatalf("%v %q", info.Mode(), data)
	}
	// Without Secret, a file keeps its mode.
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteText(path, "a = 3\n", WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Fatalf("%v", info.Mode())
	}
}

func TestAFileThatChangedAfterReadingIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("{\"client\": true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := WriteText(path, "{}\n", WriteOptions{Expected: ExpectText("{}\n", true)})
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("%v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "{\"client\": true}\n" {
		t.Fatalf("%q", data)
	}
	missing := filepath.Join(dir, "missing.json")
	if err := WriteText(missing, "{}\n", WriteOptions{Expected: ExpectText("{}\n", true)}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Fatal("wrote the missing file")
	}
	if err := WriteText(path, "{}\n", WriteOptions{Expected: ExpectText("{\"client\": true}\n", true)}); err != nil {
		t.Fatal(err)
	}
	if err := WriteText(missing, "x", WriteOptions{Expected: ExpectText("", false)}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("left %s", entry.Name())
		}
	}
}

func TestASymlinkedFileIsReplacedAtItsTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.json")
	link := filepath.Join(dir, "link.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	if err := WriteText(link, "[]", WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced")
	}
	if data, _ := os.ReadFile(target); string(data) != "[]" {
		t.Fatalf("%q", data)
	}
}

func TestRemovePathTakesFilesAndDirectories(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a")
	sub := filepath.Join(dir, "b", "c")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, filepath.Join(dir, "b"), filepath.Join(dir, "missing")} {
		if err := RemovePath(path); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(path); err == nil {
			t.Fatalf("%s is left", path)
		}
	}
	if _, _, err := ReadOptional(file); err != nil {
		t.Fatal(err)
	}
}
