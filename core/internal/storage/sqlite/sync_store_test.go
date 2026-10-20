package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestSyncStateDefaultsAndRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	state, err := store.SyncState(ctx)
	if err != nil {
		t.Fatalf("sync state: %v", err)
	}
	if state.DeviceID == "" || state.PassphraseState != "unset" || state.Config.Enabled || !state.Config.Scope.Tokens {
		t.Fatalf("defaults = %+v", state)
	}
	// A second read returns the same device identity, not a new one.
	again, err := store.SyncState(ctx)
	if err != nil || again.DeviceID != state.DeviceID {
		t.Fatalf("second read = %+v, %v", again, err)
	}
	config := contract.DefaultSyncConfig()
	config.Enabled = true
	config.Remote = contract.SyncRemote{Kind: contract.SyncRemoteWebDAV, Endpoint: "https://dav.example/astr", Path: "/astrlink"}
	if err := store.UpdateSyncConfig(ctx, config); err != nil {
		t.Fatalf("update config: %v", err)
	}
	state, err = store.SyncState(ctx)
	if err != nil || !state.Configured || state.Config.Remote.Endpoint != "https://dav.example/astr" {
		t.Fatalf("state = %+v, %v", state, err)
	}
}

func TestSyncStateDoesNotExpandLegacyTokenScope(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "legacy-sync.db"))
	defer store.Close()
	config := contract.DefaultSyncConfig()
	config.Enabled = true
	config.Remote = contract.SyncRemote{Kind: contract.SyncRemoteWebDAV, Endpoint: "https://dav.example/astr", Path: "/astrlink"}
	if err := store.UpdateSyncConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE sync_state SET config_json = json_remove(config_json, '$.scope.tokens') WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	state, err := store.SyncState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Config.Scope.Tokens || !state.Config.Enabled || !state.Config.Scope.Services {
		t.Fatal("reading a legacy configuration changed its sync scope")
	}
	for _, enabled := range []bool{true, false} {
		config.Scope.Tokens = enabled
		if err := store.UpdateSyncConfig(ctx, config); err != nil {
			t.Fatal(err)
		}
		state, err := store.SyncState(ctx)
		if err != nil || state.Config.Scope.Tokens != enabled {
			t.Fatalf("explicit tokens=%v did not survive a reload: %v", enabled, err)
		}
	}
}

func TestSyncConfigValidation(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	bad := contract.DefaultSyncConfig()
	bad.Trigger.Mode = contract.SyncTriggerCron
	if err := store.UpdateSyncConfig(ctx, bad); !errors.Is(err, storagecontract.ErrInvalidArgument) {
		t.Fatalf("empty cron = %v, want ErrInvalidArgument", err)
	}
	bad = contract.DefaultSyncConfig()
	bad.Trigger.IntervalMinutes = 1
	if err := store.UpdateSyncConfig(ctx, bad); !errors.Is(err, storagecontract.ErrInvalidArgument) {
		t.Fatalf("short interval = %v, want ErrInvalidArgument", err)
	}
}

