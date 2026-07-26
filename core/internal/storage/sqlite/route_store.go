package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

// ListRoutes reads a strict, complete snapshot in stable row-ID order.
// StoreResolver applies request-specific priority and model-specific ordering
// after the complete snapshot has been validated.
func (store *Store) ListRoutes(ctx context.Context) ([]storagecontract.RouteRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id, document_json FROM routes ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list routes: %w", err)
	}
	defer rows.Close()

	records := make([]storagecontract.RouteRecord, 0)
	for rows.Next() {
		var id, document string
		if err := rows.Scan(&id, &document); err != nil {
			return nil, fmt.Errorf("scan route: %w", err)
		}
		record, err := decodeRouteRecord(id, []byte(document))
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate routes: %w", err)
	}
	return records, nil
}

func decodeRouteRecord(rowID string, document []byte) (storagecontract.RouteRecord, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var route contract.Route
	if err := decoder.Decode(&route); err != nil {
		return storagecontract.RouteRecord{}, fmt.Errorf(
			"%w: decode route %q: %v",
			storagecontract.ErrInvalidRecord,
			rowID,
			err,
		)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return storagecontract.RouteRecord{}, fmt.Errorf(
			"%w: decode route %q: %v",
			storagecontract.ErrInvalidRecord,
			rowID,
			err,
		)
	}
	if string(route.ID) != rowID {
		return storagecontract.RouteRecord{}, fmt.Errorf(
			"%w: route row id does not match document id",
			storagecontract.ErrInvalidRecord,
		)
	}
	if err := route.ValidateForAlpha(); err != nil {
		return storagecontract.RouteRecord{}, fmt.Errorf(
			"%w: route %q: %v",
			storagecontract.ErrInvalidRecord,
			rowID,
			err,
		)
	}
	return storagecontract.RouteRecord{Route: route, ETag: entityTag(document)}, nil
}

var _ storagecontract.RouteReader = (*Store)(nil)
