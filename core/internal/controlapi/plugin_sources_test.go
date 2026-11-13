package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/plugininstall"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// fakeInstaller stands in for the installer: a preview of "good" stages
// the hello plugin, which Install writes into the plugin directory.
type fakeInstaller struct {
	t          *testing.T
	directory  string
	existing   *plugininstall.Existing
	installed  []string
	rolledBack []string
	removed    []string
}

const helloPlugin = `export const meta = { apiVersion: 2, key: "hello", name: "Hello", version: "1.1.0", capabilities: ["storage"] };
export async function prepareTurn() { return {}; }
`

func (fake *fakeInstaller) review() contract.PluginInstallReview {
	return contract.PluginInstallReview{
		Token: "token", Key: "hello", Name: "Hello", Version: "1.1.0", Capabilities: []string{"storage"},
		Source: contract.PluginInstallSource{Kind: "github", Repository: "acme/hello", Ref: "v1.1.0", RefKind: contract.PluginRefTag, Commit: "abc"},
	}
}

func (fake *fakeInstaller) Preview(_ context.Context, link string, existing func(string) *plugininstall.Existing) (contract.PluginInstallReview, error) {
	fake.existing = existing("demo")
	if link != "good" {
		return contract.PluginInstallReview{}, &plugininstall.Failure{PluginInstallFailure: contract.PluginInstallFailure{Reason: contract.PluginInstallNoLockfile}}
	}
	return fake.review(), nil
}

func (fake *fakeInstaller) Staged(token string) (contract.PluginInstallReview, bool) {
	return fake.review(), token == "token"
}

func (fake *fakeInstaller) Install(token string) (string, error) {
	fake.installed = append(fake.installed, token)
	writePlugin(fake.t, fake.directory, "hello", "plugin.js", helloPlugin)
	record, _ := json.Marshal(contract.PluginInstallRecord{Source: fake.review().Source, Version: "1.1.0", InstalledAt: "2026-10-23T12:00:00Z"})
	if err := os.WriteFile(filepath.Join(fake.directory, "hello", plugin.InstallRecord), record, 0o600); err != nil {
		fake.t.Fatal(err)
	}
	return "hello", nil
}

func (fake *fakeInstaller) Versions(key string) []contract.PluginInstalledVersion {
	if key != "hello" {
		return nil
	}
	return []contract.PluginInstalledVersion{{ID: "20261023T120000Z-0000abcd", Version: "1.0.0", ReplacedAt: "2026-10-23T12:00:00Z"}}
}

func (fake *fakeInstaller) RollbackReview(key, id string, current *plugininstall.Existing) (contract.PluginInstallReview, error) {
	if id != "20261023T120000Z-0000abcd" {
		return contract.PluginInstallReview{}, os.ErrNotExist
	}
	review := fake.review()
	review.Token, review.Version = "", "1.0.0"
	review.Changes = &contract.PluginInstallChanges{FromVersion: current.Version}
	return review, nil
}

func (fake *fakeInstaller) Rollback(key, id string) error {
	fake.rolledBack = append(fake.rolledBack, key+"/"+id)
	return nil
}

func (fake *fakeInstaller) Uninstall(key string) error {
	if _, err := os.Stat(filepath.Join(fake.directory, key, plugin.InstallRecord)); err != nil {
		return plugininstall.ErrNotInstalled
	}
	fake.removed = append(fake.removed, key)
	return os.RemoveAll(filepath.Join(fake.directory, key))
}

func (fake *fakeInstaller) CheckUpdate(_ context.Context, record contract.PluginInstallRecord) (contract.PluginUpdateCheck, error) {
	return contract.PluginUpdateCheck{Available: true, Ref: "v1.2.0", RefKind: contract.PluginRefTag, URL: "https://github.com/" + record.Source.Repository + "/tree/v1.2.0"}, nil
}

func (fake *fakeInstaller) Market(_ context.Context, index string, installed func(string) plugininstall.Installed) (contract.PluginMarket, error) {
	return contract.PluginMarket{Index: index, Name: "Test", Plugins: []contract.PluginMarketEntry{{Key: "sora", Name: "Sora", Latest: "1.1.0", Installed: installed("sora").Version}}}, nil
}

func (fake *fakeInstaller) FindByCode(_ context.Context, code string, installed func(string) plugininstall.Installed) (contract.PluginMarket, error) {
	if code != "12345678" {
		return contract.PluginMarket{}, &plugininstall.Failure{PluginInstallFailure: contract.PluginInstallFailure{Reason: contract.PluginInstallNoPlugin}}
	}
	return contract.PluginMarket{Index: contract.CommunityPluginResolve + "?code=" + code, Plugins: []contract.PluginMarketEntry{{Key: "someone_sora", Code: code, Installed: installed("someone_sora").Version}}}, nil
}

