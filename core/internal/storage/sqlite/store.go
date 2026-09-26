// Package sqlite owns AstrLink's persistent SQLite configuration, credential,
// and local access-token storage.
package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/migrate"
	_ "modernc.org/sqlite"
)

const (
	driverName       = "sqlite"
	defaultListLimit = 50
	maxListLimit     = 200
	maxCredentialLen = 16_384
	maxOpenConns     = 8
	maxIdleConns     = 4
	busyTimeoutMS    = 5000
	slowListAfter    = time.Second
)

type Store struct {
	channelBindingsMu chan struct{}
	db                *sql.DB
	now               func() time.Time
}

// Open creates or opens a file-backed database, applies restrictive defaults,
// enables SQLite integrity pragmas, and runs all forward migrations before it
// returns.
func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("database path is required")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if err := prepareDatabaseFiles(absolutePath); err != nil {
		return nil, err
	}
	database, err := sql.Open(driverName, sqliteFileDSN(absolutePath))
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	store, err := initialize(ctx, database)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	if err := restrictDatabaseFiles(absolutePath); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

func initialize(ctx context.Context, database *sql.DB) (*Store, error) {
	if database == nil {
		return nil, fmt.Errorf("database is required")
	}
	// WAL in the DSN applies to every pooled connection. A small pool lets
	// request-record lists share the file with ingress upserts; SQLite still
	// serializes writers and busy_timeout covers the wait.
	database.SetMaxOpenConns(maxOpenConns)
	database.SetMaxIdleConns(maxIdleConns)
	if err := requireWALMode(ctx, database); err != nil {
		return nil, err
	}
	runner, err := migrate.New(migrate.SQLDatabase{DB: database}, migrate.DefaultMigrations())
	if err != nil {
		return nil, fmt.Errorf("create migration runner: %w", err)
	}
	if err := runner.Up(ctx); err != nil {
		return nil, fmt.Errorf("migrate sqlite database: %w", err)
	}
	store := &Store{db: database, now: time.Now, channelBindingsMu: make(chan struct{}, 1)}
	manager, err := accesstoken.NewManager(store)
	if err != nil {
		return nil, fmt.Errorf("create access token manager: %w", err)
	}
	if _, _, err := manager.EnsureDefault(ctx); err != nil {
		return nil, fmt.Errorf("bootstrap default access token: %w", err)
	}
	return store, nil
}

func sqliteFileDSN(absolutePath string) string {
	return fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(%d)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)",
		filepath.ToSlash(absolutePath),
		busyTimeoutMS,
	)
}

func requireWALMode(ctx context.Context, database *sql.DB) error {
	var mode string
	if err := database.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		return fmt.Errorf("read sqlite journal_mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("sqlite journal_mode is %q, want wal", mode)
	}
	return nil
}

func (store *Store) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	return store.db.Close()
}

