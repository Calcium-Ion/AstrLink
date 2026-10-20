package sync

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/syncseal"
	"github.com/QuantumNous/astrlink/core/internal/sync/remote"
)

func remotePackage(t *testing.T, device *device) (Document, []byte, string) {
	t.Helper()
	ctx := context.Background()
	state, err := device.store.SyncState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := device.engine.remote(state)
	if err != nil {
		t.Fatal(err)
	}
	payload, etag, exists, err := target.Read(ctx)
	if err != nil || !exists {
		t.Fatalf("read remote: exists=%v error=%v", exists, err)
	}
	key, _, err := device.store.SyncKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	document, err := openRemotePackage(payload, key)
	if err != nil {
		t.Fatal(err)
	}
	return document, payload, etag
}

func TestSyncUpgradesPackageOnlyWhenTokensParticipate(t *testing.T) {
	pair := newPair(t)
	ctx := context.Background()
	config := pair.syncConfig()
	config.Scope.Tokens = false
	for _, device := range []*device{pair.first, pair.second} {
		if err := device.engine.Configure(ctx, config); err != nil {
			t.Fatal(err)
		}
		if err := device.engine.SetPassphrase(ctx, []byte("correct horse battery")); err != nil {
			t.Fatal(err)
		}
	}
	id := serviceID("q")
	newTestService(t, pair.first.store, id, "quebec")
	pair.sync(pair.first)
	if document, _, _ := remotePackage(t, pair.first); document.Version != 1 {
		t.Fatalf("token-free sync unnecessarily requires a new reader: version=%d", document.Version)
	}
	pair.sync(pair.second)
	if _, err := pair.second.store.GetService(ctx, id); err != nil {
		t.Fatalf("v1 provider was not imported: %v", err)
	}
	config.ForcePush = true
	if err := pair.second.engine.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	pair.sync(pair.second)
	if document, _, _ := remotePackage(t, pair.second); document.Version != 1 {
		t.Fatalf("token-free force push upgraded to version %d", document.Version)
	}
	config.ForcePush = false
	config.Scope.Tokens = true
	if err := pair.first.engine.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	created := createAccessToken(t, pair.first.store, "version gate")
	pair.sync(pair.first)
	document, _, _ := remotePackage(t, pair.first)
	if document.Version != 2 {
		t.Fatalf("token package can still be overwritten by a v1 writer: version=%d", document.Version)
	}
	pair.sync(pair.second)
	document, _, _ = remotePackage(t, pair.second)
	if _, ok := document.Get(KindAccessToken, string(created.Token.ID)); !ok || document.Version != 2 {
		t.Fatal("a narrow scope dropped the tokens or their version protection")
	}
	tokens, err := pair.first.store.ListAccessTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range tokens {
		if err := pair.first.store.DeleteAccessToken(ctx, token.ID); err != nil {
			t.Fatal(err)
		}
	}
	pair.sync(pair.first)
	document, _, _ = remotePackage(t, pair.first)
	if document.Version != 2 || len(document.Entities[KindAccessToken]) != 0 {
		t.Fatal("deleting the last token downgraded the package or retained the token")
	}
	config.Scope.Tokens = false
	config.ForcePush = true
	if err := pair.first.engine.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	pair.sync(pair.first)
	if document, _, _ := remotePackage(t, pair.first); document.Version != 2 {
		t.Fatal("disabling token sync downgraded an existing v2 remote")
	}
}

