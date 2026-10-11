package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

// Secret columns are sealed under dek_secrets (plan §5.4). The AAD binds the
// table and the primary key, so a value copied onto another row does not
// open. Rows copied by migration 45 from a test build keep sealed = 0 until
// SealPlaintextSecrets seals them at the next start; every write sets
// sealed = 1, so reads refuse a sealed = 0 row.
const (
	serviceCredentialsTable      = "service_credentials"
	accessTokenSecretsTable      = "local_access_token_secrets"
	builtinToolCredentialsTable  = "builtin_tool_credentials"
	serviceProxyCredentialsTable = "service_proxy_credentials"
	subscriptionCredentialsTable = "subscription_credentials"
	syncKeyMaterialTable         = "sync_key_material"
	syncBaselineTable            = "sync_baseline"

	subscriptionRefPrefix = "local://subscription/"
	// maxSubscriptionCredentialLen bounds one account's OAuth token JSON.
	maxSubscriptionCredentialLen = 65_536
)

type secretColumn struct {
	table, key, value string
	// flagged tables carry a sealed column; subscription_credentials is
	// sealed from its first row.
	flagged bool
}

var secretColumns = []secretColumn{
	{table: serviceCredentialsTable, key: "service_id", value: "credential_value", flagged: true},
	{table: accessTokenSecretsTable, key: "token_id", value: "token_value", flagged: true},
	{table: builtinToolCredentialsTable, key: "kind", value: "credential_value", flagged: true},
	{table: serviceProxyCredentialsTable, key: "service_id", value: "credential_value", flagged: true},
	{table: subscriptionCredentialsTable, key: "service_id", value: "credential_value"},
	{table: syncKeyMaterialTable, key: "key", value: "credential_value", flagged: true},
	{table: syncBaselineTable, key: "key", value: "payload_blob", flagged: true},
}

// openSecret returns the plaintext of one stored value. It takes ownership of
// stored and clears it; a row not marked sealed is refused.
func (store *Store) openSecret(table, key string, stored []byte, sealed bool) ([]byte, error) {
	defer clear(stored)
	if !sealed {
		return nil, fmt.Errorf("%w: %s %s is not sealed", storagecontract.ErrInvalidRecord, table, key)
	}
	return store.keys.openColumn(table, key, stored)
}

// SealPlaintextSecrets seals every secret row still stored as plaintext, in
// one transaction, and returns how many it sealed. It is idempotent. Open
// calls it once the data keys are ready and then scrubs the file, so the
// plaintext left in free pages and the WAL goes too.
func (store *Store) SealPlaintextSecrets(ctx context.Context) (sealed int, err error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin secret sealing: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	for _, column := range secretColumns {
		if !column.flagged {
			continue
		}
		count, sealErr := store.sealPlaintextColumnTx(ctx, transaction, column)
		if sealErr != nil {
			err = sealErr
			return 0, err
		}
		sealed += count
	}
	if err = transaction.Commit(); err != nil {
		return 0, fmt.Errorf("commit secret sealing: %w", err)
	}
	return sealed, nil
}

func (store *Store) sealPlaintextColumnTx(ctx context.Context, transaction *sql.Tx, column secretColumn) (int, error) {
	type plaintextRow struct {
		key   string
		value []byte
	}
	var pending []plaintextRow
	defer func() {
		for _, row := range pending {
			clear(row.value)
		}
	}()
	rows, err := transaction.QueryContext(ctx, fmt.Sprintf(`SELECT %s, %s FROM %s WHERE sealed = 0`,
		column.key, column.value, column.table))
	if err != nil {
		return 0, fmt.Errorf("read plaintext %s: %w", column.table, err)
	}
	for rows.Next() {
		var row plaintextRow
		if err := rows.Scan(&row.key, &row.value); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("read plaintext %s: %w", column.table, err)
		}
		pending = append(pending, row)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("read plaintext %s: %w", column.table, err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read plaintext %s: %w", column.table, err)
	}
	for _, row := range pending {
		sealedValue, err := store.keys.sealColumn(column.table, row.key, row.value)
		if err != nil {
			return 0, fmt.Errorf("seal %s: %w", column.table, err)
		}
		if _, err := transaction.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET %s = ?, sealed = 1 WHERE %s = ? AND sealed = 0`,
			column.table, column.value, column.key), sealedValue, row.key); err != nil {
			return 0, fmt.Errorf("seal %s: %w", column.table, err)
		}
	}
	return len(pending), nil
}

// hasPlaintextSecrets reports whether a row still waits for
// SealPlaintextSecrets, which only a Core start may run.
func (store *Store) hasPlaintextSecrets(ctx context.Context) (bool, error) {
	for _, column := range secretColumns {
		if !column.flagged {
			continue
		}
		var pending bool
		if err := store.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE sealed = 0)`, column.table)).Scan(&pending); err != nil {
			return false, fmt.Errorf("read plaintext %s: %w", column.table, err)
		}
		if pending {
			return true, nil
		}
	}
	return false, nil
}

