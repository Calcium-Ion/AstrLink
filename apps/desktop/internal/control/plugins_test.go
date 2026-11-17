package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestPluginActionsMapToTheirControlRequests(t *testing.T) {
	for _, tc := range []struct {
		action, key, name, input string
		method, path, body       string
	}{
		{"list", "", "", "", http.MethodGet, "/control/v1/plugins", ""},
		{"reload", "", "", "", http.MethodPost, "/control/v1/plugins/reload", ""},
		{"update", "team-rules", "", `{"enabled":false}`, http.MethodPatch, "/control/v1/plugins/team-rules", `{"enabled":false}`},
		{"status", "powercontext", "", "", http.MethodGet, "/control/v1/plugins/powercontext/status", ""},
		{"delete_credential", "powercontext", "api_token", "", http.MethodDelete, "/control/v1/plugins/powercontext/credentials/api_token", ""},
		{"save_credential", "powercontext", "api_token", `{"value":"x"}`, http.MethodPut, "/control/v1/plugins/powercontext/credentials/api_token", `{"value":"x"}`},
		{"stats", "powercontext", "", `{"days":7}`, http.MethodGet, "/control/v1/plugins/powercontext/stats?days=7", ""},
		{"journal", "notes", "", `{"since":42}`, http.MethodGet, "/control/v1/plugins/notes/journal?since=42", ""},
		{"journal", "notes", "", "", http.MethodGet, "/control/v1/plugins/notes/journal?since=0", ""},
		{"dry_run", "notes", "", `{"hook":"describeStatus","args":[]}`, http.MethodPost, "/control/v1/plugins/notes/dry-run", `{"hook":"describeStatus","args":[]}`},
		{"login", "vendor", "", `{"service_id":"svc_1"}`, http.MethodPost, "/control/v1/plugins/vendor/login", `{"service_id":"svc_1"}`},
		{"request_dry_run", "vendor", "", `{"service_id":"svc_1"}`, http.MethodPost, "/control/v1/plugins/vendor/request-dry-run", `{"service_id":"svc_1"}`},
		{"provider_state", "vendor", "", `{"service_id":"svc_1"}`, http.MethodPost, "/control/v1/plugins/vendor/provider-state", `{"service_id":"svc_1"}`},
		{"credits", "powercontext", "", "", http.MethodGet, "/control/v1/plugins/powercontext/credits", ""},
		{"host", "", "", "", http.MethodGet, "/control/v1/plugin-host", ""},
		{"update_host", "", "", `{"language":"ja"}`, http.MethodPatch, "/control/v1/plugin-host", `{"language":"ja"}`},
		{"market", "", "", "", http.MethodGet, "/control/v1/plugins/market", ""},
		{"market", "", "", `{"index":"https://example.com/index.json"}`, http.MethodGet, "/control/v1/plugins/market?index=https%3A%2F%2Fexample.com%2Findex.json", ""},
		{"market", "", "", `{"code":"46324580"}`, http.MethodGet, "/control/v1/plugins/market?code=46324580", ""},
		{"market_doc", "", "", `{"index":"https://www.newapi.ai/api/v1/community/index.json","key":"rexcoding-yu_artdance","version":"1.0.2","doc":"readme"}`,
			http.MethodGet, "/control/v1/plugins/market/doc?key=rexcoding-yu_artdance&doc=readme&index=https%3A%2F%2Fwww.newapi.ai%2Fapi%2Fv1%2Fcommunity%2Findex.json&version=1.0.2", ""},
		{"tasks", "sora", "", `{"cursor":"2026-10-25T08:00:00.000000000Z|task_1"}`, http.MethodGet, "/control/v1/plugins/sora/tasks?cursor=2026-10-25T08%3A00%3A00.000000000Z%7Ctask_1", ""},
		{"tasks", "sora", "", "", http.MethodGet, "/control/v1/plugins/sora/tasks", ""},
		{"clear_tasks", "sora", "", "", http.MethodDelete, "/control/v1/plugins/sora/tasks", ""},
		{"source_preview", "", "", `{"url":"https://github.com/acme/hello"}`, http.MethodPost, "/control/v1/plugins/sources/preview", `{"url":"https://github.com/acme/hello"}`},
		{"source_install", "", "", `{"token":"staged-review"}`, http.MethodPost, "/control/v1/plugins/sources/install", `{"token":"staged-review"}`},
		{"source_check", "hello", "", "", http.MethodPost, "/control/v1/plugins/hello/source/check", ""},
		{"source_rollback", "hello", "", `{"version":"20261023T120000Z-0000abcd"}`, http.MethodPost, "/control/v1/plugins/hello/source/rollback", `{"version":"20261023T120000Z-0000abcd"}`},
		{"source_uninstall", "hello", "", "", http.MethodDelete, "/control/v1/plugins/hello/source", ""},
		{"action", "powercontext", "", `{"action":"refresh","session":"session_1"}`, http.MethodPost, "/control/v1/plugins/powercontext/actions", `{"action":"refresh","session":"session_1"}`},
		{"ui_call", "powercontext", "", `{"method":"list"}`, http.MethodPost, "/control/v1/plugins/powercontext/ui-call", `{"method":"list"}`},
		{"clear_workspaces", "powercontext", "", "", http.MethodDelete, "/control/v1/plugins/powercontext/workspaces", ""},
		{"clear_values", "powercontext", "", "", http.MethodDelete, "/control/v1/plugins/powercontext/values", ""},
		{"clear_files", "powercontext", "", "", http.MethodDelete, "/control/v1/plugins/powercontext/files", ""},
		{"revoke_lookups", "powercontext", "", "", http.MethodDelete, "/control/v1/plugins/powercontext/lookup-credentials", ""},
	} {
		var input any
		if tc.input != "" {
			input = v(t, tc.input)
		}
		req, err := pluginRequest(tc.action, tc.key, tc.name, input, tc.input != "")
		if err != nil {
			t.Fatalf("%s: %v", tc.action, err)
		}
		if req.Method != tc.method || req.Path != tc.path {
			t.Errorf("%s = %s %s, want %s %s", tc.action, req.Method, req.Path, tc.method, tc.path)
		}
		var body string
		if req.Body != nil {
			encoded, _ := json.Marshal(req.Body)
			body = string(encoded)
		}
		if tc.body == "" && body != "" || tc.body != "" && !sameJSON(body, tc.body) {
			t.Errorf("%s body = %s, want %s", tc.action, body, tc.body)
		}
	}
	for _, tc := range []struct{ action, key, name, input string }{
		{"stats", "powercontext", "", `{"days":0}`},
		{"stats", "powercontext", "", `{"days":31}`},
		{"dry_run", "notes", "", ""},
		{"login", "vendor", "", ""},
		{"request_dry_run", "vendor", "", ""},
		{"provider_state", "vendor", "", ""},
		{"market", "", "", `{"code":"4632&x=1"}`},
		{"market_doc", "", "", `{"key":"Bad Key","doc":"readme"}`},
		{"market_doc", "", "", `{"key":"sora","doc":"license"}`},
		{"source_install", "", "", ""},
		{"update_host", "", "", ""},
		{"action", "powercontext", "", ""},
		{"get", "../services", "", ""},
		{"get", "Team", "", ""},
		{"get", "", "", ""},
		{"update", "team-rules", "", ""},
		{"save_credential", "powercontext", "api-token", `{}`},
		{"save_credential", "powercontext", "api_token", ""},
		{"uninstall", "powercontext", "", ""},
	} {
		var input any
		if tc.input != "" {
			input = v(t, tc.input)
		}
		if _, err := pluginRequest(tc.action, tc.key, tc.name, input, tc.input != ""); err == nil {
			t.Errorf("%s %q %q %s accepted", tc.action, tc.key, tc.name, tc.input)
		}
	}
	for key, want := range map[string]bool{
		"rexcoding-yu_artdance": true,
		"1password":             true,
		"_sora":                 false,
		"-sora":                 false,
		strings.Repeat("a", 64): true,
		strings.Repeat("a", 65): false,
	} {
		if pluginKeyOK(key) != want {
			t.Errorf("pluginKeyOK(%q) = %v", key, !want)
		}
	}
}

