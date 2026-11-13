package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/metadata"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/plugin/hostapi"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

// loaderPlugin keeps a session's project in its system prompt and refreshes
// it from a record's button.
const loaderPlugin = `export const meta = {
  apiVersion: 1, key: "loader", name: "Loader", version: "1.0.0",
  capabilities: ["context.inject", "request.read", "storage", "ui.views"],
  metadata: [{ key: "project", label: "Project", pattern: "^[a-z0-9-]{1,32}$", marker: "memo-project" }],
  storage: [{ label: "Server ids of projects", retention: "Until the plugin is cleared" }],
};
export function prepareSession(view) {
  const project = view.meta.project;
  if (project === undefined) return undefined;
  astrlink.store.set("scope:" + project.value, "scp_1");
  return { system: "<ctx " + view.language + "> " + project.value };
}
export function onAction(view) {
  return { refresh: view.action === "refresh", message: "refreshing " + view.meta.project.value };
}
export function describeStatus(view) {
  return [{ type: "text", text: "language " + view.language }];
}
`

type pluginHostFixture struct {
	store     *sqlite.Store
	handler   *Handler
	host      *pluginhost.Host
	manager   *plugin.Manager
	directory string
}

func newPluginHostFixture(t *testing.T) pluginHostFixture {
	t.Helper()
	return buildPluginHostFixture(t, nil)
}

// buildPluginHostFixture enables the loader; the other plugins it is given
// start disabled.
func buildPluginHostFixture(t *testing.T, others map[string]string) pluginHostFixture {
	t.Helper()
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	directory := t.TempDir()
	writePlugin(t, directory, "loader", "plugin.js", loaderPlugin)
	for key, source := range others {
		writePlugin(t, directory, key, "plugin.js", source)
	}
	manager := plugin.NewManager(plugin.ManagerOptions{
		Dir: directory, Settings: hostapi.Settings{Store: store},
		Services: plugin.Services{Store: hostapi.Store{Values: store}, Metadata: hostapi.Metadata{Store: store}},
	})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	registry := metadata.NewRegistry(store)
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	host := pluginhost.New(pluginhost.Options{
		Plugins: manager, Metadata: registry, Bindings: store, Store: store, Records: store,
		WorkspaceKey: []byte("workspace key"), Logf: t.Logf,
	})
	t.Cleanup(host.Close)
	tokens, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		ServiceStore: store, RequestRecords: store, ControlToken: testControlToken, ObserverToken: testObserverToken,
		AccessTokenManager: tokens, Plugins: manager, PluginStore: store, Metadata: registry, MetadataStore: store, PluginHost: host,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response := approvedRequest(t, handler, http.MethodPatch, PluginsPath+"/loader", "application/json",
		`{"enabled":true,"granted_capabilities":["context.inject","request.read","storage","ui.views"]}`, ""); response.Code != http.StatusOK {
		t.Fatalf("enable = %d %s", response.Code, response.Body.String())
	}
	return pluginHostFixture{store: store, handler: handler, host: host, manager: manager, directory: directory}
}

// send runs one request of session through the plugins, as the gateway
// does, and keeps its record.
func (fixture pluginHostFixture) send(t *testing.T, session string, turn int, system, text string) []byte {
	t.Helper()
	body := `{"model":"claude","system":` + quote(system) + `,"messages":[{"role":"user","content":` + quote(text) + `}]}`
	run := fixture.host.Begin(context.Background(), pluginhost.Input{
		RequestID: contract.RequestID("request_" + session), Protocol: contract.ProtocolAnthropicMessages, Model: "claude",
		Client: "claude_code", Header: http.Header{}, Session: session, Turn: turn, Body: []byte(body),
	})
	if run == nil {
		t.Fatal("Begin = nil")
	}
	sent := run.Inject([]byte(body))
	run.Report()
	id, index := contract.SessionID(session), turn
	if err := fixture.store.InsertRequestRecord(context.Background(), contract.RequestRecord{
		ID: contract.RequestID("request_" + session + "_" + strconv.FormatInt(time.Now().UnixNano(), 10)), StartedAt: time.Now().UTC(), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolAnthropicMessages, SessionID: &id, TurnIndex: &index, Audit: contract.NotCapturedAuditSummary(),
	}); err != nil {
		t.Fatal(err)
	}
	return sent
}

func quote(text string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(text) + `"`
}

