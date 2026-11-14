package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/controlapi"
)

// gateway is one instance under test: a Control API whose session file sits
// in a temporary home.
type gateway struct {
	server *httptest.Server
	name   string
}

func startGateway(t *testing.T, name string) *gateway {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(controlapi.AuditSettingsPath, func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{"instance": name})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &gateway{server: server, name: name}
}

// tempHome points the CLI's default session path at a temporary home.
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ASTRLINK_CONTROL_SESSION", "")
	t.Setenv("ASTRLINK_CONTROL_SOCKET", "")
	if err := os.MkdirAll(filepath.Join(home, ".astrlink"), 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

func writeSession(t *testing.T, path string, session SessionFile) {
	t.Helper()
	session.SchemaVersion = sessionSchemaVersion
	raw, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (g *gateway) publish(t *testing.T, path, gatewayURL string) {
	t.Helper()
	writeSession(t, path, SessionFile{ControlURL: g.server.URL, ControlToken: "secret-" + g.name, GatewayURL: gatewayURL, PID: os.Getpid()})
}

func runIn(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func listed(t *testing.T, stdout string) instancesReport {
	t.Helper()
	var report instancesReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	return report
}

func (r instancesReport) summary() string {
	var parts []string
	for _, entry := range r.Instances {
		part := entry.Name
		if entry.Running {
			part += " running"
		}
		if entry.GatewayURL != nil {
			part += " " + *entry.GatewayURL
		}
		parts = append(parts, part)
	}
	selected := "none"
	if r.Selected != nil {
		selected = *r.Selected
	}
	return strings.Join(parts, ", ") + "; selected " + selected
}

func TestInstancesReportsEachInstanceAndTheOneInUse(t *testing.T) {
	home := tempHome(t)
	defaultPath := filepath.Join(home, ".astrlink", "control-session.json")
	devPath := filepath.Join(home, ".astrlink", "control-session-dev.json")
	main, dev := startGateway(t, "default"), startGateway(t, "dev")
	main.publish(t, defaultPath, "http://localhost:8317")
	dev.publish(t, devPath, "http://localhost:8318")

	expect := func(want string, args ...string) {
		t.Helper()
		code, stdout, stderr := runIn(append([]string{"instances"}, args...)...)
		if code != 0 || stderr != "" {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
		for _, secret := range []string{"secret-", "control_url", "control_socket", "control_token", main.server.URL, dev.server.URL, home} {
			if strings.Contains(stdout, secret) {
				t.Fatalf("instances shows %q: %s", secret, stdout)
			}
		}
		if got := listed(t, stdout).summary(); got != want {
			t.Fatalf("instances %v = %s, want %s", args, got, want)
		}
	}
	expect("default running http://localhost:8317, dev running http://localhost:8318; selected default")
	expect("default running http://localhost:8317, dev running http://localhost:8318; selected dev", "--instance", "dev")

	// The default instance quits: its session may stay behind if it
	// crashed, but nothing answers there.
	main.server.Close()
	expect("default http://localhost:8317, dev running http://localhost:8318; selected dev")
	if err := os.Remove(defaultPath); err != nil {
		t.Fatal(err)
	}
	expect("default, dev running http://localhost:8318; selected dev")

	dev.server.Close()
	expect("default, dev http://localhost:8318; selected none")
	if err := os.Remove(devPath); err != nil {
		t.Fatal(err)
	}
	expect("default, dev; selected none")

	// --control-url bypasses the instances.
	code, stdout, _ := runIn("instances", "--control-url", "http://127.0.0.1:1")
	if code != 0 || listed(t, stdout).Selected != nil {
		t.Fatalf("code=%d %s", code, stdout)
	}
	if code, _, stderr := runIn("instances", "--instance", "staging"); code != 1 || !strings.Contains(stderr, `unknown instance "staging"`) {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestCommandsPickTheRunningInstance(t *testing.T) {
	home := tempHome(t)
	defaultPath := filepath.Join(home, ".astrlink", "control-session.json")
	devPath := filepath.Join(home, ".astrlink", "control-session-dev.json")
	main, dev := startGateway(t, "default"), startGateway(t, "dev")

	read := func(args ...string) (string, string) {
		t.Helper()
		code, stdout, stderr := runIn(append([]string{"audit-settings"}, args...)...)
		if code != 0 {
			t.Fatalf("%v: code=%d stderr=%q", args, code, stderr)
		}
		var payload map[string]string
		if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
			t.Fatal(err)
		}
		return payload["instance"], stderr
	}

	// Only the default instance: no notice.
	main.publish(t, defaultPath, "")
	if got, notice := read(); got != "default" || notice != "" {
		t.Fatalf("got %s, notice %q", got, notice)
	}
	// Both: default, with one line saying so; stdout is the same JSON.
	dev.publish(t, devPath, "")
	got, notice := read()
	if got != "default" || strings.Count(notice, "\n") != 1 || !strings.Contains(notice, "both running") || !strings.Contains(notice, "--instance dev") {
		t.Fatalf("got %s, notice %q", got, notice)
	}
	if got, notice := read("--instance", "dev"); got != "dev" || notice != "" {
		t.Fatalf("got %s, notice %q", got, notice)
	}
	if got, notice := read("--instance", "default"); got != "default" || notice != "" {
		t.Fatalf("got %s, notice %q", got, notice)
	}
	// Only dev runs: it stands in, with one line.
	main.server.Close()
	got, notice = read()
	if got != "dev" || strings.Count(notice, "\n") != 1 || !strings.Contains(notice, "using the dev instance") {
		t.Fatalf("got %s, notice %q", got, notice)
	}
	// Asking for the stopped default one gets it, and its connection error.
	if code, stdout, stderr := runIn("audit-settings", "--instance", "default"); code != 1 || stdout != "" || stderr == "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	// None runs: the familiar error, naming what was looked for.
	dev.server.Close()
	code, stdout, stderr := runIn("audit-settings")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "AstrLink control session unavailable (is the desktop gateway running?)") ||
		!strings.Contains(stderr, "default is not running") || !strings.Contains(stderr, "dev is not running") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if err := os.Remove(devPath); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runIn("audit-settings", "--instance", "dev"); code != 1 || !strings.Contains(stderr, "control session unavailable") || !strings.Contains(stderr, "dev instance") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestAnExplicitSessionPathHasItsDevFileBesideIt(t *testing.T) {
	tempHome(t)
	elsewhere := t.TempDir()
	dev := startGateway(t, "dev")
	dev.publish(t, filepath.Join(elsewhere, "control-session-dev.json"), "http://localhost:8318")
	session := filepath.Join(elsewhere, "control-session.json")
	for _, args := range [][]string{{"--session", session}, {}} {
		if len(args) == 0 {
			t.Setenv("ASTRLINK_CONTROL_SESSION", session)
		}
		code, stdout, stderr := runIn(append([]string{"audit-settings"}, args...)...)
		if code != 0 || !strings.Contains(stdout, `"dev"`) || !strings.Contains(stderr, "using the dev instance") {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
		code, stdout, _ = runIn(append([]string{"instances"}, args...)...)
		if got := listed(t, stdout).summary(); code != 0 || got != "default, dev running http://localhost:8318; selected dev" {
			t.Fatalf("%v: %s", args, got)
		}
	}
	// --control-url still goes straight to its address, without a notice.
	t.Setenv("ASTRLINK_CONTROL_SESSION", "")
	code, stdout, stderr := runIn("audit-settings", "--control-url", dev.server.URL)
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"dev"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

// A session whose endpoint cannot be checked (a sandbox blocks the socket)
// is used when its process is alive, so the request reports the real cause.
func TestAnUncheckableEndpointCountsWhileItsProcessLives(t *testing.T) {
	home := tempHome(t)
	path := filepath.Join(home, ".astrlink", "control-session.json")
	writeSession(t, path, SessionFile{ControlSocket: filepath.Join(home, "control.sock"), PID: 4242})
	probe, alive := dialProbe, processAlive
	t.Cleanup(func() { dialProbe, processAlive = probe, alive })
	dialProbe = func(string, string) (net.Conn, error) { return nil, syscall.EPERM }
	var asked []int
	living := true
	processAlive = func(pid int) bool { asked = append(asked, pid); return living }

	state := inspectInstance(instanceDefault, path)
	if state.status != statusRunning || !slices.Equal(asked, []int{4242}) {
		t.Fatalf("status %v, asked %v", state.status, asked)
	}
	living = false
	if state := inspectInstance(instanceDefault, path); state.status != statusUnknown {
		t.Fatalf("status %v", state.status)
	}
	// Still tried rather than reported missing.
	resolved, err := resolveDial(DialOptions{})
	if err != nil || resolved.socket != filepath.Join(home, "control.sock") {
		t.Fatalf("%+v, %v", resolved, err)
	}
	// A refused connection is a stopped instance whatever the process.
	dialProbe = func(string, string) (net.Conn, error) { return nil, syscall.ECONNREFUSED }
	living = true
	if state := inspectInstance(instanceDefault, path); state.status != statusStopped {
		t.Fatalf("status %v", state.status)
	}
}
