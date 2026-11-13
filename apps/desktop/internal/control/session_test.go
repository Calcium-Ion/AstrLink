package control

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUnixSessionOmitsTheControlToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows sessions carry the observer token")
	}
	home := t.TempDir()
	data := filepath.Join(home, "data")
	if err := PublishSession(home, data, "http://127.0.0.1:9", "secret-token", 7); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(SessionPath(home))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "secret-token") || strings.Contains(text, "control_token") || !strings.Contains(text, "control.sock") {
		t.Fatalf("session %s", text)
	}
	info, err := os.Stat(SessionPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("session mode %v", info.Mode().Perm())
	}
	if info, err := os.Stat(filepath.Dir(SessionPath(home))); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("session directory mode %v, %v", info.Mode().Perm(), err)
	}
}

func TestWindowsSessionCarriesOnlyTheObserverToken(t *testing.T) {
	session := sessionFile(true, "C:/data", "http://127.0.0.1:9", "observer-token", 7)
	if session.ControlToken != "observer-token" || session.ControlURL != "http://127.0.0.1:9" || session.ControlSocket != "" {
		t.Fatalf("windows session %+v", session)
	}
	unix := sessionFile(false, "/data", "http://127.0.0.1:9", "observer-token", 7)
	if unix.ControlToken != "" || unix.ControlURL != "" || unix.ControlSocket == "" || unix.DesktopPID != os.Getpid() {
		t.Fatalf("unix session %+v", unix)
	}
	// The fields agent tools read keep their names.
	raw, _ := json.Marshal(session)
	for _, field := range []string{`"schema_version":1`, `"control_url"`, `"control_token"`, `"pid":7`, `"desktop_pid"`} {
		if !strings.Contains(string(raw), field) {
			t.Errorf("%s lacks %s", raw, field)
		}
	}
}

func TestClearRemovesOnlyOurSession(t *testing.T) {
	home := t.TempDir()
	if err := PublishSession(home, filepath.Join(home, "data"), "http://127.0.0.1:9", "", 0); err != nil {
		t.Fatal(err)
	}
	// A different desktop process (the successor after a development
	// restart) must not lose its locator to our shutdown.
	if err := clearSessionFor(home, os.Getpid()+1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SessionPath(home)); err != nil {
		t.Fatal("another desktop removed our session")
	}
	if err := ClearSession(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SessionPath(home)); !os.IsNotExist(err) {
		t.Fatal("the session survived")
	}
	// Legacy files without an owner are still cleared.
	if err := os.WriteFile(SessionPath(home), []byte(`{"schema_version":1,"control_socket":"/tmp/x.sock"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := clearSessionFor(home, os.Getpid()+1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SessionPath(home)); !os.IsNotExist(err) {
		t.Fatal("a legacy session survived")
	}
	if err := ClearSession(home); err != nil {
		t.Fatalf("clearing a missing session: %v", err)
	}
}

func TestUserHomePrefersTheAgentHome(t *testing.T) {
	t.Setenv("ASTRLINK_AGENT_HOME", filepath.Join("agents", "home"))
	if home, err := UserHome(); err != nil || home != filepath.Join("agents", "home") {
		t.Fatalf("got %q, %v", home, err)
	}
}