func TestSyncKeyIsSealedAndBoundToItsRow(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	if _, _, err := store.SyncKey(ctx); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("unset key = %v, want ErrNotFound", err)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	salt := []byte("0123456789abcdef")
	if err := store.PutSyncKey(ctx, key, salt, []byte(`{"alg":"argon2id"}`)); err != nil {
		t.Fatalf("put key: %v", err)
	}
	// The row is sealed, and cannot be read as plaintext.
	var stored []byte
	if err := store.db.QueryRow(`SELECT credential_value FROM sync_key_material WHERE key = 'sync'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) == len(key) {
		t.Fatal("sync key stored unsealed")
	}
	state, err := store.SyncState(ctx)
	if err != nil || state.PassphraseState != "set" {
		t.Fatalf("state = %+v, %v", state, err)
	}
	gotKey, gotSalt, err := store.SyncKey(ctx)
	if err != nil || string(gotKey) != string(key) || string(gotSalt) != string(salt) {
		t.Fatalf("key round trip = %x %q %v", gotKey, gotSalt, err)
	}
	if err := store.ClearSyncKey(ctx); err != nil {
		t.Fatalf("clear key: %v", err)
	}
	if _, _, err := store.SyncKey(ctx); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("cleared key = %v, want ErrNotFound", err)
	}
}

func TestSyncChangeLogFollowsConfigWrites(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	// An access token is configuration a package carries, so a store that has
	// just been created already has one change waiting: the bootstrap token it
	// issued for itself.
	changes, err := store.ChangesSince(ctx, time.Time{})
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(changes) != 1 || changes[0].Kind != syncKindAccessToken {
		t.Fatalf("a fresh store recorded %+v", changes)
	}
	id := contract.ServiceID("service_alpha")
	if _, err := store.CreateService(ctx, contract.ServiceFromEndpoint(testEndpoint(id)), storagecontract.CredentialMutation{
		Present: true, Secret: []byte("sk-alpha"),
	}); err != nil {
		t.Fatal(err)
	}
	count, err := store.PendingChanges(ctx)
	if err != nil || count == 0 {
		t.Fatalf("after create = %d, %v", count, err)
	}
	changes, err = store.ChangesSince(ctx, time.Time{})
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	kinds := map[string]int{}
	for _, change := range changes {
		kinds[change.Kind]++
	}
	// One service row and its credential, each reported once to this device.
	if kinds["service"] != 1 || kinds["service_credential"] != 1 {
		t.Fatalf("kinds = %v", kinds)
	}
	if err := store.ClearChangesThrough(ctx, time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if count, err := store.PendingChanges(ctx); err != nil || count != 0 {
		t.Fatalf("after clear = %d, %v", count, err)
	}
}

// Request records, audit blobs, and the billing ledger are written per request
// and never synced, so they must not schedule a sync.
func TestSyncChangeLogIgnoresPerRequestTables(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	if err := store.ClearChangesThrough(ctx, time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatalf("clear: %v", err)
	}
	mustExec(t, store, `INSERT INTO request_records (id, started_at, status, input_protocol, streaming, audit_json, created_at)
VALUES ('req_1', '2026-10-10T00:00:00Z', 'succeeded', 'openai_chat', 0, '{}', '2026-10-10T00:00:00Z')`)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO billing_metadata (key, value) VALUES ('probe', '1')
ON CONFLICT(key) DO UPDATE SET value = excluded.value`); err != nil {
		t.Fatal(err)
	}
	if count, err := store.PendingChanges(ctx); err != nil || count != 0 {
		t.Fatalf("per-request writes scheduled a sync: %d, %v", count, err)
	}
}

func TestRecordSyncResultAndTomstoneWatermark(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	syncedAt := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	if err := store.RecordSyncResult(ctx, contract.RemoteIdentity{Identity: "dav:astrlink", ETag: `"v1"`, Revision: 4}, &syncedAt, ""); err != nil {
		t.Fatalf("record: %v", err)
	}
	state, err := store.SyncState(ctx)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.RemoteIdentity != "dav:astrlink" || state.RemoteETag != `"v1"` || state.RemoteRevision != 4 {
		t.Fatalf("remote = %+v", state)
	}
	if state.LastSyncedAt == nil || !state.LastSyncedAt.Equal(syncedAt) {
		t.Fatalf("last synced = %v", state.LastSyncedAt)
	}
	if err := store.RecordSyncResult(ctx, contract.RemoteIdentity{}, nil, "remote unreachable"); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	state, _ = store.SyncState(ctx)
	if state.LastError != "remote unreachable" || state.LastSyncedAt == nil {
		t.Fatalf("a failed run changed the last success: %+v", state)
	}
	if err := store.RetainTombstonesThrough(ctx, syncedAt); err != nil {
		t.Fatalf("retain: %v", err)
	}
	state, _ = store.SyncState(ctx)
	if state.RetainedThrough == nil || !state.RetainedThrough.Equal(syncedAt) {
		t.Fatalf("retained through = %v", state.RetainedThrough)
	}
}