func (store *Store) EnsureDefaultAccessToken(ctx context.Context, candidate storagecontract.NewAccessToken) (record storagecontract.AccessTokenMetadata, created bool, err error) {
	if err := validateNewAccessToken(candidate); err != nil {
		return record, false, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return record, false, fmt.Errorf("begin default access token bootstrap: %w", err)
	}
	defer rollbackOnError(transaction, &err)

	var completed int
	if err = transaction.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM local_access_token_bootstrap_state WHERE singleton = 1)`,
	).Scan(&completed); err != nil {
		return record, false, fmt.Errorf("read access token bootstrap state: %w", err)
	}
	if completed == 1 {
		if err = transaction.Commit(); err != nil {
			return record, false, fmt.Errorf("commit access token bootstrap read: %w", err)
		}
		return storagecontract.AccessTokenMetadata{}, false, nil
	}

	now := store.now().UTC()
	if record, err = insertAccessTokenTx(ctx, transaction, candidate, now); err != nil {
		return storagecontract.AccessTokenMetadata{}, false, err
	}
	if _, err = transaction.ExecContext(
		ctx,
		`INSERT INTO local_access_token_bootstrap_state (singleton, completed_at) VALUES (1, ?)`,
		now.Format(time.RFC3339Nano),
	); err != nil {
		return storagecontract.AccessTokenMetadata{}, false, fmt.Errorf("record access token bootstrap state: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return storagecontract.AccessTokenMetadata{}, false, fmt.Errorf("commit default access token bootstrap: %w", err)
	}
	return record, true, nil
}

func (store *Store) CreateAccessToken(ctx context.Context, candidate storagecontract.NewAccessToken) (record storagecontract.AccessTokenMetadata, err error) {
	if err := validateNewAccessToken(candidate); err != nil {
		return record, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return record, fmt.Errorf("begin access token create: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	record, err = insertAccessTokenTx(ctx, transaction, candidate, store.now().UTC())
	if err != nil {
		return storagecontract.AccessTokenMetadata{}, err
	}
	if err = transaction.Commit(); err != nil {
		return storagecontract.AccessTokenMetadata{}, fmt.Errorf("commit access token create: %w", err)
	}
	return record, nil
}

func (store *Store) ListAccessTokens(ctx context.Context) ([]storagecontract.AccessTokenMetadata, error) {
	// This projection is intentionally metadata-only. In particular, neither
	// token_hash nor local_access_token_secrets participates in this query.
	rows, err := store.db.QueryContext(ctx, `SELECT id, name, name_key, token_hint, source, created_at
FROM local_access_tokens
ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list access tokens: %w", err)
	}
	defer rows.Close()

	records := make([]storagecontract.AccessTokenMetadata, 0)
	for rows.Next() {
		record, err := scanAccessTokenMetadata(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate access tokens: %w", err)
	}
	return records, nil
}

func (store *Store) RevealAccessToken(ctx context.Context, id contract.AccessTokenID) (string, error) {
	if err := id.Validate(); err != nil {
		return "", fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	row, err := readAccessTokenMaterial(store.db.QueryRowContext(ctx, `SELECT
    t.id, t.name, t.name_key, t.token_hash, t.token_hint, t.source, t.created_at, s.token_value
FROM local_access_tokens AS t
LEFT JOIN local_access_token_secrets AS s ON s.token_id = t.id
WHERE t.id = ?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: access token %q", storagecontract.ErrNotFound, id)
		}
		return "", err
	}
	return row.value, nil
}

func (store *Store) DeleteAccessToken(ctx context.Context, id contract.AccessTokenID) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	result, err := store.db.ExecContext(ctx, `DELETE FROM local_access_tokens WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete access token: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read access token delete result: %w", err)
	}
	if deleted == 0 {
		return fmt.Errorf("%w: access token %q", storagecontract.ErrNotFound, id)
	}
	return nil
}

func (store *Store) FindAccessTokenByHash(ctx context.Context, hash storagecontract.AccessTokenHash) (storagecontract.AccessTokenMetadata, error) {
	row, err := readAccessTokenMaterial(store.db.QueryRowContext(ctx, `SELECT
    t.id, t.name, t.name_key, t.token_hash, t.token_hint, t.source, t.created_at, s.token_value
FROM local_access_tokens AS t
LEFT JOIN local_access_token_secrets AS s ON s.token_id = t.id
WHERE t.token_hash = ?`, hash[:]))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storagecontract.AccessTokenMetadata{}, fmt.Errorf("%w: access token hash", storagecontract.ErrNotFound)
		}
		return storagecontract.AccessTokenMetadata{}, err
	}
	if subtle.ConstantTimeCompare(row.hash[:], hash[:]) != 1 {
		return storagecontract.AccessTokenMetadata{}, fmt.Errorf("%w: access token hash lookup mismatch", storagecontract.ErrInvalidRecord)
	}
	return row.metadata, nil
}