func (fake *fakeInstaller) MarketDoc(_ context.Context, index, key, version, doc string) (contract.PluginMarketDoc, error) {
	return contract.PluginMarketDoc{Markdown: "# " + key + " " + version + " " + doc, Language: "en"}, nil
}

func TestInstallingFromSourceWaitsForConfirmation(t *testing.T) {
	_, handler, manager, directory := newPluginHandler(t)
	fake := &fakeInstaller{t: t, directory: directory}
	handler.pluginInstaller = fake

	response := controlRequest(t, handler, http.MethodPost, PluginsPath+"/sources/preview", "application/json", `{"url":"bad"}`, "")
	failure := decodeBody[installFailureEnvelope](t, response)
	if response.Code != http.StatusUnprocessableEntity || failure.Failure.Reason != contract.PluginInstallNoLockfile || failure.Error.Code != "plugin_install_failed" {
		t.Fatalf("a preview that fails = %d %s", response.Code, response.Body.String())
	}
	response = controlRequest(t, handler, http.MethodPost, PluginsPath+"/sources/preview", "application/json", `{"url":"good"}`, "")
	if review := decodeBody[contract.PluginInstallReview](t, response); response.Code != http.StatusOK || review.Key != "hello" {
		t.Fatalf("preview = %d %s", response.Code, response.Body.String())
	}
	if fake.existing == nil || fake.existing.Name != "Demo memory" || !fake.existing.Loaded || fake.existing.Origin != string(plugin.OriginDirectory) {
		t.Fatalf("the preview compared with %+v", fake.existing)
	}
	// An observer reads, but never previews.
	if response := observerRequest(handler, http.MethodPost, PluginsPath+"/sources/preview", `{"url":"good"}`); response.Code != http.StatusForbidden {
		t.Fatalf("an observer's preview = %d", response.Code)
	}

	install := PluginsPath + "/sources/install"
	response = controlRequest(t, handler, http.MethodPost, install, "application/json", `{"token":"token"}`, "")
	envelope := decodeBody[approvalEnvelope](t, response)
	if response.Code != http.StatusConflict || envelope.Error.Code != "approval_required" || envelope.Approval.Install == nil ||
		envelope.Approval.Install.Source.Repository != "acme/hello" || envelope.Approval.Plugin != "hello" {
		t.Fatalf("install without confirmation = %d %s", response.Code, response.Body.String())
	}
	if len(fake.installed) != 0 {
		t.Fatal("installed without confirmation")
	}
	if response := approvedRequest(t, handler, http.MethodPost, install, "application/json", `{"token":"old"}`, ""); response.Code != http.StatusGone {
		t.Fatalf("an expired token = %d %s", response.Code, response.Body.String())
	}
	response = approvedRequest(t, handler, http.MethodPost, install, "application/json", `{"token":"token"}`, "")
	detail := decodeBody[contract.PluginDetail](t, response)
	if response.Code != http.StatusOK || detail.Key != "hello" || detail.Installed == nil || detail.Installed.Ref != "v1.1.0" || len(detail.Versions) != 1 {
		t.Fatalf("approved install = %d %s", response.Code, response.Body.String())
	}
	if manager.Current().Plugin("hello") == nil {
		t.Fatal("the registry did not load the plugin installed")
	}

	response = controlRequest(t, handler, http.MethodPost, PluginsPath+"/hello/source/check", "", "", "")
	if check := decodeBody[contract.PluginUpdateCheck](t, response); response.Code != http.StatusOK || !check.Available || check.Ref != "v1.2.0" {
		t.Fatalf("check = %d %s", response.Code, response.Body.String())
	}
	if response := controlRequest(t, handler, http.MethodPost, PluginsPath+"/demo/source/check", "", "", ""); response.Code != http.StatusConflict {
		t.Fatalf("check of a plugin not installed from source = %d", response.Code)
	}

	rollback := PluginsPath + "/hello/source/rollback"
	body := `{"version":"20261023T120000Z-0000abcd"}`
	response = controlRequest(t, handler, http.MethodPost, rollback, "application/json", body, "")
	envelope = decodeBody[approvalEnvelope](t, response)
	if response.Code != http.StatusConflict || envelope.Approval.Install == nil || envelope.Approval.Install.Version != "1.0.0" || !envelope.Approval.Rollback ||
		envelope.Approval.Install.Changes.FromVersion != "1.1.0" {
		t.Fatalf("rollback without confirmation = %d %s", response.Code, response.Body.String())
	}
	if response := approvedRequest(t, handler, http.MethodPost, rollback, "application/json", `{"version":"nope"}`, ""); response.Code != http.StatusNotFound {
		t.Fatalf("rollback to no version = %d", response.Code)
	}
	if response := approvedRequest(t, handler, http.MethodPost, rollback, "application/json", body, ""); response.Code != http.StatusOK || len(fake.rolledBack) != 1 {
		t.Fatalf("approved rollback = %d %s", response.Code, response.Body.String())
	}

	if response := controlRequest(t, handler, http.MethodDelete, PluginsPath+"/demo/source", "", "", ""); response.Code != http.StatusConflict {
		t.Fatalf("uninstall of a plugin not installed from source = %d", response.Code)
	}
	response = controlRequest(t, handler, http.MethodDelete, PluginsPath+"/hello/source", "", "", "")
	if response.Code != http.StatusOK || manager.Current().Plugin("hello") != nil {
		t.Fatalf("uninstall = %d %s", response.Code, response.Body.String())
	}
}

