package sync

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

// davStore is a WebDAV-shaped store that honours If-Match and If-None-Match, so
// the engine's compare-and-swap can be exercised end to end without a server.
type davStore struct {
	mu      sync.Mutex
	objects map[string]davObject
	serial  int
}

type davObject struct {
	payload []byte
	etag    string
}

func (store *davStore) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	store.mu.Lock()
	defer store.mu.Unlock()
	name := strings.TrimPrefix(request.URL.Path, "/dav/")
	existing, exists := store.objects[name]
	switch request.Method {
	case http.MethodGet:
		if !exists {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("ETag", existing.etag)
		_, _ = writer.Write(existing.payload)
	case http.MethodPut:
		if request.Header.Get("If-None-Match") == "*" && exists {
			writer.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		if match := request.Header.Get("If-Match"); match != "" && (!exists || existing.etag != match) {
			writer.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		store.serial++
		etag := fmt.Sprintf(`"v%d"`, store.serial)
		store.objects[name] = davObject{payload: payload, etag: etag}
		writer.Header().Set("ETag", etag)
		writer.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		if !exists {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		if match := request.Header.Get("If-Match"); match != "" && existing.etag != match {
			writer.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		delete(store.objects, name)
		writer.WriteHeader(http.StatusNoContent)
	default:
		writer.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// device is one Core installation: its own database, and a clock the test can
// advance to order changes across devices.
type device struct {
	engine *Engine
	store  *sqlite.Store
	clock  time.Time
}

// pair is two devices sharing one remote.
type pair struct {
	t      *testing.T
	url    string
	first  *device
	second *device
}

func newPair(t *testing.T) *pair {
	t.Helper()
	shared := &davStore{objects: map[string]davObject{}}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/dav/" {
			request.URL.Path = "/dav"
		}
		shared.ServeHTTP(writer, request)
	}))
	t.Cleanup(server.Close)
	result := &pair{t: t, url: server.URL + "/dav"}
	base := time.Now().UTC().Add(-time.Hour)
	for index := 0; index < 2; index++ {
		database, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), fmt.Sprintf("device%d.db", index)))
		if err != nil {
			t.Fatalf("open database: %v", err)
		}
		t.Cleanup(func() { _ = database.Close() })
		device := &device{store: database, clock: base}
		engine, err := New(Dependencies{
			Store:  database,
			Client: server.Client(),
			Now:    func() time.Time { return device.clock },
			Logf:   t.Logf,
		})
		if err != nil {
			t.Fatalf("new engine: %v", err)
		}
		device.engine = engine
		if index == 0 {
			result.first = device
		} else {
			result.second = device
		}
	}
	return result
}

// configure points one device at the shared remote and sets the passphrase.
func (p *pair) configure(device *device, passphrase []byte) {
	p.t.Helper()
	config := contract.DefaultSyncConfig()
	config.Enabled = true
	config.Trigger = contract.SyncTriggerSettings{Mode: contract.SyncTriggerManual}
	config.Remote = contract.SyncRemote{Kind: contract.SyncRemoteWebDAV, Endpoint: p.url, Path: contract.SyncDefaultFolder}
	if err := device.engine.Configure(context.Background(), config); err != nil {
		p.t.Fatalf("configure: %v", err)
	}
	if err := device.engine.SetPassphrase(context.Background(), passphrase); err != nil {
		p.t.Fatalf("passphrase: %v", err)
	}
}

func (p *pair) sync(device *device) contract.SyncStatus {
	p.t.Helper()
	status, err := device.engine.Sync(context.Background())
	if err != nil {
		p.t.Fatalf("sync: %v", err)
	}
	return status
}

func testService(id contract.ServiceID, name string) contract.Service {
	endpoint := contract.Endpoint{
		ID: id, Name: name, Kind: contract.EndpointKindOpenAI,
		BaseURL: "https://api.example/v1", Auth: contract.EndpointAuth{Scheme: contract.AuthSchemeBearer},
		Enabled: true, Models: []string{"upstream-model"},
		Capabilities: []contract.Capability{{
			Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true,
		}},
	}
	return contract.ServiceFromEndpoint(endpoint)
}

func newTestService(t *testing.T, store *sqlite.Store, id contract.ServiceID, name string) {
	t.Helper()
	if _, err := store.CreateService(context.Background(), testService(id, name), storage.CredentialMutation{
		Present: true, Secret: []byte("sk-" + name),
	}); err != nil {
		t.Fatalf("create service: %v", err)
	}
}

func serviceRef(id contract.ServiceID) secretstore.Ref {
	return secretstore.Ref("local://service/" + string(id))
}

func serviceID(seed string) contract.ServiceID {
	return contract.ServiceID("service_" + strings.Repeat(seed, 24))
}

