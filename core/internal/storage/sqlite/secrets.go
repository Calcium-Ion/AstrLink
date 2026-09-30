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
// open. Every write sets sealed = 1, so a sealed = 0 row is invalid.
const (
	serviceCredentialsTable      = "service_credentials"
	accessTokenSecretsTable      = "local_access_token_secrets"
	builtinToolCredentialsTable  = "builtin_tool_credentials"
	serviceProxyCredentialsTable = "service_proxy_credentials"
	subscriptionCredentialsTable = "subscription_credentials"

	subscriptionRefPrefix = "local://subscription/"
	// maxSubscriptionCredentialLen bounds one account's OAuth token JSON.
	maxSubscriptionCredentialLen = 65_536
)

type secretColumn struct {
	table, key, value string
	// sealedFlag marks the four tables whose rows carry an explicit sealed
	// column. The sealed_secrets migration (v45) copied their pre-encryption
	// rows with sealed = 0, so startup seals those rows again; the fifth
	// table, subscription_credentials, has no plaintext form to repair.
	sealedFlag bool
}

var secretColumns = []secretColumn{
	{table: serviceCredentialsTable, key: "service_id", value: "credential_value", sealedFlag: true},
	{table: accessTokenSecretsTable, key: "token_id", value: "token_value", sealedFlag: true},
	{table: builtinToolCredentialsTable, key: "kind", value: "credential_value", sealedFlag: true},
	{table: serviceProxyCredentialsTable, key: "service_id", value: "credential_value", sealedFlag: true},
	{table: subscriptionCredentialsTable, key: "service_id", value: "credential_value"},
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

// sealLegacyPlaintext seals rows the sealed_secrets migration (v45) copied
// with sealed = 0. Migrations run before the data keys exist, so values saved
// by earlier versions were left in the clear, and openSecret refuses them:
// without this pass they would never read again. It runs once per open, after
// ensureKeyRing, and rewrites each row under dek_secrets, so the
// every-write-seals invariant holds from then on. A row that fails to seal is
// logged and left as it was; the next open tries again.
func (store *Store) sealLegacyPlaintext(ctx context.Context, logf func(format string, args ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	type legacyRow struct {
		key   string
		value []byte
	}
	for _, column := range secretColumns {
		if !column.sealedFlag {
			continue
		}
		rows, err := store.db.QueryContext(ctx, fmt.Sprintf(`SELECT %s, %s FROM %s WHERE sealed = 0`, column.key, column.value, column.table))
		if err != nil {
			logf("astrlink storage: read legacy plaintext %s failed: %v", column.table, err)
			continue
		}
		var batch []legacyRow
		var readErr error
		for rows.Next() {
			var row legacyRow
			if err := rows.Scan(&row.key, &row.value); err != nil {
				readErr = err
				break
			}
			batch = append(batch, row)
		}
		if readErr == nil {
			readErr = rows.Err()
		}
		rows.Close()
		if readErr != nil {
			logf("astrlink storage: read legacy plaintext %s failed: %v", column.table, readErr)
			continue
		}
		var sealedCount int
		for _, row := range batch {
			sealed, err := store.keys.sealColumn(column.table, row.key, row.value)
			if err == nil {
				_, err = store.db.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET %s = ?, sealed = 1 WHERE %s = ? AND sealed = 0`, column.table, column.value, column.key), sealed, row.key)
			}
			if err != nil {
				logf("astrlink storage: seal legacy %s %s failed: %v", column.table, row.key, err)
				continue
			}
			clear(row.value)
			sealedCount++
		}
		if sealedCount > 0 {
			logf("astrlink storage: sealed %d legacy plaintext %s row(s) saved by an earlier version", sealedCount, column.table)
		}
	}
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
