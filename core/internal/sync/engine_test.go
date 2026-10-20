package sync

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
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
	engine       *Engine
	store        *sqlite.Store
	clock        time.Time
	databasePath string
}

// pair is two devices sharing one remote.
type pair struct {
	t      *testing.T
	url    string
	server *httptest.Server
	first  *device
	second *device
}

func newPair(t *testing.T) *pair {
	t.Helper()
	result := newRemote(t)
	result.first = result.install(0)
	result.second = result.install(1)
	return result
}

// newRemote starts the shared remote without installing a device, for a test
// that decides itself when each device appears.
func newRemote(t *testing.T) *pair {
	t.Helper()
	shared := &davStore{objects: map[string]davObject{}}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/dav/" {
			request.URL.Path = "/dav"
		}
		shared.ServeHTTP(writer, request)
	}))
	t.Cleanup(server.Close)
	return &pair{t: t, url: server.URL + "/dav", server: server}
}

// install opens one more device against the pair's remote. A test calls it
// itself when the moment a device is installed is part of what it checks: a
// device installed after another one has published is what makes its own seeded
// rows newer than the remote's changes, the way a real second machine is
// installed later.
func (p *pair) install(index int) *device {
	p.t.Helper()
	databasePath := filepath.Join(p.t.TempDir(), fmt.Sprintf("device%d.db", index))
	database, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		p.t.Fatalf("open database: %v", err)
	}
	p.t.Cleanup(func() { _ = database.Close() })
	device := &device{store: database, clock: time.Now().UTC().Add(-time.Hour), databasePath: databasePath}
	engine, err := New(Dependencies{
		Store:  database,
		Client: p.server.Client(),
		Now:    func() time.Time { return device.clock },
		Logf:   p.t.Logf,
	})
	if err != nil {
		p.t.Fatalf("new engine: %v", err)
	}
	device.engine = engine
	return device
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

// syncConfig points one device at the shared remote. It is the same
// configuration pair.configure saves, in a form a test can adjust before it
// saves it.
func (p *pair) syncConfig() contract.SyncConfig {
	config := contract.DefaultSyncConfig()
	config.Enabled = true
	config.Trigger = contract.SyncTriggerSettings{Mode: contract.SyncTriggerManual}
	config.Remote = contract.SyncRemote{Kind: contract.SyncRemoteWebDAV, Endpoint: p.url, Path: contract.SyncDefaultFolder}
	return config
}

// setPolicyEnabled turns the fixed privacy policy on, so a test can tell one
// device's policy from the default every installation starts with.
func setPolicyEnabled(ctx context.Context, store *sqlite.Store, enabled bool) error {
	record, err := store.GetPolicy(ctx, contract.DefaultPrivacyPolicyID)
	if err != nil {
		return err
	}
	policy := record.Policy
	policy.Enabled = enabled
	_, err = store.UpdatePolicy(ctx, policy, record.ETag)
	return err
}

// createAccessToken issues a gateway access token on one device, the way the
// token manager does.
func createAccessToken(t *testing.T, store *sqlite.Store, name string) accesstoken.CreatedToken {
	t.Helper()
	manager, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatalf("access token manager: %v", err)
	}
	created, err := manager.Create(context.Background(), name)
	if err != nil {
		t.Fatalf("create access token: %v", err)
	}
	return created
}

// accessTokenNames is every token name a device holds, sorted, so two devices
// can be compared without depending on the order they were stored in.
func accessTokenNames(t *testing.T, store *sqlite.Store) []string {
	t.Helper()
	tokens, err := store.ListAccessTokens(context.Background())
	if err != nil {
		t.Fatalf("list access tokens: %v", err)
	}
	names := make([]string, 0, len(tokens))
	for _, token := range tokens {
		names = append(names, token.Name)
	}
	sort.Strings(names)
	return names
}

// authenticateAccessToken presents a token value to a device, which is what a
// client does.
func authenticateAccessToken(t *testing.T, store *sqlite.Store, value string) contract.AccessTokenID {
	t.Helper()
	manager, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatalf("access token manager: %v", err)
	}
	id, err := manager.Authenticate(context.Background(), value)
	if err != nil {
		t.Fatalf("access token does not authenticate: %v", err)
	}
	return id
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

