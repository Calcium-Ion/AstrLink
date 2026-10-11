package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// The kinds a package may carry. They are the entity_kind the change log
// records, so a merge result names the same things the log does.
const (
	syncKindService                = "service"
	syncKindServiceCredential      = "service_credential"
	syncKindServiceProxyCredential = "service_proxy_credential"
	syncKindBuiltinToolCredential  = "builtin_tool_credential"
	syncKindPolicy                 = "policy"
	syncKindPricingConfig          = "pricing_config"
)

// SyncWriteEntity writes one entity a merge chose, under its local id. A
// document is validated before it is stored, so a package written by another
// device cannot introduce a row this device would refuse to read.
//
// Device-local references are not taken from the document: a credential_ref
// names this device's own row, so a service arrives with its references
// cleared and SyncBindCredentialRefs points them at the rows that exist here
// afterwards. A subscription account is the one thing a package cannot carry
// at all: its status is this device's, so an account with no local token
// arrives signed out, waiting for the user to sign in here.
func (store *Store) SyncWriteEntity(ctx context.Context, kind, localID string, document []byte) error {
	switch kind {
	case syncKindService:
		return store.syncWriteService(ctx, localID, document)
	case syncKindPolicy:
		return store.syncWritePolicy(ctx, localID, document)
	case syncKindPricingConfig:
		return store.syncWritePricingConfig(ctx, localID, document)
	default:
		return fmt.Errorf("%w: %s is not a stored entity kind", storage.ErrInvalidArgument, kind)
	}
}