// LocalDataStatus counts saved secrets this device cannot decrypt (§5.7).
// Each value is opened once, so it also catches a damaged row.
func (store *Store) LocalDataStatus(ctx context.Context) (contract.LocalDataStatus, error) {
	status := contract.LocalDataStatus{AuditKeyMissing: store.HasOrphanedAuditKey()}
	for _, column := range secretColumns {
		unreadable, err := store.countUnreadable(ctx, column)
		if err != nil {
			return contract.LocalDataStatus{}, err
		}
		if column.table == accessTokenSecretsTable {
			status.UnreadableAccessTokens += unreadable
		} else {
			status.UnreadableCredentials += unreadable
		}
	}
	return status, nil
}

func (store *Store) countUnreadable(ctx context.Context, column secretColumn) (int, error) {
	rows, err := store.db.QueryContext(ctx, fmt.Sprintf(`SELECT %s, %s FROM %s`, column.key, column.value, column.table))
	if err != nil {
		return 0, fmt.Errorf("read sealed %s: %w", column.table, err)
	}
	defer rows.Close()
	unreadable := 0
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return 0, fmt.Errorf("read sealed %s: %w", column.table, err)
		}
		plaintext, err := store.openSecret(column.table, key, value, true)
		clear(plaintext)
		if err != nil {
			unreadable++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read sealed %s: %w", column.table, err)
	}
	return unreadable, nil
}

// putServiceCredentialTx seals and writes one service API key.
func (store *Store) putServiceCredentialTx(ctx context.Context, transaction *sql.Tx, id contract.ServiceID, secret []byte, now string) error {
	if err := validateCredential(secret); err != nil {
		return err
	}
	sealed, err := store.keys.sealColumn(serviceCredentialsTable, string(id), secret)
	if err != nil {
		return fmt.Errorf("seal service credential: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO service_credentials (service_id, credential_value, created_at, updated_at, sealed)
VALUES (?, ?, ?, ?, 1)
ON CONFLICT(service_id) DO UPDATE SET credential_value = excluded.credential_value, updated_at = excluded.updated_at, sealed = 1`,
		id, sealed, now, now); err != nil {
		return fmt.Errorf("write service credential: %w", err)
	}
	return nil
}

func (store *Store) getServiceCredential(ctx context.Context, ref secretstore.Ref, id contract.ServiceID) ([]byte, error) {
	var stored []byte
	var sealed bool
	if err := store.db.QueryRowContext(ctx, `SELECT credential_value, sealed FROM service_credentials WHERE service_id = ?`, id).Scan(&stored, &sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", secretstore.ErrNotFound, ref)
		}
		return nil, fmt.Errorf("read endpoint credential: %w", err)
	}
	secret, err := store.openSecret(serviceCredentialsTable, string(id), stored, sealed)
	if err != nil {
		return nil, err
	}
	if err := validateCredential(secret); err != nil {
		clear(secret)
		return nil, fmt.Errorf("%w: %v", storagecontract.ErrInvalidRecord, err)
	}
	return secret, nil
}

// subscriptionCredential serves local://subscription/<service id>: one
// account's OAuth token JSON, always sealed (§5.9).
func (store *Store) subscriptionCredential(ctx context.Context, ref secretstore.Ref, operation string, secret []byte) ([]byte, error) {
	if err := contract.ValidateCredentialRef(string(ref)); err != nil {
		return nil, err
	}
	id := contract.ServiceID(strings.TrimPrefix(string(ref), subscriptionRefPrefix))
	if err := id.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %s", storagecontract.ErrUnsupportedRef, ref)
	}
	switch operation {
	case "put":
		if len(secret) == 0 || len(secret) > maxSubscriptionCredentialLen {
			return nil, fmt.Errorf("%w: subscription credential must contain 1 to %d bytes", storagecontract.ErrInvalidArgument, maxSubscriptionCredentialLen)
		}
		sealed, err := store.keys.sealColumn(subscriptionCredentialsTable, string(id), secret)
		if err != nil {
			return nil, fmt.Errorf("seal subscription credential: %w", err)
		}
		if _, err := store.db.ExecContext(ctx, `INSERT INTO subscription_credentials (service_id, credential_value, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(service_id) DO UPDATE SET credential_value = excluded.credential_value, updated_at = excluded.updated_at`,
			id, sealed, store.now().UTC().Format(time.RFC3339Nano)); err != nil {
			return nil, fmt.Errorf("write subscription credential: %w", err)
		}
		return nil, nil
	case "delete":
		result, err := store.db.ExecContext(ctx, `DELETE FROM subscription_credentials WHERE service_id = ?`, id)
		if err != nil {
			return nil, fmt.Errorf("delete subscription credential: %w", err)
		}
		if deleted, err := result.RowsAffected(); err != nil {
			return nil, fmt.Errorf("read subscription credential delete result: %w", err)
		} else if deleted == 0 {
			return nil, fmt.Errorf("%w: %s", secretstore.ErrNotFound, ref)
		}
		return nil, nil
	default:
		var stored []byte
		if err := store.db.QueryRowContext(ctx, `SELECT credential_value FROM subscription_credentials WHERE service_id = ?`, id).Scan(&stored); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("%w: %s", secretstore.ErrNotFound, ref)
			}
			return nil, fmt.Errorf("read subscription credential: %w", err)
		}
		return store.openSecret(subscriptionCredentialsTable, string(id), stored, true)
	}
}