// A device that turns a group off in its own settings must not seal the copy it
// already synced back over a change the other device has published since. The
// kind is out of the merge, so it carries the remote's version through.
func TestNarrowScopeKeepsTheRemotesCredential(t *testing.T) {
	pair := newPair(t)
	ctx := context.Background()
	passphrase := []byte("correct horse battery")
	pair.configure(pair.first, passphrase)
	pair.configure(pair.second, passphrase)
	id := serviceID("f")
	newTestService(t, pair.first.store, id, "foxtrot")
	pair.sync(pair.first)
	pair.sync(pair.second)

	// The wide device rotates the credential and publishes it.
	record, err := pair.second.store.GetService(ctx, id)
	if err != nil {
		t.Fatalf("read for rotate: %v", err)
	}
	pair.second.clock = pair.second.clock.Add(time.Minute)
	if _, err := pair.second.store.UpdateService(ctx, record.Service, storage.CredentialMutation{
		Present: true, Secret: []byte("sk-rotated"),
	}, record.ETag); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	pair.sync(pair.second)

	// The first device turns credentials off and syncs. Its merge base still
	// holds the credential as it was before the rotation.
	config := contract.DefaultSyncConfig()
	config.Enabled = true
	config.Trigger = contract.SyncTriggerSettings{Mode: contract.SyncTriggerManual}
	config.Remote = contract.SyncRemote{
		Kind: contract.SyncRemoteWebDAV, Endpoint: pair.url,
		Path: contract.SyncDefaultFolder, Addressing: contract.SyncS3AddressingPath,
	}
	config.Scope.Credentials = false
	if err := pair.first.engine.Configure(ctx, config); err != nil {
		t.Fatalf("configure narrow: %v", err)
	}
	pair.sync(pair.first)

	// The wide device syncs again: the narrow device must not have undone its
	// change on either the remote or the wide device.
	pair.sync(pair.second)
	secret, err := pair.second.store.Get(ctx, serviceRef(id))
	if err != nil {
		t.Fatalf("read credential: %v", err)
	}
	if string(secret) != "sk-rotated" {
		t.Fatalf("a narrow scope on the other device reverted the credential: got %q", secret)
	}
	// The narrow device keeps the copy it already had: turning a group off in
	// the settings leaves this device's own data alone.
	local, err := pair.first.store.Get(ctx, serviceRef(id))
	if err != nil || string(local) != "sk-foxtrot" {
		t.Fatalf("the narrow device's own credential changed: %q, %v", local, err)
	}
}

// A successful sync packages the change log and clears it. The merge's own
// writes must not be logged after the snapshot: a row left behind makes the
// debounce branch due on the next tick, so every interval becomes the debounce.
func TestSuccessfulSyncLeavesNothingPending(t *testing.T) {
	pair := newPair(t)
	ctx := context.Background()
	passphrase := []byte("correct horse battery")
	// The change log is stamped by SQLite, so this device's clock is put on the
	// same timeline rather than an hour behind it.
	pair.first.clock = time.Now().UTC()
	config := contract.DefaultSyncConfig()
	config.Enabled = true
	config.Trigger = contract.SyncTriggerSettings{Mode: contract.SyncTriggerInterval, IntervalMinutes: 60, DebounceSeconds: 30}
	config.Remote = contract.SyncRemote{
		Kind: contract.SyncRemoteWebDAV, Endpoint: pair.url,
		Path: contract.SyncDefaultFolder, Addressing: contract.SyncS3AddressingPath,
	}
	if err := pair.first.engine.Configure(ctx, config); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if err := pair.first.engine.SetPassphrase(ctx, passphrase); err != nil {
		t.Fatalf("passphrase: %v", err)
	}
	newTestService(t, pair.first.store, serviceID("g"), "golf")
	pair.first.clock = time.Now().UTC()
	pair.sync(pair.first)

	count, err := pair.first.store.PendingChanges(ctx)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if count != 0 {
		changes, _ := pair.first.store.ChangesSince(ctx, time.Time{})
		for _, change := range changes {
			t.Logf("leftover %s/%s at %s", change.Kind, change.EntityID, change.ChangedAt.Format("15:04:05.000"))
		}
		t.Fatalf("a successful sync left %d changes pending", count)
	}
	// And the scheduler agrees: an interval trigger is not due again until its
	// interval, not one debounce later.
	pair.first.clock = pair.first.clock.Add(31 * time.Second)
	due, err := pair.first.engine.due(ctx, false)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if due {
		t.Fatal("a device on a 60 minute interval is due 31 seconds after a successful sync")
	}
}