type sessionPluginList struct {
	Plugins []contract.SessionPlugin `json:"plugins"`
}

func TestPluginHostSettingsArePausedBudgetedAndTold(t *testing.T) {
	fixture := newPluginHostFixture(t)
	handler := fixture.handler
	if got := decodeBody[contract.PluginHostSettings](t, controlRequest(t, handler, http.MethodGet, PluginHostPath, "", "", "")); !reflect.DeepEqual(got, contract.DefaultPluginHostSettings()) {
		t.Fatalf("defaults = %+v", got)
	}
	response := approvedRequest(t, handler, http.MethodPatch, PluginHostPath, "application/json", `{"language":"ja","injection_budget_bytes":8192}`, "")
	if got := decodeBody[contract.PluginHostSettings](t, response); response.Code != http.StatusOK || got.Language != "ja" || got.InjectionBudgetBytes != 8192 || got.Paused {
		t.Fatalf("PATCH = %d %s", response.Code, response.Body.String())
	}
	for _, body := range []string{`{"language":"fr"}`, `{"injection_budget_bytes":100}`} {
		if response := approvedRequest(t, handler, http.MethodPatch, PluginHostPath, "application/json", body, ""); response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("PATCH %s = %d %s", body, response.Code, response.Body.String())
		}
	}
	if settings, err := fixture.store.PluginHostSettings(context.Background()); err != nil || settings.Language != "ja" {
		t.Fatalf("stored = %+v, %v", settings, err)
	}
	// The plugin page's own views are written in the interface language.
	status := decodeBody[contract.PluginStatus](t, controlRequest(t, handler, http.MethodGet, PluginsPath+"/loader/status", "", "", ""))
	if len(status.View) != 1 || status.View[0].Text != "language ja" {
		t.Fatalf("status view = %+v", status.View)
	}
	request := httptest.NewRequest(http.MethodPatch, PluginHostPath, strings.NewReader(`{"paused":true}`))
	request.Header.Set("Authorization", "Bearer "+testObserverToken)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("an observer paused the plugins: %d", recorder.Code)
	}
}

func TestPluginDetailShowsTheHostSwitchesAndWhatIsKept(t *testing.T) {
	fixture := newPluginHostFixture(t)
	handler := fixture.handler
	detail := decodeBody[contract.PluginDetail](t, controlRequest(t, handler, http.MethodGet, PluginsPath+"/loader", "", "", ""))
	if !detail.WorkspaceBinding || detail.AutoRefresh || detail.ExcludedClients == nil || len(detail.ExcludedClients) != 0 {
		t.Fatalf("switches = %+v", detail)
	}
	if len(detail.Storage) != 1 || detail.Storage[0].Label != "Server ids of projects" || detail.Storage[0].Content {
		t.Fatalf("storage = %+v", detail.Storage)
	}
	kinds := map[string]*int{}
	for _, kept := range detail.HostStorage {
		kinds[kept.Kind] = kept.Count
	}
	for _, kind := range []string{contract.PluginStoredSessionBindings, contract.PluginStoredWorkspaceBindings, contract.PluginStoredSessions,
		contract.PluginStoredRecords, contract.PluginStoredResidentBlock, contract.PluginStoredValues} {
		if _, found := kinds[kind]; !found {
			t.Fatalf("host storage lacks %s: %+v", kind, detail.HostStorage)
		}
	}
	if count := kinds[contract.PluginStoredWorkspaceBindings]; count == nil || *count != 0 {
		t.Fatalf("workspace bindings = %v", count)
	}
	response := approvedRequest(t, handler, http.MethodPatch, PluginsPath+"/loader", "application/json",
		`{"excluded_clients":["cursor","unknown","cursor"],"workspace_binding":false,"auto_refresh":true}`, "")
	detail = decodeBody[contract.PluginDetail](t, response)
	if response.Code != http.StatusOK || len(detail.ExcludedClients) != 2 || detail.WorkspaceBinding || !detail.AutoRefresh {
		t.Fatalf("PATCH = %d %s", response.Code, response.Body.String())
	}
	if response := approvedRequest(t, handler, http.MethodPatch, PluginsPath+"/loader", "application/json", `{"excluded_clients":["nobody"]}`, ""); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown client = %d %s", response.Code, response.Body.String())
	}
}

