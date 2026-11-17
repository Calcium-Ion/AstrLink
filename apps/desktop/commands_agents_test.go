package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	"github.com/QuantumNous/astrlink/desktop/internal/instance"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// Nothing here reads the user's home, reveals a token or opens a
// terminal; the checks under test fail before any of that.

func TestEveryAgentCommandIsABoundMethod(t *testing.T) {
	services := []any{AgentTools{}, ProviderImport{}, LocalImport{}, LocalClients{}, Terminal{}}
	commands := []string{
		"agent_debug_status", "install_agent_debug", "uninstall_agent_debug",
		"local_import_sources", "read_local_import", "probe_local_import_models", "confirm_local_import", "dismiss_local_import",
		"pending_provider_import", "dismiss_provider_import", "confirm_provider_import",
		"local_client_status", "refresh_local_clients", "update_local_clients", "repair_local_clients",
		"list_terminal_apps", "pick_terminal_directory", "open_service_terminal",
	}
	var methods []string
	for _, command := range commands {
		var method strings.Builder
		for _, word := range strings.Split(command, "_") {
			method.WriteString(strings.ToUpper(word[:1]) + word[1:])
		}
		methods = append(methods, method.String())
	}
	// The services bind these methods and no others.
	var bound []string
	for _, service := range services {
		kind := reflect.TypeOf(service)
		for i := range kind.NumMethod() {
			bound = append(bound, kind.Method(i).Name)
		}
	}
	slices.Sort(methods)
	slices.Sort(bound)
	if !slices.Equal(bound, methods) {
		t.Fatalf("bound %v, want %v", bound, methods)
	}
}

func TestATerminalTakesTheConfiguredTokenOrTheFirst(t *testing.T) {
	page := json.RawMessage(`{"items": [{"id": "tok_a"}, "odd", {"id": 3}, {"id": "tok_b"}]}`)
	for _, tc := range []struct {
		page       json.RawMessage
		configured *string
		want       string
	}{
		{page, ptr("tok_b"), "tok_b"},
		{page, ptr("tok_gone"), "tok_a"},
		{page, nil, "tok_a"},
	} {
		if got, err := terminalTokenID(tc.page, tc.configured); err != nil || got != tc.want {
			t.Errorf("%v: %q, %v", tc.configured, got, err)
		}
	}
	for _, empty := range []string{`{"items": []}`, `{"items": {}}`, `[]`, `not json`} {
		if _, err := terminalTokenID(json.RawMessage(empty), ptr("tok_a")); err == nil || err.Error() != "create an access token first" {
			t.Errorf("%s: %v", empty, err)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestGuardsCoverEveryInstance(t *testing.T) {
	root := t.TempDir()
	data, pins := guardedPaths(instance.Directories{
		Data:   filepath.Join(root, "data", instance.Identifier),
		Config: filepath.Join(root, "config", instance.IsolatedIdentifier),
	})
	if !slices.Equal(data, []string{
		filepath.Join(root, "data", instance.Identifier),
		filepath.Join(root, "data", instance.IsolatedIdentifier),
	}) || !slices.Equal(pins, []string{
		filepath.Join(root, "config", instance.Identifier, "raw-key-pins.json"),
		filepath.Join(root, "config", instance.IsolatedIdentifier, "raw-key-pins.json"),
	}) {
		t.Fatal(data, pins)
	}
	if data, pins := guardedPaths(instance.Directories{}); data != nil || pins != nil {
		t.Fatal(data, pins)
	}
}

func TestATerminalOpensInAResolvedFolder(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "work")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := folder(target); !ok || got != want {
		t.Fatal(got, ok)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(root, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if got, ok := folder(link); !ok || got != want {
			t.Fatal(got, ok)
		}
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, filepath.Join(root, "missing")} {
		if _, ok := folder(path); ok {
			t.Error(path)
		}
	}
}

func TestATerminalRequestIsCheckedBeforeAnyTokenIsRead(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	valid := ServiceTerminalRequest{
		ServiceID:    "svc_claude",
		ServiceName:  "Claude 2",
		Client:       agents.TerminalClaude,
		Directory:    file,
		Terminal:     agents.TerminalAppTerminal,
		InferenceURL: "http://127.0.0.1:18317",
	}
	for _, tc := range []struct {
		change func(*ServiceTerminalRequest)
		want   string
	}{
		{func(r *ServiceTerminalRequest) { r.ServiceID = "Svc" }, "invalid API provider ID"},
		{func(r *ServiceTerminalRequest) { r.Client = "" }, "unknown client"},
		{func(r *ServiceTerminalRequest) { r.Model = ptr("a\nb") }, ""},
		{func(r *ServiceTerminalRequest) { r.InferenceURL = "http://192.168.1.2:18317" }, "invalid local inference URL"},
		{func(*ServiceTerminalRequest) {}, file + " is not a folder"},
	} {
		request := valid
		tc.change(&request)
		session, err := Terminal{}.session(t.Context(), request)
		if err == nil || session != nil || tc.want != "" && err.Error() != tc.want {
			t.Errorf("%+v: %v", request, err)
		}
	}
}

func TestATerminalRequestDecodesWhatThePageSends(t *testing.T) {
	var request ServiceTerminalRequest
	err := json.Unmarshal([]byte(`{"service_id": "svc_codex", "service_name": "Relay", "client": "codex",
		"directory": "/work", "model": null, "terminal": "gnome_terminal", "inference_url": "http://127.0.0.1:18317"}`), &request)
	if err != nil || request.Client != agents.TerminalCodex || request.Terminal != agents.TerminalAppGnomeTerminal || request.Model != nil {
		t.Fatal(request, err)
	}
	for _, bad := range []string{`{"client": "gemini"}`, `{"terminal": "xterm; rm"}`} {
		if err := json.Unmarshal([]byte(bad), &request); err == nil {
			t.Error(bad)
		}
	}
}

func TestTerminalMessagesNameTheClientAndService(t *testing.T) {
	request := ServiceTerminalRequest{Client: agents.TerminalCodex, ServiceName: "Relay"}
	banner, exited := terminalMessages(prefs.LocaleEn, request)
	if banner != "AstrLink: Codex in this window uses only “Relay”." || !strings.HasPrefix(exited, "Codex exited.") {
		t.Fatal(banner, exited)
	}
	denied := terminalError(prefs.LocaleEn, agents.TerminalAppITerm2, errNotAuthorized)
	if denied == nil || !strings.Contains(denied.Error(), "permission to control iTerm2") {
		t.Fatal(denied)
	}
	if err := terminalError(prefs.LocaleEn, agents.TerminalAppITerm2, os.ErrNotExist); err != os.ErrNotExist {
		t.Fatal(err)
	}
	if err := terminalError(prefs.LocaleEn, agents.TerminalAppITerm2, nil); err != nil {
		t.Fatal(err)
	}
}

var errNotAuthorized = &osascriptError{"execution error: Not authorized to send Apple events to iTerm2. (-1743)"}

type osascriptError struct{ message string }

func (e *osascriptError) Error() string { return e.message }
