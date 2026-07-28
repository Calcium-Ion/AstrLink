package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestOpenMigratesDatabaseAndUsesRestrictiveFileModes(t *testing.T) {
	dataDirectory := filepath.Join(t.TempDir(), "data")
	databasePath := filepath.Join(dataDirectory, "astrlink.db")
	store := openTestStore(t, databasePath)
	defer store.Close()

	info, err := os.Stat(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("data directory mode = %o, want 700", info.Mode().Perm())
	}
	for _, path := range []string{databasePath, databasePath + "-wal", databasePath + "-shm"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", filepath.Base(path), err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", filepath.Base(path), info.Mode().Perm())
		}
	}

	var tableCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('services', 'service_credentials')`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 2 {
		t.Fatalf("migrated table count = %d, want 2", tableCount)
	}
}

func TestEndpointAndCredentialRoundTripKeepsSecretOutOfDocumentJSON(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	endpoint := testEndpoint("endpoint_01")
	secret := []byte("provider-secret-value")

	record, err := store.CreateEndpoint(ctx, endpoint, storagecontract.CredentialMutation{Present: true, Secret: secret})
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	if record.Endpoint.CredentialRef != "local://service/endpoint_01" || record.ETag == "" {
		t.Fatalf("created endpoint = %#v", record)
	}
	var document string
	if err := store.db.QueryRow(`SELECT document_json FROM services WHERE id = ?`, endpoint.ID).Scan(&document); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(document, string(secret)) || !strings.Contains(document, `"credential_ref":"local://service/endpoint_01"`) {
		t.Fatalf("credential storage boundary violated: %s", document)
	}
	loaded, err := store.GetEndpoint(ctx, endpoint.ID)
	if err != nil {
		t.Fatalf("GetEndpoint: %v", err)
	}
	if !reflect.DeepEqual(loaded, record) {
		t.Fatalf("loaded = %#v, want %#v", loaded, record)
	}
	loadedSecret, err := store.Get(ctx, secretstore.Ref(record.Endpoint.CredentialRef))
	if err != nil {
		t.Fatalf("Get credential: %v", err)
	}
	if string(loadedSecret) != string(secret) {
		t.Fatalf("credential = %q", loadedSecret)
	}
	loadedSecret[0] = 'X'
	reloadedSecret, err := store.Get(ctx, secretstore.Ref(record.Endpoint.CredentialRef))
	if err != nil || string(reloadedSecret) != string(secret) {
		t.Fatalf("CredentialStore returned aliased bytes: %q, %v", reloadedSecret, err)
	}
}

func TestEndpointUpdateUsesETagAndRotatesOrDeletesCredentialAtomically(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	created, err := store.CreateEndpoint(ctx, testEndpoint("endpoint_01"), storagecontract.CredentialMutation{Present: true, Secret: []byte("first-secret")})
	if err != nil {
		t.Fatal(err)
	}

	updatedEndpoint := created.Endpoint
	updatedEndpoint.Name = "updated"
	if _, err := store.UpdateEndpoint(ctx, updatedEndpoint, storagecontract.CredentialMutation{Present: true, Secret: []byte("second-secret")}, `"stale"`); !errors.Is(err, storagecontract.ErrPrecondition) {
		t.Fatalf("stale UpdateEndpoint error = %v", err)
	}
	secret, err := store.Get(ctx, secretstore.Ref(created.Endpoint.CredentialRef))
	if err != nil || string(secret) != "first-secret" {
		t.Fatalf("failed update changed credential: %q, %v", secret, err)
	}

	updated, err := store.UpdateEndpoint(ctx, updatedEndpoint, storagecontract.CredentialMutation{Present: true, Secret: []byte("second-secret")}, created.ETag)
	if err != nil {
		t.Fatalf("UpdateEndpoint: %v", err)
	}
	if updated.ETag == created.ETag || updated.Endpoint.Name != "updated" {
		t.Fatalf("updated record = %#v", updated)
	}
	secret, err = store.Get(ctx, secretstore.Ref(updated.Endpoint.CredentialRef))
	if err != nil || string(secret) != "second-secret" {
		t.Fatalf("rotated credential = %q, %v", secret, err)
	}

	withoutCredential, err := store.UpdateEndpoint(ctx, updated.Endpoint, storagecontract.CredentialMutation{Present: true}, updated.ETag)
	if err != nil {
		t.Fatalf("delete credential update: %v", err)
	}
	if withoutCredential.Endpoint.CredentialRef != "" {
		t.Fatalf("credential_ref survived deletion: %#v", withoutCredential.Endpoint)
	}
	if _, err := store.Get(ctx, secretstore.Ref(updated.Endpoint.CredentialRef)); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("deleted credential Get error = %v", err)
	}
}

func TestEndpointDeleteChecksETagRouteReferencesAndCredentialCascade(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	created, err := store.CreateEndpoint(ctx, testEndpoint("endpoint_01"), storagecontract.CredentialMutation{Present: true, Secret: []byte("provider-secret")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteEndpoint(ctx, created.Endpoint.ID, `"stale"`); !errors.Is(err, storagecontract.ErrPrecondition) {
		t.Fatalf("stale DeleteEndpoint error = %v", err)
	}

	route := contract.Route{
		ID: "route_01", Name: "default", Enabled: true,
		Match: contract.RouteMatch{Protocol: contract.ProtocolOpenAIResponses},
		Targets: []contract.RouteTarget{{
			ServiceID: created.Endpoint.ID, PlanType: contract.PlanTypeNative,
			UpstreamProtocol: contract.ProtocolOpenAIResponses,
		}},
	}
	document, err := json.Marshal(route)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO routes (id, document_json, created_at, updated_at) VALUES (?, ?, ?, ?)`, route.ID, document, "now", "now"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteEndpoint(ctx, created.Endpoint.ID, created.ETag); !errors.Is(err, storagecontract.ErrConflict) {
		t.Fatalf("referenced DeleteEndpoint error = %v", err)
	}
	if _, err := store.db.Exec(`DELETE FROM routes WHERE id = ?`, route.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteEndpoint(ctx, created.Endpoint.ID, created.ETag); err != nil {
		t.Fatalf("DeleteEndpoint: %v", err)
	}
	if _, err := store.GetEndpoint(ctx, created.Endpoint.ID); !errors.Is(err, storagecontract.ErrNotFound) {
		t.Fatalf("deleted endpoint Get error = %v", err)
	}
	if _, err := store.Get(ctx, secretstore.Ref(created.Endpoint.CredentialRef)); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("cascaded credential Get error = %v", err)
	}
}

