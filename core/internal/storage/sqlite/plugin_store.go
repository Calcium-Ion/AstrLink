package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const (
	pluginCredentialsTable = "plugin_credentials"
	pluginRefPrefix        = "local://plugin/"
)

func (store *Store) ListPluginSettings(ctx context.Context) ([]storage.PluginSettingsRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT plugin_key, enabled, granted_capabilities_json, network_origins_json, privacy_trusted, turn_wait_ms, config_json,
    excluded_clients_json, workspace_binding, auto_refresh, lookup_enabled, hook_limits_json, fail_closed, ui_enabled, created_at, updated_at
FROM plugin_settings ORDER BY plugin_key`)
	if err != nil {
		return nil, fmt.Errorf("read plugin settings: %w", err)
	}
	defer rows.Close()
	var records []storage.PluginSettingsRecord
	for rows.Next() {
		record, err := scanPluginSettings(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read plugin settings: %w", err)
	}
	return records, nil
}

func (store *Store) GetPluginSettings(ctx context.Context, plugin string) (storage.PluginSettingsRecord, error) {
	row := store.db.QueryRowContext(ctx, `SELECT plugin_key, enabled, granted_capabilities_json, network_origins_json, privacy_trusted, turn_wait_ms, config_json,
    excluded_clients_json, workspace_binding, auto_refresh, lookup_enabled, hook_limits_json, fail_closed, ui_enabled, created_at, updated_at
FROM plugin_settings WHERE plugin_key = ?`, plugin)
	record, err := scanPluginSettings(row)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.PluginSettingsRecord{}, fmt.Errorf("%w: plugin settings %s", storage.ErrNotFound, plugin)
	}
	return record, err
}

type scanner interface{ Scan(...any) error }

func scanPluginSettings(row scanner) (storage.PluginSettingsRecord, error) {
	var record storage.PluginSettingsRecord
	var capabilities, origins, config, excluded, limits, created, updated string
	if err := row.Scan(&record.Key, &record.Enabled, &capabilities, &origins, &record.Trusted, &record.TurnWaitMs, &config,
		&excluded, &record.WorkspaceBinding, &record.AutoRefresh, &record.LookupEnabled, &limits, &record.FailClosed, &record.UIEnabled, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return record, err
		}
		return record, fmt.Errorf("read plugin settings: %w", err)
	}
	if json.Unmarshal([]byte(capabilities), &record.Capabilities) != nil || json.Unmarshal([]byte(origins), &record.Origins) != nil ||
		json.Unmarshal([]byte(excluded), &record.ExcludedClients) != nil || !json.Valid([]byte(config)) ||
		json.Unmarshal([]byte(limits), &record.HookLimitsMs) != nil {
		return record, fmt.Errorf("%w: plugin settings %s", storage.ErrInvalidRecord, record.Key)
	}
	record.Config = json.RawMessage(config)
	var err error
	if record.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return record, fmt.Errorf("%w: plugin settings %s", storage.ErrInvalidRecord, record.Key)
	}
	if record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return record, fmt.Errorf("%w: plugin settings %s", storage.ErrInvalidRecord, record.Key)
	}
	return record, nil
}

func (store *Store) PutPluginSettings(ctx context.Context, record storage.PluginSettingsRecord) error {
	if record.Key == "" || len(record.Key) > 32 {
		return fmt.Errorf("%w: plugin key", storage.ErrInvalidArgument)
	}
	capabilities, err := json.Marshal(nonNil(record.Capabilities))
	if err != nil {
		return fmt.Errorf("%w: plugin capabilities", storage.ErrInvalidArgument)
	}
	origins, err := json.Marshal(nonNil(record.Origins))
	if err != nil {
		return fmt.Errorf("%w: plugin origins", storage.ErrInvalidArgument)
	}
	config := record.Config
	if len(config) == 0 {
		config = json.RawMessage("{}")
	}
	if len(config) > storage.MaxPluginValueBytes || !json.Valid(config) {
		return fmt.Errorf("%w: plugin config must be JSON of at most %d KiB", storage.ErrInvalidArgument, storage.MaxPluginValueBytes>>10)
	}
	wait := record.TurnWaitMs
	if wait == 0 {
		wait = contract.DefaultPluginTurnWaitMs
	}
	if wait < contract.MinPluginTurnWaitMs || wait > contract.MaxPluginTurnWaitMs {
		return fmt.Errorf("%w: plugin turn wait must be %d to %d ms", storage.ErrInvalidArgument, contract.MinPluginTurnWaitMs, contract.MaxPluginTurnWaitMs)
	}
	if len(record.ExcludedClients) > contract.MaxPluginExcludedClients {
		return fmt.Errorf("%w: a plugin is off for at most %d clients", storage.ErrInvalidArgument, contract.MaxPluginExcludedClients)
	}
	excluded, err := json.Marshal(nonNil(record.ExcludedClients))
	if err != nil {
		return fmt.Errorf("%w: plugin clients", storage.ErrInvalidArgument)
	}
	for hook, ms := range record.HookLimitsMs {
		bounds, known := contract.PluginHookLimits[hook]
		if !known || ms < 1 || ms > bounds.MaxMs {
			return fmt.Errorf("%w: plugin time limit of %s", storage.ErrInvalidArgument, hook)
		}
	}
	limits, err := json.Marshal(nonNilMap(record.HookLimitsMs))
	if err != nil {
		return fmt.Errorf("%w: plugin time limits", storage.ErrInvalidArgument)
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO plugin_settings (plugin_key, enabled, granted_capabilities_json, network_origins_json, privacy_trusted, turn_wait_ms, config_json,
    excluded_clients_json, workspace_binding, auto_refresh, lookup_enabled, hook_limits_json, fail_closed, ui_enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(plugin_key) DO UPDATE SET enabled = excluded.enabled, granted_capabilities_json = excluded.granted_capabilities_json,
    network_origins_json = excluded.network_origins_json, privacy_trusted = excluded.privacy_trusted, turn_wait_ms = excluded.turn_wait_ms,
    config_json = excluded.config_json, excluded_clients_json = excluded.excluded_clients_json,
    workspace_binding = excluded.workspace_binding, auto_refresh = excluded.auto_refresh, lookup_enabled = excluded.lookup_enabled,
    hook_limits_json = excluded.hook_limits_json, fail_closed = excluded.fail_closed, ui_enabled = excluded.ui_enabled,
    updated_at = excluded.updated_at`,
		record.Key, record.Enabled, string(capabilities), string(origins), record.Trusted, wait, string(config),
		string(excluded), record.WorkspaceBinding, record.AutoRefresh, record.LookupEnabled, string(limits), record.FailClosed, record.UIEnabled, now, now); err != nil {
		return fmt.Errorf("write plugin settings: %w", err)
	}
	return nil
}

