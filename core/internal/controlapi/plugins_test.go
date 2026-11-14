package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/metadata"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/plugin/hostapi"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

const demoPlugin = `export const meta = {
  apiVersion: 1,
  key: "demo",
  name: "Demo memory",
  version: "1.0.0",
  capabilities: ["context.inject", "network", "storage", "ui.views"],
  network: { purpose: "Reach the memory server the user allows" },
  credentials: [{ name: "token", label: "Memory token", header: "Authorization", scheme: "Bearer" }],
  metadata: [{ key: "project", label: "Project", pattern: "^[a-z0-9][a-z0-9._-]{0,63}$", marker: "memory-project" }],
  notice: "Asks for a project name in new sessions.",
};
export function testConnection() {
  const config = astrlink.config.get();
  const info = astrlink.http.fetch({ method: "GET", url: config.serverUrl + "/v1/server-info", credential: "token", timeoutMs: 3000 });
  if (info.status !== 200) return { status: "error", summary: info.error ?? "HTTP " + info.status };
  astrlink.store.set("server", info.json.package_version);
  return { status: "ok", summary: "Memory " + info.json.package_version, view: [{ type: "kv", items: [{ label: "Version", value: info.json.package_version }] }] };
}
export function describeStatus() {
  return [{ type: "table", columns: ["Key", "Value"], rows: astrlink.store.keys().map((key) => [key, String(astrlink.store.get(key))]) }];
}
`

func newPluginHandler(t *testing.T) (*sqlite.Store, *Handler, *plugin.Manager, string) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	directory := t.TempDir()
	writePlugin(t, directory, "demo", "plugin.js", demoPlugin)
	writePlugin(t, directory, "broken", "plugin.ts", "export const meta = { apiVersion: 1, key: \"broken\", name: \"Broken\", version: \"1.0.0\", capabilities: [] };\nexport async function prepareTurn() {}\n")
	manager := plugin.NewManager(plugin.ManagerOptions{
		Dir:      directory,
		Settings: hostapi.Settings{Store: store},
		Services: plugin.Services{Fetcher: &hostapi.Fetcher{Credentials: store}, Store: hostapi.Store{Values: store}},
	})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	registry := metadata.NewRegistry(store)
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	tokens, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		ServiceStore: store, ControlToken: testControlToken, ObserverToken: testObserverToken, AccessTokenManager: tokens,
		Plugins: manager, PluginStore: store, Metadata: registry, MetadataStore: store,
	})
	if err != nil {
		t.Fatalf("NewWithDependencies: %v", err)
	}
	return store, handler, manager, directory
}

func writePlugin(t *testing.T, directory, key, file, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(directory, key), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, key, file), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func decodeBody[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode %s: %v", response.Body.String(), err)
	}
	return value
}

func TestPluginListShowsLoadedAndFailedPlugins(t *testing.T) {
	_, handler, _, directory := newPluginHandler(t)
	response := controlRequest(t, handler, http.MethodGet, PluginsPath, "", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list = %d %s", response.Code, response.Body.String())
	}
	list := decodeBody[struct {
		Directory string                   `json:"directory"`
		Plugins   []contract.PluginSummary `json:"plugins"`
	}](t, response)
	if list.Directory != directory || len(list.Plugins) != 2 {
		t.Fatalf("list = %+v", list)
	}
	broken, demo := list.Plugins[0], list.Plugins[1]
	if broken.Key != "broken" || broken.State != contract.PluginStateFailed || broken.Error == nil || broken.Error.Code != plugin.CodeAsync || broken.Error.Line != 2 || broken.Error.File != "plugin.ts" {
		t.Fatalf("broken = %+v %+v", broken, broken.Error)
	}
	if demo.Key != "demo" || demo.State != contract.PluginStateLoaded || demo.Name != "Demo memory" || demo.Source != contract.PluginSourceDirectory || demo.Enabled || !demo.NeedsNetwork || !slices.Equal(demo.GrantedCapabilities, demo.RequestedCapabilities) {
		t.Fatalf("demo = %+v", demo)
	}
}