func TestListEndpointsPaginatesFiltersAndRejectsCorruptDocuments(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	for _, id := range []contract.ServiceID{"endpoint_01", "endpoint_02", "endpoint_03"} {
		endpoint := testEndpoint(id)
		if id == "endpoint_02" {
			endpoint.Enabled = false
		}
		if _, err := store.CreateEndpoint(ctx, endpoint, storagecontract.CredentialMutation{}); err != nil {
			t.Fatal(err)
		}
	}

	enabled := true
	page, err := store.ListEndpoints(ctx, storagecontract.EndpointListOptions{Limit: 1, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Endpoint.ID != "endpoint_01" || page.NextCursor == "" {
		t.Fatalf("first page = %#v", page)
	}
	page, err = store.ListEndpoints(ctx, storagecontract.EndpointListOptions{Limit: 1, Cursor: page.NextCursor, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Endpoint.ID != "endpoint_03" || page.NextCursor != "" {
		t.Fatalf("second page = %#v", page)
	}
	if _, err := store.ListEndpoints(ctx, storagecontract.EndpointListOptions{Cursor: "not+a+cursor"}); err == nil {
		t.Fatal("invalid cursor was accepted")
	}

	if _, err := store.db.Exec(`UPDATE services SET document_json = '{"id":"endpoint_02","unexpected":true}' WHERE id = 'endpoint_02'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetEndpoint(ctx, "endpoint_02"); !errors.Is(err, storagecontract.ErrInvalidRecord) {
		t.Fatalf("corrupt endpoint error = %v", err)
	}
	if _, err := store.ListEndpoints(ctx, storagecontract.EndpointListOptions{}); !errors.Is(err, storagecontract.ErrInvalidRecord) {
		t.Fatalf("list corrupt endpoint error = %v", err)
	}
}

func TestLocalCredentialStoreRejectsOptionalKeyringBackendAndInvalidSecrets(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	if _, err := store.Get(ctx, "keyring://endpoint/endpoint_01"); !errors.Is(err, storagecontract.ErrUnsupportedRef) {
		t.Fatalf("keyring Get error = %v", err)
	}
	if err := store.Put(ctx, "local://service/endpoint_01", []byte("line\nbreak")); err == nil {
		t.Fatal("credential with HTTP control character was accepted")
	}
}

func TestDefaultAccessTokenBootstrapsOnceAndSurvivesRestart(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "astrlink.db")
	store := openTestStore(t, databasePath)
	manager, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	tokens, err := manager.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || tokens[0].Name != accesstoken.DefaultTokenName ||
		tokens[0].Source != accesstoken.SourceBootstrap {
		t.Fatalf("bootstrap tokens = %#v", tokens)
	}
	id := tokens[0].ID
	raw, err := manager.Reveal(ctx, id)
	if err != nil {
		t.Fatalf("Reveal bootstrap token: %v", err)
	}
	if authenticated, err := manager.Authenticate(ctx, raw); err != nil || authenticated != id {
		t.Fatalf("Authenticate bootstrap token = %q, %v", authenticated, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store = openTestStore(t, databasePath)
	manager, err = accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err = manager.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || tokens[0].ID != id {
		t.Fatalf("tokens after restart = %#v", tokens)
	}
	if revealed, err := manager.Reveal(ctx, id); err != nil || revealed != raw {
		t.Fatalf("Reveal after restart = %q, %v", revealed, err)
	}
	if err := manager.Delete(ctx, id); err != nil {
		t.Fatalf("Delete bootstrap token: %v", err)
	}
	var tokenCount, secretCount, bootstrapCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM local_access_tokens`).Scan(&tokenCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM local_access_token_secrets`).Scan(&secretCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM local_access_token_bootstrap_state`).Scan(&bootstrapCount); err != nil {
		t.Fatal(err)
	}
	if tokenCount != 0 || secretCount != 0 || bootstrapCount != 1 {
		t.Fatalf("post-delete counts token=%d secret=%d bootstrap=%d", tokenCount, secretCount, bootstrapCount)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store = openTestStore(t, databasePath)
	defer store.Close()
	manager, err = accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err = manager.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Fatalf("deleted bootstrap token was recreated: %#v", tokens)
	}
}

func TestAccessTokenCreateListRevealAuthenticateAndDelete(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	manager, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	created, err := manager.Create(ctx, "  CI Agent  ")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Token.Name != "CI Agent" || created.Token.Source != accesstoken.SourceUser ||
		!strings.HasPrefix(created.Value, "astr_") || len(created.Value) != 48 {
		t.Fatalf("created = %#v", created)
	}

	var storedHash []byte
	var storedSecret string
	if err := store.db.QueryRow(`SELECT token_hash FROM local_access_tokens WHERE id = ?`, created.Token.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT token_value FROM local_access_token_secrets WHERE token_id = ?`, created.Token.ID).Scan(&storedSecret); err != nil {
		t.Fatal(err)
	}
	expectedHash := sha256.Sum256([]byte(created.Value))
	if !bytes.Equal(storedHash, expectedHash[:]) || storedSecret != created.Value {
		t.Fatal("access token hash/secret persistence mismatch")
	}

	tokens, err := manager.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := json.Marshal(tokens)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(serialized, []byte(created.Value)) ||
		bytes.Contains(serialized, []byte(hex.EncodeToString(expectedHash[:]))) ||
		bytes.Contains(serialized, []byte(base64.RawURLEncoding.EncodeToString(expectedHash[:]))) {
		t.Fatalf("list leaked secret/hash: %s", serialized)
	}
	if revealed, err := manager.Reveal(ctx, created.Token.ID); err != nil || revealed != created.Value {
		t.Fatalf("Reveal = %q, %v", revealed, err)
	}
	if id, err := manager.Authenticate(ctx, created.Value); err != nil || id != created.Token.ID {
		t.Fatalf("Authenticate = %q, %v", id, err)
	}
	if _, err := manager.Create(ctx, "ci agent"); !errors.Is(err, accesstoken.ErrConflict) {
		t.Fatalf("case-insensitive duplicate error = %v", err)
	}
	if _, err := manager.Create(ctx, "Σ Agent"); err != nil {
		t.Fatalf("Create Unicode case-fold token: %v", err)
	}
	if _, err := manager.Create(ctx, "ς agent"); !errors.Is(err, accesstoken.ErrConflict) {
		t.Fatalf("Unicode case-insensitive duplicate error = %v", err)
	}
	if err := manager.Delete(ctx, created.Token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Reveal(ctx, created.Token.ID); !errors.Is(err, accesstoken.ErrNotFound) {
		t.Fatalf("deleted Reveal error = %v", err)
	}
	if _, err := manager.Authenticate(ctx, created.Value); !errors.Is(err, accesstoken.ErrInvalidToken) {
		t.Fatalf("deleted Authenticate error = %v", err)
	}
}

func TestAccessTokenLimitIsEnforcedAtomically(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	manager, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// The automatically created default consumes one of the 100 slots.
	for index := 1; index < storagecontract.AccessTokenLimit; index++ {
		if _, err := manager.Create(ctx, fmt.Sprintf("client-%03d", index)); err != nil {
			t.Fatalf("Create %d: %v", index, err)
		}
	}
	if _, err := manager.Create(ctx, "one-too-many"); !errors.Is(err, accesstoken.ErrTokenLimit) {
		t.Fatalf("limit error = %v", err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM local_access_tokens`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != storagecontract.AccessTokenLimit {
		t.Fatalf("access token count = %d", count)
	}
}

func TestDefaultAccessTokenBootstrapRollsBackAsOneTransaction(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	if _, err := store.db.Exec(`DELETE FROM local_access_tokens`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM local_access_token_bootstrap_state`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_access_token_secret
BEFORE INSERT ON local_access_token_secrets
BEGIN
    SELECT RAISE(ABORT, 'injected secret failure');
END`); err != nil {
		t.Fatal(err)
	}
	manager, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.EnsureDefault(ctx); err == nil {
		t.Fatal("EnsureDefault unexpectedly succeeded")
	}
	for _, table := range []string{
		"local_access_tokens",
		"local_access_token_secrets",
		"local_access_token_bootstrap_state",
	} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s count after rollback = %d", table, count)
		}
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_access_token_secret`); err != nil {
		t.Fatal(err)
	}
	if _, created, err := manager.EnsureDefault(ctx); err != nil || !created {
		t.Fatalf("retry EnsureDefault created=%t, error=%v", created, err)
	}
}

func TestAccessTokenAuthenticationFailsClosedForCorruptStoredRecords(t *testing.T) {
	otherRaw := "astr_" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x7f}, 32))
	tests := []struct {
		name   string
		mutate func(*testing.T, *Store, contract.AccessTokenID)
	}{
		{
			name: "hash length",
			mutate: func(t *testing.T, store *Store, id contract.AccessTokenID) {
				enableIgnoredChecks(t, store)
				mustExec(t, store, `UPDATE local_access_tokens SET token_hash = ? WHERE id = ?`, []byte{0x01}, id)
			},
		},
		{
			name: "invalid id",
			mutate: func(t *testing.T, store *Store, id contract.AccessTokenID) {
				mustExec(t, store, `PRAGMA foreign_keys = OFF`)
				mustExec(t, store, `UPDATE local_access_tokens SET id = 'INVALID_ID' WHERE id = ?`, id)
			},
		},
		{
			name: "noncanonical name",
			mutate: func(t *testing.T, store *Store, id contract.AccessTokenID) {
				mustExec(t, store, `UPDATE local_access_tokens SET name = ' padded ' WHERE id = ?`, id)
			},
		},
		{
			name: "name key mismatch",
			mutate: func(t *testing.T, store *Store, id contract.AccessTokenID) {
				mustExec(t, store, `UPDATE local_access_tokens SET name_key = 'different' WHERE id = ?`, id)
			},
		},
		{
			name: "invalid source",
			mutate: func(t *testing.T, store *Store, id contract.AccessTokenID) {
				enableIgnoredChecks(t, store)
				mustExec(t, store, `UPDATE local_access_tokens SET source = 'other' WHERE id = ?`, id)
			},
		},
		{
			name: "invalid timestamp",
			mutate: func(t *testing.T, store *Store, id contract.AccessTokenID) {
				mustExec(t, store, `UPDATE local_access_tokens SET created_at = 'not-a-time' WHERE id = ?`, id)
			},
		},
		{
			name: "missing secret",
			mutate: func(t *testing.T, store *Store, id contract.AccessTokenID) {
				mustExec(t, store, `DELETE FROM local_access_token_secrets WHERE token_id = ?`, id)
			},
		},
		{
			name: "invalid secret format",
			mutate: func(t *testing.T, store *Store, id contract.AccessTokenID) {
				mustExec(t, store, `UPDATE local_access_token_secrets SET token_value = ? WHERE token_id = ?`, strings.Repeat("!", 48), id)
			},
		},
		{
			name: "secret hash mismatch",
			mutate: func(t *testing.T, store *Store, id contract.AccessTokenID) {
				mustExec(t, store, `UPDATE local_access_token_secrets SET token_value = ? WHERE token_id = ?`, otherRaw, id)
			},
		},
		{
			name: "hint mismatch",
			mutate: func(t *testing.T, store *Store, id contract.AccessTokenID) {
				mustExec(t, store, `UPDATE local_access_tokens SET token_hint = 'astr_…AAAAAA' WHERE id = ?`, id)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
			defer store.Close()
			manager, err := accesstoken.NewManager(store)
			if err != nil {
				t.Fatal(err)
			}
			created, err := manager.Create(context.Background(), "corruption target")
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, store, created.Token.ID)
			id, err := manager.Authenticate(context.Background(), created.Value)
			if id != "" || !errors.Is(err, accesstoken.ErrInvalidToken) {
				t.Fatalf("Authenticate corrupt record = %q, %v", id, err)
			}
			if strings.Contains(fmt.Sprint(err), created.Value) {
				t.Fatal("authentication error leaked raw token")
			}
		})
	}
}

func enableIgnoredChecks(t *testing.T, store *Store) {
	t.Helper()
	mustExec(t, store, `PRAGMA ignore_check_constraints = ON`)
}

func mustExec(t *testing.T, store *Store, query string, args ...any) {
	t.Helper()
	if _, err := store.db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return store
}

func testEndpoint(id contract.ServiceID) contract.Endpoint {
	return contract.Endpoint{
		ID: id, Name: string(id), Kind: contract.EndpointKindOpenAI,
		BaseURL: "https://api.example/v1", Auth: contract.EndpointAuth{Scheme: contract.AuthSchemeBearer},
		Enabled: true,
		Capabilities: []contract.Capability{{
			Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true,
		}},
	}
}
