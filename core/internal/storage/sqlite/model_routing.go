package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

const modelRoutingBindingColumns = `principal, session_id, protocol, virtual_model_id, group_id, member_id, service_id, model, config_snapshot_version, request_id, created_at, updated_at, expires_at`

func scanModelRoutingBinding(row interface{ Scan(...any) error }) (contract.ModelRoutingBinding, error) {
	var binding contract.ModelRoutingBinding
	if err := row.Scan(&binding.Principal, &binding.SessionID, &binding.Protocol, &binding.VirtualModelID, &binding.GroupID, &binding.MemberID, &binding.ServiceID, &binding.Model, &binding.ConfigSnapshotVersion, &binding.RequestID, &binding.CreatedAt, &binding.UpdatedAt, &binding.ExpiresAt); err != nil {
		return contract.ModelRoutingBinding{}, err
	}
	if err := binding.Validate(); err != nil {
		return contract.ModelRoutingBinding{}, fmt.Errorf("%w: model routing binding: %v", storagecontract.ErrInvalidRecord, err)
	}
	return binding, nil
}

func (store *Store) GetModelRoutingBinding(ctx context.Context, principal string, sessionID contract.SessionID, protocol contract.ProtocolID, virtualModelID string) (contract.ModelRoutingBinding, error) {
	binding, err := scanModelRoutingBinding(store.db.QueryRowContext(ctx, `SELECT `+modelRoutingBindingColumns+` FROM model_routing_bindings WHERE principal = ? AND session_id = ? AND protocol = ? AND virtual_model_id = ? AND expires_at > ?`, principal, sessionID, protocol, virtualModelID, bindingTime(store.now())))
	if errors.Is(err, sql.ErrNoRows) {
		return contract.ModelRoutingBinding{}, fmt.Errorf("%w: model routing binding", storagecontract.ErrNotFound)
	}
	return binding, err
}

