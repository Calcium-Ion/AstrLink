package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

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
	syncKindAccessToken            = "access_token"
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
	case syncKindAccessToken:
		// The value a client presents cascades with the row that names it.
		if _, err := store.db.ExecContext(ctx, `DELETE FROM local_access_tokens WHERE id = ?`, localID); err != nil {
			return fmt.Errorf("delete synced access token: %w", err)
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
	rows, err := transaction.QueryContext(ctx, `SELECT id, sort_position FROM services ORDER BY sort_position, id`)
	if err != nil {
		return fmt.Errorf("read services for reorder: %w", err)
	}
	current := map[contract.ServiceID]int{}
	// The list keeps the query's order, so services the merged order never
	// named are placed by the position they already hold rather than by map
	// iteration order, which differs from run to run.
	var ordered []contract.ServiceID
	for rows.Next() {
		var id contract.ServiceID
		var position int
		if err := rows.Scan(&id, &position); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read service for reorder: %w", err)
		}
		current[id] = position
		ordered = append(ordered, id)
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
		if _, known := current[id]; !known || placed[id] {
			return nil
		}
		placed[id] = true
		// A service already in this position is left alone: the trigger on
		// UPDATE would otherwise record a reorder that did not happen.
		if current[id] != position {
			if _, err := transaction.ExecContext(ctx, `UPDATE services SET sort_position = ? WHERE id = ?`, position, id); err != nil {
				return fmt.Errorf("reorder service: %w", err)
			}
		}
		position++
		return nil
	}
	for _, id := range ids {
		if err := apply(id); err != nil {
			return err
		}
	}
	for _, id := range ordered {
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
	unchanged, err := store.syncDocumentUnchanged(ctx, `SELECT document_json FROM services WHERE id = ?`, id, encoded,
		func(stored []byte) ([]byte, error) { return normalizeStoredService(id, stored) })
	if err != nil {
		return err
	}
	if unchanged {
		return nil
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

// normalizeStoredService puts the document a service row already holds through
// the same decode and encode the write path uses. A stored service carries the
// references this device bound locally, so comparing it against a merged
// document has to compare the two configurations rather than those references.
func normalizeStoredService(id contract.ServiceID, document []byte) ([]byte, error) {
	service, err := decodeSyncedService(id, document)
	if err != nil {
		return nil, err
	}
	return encodeService(service)
}

// syncDocumentUnchanged reports whether a row already holds the document a
// merge decided on. Rewriting a row that is already correct is not free: the
// change-log trigger fires on every UPDATE, so a no-op write would record a
// change that never happened and schedule another sync for it. A row whose
// document this build cannot read reports "changed", so the write goes ahead
// and repairs it.
func (store *Store) syncDocumentUnchanged(
	ctx context.Context,
	query string,
	key any,
	encoded []byte,
	normalize func([]byte) ([]byte, error),
) (bool, error) {
	var stored string
	if err := store.db.QueryRowContext(ctx, query, key).Scan(&stored); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("read stored document: %w", err)
	}
	if stored == string(encoded) {
		return true, nil
	}
	normalized, err := normalize([]byte(stored))
	if err != nil {
		return false, nil
	}
	return bytes.Equal(normalized, encoded), nil
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
	encoded, err := normalizeSyncedPolicy(document)
	if err != nil {
		return err
	}
	unchanged, err := store.syncDocumentUnchanged(ctx, `SELECT document_json FROM policies WHERE id = ?`, localID, encoded,
		normalizeSyncedPolicy)
	if err != nil {
		return err
	}
	if unchanged {
		return nil
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `UPDATE policies SET document_json = ?, updated_at = ? WHERE id = ?`,
		string(encoded), now, contract.DefaultPrivacyPolicyID); err != nil {
		return fmt.Errorf("write synced policy: %w", err)
	}
	return nil
}

// normalizeSyncedPolicy decodes, validates, and re-encodes a policy document.
// The write path and the no-op comparison share it, so the two cannot drift
// apart on how a policy is spelled.
func normalizeSyncedPolicy(document []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var policy contract.Policy
	if err := decoder.Decode(&policy); err != nil {
		return nil, fmt.Errorf("%w: decode synced policy: %v", storage.ErrInvalidArgument, err)
	}
	policy.ID = contract.DefaultPrivacyPolicyID
	if err := contract.ValidatePrivacyDefault(policy); err != nil {
		return nil, fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	contract.NormalizePrivacyPolicyDefaults(&policy)
	encoded, err := json.Marshal(policy)
	if err != nil {
		return nil, fmt.Errorf("encode synced policy: %w", err)
	}
	return encoded, nil
}

// syncWritePricingConfig writes one service's price bindings. A price binding
// with no service here is skipped: the merge drops a service and its pricing
// together, so a row left over would be a reference to nothing.
func (store *Store) syncWritePricingConfig(ctx context.Context, localID string, document []byte) error {
	encoded, err := normalizeSyncedPricingConfig(document)
	if err != nil {
		return err
	}
	if _, err := store.GetService(ctx, contract.ServiceID(localID)); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil
		}
		return err
	}
	unchanged, err := store.syncDocumentUnchanged(ctx, `SELECT document_json FROM pricing_configs WHERE service_id = ?`, localID, encoded,
		normalizeSyncedPricingConfig)
	if err != nil {
		return err
	}
	if unchanged {
		return nil
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO pricing_configs (service_id, document_json) VALUES (?, ?)
ON CONFLICT(service_id) DO UPDATE SET document_json = excluded.document_json`,
		localID, string(encoded)); err != nil {
		return fmt.Errorf("write synced pricing config: %w", err)
	}
	return nil
}

// normalizeSyncedPricingConfig decodes, validates, and re-encodes a price
// binding document, shared by the write path and the no-op comparison.
func normalizeSyncedPricingConfig(document []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var config pricing.Config
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("%w: decode synced pricing config: %v", storage.ErrInvalidArgument, err)
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	if config.Bindings == nil {
		config.Bindings = map[string]pricing.Binding{}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode synced pricing config: %w", err)
	}
	return encoded, nil
}

// SyncWriteAccessToken writes one gateway access token a merge chose, with the
// value a client presents. The value is what the token is: the digest an
// authentication compares and the hint it checks with are derived from it here
// rather than taken from the package, so a package can neither name a token
// nobody can present nor point an existing name at another value.
//
// A name two devices each used is resolved by their ids, so both tokens are
// kept and every device ends up with the same names.
//
// The creation limit the interface applies is deliberately not enforced here. A
// merge has to be able to hold what the other devices hold, or the token would
// be deleted from every device on the next run.
func (store *Store) SyncWriteAccessToken(ctx context.Context, localID string, document, value []byte) (err error) {
	id := contract.AccessTokenID(localID)
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	if !validAccessTokenValue(string(value)) {
		return fmt.Errorf("%w: access token value is not a canonical token", storage.ErrInvalidArgument)
	}
	synced, err := decodeSyncedAccessToken(document)
	if err != nil {
		return err
	}
	name, nameKey, err := normalizeAccessTokenName(synced.Name)
	if err != nil {
		return fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	source := storage.AccessTokenSource(synced.Source)
	if !source.Valid() {
		return fmt.Errorf("%w: access token source %q is unknown", storage.ErrInvalidArgument, synced.Source)
	}
	// A package that lost the stamp still names a token; the moment this device
	// stored it is the only other time there is.
	createdAt := synced.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = store.now().UTC()
	}
	hash := sha256.Sum256(value)
	unchanged, err := store.accessTokenUnchanged(ctx, id, name, source, createdAt, value, hash)
	if err != nil {
		return err
	}
	if unchanged {
		return nil
	}

	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin access token write: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	if name, nameKey, err = store.uniqueAccessTokenName(ctx, transaction, id, name); err != nil {
		return err
	}
	// A digest cannot be held twice, so a value already stored under another id
	// is the same token. Only the package's id can be the one two devices share,
	// so the other row is the one that goes; the duplicate it leaves behind is
	// resolved by the next merge.
	if _, err = transaction.ExecContext(ctx,
		`DELETE FROM local_access_tokens WHERE token_hash = ? AND id <> ?`, hash[:], id); err != nil {
		return fmt.Errorf("drop duplicate access token: %w", err)
	}
	if _, err = transaction.ExecContext(ctx, `INSERT INTO local_access_tokens
    (id, name, name_key, token_hash, token_hint, source, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    name = excluded.name, name_key = excluded.name_key, token_hash = excluded.token_hash,
    token_hint = excluded.token_hint, source = excluded.source, created_at = excluded.created_at`,
		id, name, nameKey, hash[:], accessTokenHint(string(value)), string(source),
		createdAt.Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("write synced access token: %w", err)
	}
	sealed, err := store.keys.sealColumn(accessTokenSecretsTable, string(id), value)
	if err != nil {
		return fmt.Errorf("seal synced access token: %w", err)
	}
	if _, err = transaction.ExecContext(ctx, `INSERT INTO local_access_token_secrets (token_id, token_value, sealed)
VALUES (?, ?, 1)
ON CONFLICT(token_id) DO UPDATE SET token_value = excluded.token_value, sealed = 1`,
		id, sealed,
	); err != nil {
		return fmt.Errorf("write synced access token secret: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit access token write: %w", err)
	}
	return nil
}

// accessTokenUnchanged reports whether a row already holds exactly this token.
// Rewriting it would fire the change-log trigger, which would schedule a sync
// for a change that never happened.
func (store *Store) accessTokenUnchanged(
	ctx context.Context,
	id contract.AccessTokenID,
	name string,
	source storage.AccessTokenSource,
	createdAt time.Time,
	value []byte,
	hash [sha256.Size]byte,
) (bool, error) {
	var storedName, storedSource, storedCreatedAt string
	var storedHash []byte
	err := store.db.QueryRowContext(ctx, `SELECT name, source, created_at, token_hash
FROM local_access_tokens WHERE id = ?`, id).Scan(&storedName, &storedSource, &storedCreatedAt, &storedHash)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read stored access token: %w", err)
	}
	if storedName != name || storedSource != string(source) || !bytes.Equal(storedHash, hash[:]) {
		return false, nil
	}
	storedAt, err := time.Parse(time.RFC3339Nano, storedCreatedAt)
	if err != nil || !storedAt.Equal(createdAt) {
		return false, nil
	}
	// The names agree, so the value is the only thing left to compare. It is
	// stored sealed, and a value this device cannot open is rewritten: that is
	// also how a row left sealed under an older key heals.
	var stored []byte
	var sealed bool
	if err := store.db.QueryRowContext(ctx, `SELECT token_value, sealed FROM local_access_token_secrets WHERE token_id = ?`, id).Scan(&stored, &sealed); err != nil {
		return false, nil
	}
	plaintext, err := store.openSecret(accessTokenSecretsTable, string(id), stored, sealed)
	if err != nil {
		return false, nil
	}
	defer clear(plaintext)
	return bytes.Equal(plaintext, value), nil
}

// uniqueAccessTokenName returns the name and name key the arriving token is
// stored under.
//
// Two devices may each have named a token the same thing before they met, and
// the table keeps names unique, so one of them has to move. Which one is decided
// by the ids rather than by which device synced first: the smaller id keeps the
// name it was given and the larger takes its own id as a suffix. Every device
// computes the same answer, so the two settle instead of renaming each other's
// tokens on run after run.
func (store *Store) uniqueAccessTokenName(
	ctx context.Context,
	transaction *sql.Tx,
	id contract.AccessTokenID,
	name string,
) (string, string, error) {
	nameKey := simpleFoldAccessTokenName(name)
	holder, err := accessTokenNameHolder(ctx, transaction, nameKey)
	if err != nil {
		return "", "", err
	}
	if holder == "" || holder == string(id) {
		return name, nameKey, nil
	}
	if string(id) < holder {
		// This token is the smaller one, so the name is its own and the row that
		// has to move is the one holding it.
		holderID := contract.AccessTokenID(holder)
		moved, movedKey, err := store.freeAccessTokenName(ctx, transaction, holderID, name)
		if err != nil {
			return "", "", err
		}
		if _, err := transaction.ExecContext(ctx,
			`UPDATE local_access_tokens SET name = ?, name_key = ? WHERE id = ?`,
			moved, movedKey, holderID); err != nil {
			return "", "", fmt.Errorf("rename access token: %w", err)
		}
		return name, nameKey, nil
	}
	// The token that has the name keeps it: this one takes the name its own id
	// makes for it.
	return store.freeAccessTokenName(ctx, transaction, id, name)
}

// freeAccessTokenName is the suffixed name a token takes when a smaller id holds
// the name it wanted. The suffix is the token's own id, so the name depends
// neither on when the token arrived nor on which device stored it first.
func (store *Store) freeAccessTokenName(
	ctx context.Context,
	transaction *sql.Tx,
	id contract.AccessTokenID,
	name string,
) (string, string, error) {
	for _, candidate := range []string{
		accessTokenNameWithSuffix(name, accessTokenIDTail(id)),
		accessTokenNameWithSuffix(name, string(id)),
	} {
		key := simpleFoldAccessTokenName(candidate)
		holder, err := accessTokenNameHolder(ctx, transaction, key)
		if err != nil {
			return "", "", err
		}
		if holder == "" || holder == string(id) {
			return candidate, key, nil
		}
	}
	return "", "", fmt.Errorf("%w: no free name for access token %s", storage.ErrConflict, id)
}

// accessTokenNameHolder returns the id of the token that holds a name key, or an
// empty string when the name is free.
func accessTokenNameHolder(ctx context.Context, transaction *sql.Tx, nameKey string) (string, error) {
	var id string
	err := transaction.QueryRowContext(ctx, `SELECT id FROM local_access_tokens WHERE name_key = ?`, nameKey).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read access token name holder: %w", err)
	}
	return id, nil
}

// accessTokenNameWithSuffix appends a suffix to a name, keeping the result
// inside the length the table allows.
func accessTokenNameWithSuffix(name, suffix string) string {
	const limit = 64
	tail := " (" + suffix + ")"
	runes := []rune(strings.TrimSpace(name))
	if room := limit - utf8.RuneCountInString(tail); len(runes) > room {
		runes = runes[:room]
	}
	return strings.TrimRight(string(runes), " ") + tail
}

// accessTokenIDTail is the last few characters of a token id, which tell two
// tokens apart in a name without spelling the whole id out.
func accessTokenIDTail(id contract.AccessTokenID) string {
	const tail = 6
	runes := []rune(id)
	if len(runes) <= tail {
		return string(runes)
	}
	return string(runes[len(runes)-tail:])
}

// decodeSyncedAccessToken reads the metadata half of a token entity.
func decodeSyncedAccessToken(document []byte) (contract.AccessTokenDocument, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var record contract.AccessTokenDocument
	if err := decoder.Decode(&record); err != nil {
		return contract.AccessTokenDocument{}, fmt.Errorf("%w: decode synced access token: %v", storage.ErrInvalidArgument, err)
	}
	return record, nil
}