// SyncDeleteEntity removes one entity a merge chose is gone. Deleting a service
// cascades to its credentials; the mappings and tombstones that named it are
// the engine's business, not this method's.
func (store *Store) SyncDeleteEntity(ctx context.Context, kind, localID string) error {
	switch kind {
	case syncKindService:
		if _, err := store.db.ExecContext(ctx, `DELETE FROM services WHERE id = ?`, localID); err != nil {
			return fmt.Errorf("delete synced service: %w", err)
		}
		return nil
	case syncKindPolicy:
		// The fixed privacy policy is part of the product, not user content: a
		// device recreates it rather than receiving a deletion for it.
		if localID == string(contract.DefaultPrivacyPolicyID) {
			return nil
		}
		if _, err := store.db.ExecContext(ctx, `DELETE FROM policies WHERE id = ?`, localID); err != nil {
			return fmt.Errorf("delete synced policy: %w", err)
		}
		return nil
	case syncKindPricingConfig:
		if _, err := store.db.ExecContext(ctx, `DELETE FROM pricing_configs WHERE service_id = ?`, localID); err != nil {
			return fmt.Errorf("delete synced pricing config: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("%w: %s is not a stored entity kind", storage.ErrInvalidArgument, kind)
	}
}

// SyncWriteOrder applies the service priority list a merge resolved. The list
// is positional, so an id this device does not have is skipped rather than
// landing on an unknown row, and a service the list never named is placed after
// the named ones instead of keeping whatever position it happened to hold.
func (store *Store) SyncWriteOrder(ctx context.Context, ids []contract.ServiceID) error {
	if len(ids) == 0 {
		return nil
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin service order write: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	// Acquire the SQLite write lock before reading, so the order is applied
	// against the committed current list.
	if _, err = transaction.ExecContext(ctx, `UPDATE services SET sort_position = sort_position WHERE 0`); err != nil {
		return fmt.Errorf("lock services for reorder: %w", err)
	}
	rows, err := transaction.QueryContext(ctx, `SELECT id FROM services`)
	if err != nil {
		return fmt.Errorf("read services for reorder: %w", err)
	}
	known := map[contract.ServiceID]bool{}
	for rows.Next() {
		var id contract.ServiceID
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read service for reorder: %w", err)
		}
		known[id] = true
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("read services for reorder: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read services for reorder: %w", err)
	}
	position := 0
	placed := map[contract.ServiceID]bool{}
	apply := func(id contract.ServiceID) error {
		if !known[id] || placed[id] {
			return nil
		}
		placed[id] = true
		if _, err := transaction.ExecContext(ctx, `UPDATE services SET sort_position = ? WHERE id = ?`, position, id); err != nil {
			return fmt.Errorf("reorder service: %w", err)
		}
		position++
		return nil
	}
	for _, id := range ids {
		if err := apply(id); err != nil {
			return err
		}
	}
	for id := range known {
		if err := apply(id); err != nil {
			return err
		}
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit service order write: %w", err)
	}
	return nil
}

// SyncBindCredentialRefs points every service's credential references at the
// rows this device actually holds, and signs out a subscription account that
// has no local token. It runs after the credentials of a merge are imported, so
// a reference never outlives the row it names, and it also repairs a device
// whose rows were changed without a document write.
func (store *Store) SyncBindCredentialRefs(ctx context.Context) (err error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin credential rebind: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	rows, err := transaction.QueryContext(ctx, `SELECT id, document_json FROM services`)
	if err != nil {
		return fmt.Errorf("read services for rebind: %w", err)
	}
	type pending struct {
		id       contract.ServiceID
		document []byte
	}
	var services []pending
	for rows.Next() {
		var id, document string
		if err := rows.Scan(&id, &document); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read service for rebind: %w", err)
		}
		services = append(services, pending{id: contract.ServiceID(id), document: []byte(document)})
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("read services for rebind: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read services for rebind: %w", err)
	}
	for _, service := range services {
		present, err := store.credentialPresenceTx(ctx, transaction, service.id)
		if err != nil {
			return err
		}
		rebound, err := rebindServiceRefs(service.id, service.document, present)
		if err != nil {
			// A row this build cannot read is left alone rather than blocking
			// the whole sync; the services list reports it separately.
			continue
		}
		if bytes.Equal(rebound, service.document) {
			continue
		}
		if _, err := transaction.ExecContext(ctx, `UPDATE services SET document_json = ? WHERE id = ?`,
			string(rebound), service.id); err != nil {
			return fmt.Errorf("rebind service credentials: %w", err)
		}
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit credential rebind: %w", err)
	}
	return nil
}

// credentialPresence is which of a service's credentials this device holds.
type credentialPresence struct {
	api, proxy, subscription bool
}

func (store *Store) credentialPresenceTx(ctx context.Context, transaction *sql.Tx, id contract.ServiceID) (credentialPresence, error) {
	var presence credentialPresence
	var err error
	if presence.api, err = rowExistsTx(ctx, transaction, `SELECT EXISTS(SELECT 1 FROM service_credentials WHERE service_id = ?)`, id); err != nil {
		return presence, err
	}
	if presence.proxy, err = rowExistsTx(ctx, transaction, `SELECT EXISTS(SELECT 1 FROM service_proxy_credentials WHERE service_id = ?)`, id); err != nil {
		return presence, err
	}
	if presence.subscription, err = rowExistsTx(ctx, transaction, `SELECT EXISTS(SELECT 1 FROM subscription_credentials WHERE service_id = ?)`, id); err != nil {
		return presence, err
	}
	return presence, nil
}

func rowExistsTx(ctx context.Context, transaction *sql.Tx, query string, args ...any) (bool, error) {
	var exists bool
	if err := transaction.QueryRowContext(ctx, query, args...).Scan(&exists); err != nil {
		return false, fmt.Errorf("read row presence: %w", err)
	}
	return exists, nil
}

// rebindServiceRefs rewrites one service document's device-local references.
func rebindServiceRefs(id contract.ServiceID, document []byte, presence credentialPresence) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var service contract.Service
	if err := decoder.Decode(&service); err != nil {
		return nil, fmt.Errorf("%w: decode service %q: %v", storage.ErrInvalidRecord, id, err)
	}
	if string(service.ID) != string(id) {
		return nil, fmt.Errorf("%w: service row id does not match document", storage.ErrInvalidRecord)
	}
	if service.HTTP != nil {
		connection := *service.HTTP
		service.HTTP = &connection
		connection.CredentialRef = ""
		if presence.api {
			connection.CredentialRef = localServiceRef(id)
		}
	}
	if service.Proxy != nil {
		proxy := *service.Proxy
		service.Proxy = &proxy
		if proxy.Mode == "custom" {
			proxy.CredentialRef = ""
			if presence.proxy {
				proxy.CredentialRef = localProxyRef(id)
			}
		}
	}
	if service.Subscription != nil {
		connection := *service.Subscription
		service.Subscription = &connection
		if !presence.subscription {
			// The OAuth token stays on the device that signed in. Without one
			// here the account is signed out, and the user signs in again.
			connection.Status = contract.SubscriptionStatusDisconnected
			connection.CredentialRef = ""
			connection.TokenExpiresAt = nil
			connection.LastRefreshAt = nil
			connection.LastError = nil
		} else if connection.Status != contract.SubscriptionStatusAuthorizing {
			connection.CredentialRef = subscriptionRefPrefix + string(id)
		}
	}
	if err := service.Validate(); err != nil {
		return nil, fmt.Errorf("%w: service %q: %v", storage.ErrInvalidRecord, id, err)
	}
	encoded, err := json.Marshal(service)
	if err != nil {
		return nil, fmt.Errorf("encode service %q: %w", id, err)
	}
	return encoded, nil
}

