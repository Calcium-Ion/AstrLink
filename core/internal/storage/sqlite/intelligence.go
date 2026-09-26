package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func (s *Store) LoadIntelligence(ctx context.Context, key string) (json.RawMessage, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT document_json FROM intelligence_documents WHERE id=?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	return json.RawMessage(raw), err
}
func (s *Store) SaveIntelligence(ctx context.Context, key, service, kind string, raw json.RawMessage) error {
	if !json.Valid(raw) || len(raw) > 80<<20 {
		return fmt.Errorf("invalid intelligence document")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO intelligence_documents(id,service_id,kind,document_json) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET document_json=excluded.document_json`, key, service, kind, string(raw))
	return err
}
func (s *Store) ListIntelligenceRuns(ctx context.Context, service string) ([]json.RawMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT document_json FROM intelligence_documents WHERE kind='run' AND service_id=? ORDER BY seq DESC LIMIT 20`, service)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(raw))
	}
	return out, rows.Err()
}