func TestMetadataActionsMapToTheirControlRequests(t *testing.T) {
	req, err := metadataRequest("create", "", v(t, `{"key":"env"}`), true)
	if err != nil || req.Method != http.MethodPost || req.Path != "/control/v1/metadata/attributes" {
		t.Errorf("create = %+v, %v", req, err)
	}
	req, err = metadataRequest("delete", "env", nil, false)
	if err != nil || req.Method != http.MethodDelete || req.Path != "/control/v1/metadata/attributes/env" {
		t.Errorf("delete = %+v, %v", req, err)
	}
	req, err = metadataRequest("values", "project", nil, false)
	if err != nil || req.Method != http.MethodGet || req.Path != "/control/v1/metadata/attributes/project/values" {
		t.Errorf("values = %+v, %v", req, err)
	}
	for _, tc := range []struct{ action, key string }{
		{"delete", "env/../x"}, {"delete", "Env"}, {"update", "env"}, {"rename", "env"},
	} {
		if _, err := metadataRequest(tc.action, tc.key, nil, false); err == nil {
			t.Errorf("%s %q accepted", tc.action, tc.key)
		}
	}
}

func TestSessionMetadataAndPluginsMapToTheirControlRequests(t *testing.T) {
	base := "/control/v1/request-sessions/session_1/metadata"
	for _, tc := range []struct {
		key, input   string
		unbind       bool
		method, path string
	}{
		{"", "", false, http.MethodGet, base},
		{"project", `{"value":"astrlink"}`, false, http.MethodPut, base + "/project"},
		{"project", "", true, http.MethodDelete, base + "/project"},
	} {
		req, err := sessionMetadataRequest("session_1", tc.key, raw(tc.input), tc.unbind)
		if err != nil || req.Method != tc.method || req.Path != tc.path {
			t.Errorf("%q = %+v, %v", tc.key, req, err)
		}
	}
	if _, err := sessionMetadataRequest("session_1", "project", nil, false); err == nil {
		t.Error("a binding without its value was accepted")
	}
	if _, err := sessionMetadataRequest("session_1", "../x", nil, true); err == nil {
		t.Error("a traversal was accepted")
	}

	base = "/control/v1/request-sessions/session_1/plugins"
	for _, tc := range []struct {
		key, input   string
		refresh      bool
		method, path string
	}{
		{"", "", false, http.MethodGet, base},
		{"powercontext", `{"paused":true}`, false, http.MethodPut, base + "/powercontext"},
		{"powercontext", "", true, http.MethodPost, base + "/powercontext/refresh"},
	} {
		req, err := sessionPluginsRequest("session_1", tc.key, raw(tc.input), tc.refresh)
		if err != nil || req.Method != tc.method || req.Path != tc.path {
			t.Errorf("%q = %+v, %v", tc.key, req, err)
		}
	}
	if _, err := sessionPluginsRequest("session_1", "powercontext", nil, false); err == nil {
		t.Error("a pause without its value was accepted")
	}
	if _, err := sessionPluginsRequest("session_1", "../x", nil, true); err == nil {
		t.Error("a traversal was accepted")
	}
}

