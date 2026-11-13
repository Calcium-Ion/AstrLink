package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/controlapi"
)

func TestDialPrefersSocketOverSessionToken(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "control.sock")
	sessionPath := filepath.Join(dir, "session.json")
	if err := os.WriteFile(sessionPath, []byte(`{"schema_version":1,"control_socket":"`+escapeJSON(socket)+`","control_url":"http://127.0.0.1:1","control_token":"should-not-use"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveDial(DialOptions{SessionPath: sessionPath})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.socket != socket {
		t.Fatalf("socket = %q, want %q", resolved.socket, socket)
	}
	if resolved.token != "" {
		t.Fatalf("token leaked from session when socket is present: %q", resolved.token)
	}
}

func TestLoadSessionFileRejectsUnknownSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":2,"control_url":"http://127.0.0.1:1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSessionFile(path); err == nil {
		t.Fatal("expected schema error")
	}
}

// runCLI runs one invocation against a test Control API.
func runCLI(t *testing.T, controlURL string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	args = append(args, "--control-url", controlURL, "--control-token", "test-token")
	code := Run(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRunCallsRequestRecordCommands(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(controlapi.RequestSessionsPath, func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		if request.Header.Get("User-Agent") != userAgent {
			t.Errorf("user agent = %q", request.Header.Get("User-Agent"))
		}
		query := request.URL.Query()
		if query.Get("status") != "failed" || query.Get("limit") != "10" || query.Get("service_id") != "svc_1" || query.Has("cursor") {
			t.Errorf("query = %s", request.URL.RawQuery)
		}
		writeJSON(writer, map[string]any{"items": []any{}, "next_cursor": nil})
	})
	mux.HandleFunc(controlapi.RequestSessionsPath+"/sess_1", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{"id": "sess_1", "turns": []any{}})
	})
	mux.HandleFunc(controlapi.RequestsPath, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != controlapi.RequestsPath {
			http.NotFound(writer, request)
			return
		}
		writeJSON(writer, map[string]any{"items": []any{map[string]any{"id": "req_1"}}, "next_cursor": nil})
	})
	mux.HandleFunc(controlapi.RequestsPath+"/req_1", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{"id": "req_1", "events": []any{map[string]any{"kind": "accepted"}}})
	})
	mux.HandleFunc(controlapi.RequestsPath+"/req_1/children", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{"items": []any{}})
	})
	mux.HandleFunc(controlapi.RequestsPath+"/req_1/audit", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("view") != "shareable" {
			http.Error(writer, "agents read the shareable view", http.StatusForbidden)
			return
		}
		writeJSON(writer, map[string]any{"request_id": "req_1"})
	})
	mux.HandleFunc(controlapi.AuditSettingsPath, func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{"request_body_enabled": false, "response_content_enabled": false})
	})
	var routingCalls atomic.Int32
	mux.HandleFunc(controlapi.RoutingSettingsPath, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		routingCalls.Add(1)
		writeJSON(writer, map[string]any{
			"model_redirects": []any{map[string]any{"from": "gpt-4o", "to": "gpt-5", "enabled": true}},
			"max_attempts":    3,
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	for _, args := range [][]string{
		{"sessions", "--status", "failed", "--limit", "10", "--service-id", "svc_1"},
		{"session", "sess_1"},
		{"requests"},
		{"request", "req_1"},
		{"children", "req_1"},
		{"audit-settings"},
	} {
		if code, stdout, stderr := runCLI(t, server.URL, args...); code != 0 || !json.Valid([]byte(stdout)) {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}

	code, stdout, stderr := runCLI(t, server.URL, "audit", "req_1")
	if code != 0 || !strings.Contains(stdout, `"bodies_captured":false`) || !strings.Contains(stdout, "were not captured") {
		t.Fatalf("audit: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, _ = runCLI(t, server.URL, "routing", "--pretty")
	if code != 0 || !strings.Contains(stdout, `"model_redirects": [`) || !strings.Contains(stdout, `"to": "gpt-5"`) {
		t.Fatalf("routing: code=%d stdout=%s", code, stdout)
	}
	if calls := routingCalls.Load(); calls != 1 {
		t.Fatalf("routing settings calls = %d, want 1", calls)
	}

	// Control API errors exit non-zero with the reason on stderr.
	if code, stdout, stderr := runCLI(t, server.URL, "request", "missing"); code != 1 || stdout != "" || !strings.Contains(stderr, "HTTP 404") {
		t.Fatalf("missing record: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunValidatesUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help code=%d", code)
	}
	for _, name := range []string{
		"sessions", "session", "requests", "search", "request", "children", "explain", "audit",
		"raw-audit", "raw-revoke", "audit-settings", "routing", "services", "service", "privacy",
	} {
		if !strings.Contains(stdout.String(), "\n  "+name+" ") {
			t.Fatalf("help misses %s:\n%s", name, stdout.String())
		}
	}
	if len(commandCatalog()) != 15 {
		t.Fatalf("command count = %d", len(commandCatalog()))
	}

	stdout.Reset()
	if code := Run(context.Background(), []string{"raw-audit", "--help"}, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), "--reason <value>") {
		t.Fatalf("raw-audit help code=%d out=%s", code, stdout.String())
	}
	for _, args := range [][]string{
		{},
		{"purge"},
		{"request"},
		{"request", "a", "b"},
		{"sessions", "extra"},
		{"sessions", "--limit", "many"},
		{"audit-settings", "--unknown"},
	} {
		stderr.Reset()
		if code := Run(context.Background(), args, io.Discard, &stderr); code != 2 || stderr.Len() == 0 {
			t.Fatalf("%v: code=%d stderr=%q", args, code, stderr.String())
		}
	}
}

func TestParseInterspersedAcceptsFlagsAfterArguments(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	reason := flags.String("reason", "", "")
	positional, err := parseInterspersed(flags, []string{"req_1", "--reason", "why", "--", "--literal"})
	if err != nil {
		t.Fatal(err)
	}
	if *reason != "why" || len(positional) != 2 || positional[0] != "req_1" || positional[1] != "--literal" {
		t.Fatalf("reason=%q positional=%v", *reason, positional)
	}
}

func TestDetectAgentFromHostEnvironment(t *testing.T) {
	for env, want := range map[string]string{"CLAUDECODE": "Claude Code", "CODEX_SANDBOX": "Codex", "": ""} {
		getenv := func(name string) string {
			if name == env {
				return "1"
			}
			return ""
		}
		if got := detectAgent(getenv); got != want {
			t.Fatalf("detectAgent(%s) = %q, want %q", env, got, want)
		}
	}
}

func TestRunReportsAMissingControlSession(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"audit-settings", "--session", filepath.Join(t.TempDir(), "missing-session.json"),
	}, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "desktop gateway") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestGetRequestAuditIncludesBodiesWhenPresent(t *testing.T) {
	raw, err := annotateAudit(json.RawMessage(`{"request_id":"req_1","request_body":{"content":"hi"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"bodies_captured":true`)) {
		t.Fatalf("wrapped = %s", raw)
	}
	if bytes.Contains(raw, []byte("hint")) {
		t.Fatalf("hint should be omitted when bodies exist: %s", raw)
	}
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}

func escapeJSON(value string) string {
	raw, _ := json.Marshal(value)
	return strings.Trim(string(raw), `"`)
}

func TestAuditReadsReturnBodiesLargerThanOtherResponses(t *testing.T) {
	body := strings.Repeat("x", maxControlResponseBytes+1)
	auditPath := controlapi.RequestsPath + "/req_1/audit"
	mux := http.NewServeMux()
	mux.HandleFunc(auditPath, func(writer http.ResponseWriter, request *http.Request) {
		view := request.URL.Query().Get("view")
		exposure := map[string]string{"shareable": "shareable", "full": "raw"}[view]
		if exposure == "" || (view == "full") != (request.Header.Get(controlapi.RawGrantHeader) == "grant-1") {
			http.Error(writer, "unexpected audit read", http.StatusBadRequest)
			return
		}
		writeJSON(writer, map[string]any{
			"request_id":   "req_1",
			"request_body": map[string]any{"content": body, "exposure": exposure},
		})
	})
	mux.HandleFunc(controlapi.AuditSettingsPath, func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{"padding": body})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	for _, args := range [][]string{{"audit", "req_1"}, {"raw-audit", "req_1", "--grant", "grant-1"}} {
		code, stdout, stderr := runCLI(t, server.URL, args...)
		if code != 0 {
			t.Fatalf("%v: code=%d stderr=%s", args, code, stderr)
		}
		var wrapped map[string]any
		if err := json.Unmarshal([]byte(stdout), &wrapped); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if got := auditPart(t, wrapped, "request_body")["content"]; got != body {
			t.Fatalf("%v: request_body content has %d bytes, want %d", args, len(fmt.Sprint(got)), len(body))
		}
	}

	code, stdout, stderr := runCLI(t, server.URL, "audit-settings")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "larger than 8 MiB") {
		t.Fatalf("oversized response: code=%d stdout=%d bytes stderr=%q", code, len(stdout), stderr)
	}
}
