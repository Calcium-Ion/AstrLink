package controlapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

// fakeSync is the sync engine as the API surface sees it, recording what a
// request actually did so a test can assert on the call rather than only on the
// response.
type fakeSync struct {
	config      contract.SyncConfig
	passphrase  []byte
	understood  bool
	configured  int
	cleared     int
	runErr      error
	status      contract.SyncStatus
	runStarted  int
	configError error
}

func (f *fakeSync) Config(context.Context) (contract.SyncConfig, error) {
	if f.configError != nil {
		return contract.SyncConfig{}, f.configError
	}
	return f.config, nil
}

func (f *fakeSync) Configure(_ context.Context, config contract.SyncConfig) error {
	f.configured++
	f.config = config
	return nil
}

func (f *fakeSync) SetPassphrase(_ context.Context, passphrase []byte) error {
	f.understood = true
	f.passphrase = append([]byte(nil), passphrase...)
	return nil
}

func (f *fakeSync) ClearPassphrase(context.Context) error {
	f.cleared++
	f.understood = false
	return nil
}

func (f *fakeSync) Status(context.Context) (contract.SyncStatus, error) {
	return f.status, nil
}

func (f *fakeSync) Sync(context.Context) (contract.SyncStatus, error) {
	f.runStarted++
	if f.runErr != nil {
		return contract.SyncStatus{}, f.runErr
	}
	return f.status, nil
}

func newSyncHandler(t *testing.T, manager SyncManager) *Handler {
	t.Helper()
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	handler, err := NewWithDependencies(
		contract.DefaultVersionResponse("0.1.0-test", "abc1234"),
		Dependencies{
			ServiceStore: store,
			ControlToken: testControlToken,
			Sync:         manager,
		},
	)
	if err != nil {
		t.Fatalf("NewWithDependencies: %v", err)
	}
	return handler
}

func TestSyncSurfaceReportsUnavailableWithoutAnEngine(t *testing.T) {
	_, handler := newRouteHandler(t)
	response := controlRequest(t, handler, http.MethodGet, SyncPath+"/status", "", "", "")
	if response.Code != 503 || !strings.Contains(response.Body.String(), "sync_unavailable") {
		t.Fatal(response.Code, response.Body.String())
	}
}

func TestSyncConfigReadHidesTheRemoteSecrets(t *testing.T) {
	manager := &fakeSync{config: contract.SyncConfig{
		Enabled: true,
		Trigger: contract.SyncTriggerSettings{Mode: contract.SyncTriggerInterval, IntervalMinutes: 60},
		Remote: contract.SyncRemote{
			Kind: contract.SyncRemoteWebDAV, Endpoint: "https://dav.example",
			Username: "me", Password: "hunter2", SecretKey: "s3cr3t",
		},
	}}
	handler := newSyncHandler(t, manager)
	response := controlRequest(t, handler, http.MethodGet, SyncPath+"/config", "", "", "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "hunter2") || strings.Contains(body, "s3cr3t") {
		t.Fatal(body)
	}
	if !strings.Contains(body, `"remote_password_set":true`) ||
		!strings.Contains(body, `"remote_secret_key_set":true`) {
		t.Fatal(body)
	}
}

