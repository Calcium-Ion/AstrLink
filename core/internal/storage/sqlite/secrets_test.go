package sqlite

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestSealedValuesAreBoundToTheirRow(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	for _, id := range []contract.ServiceID{"service_alpha", "service_beta"} {
		if _, err := store.CreateService(ctx, contract.ServiceFromEndpoint(testEndpoint(id)), storagecontract.CredentialMutation{
			Present: true, Secret: []byte("sk-" + string(id)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	var alpha []byte
	if err := store.db.QueryRow(`SELECT credential_value FROM service_credentials WHERE service_id = 'service_alpha'`).Scan(&alpha); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(alpha, []byte("sk-service_alpha")) || len(alpha) != len("sk-service_alpha")+29 {
		t.Fatalf("stored value is not a sealed column (%d bytes)", len(alpha))
	}
	mustExec(t, store, `UPDATE service_credentials SET credential_value = ? WHERE service_id = 'service_beta'`, alpha)
	if _, err := store.Get(ctx, "local://service/service_beta"); !errors.Is(err, secretstore.ErrUnavailable) {
		t.Fatalf("a value copied from another row = %v, want ErrUnavailable", err)
	}
	status, err := store.LocalDataStatus(ctx)
	if err != nil || status.UnreadableCredentials != 1 {
		t.Fatalf("LocalDataStatus = %+v, %v", status, err)
	}
	// A row not marked sealed is refused rather than read as plaintext.
	mustExec(t, store, `UPDATE service_credentials SET credential_value = ?, sealed = 0 WHERE service_id = 'service_alpha'`, []byte("sk-service_alpha"))
	if _, err := store.Get(ctx, "local://service/service_alpha"); !errors.Is(err, storagecontract.ErrInvalidRecord) {
		t.Fatalf("an unsealed row = %v, want ErrInvalidRecord", err)
	}
	if status, err := store.LocalDataStatus(ctx); err != nil || status.UnreadableCredentials != 2 {
		t.Fatalf("LocalDataStatus with an unsealed row = %+v, %v", status, err)
	}
}

func TestSubscriptionCredentialsAreSealedAndFollowTheirService(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	id := contract.ServiceID("service_codex")
	if _, err := store.CreateService(ctx, contract.ServiceFromEndpoint(testEndpoint(id)), storagecontract.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	ref := secretstore.Ref(subscriptionRefPrefix + string(id))
	tokens := []byte(`{"access_token":"PLAINTEXT-oauth-access","refresh_token":"PLAINTEXT-oauth-refresh"}`)
	if err := store.Put(ctx, ref, tokens); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := store.db.QueryRow(`SELECT credential_value FROM subscription_credentials WHERE service_id = ?`, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("PLAINTEXT-oauth")) {
		t.Fatal("OAuth tokens are stored in plaintext")
	}
	if got, err := store.Get(ctx, ref); err != nil || !bytes.Equal(got, tokens) {
		t.Fatalf("Get = %v", err)
	}
	for _, bad := range []struct {
		ref    secretstore.Ref
		secret []byte
	}{
		{secretstore.Ref(subscriptionRefPrefix + "BAD ID"), tokens},
		{ref, nil},
		{ref, bytes.Repeat([]byte("x"), maxSubscriptionCredentialLen+1)},
	} {
		if err := store.Put(ctx, bad.ref, bad.secret); err == nil {
			t.Fatalf("Put(%q, %d bytes) succeeded", bad.ref, len(bad.secret))
		}
	}
	if err := store.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, ref); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("Get after Delete = %v", err)
	}
	if err := store.Delete(ctx, ref); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("second Delete = %v", err)
	}

	if err := store.Put(ctx, ref, tokens); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetService(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteService(ctx, id, record.ETag); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, store, subscriptionCredentialsTable); got != 0 {
		t.Fatalf("deleting the service left %d subscription credentials", got)
	}
}

func TestLostLocalKeyLeavesSecretsUnavailableUntilReentered(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "astrlink.db")
	const serviceID = contract.ServiceID("service_lost_key")
	store := openWithKey(t, path, testLocalKey(t, 0x31), nil)
	service := contract.ServiceFromEndpoint(testEndpoint(serviceID))
	service.Proxy = &contract.ServiceProxy{Mode: "custom", URL: "socks5://proxy.example:1080"}
	if _, err := store.CreateService(ctx, service, storagecontract.CredentialMutation{
		Present: true, Secret: []byte("sk-service-key-before-the-key-was-lost"),
		ProxyPresent: true, Proxy: &contract.ProxyCredential{Username: "proxy-user", Password: "proxy-password"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "local://builtin-tool/web_search", []byte("tvly-tool-key-before-the-key-was-lost")); err != nil {
		t.Fatal(err)
	}
	ref := secretstore.Ref(subscriptionRefPrefix + string(serviceID))
	if err := store.Put(ctx, ref, []byte(`{"access_token":"a","refresh_token":"r"}`)); err != nil {
		t.Fatal(err)
	}
	manager, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := manager.List(ctx)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("access tokens = %d, %v", len(tokens), err)
	}
	tokenID := tokens[0].ID
	token, err := manager.Reveal(ctx, tokenID)
	if err != nil {
		t.Fatal(err)
	}
	if status, err := store.LocalDataStatus(ctx); err != nil || status != (contract.LocalDataStatus{}) {
		t.Fatalf("LocalDataStatus with the right key = %+v, %v", status, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store = openWithKey(t, path, testLocalKey(t, 0x32), nil)
	defer store.Close()
	for _, ref := range []secretstore.Ref{
		secretstore.Ref(localRef(serviceID)),
		"local://builtin-tool/web_search",
		secretstore.Ref("local://service-proxy/" + string(serviceID)),
		ref,
	} {
		if _, err := store.Get(ctx, ref); !errors.Is(err, secretstore.ErrUnavailable) {
			t.Fatalf("Get(%s) under a replacement key = %v, want ErrUnavailable", ref, err)
		}
	}
	status, err := store.LocalDataStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status != (contract.LocalDataStatus{UnreadableCredentials: 4, UnreadableAccessTokens: 1, AuditKeyMissing: true}) {
		t.Fatalf("LocalDataStatus under a replacement key = %+v", status)
	}
	manager, err = accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := manager.Authenticate(ctx, token); err != nil || id != tokenID {
		t.Fatalf("Authenticate under a replacement key = %q, %v", id, err)
	}
	if _, err := manager.Reveal(ctx, tokenID); !errors.Is(err, secretstore.ErrUnavailable) {
		t.Fatalf("Reveal under a replacement key = %v", err)
	}

	// Entering the credential again stores it under the new key.
	if err := store.Put(ctx, secretstore.Ref(localRef(serviceID)), []byte("sk-entered-again")); err != nil {
		t.Fatal(err)
	}
	if secret, err := store.Get(ctx, secretstore.Ref(localRef(serviceID))); err != nil || string(secret) != "sk-entered-again" {
		t.Fatalf("re-entered credential = %v", err)
	}
	if status, err := store.LocalDataStatus(ctx); err != nil || status.UnreadableCredentials != 3 {
		t.Fatalf("LocalDataStatus after re-entry = %+v, %v", status, err)
	}
}

func BenchmarkSecretGet(b *testing.B) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(b.TempDir(), "astrlink.db"), WithLocalKey(bytes.Repeat([]byte{0x41}, 32)), WithLogger(func(string, ...any) {}))
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateService(ctx, contract.ServiceFromEndpoint(testEndpoint("service_sealed")), storagecontract.CredentialMutation{
		Present: true, Secret: []byte("sk-benchmark-service_sealed-0123456789abcdef0123456789abcdef"),
	}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		secret, err := store.Get(ctx, "local://service/service_sealed")
		if err != nil {
			b.Fatal(err)
		}
		clear(secret)
	}
}

// classifier_credentials keeps its one row at id 1 but seals it under the
// name of its ref, "default". The local-data check and the plaintext sealing
// must open and seal it under that key, or a working key reads as unreadable.
func TestClassifierCredentialIsReadableToTheLocalDataCheck(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ref := secretstore.Ref("local://classifier/default")
	if err := store.Put(ctx, ref, []byte("sk-classifier")); err != nil {
		t.Fatal(err)
	}
	if status, err := store.LocalDataStatus(ctx); err != nil || status.UnreadableCredentials != 0 {
		t.Fatalf("LocalDataStatus after storing the classifier key = %+v, %v", status, err)
	}
	// A row left in plaintext is sealed under the same key the reads use.
	mustExec(t, store, `UPDATE classifier_credentials SET credential_value = ?, sealed = 0 WHERE id = 1`, []byte("sk-plain"))
	if sealed, err := store.SealPlaintextSecrets(ctx); err != nil || sealed != 1 {
		t.Fatalf("SealPlaintextSecrets = %d, %v", sealed, err)
	}
	value, err := store.Get(ctx, ref)
	if err != nil || string(value) != "sk-plain" {
		t.Fatalf("classifier key after sealing = %q, %v", value, err)
	}
	if status, err := store.LocalDataStatus(ctx); err != nil || status.UnreadableCredentials != 0 {
		t.Fatalf("LocalDataStatus after sealing = %+v, %v", status, err)
	}
}
