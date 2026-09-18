package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type orderQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readServiceOrder(ctx context.Context, query orderQuerier) (storage.ServiceOrderRecord, error) {
	order := contract.ServiceOrder{ServiceIDs: []contract.ServiceID{}}
	rows, err := query.QueryContext(ctx, `SELECT id FROM services ORDER BY sort_position, id`)
	if err != nil {
		return storage.ServiceOrderRecord{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id contract.ServiceID
		if err := rows.Scan(&id); err != nil {
			return storage.ServiceOrderRecord{}, err
		}
		order.ServiceIDs = append(order.ServiceIDs, id)
	}
	if err := rows.Err(); err != nil {
		return storage.ServiceOrderRecord{}, err
	}
	document, _ := json.Marshal(order)
	return storage.ServiceOrderRecord{Order: order, ETag: entityTag(document)}, nil
}

func (store *Store) GetServiceOrder(ctx context.Context) (storage.ServiceOrderRecord, error) {
	return readServiceOrder(ctx, store.db)
}

func (store *Store) UpdateServiceOrder(ctx context.Context, order contract.ServiceOrder, expected string) (record storage.ServiceOrderRecord, err error) {
	if expected == "" || order.ServiceIDs == nil {
		return record, fmt.Errorf("%w: ETag and service_ids are required", storage.ErrInvalidArgument)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return record, err
	}
	defer rollbackOnError(tx, &err)
	// Acquire the SQLite write lock before reading, so concurrent reorders and
	// service creates/deletes are compared against the committed current list.
	if _, err = tx.ExecContext(ctx, `UPDATE services SET sort_position = sort_position WHERE 0`); err != nil {
		return record, err
	}
	current, err := readServiceOrder(ctx, tx)
	if err != nil {
		return record, err
	}
	if current.ETag != expected {
		return record, fmt.Errorf("%w: service order changed; reload the list", storage.ErrPrecondition)
	}
	remaining := make(map[contract.ServiceID]bool, len(current.Order.ServiceIDs))
	for _, id := range current.Order.ServiceIDs {
		remaining[id] = true
	}
	if len(order.ServiceIDs) != len(remaining) {
		return record, fmt.Errorf("%w: service_ids must contain every service", storage.ErrInvalidArgument)
	}
	for _, id := range order.ServiceIDs {
		if !remaining[id] {
			return record, fmt.Errorf("%w: duplicate or unknown service ID", storage.ErrInvalidArgument)
		}
		delete(remaining, id)
	}
	for position, id := range order.ServiceIDs {
		if _, err = tx.ExecContext(ctx, `UPDATE services SET sort_position = ? WHERE id = ?`, position, id); err != nil {
			return record, err
		}
	}
	if err = tx.Commit(); err != nil {
		return record, err
	}
	document, _ := json.Marshal(order)
	return storage.ServiceOrderRecord{Order: order, ETag: entityTag(document)}, nil
}