// A client that leaves the S3 addressing out names the same remote as one that
// says "path", so saving it must not drop the merge base.
func TestConfigureKeepsTheBaselineWhenAddressingIsOmitted(t *testing.T) {
	pair := newPair(t)
	ctx := context.Background()
	passphrase := []byte("correct horse battery")
	pair.configure(pair.first, passphrase)
	newTestService(t, pair.first.store, serviceID("h"), "hotel")
	pair.sync(pair.first)

	// The same remote, spelled without the addressing field.
	config := contract.DefaultSyncConfig()
	config.Enabled = true
	config.Trigger = contract.SyncTriggerSettings{Mode: contract.SyncTriggerManual}
	config.Remote = contract.SyncRemote{Kind: contract.SyncRemoteWebDAV, Endpoint: pair.url, Path: contract.SyncDefaultFolder}
	if err := pair.first.engine.Configure(ctx, config); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if _, _, err := pair.first.store.Baseline(ctx); err != nil {
		t.Fatalf("re-saving the same remote dropped the merge base: %v", err)
	}
}

// A device that joins a remote which already holds a configuration adopts it.
// Everything the installation seeded carries a stamp from when the device was
// set up, which is later than a change the other device made hours ago, so
// comparing times alone would let the newcomer publish its own default over the
// configuration the other devices share.
func TestFirstSyncAdoptsTheRemoteInsteadOfOverwritingIt(t *testing.T) {
	pair := newRemote(t)
	ctx := context.Background()
	passphrase := []byte("correct horse battery")
	pair.first = pair.install(0)
	pair.configure(pair.first, passphrase)
	id := serviceID("i")
	newTestService(t, pair.first.store, id, "india")
	if err := setPolicyEnabled(ctx, pair.first.store, true); err != nil {
		t.Fatalf("edit policy: %v", err)
	}
	pair.sync(pair.first)

	// The joining device is installed now, so the row its own installation
	// seeded carries a later stamp than the change the first device published.
	// That is what a device that joins a remote is really like, and it is why
	// comparing stamps alone is not enough.
	pair.second = pair.install(1)
	pair.configure(pair.second, passphrase)
	before, err := pair.second.engine.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !before.FirstSyncPending {
		t.Fatal("a device with no merge base did not report a first sync")
	}
	pair.sync(pair.second)

	policy, err := pair.second.store.GetPolicy(ctx, contract.DefaultPrivacyPolicyID)
	if err != nil {
		t.Fatalf("read policy: %v", err)
	}
	if !policy.Policy.Enabled {
		t.Fatal("the joining device published its seeded policy over the remote's")
	}
	if _, err := pair.second.store.GetService(ctx, id); err != nil {
		t.Fatalf("the provider was not pulled onto the joining device: %v", err)
	}
	// And it did not revert the change on the device that published it.
	pair.sync(pair.first)
	kept, err := pair.first.store.GetPolicy(ctx, contract.DefaultPrivacyPolicyID)
	if err != nil {
		t.Fatalf("read policy on the first device: %v", err)
	}
	if !kept.Policy.Enabled {
		t.Fatal("the joining device reverted the policy the first device published")
	}
	after, err := pair.second.engine.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if after.FirstSyncPending {
		t.Fatal("a device that has exchanged a package still reports a first sync")
	}
}