func TestASessionsPluginsArePausedRefreshedAndCleared(t *testing.T) {
	fixture := newPluginHostFixture(t)
	handler, ctx := fixture.handler, context.Background()
	system := "<env>\nWorking directory: /work/astrlink\n</env>"
	fixture.send(t, "session_1", 1, system, "<memo-project>astrlink</memo-project>")
	path := RequestSessionsPath + "/session_1/plugins"
	list := decodeBody[sessionPluginList](t, controlRequest(t, handler, http.MethodGet, path, "", "", ""))
	if len(list.Plugins) != 1 || list.Plugins[0].Block == nil || list.Plugins[0].Block.Text != "<ctx en> astrlink" ||
		list.Plugins[0].Block.Trigger != contract.PluginTriggerStart || list.Plugins[0].FirstTurn != 1 || list.Plugins[0].Paused {
		t.Fatalf("session plugins = %+v", list)
	}
	response := controlRequest(t, handler, http.MethodPost, path+"/loader/refresh", "", "", "")
	if list := decodeBody[sessionPluginList](t, response); response.Code != http.StatusOK || !list.Plugins[0].RefreshPending {
		t.Fatalf("refresh = %d %s", response.Code, response.Body.String())
	}
	response = controlRequest(t, handler, http.MethodPut, path+"/loader", "application/json", `{"paused":true}`, "")
	if list := decodeBody[sessionPluginList](t, response); response.Code != http.StatusOK || !list.Plugins[0].Paused {
		t.Fatalf("pause = %d %s", response.Code, response.Body.String())
	}
	if sent := fixture.send(t, "session_1", 2, system, "继续"); strings.Contains(string(sent), "<ctx") {
		t.Fatalf("a paused session got a block: %s", sent)
	}
	// A record's button runs the plugin's onAction for its session.
	response = controlRequest(t, handler, http.MethodPost, PluginsPath+"/loader/actions", "application/json", `{"action":"refresh","session":"session_1"}`, "")
	if result := decodeBody[contract.PluginActionResult](t, response); response.Code != http.StatusOK || !result.Refresh || result.Message != "refreshing astrlink" {
		t.Fatalf("action = %d %s", response.Code, response.Body.String())
	}
	for _, request := range [][3]string{
		{http.MethodPost, PluginsPath + "/loader/actions", `{"action":"Refresh!","session":"session_1"}`},
		{http.MethodPost, PluginsPath + "/loader/actions", `{"action":"refresh","session":"session_missing"}`},
		{http.MethodPut, RequestSessionsPath + "/session_missing/plugins/loader", `{"paused":true}`},
		{http.MethodPut, path + "/nobody", `{"paused":true}`},
		{http.MethodPost, path + "/loader/other", ``},
	} {
		if response := controlRequest(t, handler, request[0], request[1], "application/json", request[2], ""); response.Code < 400 {
			t.Fatalf("%s %s = %d %s", request[0], request[1], response.Code, response.Body.String())
		}
	}
	// What the host keeps for the plugin can be cleared.
	detail := decodeBody[contract.PluginDetail](t, controlRequest(t, handler, http.MethodGet, PluginsPath+"/loader", "", "", ""))
	for _, kept := range detail.HostStorage {
		if (kept.Kind == contract.PluginStoredWorkspaceBindings || kept.Kind == contract.PluginStoredValues) && (kept.Count == nil || *kept.Count != 1) {
			t.Fatalf("%s = %v", kept.Kind, kept.Count)
		}
	}
	for _, what := range []string{"workspaces", "values"} {
		response := controlRequest(t, handler, http.MethodDelete, PluginsPath+"/loader/"+what, "", "", "")
		if cleared := decodeBody[contract.PluginClearResult](t, response); response.Code != http.StatusOK || cleared.Removed != 1 {
			t.Fatalf("clear %s = %d %s", what, response.Code, response.Body.String())
		}
	}
	if values, _ := fixture.store.WorkspaceValues(ctx, "x"); len(values) != 0 {
		t.Fatalf("values = %+v", values)
	}
	request := httptest.NewRequest(http.MethodPut, path+"/loader", strings.NewReader(`{"paused":false}`))
	request.Header.Set("Authorization", "Bearer "+testObserverToken)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("an observer resumed a plugin: %d", recorder.Code)
	}
}

