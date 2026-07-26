package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestPrivacyModelInstallationStorePersistsPrivateManifestBinding(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	record := testReadyPrivacyModelRecord()
	if err := store.PutPrivacyModelInstallation(ctx, record); err != nil {
		t.Fatalf("PutPrivacyModelInstallation: %v", err)
	}
	loaded, err := store.GetPrivacyModelInstallation(
		ctx,
		record.Installation.ID,
	)
	if err != nil || !reflect.DeepEqual(loaded, record) {
		t.Fatalf("GetPrivacyModelInstallation=%#v, %v", loaded, err)
	}
	listed, err := store.ListPrivacyModelInstallations(ctx)
	if err != nil || len(listed) != 1 ||
		!reflect.DeepEqual(listed[0], record) {
		t.Fatalf("ListPrivacyModelInstallations=%#v, %v", listed, err)
	}
	if err := store.DeletePrivacyModelInstallation(
		ctx,
		record.Installation.ID,
	); err != nil {
		t.Fatalf("DeletePrivacyModelInstallation: %v", err)
	}
	if _, err := store.GetPrivacyModelInstallation(
		ctx,
		record.Installation.ID,
	); !errors.Is(err, storagecontract.ErrNotFound) {
		t.Fatalf("deleted Get error=%v", err)
	}
}

func TestPrivacyModelInstallationStoreRejectsMissingOrCorruptBinding(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	record := testReadyPrivacyModelRecord()
	withoutBinding := record
	withoutBinding.Manifest = nil
	if err := store.PutPrivacyModelInstallation(
		ctx,
		withoutBinding,
	); !errors.Is(err, storagecontract.ErrInvalidArgument) {
		t.Fatalf("missing binding error=%v", err)
	}
	customWithCatalog := record
	customWithCatalog.Installation.Source = contract.PrivacyModelSourceCustom
	if err := store.PutPrivacyModelInstallation(
		ctx,
		customWithCatalog,
	); !errors.Is(err, storagecontract.ErrInvalidArgument) {
		t.Fatalf("custom catalog_id error=%v", err)
	}
	if err := store.PutPrivacyModelInstallation(ctx, record); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(
		ctx,
		`UPDATE privacy_model_installations
SET manifest_sha256 = ?
WHERE id = ?`,
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		record.Installation.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPrivacyModelInstallation(
		ctx,
		record.Installation.ID,
	); !errors.Is(err, storagecontract.ErrInvalidRecord) {
		t.Fatalf("corrupt binding error=%v", err)
	}
}

func testReadyPrivacyModelRecord() storagecontract.PrivacyModelInstallationRecord {
	catalogID := contract.PrivacyModelCatalogID("catalog_test_model")
	catalogSource := contract.PrivacyModelCatalogSourceCommunity
	license := "Apache-2.0"
	email := contract.CanonicalKindEmail
	installedAt := "2026-07-24T00:00:00Z"
	document := []byte(`{"version":1,"files":[{"path":"model.onnx","size":1,"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`)
	digest := sha256.Sum256(document)
	installation := contract.PrivacyModelInstallation{
		ID:            contract.PrivacyModelID("model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		Source:        contract.PrivacyModelSourceCatalog,
		CatalogID:     &catalogID,
		CatalogSource: &catalogSource,
		Name:          "Test Model",
		License:       &license,
		Languages:     []string{"en"},
		RepoID:        "acme/privacy-model",
		Revision:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		VariantID:     "q4", VariantName: "Q4",
		Quantization:    "q4",
		Adapter:         contract.PrivacyModelAdapterHFToken,
		Status:          contract.PrivacyModelStatusReady,
		BytesDownloaded: 1, BytesTotal: 1,
		EstimatedRAMBytes: 2,
		LabelMapping:      map[string]*contract.CanonicalKind{"EMAIL": &email},
		InstalledAt:       &installedAt,
	}
	return storagecontract.PrivacyModelInstallationRecord{
		Installation: installation,
		Manifest: &storagecontract.PrivacyModelManifestBinding{
			Identity: installation.RepoID + "@" +
				installation.Revision + "#" + installation.VariantID,
			SHA256: hex.EncodeToString(digest[:]),
			JSON:   document,
		},
	}
}