func prepareDatabaseFiles(path string) error {
	directory := filepath.Dir(path)
	if info, err := os.Stat(directory); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create sqlite data directory: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect sqlite data directory: %w", err)
	} else if !info.IsDir() {
		return fmt.Errorf("sqlite parent path is not a directory")
	}
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		file, err := os.OpenFile(candidate, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return fmt.Errorf("prepare sqlite file %s: %w", filepath.Base(candidate), err)
		}
		if closeErr := file.Close(); closeErr != nil {
			return fmt.Errorf("close sqlite file %s: %w", filepath.Base(candidate), closeErr)
		}
	}
	return restrictDatabaseFiles(path)
}

func restrictDatabaseFiles(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(candidate, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("restrict sqlite file %s: %w", filepath.Base(candidate), err)
		}
	}
	return nil
}

func (store *Store) CreateEndpoint(ctx context.Context, endpoint contract.Endpoint, credential storagecontract.CredentialMutation) (record storagecontract.EndpointRecord, err error) {
	created, err := store.CreateService(ctx, contract.ServiceFromEndpoint(endpoint), credential)
	if err != nil {
		return record, err
	}
	view, err := created.Service.EndpointView()
	if err != nil {
		return record, fmt.Errorf("%w: %v", storagecontract.ErrInvalidRecord, err)
	}
	return storagecontract.EndpointRecord{Endpoint: view, ETag: created.ETag}, nil
}

func (store *Store) GetEndpoint(ctx context.Context, id contract.ServiceID) (storagecontract.EndpointRecord, error) {
	service, err := store.GetService(ctx, id)
	if err != nil {
		return storagecontract.EndpointRecord{}, err
	}
	view, err := service.Service.EndpointView()
	if err != nil {
		return storagecontract.EndpointRecord{}, fmt.Errorf("%w: %v", storagecontract.ErrNotFound, err)
	}
	return storagecontract.EndpointRecord{Endpoint: view, ETag: service.ETag}, nil
}

func (store *Store) ListEndpoints(ctx context.Context, options storagecontract.EndpointListOptions) (storagecontract.EndpointPage, error) {
	serviceOptions := storagecontract.ServiceListOptions{
		Limit: options.Limit, Cursor: options.Cursor, Enabled: options.Enabled,
	}
	if options.Kind != nil {
		kind := contract.ServiceKind(*options.Kind)
		serviceOptions.Kind = &kind
	}
	services, err := store.ListServices(ctx, serviceOptions)
	if err != nil {
		return storagecontract.EndpointPage{}, err
	}
	items := make([]storagecontract.EndpointRecord, 0, len(services.Items))
	for _, service := range services.Items {
		if !service.Service.Kind.IsHTTP() {
			continue
		}
		view, err := service.Service.EndpointView()
		if err != nil {
			return storagecontract.EndpointPage{}, fmt.Errorf("%w: %v", storagecontract.ErrInvalidRecord, err)
		}
		items = append(items, storagecontract.EndpointRecord{Endpoint: view, ETag: service.ETag})
	}
	return storagecontract.EndpointPage{Items: items, NextCursor: services.NextCursor}, nil
}

func (store *Store) UpdateEndpoint(ctx context.Context, endpoint contract.Endpoint, credential storagecontract.CredentialMutation, expectedETag string) (record storagecontract.EndpointRecord, err error) {
	current, err := store.GetService(ctx, endpoint.ID)
	if err != nil {
		return record, err
	}
	service := contract.ServiceFromEndpoint(endpoint)
	service.Proxy = current.Service.Proxy
	service.ResponsesWebSocketEnabled = current.Service.ResponsesWebSocketEnabled
	service.FailurePolicy = current.Service.FailurePolicy
	service.CreatedAt = current.Service.CreatedAt
	updated, err := store.UpdateService(ctx, service, credential, expectedETag)
	if err != nil {
		return record, err
	}
	view, err := updated.Service.EndpointView()
	if err != nil {
		return record, fmt.Errorf("%w: %v", storagecontract.ErrInvalidRecord, err)
	}
	return storagecontract.EndpointRecord{Endpoint: view, ETag: updated.ETag}, nil
}

