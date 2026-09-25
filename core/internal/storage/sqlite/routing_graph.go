package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type graphQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readGraph(ctx context.Context, db graphQuery) (contract.RoutingGraphDocument, error) {
	doc := contract.RoutingGraphDocument{Active: contract.EmptyRoutingGraph(), Layout: contract.RoutingLayout{}, History: []contract.RoutingGraphRevision{}}
	var draft, layout string
	if err := db.QueryRowContext(ctx, `SELECT draft_json,layout_json,active_revision FROM model_routing_graph WHERE id=1`).Scan(&draft, &layout, &doc.Revision); err != nil {
		return doc, err
	}
	if json.Unmarshal([]byte(draft), &doc.Draft) != nil || json.Unmarshal([]byte(layout), &doc.Layout) != nil {
		return doc, fmt.Errorf("%w: routing graph draft", storage.ErrInvalidRecord)
	}
	if err := doc.Draft.ValidateDraft(doc.Layout); err != nil {
		return doc, fmt.Errorf("%w: %v", storage.ErrInvalidRecord, err)
	}
	if doc.Revision > 0 {
		var active string
		if err := db.QueryRowContext(ctx, `SELECT document_json FROM model_routing_revisions WHERE revision=?`, doc.Revision).Scan(&active); err != nil {
			return doc, err
		}
		if json.Unmarshal([]byte(active), &doc.Active) != nil {
			return doc, fmt.Errorf("%w: active routing graph", storage.ErrInvalidRecord)
		}
		if err := doc.Active.Validate(); err != nil {
			return doc, fmt.Errorf("%w: %v", storage.ErrInvalidRecord, err)
		}
	}
	raw, _ := json.Marshal([]any{doc.Draft, doc.Layout, doc.Revision})
	doc.ETag = entityTag(raw)
	rows, err := db.QueryContext(ctx, `SELECT revision,created_at FROM model_routing_revisions ORDER BY revision DESC LIMIT 20`)
	if err != nil {
		return doc, err
	}
	defer rows.Close()
	for rows.Next() {
		var revision contract.RoutingGraphRevision
		if err := rows.Scan(&revision.Revision, &revision.CreatedAt); err != nil {
			return doc, err
		}
		doc.History = append(doc.History, revision)
	}
	return doc, rows.Err()
}

func (store *Store) GetRoutingGraph(ctx context.Context) (contract.RoutingGraphDocument, error) {
	return readGraph(ctx, store.db)
}

func (store *Store) GetActiveRoutingGraph(ctx context.Context) (contract.RoutingGraph, int64, error) {
	graph := contract.EmptyRoutingGraph()
	var revision int64
	var raw string
	err := store.db.QueryRowContext(ctx, `SELECT g.active_revision,COALESCE(r.document_json,'{"nodes":[],"edges":[]}') FROM model_routing_graph g LEFT JOIN model_routing_revisions r ON r.revision=g.active_revision WHERE g.id=1`).Scan(&revision, &raw)
	if err != nil {
		return graph, 0, err
	}
	if json.Unmarshal([]byte(raw), &graph) != nil {
		return graph, 0, fmt.Errorf("%w: routing graph", storage.ErrInvalidRecord)
	}
	if err := graph.Validate(); err != nil {
		return graph, 0, fmt.Errorf("%w: %v", storage.ErrInvalidRecord, err)
	}
	return graph, revision, nil
}

func (store *Store) GetRoutingGraphRevision(ctx context.Context, revision int64) (contract.RoutingGraph, error) {
	var raw string
	graph := contract.EmptyRoutingGraph()
	if err := store.db.QueryRowContext(ctx, `SELECT document_json FROM model_routing_revisions WHERE revision=?`, revision).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return graph, storage.ErrNotFound
		}
		return graph, err
	}
	if err := json.Unmarshal([]byte(raw), &graph); err != nil {
		return graph, fmt.Errorf("%w: routing graph revision", storage.ErrInvalidRecord)
	}
	return graph, graph.Validate()
}

func (store *Store) SaveRoutingGraph(ctx context.Context, graph contract.RoutingGraph, layout contract.RoutingLayout, expected string, apply bool) (result contract.RoutingGraphDocument, err error) {
	if err = graph.ValidateDraft(layout); err != nil {
		return result, fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
	}
	if expected == "" {
		return result, storage.ErrPrecondition
	}
	if apply {
		if err = graph.Validate(); err != nil {
			return result, fmt.Errorf("%w: %v", storage.ErrInvalidArgument, err)
		}
	}
	if graph.Nodes == nil {
		graph.Nodes = []contract.RoutingGraphNode{}
	}
	if graph.Edges == nil {
		graph.Edges = []contract.RoutingGraphEdge{}
	}
	if layout == nil {
		layout = contract.RoutingLayout{}
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer rollbackOnError(tx, &err)
	if _, err = tx.ExecContext(ctx, `UPDATE model_routing_graph SET id=id WHERE id=1`); err != nil {
		return result, err
	}
	current, err := readGraph(ctx, tx)
	if err != nil {
		return result, err
	}
	if current.ETag != expected {
		return result, fmt.Errorf("%w: routing graph changed; reload before saving", storage.ErrPrecondition)
	}
	raw, _ := json.Marshal(graph)
	positions, _ := json.Marshal(layout)
	revision := current.Revision
	if apply {
		insert, insertErr := tx.ExecContext(ctx, `INSERT INTO model_routing_revisions(document_json,created_at) VALUES(?,?)`, string(raw), store.now().UTC().Format(time.RFC3339Nano))
		if insertErr != nil {
			return result, insertErr
		}
		revision, err = insert.LastInsertId()
		if err != nil {
			return result, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE model_routing_graph SET draft_json=?,layout_json=?,active_revision=? WHERE id=1`, string(raw), string(positions), revision); err != nil {
		return result, err
	}
	result, err = readGraph(ctx, tx)
	if err != nil {
		return result, err
	}
	err = tx.Commit()
	return result, err
}

func (store *Store) GetRoutingQuota(ctx context.Context) (map[contract.ServiceID]contract.SubscriptionUsage, error) {
	result := map[contract.ServiceID]contract.SubscriptionUsage{}
	rows, err := store.db.QueryContext(ctx, `SELECT q.document_json FROM model_routing_quota q JOIN services s ON s.id=q.service_id`)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return result, err
		}
		var usage contract.SubscriptionUsage
		if json.Unmarshal([]byte(raw), &usage) == nil && usage.Validate() == nil {
			result[usage.ServiceID] = usage
		}
	}
	return result, rows.Err()
}
