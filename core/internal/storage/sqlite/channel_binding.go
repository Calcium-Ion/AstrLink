package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

const bindingTimeLayout = "2006-01-02T15:04:05.000000000Z"

func bindingTime(t time.Time) string { return t.UTC().Format(bindingTimeLayout) }

// Include writer serialization in the request's persistence deadline.
func (store *Store) lockChannelBindings(ctx context.Context) error {
	select {
	case store.channelBindingsMu <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func bindingScopeArgs(scope contract.ChannelBindingScope) []any {
	return []any{scope.SessionID, scope.Principal, scope.Protocol, scope.Model}
}

const bindingColumns = `session_id, principal, protocol, model, service_id, source, request_id, updated_at, expires_at`
const bindingWhere = `session_id = ? AND principal = ? AND protocol = ? AND model = ?`

func scanChannelBinding(row interface{ Scan(...any) error }) (contract.ChannelBinding, error) {
	var b contract.ChannelBinding
	var updated, expires string
	err := row.Scan(&b.SessionID, &b.Principal, &b.Protocol, &b.Model, &b.ServiceID, &b.Source, &b.RequestID, &updated, &expires)
	if err != nil {
		return b, err
	}
	if b.UpdatedAt, err = time.Parse(bindingTimeLayout, updated); err != nil {
		return b, err
	}
	b.ExpiresAt, err = time.Parse(bindingTimeLayout, expires)
	return b, err
}

// Return expired entries too so selection can explain an expired preference.
func (store *Store) GetChannelBinding(ctx context.Context, scope contract.ChannelBindingScope) (contract.ChannelBinding, bool, error) {
	b, err := scanChannelBinding(store.db.QueryRowContext(ctx, `SELECT `+bindingColumns+` FROM channel_bindings WHERE `+bindingWhere, bindingScopeArgs(scope)...))
	if errors.Is(err, sql.ErrNoRows) {
		return b, false, nil
	}
	return b, err == nil, err
}

func insertBindingEvent(ctx context.Context, tx *sql.Tx, event contract.ChannelBindingEvent) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO channel_binding_events(session_id, document_json) VALUES (?, ?)`, event.SessionID, string(raw)); err != nil {
		return err
	}
	// A bounded local diagnostic log; the API explicitly reports truncation.
	_, err = tx.ExecContext(ctx, `DELETE FROM channel_binding_events WHERE id <= (SELECT MAX(id) FROM channel_binding_events) - 50000`)
	return err
}

func (store *Store) RecordChannelBindingEvent(ctx context.Context, event contract.ChannelBindingEvent) (err error) {
	if err = store.lockChannelBindings(ctx); err != nil {
		return err
	}
	defer func() { <-store.channelBindingsMu }()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackOnError(tx, &err)
	event.At = store.now().UTC()
	if err = insertBindingEvent(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}

func (store *Store) RememberChannelBinding(ctx context.Context, binding contract.ChannelBinding, started time.Time) (err error) {
	if err = store.lockChannelBindings(ctx); err != nil {
		return err
	}
	defer func() { <-store.channelBindingsMu }()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// User release fences requests already in flight. The tombstone survives
	// restart and never changes the protocol's strict response affinity.
	var released string
	err = tx.QueryRowContext(ctx, `SELECT released_at FROM channel_binding_releases WHERE session_id = ?`, binding.SessionID).Scan(&released)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if released != "" && bindingTime(started) <= released {
		return nil
	}
	var oldStart string
	var oldService contract.ServiceID
	var oldExpires string
	err = tx.QueryRowContext(ctx, `SELECT service_id, request_started_at, expires_at FROM channel_bindings WHERE `+bindingWhere, bindingScopeArgs(binding.ChannelBindingScope)...).Scan(&oldService, &oldStart, &oldExpires)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// A late completion cannot replace a newer request's successful decision.
	if oldStart != "" && oldStart >= bindingTime(started) {
		return nil
	}
	args := append(bindingScopeArgs(binding.ChannelBindingScope), binding.ServiceID, binding.Source, binding.RequestID, bindingTime(binding.UpdatedAt), bindingTime(binding.ExpiresAt), bindingTime(started))
	_, err = tx.ExecContext(ctx, `INSERT INTO channel_bindings (`+bindingColumns+`, request_started_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id, principal, protocol, model) DO UPDATE SET service_id=excluded.service_id, source=excluded.source, request_id=excluded.request_id, updated_at=excluded.updated_at, expires_at=excluded.expires_at, request_started_at=excluded.request_started_at`, args...)
	if err != nil {
		return err
	}
	if oldService != binding.ServiceID || oldExpires <= bindingTime(binding.UpdatedAt) {
		action := "bound"
		if oldService != "" && oldService != binding.ServiceID {
			action = "switched"
		}
		err = insertBindingEvent(ctx, tx, contract.ChannelBindingEvent{ChannelBindingScope: binding.ChannelBindingScope, At: binding.UpdatedAt, Action: action, Reason: "request_succeeded", Source: binding.Source, ServiceID: binding.ServiceID, PreviousServiceID: oldService, RequestID: binding.RequestID})
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM channel_bindings WHERE rowid IN (SELECT rowid FROM channel_bindings ORDER BY expires_at DESC LIMIT -1 OFFSET 10000)`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (store *Store) GetChannelBindingAudit(ctx context.Context, id contract.SessionID, before int64) (contract.ChannelBindingAudit, error) {
	result := contract.ChannelBindingAudit{Bindings: []contract.ChannelBinding{}, Events: []contract.ChannelBindingEvent{}}
	settings, err := store.GetRoutingSettings(ctx)
	if err != nil {
		return result, err
	}
	result.Enabled = settings.ChannelStickiness != nil && settings.ChannelStickiness.Enabled
	rows, err := store.db.QueryContext(ctx, `SELECT `+bindingColumns+` FROM channel_bindings WHERE session_id = ? AND expires_at > ? ORDER BY model, protocol, principal`, id, bindingTime(store.now()))
	if err != nil {
		return result, err
	}
	for rows.Next() {
		binding, scanErr := scanChannelBinding(rows)
		if scanErr != nil {
			rows.Close()
			return result, scanErr
		}
		result.Bindings = append(result.Bindings, binding)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	rows, err = store.db.QueryContext(ctx, `SELECT id, document_json FROM channel_binding_events WHERE session_id = ? AND (? = 0 OR id < ?) ORDER BY id DESC LIMIT 101`, id, before, before)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var event contract.ChannelBindingEvent
		var raw string
		var eventID int64
		if err = rows.Scan(&eventID, &raw); err != nil {
			return result, err
		}
		if err = json.Unmarshal([]byte(raw), &event); err != nil {
			return result, err
		}
		event.ID = eventID
		result.Events = append(result.Events, event)
	}
	if len(result.Events) > 100 {
		result.HasMore = true
		result.Events = result.Events[:100]
	}
	return result, rows.Err()
}

func (store *Store) ReleaseChannelBindings(ctx context.Context, id contract.SessionID) (err error) {
	if err = store.lockChannelBindings(ctx); err != nil {
		return err
	}
	defer func() { <-store.channelBindingsMu }()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackOnError(tx, &err)
	now := store.now().UTC()
	_, err = tx.ExecContext(ctx, `INSERT INTO channel_binding_releases(session_id, released_at) VALUES (?, ?) ON CONFLICT(session_id) DO UPDATE SET released_at=excluded.released_at`, id, bindingTime(now))
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM channel_bindings WHERE session_id = ?`, id); err != nil {
		return err
	}
	if err = insertBindingEvent(ctx, tx, contract.ChannelBindingEvent{ChannelBindingScope: contract.ChannelBindingScope{SessionID: id}, At: now, Action: "released", Reason: "user_requested"}); err != nil {
		return err
	}
	return tx.Commit()
}