func TestChangingASessionsProjectCanMoveItsWorkspace(t *testing.T) {
	fixture := newPluginHostFixture(t)
	handler := fixture.handler
	system := "<env>\nWorking directory: /work/astrlink\n</env>"
	fixture.send(t, "session_1", 1, system, "帮我看看分页逻辑")
	path := RequestSessionsPath + "/session_1/metadata"
	got := decodeBody[contract.SessionMetadata](t, controlRequest(t, handler, http.MethodGet, path, "", "", ""))
	if got.Workspace == nil || got.Workspace.Name != "astrlink" || len(got.Workspace.Values) != 0 {
		t.Fatalf("metadata = %+v", got)
	}
	// The first project picked binds the workspace too.
	got = decodeBody[contract.SessionMetadata](t, controlRequest(t, handler, http.MethodPut, path+"/project", "application/json", `{"value":"astrlink"}`, ""))
	if got.Workspace == nil || got.Workspace.Values["project"] != "astrlink" {
		t.Fatalf("after the first pick = %+v", got.Workspace)
	}
	// A later pick moves only the session, unless the user says otherwise.
	got = decodeBody[contract.SessionMetadata](t, controlRequest(t, handler, http.MethodPut, path+"/project", "application/json", `{"value":"paseo"}`, ""))
	if got.Values["project"].Value != "paseo" || got.Workspace.Values["project"] != "astrlink" {
		t.Fatalf("after a second pick = %+v", got)
	}
	got = decodeBody[contract.SessionMetadata](t, controlRequest(t, handler, http.MethodPut, path+"/project", "application/json", `{"value":"paseo","workspace":true}`, ""))
	if got.Workspace.Values["project"] != "paseo" {
		t.Fatalf("after moving the workspace = %+v", got.Workspace)
	}
	// A new session there starts with the workspace's project.
	if sent := fixture.send(t, "session_2", 1, system, "继续"); !strings.Contains(string(sent), "<ctx en> paseo") {
		t.Fatalf("a new session in the workspace got %s", sent)
	}
	values := decodeBody[struct {
		Values []contract.PluginBindingValue `json:"values"`
	}](t, controlRequest(t, handler, http.MethodGet, MetadataAttributesPath+"/project/values", "", "", ""))
	if len(values.Values) != 1 || values.Values[0].Value != "paseo" || values.Values[0].Sessions != 2 {
		t.Fatalf("values = %+v", values)
	}
	if response := controlRequest(t, handler, http.MethodGet, MetadataAttributesPath+"/project/other", "", "", ""); response.Code != http.StatusNotFound {
		t.Fatalf("an unknown sub-resource = %d", response.Code)
	}
}

// finderPlugin offers the model a search of the session's repository.
const finderPlugin = `export const meta = {
  apiVersion: 1, key: "finder", name: "Finder", version: "1.0.0",
  capabilities: ["request.read", "model.tools"],
  metadata: [{ key: "repo", label: "Repository", pattern: "^[a-z0-9-]{1,32}$", marker: "memo-repo" }],
  tools: [{ name: "search", description: { en: "Search the repository's memory.", "zh-CN": "检索本仓库的记忆。" },
    params: [{ name: "query", type: "string", required: true, example: "what to look up" }] }],
};
export function callTool(view) { return { text: "found in " + view.meta.repo.value }; }
`

// sendShell runs a request of session that offers the model a shell, from
// this machine, and returns the lookup credential it carries.
func (fixture pluginHostFixture) sendShell(t *testing.T, session string) string {
	t.Helper()
	body := `{"model":"claude","tools":[{"name":"Bash","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"look it up"}]}`
	run := fixture.host.Begin(context.Background(), pluginhost.Input{
		RequestID: contract.RequestID("request_" + session), Protocol: contract.ProtocolAnthropicMessages, Model: "claude",
		Client: "claude_code", Header: http.Header{}, Session: session, Turn: 1, Body: []byte(body), LookupBase: "http://127.0.0.1:18317",
	})
	sent := string(run.Inject([]byte(body)))
	run.Report()
	at := strings.Index(sent, "Bearer mk-")
	if at < 0 {
		t.Fatalf("no credential in %s", sent)
	}
	return sent[at+len("Bearer ") : at+len("Bearer mk-")+32]
}

func (fixture pluginHostFixture) lookup(token string) int {
	return fixture.host.Lookup(context.Background(), pluginhost.LookupRequest{
		Plugin: "finder", Tool: "search", Token: token, Input: []byte(`{"query":"x"}`),
	}).Status
}