func (store *Store) DeleteEndpoint(ctx context.Context, id contract.ServiceID, expectedETag string) (err error) {
	return store.DeleteService(ctx, id, expectedETag)
}

func (store *Store) Get(ctx context.Context, ref secretstore.Ref) ([]byte, error) {
	if strings.HasPrefix(string(ref), "local://builtin-tool/") {
		return store.builtinCredential(ctx, ref, "get", nil)
	}
	if strings.HasPrefix(string(ref), "local://service-proxy/") {
		return store.getProxyCredential(ctx, ref)
	}
	id, err := localServiceID(ref)
	if err != nil {
		return nil, err
	}
	var secret []byte
	if err := store.db.QueryRowContext(ctx, `SELECT credential_value FROM service_credentials WHERE service_id = ?`, id).Scan(&secret); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", secretstore.ErrNotFound, ref)
		}
		return nil, fmt.Errorf("read endpoint credential: %w", err)
	}
	if err := validateCredential(secret); err != nil {
		clear(secret)
		return nil, fmt.Errorf("%w: %v", storagecontract.ErrInvalidRecord, err)
	}
	result := append([]byte(nil), secret...)
	clear(secret)
	return result, nil
}

func (store *Store) Put(ctx context.Context, ref secretstore.Ref, secret []byte) (err error) {
	if strings.HasPrefix(string(ref), "local://builtin-tool/") {
		_, err := store.builtinCredential(ctx, ref, "put", secret)
		return err
	}
	id, err := localServiceID(ref)
	if err != nil {
		return err
	}
	if err := validateCredential(secret); err != nil {
		return err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin credential write: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	now := store.now().UTC().Format(time.RFC3339Nano)
	if err = putServiceCredentialTx(ctx, transaction, id, secret, now); err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit credential write: %w", err)
	}
	return nil
}

