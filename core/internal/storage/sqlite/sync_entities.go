package sqlite

import (
	"context"
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// SyncEntities returns every row of one kind this device holds. A credential
// kind returns the plaintext under Secret; every other kind returns the stored
// document. The engine seals the result into the package, so this is the only
// place a credential is read in the clear for sync.
//
// Each entity carries when this device last changed it. A credential table has
// no timestamp of its own, so its query takes the later of the owning row's
// time and the change log's, which is what keeps a credential changed with its
// provider from looking older than the provider.
func (store *Store) SyncEntities(ctx context.Context, kind string) ([]storage.SyncLocalEntity, error) {
	times, err := store.entityTimes(ctx, kind)
	if err != nil {
		return nil, err
	}
	switch kind {
	case syncKindService:
		return store.syncDocuments(ctx, kind, times, `SELECT id, document_json FROM services`)
	case syncKindPolicy:
		return store.syncDocuments(ctx, kind, times, `SELECT id, document_json FROM policies`)
	case syncKindPricingConfig:
		return store.syncDocuments(ctx, kind, times, `SELECT service_id, document_json FROM pricing_configs`)
	case syncKindServiceCredential:
		return store.syncSecrets(ctx, kind, times, `SELECT service_id, credential_value, sealed FROM service_credentials`, serviceCredentialsTable)
	case syncKindServiceProxyCredential:
		return store.syncSecrets(ctx, kind, times, `SELECT service_id, credential_value, sealed FROM service_proxy_credentials`, serviceProxyCredentialsTable)
	case syncKindBuiltinToolCredential:
		return store.syncSecrets(ctx, kind, times, `SELECT kind, credential_value, sealed FROM builtin_tool_credentials`, builtinToolCredentialsTable)
	default:
		return nil, fmt.Errorf("%w: %s is not an entity kind", storage.ErrInvalidArgument, kind)
	}
}

// entityTimes returns the last write time of every row of one kind, keyed by
// the row's local id.
func (store *Store) entityTimes(ctx context.Context, kind string) (map[string]string, error) {
	rows, err := store.db.QueryContext(ctx, syncEntityTimeQuery(kind))
	if err != nil {
		return nil, fmt.Errorf("read %s times: %w", kind, err)
	}
	defer rows.Close()
	times := map[string]string{}
	for rows.Next() {
		var id, stamp string
		if err := rows.Scan(&id, &stamp); err != nil {
			return nil, fmt.Errorf("read %s time: %w", kind, err)
		}
		times[id] = stamp
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read %s times: %w", kind, err)
	}
	return times, nil
}

func (store *Store) syncDocuments(ctx context.Context, kind string, times map[string]string, query string) ([]storage.SyncLocalEntity, error) {
	rows, err := store.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", kind, err)
	}
	defer rows.Close()
	var entities []storage.SyncLocalEntity
	for rows.Next() {
		var id, document string
		if err := rows.Scan(&id, &document); err != nil {
			return nil, fmt.Errorf("read %s: %w", kind, err)
		}
		entities = append(entities, storage.SyncLocalEntity{
			LocalID:   id,
			Document:  []byte(document),
			UpdatedAt: parseEntityTime(times[id]),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", kind, err)
	}
	return entities, nil
}

func (store *Store) syncSecrets(ctx context.Context, kind string, times map[string]string, query, table string) ([]storage.SyncLocalEntity, error) {
	rows, err := store.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", kind, err)
	}
	defer rows.Close()
	var entities []storage.SyncLocalEntity
	for rows.Next() {
		var id string
		var stored []byte
		var sealed bool
		if err := rows.Scan(&id, &stored, &sealed); err != nil {
			return nil, fmt.Errorf("read %s: %w", kind, err)
		}
		plaintext, err := store.openSecret(table, id, stored, sealed)
		if err != nil {
			// A value this device cannot open is skipped rather than failing
			// the sync: the row stays where it is, and the local-data status
			// already reports it as unreadable.
			continue
		}
		entities = append(entities, storage.SyncLocalEntity{
			LocalID:   id,
			Secret:    plaintext,
			UpdatedAt: parseEntityTime(times[id]),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", kind, err)
	}
	return entities, nil
}

// SyncServiceOrder returns the provider priority list, in order.
func (store *Store) SyncServiceOrder(ctx context.Context) ([]contract.ServiceID, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id FROM services ORDER BY sort_position, id`)
	if err != nil {
		return nil, fmt.Errorf("read service order: %w", err)
	}
	defer rows.Close()
	var ids []contract.ServiceID
	for rows.Next() {
		var id contract.ServiceID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("read service order: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read service order: %w", err)
	}
	return ids, nil
}

// syncEntityTimeQuery is the timestamp query for one entity kind. Every kind a
// package carries is listed; an unknown kind reads nothing rather than
// scanning an arbitrary table.
//
// Times are compared with julianday rather than as text: a Go time is written
// without its trailing zeros, so "12:00:00Z" sorts after "12:00:00.5Z" as text
// while being earlier in fact.
func syncEntityTimeQuery(kind string) string {
	later := func(left, right string) string {
		return "CASE WHEN julianday(" + left + ") >= julianday(" + right + ") THEN " + left + " ELSE " + right + " END"
	}
	lastChange := func(kind, id string) string {
		return "(SELECT MAX(changed_at) FROM config_change_log WHERE entity_kind = '" + kind + "' AND entity_id = " + id + ")"
	}
	switch kind {
	case syncKindService:
		return `SELECT id, updated_at FROM services`
	case syncKindServiceCredential:
		return `SELECT credential.service_id, ` + later("service.updated_at", "credential.updated_at") + `
FROM service_credentials credential JOIN services service ON service.id = credential.service_id`
	case syncKindServiceProxyCredential:
		change := lastChange(syncKindServiceProxyCredential, "credential.service_id")
		return `SELECT credential.service_id, ` + later("service.updated_at", change) + `
FROM service_proxy_credentials credential JOIN services service ON service.id = credential.service_id`
	case syncKindBuiltinToolCredential:
		// Every write of this table is logged by a trigger, and the log's own
		// stamp is a fixed-width format, so MAX is chronological here.
		return `SELECT entity_id, MAX(changed_at) FROM config_change_log
WHERE entity_kind = 'builtin_tool_credential' GROUP BY entity_id`
	case syncKindPolicy:
		return `SELECT id, updated_at FROM policies`
	case syncKindPricingConfig:
		change := lastChange(syncKindPricingConfig, "pricing.service_id")
		return `SELECT pricing.service_id, ` + later("service.updated_at", change) + `
FROM pricing_configs pricing JOIN services service ON service.id = pricing.service_id`
	default:
		return `SELECT '' AS id, '' AS stamp WHERE 0`
	}
}

// SyncImportCredential re-seals one credential a package carried under the row
// this device stores it on. The credential tables other than the builtin tools'
// cascade from services, so the owning service must already be written.
func (store *Store) SyncImportCredential(ctx context.Context, kind, localID string, plaintext []byte) error {
	table, err := syncCredentialTable(kind)
	if err != nil {
		return err
	}
	return store.ImportCredential(ctx, table, localID, plaintext)
}

// SyncDeleteCredential removes one credential a merge decided is gone.
func (store *Store) SyncDeleteCredential(ctx context.Context, kind, localID string) error {
	table, err := syncCredentialTable(kind)
	if err != nil {
		return err
	}
	statement := `DELETE FROM ` + table + ` WHERE ` + syncTableKeyColumn(table) + ` = ?`
	if _, err := store.db.ExecContext(ctx, statement, localID); err != nil {
		return fmt.Errorf("delete synced credential: %w", err)
	}
	return nil
}

// syncCredentialTable names the table one credential kind is stored in.
func syncCredentialTable(kind string) (string, error) {
	switch kind {
	case syncKindServiceCredential:
		return serviceCredentialsTable, nil
	case syncKindServiceProxyCredential:
		return serviceProxyCredentialsTable, nil
	case syncKindBuiltinToolCredential:
		return builtinToolCredentialsTable, nil
	default:
		return "", fmt.Errorf("%w: %s is not a credential kind", storage.ErrInvalidArgument, kind)
	}
}