func TestLookupsAreListedSwitchedAndRevoked(t *testing.T) {
	fixture := buildPluginHostFixture(t, map[string]string{"finder": finderPlugin})
	handler := fixture.handler
	enable := `{"enabled":true,"granted_capabilities":["request.read","model.tools"]}`
	response := approvedRequest(t, handler, http.MethodPatch, PluginsPath+"/finder", "application/json", enable, "")
	detail := decodeBody[contract.PluginDetail](t, response)
	if response.Code != http.StatusOK || !detail.LookupEnabled || len(detail.Tools) != 1 ||
		detail.Tools[0] != (contract.PluginTool{Name: "search", Description: "Search the repository's memory."}) {
		t.Fatalf("detail = %d %s", response.Code, response.Body.String())
	}
	approvedRequest(t, handler, http.MethodPatch, PluginHostPath, "application/json", `{"language":"zh-CN"}`, "")
	detail = decodeBody[contract.PluginDetail](t, controlRequest(t, handler, http.MethodGet, PluginsPath+"/finder", "", "", ""))
	if detail.Tools[0].Description != "检索本仓库的记忆。" {
		t.Fatalf("tools in the interface language = %+v", detail.Tools)
	}
	// Bind the session, then look up under its credential.
	fixture.send(t, "session_1", 1, "", "<memo-repo>astrlink</memo-repo>")
	token := fixture.sendShell(t, "session_1")
	if status := fixture.lookup(token); status != http.StatusOK {
		t.Fatalf("lookup = %d", status)
	}
	// What is kept counts the credentials, which the plugin page voids.
	detail = decodeBody[contract.PluginDetail](t, controlRequest(t, handler, http.MethodGet, PluginsPath+"/finder", "", "", ""))
	if held := storedCount(detail, contract.PluginStoredLookupCredentials); held == nil || *held != 1 {
		t.Fatalf("host storage = %+v", detail.HostStorage)
	}
	response = controlRequest(t, handler, http.MethodDelete, PluginsPath+"/finder/lookup-credentials", "", "", "")
	if revoked := decodeBody[contract.PluginClearResult](t, response); response.Code != http.StatusOK || revoked.Removed != 1 {
		t.Fatalf("revoke = %d %s", response.Code, response.Body.String())
	}
	if status := fixture.lookup(token); status != http.StatusUnauthorized {
		t.Fatalf("a revoked credential = %d", status)
	}
	// Settings that leave the lookups open keep the credential.
	token = fixture.sendShell(t, "session_1")
	approvedRequest(t, handler, http.MethodPatch, PluginsPath+"/finder", "application/json", `{"turn_wait_ms":3000}`, "")
	if status := fixture.lookup(token); status != http.StatusOK {
		t.Fatalf("after other settings = %d", status)
	}
	// Turning the lookups off voids it; so does moving the session.
	response = approvedRequest(t, handler, http.MethodPatch, PluginsPath+"/finder", "application/json", `{"lookup_enabled":false}`, "")
	if detail := decodeBody[contract.PluginDetail](t, response); response.Code != http.StatusOK || detail.LookupEnabled {
		t.Fatalf("turn off = %d %s", response.Code, response.Body.String())
	}
	approvedRequest(t, handler, http.MethodPatch, PluginsPath+"/finder", "application/json", `{"lookup_enabled":true}`, "")
	if status := fixture.lookup(token); status != http.StatusUnauthorized {
		t.Fatalf("a credential of lookups turned off = %d", status)
	}
	token = fixture.sendShell(t, "session_1")
	path := RequestSessionsPath + "/session_1/metadata/repo"
	controlRequest(t, handler, http.MethodPut, path, "application/json", `{"value":"astrlink"}`, "")
	if status := fixture.lookup(token); status != http.StatusOK {
		t.Fatalf("the same value kept = %d", status)
	}
	controlRequest(t, handler, http.MethodPut, path, "application/json", `{"value":"paseo"}`, "")
	if status := fixture.lookup(token); status != http.StatusUnauthorized {
		t.Fatalf("a credential of the old value = %d", status)
	}
	token = fixture.sendShell(t, "session_1")
	if status := fixture.lookup(token); status != http.StatusOK {
		t.Fatalf("a new credential = %d", status)
	}
	approvedRequest(t, handler, http.MethodPatch, PluginsPath+"/finder", "application/json", `{"enabled":false}`, "")
	approvedRequest(t, handler, http.MethodPatch, PluginsPath+"/finder", "application/json", enable, "")
	if status := fixture.lookup(token); status != http.StatusUnauthorized {
		t.Fatalf("a credential of a disabled plugin = %d", status)
	}
	request := httptest.NewRequest(http.MethodDelete, PluginsPath+"/finder/lookup-credentials", nil)
	request.Header.Set("Authorization", "Bearer "+testObserverToken)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("an observer revoked the credentials: %d", recorder.Code)
	}
}