func TestAPluginChangeIsDecidedOnce(t *testing.T) {
	approvals := &PluginApprovals{}
	id := approvals.hold("gate", raw(`{"enable":true}`), pendingPluginChange{input: raw(`{"enabled":true}`)})
	if held := approvals.Current(); held == nil || held.Key != "gate" {
		t.Fatalf("current = %+v", held)
	}
	if _, ok := approvals.take("other"); ok {
		t.Error("another id took the change")
	}
	if _, ok := approvals.take(id); !ok {
		t.Error("the change was not taken")
	}
	if _, ok := approvals.take(id); ok {
		t.Error("the change was taken twice")
	}
}

func TestUntickedCapabilitiesAreLeftOut(t *testing.T) {
	got := narrowGrants(raw(`{"enabled":true,"granted_capabilities":["storage","network","ui.code"]}`),
		[]string{"network", "ui.code"}, []string{"network"})
	if !sameJSON(string(got), `{"enabled":true,"granted_capabilities":["storage","network"]}`) {
		t.Errorf("narrowed = %s", got)
	}
}

// approvalWindow is the confirmation window, recorded.
type approvalWindow struct {
	mu       sync.Mutex
	opened   []bool
	finished []PluginApprovalFinished
}

func (w *approvalWindow) Open(focus bool) error {
	w.mu.Lock()
	w.opened = append(w.opened, focus)
	w.mu.Unlock()
	return nil
}

