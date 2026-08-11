package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func (store *Store) CreateService(
	ctx context.Context,
	service contract.Service,
	credential storagecontract.CredentialMutation,
) (record storagecontract.ServiceRecord, err error) {
	now := store.now().UTC()
	if service.CreatedAt.IsZero() {
		service.CreatedAt = now
	}
	service.UpdatedAt = now
	service.Models, err = contract.NormalizeServiceModels(service.Models)
	if err != nil {
		return record, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	service, err = applyServiceCredentialMutation(service, credential)
	if err != nil {
		return record, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	document, err := encodeService(service)
	if err != nil {
		return record, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return record, fmt.Errorf("begin service create: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	timestamp := now.Format(time.RFC3339Nano)
	if _, err = transaction.ExecContext(ctx,
		`INSERT INTO services (id, document_json, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		service.ID, string(document), service.CreatedAt.UTC().Format(time.RFC3339Nano), timestamp,
	); err != nil {
		var exists int
		if scanErr := transaction.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM services WHERE id = ?)`, service.ID).Scan(&exists); scanErr == nil && exists == 1 {
			return record, fmt.Errorf("%w: service %q", storagecontract.ErrConflict, service.ID)
		}
		return record, fmt.Errorf("insert service: %w", err)
	}
	if service.Kind.IsHTTP() && credential.Present && len(credential.Secret) > 0 {
		if err = putServiceCredentialTx(ctx, transaction, service.ID, credential.Secret, timestamp); err != nil {
			return record, err
		}
	}
	if err = transaction.Commit(); err != nil {
		return record, fmt.Errorf("commit service create: %w", err)
	}
	return storagecontract.ServiceRecord{Service: service, ETag: entityTag(document)}, nil
}

func (store *Store) GetService(ctx context.Context, id contract.ServiceID) (storagecontract.ServiceRecord, error) {
	if err := id.Validate(); err != nil {
		return storagecontract.ServiceRecord{}, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	var document string
	if err := store.db.QueryRowContext(ctx, `SELECT document_json FROM services WHERE id = ?`, id).Scan(&document); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storagecontract.ServiceRecord{}, fmt.Errorf("%w: service %q", storagecontract.ErrNotFound, id)
		}
		return storagecontract.ServiceRecord{}, fmt.Errorf("read service: %w", err)
	}
	return decodeServiceRecord(string(id), []byte(document))
}

func (store *Store) ListServices(ctx context.Context, options storagecontract.ServiceListOptions) (storagecontract.ServicePage, error) {
	limit := options.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	if limit < 1 || limit > maxListLimit {
		return storagecontract.ServicePage{}, fmt.Errorf("%w: limit must be between 1 and %d", storagecontract.ErrInvalidArgument, maxListLimit)
	}
	after, err := decodeServiceCursor(options.Cursor)
	if err != nil {
		return storagecontract.ServicePage{}, err
	}
	rows, err := store.db.QueryContext(ctx, `SELECT id, document_json FROM services WHERE id > ? ORDER BY id`, after)
	if err != nil {
		return storagecontract.ServicePage{}, fmt.Errorf("list services: %w", err)
	}
	defer rows.Close()
	matched := make([]storagecontract.ServiceRecord, 0, limit+1)
	for rows.Next() {
		var id, document string
		if err := rows.Scan(&id, &document); err != nil {
			return storagecontract.ServicePage{}, fmt.Errorf("scan service: %w", err)
		}
		record, err := decodeServiceRecord(id, []byte(document))
		if err != nil {
			return storagecontract.ServicePage{}, err
		}
		if options.Enabled != nil && record.Service.Enabled != *options.Enabled {
			continue
		}
		if options.Kind != nil && record.Service.Kind != *options.Kind {
			continue
		}
		matched = append(matched, record)
		if len(matched) == limit+1 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return storagecontract.ServicePage{}, fmt.Errorf("iterate services: %w", err)
	}
	page := storagecontract.ServicePage{Items: matched}
	if len(matched) > limit {
		page.Items = matched[:limit]
		page.NextCursor = encodeServiceCursor(page.Items[len(page.Items)-1].Service.ID)
	}
	return page, nil
}

func (store *Store) UpdateService(
	ctx context.Context,
	service contract.Service,
	credential storagecontract.CredentialMutation,
	expectedETag string,
) (record storagecontract.ServiceRecord, err error) {
	if expectedETag == "" {
		return record, fmt.Errorf("%w: expected ETag is required", storagecontract.ErrInvalidArgument)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return record, fmt.Errorf("begin service update: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	var currentDocument string
	if err = transaction.QueryRowContext(ctx, `SELECT document_json FROM services WHERE id = ?`, service.ID).Scan(&currentDocument); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return record, fmt.Errorf("%w: service %q", storagecontract.ErrNotFound, service.ID)
		}
		return record, fmt.Errorf("read service for update: %w", err)
	}
	current, decodeErr := decodeServiceRecord(string(service.ID), []byte(currentDocument))
	if decodeErr != nil {
		return record, decodeErr
	}
	if entityTag([]byte(currentDocument)) != expectedETag {
		return record, fmt.Errorf("%w: service %q", storagecontract.ErrPrecondition, service.ID)
	}
	if current.Service.Kind != service.Kind {
		return record, fmt.Errorf("%w: service kind is immutable", storagecontract.ErrInvalidArgument)
	}
	service.CreatedAt = current.Service.CreatedAt
	service.UpdatedAt = store.now().UTC()
	service.Models, err = contract.NormalizeServiceModels(service.Models)
	if err != nil {
		return record, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	service, err = applyServiceCredentialMutation(service, credential)
	if err != nil {
		return record, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	document, err := encodeService(service)
	if err != nil {
		return record, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	now := service.UpdatedAt.Format(time.RFC3339Nano)
	if _, err = transaction.ExecContext(ctx, `UPDATE services SET document_json = ?, updated_at = ? WHERE id = ?`, string(document), now, service.ID); err != nil {
		return record, fmt.Errorf("update service: %w", err)
	}
	if service.Kind.IsHTTP() && credential.Present {
		if len(credential.Secret) == 0 {
			if _, err = transaction.ExecContext(ctx, `DELETE FROM service_credentials WHERE service_id = ?`, service.ID); err != nil {
				return record, fmt.Errorf("delete service credential: %w", err)
			}
		} else if err = putServiceCredentialTx(ctx, transaction, service.ID, credential.Secret, now); err != nil {
			return record, err
		}
	}
	if err = transaction.Commit(); err != nil {
		return record, fmt.Errorf("commit service update: %w", err)
	}
	return storagecontract.ServiceRecord{Service: service, ETag: entityTag(document)}, nil
}

func (store *Store) DeleteService(ctx context.Context, id contract.ServiceID, expectedETag string) (err error) {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	if expectedETag == "" {
		return fmt.Errorf("%w: expected ETag is required", storagecontract.ErrInvalidArgument)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin service delete: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	var document string
	if err = transaction.QueryRowContext(ctx, `SELECT document_json FROM services WHERE id = ?`, id).Scan(&document); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: service %q", storagecontract.ErrNotFound, id)
		}
		return fmt.Errorf("read service for delete: %w", err)
	}
	if _, err = decodeServiceRecord(string(id), []byte(document)); err != nil {
		return err
	}
	if entityTag([]byte(document)) != expectedETag {
		return fmt.Errorf("%w: service %q", storagecontract.ErrPrecondition, id)
	}
	referenced, referenceErr := routeReferencesEndpoint(ctx, transaction, id)
	if referenceErr != nil {
		return referenceErr
	}
	if referenced {
		return fmt.Errorf("%w: service %q is referenced by a route", storagecontract.ErrConflict, id)
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM services WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit service delete: %w", err)
	}
	return nil
}

func applyServiceCredentialMutation(service contract.Service, credential storagecontract.CredentialMutation) (contract.Service, error) {
	if !service.Kind.IsHTTP() {
		if credential.Present {
			return service, fmt.Errorf("subscription services cannot store HTTP credentials")
		}
		return service, nil
	}
	if service.HTTP == nil {
		return service, fmt.Errorf("http connection is required")
	}
	if !credential.Present {
		return service, nil
	}
	if len(credential.Secret) == 0 {
		service.HTTP.CredentialRef = ""
		return service, nil
	}
	if err := validateCredential(credential.Secret); err != nil {
		return service, err
	}
	service.HTTP.CredentialRef = localServiceRef(service.ID)
	return service, nil
}

func encodeService(service contract.Service) ([]byte, error) {
	models, err := contract.NormalizeServiceModels(service.Models)
	if err != nil {
		return nil, err
	}
	service.Models = models
	if err := service.Validate(); err != nil {
		return nil, err
	}
	document, err := json.Marshal(service)
	if err != nil {
		return nil, fmt.Errorf("encode service: %w", err)
	}
	if containsSubscriptionSecret(document) {
		return nil, fmt.Errorf("service document must not embed secret material")
	}
	return document, nil
}

func decodeServiceRecord(id string, document []byte) (storagecontract.ServiceRecord, error) {
	if containsSubscriptionSecret(document) {
		return storagecontract.ServiceRecord{}, fmt.Errorf("%w: service %q contains secret material", storagecontract.ErrInvalidRecord, id)
	}
	decoder := json.NewDecoder(strings.NewReader(string(document)))
	decoder.DisallowUnknownFields()
	var service contract.Service
	if err := decoder.Decode(&service); err != nil {
		return storagecontract.ServiceRecord{}, fmt.Errorf("%w: decode service %q: %v", storagecontract.ErrInvalidRecord, id, err)
	}
	if string(service.ID) != id {
		return storagecontract.ServiceRecord{}, fmt.Errorf("%w: service id mismatch", storagecontract.ErrInvalidRecord)
	}
	if err := service.Validate(); err != nil {
		return storagecontract.ServiceRecord{}, fmt.Errorf("%w: service %q: %v", storagecontract.ErrInvalidRecord, id, err)
	}
	return storagecontract.ServiceRecord{Service: service, ETag: entityTag(document)}, nil
}

func localServiceRef(id contract.ServiceID) string {
	return "local://service/" + string(id)
}

func putServiceCredentialTx(ctx context.Context, transaction *sql.Tx, id contract.ServiceID, secret []byte, now string) error {
	if err := validateCredential(secret); err != nil {
		return err
	}
	credentialValue := append([]byte(nil), secret...)
	defer clear(credentialValue)
	_, err := transaction.ExecContext(ctx, `INSERT INTO service_credentials (service_id, credential_value, created_at, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(service_id) DO UPDATE SET credential_value = excluded.credential_value, updated_at = excluded.updated_at`,
		id, credentialValue, now, now)
	if err != nil {
		return fmt.Errorf("write service credential: %w", err)
	}
	return nil
}

func encodeServiceCursor(id contract.ServiceID) string {
	return encodeCursor(id)
}

func decodeServiceCursor(cursor string) (contract.ServiceID, error) {
	decoded, err := decodeCursor(cursor)
	return contract.ServiceID(decoded), err
}

var _ storagecontract.ServiceStore = (*Store)(nil)