// An armed force push replaces the package at the remote with this device's
// configuration, and the arming is spent by the run that uses it: the run after
// it merges again rather than overwriting the remote a second time.
func TestForcePushOverwritesTheRemoteOnceAndThenMergesAgain(t *testing.T) {
	pair := newPair(t)
	ctx := context.Background()
	passphrase := []byte("correct horse battery")
	pair.configure(pair.first, passphrase)
	pair.sync(pair.first)
	pair.configure(pair.second, passphrase)
	pair.sync(pair.second)
	theirs := serviceID("m")
	newTestService(t, pair.second.store, theirs, "mike")
	pair.sync(pair.second)

	// The first device holds neither that provider nor any other, and the user
	// arms the run that overwrites the remote with what it does hold.
	config := pair.syncConfig()
	config.ForcePush = true
	if err := pair.first.engine.Configure(ctx, config); err != nil {
		t.Fatalf("arm force push: %v", err)
	}
	status, err := pair.first.engine.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.ForcePushArmed {
		t.Fatal("the armed run was not reported")
	}
	pair.sync(pair.first)

	// The remote now holds what the first device held, so the provider it never
	// had is gone from the remote and from the device that published it.
	pair.sync(pair.second)
	if _, err := pair.second.store.GetService(ctx, theirs); err == nil {
		t.Fatal("the force push left the other device's provider on the remote")
	}
	after, err := pair.first.engine.Config(ctx)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if after.ForcePush {
		t.Fatal("a force push stayed armed after the run that used it")
	}
	// The next run merges again: a provider added on the other device arrives
	// here instead of being overwritten with this device's copy.
	added := serviceID("n")
	newTestService(t, pair.second.store, added, "november")
	pair.sync(pair.second)
	pair.sync(pair.first)
	if _, err := pair.first.store.GetService(ctx, added); err != nil {
		t.Fatalf("the run after a force push did not merge: %v", err)
	}
}

// Saving the settings that point a device at a remote is followed by a run,
// without waiting for the interval: that run is what brings an existing
// configuration onto a device that has none. The passphrase is what a device is
// waiting on, so entering it is the save that starts the run here.
func TestASettingsSaveStartsARunWithoutWaitingForTheSchedule(t *testing.T) {
	pair := newPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		pair.second.engine.Run(ctx, t.Logf)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	// The interval is an hour, so the only thing that can start a run now is the
	// save itself.
	pair.second.clock = time.Now().UTC()
	passphrase := []byte("correct horse battery")
	pair.configure(pair.first, passphrase)
	id := serviceID("o")
	newTestService(t, pair.first.store, id, "oscar")
	pair.sync(pair.first)

	config := pair.syncConfig()
	config.Trigger = contract.SyncTriggerSettings{Mode: contract.SyncTriggerInterval, IntervalMinutes: 60, DebounceSeconds: 30}
	if err := pair.second.engine.Configure(ctx, config); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if err := pair.second.engine.SetPassphrase(ctx, passphrase); err != nil {
		t.Fatalf("passphrase: %v", err)
	}
	deadline := time.After(20 * time.Second)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := pair.second.store.GetService(ctx, id); err == nil {
			return
		}
		select {
		case <-deadline:
			t.Fatal("the settings save did not start a run")
		case <-ticker.C:
		}
	}
}

// A token issued on one device works on the other: the value travels, and the
// device that receives it derives the digest and the hint an authentication
// reads from the value it stored.
func TestAccessTokenSyncsAndAuthenticates(t *testing.T) {
	pair := newPair(t)
	ctx := context.Background()
	passphrase := []byte("correct horse battery")
	pair.configure(pair.first, passphrase)
	pair.configure(pair.second, passphrase)
	created := createAccessToken(t, pair.first.store, "laptop client")
	pair.sync(pair.first)
	pair.sync(pair.second)

	if authenticated := authenticateAccessToken(t, pair.second.store, created.Value); authenticated != created.Token.ID {
		t.Fatalf("the token authenticates as %q, want %q", authenticated, created.Token.ID)
	}
	// The other device keeps its own sealed copy, so it can hand the value to a
	// client of its own.
	revealed, err := pair.second.store.RevealAccessToken(ctx, created.Token.ID)
	if err != nil || revealed != created.Value {
		t.Fatalf("reveal on the other device = %q, %v", revealed, err)
	}
	if names := accessTokenNames(t, pair.second.store); !containsName(names, "laptop client") {
		t.Fatalf("the token arrived without the name it was given: %v", names)
	}
	// And the device that issued it did not have its row rewritten.
	pair.sync(pair.first)
	if names := accessTokenNames(t, pair.first.store); !containsName(names, "laptop client") {
		t.Fatalf("the issuing device lost the name: %v", names)
	}
}