func (store *Store) Delete(ctx context.Context, ref secretstore.Ref) error {
	if strings.HasPrefix(string(ref), "local://builtin-tool/") {
		_, err := store.builtinCredential(ctx, ref, "delete", nil)
		return err
	}
	id, err := localServiceID(ref)
	if err != nil {
		return err
	}
	result, err := store.db.ExecContext(ctx, `DELETE FROM service_credentials WHERE service_id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete endpoint credential: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read credential delete result: %w", err)
	}
	if deleted == 0 {
		return fmt.Errorf("%w: %s", secretstore.ErrNotFound, ref)
	}
	return nil
}

func applyCredentialMutation(endpoint contract.Endpoint, credential storagecontract.CredentialMutation) (contract.Endpoint, error) {
	if !credential.Present {
		return endpoint, nil
	}
	if len(credential.Secret) == 0 {
		endpoint.CredentialRef = ""
		return endpoint, nil
	}
	if endpoint.Auth.Scheme == contract.AuthSchemeNone {
		return endpoint, fmt.Errorf("credential is not valid when auth scheme is none")
	}
	if err := validateCredential(credential.Secret); err != nil {
		return endpoint, err
	}
	endpoint.CredentialRef = localRef(endpoint.ID)
	return endpoint, nil
}

func validateStoredEndpoint(endpoint contract.Endpoint) error {
	if err := endpoint.Validate(); err != nil {
		return err
	}
	if strings.HasPrefix(endpoint.CredentialRef, "local://") && endpoint.CredentialRef != localRef(endpoint.ID) {
		return fmt.Errorf("endpoint local credential_ref must match endpoint id")
	}
	return nil
}

func validateCredential(secret []byte) error {
	if len(secret) == 0 || len(secret) > maxCredentialLen {
		return fmt.Errorf("credential must contain 1 to %d bytes", maxCredentialLen)
	}
	for _, value := range secret {
		if value < 0x20 || value == 0x7f {
			return fmt.Errorf("credential contains an HTTP control character")
		}
	}
	if strings.TrimSpace(string(secret)) == "" {
		return fmt.Errorf("credential must not be blank")
	}
	return nil
}

func localRef(id contract.ServiceID) string {
	return localServiceRef(id)
}

func localServiceID(ref secretstore.Ref) (contract.ServiceID, error) {
	if err := contract.ValidateCredentialRef(string(ref)); err != nil {
		return "", err
	}
	const prefix = "local://service/"
	if !strings.HasPrefix(string(ref), prefix) {
		return "", fmt.Errorf("%w: %s", storagecontract.ErrUnsupportedRef, ref)
	}
	id := contract.ServiceID(strings.TrimPrefix(string(ref), prefix))
	if err := id.Validate(); err != nil {
		return "", err
	}
	return id, nil
}

func putCredentialTx(ctx context.Context, transaction *sql.Tx, id contract.ServiceID, secret []byte, now string) error {
	return putServiceCredentialTx(ctx, transaction, id, secret, now)
}

func insertAccessTokenTx(ctx context.Context, transaction *sql.Tx, candidate storagecontract.NewAccessToken, now time.Time) (storagecontract.AccessTokenMetadata, error) {
	var count int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM local_access_tokens`).Scan(&count); err != nil {
		return storagecontract.AccessTokenMetadata{}, fmt.Errorf("count access tokens: %w", err)
	}
	if count >= storagecontract.AccessTokenLimit {
		return storagecontract.AccessTokenMetadata{}, fmt.Errorf("%w: at most %d access tokens are allowed", storagecontract.ErrLimitReached, storagecontract.AccessTokenLimit)
	}

	createdAt := now.UTC()
	createdAtText := createdAt.Format(time.RFC3339Nano)
	if _, err := transaction.ExecContext(ctx, `INSERT INTO local_access_tokens
    (id, name, name_key, token_hash, token_hint, source, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		candidate.ID,
		candidate.Name,
		candidate.NameKey,
		candidate.Hash[:],
		candidate.Hint,
		storagecontract.AccessTokenSourceUser,
		createdAtText,
	); err != nil {
		var exists int
		scanErr := transaction.QueryRowContext(ctx, `SELECT EXISTS(
    SELECT 1 FROM local_access_tokens WHERE id = ? OR name_key = ? OR token_hash = ?
)`, candidate.ID, candidate.NameKey, candidate.Hash[:]).Scan(&exists)
		if scanErr == nil && exists == 1 {
			return storagecontract.AccessTokenMetadata{}, fmt.Errorf("%w: access token id, name, or hash", storagecontract.ErrConflict)
		}
		return storagecontract.AccessTokenMetadata{}, fmt.Errorf("insert access token metadata: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO local_access_token_secrets (token_id, token_value)
VALUES (?, ?)`, candidate.ID, candidate.Value); err != nil {
		return storagecontract.AccessTokenMetadata{}, fmt.Errorf("insert access token secret: %w", err)
	}
	return storagecontract.AccessTokenMetadata{
		ID:        candidate.ID,
		Name:      candidate.Name,
		Hint:      candidate.Hint,
		CreatedAt: createdAt,
	}, nil
}

type accessTokenScanner interface {
	Scan(...any) error
}

type storedAccessTokenMaterial struct {
	metadata storagecontract.AccessTokenMetadata
	hash     storagecontract.AccessTokenHash
	value    string
}

func scanAccessTokenMetadata(scanner accessTokenScanner) (storagecontract.AccessTokenMetadata, error) {
	var id, name, nameKey, hint, source, createdAt string
	if err := scanner.Scan(&id, &name, &nameKey, &hint, &source, &createdAt); err != nil {
		return storagecontract.AccessTokenMetadata{}, fmt.Errorf("scan access token metadata: %w", err)
	}
	return decodeAccessTokenMetadata(id, name, nameKey, hint, source, createdAt)
}