func (w *approvalWindow) Decided(finished PluginApprovalFinished) {
	w.mu.Lock()
	w.finished = append(w.finished, finished)
	w.mu.Unlock()
}

type callerWindow struct{}

// fromWindow is a call made from the window labeled label.
func fromWindow(ctx context.Context, label string) context.Context {
	return context.WithValue(ctx, callerWindow{}, label)
}

func windowOf(ctx context.Context) string {
	label, _ := ctx.Value(callerWindow{}).(string)
	return label
}

func TestAPluginChangeThatGrantsMoreWaitsForTheConfirmationWindow(t *testing.T) {
	core := newFakeCore(t)
	window := &approvalWindow{}
	plugins := Plugins{API: &API{Caller: core}, Approvals: &PluginApprovals{}, Owner: windowOf, Window: window}
	main := fromWindow(t.Context(), "main")
	confirm := fromWindow(t.Context(), PluginApprovalLabel)

	approval := `{"plugin":"gate","enable":true,"added_capabilities":["network","ui.code"]}`
	core.answer(http.StatusConflict, "", `{"error":{"code":"approval_required","message":"confirm"},"approval":`+approval+`}`)
	settings := `{"enabled":true,"granted_capabilities":["storage","network","ui.code"]}`
	_, err := plugins.PluginAction(main, "update", "gate", "", raw(settings))
	if err == nil || err.Error() != PluginApprovalPendingError {
		t.Fatalf("update = %v, want the pending error", err)
	}
	if seen := core.seen(); len(seen) != 1 || seen[0].approval != "" {
		t.Fatalf("an unconfirmed change carried the confirmation: %+v", seen)
	}
	if len(window.opened) != 1 || window.opened[0] {
		t.Fatalf("window opened = %v, want once without focus", window.opened)
	}

	// The main window can neither read nor decide the change.
	if _, err := plugins.PluginApprovalPending(main); err == nil {
		t.Error("the main window read the change")
	}
	pending, err := plugins.PluginApprovalPending(confirm)
	if err != nil || pending == nil || pending.Key != "gate" || !sameJSON(string(pending.Approval), approval) {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	encoded, _ := json.Marshal(pending)
	if strings.Contains(string(encoded), "storage") {
		t.Errorf("the window was handed the change itself: %s", encoded)
	}
	if err := plugins.PluginApprovalDecide(main, pending.ID, true, nil); err == nil {
		t.Fatal("the main window confirmed a change")
	}

	core.answer(http.StatusOK, "", `{"key":"gate"}`)
	kept := []string{"network"}
	if err := plugins.PluginApprovalDecide(confirm, pending.ID, true, &kept); err != nil {
		t.Fatalf("decide: %v", err)
	}
	seen := core.seen()
	if len(seen) != 1 || seen[0].method != http.MethodPatch || seen[0].path != "/control/v1/plugins/gate" ||
		seen[0].approval != "confirmed" ||
		!sameJSON(seen[0].body, `{"enabled":true,"granted_capabilities":["storage","network"]}`) {
		t.Fatalf("confirmed change = %+v", seen)
	}
	if len(window.finished) != 1 || !window.finished[0].Approved || window.finished[0].Error != nil ||
		window.finished[0].Key != "gate" || window.finished[0].ID != pending.ID {
		t.Errorf("finished = %+v", window.finished)
	}
	if err := plugins.PluginApprovalDecide(confirm, pending.ID, true, nil); err == nil {
		t.Error("the change was decided twice")
	}
	if opened, err := plugins.OpenPluginApproval(); opened || err != nil {
		t.Errorf("open with nothing waiting = %v, %v", opened, err)
	}
}

func TestAnInstallFromSourceWaitsAndARefusalComesBackWhole(t *testing.T) {
	core := newFakeCore(t)
	window := &approvalWindow{}
	plugins := Plugins{API: &API{Caller: core}, Approvals: &PluginApprovals{}, Owner: windowOf, Window: window}
	main := fromWindow(t.Context(), "main")
	confirm := fromWindow(t.Context(), PluginApprovalLabel)

	core.answer(http.StatusConflict, "", `{"error":{"code":"approval_required"},"approval":{"plugin":"hello","install":true}}`)
	if _, err := plugins.PluginAction(main, "source_install", "", "", raw(`{"token":"staged"}`)); err == nil ||
		err.Error() != PluginApprovalPendingError {
		t.Fatalf("install = %v", err)
	}
	if opened, err := plugins.OpenPluginApproval(); !opened || err != nil {
		t.Fatalf("open = %v, %v", opened, err)
	}
	if len(window.opened) != 2 || !window.opened[1] {
		t.Errorf("window opened = %v, want the second with focus", window.opened)
	}
	pending, _ := plugins.PluginApprovalPending(confirm)
	if pending == nil || pending.Key != "hello" {
		t.Fatalf("pending = %+v", pending)
	}

	// A change declined sends nothing.
	core.answer(http.StatusOK, "", `{}`)
	if err := plugins.PluginApprovalDecide(confirm, pending.ID, false, nil); err != nil {
		t.Fatal(err)
	}
	if seen := core.seen(); len(seen) != 0 {
		t.Errorf("a declined change was sent: %+v", seen)
	}
	if len(window.finished) != 1 || window.finished[0].Approved {
		t.Errorf("finished = %+v", window.finished)
	}

	// Why an install cannot go on comes back as Core's JSON.
	core.answer(http.StatusUnprocessableEntity, "", `{"error":{"code":"build_failed"},"failure":{"stage":"build"}}`)
	_, err := plugins.PluginAction(main, "source_preview", "", "", raw(`{"url":"https://github.com/acme/hello"}`))
	if err == nil || !sameJSON(err.Error(), `{"error":{"code":"build_failed"},"failure":{"stage":"build"}}`) {
		t.Errorf("refusal = %v", err)
	}
}

func TestPluginInterfaceCodeIsServedToTheMainWindowOnly(t *testing.T) {
	for path, want := range map[string]bool{
		"/notes/client.js":         true,
		"/notes/client.js.map":     true,
		"/ollama_rerank/client.js": true,
		"/1panel/client.js":        true,
		"/notes/plugin.js":         false,
		"/../client.js":            false,
		"/Notes/client.js":         false,
		"/notes":                   false,
	} {
		if _, _, ok := pluginClientFile(path); ok != want {
			t.Errorf("%s served = %v", path, ok)
		}
	}

	core := newFakeCore(t)
	core.answer(http.StatusOK, "", `window.__astrlinkPlugins.define("notes", () => {})`)
	handler := PluginClientHandler(core, windowOf)
	serve := func(label, method, path string) *httptest.ResponseRecorder {
		request := httptest.NewRequestWithContext(fromWindow(t.Context(), label), method, "astrlink-plugin://localhost"+path, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	got := serve("main", http.MethodGet, "/notes/client.js")
	if got.Code != http.StatusOK || got.Header().Get("Content-Type") != "text/javascript; charset=utf-8" ||
		!strings.Contains(got.Body.String(), "define") {
		t.Fatalf("client.js = %d %q %q", got.Code, got.Header().Get("Content-Type"), got.Body.String())
	}
	if seen := core.seen(); len(seen) != 1 || seen[0].path != "/control/v1/plugins/notes/client.js" {
		t.Errorf("Core was asked %+v", seen)
	}
	for _, tc := range []struct {
		label, method, path string
		status              int
	}{
		{PluginApprovalLabel, http.MethodGet, "/notes/client.js", http.StatusForbidden},
		{"main", http.MethodPost, "/notes/client.js", http.StatusForbidden},
		{"main", http.MethodGet, "/notes/plugin.js", http.StatusNotFound},
	} {
		if got := serve(tc.label, tc.method, tc.path); got.Code != tc.status {
			t.Errorf("%s %s %s = %d, want %d", tc.label, tc.method, tc.path, got.Code, tc.status)
		}
	}
	core.answer(http.StatusNotFound, "", `{"error":{"code":"not_found"}}`)
	if got := serve("main", http.MethodGet, "/notes/client.js"); got.Code != http.StatusNotFound {
		t.Errorf("a plugin Core does not serve = %d", got.Code)
	}
}

func TestMetadataCommandsSendTheirRequest(t *testing.T) {
	core := newFakeCore(t)
	metadata := Metadata{API: &API{Caller: core}}
	plugins := Plugins{API: &API{Caller: core}, Approvals: &PluginApprovals{}, Owner: windowOf, Window: &approvalWindow{}}
	ctx := t.Context()
	for _, tc := range []struct {
		name               string
		call               func() (json.RawMessage, error)
		method, path, body string
	}{
		{"metadata list", func() (json.RawMessage, error) { return metadata.MetadataAction(ctx, "list", "", nil) },
			http.MethodGet, "/control/v1/metadata/attributes", ""},
		{"metadata update", func() (json.RawMessage, error) {
			return metadata.MetadataAction(ctx, "update", "env", raw(`{"label":"Env"}`))
		}, http.MethodPatch, "/control/v1/metadata/attributes/env", `{"label":"Env"}`},
		{"token metadata", func() (json.RawMessage, error) { return metadata.AccessTokenMetadata(ctx, "token_1", nil) },
			http.MethodGet, "/control/v1/access-tokens/token_1/metadata", ""},
		{"token metadata replace", func() (json.RawMessage, error) {
			return metadata.AccessTokenMetadata(ctx, "token_1", raw(`{"project":"a"}`))
		}, http.MethodPut, "/control/v1/access-tokens/token_1/metadata", `{"project":"a"}`},
		{"session metadata unbind", func() (json.RawMessage, error) {
			return metadata.SessionMetadata(ctx, "session_1", "project", nil, true)
		}, http.MethodDelete, "/control/v1/request-sessions/session_1/metadata/project", ""},
		{"session plugins refresh", func() (json.RawMessage, error) {
			return plugins.SessionPlugins(ctx, "session_1", "powercontext", nil, true)
		}, http.MethodPost, "/control/v1/request-sessions/session_1/plugins/powercontext/refresh", ""},
		{"plugin get", func() (json.RawMessage, error) { return plugins.PluginAction(ctx, "get", "notes", "", nil) },
			http.MethodGet, "/control/v1/plugins/notes", ""},
	} {
		core.answer(http.StatusOK, "", `{"ok":true}`)
		if _, err := tc.call(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		seen := core.seen()
		if len(seen) != 1 || seen[0].method != tc.method || seen[0].path != tc.path ||
			tc.body != "" && !sameJSON(seen[0].body, tc.body) || seen[0].approval != "" {
			t.Errorf("%s sent %+v", tc.name, seen)
		}
	}
	// A deletion Core answers without a body reads as null.
	core.answer(http.StatusNoContent, "", "")
	if got, err := metadata.MetadataAction(ctx, "delete", "env", nil); err != nil || string(got) != "null" {
		t.Errorf("delete = %s, %v", got, err)
	}
	for name, call := range map[string]func() error{
		"token traversal":   func() error { _, err := metadata.AccessTokenMetadata(ctx, "../x", nil); return err },
		"session traversal": func() error { _, err := metadata.SessionMetadata(ctx, "../x", "", nil, false); return err },
		"plugins traversal": func() error { _, err := plugins.SessionPlugins(ctx, "../x", "", nil, false); return err },
	} {
		core.answer(http.StatusOK, "", `{}`)
		if call() == nil || len(core.seen()) != 0 {
			t.Errorf("%s reached Core", name)
		}
	}
}
