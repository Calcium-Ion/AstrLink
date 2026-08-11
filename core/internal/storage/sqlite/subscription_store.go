package sqlite

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func (store *Store) ListSubscriptionAccounts(ctx context.Context) ([]contract.SubscriptionAccount, error) {
	kind := contract.ServiceKindCodexSubscription
	page, err := store.ListServices(ctx, storagecontract.ServiceListOptions{Limit: maxListLimit, Kind: &kind})
	if err != nil {
		return nil, fmt.Errorf("list subscription accounts: %w", err)
	}
	items := make([]contract.SubscriptionAccount, 0, len(page.Items))
	for _, record := range page.Items {
		account, err := record.Service.SubscriptionAccountView()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", storagecontract.ErrInvalidRecord, err)
		}
		items = append(items, account)
	}
	return items, nil
}

func (store *Store) GetSubscriptionAccount(ctx context.Context, id contract.SubscriptionAccountID) (contract.SubscriptionAccount, error) {
	record, err := store.GetService(ctx, id)
	if err != nil {
		return contract.SubscriptionAccount{}, err
	}
	account, err := record.Service.SubscriptionAccountView()
	if err != nil {
		return contract.SubscriptionAccount{}, fmt.Errorf("%w: %v", storagecontract.ErrNotFound, err)
	}
	return account, nil
}

func (store *Store) PutSubscriptionAccount(ctx context.Context, account contract.SubscriptionAccount) error {
	if err := account.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	service := contract.ServiceFromSubscriptionAccount(account)
	service.Enabled = true
	if existing, err := store.GetService(ctx, account.ID); err == nil {
		if !existing.Service.Kind.IsSubscription() {
			return fmt.Errorf("%w: service %q has kind %q", storagecontract.ErrConflict, account.ID, existing.Service.Kind)
		}
		service.Enabled = existing.Service.Enabled
		service.Models = append([]string{}, existing.Service.Models...)
		service.CreatedAt = existing.Service.CreatedAt
	} else if !errors.Is(err, storagecontract.ErrNotFound) {
		return err
	}
	service.UpdatedAt = store.now().UTC()
	document, err := encodeService(service)
	if err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	_, err = store.db.ExecContext(
		ctx,
		`INSERT INTO services (id, document_json, created_at, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET document_json = excluded.document_json, updated_at = excluded.updated_at`,
		service.ID, string(document), service.CreatedAt.UTC().Format(time.RFC3339Nano), now,
	)
	if err != nil {
		return fmt.Errorf("upsert subscription service: %w", err)
	}
	return nil
}

func (store *Store) DeleteSubscriptionAccount(ctx context.Context, id contract.SubscriptionAccountID) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	result, err := store.db.ExecContext(ctx, `DELETE FROM services WHERE id = ? AND json_extract(document_json, '$.kind') = 'codex_subscription'`, id)
	if err != nil {
		return fmt.Errorf("delete subscription account: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("subscription delete rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: subscription account %q", storagecontract.ErrNotFound, id)
	}
	return nil
}

func containsSubscriptionSecret(document []byte) bool {
	lower := strings.ToLower(string(document))
	for _, needle := range []string{
		`"access_token"`,
		`"refresh_token"`,
		`"id_token"`,
		`"device_auth_id"`,
		`"code_verifier"`,
		`"authorization_code"`,
		"bearer ",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

var _ storagecontract.SubscriptionAccountStore = (*Store)(nil)