func (store *Store) PluginHostSettings(ctx context.Context) (contract.PluginHostSettings, error) {
	settings := contract.DefaultPluginHostSettings()
	var document string
	err := store.db.QueryRowContext(ctx, `SELECT document_json FROM plugin_host_settings WHERE id = 1`).Scan(&document)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return settings, fmt.Errorf("read plugin host settings: %w", err)
	}
	// A setting the document lacks keeps its default.
	if json.Unmarshal([]byte(document), &settings) != nil || settings.Validate() != nil {
		return contract.DefaultPluginHostSettings(), fmt.Errorf("%w: plugin host settings", storage.ErrInvalidRecord)
	}
	return settings, nil
}

func (store *Store) PutPluginHostSettings(ctx context.Context, settings contract.PluginHostSettings) error {
	if err := settings.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	document, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("%w: plugin host settings", storage.ErrInvalidArgument)
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO plugin_host_settings (id, document_json, updated_at) VALUES (1, ?, ?)
ON CONFLICT(id) DO UPDATE SET document_json = excluded.document_json, updated_at = excluded.updated_at`, string(document), now); err != nil {
		return fmt.Errorf("write plugin host settings: %w", err)
	}
	return nil
}

func (store *Store) PluginSession(ctx context.Context, plugin, session string) (storage.PluginSessionRecord, bool, error) {
	row := store.db.QueryRowContext(ctx, `SELECT plugin_key, session_id, first_turn, paused, created_at, updated_at
FROM plugin_sessions WHERE plugin_key = ? AND session_id = ?`, plugin, session)
	record, err := scanPluginSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.PluginSessionRecord{}, false, nil
	}
	if err != nil {
		return storage.PluginSessionRecord{}, false, err
	}
	return record, true, nil
}

func (store *Store) StartPluginSession(ctx context.Context, plugin, session string, turn int) (record storage.PluginSessionRecord, err error) {
	if err := validPluginSession(plugin, session, turn); err != nil {
		return record, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return record, fmt.Errorf("begin plugin session write: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	if err = store.ensurePluginSettings(ctx, transaction, plugin); err != nil {
		return record, err
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	if _, err = transaction.ExecContext(ctx, `INSERT INTO plugin_sessions (plugin_key, session_id, first_turn, created_at, updated_at)
VALUES (?, ?, ?, ?, ?) ON CONFLICT(plugin_key, session_id) DO NOTHING`, plugin, session, turn, now, now); err != nil {
		err = fmt.Errorf("write plugin session: %w", err)
		return record, err
	}
	if record, err = scanPluginSession(transaction.QueryRowContext(ctx, `SELECT plugin_key, session_id, first_turn, paused, created_at, updated_at
FROM plugin_sessions WHERE plugin_key = ? AND session_id = ?`, plugin, session)); err != nil {
		return record, err
	}
	err = transaction.Commit()
	return record, err
}

func (store *Store) PausePluginSession(ctx context.Context, plugin, session string, paused bool, turn int) (record storage.PluginSessionRecord, err error) {
	if err := validPluginSession(plugin, session, turn); err != nil {
		return record, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return record, fmt.Errorf("begin plugin session write: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	if err = store.ensurePluginSettings(ctx, transaction, plugin); err != nil {
		return record, err
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	if _, err = transaction.ExecContext(ctx, `INSERT INTO plugin_sessions (plugin_key, session_id, first_turn, paused, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(plugin_key, session_id) DO UPDATE SET paused = excluded.paused, updated_at = excluded.updated_at`,
		plugin, session, turn, paused, now, now); err != nil {
		err = fmt.Errorf("write plugin session: %w", err)
		return record, err
	}
	if record, err = scanPluginSession(transaction.QueryRowContext(ctx, `SELECT plugin_key, session_id, first_turn, paused, created_at, updated_at
FROM plugin_sessions WHERE plugin_key = ? AND session_id = ?`, plugin, session)); err != nil {
		return record, err
	}
	err = transaction.Commit()
	return record, err
}

func (store *Store) CountPluginSessions(ctx context.Context, plugin string) (int, error) {
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_sessions WHERE plugin_key = ?`, plugin).Scan(&count); err != nil {
		return 0, fmt.Errorf("count plugin sessions: %w", err)
	}
	return count, nil
}

