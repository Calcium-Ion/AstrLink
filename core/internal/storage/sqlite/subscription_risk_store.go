package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

// subscriptionRiskEventRetention bounds the history kept per account.
const subscriptionRiskEventRetention = 50

func (store *Store) AppendSubscriptionRiskEvent(ctx context.Context, event contract.SubscriptionRiskEvent) (err error) {
	event.ID = 0
	event.ObservedAt = event.ObservedAt.UTC()
	if err = event.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	document, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackOnError(tx, &err)
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO subscription_risk_events (service_id, observed_at, document_json) VALUES (?, ?, ?)`,
		event.ServiceID, bindingTime(event.ObservedAt), string(document),
	); err != nil {
		return fmt.Errorf("append subscription risk event: %w", err)
	}
	if _, err = tx.ExecContext(ctx,
		`DELETE FROM subscription_risk_events WHERE service_id = ? AND id NOT IN (
 SELECT id FROM subscription_risk_events WHERE service_id = ? ORDER BY id DESC LIMIT ?)`,
		event.ServiceID, event.ServiceID, subscriptionRiskEventRetention,
	); err != nil {
		return fmt.Errorf("trim subscription risk events: %w", err)
	}
	return tx.Commit()
}

func (store *Store) ListSubscriptionRiskEvents(ctx context.Context, id contract.ServiceID, limit int) ([]contract.SubscriptionRiskEvent, error) {
	if err := id.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	if limit <= 0 || limit > subscriptionRiskEventRetention {
		limit = subscriptionRiskEventRetention
	}
	rows, err := store.db.QueryContext(ctx,
		`SELECT id, document_json FROM subscription_risk_events WHERE service_id = ? ORDER BY id DESC LIMIT ?`, id, limit)
	if err != nil {
		return nil, fmt.Errorf("list subscription risk events: %w", err)
	}
	defer rows.Close()
	events := []contract.SubscriptionRiskEvent{}
	for rows.Next() {
		var eventID int64
		var raw string
		if err := rows.Scan(&eventID, &raw); err != nil {
			return nil, err
		}
		var event contract.SubscriptionRiskEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return nil, fmt.Errorf("%w: %v", storagecontract.ErrInvalidRecord, err)
		}
		event.ID = eventID
		events = append(events, event)
	}
	return events, rows.Err()
}

func (store *Store) CountSubscriptionRiskEvents(ctx context.Context, id contract.ServiceID, codes []string, since time.Time) (int, error) {
	if len(codes) == 0 {
		return 0, nil
	}
	arguments := []any{id, bindingTime(since)}
	for _, code := range codes {
		arguments = append(arguments, code)
	}
	var count int
	err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM subscription_risk_events WHERE service_id = ? AND observed_at >= ? AND json_extract(document_json, '$.code') IN (`+
			strings.TrimSuffix(strings.Repeat("?,", len(codes)), ",")+`)`,
		arguments...,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count subscription risk events: %w", err)
	}
	return count, nil
}