func readAccessTokenMaterial(scanner accessTokenScanner) (storedAccessTokenMaterial, error) {
	var id, name, nameKey, hint, source, createdAt string
	var hashBytes []byte
	var value sql.NullString
	if err := scanner.Scan(&id, &name, &nameKey, &hashBytes, &hint, &source, &createdAt, &value); err != nil {
		return storedAccessTokenMaterial{}, err
	}
	metadata, err := decodeAccessTokenMetadata(id, name, nameKey, hint, source, createdAt)
	if err != nil {
		return storedAccessTokenMaterial{}, err
	}
	if len(hashBytes) != sha256.Size {
		return storedAccessTokenMaterial{}, fmt.Errorf("%w: access token hash length", storagecontract.ErrInvalidRecord)
	}
	var hash storagecontract.AccessTokenHash
	copy(hash[:], hashBytes)
	clear(hashBytes)
	if !value.Valid {
		return storedAccessTokenMaterial{}, fmt.Errorf("%w: access token secret is missing", storagecontract.ErrInvalidRecord)
	}
	if !validAccessTokenValue(value.String) {
		return storedAccessTokenMaterial{}, fmt.Errorf("%w: access token secret format", storagecontract.ErrInvalidRecord)
	}
	actualHash := sha256.Sum256([]byte(value.String))
	if subtle.ConstantTimeCompare(hash[:], actualHash[:]) != 1 {
		return storedAccessTokenMaterial{}, fmt.Errorf("%w: access token secret does not match hash", storagecontract.ErrInvalidRecord)
	}
	if metadata.Hint != accessTokenHint(value.String) {
		return storedAccessTokenMaterial{}, fmt.Errorf("%w: access token hint does not match secret", storagecontract.ErrInvalidRecord)
	}
	return storedAccessTokenMaterial{metadata: metadata, hash: hash, value: value.String}, nil
}

func decodeAccessTokenMetadata(id, name, nameKey, hint, source, createdAt string) (storagecontract.AccessTokenMetadata, error) {
	tokenID := contract.AccessTokenID(id)
	if err := tokenID.Validate(); err != nil {
		return storagecontract.AccessTokenMetadata{}, fmt.Errorf("%w: %v", storagecontract.ErrInvalidRecord, err)
	}
	canonicalName, canonicalKey, err := normalizeAccessTokenName(name)
	if err != nil || canonicalName != name || canonicalKey != nameKey {
		return storagecontract.AccessTokenMetadata{}, fmt.Errorf("%w: access token name", storagecontract.ErrInvalidRecord)
	}
	if !validAccessTokenHint(hint) {
		return storagecontract.AccessTokenMetadata{}, fmt.Errorf("%w: access token hint", storagecontract.ErrInvalidRecord)
	}
	tokenSource := storagecontract.AccessTokenSource(source)
	if !tokenSource.Valid() {
		return storagecontract.AccessTokenMetadata{}, fmt.Errorf("%w: access token source", storagecontract.ErrInvalidRecord)
	}
	parsedCreatedAt, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil || parsedCreatedAt.IsZero() {
		return storagecontract.AccessTokenMetadata{}, fmt.Errorf("%w: access token created_at", storagecontract.ErrInvalidRecord)
	}
	return storagecontract.AccessTokenMetadata{
		ID:        tokenID,
		Name:      canonicalName,
		Hint:      hint,
		CreatedAt: parsedCreatedAt,
	}, nil
}