func (store *Store) ScanPluginActivity(ctx context.Context, since time.Time, visit func(storage.PluginActivity) error) error {
	rows, err := store.db.QueryContext(ctx, `SELECT started_at, COALESCE(session_id, ''), usage_json, metadata_json, plugin_records_json
FROM request_records WHERE parent_request_id IS NULL AND started_at >= ? ORDER BY started_at, id`, since.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("read plugin activity: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var started string
		var usage, metadata, records sql.NullString
		var activity storage.PluginActivity
		if err := rows.Scan(&started, &activity.SessionID, &usage, &metadata, &records); err != nil {
			return fmt.Errorf("read plugin activity: %w", err)
		}
		if activity.StartedAt, err = time.Parse(time.RFC3339Nano, started); err != nil {
			return fmt.Errorf("%w: request started at %q", storage.ErrInvalidRecord, started)
		}
		// The statistics count what they can read and leave out the rest.
		if usage.Valid && usage.String != "" {
			var parsed contract.Usage
			if json.Unmarshal([]byte(usage.String), &parsed) == nil {
				activity.Usage = &parsed
			}
		}
		if metadata.Valid && metadata.String != "" {
			var parsed contract.RequestMetadata
			if json.Unmarshal([]byte(metadata.String), &parsed) == nil {
				activity.Metadata = &parsed
			}
		}
		if records.Valid && records.String != "" {
			_ = json.Unmarshal([]byte(records.String), &activity.Records)
		}
		if err := visit(activity); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read plugin activity: %w", err)
	}
	return nil
}

func validPluginSession(plugin, session string, turn int) error {
	if plugin == "" || len(plugin) > 32 || session == "" || len(session) > 256 || turn < 0 {
		return fmt.Errorf("%w: plugin session", storage.ErrInvalidArgument)
	}
	return nil
}

func scanPluginSession(row scanner) (storage.PluginSessionRecord, error) {
	var record storage.PluginSessionRecord
	var created, updated string
	if err := row.Scan(&record.Plugin, &record.Session, &record.FirstTurn, &record.Paused, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return record, err
		}
		return record, fmt.Errorf("read plugin session: %w", err)
	}
	var err error
	if record.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return record, fmt.Errorf("%w: plugin session %s", storage.ErrInvalidRecord, record.Session)
	}
	if record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return record, fmt.Errorf("%w: plugin session %s", storage.ErrInvalidRecord, record.Session)
	}
	return record, nil
}

func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

func nonNilMap[K comparable, V any](values map[K]V) map[K]V {
	if values == nil {
		return map[K]V{}
	}
	return values
}