// TestEngineSyncsConfigurationAndCredentials is the case the feature exists
// for: a provider added on one device, with its credential, appears on the
// other, and the credential opens there.
func TestEngineSyncsConfigurationAndCredentials(t *testing.T) {
	pair := newPair(t)
	passphrase := []byte("correct horse battery")
	pair.configure(pair.first, passphrase)
	pair.configure(pair.second, passphrase)
	id := serviceID("a")
	newTestService(t, pair.first.store, id, "alpha")
	pair.sync(pair.first)
	pair.sync(pair.second)

	record, err := pair.second.store.GetService(context.Background(), id)
	if err != nil {
		t.Fatalf("the provider did not arrive: %v", err)
	}
	if record.Service.Name != "alpha" {
		t.Fatalf("service = %+v", record.Service)
	}
	secret, err := pair.second.store.Get(context.Background(), serviceRef(id))
	if err != nil || string(secret) != "sk-alpha" {
		t.Fatalf("credential = %q, %v", secret, err)
	}
	// The reference names this device's own row, not the one the package named.
	if record.Service.HTTP == nil || record.Service.HTTP.CredentialRef != string(serviceRef(id)) {
		t.Fatalf("credential reference = %+v", record.Service.HTTP)
	}
}

// A device that has never synced adopts the package's own salt and parameters,
// so the same passphrase produces the key that opens the stored package.
func TestSecondDeviceAdoptsThePackageKey(t *testing.T) {
	pair := newPair(t)
	passphrase := []byte("correct horse battery")
	pair.configure(pair.first, passphrase)
	newTestService(t, pair.first.store, serviceID("b"), "beta")
	pair.sync(pair.first)
	pair.configure(pair.second, passphrase)

	state, err := pair.second.store.SyncState(context.Background())
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	first, err := pair.first.store.SyncState(context.Background())
	if err != nil {
		t.Fatalf("first state: %v", err)
	}
	if state.KeySalt == nil || string(state.KeySalt) != string(first.KeySalt) {
		t.Fatal("the joining device chose its own salt instead of the package's")
	}
	// And it opens the package: the run succeeds rather than reporting a
	// mismatched passphrase.
	pair.sync(pair.second)
}

// A device that refuses a passphrase that does not open the stored package
// keeps the other device's configuration intact.
func TestWrongPassphraseIsRefused(t *testing.T) {
	pair := newPair(t)
	pair.configure(pair.first, []byte("correct horse battery"))
	newTestService(t, pair.first.store, serviceID("z"), "zeta")
	pair.sync(pair.first)

	config := contract.DefaultSyncConfig()
	config.Enabled = true
	config.Trigger = contract.SyncTriggerSettings{Mode: contract.SyncTriggerManual}
	config.Remote = contract.SyncRemote{Kind: contract.SyncRemoteWebDAV, Endpoint: pair.url, Path: contract.SyncDefaultFolder}
	if err := pair.second.engine.Configure(context.Background(), config); err != nil {
		t.Fatalf("configure: %v", err)
	}
	err := pair.second.engine.SetPassphrase(context.Background(), []byte("a different passphrase"))
	if err == nil {
		t.Fatal("a passphrase that does not open the package was accepted")
	}
}

// A device that leaves a group out of its scope must not delete the same group
// on the device that kept it in.
func TestNarrowScopeDoesNotDeleteOnTheWideDevice(t *testing.T) {
	pair := newPair(t)
	passphrase := []byte("correct horse battery")
	pair.configure(pair.first, passphrase)
	id := serviceID("c")
	newTestService(t, pair.first.store, id, "gamma")
	pair.sync(pair.first)

	// The second device drops credentials from its own settings and syncs.
	config := contract.DefaultSyncConfig()
	config.Enabled = true
	config.Trigger = contract.SyncTriggerSettings{Mode: contract.SyncTriggerManual}
	config.Remote = contract.SyncRemote{Kind: contract.SyncRemoteWebDAV, Endpoint: pair.url, Path: contract.SyncDefaultFolder}
	config.Scope.Credentials = false
	if err := pair.second.engine.Configure(context.Background(), config); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if err := pair.second.engine.SetPassphrase(context.Background(), passphrase); err != nil {
		t.Fatalf("passphrase: %v", err)
	}
	pair.sync(pair.second)

	if _, err := pair.first.store.Get(context.Background(), serviceRef(id)); err != nil {
		t.Fatalf("a narrower scope on another device deleted the credential: %v", err)
	}
	// The package still carries the credential the narrow device does not write,
	// so a second round trip on the wide device keeps it.
	pair.sync(pair.first)
	if _, err := pair.first.store.Get(context.Background(), serviceRef(id)); err != nil {
		t.Fatalf("credential lost after a round trip: %v", err)
	}
	// And the narrow device still has no credential of its own.
	if _, err := pair.second.store.Get(context.Background(), serviceRef(id)); err == nil {
		t.Fatal("the narrow device wrote a credential its settings exclude")
	}
}