func TestSyncConfigWriteKeepsStoredSecretsWhenLeftBlank(t *testing.T) {
	manager := &fakeSync{config: contract.SyncConfig{
		Enabled: true,
		Trigger: contract.SyncTriggerSettings{Mode: contract.SyncTriggerManual},
		Remote: contract.SyncRemote{
			Kind: contract.SyncRemoteWebDAV, Endpoint: "https://dav.example",
			Username: "me", Password: "stored", SecretKey: "stored-key",
		},
	}}
	handler := newSyncHandler(t, manager)
	body := `{"enabled":true,"trigger":{"mode":"manual"},"scope":{"services":true,"credentials":false,"policies":false,"pricing":false},` +
		`"remote":{"kind":"webdav","endpoint":"https://dav.example","username":"me"}}`
	response := controlRequest(t, handler, http.MethodPut, SyncPath+"/config", "application/json", body, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	if manager.configured != 1 {
		t.Fatalf("configured %d times", manager.configured)
	}
	if manager.config.Remote.Password != "stored" || manager.config.Remote.SecretKey != "stored-key" {
		t.Fatalf("stored secrets were dropped: %+v", manager.config.Remote)
	}
	if strings.Contains(response.Body.String(), "stored") {
		t.Fatal(response.Body.String())
	}
}

func TestSyncConfigWriteRefusesUnknownFields(t *testing.T) {
	manager := &fakeSync{}
	handler := newSyncHandler(t, manager)
	response := controlRequest(t, handler, http.MethodPut, SyncPath+"/config", "application/json",
		`{"enabled":true,"trigger":{"mode":"manual"},"scope":{"services":true},"remote":{"kind":"webdav","endpoint":"https://dav.example"},"unexpected":1}`, "")
	if response.Code != 400 || !strings.Contains(response.Body.String(), "invalid_sync_config") {
		t.Fatal(response.Code, response.Body.String())
	}
	if manager.configured != 0 {
		t.Fatal("a refused body reached the engine")
	}
}

func TestSyncPassphraseIsAcceptedAndCleared(t *testing.T) {
	manager := &fakeSync{}
	response := controlRequest(t, newSyncHandler(t, manager), http.MethodPut, SyncPath+"/passphrase",
		"application/json", `{"passphrase":"correct horse battery"}`, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"passphrase_set":true`) {
		t.Fatal(response.Code, response.Body.String())
	}
	if string(manager.passphrase) != "correct horse battery" {
		t.Fatalf("passphrase = %q", manager.passphrase)
	}
	if strings.Contains(response.Body.String(), "correct horse") {
		t.Fatal(response.Body.String())
	}

	manager.passphrase = nil
	response = controlRequest(t, newSyncHandler(t, manager), http.MethodDelete, SyncPath+"/passphrase", "", "", "")
	if response.Code != 200 || manager.cleared != 1 {
		t.Fatal(response.Code, response.Body.String())
	}
}

func TestSyncPassphraseRefusesAnInvalidBody(t *testing.T) {
	manager := &fakeSync{}
	for _, body := range []string{`{`, `{"passphrase":"x","extra":1}`} {
		response := controlRequest(t, newSyncHandler(t, manager), http.MethodPut, SyncPath+"/passphrase",
			"application/json", body, "")
		if response.Code != 400 || !strings.Contains(response.Body.String(), "invalid_sync_passphrase") {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	if manager.passphrase != nil {
		t.Fatal("an invalid body reached the engine")
	}
}

func TestSyncRunReportsAFailedRunAsBadGateway(t *testing.T) {
	manager := &fakeSync{runErr: errors.New("remote store is unreachable")}
	response := controlRequest(t, newSyncHandler(t, manager), http.MethodPost, SyncPath+"/run", "", "", "")
	if response.Code != 502 || !strings.Contains(response.Body.String(), "sync_failed") {
		t.Fatal(response.Code, response.Body.String())
	}
	if manager.runStarted != 1 {
		t.Fatalf("run started %d times", manager.runStarted)
	}
}

func TestSyncMethodsAndPaths(t *testing.T) {
	manager := &fakeSync{}
	handler := newSyncHandler(t, manager)
	for _, testCase := range []struct {
		method string
		path   string
		status int
		allow  string
	}{
		{http.MethodPost, SyncPath + "/status", 405, "GET"},
		{http.MethodDelete, SyncPath + "/status", 405, "GET"},
		{http.MethodPost, SyncPath + "/config", 405, "GET, PUT"},
		{http.MethodDelete, SyncPath + "/run", 405, "POST"},
		{http.MethodPatch, SyncPath + "/passphrase", 405, "PUT"},
		{http.MethodGet, SyncPath + "/nonsense", 404, ""},
	} {
		response := controlRequest(t, handler, testCase.method, testCase.path, "", "", "")
		if response.Code != testCase.status {
			t.Fatalf("%s %s = %d", testCase.method, testCase.path, response.Code)
		}
		if allow := response.Header().Get("Allow"); allow != testCase.allow {
			t.Fatalf("%s %s Allow = %q", testCase.method, testCase.path, allow)
		}
	}
}

func TestSyncReadReportsAStorageFailure(t *testing.T) {
	manager := &fakeSync{configError: storage.ErrInvalidRecord}
	response := controlRequest(t, newSyncHandler(t, manager), http.MethodGet, SyncPath+"/config", "", "", "")
	if response.Code != http.StatusInternalServerError {
		t.Fatal(response.Code, response.Body.String())
	}
}