func TestPluginSettingsCredentialsAndConnectionTest(t *testing.T) {
	store, handler, _, _ := newPluginHandler(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer memory-secret-0123456789abcd" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = writer.Write([]byte(`{"package_version":"1.3.0"}`))
	}))
	defer server.Close()
	path := PluginsPath + "/demo"

	detail := decodeBody[contract.PluginDetail](t, controlRequest(t, handler, http.MethodGet, path, "", "", ""))
	if string(detail.Config) != "{}" || len(detail.Credentials) != 1 || detail.Credentials[0].Configured || detail.NetworkPurpose == "" || detail.Notice == "" || len(detail.Metadata) != 1 ||
		strings.Join(detail.Hooks, ",") != "describeStatus,testConnection" || detail.TurnWaitMs != contract.DefaultPluginTurnWaitMs {
		t.Fatalf("detail = %+v", detail)
	}
	for _, body := range []string{
		`{"granted_capabilities":["filesystem"]}`,
		`{"network_origins":[{"origin":"ftp://memory.test"}]}`,
		`{"config":[1,2]}`,
		`{"config":null}`,
		`{"turn_wait_ms":999}`,
		`{"turn_wait_ms":5001}`,
	} {
		if response := approvedRequest(t, handler, http.MethodPatch, path, "application/json", body, ""); response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("PATCH %s = %d %s", body, response.Code, response.Body.String())
		}
	}
	if response := approvedRequest(t, handler, http.MethodPatch, PluginsPath+"/broken", "application/json", `{"enabled":true}`, ""); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("enabling a failed plugin = %d", response.Code)
	}
	update := `{"enabled":true,"granted_capabilities":["network","storage","ui.views","network"],"network_origins":[{"origin":"` + server.URL + `/ignored/path","allow_plaintext":true}],"turn_wait_ms":3500,"config":{"serverUrl":"` + server.URL + `"}}`
	response := approvedRequest(t, handler, http.MethodPatch, path, "application/json", update, "")
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH = %d %s", response.Code, response.Body.String())
	}
	detail = decodeBody[contract.PluginDetail](t, response)
	origin, _ := plugin.NormalizeOrigin(server.URL)
	if !detail.Enabled || strings.Join(detail.GrantedCapabilities, ",") != "context.inject,network,storage,ui.views" || len(detail.NetworkOrigins) != 1 || detail.NetworkOrigins[0] != (contract.PluginNetworkOrigin{Origin: origin}) || detail.TurnWaitMs != 3500 {
		t.Fatalf("detail after PATCH = %+v", detail)
	}
	if loaded := handler.plugins.Current().Plugin("demo"); loaded == nil || loaded.Settings.Wait() != 3500*time.Millisecond {
		t.Fatalf("the plugin waits %v", loaded.Settings.Wait())
	}

	credential := path + "/credentials/token"
	if response := controlRequest(t, handler, http.MethodPut, credential, "application/json", `{"secret":"memory-secret-0123456789abcd","origin":"https://elsewhere.test"}`, ""); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("credential for an origin the plugin may not reach = %d", response.Code)
	}
	if response := controlRequest(t, handler, http.MethodPut, path+"/credentials/missing", "application/json", `{"secret":"x","origin":"`+server.URL+`"}`, ""); response.Code != http.StatusNotFound {
		t.Fatalf("undeclared credential = %d", response.Code)
	}
	response = controlRequest(t, handler, http.MethodPut, credential, "application/json", `{"secret":"memory-secret-0123456789abcd","origin":"`+server.URL+`"}`, "")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "memory-secret") {
		t.Fatalf("PUT credential = %d %s", response.Code, response.Body.String())
	}
	if detail = decodeBody[contract.PluginDetail](t, response); !detail.Credentials[0].Configured || detail.Credentials[0].Hint != "abcd" || detail.Credentials[0].Origin != origin {
		t.Fatalf("credential status = %+v", detail.Credentials[0])
	}

	response = controlRequest(t, handler, http.MethodPost, path+"/test", "", "", "")
	result := decodeBody[contract.PluginTestResult](t, response)
	if response.Code != http.StatusOK || result.Status != contract.PluginTestOK || result.Summary != "Memory 1.3.0" || len(result.View) != 1 || result.Network.Requests != 1 {
		t.Fatalf("test = %d %s", response.Code, response.Body.String())
	}
	if value, found, _ := store.GetPluginValue(context.Background(), "demo", "server"); !found || string(value) != `"1.3.0"` {
		t.Fatalf("stored server = %s, %v", value, found)
	}
	// The counts are read before the overview runs, so it does not count
	// its own call.
	response = controlRequest(t, handler, http.MethodGet, path+"/status", "", "", "")
	status := decodeBody[contract.PluginStatus](t, response)
	if response.Code != http.StatusOK || status.Calls != 1 || status.Network.Requests != 1 || len(status.View) != 1 || status.View[0].Rows[0][0].Text != "server" {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}

	if response := controlRequest(t, handler, http.MethodDelete, credential, "", "", ""); response.Code != http.StatusOK {
		t.Fatalf("DELETE credential = %d", response.Code)
	}
	response = controlRequest(t, handler, http.MethodPost, path+"/test", "", "", "")
	if result = decodeBody[contract.PluginTestResult](t, response); result.Status != contract.PluginTestError || result.Summary != "denied" {
		t.Fatalf("test without the credential = %s", response.Body.String())
	}
}