// A service removed on one device is removed on the other, and the other device
// does not upload it back on a later run.
func TestDeletionPropagatesAndDoesNotComeBack(t *testing.T) {
	pair := newPair(t)
	passphrase := []byte("correct horse battery")
	pair.configure(pair.first, passphrase)
	pair.configure(pair.second, passphrase)
	id := serviceID("d")
	newTestService(t, pair.first.store, id, "delta")
	pair.sync(pair.first)
	pair.sync(pair.second)
	if _, err := pair.second.store.GetService(context.Background(), id); err != nil {
		t.Fatalf("the provider did not arrive: %v", err)
	}

	record, err := pair.second.store.GetService(context.Background(), id)
	if err != nil {
		t.Fatalf("read for delete: %v", err)
	}
	pair.second.clock = pair.second.clock.Add(time.Minute)
	if err := pair.second.store.DeleteService(context.Background(), id, record.ETag); err != nil {
		t.Fatalf("delete: %v", err)
	}
	pair.sync(pair.second)
	pair.sync(pair.first)
	if _, err := pair.first.store.GetService(context.Background(), id); err == nil {
		t.Fatal("the deletion did not propagate")
	}
	// The first device must not resurrect it.
	pair.sync(pair.first)
	pair.sync(pair.second)
	if _, err := pair.second.store.GetService(context.Background(), id); err == nil {
		t.Fatal("the deleted provider came back")
	}
}

// Two devices editing the same provider resolve by time, and the merge is
// reported rather than dropping one side silently.
func TestConcurrentEditsResolveByTime(t *testing.T) {
	pair := newPair(t)
	passphrase := []byte("correct horse battery")
	pair.configure(pair.first, passphrase)
	pair.configure(pair.second, passphrase)
	id := serviceID("e")
	newTestService(t, pair.first.store, id, "epsilon")
	pair.sync(pair.first)
	pair.sync(pair.second)

	rename := func(device *device, name string) {
		record, err := device.store.GetService(context.Background(), id)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		service := record.Service
		service.Name = name
		if _, err := device.store.UpdateService(context.Background(), service, storage.CredentialMutation{}, record.ETag); err != nil {
			t.Fatalf("update: %v", err)
		}
	}
	pair.first.clock = pair.first.clock.Add(time.Minute)
	rename(pair.first, "from-first")
	pair.second.clock = pair.second.clock.Add(2 * time.Minute)
	rename(pair.second, "from-second")

	pair.sync(pair.first)
	status := pair.sync(pair.second)

	record, err := pair.second.store.GetService(context.Background(), id)
	if err != nil {
		t.Fatalf("read after merge: %v", err)
	}
	if record.Service.Name != "from-second" {
		t.Fatalf("the later change did not win: %+v", record.Service)
	}
	if len(status.Conflicts) == 0 {
		t.Fatal("the conflict was not reported")
	}
	pair.sync(pair.first)
	record, err = pair.first.store.GetService(context.Background(), id)
	if err != nil {
		t.Fatalf("read on first: %v", err)
	}
	if record.Service.Name != "from-second" {
		t.Fatalf("the first device kept the losing side: %+v", record.Service)
	}
}

// A device reports the name it is configured under, and falls back to the
// host's when none is set, so the settings can show a name rather than the
// opaque identity the remote lock uses.
func TestStatusNamesTheDeviceFromTheConfigurationOrTheHost(t *testing.T) {
	pair := newPair(t)
	config := contract.DefaultSyncConfig()
	config.Enabled = true
	config.Trigger = contract.SyncTriggerSettings{Mode: contract.SyncTriggerManual}
	config.Remote = contract.SyncRemote{Kind: contract.SyncRemoteWebDAV, Endpoint: pair.url, Path: contract.SyncDefaultFolder}
	if err := pair.first.engine.Configure(context.Background(), config); err != nil {
		t.Fatalf("configure: %v", err)
	}
	status, err := pair.first.engine.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.DeviceName == "" || status.DeviceName != status.HostName {
		t.Fatalf("an unnamed device did not fall back to the host: %+v", status)
	}
	if status.DeviceID == "" || status.DeviceID == status.DeviceName {
		t.Fatalf("the lock identity and the device name did not stay apart: %+v", status)
	}

	config.DeviceName = "  studio  "
	if err := pair.first.engine.Configure(context.Background(), config); err != nil {
		t.Fatalf("configure: %v", err)
	}
	status, err = pair.first.engine.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.DeviceName != "studio" {
		t.Fatalf("the configured name was not used: %+v", status)
	}
}