// A credential sealed on one device opens on another only after the engine
// re-seals it here: the ciphertext is bound to the row's table and key.
func TestExportImportCredentialReseals(t *testing.T) {
	ctx := context.Background()
	source := openTestStore(t, filepath.Join(t.TempDir(), "source.db"))
	defer source.Close()
	target := openTestStore(t, filepath.Join(t.TempDir(), "target.db"))
	defer target.Close()
	sourceID := contract.ServiceID("service_source")
	if _, err := source.CreateService(ctx, contract.ServiceFromEndpoint(testEndpoint(sourceID)), storagecontract.CredentialMutation{
		Present: true, Secret: []byte("sk-original-key"),
	}); err != nil {
		t.Fatal(err)
	}
	plaintext, err := source.ExportCredential(ctx, serviceCredentialsTable, string(sourceID))
	if err != nil || string(plaintext) != "sk-original-key" {
		t.Fatalf("export = %q, %v", plaintext, err)
	}
	targetID := contract.ServiceID("service_target")
	if _, err := target.CreateService(ctx, contract.ServiceFromEndpoint(testEndpoint(targetID)), storagecontract.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	if err := target.ImportCredential(ctx, serviceCredentialsTable, string(targetID), plaintext); err != nil {
		t.Fatalf("import: %v", err)
	}
	secret, err := target.Get(ctx, secretstore.Ref("local://service/"+string(targetID)))
	if err != nil || string(secret) != "sk-original-key" {
		t.Fatalf("imported credential = %q, %v", secret, err)
	}
	// The source ciphertext does not open on the target device.
	var sourceCiphertext []byte
	if err := source.db.QueryRow(`SELECT credential_value FROM service_credentials WHERE service_id = ?`, sourceID).Scan(&sourceCiphertext); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Get(ctx, secretstore.Ref("local://service/"+string(sourceID))); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("a ciphertext copied across devices = %v, want ErrNotFound", err)
	}
}

func TestExportCredentialRefusesUnknownTable(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	if _, err := store.ExportCredential(ctx, "local_access_token_secrets", "token_1"); !errors.Is(err, storagecontract.ErrInvalidArgument) {
		t.Fatalf("unknown table = %v, want ErrInvalidArgument", err)
	}
	if _, err := store.ExportCredential(ctx, serviceCredentialsTable, "service_missing"); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("missing row = %v, want ErrNotFound", err)
	}
}

// A token a device already holds is left alone when the same entity arrives
// again: the write would fire the change-log trigger and schedule a sync for a
// change that never happened.
func TestSyncWriteAccessTokenLeavesAnUnchangedTokenAlone(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	manager, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	created, err := manager.Create(ctx, "work")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.ClearChangesThrough(ctx, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("clear: %v", err)
	}
	document, err := json.Marshal(contract.AccessTokenDocument{
		Name: created.Token.Name, Source: string(storagecontract.AccessTokenSourceUser),
		CreatedAt: created.Token.CreatedAt,
	})
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	if err := store.SyncWriteAccessToken(ctx, string(created.Token.ID), document, []byte(created.Value)); err != nil {
		t.Fatalf("write: %v", err)
	}
	count, err := store.PendingChanges(ctx)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if count != 0 {
		t.Fatalf("re-storing a token this device already holds recorded %d changes", count)
	}
	// The value is still the one a client presents, and still readable here.
	revealed, err := store.RevealAccessToken(ctx, created.Token.ID)
	if err != nil || revealed != created.Value {
		t.Fatalf("reveal = %q, %v", revealed, err)
	}
}

// A token that arrives with a name this device already holds is stored beside it
// rather than replacing it, and the name it takes is the one its id decides.
func TestSyncWriteAccessTokenKeepsBothNames(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	manager, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	held, err := manager.Create(ctx, "work")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	arriving, err := manager.Create(ctx, "elsewhere")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.DeleteAccessToken(ctx, arriving.Token.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	document, err := json.Marshal(contract.AccessTokenDocument{
		Name: "work", Source: string(storagecontract.AccessTokenSourceUser), CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	if err := store.SyncWriteAccessToken(ctx, string(arriving.Token.ID), document, []byte(arriving.Value)); err != nil {
		t.Fatalf("write: %v", err)
	}
	names := map[string]string{}
	tokens, err := store.ListAccessTokens(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, token := range tokens {
		names[token.Name] = string(token.ID)
	}
	if names["work"] != string(held.Token.ID) && names["work"] != string(arriving.Token.ID) {
		t.Fatalf("the plain name went nowhere: %v", names)
	}
	if len(names) != 3 {
		t.Fatalf("one token displaced another: %v", names)
	}
	if _, err := manager.Authenticate(ctx, held.Value); err != nil {
		t.Fatalf("the token that had the name stopped working: %v", err)
	}
	if _, err := manager.Authenticate(ctx, arriving.Value); err != nil {
		t.Fatalf("the arriving token does not work: %v", err)
	}
}