// ensurePluginSettings creates the default row the plugin's credentials and
// values hang from.
func (store *Store) ensurePluginSettings(ctx context.Context, executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, plugin string) error {
	if plugin == "" || len(plugin) > 32 {
		return fmt.Errorf("%w: plugin key", storage.ErrInvalidArgument)
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	if _, err := executor.ExecContext(ctx, `INSERT INTO plugin_settings (plugin_key, created_at, updated_at) VALUES (?, ?, ?) ON CONFLICT(plugin_key) DO NOTHING`, plugin, now, now); err != nil {
		return fmt.Errorf("write plugin settings: %w", err)
	}
	return nil
}

func pluginCredentialID(plugin, name string) (string, error) {
	if plugin == "" || len(plugin) > 32 || name == "" || len(name) > 32 || strings.Contains(plugin, "/") || strings.Contains(name, "/") {
		return "", fmt.Errorf("%w: plugin credential name", storage.ErrInvalidArgument)
	}
	return plugin + "/" + name, nil
}

func (store *Store) ListPluginCredentials(ctx context.Context, plugin string) ([]storage.PluginCredentialRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT plugin_key, name, origin, hint, updated_at FROM plugin_credentials WHERE plugin_key = ? ORDER BY name`, plugin)
	if err != nil {
		return nil, fmt.Errorf("read plugin credentials: %w", err)
	}
	defer rows.Close()
	var records []storage.PluginCredentialRecord
	for rows.Next() {
		var record storage.PluginCredentialRecord
		var updated string
		if err := rows.Scan(&record.Plugin, &record.Name, &record.Origin, &record.Hint, &updated); err != nil {
			return nil, fmt.Errorf("read plugin credentials: %w", err)
		}
		if record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
			return nil, fmt.Errorf("%w: plugin credential %s", storage.ErrInvalidRecord, record.Name)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read plugin credentials: %w", err)
	}
	return records, nil
}

func (store *Store) PutPluginCredential(ctx context.Context, plugin, name, origin string, secret []byte) (err error) {
	id, err := pluginCredentialID(plugin, name)
	if err != nil {
		return err
	}
	if origin == "" || len(origin) > 512 {
		return fmt.Errorf("%w: plugin credential origin", storage.ErrInvalidArgument)
	}
	if err := validateCredential(secret); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	sealed, err := store.keys.sealColumn(pluginCredentialsTable, id, secret)
	if err != nil {
		return fmt.Errorf("seal plugin credential: %w", err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin plugin credential write: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	if err = store.ensurePluginSettings(ctx, transaction, plugin); err != nil {
		return err
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	if _, err = transaction.ExecContext(ctx, `INSERT INTO plugin_credentials (id, plugin_key, name, credential_value, sealed, origin, hint, created_at, updated_at)
VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET credential_value = excluded.credential_value, sealed = 1, origin = excluded.origin, hint = excluded.hint, updated_at = excluded.updated_at`,
		id, plugin, name, sealed, origin, credentialHint(secret), now, now); err != nil {
		return fmt.Errorf("write plugin credential: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit plugin credential write: %w", err)
	}
	return nil
}

// credentialHint is the last four characters of a credential long enough to
// keep the rest unguessable, and nothing for a shorter one.
func credentialHint(secret []byte) string {
	if utf8.RuneCount(secret) < 16 {
		return ""
	}
	runes := []rune(string(secret))
	return string(runes[len(runes)-4:])
}

func (store *Store) BindPluginCredential(ctx context.Context, plugin, name, origin string) error {
	id, err := pluginCredentialID(plugin, name)
	if err != nil {
		return err
	}
	if origin == "" || len(origin) > 512 {
		return fmt.Errorf("%w: plugin credential origin", storage.ErrInvalidArgument)
	}
	result, err := store.db.ExecContext(ctx, `UPDATE plugin_credentials SET origin = ?, updated_at = ? WHERE id = ?`,
		origin, store.now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("bind plugin credential: %w", err)
	}
	if bound, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("read plugin credential bind result: %w", err)
	} else if bound == 0 {
		return fmt.Errorf("%w: plugin credential %s", storage.ErrNotFound, id)
	}
	return nil
}

func (store *Store) DeletePluginCredential(ctx context.Context, plugin, name string) error {
	id, err := pluginCredentialID(plugin, name)
	if err != nil {
		return err
	}
	result, err := store.db.ExecContext(ctx, `DELETE FROM plugin_credentials WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete plugin credential: %w", err)
	}
	if deleted, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("read plugin credential delete result: %w", err)
	} else if deleted == 0 {
		return fmt.Errorf("%w: plugin credential %s", storage.ErrNotFound, id)
	}
	return nil
}

