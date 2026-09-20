package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type pathQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readRecoveryPath(ctx context.Context, q pathQuery, id contract.RecoveryPathID) (contract.RecoveryPathRecord, error) {
	var document string
	err := q.QueryRowContext(ctx, `SELECT document_json FROM recovery_paths WHERE id=?`, id).Scan(&document)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.RecoveryPathRecord{}, fmt.Errorf("%w: recovery path %s", storage.ErrNotFound, id)
	}
	if err != nil {
		return contract.RecoveryPathRecord{}, err
	}
	return decodeRecoveryPath(id, document)
}

func decodeRecoveryPath(id contract.RecoveryPathID, document string) (contract.RecoveryPathRecord, error) {
	var path contract.RecoveryPath
	if err := json.Unmarshal([]byte(document), &path); err != nil {
		return contract.RecoveryPathRecord{}, fmt.Errorf("%w: recovery path", storage.ErrInvalidRecord)
	}
	if path.ID != id {
		return contract.RecoveryPathRecord{}, storage.ErrInvalidRecord
	}
	if err := path.Validate(); err != nil {
		return contract.RecoveryPathRecord{}, fmt.Errorf("%w: %v", storage.ErrInvalidRecord, err)
	}
	return contract.RecoveryPathRecord{Path: path, ETag: entityTag([]byte(document)), References: []contract.RecoveryPathReference{}}, nil
}
func pathReferences(ctx context.Context, q pathQuery, id contract.RecoveryPathID) ([]contract.RecoveryPathReference, error) {
	byPath, err := allPathReferences(ctx, q)
	if err != nil {
		return nil, err
	}
	refs := byPath[id]
	if refs == nil {
		refs = []contract.RecoveryPathReference{}
	}
	return refs, nil
}