func TestAPluginNewHereStartsDisabledWhateverItsKeyLeftBehind(t *testing.T) {
	store, handler, manager, directory := newPluginHandler(t)
	fake := &fakeInstaller{t: t, directory: directory}
	handler.pluginInstaller = fake
	ctx := context.Background()
	// A plugin of the key was enabled here once, and is gone.
	left := storage.NewPluginSettingsRecord("hello")
	left.Enabled, left.Capabilities, left.Config = true, []string{"storage"}, json.RawMessage(`{"greeting":"hi"}`)
	if err := store.PutPluginSettings(ctx, left); err != nil {
		t.Fatal(err)
	}
	install := PluginsPath + "/sources/install"
	if response := approvedRequest(t, handler, http.MethodPost, install, "application/json", `{"token":"token"}`, ""); response.Code != http.StatusOK {
		t.Fatalf("install = %d %s", response.Code, response.Body.String())
	}
	settings, err := store.GetPluginSettings(ctx, "hello")
	if err != nil || settings.Enabled || string(settings.Config) != `{"greeting":"hi"}` || len(settings.Capabilities) != 1 {
		t.Fatalf("settings after the install = %+v, %v", settings, err)
	}
	if manager.Current().Plugin("hello").Enabled() {
		t.Fatal("the plugin installed runs before the user enables it")
	}
	// Installing over the plugin there is an update: it stays enabled.
	settings.Enabled = true
	if err := store.PutPluginSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if response := approvedRequest(t, handler, http.MethodPost, install, "application/json", `{"token":"token"}`, ""); response.Code != http.StatusOK {
		t.Fatalf("update = %d %s", response.Code, response.Body.String())
	}
	if settings, err := store.GetPluginSettings(ctx, "hello"); err != nil || !settings.Enabled || !manager.Current().Plugin("hello").Enabled() {
		t.Fatalf("settings after the update = %+v, %v", settings, err)
	}
}

func TestPluginMarketFindsByCodeAndAnswersDocs(t *testing.T) {
	_, handler, _, directory := newPluginHandler(t)
	handler.pluginInstaller = &fakeInstaller{t: t, directory: directory}

	response := controlRequest(t, handler, http.MethodGet, PluginsPath+"/market?code=12345678", "", "", "")
	if market := decodeBody[contract.PluginMarket](t, response); response.Code != http.StatusOK || len(market.Plugins) != 1 || market.Plugins[0].Code != "12345678" {
		t.Fatalf("a plugin by code = %d %s", response.Code, response.Body.String())
	}
	response = controlRequest(t, handler, http.MethodGet, PluginsPath+"/market?code=87654321", "", "", "")
	if failure := decodeBody[installFailureEnvelope](t, response); response.Code != http.StatusUnprocessableEntity || failure.Failure.Reason != contract.PluginInstallNoPlugin {
		t.Fatalf("an unknown code = %d %s", response.Code, response.Body.String())
	}
	response = controlRequest(t, handler, http.MethodGet, PluginsPath+"/market/doc?key=someone_sora&version=1.0.0&doc=readme", "", "", "")
	if doc := decodeBody[contract.PluginMarketDoc](t, response); response.Code != http.StatusOK || doc.Markdown != "# someone_sora 1.0.0 readme" {
		t.Fatalf("a usage guide = %d %s", response.Code, response.Body.String())
	}
	for _, query := range []string{"key=someone_sora&doc=license", "key=Bad%20Key&doc=readme"} {
		if response := controlRequest(t, handler, http.MethodGet, PluginsPath+"/market/doc?"+query, "", "", ""); response.Code != http.StatusBadRequest {
			t.Fatalf("doc?%s = %d %s", query, response.Code, response.Body.String())
		}
	}
}
