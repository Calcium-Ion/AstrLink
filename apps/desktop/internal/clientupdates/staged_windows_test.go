package clientupdates

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// copyPing copies the system's PING.EXE to path: a program that keeps
// running for as long as it is told.
func copyPing(t *testing.T, path string) {
	t.Helper()
	from, err := os.Open(filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE"))
	if err != nil {
		t.Fatal(err)
	}
	defer from.Close()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	to, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(to, from); err != nil {
		t.Fatal(err)
	}
	if err := to.Close(); err != nil {
		t.Fatal(err)
	}
}

func startPing(t *testing.T, program string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(program, "-n", "60", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

// alive reports whether cmd's process has not exited.
func alive(t *testing.T, cmd *exec.Cmd) bool {
	t.Helper()
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	event, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && event == uint32(windows.WAIT_TIMEOUT)
}

func TestRefusesWhileAProgramFromTheInstallationRuns(t *testing.T) {
	dir := t.TempDir()
	ping := filepath.Join(dir, "ping.exe")
	copyPing(t, ping)
	child := startPing(t, ping)
	failure := refuseRunning([]string{dir})
	other := refuseRunning([]string{filepath.Join(dir, "other")})
	if failure == nil || failure.code != "running" || !strings.HasPrefix(failure.detail, strconv.Itoa(child.Process.Pid)+" ") {
		t.Fatal(failure)
	}
	if other != nil {
		t.Fatal(other.detail)
	}
}

// A running client moves aside with its files, keeps running, and its old
// files go at the first check after it exits.
func TestSwapsARunningClientAndDeletesItsFilesOnceItExits(t *testing.T) {
	root := t.TempDir()
	prefix := filepath.Join(root, "prefix")
	work := workDir(prefix, ClientClaude)
	program := filepath.Join(prefix, "pkg", "bin", "claude.exe")
	copyPing(t, program)
	if err := os.WriteFile(filepath.Join(prefix, "claude.cmd"), []byte("old shim"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := startPing(t, program)

	c, failure := holdClaim(work)
	if failure != nil {
		t.Fatal(failure)
	}
	runDir := filepath.Join(work, "1")
	for rel, contents := range map[string]string{`stage\pkg\version`: "new", `stage\claude.cmd`: "new shim"} {
		path := filepath.Join(runDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, failure := beginSwap(work, journal{
		Run: runDir,
		Retired: []move{
			{filepath.Join(prefix, "pkg"), filepath.Join(runDir, `previous\package-0`)},
			{filepath.Join(prefix, "claude.cmd"), filepath.Join(runDir, `previous\claude.cmd`)},
		},
		Placed: []move{
			{filepath.Join(runDir, `stage\pkg`), filepath.Join(prefix, "pkg")},
			{filepath.Join(runDir, `stage\claude.cmd`), filepath.Join(prefix, "claude.cmd")},
		},
	})
	if failure != nil {
		t.Fatal(failure)
	}
	if err := s.apply(); err != nil {
		t.Fatal(err)
	}
	s.finish()
	finishRun(prefix, ClientClaude, runDir, c)

	var live []string
	for _, entry := range tree(t, prefix) {
		if !strings.HasPrefix(entry, stagedWork) {
			live = append(live, entry)
		}
	}
	if !slices.Equal(live, []string{"claude.cmd=new shim", "pkg/version=new"}) {
		t.Fatal(live)
	}
	moved := filepath.Join(runDir, `previous\package-0\bin\claude.exe`)
	if !exists(moved) {
		t.Fatal("the running program was not moved aside")
	}
	env := &Environment{home: root, path: prefix}
	// A check meanwhile leaves it, and the new files, alone.
	recoverAll(env, ClientClaude)
	if !exists(moved) || !exists(filepath.Join(prefix, "pkg", "version")) {
		t.Fatal("a check removed the running program or the new files")
	}
	if !alive(t, client) {
		t.Fatal("the client stopped")
	}
	_ = client.Process.Kill()
	_ = client.Wait()

	recoverAll(env, ClientClaude)
	if exists(filepath.Join(prefix, stagedWork)) {
		t.Fatal("the old files outlived the client")
	}
}