func TestNarrowScopeUpgradesTokensAlreadyInV1Package(t *testing.T) {
	pair := newPair(t)
	ctx := context.Background()
	passphrase := []byte("correct horse battery")
	pair.configure(pair.first, passphrase)
	created := createAccessToken(t, pair.first.store, "pre-release token")
	pair.sync(pair.first)
	document, payload, etag := remotePackage(t, pair.first)
	document.Version = 1
	encoded, err := document.Encode()
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := syncseal.Parse(payload)
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := pair.first.store.SyncKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	sealed, err := syncseal.SealWithKey(key, encoded, pkg.Salt, pkg.KDF)
	if err != nil {
		t.Fatal(err)
	}
	legacyPayload, err := syncseal.Marshal(sealed)
	if err != nil {
		t.Fatal(err)
	}
	state, err := pair.first.store.SyncState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := pair.first.engine.remote(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Write(ctx, legacyPayload, etag); err != nil {
		t.Fatal(err)
	}
	config := pair.syncConfig()
	config.Scope.Tokens = false
	if err := pair.second.engine.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	if err := pair.second.engine.SetPassphrase(ctx, passphrase); err != nil {
		t.Fatal(err)
	}
	pair.sync(pair.second)
	document, _, _ = remotePackage(t, pair.second)
	if _, ok := document.Get(KindAccessToken, string(created.Token.ID)); !ok || document.Version != 2 {
		t.Fatal("v1 token data was dropped or left writable by legacy clients")
	}
}

type updateBeforeWriteRemote struct {
	remote.Remote
	update func(context.Context, string) error
}

func (target *updateBeforeWriteRemote) Write(ctx context.Context, payload []byte, etag string) (string, error) {
	if target.update != nil {
		update := target.update
		target.update = nil
		if err := update(ctx, etag); err != nil {
			return "", err
		}
	}
	return target.Remote.Write(ctx, payload, etag)
}

func TestForcePushRetryPreservesNewRemoteTokensAndVersion(t *testing.T) {
	pair := newPair(t)
	ctx := context.Background()
	config := pair.syncConfig()
	config.Scope.Tokens = false
	if err := pair.first.engine.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	if err := pair.first.engine.SetPassphrase(ctx, []byte("correct horse battery")); err != nil {
		t.Fatal(err)
	}
	pair.sync(pair.first)
	created := createAccessToken(t, pair.first.store, "arrived during force push")
	document, payload, _ := remotePackage(t, pair.first)
	entities, err := pair.first.store.SyncEntities(ctx, KindAccessToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range entities {
		document.Put(KindAccessToken, entity.LocalID, Entity{Document: entity.Document, Secret: encodeSecret(entity.Secret), UpdatedAt: entity.UpdatedAt})
		clear(entity.Secret)
	}
	document.Version = 2
	encoded, err := document.Encode()
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := syncseal.Parse(payload)
	if err != nil {
		t.Fatal(err)
	}
	key, salt, err := pair.first.store.SyncKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	sealed, err := syncseal.SealWithKey(key, encoded, salt, pkg.KDF)
	if err != nil {
		t.Fatal(err)
	}
	newerPayload, err := syncseal.Marshal(sealed)
	if err != nil {
		t.Fatal(err)
	}
	config.ForcePush = true
	if err := pair.first.engine.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	state, err := pair.first.store.SyncState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := pair.first.engine.remote(state)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := target.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base, hasBase, err := pair.first.engine.baseline(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	changing := &updateBeforeWriteRemote{Remote: target, update: func(ctx context.Context, etag string) error {
		_, err := target.Write(ctx, newerPayload, etag)
		return err
	}}
	if err := pair.first.engine.forcePush(ctx, state, changing, identity, key, salt, pkg.KDF, base, hasBase); err != nil {
		t.Fatal(err)
	}
	document, _, _ = remotePackage(t, pair.first)
	if _, ok := document.Get(KindAccessToken, string(created.Token.ID)); !ok || document.Version != 2 {
		t.Fatal("conditional-write retry lost the new token or downgraded its package")
	}
}

type saveBeforeBaselineStore struct {
	storage.SyncStore
	save func() error
}

func (store *saveBeforeBaselineStore) PutBaseline(ctx context.Context, payload []byte, revision int64) error {
	if err := store.save(); err != nil {
		return err
	}
	return store.SyncStore.PutBaseline(ctx, payload, revision)
}

func TestForcePushPreservesConcurrentSettingsAndNewRequests(t *testing.T) {
	for _, scenario := range []string{"rename", "disable", "rearm", "retarget", "scope"} {
		t.Run(scenario, func(t *testing.T) {
			pair := newPair(t)
			ctx := context.Background()
			pair.configure(pair.first, []byte("correct horse battery"))
			config := pair.syncConfig()
			config.ForcePush = true
			if err := pair.first.engine.Configure(ctx, config); err != nil {
				t.Fatal(err)
			}
			config, err := pair.first.engine.Config(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := config
			pair.first.engine.store = &saveBeforeBaselineStore{SyncStore: pair.first.store, save: func() error {
				next := config
				next.DeviceName = "saved during sync"
				switch scenario {
				case "disable":
					next.Enabled = false
				case "rearm":
					next.ForcePush = false
					if err := pair.first.engine.Configure(ctx, next); err != nil {
						return err
					}
					next.ForcePush = true
				case "retarget":
					next.Remote.Path = "another-folder"
				case "scope":
					next.Scope.Tokens = false
				}
				want = next
				if scenario == "rename" || scenario == "disable" {
					want.ForcePush = false
				}
				return pair.first.engine.Configure(ctx, next)
			}}
			pair.sync(pair.first)
			after, err := pair.first.engine.Config(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, want) {
				t.Fatal("a completed force push overwrote saved settings or consumed the wrong request")
			}
		})
	}
}

func TestUnreadableAccessTokenAbortsSyncWithoutRevokingOtherDevices(t *testing.T) {
	for _, forcePush := range []bool{false, true} {
		for _, failure := range []string{"corrupt", "missing"} {
			name := failure
			if forcePush {
				name += "_force_push"
			}
			t.Run(name, func(t *testing.T) {
				pair := newPair(t)
				ctx := context.Background()
				pair.first.clock = time.Now().UTC().Add(time.Hour)
				pair.second.clock = pair.first.clock
				passphrase := []byte("correct horse battery")
				pair.configure(pair.first, passphrase)
				pair.configure(pair.second, passphrase)
				created := createAccessToken(t, pair.first.store, "still valid elsewhere")
				pair.sync(pair.first)
				pair.sync(pair.second)
				pair.sync(pair.first)
				_, before, beforeTag := remotePackage(t, pair.first)
				db, err := sql.Open("sqlite", pair.first.databasePath)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				query := `UPDATE local_access_token_secrets SET token_value = zeroblob(length(token_value)) WHERE token_id = ?`
				if failure == "missing" {
					query = `DELETE FROM local_access_token_secrets WHERE token_id = ?`
				}
				if _, err := db.ExecContext(ctx, query, created.Token.ID); err != nil {
					t.Fatal(err)
				}
				if failure == "corrupt" {
					authenticateAccessToken(t, pair.first.store, created.Value)
				}
				if forcePush {
					config := pair.syncConfig()
					config.ForcePush = true
					if err := pair.first.engine.Configure(ctx, config); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := pair.first.engine.Sync(ctx); err == nil || !strings.Contains(err.Error(), string(created.Token.ID)) {
					t.Fatalf("sync must report the unreadable token: %v", err)
				}
				_, after, afterTag := remotePackage(t, pair.first)
				if beforeTag != afterTag || !bytes.Equal(before, after) {
					t.Fatal("a failed token read changed the remote package")
				}
				pair.sync(pair.second)
				authenticateAccessToken(t, pair.second.store, created.Value)
			})
		}
	}
}
