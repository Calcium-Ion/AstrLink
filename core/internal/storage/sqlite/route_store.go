package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

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

func (store *Store) CreateRoute(ctx context.Context, route contract.Route) (record storagecontract.RouteRecord, err error) {
	if err := route.ValidateForAlpha(); err != nil {
		return record, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	document, err := json.Marshal(route)
	if err != nil {
		return record, fmt.Errorf("encode route: %w", err)
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return record, fmt.Errorf("begin route create: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	if err = validateRouteEndpointReferences(ctx, transaction, route); err != nil {
		return record, err
	}
	if _, err = transaction.ExecContext(
		ctx,
		`INSERT INTO routes (id, document_json, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		route.ID,
		string(document),
		now,
		now,
	); err != nil {
		var exists int
		if scanErr := transaction.QueryRowContext(
			ctx,
			`SELECT EXISTS(SELECT 1 FROM routes WHERE id = ?)`,
			route.ID,
		).Scan(&exists); scanErr == nil && exists == 1 {
			return record, fmt.Errorf("%w: route %q", storagecontract.ErrConflict, route.ID)
		}
		return record, fmt.Errorf("insert route: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return record, fmt.Errorf("commit route create: %w", err)
	}
	return storagecontract.RouteRecord{Route: route, ETag: entityTag(document)}, nil
}

func (store *Store) GetRoute(ctx context.Context, id contract.RouteID) (storagecontract.RouteRecord, error) {
	if err := id.Validate(); err != nil {
		return storagecontract.RouteRecord{}, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	var document string
	if err := store.db.QueryRowContext(
		ctx,
		`SELECT document_json FROM routes WHERE id = ?`,
		id,
	).Scan(&document); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storagecontract.RouteRecord{}, fmt.Errorf("%w: route %q", storagecontract.ErrNotFound, id)
		}
		return storagecontract.RouteRecord{}, fmt.Errorf("read route: %w", err)
	}
	return decodeRouteRecord(string(id), []byte(document))
}

func (store *Store) ListRoutePage(
	ctx context.Context,
	options storagecontract.RouteListOptions,
) (storagecontract.RoutePage, error) {
	limit := options.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	if limit < 1 || limit > maxListLimit {
		return storagecontract.RoutePage{}, fmt.Errorf(
			"%w: limit must be between 1 and %d",
			storagecontract.ErrInvalidArgument,
			maxListLimit,
		)
	}
	records, err := store.ListRoutes(ctx)
	if err != nil {
		return storagecontract.RoutePage{}, err
	}
	filtered := make([]storagecontract.RouteRecord, 0, len(records))
	for _, record := range records {
		if options.Enabled != nil && record.Route.Enabled != *options.Enabled {
			continue
		}
		filtered = append(filtered, record)
	}
	sort.Slice(filtered, func(left, right int) bool {
		a, b := filtered[left].Route, filtered[right].Route
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		aExact, bExact := a.Match.Model != "", b.Match.Model != ""
		if aExact != bExact {
			return aExact
		}
		return a.ID < b.ID
	})
	start := 0
	if options.Cursor != "" {
		cursorID, err := decodeRouteCursor(options.Cursor)
		if err != nil {
			return storagecontract.RoutePage{}, err
		}
		found := false
		for index, record := range filtered {
			if record.Route.ID == cursorID {
				start = index + 1
				found = true
				break
			}
		}
		if !found {
			return storagecontract.RoutePage{}, fmt.Errorf(
				"%w: route cursor no longer identifies a listed route",
				storagecontract.ErrInvalidCursor,
			)
		}
	}
	end := min(start+limit, len(filtered))
	page := storagecontract.RoutePage{
		Items: append([]storagecontract.RouteRecord(nil), filtered[start:end]...),
	}
	if end < len(filtered) {
		page.NextCursor = encodeRouteCursor(page.Items[len(page.Items)-1].Route.ID)
	}
	return page, nil
}

func (store *Store) UpdateRoute(
	ctx context.Context,
	route contract.Route,
	expectedETag string,
) (record storagecontract.RouteRecord, err error) {
	if expectedETag == "" {
		return record, fmt.Errorf("%w: expected ETag is required", storagecontract.ErrInvalidArgument)
	}
	if err := route.ValidateForAlpha(); err != nil {
		return record, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	document, err := json.Marshal(route)
	if err != nil {
		return record, fmt.Errorf("encode route: %w", err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return record, fmt.Errorf("begin route update: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	var currentDocument string
	if err = transaction.QueryRowContext(
		ctx,
		`SELECT document_json FROM routes WHERE id = ?`,
		route.ID,
	).Scan(&currentDocument); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return record, fmt.Errorf("%w: route %q", storagecontract.ErrNotFound, route.ID)
		}
		return record, fmt.Errorf("read route for update: %w", err)
	}
	if _, err = decodeRouteRecord(string(route.ID), []byte(currentDocument)); err != nil {
		return record, err
	}
	if entityTag([]byte(currentDocument)) != expectedETag {
		return record, fmt.Errorf("%w: route %q", storagecontract.ErrPrecondition, route.ID)
	}
	if err = validateRouteEndpointReferences(ctx, transaction, route); err != nil {
		return record, err
	}
	if _, err = transaction.ExecContext(
		ctx,
		`UPDATE routes SET document_json = ?, updated_at = ? WHERE id = ?`,
		string(document),
		store.now().UTC().Format(time.RFC3339Nano),
		route.ID,
	); err != nil {
		return record, fmt.Errorf("update route: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return record, fmt.Errorf("commit route update: %w", err)
	}
	return storagecontract.RouteRecord{Route: route, ETag: entityTag(document)}, nil
}

func (store *Store) DeleteRoute(ctx context.Context, id contract.RouteID, expectedETag string) (err error) {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	if expectedETag == "" {
		return fmt.Errorf("%w: expected ETag is required", storagecontract.ErrInvalidArgument)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin route delete: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	var document string
	if err = transaction.QueryRowContext(
		ctx,
		`SELECT document_json FROM routes WHERE id = ?`,
		id,
	).Scan(&document); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: route %q", storagecontract.ErrNotFound, id)
		}
		return fmt.Errorf("read route for delete: %w", err)
	}
	if _, err = decodeRouteRecord(string(id), []byte(document)); err != nil {
		return err
	}
	if entityTag([]byte(document)) != expectedETag {
		return fmt.Errorf("%w: route %q", storagecontract.ErrPrecondition, id)
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM routes WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete route: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit route delete: %w", err)
	}
	return nil
}

func validateRouteEndpointReferences(ctx context.Context, transaction *sql.Tx, route contract.Route) error {
	expanded, err := expandStoredRoutePaths(ctx, transaction, route, nil)
	if err != nil {
		return err
	}
	for index, target := range expanded.ExecutableTargets() {
		var document string
		if err := transaction.QueryRowContext(
			ctx,
			`SELECT document_json FROM services WHERE id = ?`,
			target.ServiceID,
		).Scan(&document); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf(
					"%w: targets[%d] references missing endpoint %q",
					storagecontract.ErrInvalidArgument,
					index,
					target.ServiceID,
				)
			}
			return fmt.Errorf("read route endpoint reference: %w", err)
		}
		record, err := decodeServiceRecord(string(target.ServiceID), []byte(document))
		if err != nil {
			return err
		}
		effectiveModel := target.UpstreamModel
		if effectiveModel == "" {
			effectiveModel = route.Match.Model
		}
		if !serviceSupportsRouteTarget(record.Service, target, effectiveModel) {
			return fmt.Errorf(
				"%w: targets[%d] is not supported by endpoint %q",
				storagecontract.ErrInvalidArgument,
				index,
				target.ServiceID,
			)
		}
	}
	return nil
}

func serviceSupportsRouteTarget(
	service contract.Service,
	target contract.RouteTarget,
	effectiveModel string,
) bool {
	mode := contract.CapabilityMode(target.PlanType)
	if target.PlanType == contract.PlanTypeRelayKit {
		mode = contract.CapabilityModeNative
	}
	modelSupported := effectiveModel == ""
	for _, model := range service.Models {
		if model == effectiveModel {
			modelSupported = true
			break
		}
	}
	if !modelSupported {
		return false
	}
	for _, capability := range service.Capabilities {
		if capability.Protocol != target.UpstreamProtocol || capability.Mode != mode {
			continue
		}
		return true
	}
	return false
}

func encodeRouteCursor(id contract.RouteID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

func decodeRouteCursor(cursor string) (contract.RouteID, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != cursor {
		return "", fmt.Errorf("%w: route cursor encoding", storagecontract.ErrInvalidCursor)
	}
	id := contract.RouteID(decoded)
	if err := id.Validate(); err != nil {
		return "", fmt.Errorf("%w: route cursor id", storagecontract.ErrInvalidCursor)
	}
	return id, nil
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

var _ storagecontract.RouteStore = (*Store)(nil)