func TestPluginRoutesKeepPluginCodeFromObservers(t *testing.T) {
	_, handler, _, _ := newPluginHandler(t)
	call := func(method, path string) int {
		request := httptest.NewRequest(method, path, nil)
		request.Header.Set("Authorization", "Bearer "+testObserverToken)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code
	}
	if code := call(http.MethodGet, PluginsPath); code != http.StatusOK {
		t.Fatalf("observer list = %d", code)
	}
	if code := call(http.MethodGet, PluginsPath+"/demo"); code != http.StatusOK {
		t.Fatalf("observer detail = %d", code)
	}
	for _, route := range [][2]string{{http.MethodGet, PluginsPath + "/demo/status"}, {http.MethodPost, PluginsPath + "/demo/test"}, {http.MethodPatch, PluginsPath + "/demo"}, {http.MethodPost, PluginsPath + "/reload"}} {
		if code := call(route[0], route[1]); code != http.StatusForbidden {
			t.Fatalf("observer %s %s = %d", route[0], route[1], code)
		}
	}
}

func TestPluginReloadFindsNewPlugins(t *testing.T) {
	_, handler, _, directory := newPluginHandler(t)
	writePlugin(t, directory, "team-rules", "plugin.js", `export const meta = { apiVersion: 1, key: "team-rules", name: "Team rules", version: "0.0.2", capabilities: [] };`)
	response := controlRequest(t, handler, http.MethodPost, PluginsPath+"/reload", "", "", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"key":"team-rules"`) {
		t.Fatalf("reload = %d %s", response.Code, response.Body.String())
	}
	if response := controlRequest(t, handler, http.MethodGet, PluginsPath+"/missing", "", "", ""); response.Code != http.StatusNotFound {
		t.Fatalf("missing plugin = %d", response.Code)
	}
}

const memoSettingsPlugin = `export const meta = {
  apiVersion: 1, key: "memo", name: "Memo", version: "1.0.0",
  capabilities: ["network", "storage", "ui.views"],
  network: { purpose: "Reach the memo server" },
  credentials: [{ name: "token", label: "Memo token", header: "Authorization", scheme: "Bearer" }],
  metadata: [{ key: "topic", label: "Topic", pattern: "^[a-z]+$", marker: "memo-topic" }],
  settings: [
    { key: "serverUrl", label: "Server", type: "server", group: "Connection", required: true, credential: "token" },
    { key: "quotas.atomic", label: "Atomic", type: "number", group: "Recall", row: "Per turn", default: 4, min: 0, max: 20, unit: "items" },
    { key: "$wait", group: "Recall" },
    { key: "$origins", advanced: true },
  ],
  credits: [{ name: "Memo server", owner: "Example", license: "Apache-2.0", url: "https://example.com/memo" }],
};
export function testConnection() { return { status: "ok", summary: "Memo is up" }; }
export function describeStatus() {
  return [{ type: "kv", items: [{ label: "config", value: JSON.stringify(astrlink.config.get()) }] }];
}
`

func TestPluginSettingsFormSavesServersAndChecksValues(t *testing.T) {
	store, handler, manager, directory := newPluginHandler(t)
	writePlugin(t, directory, "memo", "plugin.js", memoSettingsPlugin)
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := PluginsPath + "/memo"
	patch := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		return approvedRequest(t, handler, http.MethodPatch, path, "application/json", body, "")
	}

	detail := decodeBody[contract.PluginDetail](t, controlRequest(t, handler, http.MethodGet, path, "", "", ""))
	if len(detail.Settings) != 4 || detail.Settings[0].Type != plugin.SettingServer || detail.Settings[0].Credential != "token" ||
		detail.Settings[1].Row != "Per turn" || string(detail.Settings[1].Default) != "4" || *detail.Settings[1].Max != 20 || detail.Settings[2].Key != plugin.HostSettingWait {
		t.Fatalf("settings = %+v", detail.Settings)
	}
	if len(detail.Credits) != 1 || detail.Credits[0] != (contract.PluginCredit{Name: "Memo server", Owner: "Example", License: "Apache-2.0", URL: "https://example.com/memo"}) {
		t.Fatalf("credits = %+v", detail.Credits)
	}

	// Values that do not suit their settings are refused, naming the setting.
	for body, want := range map[string]string{
		`{"config":{}}`: "serverUrl",
		`{"config":{"serverUrl":"https://memory.test:8443/api","quotas":{"atomic":25}}}`: "quotas.atomic",
		`{"config":{"serverUrl":"ftp://memory.test"}}`:                                   "serverUrl",
	} {
		if response := patch(body); response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), want) {
			t.Fatalf("PATCH %s = %d %s", body, response.Code, response.Body.String())
		}
	}

	// A server's origin joins the user's own, marked with its setting.
	response := patch(`{"config":{"serverUrl":"https://memory.test:8443/api"},"network_origins":[{"origin":"https://extra.test"}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH = %d %s", response.Code, response.Body.String())
	}
	detail = decodeBody[contract.PluginDetail](t, response)
	if len(detail.NetworkOrigins) != 2 || detail.NetworkOrigins[0].Origin != "https://extra.test:443" || detail.NetworkOrigins[0].Setting != "" ||
		detail.NetworkOrigins[1] != (contract.PluginNetworkOrigin{Origin: "https://memory.test:8443", Setting: "serverUrl"}) {
		t.Fatalf("origins = %+v", detail.NetworkOrigins)
	}
	if response := controlRequest(t, handler, http.MethodPut, path+"/credentials/token", "application/json", `{"secret":"memo-secret-0123456789","origin":"https://memory.test:8443"}`, ""); response.Code != http.StatusOK {
		t.Fatalf("PUT credential = %d %s", response.Code, response.Body.String())
	}

	// Plain http beyond this machine needs consent; with it the credential
	// follows the server and the old origin goes.
	plain := `{"config":{"serverUrl":"http://192.0.2.10:9090"}}`
	if response := patch(plain); response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "plaintext_consent_required") {
		t.Fatalf("PATCH without consent = %d %s", response.Code, response.Body.String())
	}
	response = patch(`{"config":{"serverUrl":"http://192.0.2.10:9090"},"plaintext_consent":["http://192.0.2.10:9090/"]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH with consent = %d %s", response.Code, response.Body.String())
	}
	detail = decodeBody[contract.PluginDetail](t, response)
	if len(detail.NetworkOrigins) != 2 || detail.NetworkOrigins[1] != (contract.PluginNetworkOrigin{Origin: "http://192.0.2.10:9090", AllowPlaintext: true, Plaintext: true, Setting: "serverUrl"}) ||
		detail.Credentials[0].Origin != "http://192.0.2.10:9090" || !detail.Credentials[0].Configured {
		t.Fatalf("after moving the server: origins = %+v, credential = %+v", detail.NetworkOrigins, detail.Credentials[0])
	}
	// Consent given once holds for later saves of the same server.
	if response := patch(`{"config":{"serverUrl":"http://192.0.2.10:9090","quotas":{"atomic":2}}}`); response.Code != http.StatusOK {
		t.Fatalf("PATCH again = %d %s", response.Code, response.Body.String())
	}

	// The overview: the plugin reads the defaults it lacks, the last test and
	// the values sessions are bound to.
	if response := controlRequest(t, handler, http.MethodPost, path+"/test", "", "", ""); response.Code != http.StatusOK {
		t.Fatalf("test = %d", response.Code)
	}
	// Enabling registers the attribute the binding is kept for.
	if response := patch(`{"enabled":true,"granted_capabilities":["network","storage","ui.views"]}`); response.Code != http.StatusOK {
		t.Fatalf("enable = %d %s", response.Code, response.Body.String())
	}
	now := time.Now().UTC()
	if err := store.PutMetadataValue(context.Background(), storage.MetadataValueRecord{
		TargetKind: storage.MetadataTargetSession, TargetID: "session_1", Key: "topic", Value: "paging",
		SetBy: contract.MetadataSetByManual, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	response = controlRequest(t, handler, http.MethodGet, path+"/status", "", "", "")
	status := decodeBody[contract.PluginStatus](t, response)
	if status.LastTest == nil || status.LastTest.Status != contract.PluginTestOK || status.LastTest.Summary != "Memo is up" || status.LastTest.TestedAt.IsZero() {
		t.Fatalf("last test = %+v", status.LastTest)
	}
	if len(status.Bindings) != 1 || status.Bindings[0].Key != "topic" || status.Bindings[0].Label != "Topic" ||
		len(status.Bindings[0].Values) != 1 || status.Bindings[0].Values[0].Value != "paging" || status.Bindings[0].Values[0].Sessions != 1 {
		t.Fatalf("bindings = %+v", status.Bindings)
	}
	if len(status.View) != 1 || len(status.View[0].Items) != 1 || !strings.Contains(status.View[0].Items[0].Value, `"atomic":2`) {
		t.Fatalf("view = %+v", status.View)
	}
}
