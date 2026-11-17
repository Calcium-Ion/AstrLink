package clientupdates

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestRecognizesNpmRetiredNames(t *testing.T) {
	if !npmRetired(".claude-code-1iARp2zF", "claude-code") || !npmRetired(".claude.cmd-DH0gY7Mn", "claude.cmd") {
		t.Fatal("a retired name was not recognized")
	}
	for _, other := range []string{
		"claude-code",
		".claude-code-1iARp2z",
		".claude-code-1iARp2zF9",
		".claude.cmd-DH0gY7Mn",
		".claude-code-1iAR+2zF",
	} {
		if npmRetired(other, "claude-code") {
			t.Fatal(other)
		}
	}
	if npmRetired(".claude.cmd-DH0gY7Mn", "claude") {
		t.Fatal("another name's copy was taken")
	}
}

func TestMatchesFilesAndDirectoriesLikeWindowsPaths(t *testing.T) {
	roots := newPathRoots([]string{
		`C:\Users\A\AppData\Roaming\npm\node_modules\@anthropic-ai\claude-code`,
		`C:\Users\A\.local\bin\claude.exe`,
	})
	for _, inside := range []string{
		`C:\Users\A\AppData\Roaming\npm\node_modules\@anthropic-ai\claude-code\bin\claude.exe`,
		`c:\users\a\appdata\roaming\npm\NODE_MODULES\@anthropic-ai\claude-code`,
		"C:/Users/A/.local/bin/claude.exe",
	} {
		if !roots.contains(inside) {
			t.Fatal(inside)
		}
	}
	for _, outside := range []string{
		`C:\Users\A\AppData\Roaming\npm\node_modules\@anthropic-ai\claude-code-old\claude.exe`,
		`C:\Users\A\AppData\Roaming\npm\node.exe`,
		`C:\Users\A\.local\bin\claude.exe.old`,
	} {
		if roots.contains(outside) {
			t.Fatal(outside)
		}
	}
}

func TestReportsAResetDownloadBehindAnEPERMCleanupAsANetworkError(t *testing.T) {
	// npm on Windows cannot clean up a half-extracted package that
	// antivirus holds; the cause is the reset download.
	failure := newError("command", "npm warn cleanup [Error: EPERM: operation not permitted, rmdir 'C:\\stage\\node_modules']\n"+
		"         npm error code ECONNRESET\nnpm error network aborted")
	if failure.code != "network" {
		t.Fatal(failure.code)
	}
}

// tree lists the files below root as "path=contents", sorted.
func tree(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		contents, _ := os.ReadFile(path)
		found = append(found, filepath.ToSlash(rel)+"="+string(contents))
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	slices.Sort(found)
	return found
}

// swapped is a prefix with an old client, a staged new one swapped in,
// and the journal of that swap, as AstrLink leaves it when it stops right
// after.
type swapped struct {
	t            *testing.T
	root, prefix string
	work, runDir string
	journal      journal
}

func newSwapped(t *testing.T) *swapped {
	t.Helper()
	root := t.TempDir()
	s := &swapped{t: t, root: root, prefix: filepath.Join(root, "prefix")}
	s.work = workDir(s.prefix, ClientClaude)
	s.runDir = filepath.Join(s.work, "1")
	write := func(path, contents string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(s.prefix, "pkg", "version"), "old")
	write(filepath.Join(s.prefix, "claude"), "old shim")
	write(filepath.Join(s.runDir, "stage", "pkg", "version"), "new")
	write(filepath.Join(s.runDir, "stage", "claude"), "new shim")
	s.journal = journal{
		Run: s.runDir,
		Retired: []move{
			{filepath.Join(s.prefix, "pkg"), filepath.Join(s.runDir, "previous", "package-0")},
			{filepath.Join(s.prefix, "claude"), filepath.Join(s.runDir, "previous", "claude")},
		},
		Placed: []move{
			{filepath.Join(s.runDir, "stage", "pkg"), filepath.Join(s.prefix, "pkg")},
			{filepath.Join(s.runDir, "stage", "claude"), filepath.Join(s.prefix, "claude")},
		},
	}
	return s
}

// apply begins and applies the swap.
func (s *swapped) apply() *swapped {
	s.t.Helper()
	sw, failure := beginSwap(s.work, s.journal)
	if failure != nil {
		s.t.Fatal(failure)
	}
	if err := sw.apply(); err != nil {
		s.t.Fatal(err)
	}
	return s
}

func (s *swapped) live() []string {
	s.t.Helper()
	var live []string
	for _, entry := range tree(s.t, s.prefix) {
		if !strings.HasPrefix(entry, stagedWork) {
			live = append(live, entry)
		}
	}
	return live
}

// recoverAll is what a check runs first. Windows finds the prefix on PATH
// itself, other systems its bin folder.
func (s *swapped) recoverAll() {
	s.t.Helper()
	bins := s.prefix
	if runtime.GOOS != "windows" {
		bins = filepath.Join(s.prefix, "bin")
	}
	if err := os.MkdirAll(bins, 0o755); err != nil {
		s.t.Fatal(err)
	}
	recoverAll(&Environment{home: s.root, path: bins}, ClientClaude)
	if runtime.GOOS != "windows" {
		_ = os.Remove(bins)
	}
}

var (
	oldFiles = []string{"claude=old shim", "pkg/version=old"}
	newFiles = []string{"claude=new shim", "pkg/version=new"}
)

func TestLeavesASwapAloneWhileAnotherUpdateHoldsTheClient(t *testing.T) {
	s := newSwapped(t).apply()
	other, failure := holdClaim(s.work)
	if failure != nil {
		t.Fatal(failure)
	}

	// A check, an update or a repair meanwhile changes nothing.
	s.recoverAll()
	if got := s.live(); !slices.Equal(got, newFiles) {
		t.Fatal(got)
	}
	if !exists(filepath.Join(s.work, stagedJournal)) || !exists(s.runDir) {
		t.Fatal("the swap's journal or run went while another update held it")
	}
	if _, failure := holdClaim(s.work); failure == nil || failure.code != "busy" {
		t.Fatal(failure)
	}

	// Once that update ends, however it ends, the next check undoes the
	// swap and removes what it left.
	other.release()
	s.recoverAll()
	if got := s.live(); !slices.Equal(got, oldFiles) {
		t.Fatal(got)
	}
	if exists(filepath.Join(s.prefix, stagedWork)) {
		t.Fatal("the work directory outlived the undo")
	}
}

func TestUndoesASwapHoweverFarItGot(t *testing.T) {
	// Each rename of a swap either happened or not when AstrLink stopped.
	for done := range 5 {
		s := newSwapped(t)
		if _, failure := beginSwap(s.work, s.journal); failure != nil {
			t.Fatal(failure)
		}
		moves := append(slices.Clone(s.journal.Retired), s.journal.Placed...)
		for _, m := range moves[:done] {
			if err := os.MkdirAll(filepath.Dir(m[1]), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(m[0], m[1]); err != nil {
				t.Fatal(err)
			}
		}
		if failure := recoverWork(s.work); failure != nil {
			t.Fatal(done, failure)
		}
		if exists(filepath.Join(s.work, stagedJournal)) {
			t.Fatal(done, "the journal outlived the undo")
		}
		// No update holds the run any more, so it goes with the new files.
		if got := tree(t, s.prefix); !slices.Equal(got, oldFiles) {
			t.Fatal(done, got)
		}
	}
}