func (store *Store) PluginCredential(ctx context.Context, plugin, name string) ([]byte, string, error) {
	id, err := pluginCredentialID(plugin, name)
	if err != nil {
		return nil, "", err
	}
	var stored []byte
	var sealed bool
	var origin string
	if err := store.db.QueryRowContext(ctx, `SELECT credential_value, sealed, origin FROM plugin_credentials WHERE id = ?`, id).Scan(&stored, &sealed, &origin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", fmt.Errorf("%w: plugin credential %s", storage.ErrNotFound, id)
		}
		return nil, "", fmt.Errorf("read plugin credential: %w", err)
	}
	secret, err := store.openSecret(pluginCredentialsTable, id, stored, sealed)
	if err != nil {
		return nil, "", err
	}
	return secret, origin, nil
}

// pluginCredential serves local://plugin/<plugin>/<name> for secretstore
// readers. Writes go through PutPluginCredential, which binds the origin.
func (store *Store) pluginCredential(ctx context.Context, ref secretstore.Ref) ([]byte, error) {
	plugin, name, ok := strings.Cut(strings.TrimPrefix(string(ref), pluginRefPrefix), "/")
	if !ok {
		return nil, fmt.Errorf("%w: %s", storage.ErrUnsupportedRef, ref)
	}
	secret, _, err := store.PluginCredential(ctx, plugin, name)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, fmt.Errorf("%w: %s", secretstore.ErrNotFound, ref)
	}
	return secret, err
}

func (store *Store) GetPluginValue(ctx context.Context, plugin, key string) (json.RawMessage, bool, error) {
	var value string
	err := store.db.QueryRowContext(ctx, `SELECT value_json FROM plugin_kv WHERE plugin_key = ? AND key = ?`, plugin, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read plugin value: %w", err)
	}
	return json.RawMessage(value), true, nil
}

func (store *Store) PutPluginValue(ctx context.Context, plugin, key string, value json.RawMessage) (err error) {
	if key == "" || utf8.RuneCountInString(key) > 256 || !json.Valid(value) {
		return fmt.Errorf("%w: plugin value", storage.ErrInvalidArgument)
	}
	if len(value) > storage.MaxPluginValueBytes {
		return fmt.Errorf("%w: a plugin value is at most %d KiB", storage.ErrLimitReached, storage.MaxPluginValueBytes>>10)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin plugin value write: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	if err = store.ensurePluginSettings(ctx, transaction, plugin); err != nil {
		return err
	}
	var others int64
	if err = transaction.QueryRowContext(ctx, `SELECT COALESCE(SUM(length(CAST(key AS BLOB)) + length(CAST(value_json AS BLOB))), 0) FROM plugin_kv WHERE plugin_key = ? AND key <> ?`, plugin, key).Scan(&others); err != nil {
		return fmt.Errorf("read plugin values size: %w", err)
	}
	if others+int64(len(key)+len(value)) > storage.MaxPluginValuesSize {
		err = fmt.Errorf("%w: a plugin's values are at most %d MiB", storage.ErrLimitReached, storage.MaxPluginValuesSize>>20)
		return err
	}
	if _, err = transaction.ExecContext(ctx, `INSERT INTO plugin_kv (plugin_key, key, value_json, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT(plugin_key, key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`,
		plugin, key, string(value), store.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("write plugin value: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit plugin value write: %w", err)
	}
	return nil
}

func (store *Store) DeletePluginValue(ctx context.Context, plugin, key string) error {
	if _, err := store.db.ExecContext(ctx, `DELETE FROM plugin_kv WHERE plugin_key = ? AND key = ?`, plugin, key); err != nil {
		return fmt.Errorf("delete plugin value: %w", err)
	}
	return nil
}

func (store *Store) DeletePluginValues(ctx context.Context, plugin string) (int, error) {
	result, err := store.db.ExecContext(ctx, `DELETE FROM plugin_kv WHERE plugin_key = ?`, plugin)
	if err != nil {
		return 0, fmt.Errorf("delete plugin values: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete plugin values: %w", err)
	}
	return int(removed), nil
}

func (store *Store) PluginValueKeys(ctx context.Context, plugin, prefix string) ([]string, error) {
	// substr compares the prefix exactly, where LIKE would read % and _.
	rows, err := store.db.QueryContext(ctx, `SELECT key FROM plugin_kv WHERE plugin_key = ? AND substr(key, 1, length(?)) = ? ORDER BY key`, plugin, prefix, prefix)
	if err != nil {
		return nil, fmt.Errorf("read plugin value keys: %w", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("read plugin value keys: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read plugin value keys: %w", err)
	}
	return keys, nil
}
