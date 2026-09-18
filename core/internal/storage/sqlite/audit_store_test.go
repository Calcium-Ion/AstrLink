package sqlite

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestAuditSettingsDefaultsAndKeyOnce(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()

	settings, err := store.GetAuditSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.RequestBodyEnabled || settings.ResponseContentEnabled {
		t.Fatalf("defaults enabled: %#v", settings)
	}
	if !settings.HTTPMetaEnabled {
		t.Fatalf("http_meta_enabled must default to true: %#v", settings)
	}
	if settings.RequestBodyMaxBytes != contract.DefaultRequestBodyMaxBytes {
		t.Fatalf("request max = %d", settings.RequestBodyMaxBytes)
	}

	settings.RequestBodyEnabled = true
	settings.ResponseContentEnabled = true
	settings.HTTPMetaEnabled = false
	if err := store.UpdateAuditSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetAuditSettings(ctx)
	if err != nil || !loaded.RequestBodyEnabled || !loaded.ResponseContentEnabled {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	if loaded.HTTPMetaEnabled {
		t.Fatalf("http_meta_enabled did not round-trip false: %#v", loaded)
	}

	key1, err := store.GetOrCreateAuditKey(ctx)
	if err != nil || len(key1) != storagecontract.AuditKeyBytes {
		t.Fatalf("key1=%v err=%v", key1, err)
	}
	key2, err := store.GetOrCreateAuditKey(ctx)
	if err != nil || !bytes.Equal(key1, key2) {
		t.Fatalf("key regenerated: %v vs %v err=%v", key1, key2, err)
	}
}

func TestAuditBlobUpsertReplacesSameDirection(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	key, err := store.GetOrCreateAuditKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	record := contract.RequestRecord{
		ID: "request_upsert", StartedAt: store.now(), Status: contract.RequestStatusPending,
		InputProtocol: contract.ProtocolOpenAIChat, Audit: contract.NotCapturedAuditSummary(),
	}
	if err := store.InsertRequestRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	firstNonce, firstCipher, err := storagecontract.SealAuditBlob(key, []byte(`{"first":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertAuditBlob(ctx, storagecontract.AuditBlob{
		RequestID: "request_upsert", Direction: storagecontract.AuditDirectionHTTPMeta,
		MediaType: "application/json", Nonce: firstNonce, Ciphertext: firstCipher, CapturedBytes: 14,
	}); err != nil {
		t.Fatal(err)
	}
	secondNonce, secondCipher, err := storagecontract.SealAuditBlob(key, []byte(`{"second":true,"status":200}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertAuditBlob(ctx, storagecontract.AuditBlob{
		RequestID: "request_upsert", Direction: storagecontract.AuditDirectionHTTPMeta,
		MediaType: "application/json", Nonce: secondNonce, Ciphertext: secondCipher, CapturedBytes: 29,
	}); err != nil {
		t.Fatal(err)
	}
	blobs, err := store.GetAuditBlobsByRequest(ctx, "request_upsert")
	if err != nil || len(blobs) != 1 {
		t.Fatalf("blobs=%#v err=%v", blobs, err)
	}
	plain, err := storagecontract.OpenAuditBlob(key, blobs[0].Nonce, blobs[0].Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != `{"second":true,"status":200}` || blobs[0].CapturedBytes != 29 {
		t.Fatalf("upserted blob=%#v plain=%s", blobs[0], plain)
	}
}

func TestAuditBlobCascadeDeletePurgeAndSweep(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	key, err := store.GetOrCreateAuditKey(ctx)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return start.Add(40 * 24 * time.Hour) }
	old := contract.RequestRecord{
		ID: "request_old", StartedAt: start, Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
	}
	recent := contract.RequestRecord{
		ID: "request_new", StartedAt: start.Add(35 * 24 * time.Hour),
		Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses,
		Audit: contract.NotCapturedAuditSummary(),
	}
	for _, record := range []contract.RequestRecord{old, recent} {
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []contract.RequestID{"request_old", "request_new"} {
		nonce, ciphertext, err := storagecontract.SealAuditBlob(key, []byte(`{"ok":true}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.InsertAuditBlob(ctx, storagecontract.AuditBlob{
			RequestID: id, Direction: storagecontract.AuditDirectionRequest,
			MediaType: "application/json", Nonce: nonce, Ciphertext: ciphertext,
			CapturedBytes: 11, CreatedAt: start,
		}); err != nil {
			t.Fatal(err)
		}
		metaNonce, metaCiphertext, err := storagecontract.SealAuditBlob(key, []byte(`{"method":"POST"}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.InsertAuditBlob(ctx, storagecontract.AuditBlob{
			RequestID: id, Direction: storagecontract.AuditDirectionHTTPMeta,
			MediaType: "application/json", Nonce: metaNonce, Ciphertext: metaCiphertext,
			CapturedBytes: 17, CreatedAt: start,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.DeleteRequestRecord(ctx, "request_new"); err != nil {
		t.Fatal(err)
	}
	blobs, err := store.GetAuditBlobsByRequest(ctx, "request_new")
	if err != nil || len(blobs) != 0 {
		t.Fatalf("cascade delete failed: %#v err=%v", blobs, err)
	}
	remaining, err := store.GetAuditBlobsByRequest(ctx, "request_old")
	if err != nil || len(remaining) != 2 {
		t.Fatalf("request_old blobs=%d err=%v, want body + http_meta", len(remaining), err)
	}

	settings := contract.DefaultAuditSettings()
	settings.MetadataRetentionDays = 30
	settings.ContentRetentionDays = 7
	if err := store.UpdateAuditSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	result, err := store.SweepExpiredAuditData(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedRecords < 1 || result.DeletedAuditBlobs < 1 {
		t.Fatalf("sweep=%#v", result)
	}
	if _, err := store.GetRequestRecord(ctx, "request_old"); err == nil {
		t.Fatal("old metadata should be swept")
	}

	fresh := contract.RequestRecord{
		ID: "request_purge", StartedAt: store.now(), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIChat, Audit: contract.NotCapturedAuditSummary(),
	}
	if err := store.InsertRequestRecord(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	nonce, ciphertext, err := storagecontract.SealAuditBlob(key, []byte("body"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertAuditBlob(ctx, storagecontract.AuditBlob{
		RequestID: "request_purge", Direction: storagecontract.AuditDirectionResponse,
		MediaType: "text/plain", Nonce: nonce, Ciphertext: ciphertext, CapturedBytes: 4,
	}); err != nil {
		t.Fatal(err)
	}
	purge, err := store.PurgeRequestRecords(ctx, contract.PurgeRequest{
		Scope: contract.PurgeScopeAll, Confirm: true,
	})
	if err != nil || purge.DeletedRecords != 1 || purge.DeletedAuditBlobs != 1 {
		t.Fatalf("purge=%#v err=%v", purge, err)
	}
}
