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
	if err := PublishSession(SessionPath(home), Locator{DataDirectory: data, ControlURL: "http://127.0.0.1:9", ObserverToken: "secret-token", PID: 7}); err != nil {
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
	session := sessionFile(true, Locator{DataDirectory: "C:/data", ControlURL: "http://127.0.0.1:9", ObserverToken: "observer-token", PID: 7})
	if session.ControlToken != "observer-token" || session.ControlURL != "http://127.0.0.1:9" || session.ControlSocket != "" {
		t.Fatalf("windows session %+v", session)
	}
	unix := sessionFile(false, Locator{DataDirectory: "/data", ControlURL: "http://127.0.0.1:9", ObserverToken: "observer-token", PID: 7})
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
	if err := PublishSession(SessionPath(home), Locator{DataDirectory: filepath.Join(home, "data"), ControlURL: "http://127.0.0.1:9"}); err != nil {
		t.Fatal(err)
	}
	// A different desktop process (the successor after a development
	// restart) must not lose its locator to our shutdown.
	if err := clearSessionFor(SessionPath(home), os.Getpid()+1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SessionPath(home)); err != nil {
		t.Fatal("another desktop removed our session")
	}
	if err := ClearSession(SessionPath(home)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SessionPath(home)); !os.IsNotExist(err) {
		t.Fatal("the session survived")
	}
	// Legacy files without an owner are still cleared.
	if err := os.WriteFile(SessionPath(home), []byte(`{"schema_version":1,"control_socket":"/tmp/x.sock"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := clearSessionFor(SessionPath(home), os.Getpid()+1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SessionPath(home)); !os.IsNotExist(err) {
		t.Fatal("a legacy session survived")
	}
	if err := ClearSession(SessionPath(home)); err != nil {
		t.Fatalf("clearing a missing session: %v", err)
	}
}

func TestEachInstanceKeepsItsOwnSession(t *testing.T) {
	home := t.TempDir()
	defaultPath, devPath := InstanceSessionPath(home, false), InstanceSessionPath(home, true)
	if defaultPath != filepath.Join(home, ".astrlink", "control-session.json") || defaultPath != SessionPath(home) ||
		devPath != filepath.Join(home, ".astrlink", "control-session-dev.json") {
		t.Fatal(defaultPath, devPath)
	}
	if err := PublishSession(defaultPath, Locator{DataDirectory: filepath.Join(home, "default"), ControlURL: "http://127.0.0.1:9", ObserverToken: "a", GatewayURL: "http://localhost:8317", PID: 7}); err != nil {
		t.Fatal(err)
	}
	if err := PublishSession(devPath, Locator{DataDirectory: filepath.Join(home, "dev"), ControlURL: "http://127.0.0.1:10", ObserverToken: "b", GatewayURL: "http://localhost:8318", PID: 8}); err != nil {
		t.Fatal(err)
	}
	read := func(path string) map[string]any {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		return fields
	}
	// The default file keeps its fields; the gateway address is the only
	// addition, and it is optional.
	fields := read(defaultPath)
	want := []string{"desktop_pid", "gateway_url", "pid", "schema_version"}
	if runtime.GOOS == "windows" {
		want = append(want, "control_token", "control_url")
	} else {
		want = append(want, "control_socket")
	}
	if len(fields) != len(want) || fields["gateway_url"] != "http://localhost:8317" || fields["pid"] != float64(7) {
		t.Fatalf("default session %v", fields)
	}
	for _, name := range want {
		if _, ok := fields[name]; !ok {
			t.Fatalf("default session %v lacks %s", fields, name)
		}
	}
	if dev := read(devPath); dev["gateway_url"] != "http://localhost:8318" || dev["pid"] != float64(8) {
		t.Fatalf("dev session %v", dev)
	}
	if raw, _ := json.Marshal(sessionFile(false, Locator{DataDirectory: "/data"})); strings.Contains(string(raw), "gateway_url") {
		t.Fatalf("an unknown gateway is written: %s", raw)
	}

	// Each instance clears only its own file, and only when it owns it.
	if err := clearSessionFor(devPath, os.Getpid()+1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(devPath); err != nil {
		t.Fatal("another desktop removed the dev session")
	}
	if err := ClearSession(devPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(devPath); !os.IsNotExist(err) {
		t.Fatal("the dev session survived")
	}
	if _, err := os.Stat(defaultPath); err != nil {
		t.Fatal("clearing the dev session removed the default one")
	}
}

func TestUserHomePrefersTheAgentHome(t *testing.T) {
	t.Setenv("ASTRLINK_AGENT_HOME", filepath.Join("agents", "home"))
	if home, err := UserHome(); err != nil || home != filepath.Join("agents", "home") {
		t.Fatalf("got %q, %v", home, err)
	}
}