// Two devices that each named a token the same thing keep both tokens, and both
// devices end up with the same two names. A round after that changes nothing,
// which is what deciding the name by id is for: the devices must not rename each
// other's tokens on every run.
func TestSameNamedTokensSurviveOnBothDevices(t *testing.T) {
	pair := newPair(t)
	passphrase := []byte("correct horse battery")
	pair.configure(pair.first, passphrase)
	pair.configure(pair.second, passphrase)
	mine := createAccessToken(t, pair.first.store, "work")
	theirs := createAccessToken(t, pair.second.store, "work")

	pair.sync(pair.first)
	pair.sync(pair.second)
	pair.sync(pair.first)
	pair.sync(pair.second)

	for index, device := range []*device{pair.first, pair.second} {
		for _, created := range []accesstoken.CreatedToken{mine, theirs} {
			if authenticated := authenticateAccessToken(t, device.store, created.Value); authenticated != created.Token.ID {
				t.Fatalf("device %d authenticates %q as %q, want %q", index, created.Token.Name, authenticated, created.Token.ID)
			}
		}
	}
	first := accessTokenNames(t, pair.first.store)
	second := accessTokenNames(t, pair.second.store)
	if !equalNames(first, second) {
		t.Fatalf("the devices name the same tokens differently: %v and %v", first, second)
	}
	if len(first) != 4 {
		t.Fatalf("each device should hold two named tokens and two bootstrap ones: %v", first)
	}
	pair.sync(pair.first)
	pair.sync(pair.second)
	pair.sync(pair.first)
	if settled := accessTokenNames(t, pair.first.store); !equalNames(first, settled) {
		t.Fatalf("a later round renamed tokens again: %v became %v", first, settled)
	}
}

// A token removed on one device is removed on the other, and the value stops
// authenticating on both.
func TestAccessTokenDeletionPropagates(t *testing.T) {
	pair := newPair(t)
	ctx := context.Background()
	passphrase := []byte("correct horse battery")
	pair.configure(pair.first, passphrase)
	pair.configure(pair.second, passphrase)
	created := createAccessToken(t, pair.first.store, "retired client")
	pair.sync(pair.first)
	pair.sync(pair.second)
	authenticateAccessToken(t, pair.second.store, created.Value)

	if err := pair.second.store.DeleteAccessToken(ctx, created.Token.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	pair.sync(pair.second)
	pair.sync(pair.first)
	if names := accessTokenNames(t, pair.first.store); containsName(names, "retired client") {
		t.Fatalf("the deletion did not propagate: %v", names)
	}
	manager, err := accesstoken.NewManager(pair.first.store)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	if _, err := manager.Authenticate(ctx, created.Value); err == nil {
		t.Fatal("a deleted token still authenticates")
	}
	// And the device that deleted it does not get it back.
	pair.sync(pair.first)
	pair.sync(pair.second)
	if names := accessTokenNames(t, pair.second.store); containsName(names, "retired client") {
		t.Fatalf("the deleted token came back: %v", names)
	}
}

// Tokens stay where they are when the settings leave them out of the package.
func TestTokensOutOfScopeDoNotTravel(t *testing.T) {
	pair := newPair(t)
	ctx := context.Background()
	passphrase := []byte("correct horse battery")
	scope := pair.syncConfig()
	scope.Scope.Tokens = false
	for _, device := range []*device{pair.first, pair.second} {
		if err := device.engine.Configure(ctx, scope); err != nil {
			t.Fatalf("configure: %v", err)
		}
		if err := device.engine.SetPassphrase(ctx, passphrase); err != nil {
			t.Fatalf("passphrase: %v", err)
		}
	}
	createAccessToken(t, pair.first.store, "kept at home")
	pair.sync(pair.first)
	pair.sync(pair.second)
	if names := accessTokenNames(t, pair.second.store); containsName(names, "kept at home") {
		t.Fatalf("a token travelled with the group switched off: %v", names)
	}
	if names := accessTokenNames(t, pair.first.store); !containsName(names, "kept at home") {
		t.Fatalf("the token left its own device: %v", names)
	}
}

func containsName(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

func equalNames(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