func storedCount(detail contract.PluginDetail, kind string) *int {
	for _, kept := range detail.HostStorage {
		if kept.Kind == kind {
			return kept.Count
		}
	}
	return nil
}

func TestPluginStatsSumTheRequestRecordsOfTheLastDays(t *testing.T) {
	fixture := newPluginHostFixture(t)
	handler, ctx := fixture.handler, context.Background()
	insert := func(id, session string, read int, metadata *contract.RequestMetadata, records []contract.RequestPluginRecord) {
		t.Helper()
		sessionID := contract.SessionID(session)
		if err := fixture.store.InsertRequestRecord(ctx, contract.RequestRecord{
			ID: contract.RequestID(id), StartedAt: time.Now().UTC(), Status: contract.RequestStatusSucceeded,
			InputProtocol: contract.ProtocolAnthropicMessages, SessionID: &sessionID, Audit: contract.NotCapturedAuditSummary(),
			Usage:    &contract.Usage{InputTokens: 1000, OutputTokens: 10, TotalTokens: 1010, CacheReadTokens: &read},
			Metadata: metadata, PluginRecords: records,
		}); err != nil {
			t.Fatal(err)
		}
	}
	insert("request_stats_1", "session_1", 500, &contract.RequestMetadata{
		Values: map[string]contract.MetadataValue{"project": {Value: "astrlink", Source: "session"}},
		Events: []contract.MetadataEvent{{Key: "project", Event: contract.MetadataEventBound, Value: "astrlink", SetBy: contract.MetadataSetByWorkspace}},
	}, []contract.RequestPluginRecord{
		{Plugin: "loader", PluginVersion: "1.0.0", Hook: plugin.HookPrepareSession, Title: "Loader", Status: contract.PluginRecordOK, Trigger: contract.PluginTriggerStart, ElapsedMs: 40,
			Injection: &contract.PluginInjection{Placement: contract.PluginPlacementSystem, InjectedBytes: 100}},
		{Plugin: "loader", PluginVersion: "1.0.0", Hook: plugin.HookObserveTurn, Title: "Loader", Status: contract.PluginRecordOK},
	})
	insert("request_stats_2", "session_2", 100, nil, nil)
	response := controlRequest(t, handler, http.MethodGet, PluginsPath+"/loader/stats", "", "", "")
	stats := decodeBody[contract.PluginStats](t, response)
	total := stats.Total
	if response.Code != http.StatusOK || len(stats.Days) != contract.DefaultPluginStatsDays || stats.Days[6].Date != time.Now().Format(time.DateOnly) ||
		total.Load.Count != 1 || total.Load.InjectedBytes != 100 || total.Write.OK != 1 || total.Binding.Workspace != 1 ||
		total.Cache.With != (contract.PluginCacheShare{Requests: 1, InputTokens: 1000, CacheReadTokens: 500}) ||
		total.Cache.Without != (contract.PluginCacheShare{Requests: 1, InputTokens: 1000, CacheReadTokens: 100}) {
		t.Fatalf("stats = %d %s", response.Code, response.Body.String())
	}
	if stats := decodeBody[contract.PluginStats](t, controlRequest(t, handler, http.MethodGet, PluginsPath+"/loader/stats?days=30", "", "", "")); len(stats.Days) != 30 {
		t.Fatalf("30 days = %d", len(stats.Days))
	}
	for _, days := range []string{"0", "31", "x"} {
		if response := controlRequest(t, handler, http.MethodGet, PluginsPath+"/loader/stats?days="+days, "", "", ""); response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("days=%s = %d", days, response.Code)
		}
	}
	// Numbers only: an observer reads them.
	request := httptest.NewRequest(http.MethodGet, PluginsPath+"/loader/stats", nil)
	request.Header.Set("Authorization", "Bearer "+testObserverToken)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("an observer = %d", recorder.Code)
	}
}