func (store *Store) syncWriteService(ctx context.Context, localID string, document []byte) error {
	id := contract.ServiceID(localID)
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	service, err := decodeSyncedService(id, document)
	if err != nil {
		return err
	}
	encoded, err := encodeService(service)
	if err != nil {
		return fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	_, err = store.db.ExecContext(ctx, `INSERT INTO services (id, document_json, created_at, updated_at, sort_position)
VALUES (?, ?, ?, ?, (SELECT COALESCE(MAX(sort_position), -1) + 1 FROM services))
ON CONFLICT(id) DO UPDATE SET document_json = excluded.document_json, updated_at = excluded.updated_at`,
		service.ID, string(encoded), service.CreatedAt.UTC().Format(time.RFC3339Nano), now)
	if err != nil {
		return fmt.Errorf("write synced service: %w", err)
	}
	return nil
}

// decodeSyncedService reads a service document the way the services store does
// and drops every reference that names a row on another device. The references
// are set again by SyncBindCredentialRefs once this device's credential rows
// are in place.
func decodeSyncedService(id contract.ServiceID, document []byte) (contract.Service, error) {
	if containsSubscriptionSecret(document) {
		return contract.Service{}, fmt.Errorf("%w: service document contains secret material", storage.ErrInvalidArgument)
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var service contract.Service
	if err := decoder.Decode(&service); err != nil {
		return contract.Service{}, fmt.Errorf("%w: decode synced service: %v", storage.ErrInvalidArgument, err)
	}
	service.ID = id
	if service.HTTP != nil {
		connection := *service.HTTP
		service.HTTP = &connection
		connection.CredentialRef = ""
	}
	if service.Proxy != nil {
		proxy := *service.Proxy
		service.Proxy = &proxy
		proxy.CredentialRef = ""
	}
	if service.Subscription != nil {
		connection := *service.Subscription
		service.Subscription = &connection
		connection.Status = contract.SubscriptionStatusDisconnected
		connection.CredentialRef = ""
		connection.TokenExpiresAt = nil
		connection.LastRefreshAt = nil
		connection.LastError = nil
	}
	if err := service.Validate(); err != nil {
		return contract.Service{}, fmt.Errorf("%w: synced service is invalid: %v", storage.ErrInvalidArgument, err)
	}
	return service, nil
}

func localProxyRef(id contract.ServiceID) string { return "local://service-proxy/" + string(id) }

func (store *Store) syncWritePolicy(ctx context.Context, localID string, document []byte) error {
	if localID != string(contract.DefaultPrivacyPolicyID) {
		return fmt.Errorf("%w: this build stores only the fixed privacy policy", storage.ErrInvalidArgument)
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var policy contract.Policy
	if err := decoder.Decode(&policy); err != nil {
		return fmt.Errorf("%w: decode synced policy: %v", storage.ErrInvalidArgument, err)
	}
	policy.ID = contract.DefaultPrivacyPolicyID
	if err := contract.ValidatePrivacyDefault(policy); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	contract.NormalizePrivacyPolicyDefaults(&policy)
	encoded, err := json.Marshal(policy)
	if err != nil {
		return fmt.Errorf("encode synced policy: %w", err)
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `UPDATE policies SET document_json = ?, updated_at = ? WHERE id = ?`,
		string(encoded), now, policy.ID); err != nil {
		return fmt.Errorf("write synced policy: %w", err)
	}
	return nil
}

// syncWritePricingConfig writes one service's price bindings. A price binding
// with no service here is skipped: the merge drops a service and its pricing
// together, so a row left over would be a reference to nothing.
func (store *Store) syncWritePricingConfig(ctx context.Context, localID string, document []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var config pricing.Config
	if err := decoder.Decode(&config); err != nil {
		return fmt.Errorf("%w: decode synced pricing config: %v", storage.ErrInvalidArgument, err)
	}
	if err := config.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	if config.Bindings == nil {
		config.Bindings = map[string]pricing.Binding{}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode synced pricing config: %w", err)
	}
	if _, err := store.GetService(ctx, contract.ServiceID(localID)); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil
		}
		return err
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO pricing_configs (service_id, document_json) VALUES (?, ?)
ON CONFLICT(service_id) DO UPDATE SET document_json = excluded.document_json`,
		localID, string(encoded))
	if err != nil {
		return fmt.Errorf("write synced pricing config: %w", err)
	}
	return nil
}