func validateNewAccessToken(candidate storagecontract.NewAccessToken) error {
	if err := candidate.ID.Validate(); err != nil {
		return err
	}
	name, key, err := normalizeAccessTokenName(candidate.Name)
	if err != nil || name != candidate.Name || key != candidate.NameKey {
		return fmt.Errorf("access token name is not canonical")
	}
	if !validAccessTokenValue(candidate.Value) {
		return fmt.Errorf("access token value is invalid")
	}
	actualHash := sha256.Sum256([]byte(candidate.Value))
	if subtle.ConstantTimeCompare(candidate.Hash[:], actualHash[:]) != 1 {
		return fmt.Errorf("access token hash does not match value")
	}
	if candidate.Hint != accessTokenHint(candidate.Value) {
		return fmt.Errorf("access token hint does not match value")
	}
	return nil
}

func normalizeAccessTokenName(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > 64 {
		return "", "", fmt.Errorf("access token name must contain 1 to 64 characters")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", "", fmt.Errorf("access token name contains a control character")
		}
	}
	return value, simpleFoldAccessTokenName(value), nil
}

func simpleFoldAccessTokenName(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, character := range value {
		smallest := character
		for folded := unicode.SimpleFold(character); folded != character; folded = unicode.SimpleFold(folded) {
			if folded < smallest {
				smallest = folded
			}
		}
		builder.WriteRune(smallest)
	}
	return builder.String()
}

func validAccessTokenValue(value string) bool {
	const prefix = "astr_"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	encoded := strings.TrimPrefix(value, prefix)
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != 32 {
		return false
	}
	return base64.RawURLEncoding.EncodeToString(decoded) == encoded
}

func accessTokenHint(value string) string {
	const visibleSuffix = 6
	return "astr_…" + value[len(value)-visibleSuffix:]
}

func validAccessTokenHint(value string) bool {
	const prefix = "astr_…"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+6 {
		return false
	}
	for _, character := range strings.TrimPrefix(value, prefix) {
		if !((character >= 'A' && character <= 'Z') ||
			(character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '-' ||
			character == '_') {
			return false
		}
	}
	return true
}

func decodeEndpointRecord(rowID string, document []byte) (storagecontract.EndpointRecord, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var endpoint contract.Endpoint
	if err := decoder.Decode(&endpoint); err != nil {
		return storagecontract.EndpointRecord{}, fmt.Errorf("%w: decode endpoint %q: %v", storagecontract.ErrInvalidRecord, rowID, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return storagecontract.EndpointRecord{}, fmt.Errorf("%w: decode endpoint %q: %v", storagecontract.ErrInvalidRecord, rowID, err)
	}
	if string(endpoint.ID) != rowID {
		return storagecontract.EndpointRecord{}, fmt.Errorf("%w: endpoint row id does not match document id", storagecontract.ErrInvalidRecord)
	}
	if err := validateStoredEndpoint(endpoint); err != nil {
		return storagecontract.EndpointRecord{}, fmt.Errorf("%w: endpoint %q: %v", storagecontract.ErrInvalidRecord, rowID, err)
	}
	return storagecontract.EndpointRecord{Endpoint: endpoint, ETag: entityTag(document)}, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func entityTag(document []byte) string {
	sum := sha256.Sum256(document)
	return fmt.Sprintf(`"sha256:%x"`, sum)
}

func encodeCursor(id contract.ServiceID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

func decodeCursor(cursor string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != cursor {
		return "", fmt.Errorf("%w: endpoint cursor encoding", storagecontract.ErrInvalidCursor)
	}
	id := contract.ServiceID(decoded)
	if err := id.Validate(); err != nil {
		return "", fmt.Errorf("%w: endpoint cursor id", storagecontract.ErrInvalidCursor)
	}
	return string(id), nil
}

func rollbackOnError(transaction *sql.Tx, err *error) {
	if *err != nil {
		_ = transaction.Rollback()
	}
}

var _ storagecontract.EndpointStore = (*Store)(nil)
var _ storagecontract.AccessTokenStore = (*Store)(nil)
var _ secretstore.SecretStore = (*Store)(nil)