// UpsertModelRoutingBinding shares the API provider bindings' writer lock and
// release fence: "re-select" in the session details clears both kinds at
// once, and a request already in flight at that moment must not bring a
// routing binding back.
func (store *Store) UpsertModelRoutingBinding(ctx context.Context, binding contract.ModelRoutingBinding, started time.Time) error {
	if err := binding.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	if err := store.lockChannelBindings(ctx); err != nil {
		return err
	}
	defer func() { <-store.channelBindingsMu }()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var released string
	err = tx.QueryRowContext(ctx, `SELECT released_at FROM channel_binding_releases WHERE session_id = ?`, binding.SessionID).Scan(&released)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if released != "" && bindingTime(started) <= released {
		return nil
	}
	// A late completion cannot replace a newer request's decision, the same
	// guard API provider bindings keep.
	var oldStart string
	err = tx.QueryRowContext(ctx, `SELECT request_started_at FROM model_routing_bindings WHERE principal = ? AND session_id = ? AND protocol = ? AND virtual_model_id = ?`, binding.Principal, binding.SessionID, binding.Protocol, binding.VirtualModelID).Scan(&oldStart)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if oldStart != "" && oldStart >= bindingTime(started) {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO model_routing_bindings (`+modelRoutingBindingColumns+`, request_started_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(principal, session_id, protocol, virtual_model_id) DO UPDATE SET group_id = excluded.group_id, member_id = excluded.member_id, service_id = excluded.service_id, model = excluded.model, config_snapshot_version = excluded.config_snapshot_version, request_id = excluded.request_id, updated_at = excluded.updated_at, expires_at = excluded.expires_at, request_started_at = excluded.request_started_at`, binding.Principal, binding.SessionID, binding.Protocol, binding.VirtualModelID, binding.GroupID, binding.MemberID, binding.ServiceID, binding.Model, binding.ConfigSnapshotVersion, binding.RequestID, binding.CreatedAt, binding.UpdatedAt, binding.ExpiresAt, bindingTime(started)); err != nil {
		return err
	}
	// Every new session adds a row, so the table is kept bounded the way API
	// provider bindings are: an expired row is never read again, and past
	// 10000 rows the ones expiring soonest go first.
	if _, err = tx.ExecContext(ctx, `DELETE FROM model_routing_bindings WHERE expires_at <= ? OR rowid IN (SELECT rowid FROM model_routing_bindings ORDER BY expires_at DESC LIMIT -1 OFFSET 10000)`, bindingTime(store.now())); err != nil {
		return err
	}
	return tx.Commit()
}

func (store *Store) DeleteModelRoutingBinding(ctx context.Context, principal string, sessionID contract.SessionID, protocol contract.ProtocolID, virtualModelID string) error {
	_, err := store.db.ExecContext(ctx, `DELETE FROM model_routing_bindings WHERE principal = ? AND session_id = ? AND protocol = ? AND virtual_model_id = ?`, principal, sessionID, protocol, virtualModelID)
	return err
}

// sessionModelRoutingBindings lists the session's routing bindings a request
// would still reuse: unexpired, in a group that is enabled, and naming a
// member the group still has. A binding that fails any of these is dropped by
// the next request anyway, so the session details do not offer it.
func (store *Store) sessionModelRoutingBindings(ctx context.Context, id contract.SessionID, groups []contract.ModelRoutingGroup) ([]contract.ModelRoutingSessionBinding, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT `+modelRoutingBindingColumns+` FROM model_routing_bindings WHERE session_id = ? AND expires_at > ? ORDER BY virtual_model_id, protocol, principal`, id, bindingTime(store.now()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := []contract.ModelRoutingSessionBinding{}
	for rows.Next() {
		binding, err := scanModelRoutingBinding(rows)
		if err != nil {
			return nil, err
		}
		if !contract.ModelRoutingBindingCurrent(binding, groups) {
			continue
		}
		expires, err := time.Parse(time.RFC3339Nano, binding.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("%w: model routing binding expiry", storagecontract.ErrInvalidRecord)
		}
		bindings = append(bindings, contract.ModelRoutingSessionBinding{
			Principal: binding.Principal, Protocol: binding.Protocol, VirtualModelID: binding.VirtualModelID,
			ServiceID: binding.ServiceID, Model: binding.Model, RequestID: contract.RequestID(binding.RequestID), ExpiresAt: expires.UTC(),
		})
	}
	return bindings, rows.Err()
}

// ensureModelRoutingGroupVersionTx returns the version number of the group's
// configuration, saving it as the group's next version when its content
// differs from the newest one. Content that matches an older version still
// becomes a new version: going back to an earlier configuration is a change
// someone made, and the record should say when.
func ensureModelRoutingGroupVersionTx(ctx context.Context, tx *sql.Tx, group contract.ModelRoutingGroup, now time.Time) (int, error) {
	hash, err := contract.ModelRoutingGroupConfigHash(group)
	if err != nil {
		return 0, err
	}
	var latest int
	var latestHash string
	err = tx.QueryRowContext(ctx, `SELECT version, config_hash FROM model_routing_group_versions WHERE group_id = ? ORDER BY version DESC LIMIT 1`, group.ID).Scan(&latest, &latestHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if latestHash == hash {
		return latest, nil
	}
	document, err := json.Marshal(group)
	if err != nil {
		return 0, err
	}
	// A concurrent writer may have saved the same content first; the re-read
	// below then returns its version instead of minting a duplicate.
	if _, err = tx.ExecContext(ctx, `INSERT INTO model_routing_group_versions (group_id, version, config_hash, document_json, created_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(group_id, version) DO NOTHING`, group.ID, latest+1, hash, string(document), bindingTime(now)); err != nil {
		return 0, err
	}
	var version int
	if err = tx.QueryRowContext(ctx, `SELECT version FROM model_routing_group_versions WHERE group_id = ? AND config_hash = ? ORDER BY version DESC LIMIT 1`, group.ID, hash).Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

// EnsureModelRoutingGroupVersion is the request path's read of the version a
// group is routed under. Content a save already numbered keeps that number —
// the newest version with the same content — so a request that read its
// settings just before a save labels itself with the configuration it really
// used and never adds a version of its own. Only content that reached the
// store without a save, such as a configuration an older Core wrote, is
// numbered here.
func (store *Store) EnsureModelRoutingGroupVersion(ctx context.Context, group contract.ModelRoutingGroup) (int, error) {
	hash, err := contract.ModelRoutingGroupConfigHash(group)
	if err != nil {
		return 0, err
	}
	var version int
	err = store.db.QueryRowContext(ctx, `SELECT version FROM model_routing_group_versions WHERE group_id = ? AND config_hash = ? ORDER BY version DESC LIMIT 1`, group.ID, hash).Scan(&version)
	if err == nil {
		return version, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	version, err = ensureModelRoutingGroupVersionTx(ctx, tx, group, store.now())
	if err != nil {
		return 0, err
	}
	return version, tx.Commit()
}

func (store *Store) GetModelRoutingGroupVersion(ctx context.Context, groupID string, version int) (contract.ModelRoutingGroupVersion, error) {
	var document, createdAt string
	err := store.db.QueryRowContext(ctx, `SELECT document_json, created_at FROM model_routing_group_versions WHERE group_id = ? AND version = ?`, groupID, version).Scan(&document, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.ModelRoutingGroupVersion{}, fmt.Errorf("%w: model routing group version", storagecontract.ErrNotFound)
	}
	if err != nil {
		return contract.ModelRoutingGroupVersion{}, err
	}
	result := contract.ModelRoutingGroupVersion{GroupID: groupID, Version: version, CreatedAt: createdAt}
	if err := json.Unmarshal([]byte(document), &result.Group); err != nil {
		return contract.ModelRoutingGroupVersion{}, fmt.Errorf("%w: model routing group version", storagecontract.ErrInvalidRecord)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT MAX(version) FROM model_routing_group_versions WHERE group_id = ?`, groupID).Scan(&result.LatestVersion); err != nil {
		return contract.ModelRoutingGroupVersion{}, err
	}
	return result, nil
}

func (store *Store) UpdateRoutingSettingsAtomic(ctx context.Context, settings contract.RoutingSettings) (err error) {
	if settings.ModelRoutingGroups == nil {
		settings.ModelRoutingGroups = []contract.ModelRoutingGroup{}
	}
	if settings.ModelRedirects == nil {
		settings.ModelRedirects = []contract.ModelRedirect{}
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackOnError(tx, &err)
	if err = settings.ValidateWithDimensions(routingSettingsDimensions(ctx, tx)); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	if err = validateModelRoutingReferencesTx(ctx, tx, settings.ModelRoutingGroups); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	document, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE routing_settings SET document_json = ? WHERE id = 1`, string(document)); err != nil {
		return err
	}
	// Versions are taken in the same transaction as the document, so a saved
	// configuration always has the number its first request will record.
	now := store.now()
	for _, group := range settings.ModelRoutingGroups {
		if _, err = ensureModelRoutingGroupVersionTx(ctx, tx, group, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func validateModelRoutingReferencesTx(ctx context.Context, tx *sql.Tx, groups []contract.ModelRoutingGroup) error {
	for _, group := range groups {
		for _, member := range group.Members {
			var document string
			if err := tx.QueryRowContext(ctx, `SELECT document_json FROM services WHERE id = ?`, member.ServiceID).Scan(&document); err != nil {
				return fmt.Errorf("group %q member %q service %q is unavailable", group.ID, member.ID, member.ServiceID)
			}
			var service contract.Service
			if err := json.Unmarshal([]byte(document), &service); err != nil {
				return fmt.Errorf("service %q is invalid", member.ServiceID)
			}
			if !service.Enabled {
				return fmt.Errorf("group %q member %q service %q is disabled", group.ID, member.ID, member.ServiceID)
			}
			found := false
			for _, model := range service.Models {
				if model == member.Model {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("group %q member %q model %q is not configured on service %q", group.ID, member.ID, member.Model, member.ServiceID)
			}
		}
	}
	return nil
}

func validateStoredRoutingReferencesTx(ctx context.Context, tx *sql.Tx) error {
	var document string
	if err := tx.QueryRowContext(ctx, `SELECT document_json FROM routing_settings WHERE id = 1`).Scan(&document); err != nil {
		return err
	}
	settings := contract.DefaultRoutingSettings()
	if err := json.Unmarshal([]byte(document), &settings); err != nil {
		return err
	}
	// The dimensions live in their own singleton; read them inside the same
	// transaction so a group naming a custom dimension still validates here.
	if err := settings.ValidateWithDimensions(routingSettingsDimensions(ctx, tx)); err != nil {
		return fmt.Errorf("%w: stored routing settings: %v", storagecontract.ErrInvalidRecord, err)
	}
	if settings.ModelRoutingGroups == nil {
		settings.ModelRoutingGroups = []contract.ModelRoutingGroup{}
	}
	// The stored groups were valid before this change, so a member that no
	// longer resolves is the change's doing.
	if err := validateModelRoutingReferencesTx(ctx, tx, settings.ModelRoutingGroups); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrModelRoutingReference, err)
	}
	return nil
}
