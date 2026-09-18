package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"time"
)

func (store *Store) GetResponseAffinity(ctx context.Context, principal, responseID string) (storage.ResponseAffinity, bool, error) {
	var binding storage.ResponseAffinity
	err := store.db.QueryRowContext(ctx, `SELECT service_id, upstream_model, upstream_protocol, plan_type FROM response_affinities WHERE principal = ? AND response_id = ? AND created_at >= ?`, principal, responseID, store.now().Add(-24*time.Hour).UTC().Format(time.RFC3339Nano)).Scan(&binding.ServiceID, &binding.UpstreamModel, &binding.UpstreamProtocol, &binding.PlanType)
	if errors.Is(err, sql.ErrNoRows) {
		return binding, false, nil
	}
	return binding, err == nil, err
}

func (store *Store) PutResponseAffinity(ctx context.Context, principal, responseID string, binding storage.ResponseAffinity) (err error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackOnError(tx, &err)
	_, err = tx.ExecContext(ctx, `INSERT INTO response_affinities (principal, response_id, service_id, upstream_model, upstream_protocol, plan_type, created_at) VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(principal, response_id) DO UPDATE SET service_id = excluded.service_id, upstream_model = excluded.upstream_model, upstream_protocol = excluded.upstream_protocol, plan_type = excluded.plan_type, created_at = excluded.created_at`, principal, responseID, binding.ServiceID, binding.UpstreamModel, binding.UpstreamProtocol, binding.PlanType, store.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM response_affinities WHERE rowid IN (SELECT rowid FROM response_affinities ORDER BY created_at DESC, rowid DESC LIMIT -1 OFFSET 10000) OR created_at < ?`, store.now().Add(-24*time.Hour).UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	return tx.Commit()
}
