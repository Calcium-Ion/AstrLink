package agentmcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

func TestMCPListsAndCallsRequestRecordTools(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(controlapi.RequestSessionsPath, func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		if request.URL.Query().Get("status") != "failed" {
			t.Fatalf("status filter = %q", request.URL.Query().Get("status"))
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
	mux.HandleFunc(controlapi.RequestsPath+"/req_1/audit", func(writer http.ResponseWriter, _ *http.Request) {
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

	client, err := Dial(DialOptions{ControlURL: server.URL, ControlToken: "test-token"})
	if err != nil {
		t.Fatal(err)
	}

	stdin, input := io.Pipe()
	output := &bytes.Buffer{}
	done := make(chan error, 1)
	go func() {
		done <- ServeStdio(context.Background(), &Server{Client: client}, stdin, output)
	}()

	writeRPC(t, input, 1, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	writeRPC(t, input, 2, "tools/list", nil)
	writeRPC(t, input, 3, "tools/call", map[string]any{
		"name":      "list_request_sessions",
		"arguments": map[string]any{"status": "failed", "limit": 10},
	})
	writeRPC(t, input, 4, "tools/call", map[string]any{
		"name":      "get_request_session",
		"arguments": map[string]any{"id": "sess_1"},
	})
	writeRPC(t, input, 5, "tools/call", map[string]any{
		"name":      "list_request_records",
		"arguments": map[string]any{},
	})
	writeRPC(t, input, 6, "tools/call", map[string]any{
		"name":      "get_request_record",
		"arguments": map[string]any{"id": "req_1"},
	})
	writeRPC(t, input, 7, "tools/call", map[string]any{
		"name":      "get_request_children",
		"arguments": map[string]any{"id": "req_1"},
	})
	writeRPC(t, input, 8, "tools/call", map[string]any{
		"name":      "get_request_audit",
		"arguments": map[string]any{"id": "req_1"},
	})
	writeRPC(t, input, 9, "tools/call", map[string]any{
		"name":      "get_audit_settings",
		"arguments": map[string]any{},
	})
	writeRPC(t, input, 10, "tools/call", map[string]any{
		"name":      "get_routing_settings",
		"arguments": map[string]any{},
	})
	writeRPC(t, input, 11, "tools/call", map[string]any{
		"name":      "purge_request_records",
		"arguments": map[string]any{},
	})
	_ = input.Close()
	if err := <-done; err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}

	responses := readAllResponses(t, output)
	if len(responses) != 11 {
		t.Fatalf("responses = %d, payload=%s", len(responses), output.String())
	}
	if _, ok := responses[1]["result"].(map[string]any)["capabilities"]; !ok {
		t.Fatalf("initialize result = %#v", responses[1]["result"])
	}
	tools := responses[2]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 8 {
		t.Fatalf("tool count = %d", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.(map[string]any)["name"].(string)] = true
	}
	for _, name := range []string{
		"list_request_sessions", "get_request_session", "list_request_records",
		"get_request_record", "get_request_children", "get_request_audit", "get_audit_settings",
		"get_routing_settings",
	} {
		if !names[name] {
			t.Fatalf("missing tool %s", name)
		}
	}
	if names["purge_request_records"] {
		t.Fatal("purge must not be exposed")
	}

	auditText := callText(t, responses[8])
	if !strings.Contains(auditText, `"bodies_captured":false`) {
		t.Fatalf("audit wrapper = %s", auditText)
	}
	if !strings.Contains(auditText, "were not captured") {
		t.Fatalf("audit hint missing: %s", auditText)
	}
	if calls := routingCalls.Load(); calls != 1 {
		t.Fatalf("routing settings calls = %d, want 1", calls)
	}
	routing := callResult(t, responses[10])
	if routing["isError"] == true {
		t.Fatalf("get_routing_settings should succeed: %#v", routing)
	}
	routingText := callText(t, responses[10])
	if !strings.Contains(routingText, `"model_redirects"`) || !strings.Contains(routingText, `"to":"gpt-5"`) {
		t.Fatalf("routing settings = %s", routingText)
	}
	unknown := callResult(t, responses[11])
	if unknown["isError"] != true {
		t.Fatalf("unknown tool should be an error: %#v", unknown)
	}
}

func TestServeStdioHandshakesWithoutControlSession(t *testing.T) {
	stdin, input := io.Pipe()
	output := &bytes.Buffer{}
	done := make(chan error, 1)
	server := &Server{
		Options: DialOptions{SessionPath: filepath.Join(t.TempDir(), "missing-session.json")},
	}
	go func() {
		done <- ServeStdio(context.Background(), server, stdin, output)
	}()

	writeRPC(t, input, 1, "initialize", map[string]any{"protocolVersion": "2025-03-26"})
	writeRPC(t, input, 2, "tools/list", nil)
	writeRPC(t, input, 3, "tools/call", map[string]any{
		"name":      "get_audit_settings",
		"arguments": map[string]any{},
	})
	_ = input.Close()
	if err := <-done; err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}

	responses := readAllResponses(t, output)
	if len(responses) != 3 {
		t.Fatalf("responses = %d, payload=%s", len(responses), output.String())
	}
	if strings.Contains(output.String(), "Content-Length") {
		t.Fatalf("handshake used Content-Length framing: %s", output.String())
	}
	info := responses[1]["result"].(map[string]any)["serverInfo"].(map[string]any)
	if info["version"] != mcpServerVersion {
		t.Fatalf("server version = %#v", info["version"])
	}
	tools := responses[2]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 8 {
		t.Fatalf("tool count = %d", len(tools))
	}
	call := callResult(t, responses[3])
	if call["isError"] != true {
		t.Fatalf("missing session should be a tool error: %#v", call)
	}
	if !strings.Contains(callText(t, responses[3]), "desktop gateway") {
		t.Fatalf("error should mention desktop gateway: %s", callText(t, responses[3]))
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

func writeRPC(t *testing.T, writer io.Writer, id int, method string, params any) {
	t.Helper()
	payload := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeMCPMessage(writer, raw); err != nil {
		t.Fatal(err)
	}
}

func readAllResponses(t *testing.T, buffer *bytes.Buffer) map[int]map[string]any {
	t.Helper()
	reader := bufio.NewReader(bytes.NewReader(buffer.Bytes()))
	out := map[int]map[string]any{}
	for {
		payload, err := readMCPMessage(reader)
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatal(err)
		}
		id := int(decoded["id"].(float64))
		out[id] = decoded
	}
}

func callResult(t *testing.T, response map[string]any) map[string]any {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("result = %#v", response["result"])
	}
	return result
}

func callText(t *testing.T, response map[string]any) string {
	t.Helper()
	result := callResult(t, response)
	content := result["content"].([]any)
	return content[0].(map[string]any)["text"].(string)
}

func escapeJSON(value string) string {
	raw, _ := json.Marshal(value)
	return strings.Trim(string(raw), `"`)
}
