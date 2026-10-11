package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
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
	if state.DeviceID == "" || state.PassphraseState != "unset" || state.Config.Enabled {
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
	if count, err := store.PendingChanges(ctx); err != nil || count != 0 {
		t.Fatalf("empty log = %d, %v", count, err)
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
	changes, err := store.ChangesSince(ctx, time.Time{})
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
