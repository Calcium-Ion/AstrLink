package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

const maxStoredPrivacyModelManifestBytes = 256 << 10

var storedManifestDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (store *Store) ListPrivacyModelInstallations(
	ctx context.Context,
) ([]storagecontract.PrivacyModelInstallationRecord, error) {
	rows, err := store.db.QueryContext(
		ctx,
		`SELECT id, document_json, manifest_identity, manifest_sha256, manifest_json
FROM privacy_model_installations ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list privacy model installations: %w", err)
	}
	defer rows.Close()
	result := make([]storagecontract.PrivacyModelInstallationRecord, 0)
	for rows.Next() {
		var id, document string
		var manifestIdentity, manifestSHA256, manifestJSON sql.NullString
		if err := rows.Scan(
			&id, &document, &manifestIdentity, &manifestSHA256, &manifestJSON,
		); err != nil {
			return nil, fmt.Errorf("scan privacy model installation: %w", err)
		}
		record, err := decodePrivacyModelInstallation(
			id,
			[]byte(document),
			manifestIdentity,
			manifestSHA256,
			manifestJSON,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate privacy model installations: %w", err)
	}
	return result, nil
}

func (store *Store) GetPrivacyModelInstallation(
	ctx context.Context,
	id contract.PrivacyModelID,
) (storagecontract.PrivacyModelInstallationRecord, error) {
	if err := id.Validate(); err != nil {
		return storagecontract.PrivacyModelInstallationRecord{},
			fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	var document string
	var manifestIdentity, manifestSHA256, manifestJSON sql.NullString
	if err := store.db.QueryRowContext(
		ctx,
		`SELECT document_json, manifest_identity, manifest_sha256, manifest_json
FROM privacy_model_installations WHERE id = ?`,
		id,
	).Scan(
		&document, &manifestIdentity, &manifestSHA256, &manifestJSON,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storagecontract.PrivacyModelInstallationRecord{},
				fmt.Errorf("%w: privacy model installation %q", storagecontract.ErrNotFound, id)
		}
		return storagecontract.PrivacyModelInstallationRecord{},
			fmt.Errorf("read privacy model installation: %w", err)
	}
	return decodePrivacyModelInstallation(
		string(id),
		[]byte(document),
		manifestIdentity,
		manifestSHA256,
		manifestJSON,
	)
}

func (store *Store) PutPrivacyModelInstallation(
	ctx context.Context,
	record storagecontract.PrivacyModelInstallationRecord,
) error {
	installation := record.Installation
	if err := contract.ValidatePrivacyModelInstallation(installation); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	if err := validatePrivacyModelManifestBinding(
		installation,
		record.Manifest,
	); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	document, err := json.Marshal(installation)
	if err != nil {
		return fmt.Errorf("encode privacy model installation: %w", err)
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	var manifestIdentity, manifestSHA256, manifestJSON any
	if record.Manifest != nil {
		manifestIdentity = record.Manifest.Identity
		manifestSHA256 = record.Manifest.SHA256
		manifestJSON = string(record.Manifest.JSON)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO privacy_model_installations
	    (id, document_json, manifest_identity, manifest_sha256, manifest_json, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
	    document_json = excluded.document_json,
	    manifest_identity = excluded.manifest_identity,
	    manifest_sha256 = excluded.manifest_sha256,
	    manifest_json = excluded.manifest_json,
	    updated_at = excluded.updated_at`,
		installation.ID,
		string(document),
		manifestIdentity,
		manifestSHA256,
		manifestJSON,
		now,
		now,
	); err != nil {
		return fmt.Errorf("persist privacy model installation: %w", err)
	}
	return nil
}

func (store *Store) DeletePrivacyModelInstallation(
	ctx context.Context,
	id contract.PrivacyModelID,
) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	result, err := store.db.ExecContext(
		ctx,
		`DELETE FROM privacy_model_installations WHERE id = ?`,
		id,
	)
	if err != nil {
		return fmt.Errorf("delete privacy model installation: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read privacy model installation delete result: %w", err)
	}
	if deleted == 0 {
		return fmt.Errorf("%w: privacy model installation %q", storagecontract.ErrNotFound, id)
	}
	return nil
}

func decodePrivacyModelInstallation(
	rowID string,
	document []byte,
	manifestIdentity sql.NullString,
	manifestSHA256 sql.NullString,
	manifestJSON sql.NullString,
) (storagecontract.PrivacyModelInstallationRecord, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var installation contract.PrivacyModelInstallation
	if err := decoder.Decode(&installation); err != nil {
		return storagecontract.PrivacyModelInstallationRecord{},
			fmt.Errorf("%w: decode privacy model installation %q", storagecontract.ErrInvalidRecord, rowID)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return storagecontract.PrivacyModelInstallationRecord{},
			fmt.Errorf("%w: decode privacy model installation %q", storagecontract.ErrInvalidRecord, rowID)
	}
	if string(installation.ID) != rowID {
		return storagecontract.PrivacyModelInstallationRecord{},
			fmt.Errorf("%w: privacy model row id does not match document id", storagecontract.ErrInvalidRecord)
	}
	if err := contract.ValidatePrivacyModelInstallation(installation); err != nil {
		return storagecontract.PrivacyModelInstallationRecord{},
			fmt.Errorf("%w: privacy model installation %q violates the contract", storagecontract.ErrInvalidRecord, rowID)
	}
	record := storagecontract.PrivacyModelInstallationRecord{
		Installation: installation,
	}
	switch {
	case manifestIdentity.Valid && manifestSHA256.Valid && manifestJSON.Valid:
		record.Manifest = &storagecontract.PrivacyModelManifestBinding{
			Identity: manifestIdentity.String,
			SHA256:   manifestSHA256.String,
			JSON:     []byte(manifestJSON.String),
		}
	case manifestIdentity.Valid || manifestSHA256.Valid || manifestJSON.Valid:
		return storagecontract.PrivacyModelInstallationRecord{},
			fmt.Errorf("%w: privacy model manifest binding is incomplete", storagecontract.ErrInvalidRecord)
	}
	if err := validatePrivacyModelManifestBinding(
		installation,
		record.Manifest,
	); err != nil {
		return storagecontract.PrivacyModelInstallationRecord{},
			fmt.Errorf("%w: privacy model manifest binding is invalid", storagecontract.ErrInvalidRecord)
	}
	return record, nil
}

func validatePrivacyModelManifestBinding(
	installation contract.PrivacyModelInstallation,
	binding *storagecontract.PrivacyModelManifestBinding,
) error {
	if installation.Status == contract.PrivacyModelStatusReady {
		if binding == nil {
			return fmt.Errorf("ready installation requires a manifest binding")
		}
	} else if binding != nil {
		return fmt.Errorf("non-ready installation cannot retain a manifest binding")
	}
	if binding == nil {
		return nil
	}
	expectedIdentity := installation.RepoID + "@" +
		installation.Revision + "#" + installation.VariantID
	if binding.Identity != expectedIdentity ||
		!storedManifestDigestPattern.MatchString(binding.SHA256) ||
		len(binding.JSON) == 0 ||
		len(binding.JSON) > maxStoredPrivacyModelManifestBytes ||
		!json.Valid(binding.JSON) {
		return fmt.Errorf("manifest binding is invalid")
	}
	digest := sha256.Sum256(binding.JSON)
	if hex.EncodeToString(digest[:]) != binding.SHA256 {
		return fmt.Errorf("manifest digest does not match document")
	}
	return nil
}
