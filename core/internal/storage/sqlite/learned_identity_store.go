package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

// learnedIdentityProvider accepts the providers whose client identity is
// learned.
func learnedIdentityProvider(provider contract.SubscriptionProvider) error {
	switch provider {
	case contract.SubscriptionProviderClaudeCode, contract.SubscriptionProviderOpenAICodex:
		return nil
	}
	return fmt.Errorf("%w: unsupported learned identity provider", storagecontract.ErrInvalidArgument)
}

func (store *Store) GetLearnedIdentity(ctx context.Context, provider contract.SubscriptionProvider) ([]byte, error) {
	if err := learnedIdentityProvider(provider); err != nil {
		return nil, err
	}
	var document string
	err := store.db.QueryRowContext(ctx, `SELECT document_json FROM learned_client_identity WHERE provider = ?`, provider).Scan(&document)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storagecontract.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get learned client identity: %w", err)
	}
	return []byte(document), nil
}

// ListLearnedIdentities skips rows of providers this build does not learn.
func (store *Store) ListLearnedIdentities(ctx context.Context) (map[contract.SubscriptionProvider][]byte, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT provider, document_json FROM learned_client_identity`)
	if err != nil {
		return nil, fmt.Errorf("list learned client identities: %w", err)
	}
	defer rows.Close()
	documents := make(map[contract.SubscriptionProvider][]byte)
	for rows.Next() {
		var provider contract.SubscriptionProvider
		var document string
		if err := rows.Scan(&provider, &document); err != nil {
			return nil, err
		}
		if learnedIdentityProvider(provider) == nil {
			documents[provider] = []byte(document)
		}
	}
	return documents, rows.Err()
}

func (store *Store) PutLearnedIdentity(ctx context.Context, provider contract.SubscriptionProvider, document []byte) error {
	if err := learnedIdentityProvider(provider); err != nil {
		return err
	}
	if !json.Valid(document) {
		return fmt.Errorf("%w: learned client identity is not JSON", storagecontract.ErrInvalidArgument)
	}
	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO learned_client_identity (provider, document_json, updated_at) VALUES (?, ?, ?)
ON CONFLICT(provider) DO UPDATE SET document_json = excluded.document_json, updated_at = excluded.updated_at`,
		provider, string(document), store.now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("put learned client identity: %w", err)
	}
	return nil
}

var _ storagecontract.LearnedIdentityStore = (*Store)(nil)