func allPathReferences(ctx context.Context, q pathQuery) (map[contract.RecoveryPathID][]contract.RecoveryPathReference, error) {
	refs := make(map[contract.RecoveryPathID][]contract.RecoveryPathReference)
	rows, err := q.QueryContext(ctx, `SELECT document_json FROM routes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		var route contract.Route
		if err = json.Unmarshal([]byte(raw), &route); err != nil {
			rows.Close()
			return nil, err
		}
		add := func(id contract.RecoveryPathID, category string) {
			if id != "" {
				refs[id] = append(refs[id], contract.RecoveryPathReference{RouteID: route.ID, Name: route.Name, Protocol: route.Match.Protocol, CategoryID: category, Override: route.FailurePolicy != nil || route.Failover != nil})
			}
		}
		add(route.RecoveryPathID, "")
		for _, category := range route.Categories {
			add(category.RecoveryPathID, category.CategoryID)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var raw string
	if err = q.QueryRowContext(ctx, `SELECT document_json FROM routing_settings WHERE id=1`).Scan(&raw); err != nil {
		return nil, err
	}
	var settings contract.RoutingSettings
	if err = json.Unmarshal([]byte(raw), &settings); err != nil {
		return nil, err
	}
	for protocol, pathID := range settings.DefaultRecoveryPaths {
		if pathID != "" {
			refs[pathID] = append(refs[pathID], contract.RecoveryPathReference{Name: "没有匹配模型规则时", Protocol: protocol})
		}
	}
	return refs, nil
}
func (store *Store) ListRecoveryPaths(ctx context.Context) ([]contract.RecoveryPathRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id, document_json FROM recovery_paths ORDER BY id`)
	if err != nil {
		return nil, err
	}
	records := []contract.RecoveryPathRecord{}
	for rows.Next() {
		var id contract.RecoveryPathID
		var document string
		if err = rows.Scan(&id, &document); err != nil {
			rows.Close()
			return nil, err
		}
		record, err := decodeRecoveryPath(id, document)
		if err != nil {
			rows.Close()
			return nil, err
		}
		records = append(records, record)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return records, nil
	}
	byPath, err := allPathReferences(ctx, store.db)
	if err != nil {
		return nil, err
	}
	for i := range records {
		if refs := byPath[records[i].Path.ID]; refs != nil {
			records[i].References = refs
		}
	}
	return records, nil
}
func (store *Store) GetRecoveryPath(ctx context.Context, id contract.RecoveryPathID) (contract.RecoveryPathRecord, error) {
	record, err := readRecoveryPath(ctx, store.db, id)
	if err != nil {
		return record, err
	}
	record.References, err = pathReferences(ctx, store.db, id)
	return record, err
}
func (store *Store) CreateRecoveryPath(ctx context.Context, path contract.RecoveryPath) (contract.RecoveryPathRecord, error) {
	return store.saveRecoveryPath(ctx, path, "")
}
func (store *Store) UpdateRecoveryPath(ctx context.Context, path contract.RecoveryPath, etag string) (contract.RecoveryPathRecord, error) {
	if etag == "" {
		return contract.RecoveryPathRecord{}, storage.ErrPrecondition
	}
	return store.saveRecoveryPath(ctx, path, etag)
}
func (store *Store) saveRecoveryPath(ctx context.Context, path contract.RecoveryPath, etag string) (record contract.RecoveryPathRecord, err error) {
	if err = path.Validate(); err != nil {
		return record, fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return record, err
	}
	defer rollbackOnError(tx, &err)
	if etag != "" {
		current, e := readRecoveryPath(ctx, tx, path.ID)
		if e != nil {
			return record, e
		}
		if current.ETag != etag {
			return record, storage.ErrPrecondition
		}
	}
	if err = validatePathTargets(ctx, tx, path); err != nil {
		return record, err
	}
	refs, err := pathReferences(ctx, tx, path.ID)
	if err != nil {
		return record, err
	}
	for _, ref := range refs {
		if ref.RouteID == "" {
			if err = validateDefaultPath(path, ref.Protocol); err != nil {
				return record, err
			}
			continue
		}
		var raw string
		if err = tx.QueryRowContext(ctx, `SELECT document_json FROM routes WHERE id=?`, ref.RouteID).Scan(&raw); err != nil {
			return record, err
		}
		var route contract.Route
		if err = json.Unmarshal([]byte(raw), &route); err != nil {
			return record, err
		}
		if _, err = expandStoredRoutePaths(ctx, tx, route, &path); err != nil {
			return record, err
		}
	}
	document, err := json.Marshal(path)
	if err != nil {
		return record, err
	}
	if etag == "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO recovery_paths(id,document_json) VALUES(?,?)`, path.ID, string(document))
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE recovery_paths SET document_json=? WHERE id=?`, string(document), path.ID)
	}
	if err != nil {
		return record, err
	}
	if err = tx.Commit(); err != nil {
		return record, err
	}
	return contract.RecoveryPathRecord{Path: path, ETag: entityTag(document), References: refs}, nil
}
func (store *Store) DeleteRecoveryPath(ctx context.Context, id contract.RecoveryPathID, etag string) (err error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackOnError(tx, &err)
	record, err := readRecoveryPath(ctx, tx, id)
	if err != nil {
		return err
	}
	if record.ETag != etag {
		return storage.ErrPrecondition
	}
	refs, err := pathReferences(ctx, tx, id)
	if err != nil {
		return err
	}
	if len(refs) > 0 {
		return fmt.Errorf("%w: recovery path is still referenced", storage.ErrConflict)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM recovery_paths WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func validatePathTargets(ctx context.Context, q pathQuery, path contract.RecoveryPath) error {
	for _, node := range path.Nodes() {
		var raw string
		if err := q.QueryRowContext(ctx, `SELECT document_json FROM services WHERE id=?`, node.ServiceID).Scan(&raw); err != nil {
			return fmt.Errorf("%w: missing service %s", storage.ErrInvalidArgument, node.ServiceID)
		}
		record, err := decodeServiceRecord(string(node.ServiceID), []byte(raw))
		if err != nil {
			return err
		}
		if !serviceSupportsRouteTarget(record.Service, node.Target(0), node.UpstreamModel) {
			return fmt.Errorf("%w: unsupported target %s", storage.ErrInvalidArgument, node.ID)
		}
	}
	return nil
}
func validateDefaultPath(path contract.RecoveryPath, protocol contract.ProtocolID) error {
	if path.Protocol != protocol {
		return fmt.Errorf("%w: default path protocol mismatch", storage.ErrInvalidArgument)
	}
	for _, node := range path.Nodes() {
		if node.UpstreamModel != "" || node.UpstreamProtocol != protocol {
			return fmt.Errorf("%w: default paths must preserve the requested model and protocol", storage.ErrInvalidArgument)
		}
	}
	return nil
}
func expandStoredRoutePaths(ctx context.Context, q pathQuery, route contract.Route, replacement *contract.RecoveryPath) (contract.Route, error) {
	expand := func(id contract.RecoveryPathID) ([]contract.RouteTarget, error) {
		var path contract.RecoveryPath
		if replacement != nil && replacement.ID == id {
			path = *replacement
		} else {
			record, err := readRecoveryPath(ctx, q, id)
			if err != nil {
				return nil, fmt.Errorf("%w: missing recovery path %s", storage.ErrInvalidArgument, id)
			}
			path = record.Path
		}
		if path.Protocol != route.Match.Protocol {
			return nil, fmt.Errorf("%w: path protocol mismatch", storage.ErrInvalidArgument)
		}
		targets := make([]contract.RouteTarget, 0, len(path.Nodes()))
		for i, node := range path.Nodes() {
			targets = append(targets, node.Target(i))
		}
		return targets, nil
	}
	if route.RecoveryPathID != "" {
		targets, err := expand(route.RecoveryPathID)
		if err != nil {
			return route, err
		}
		route.Targets = targets
		route.RecoveryPathID = ""
	}
	route.Categories = append([]contract.RouteCategory(nil), route.Categories...)
	for i := range route.Categories {
		category := &route.Categories[i]
		if category.RecoveryPathID != "" {
			targets, err := expand(category.RecoveryPathID)
			if err != nil {
				return route, err
			}
			category.Targets = targets
			category.RecoveryPathID = ""
		}
	}
	if err := route.Validate(); err != nil {
		return route, fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	return route, nil
}
