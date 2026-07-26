package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func (store *Store) GetPolicy(ctx context.Context, id contract.PolicyID) (storagecontract.PolicyRecord, error) {
	if err := id.Validate(); err != nil {
		return storagecontract.PolicyRecord{}, fmt.Errorf("%w: policy id is invalid", storagecontract.ErrInvalidArgument)
	}
	var document string
	if err := store.db.QueryRowContext(
		ctx,
		`SELECT document_json FROM policies WHERE id = ?`,
		id,
	).Scan(&document); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storagecontract.PolicyRecord{}, fmt.Errorf("%w: policy %q", storagecontract.ErrNotFound, id)
		}
		return storagecontract.PolicyRecord{}, fmt.Errorf("read policy: %w", err)
	}
	return decodePolicyRecord(string(id), []byte(document))
}

func (store *Store) ListPolicies(ctx context.Context) (storagecontract.PolicyPage, error) {
	rows, err := store.db.QueryContext(
		ctx,
		`SELECT id, document_json FROM policies WHERE id = ? ORDER BY id`,
		contract.DefaultPrivacyPolicyID,
	)
	if err != nil {
		return storagecontract.PolicyPage{}, fmt.Errorf("list policies: %w", err)
	}
	defer rows.Close()
	page := storagecontract.PolicyPage{Items: make([]storagecontract.PolicyRecord, 0, 1)}
	for rows.Next() {
		var id, document string
		if err := rows.Scan(&id, &document); err != nil {
			return storagecontract.PolicyPage{}, fmt.Errorf("scan policy: %w", err)
		}
		record, err := decodePolicyRecord(id, []byte(document))
		if err != nil {
			return storagecontract.PolicyPage{}, err
		}
		page.Items = append(page.Items, record)
	}
	if err := rows.Err(); err != nil {
		return storagecontract.PolicyPage{}, fmt.Errorf("iterate policies: %w", err)
	}
	if len(page.Items) != 1 {
		return storagecontract.PolicyPage{}, fmt.Errorf(
			"%w: fixed privacy policy is missing",
			storagecontract.ErrInvalidRecord,
		)
	}
	return page, nil
}

func (store *Store) UpdatePolicy(
	ctx context.Context,
	policy contract.Policy,
	expectedETag string,
) (record storagecontract.PolicyRecord, err error) {
	if expectedETag == "" {
		return record, fmt.Errorf("%w: expected ETag is required", storagecontract.ErrInvalidArgument)
	}
	if err := contract.ValidatePrivacyDefault(policy); err != nil {
		return record, fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	document, err := json.Marshal(policy)
	if err != nil {
		return record, fmt.Errorf("encode policy: %w", err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return record, fmt.Errorf("begin policy update: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	var currentDocument string
	if err = transaction.QueryRowContext(
		ctx,
		`SELECT document_json FROM policies WHERE id = ?`,
		policy.ID,
	).Scan(&currentDocument); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return record, fmt.Errorf("%w: policy %q", storagecontract.ErrNotFound, policy.ID)
		}
		return record, fmt.Errorf("read policy for update: %w", err)
	}
	if _, err = decodePolicyRecord(string(policy.ID), []byte(currentDocument)); err != nil {
		return record, err
	}
	if entityTag([]byte(currentDocument)) != expectedETag {
		return record, fmt.Errorf("%w: policy %q", storagecontract.ErrPrecondition, policy.ID)
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	if _, err = transaction.ExecContext(
		ctx,
		`UPDATE policies SET document_json = ?, updated_at = ? WHERE id = ?`,
		string(document),
		now,
		policy.ID,
	); err != nil {
		return record, fmt.Errorf("update policy: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return record, fmt.Errorf("commit policy update: %w", err)
	}
	return storagecontract.PolicyRecord{Policy: policy, ETag: entityTag(document)}, nil
}

func decodePolicyRecord(rowID string, document []byte) (storagecontract.PolicyRecord, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var policy contract.Policy
	if err := decoder.Decode(&policy); err != nil {
		return storagecontract.PolicyRecord{}, fmt.Errorf(
			"%w: decode policy %q",
			storagecontract.ErrInvalidRecord,
			rowID,
		)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return storagecontract.PolicyRecord{}, fmt.Errorf(
			"%w: decode policy %q",
			storagecontract.ErrInvalidRecord,
			rowID,
		)
	}
	if string(policy.ID) != rowID {
		return storagecontract.PolicyRecord{}, fmt.Errorf(
			"%w: policy row id does not match document id",
			storagecontract.ErrInvalidRecord,
		)
	}
	if err := contract.ValidatePrivacyDefault(policy); err != nil {
		return storagecontract.PolicyRecord{}, fmt.Errorf(
			"%w: policy %q violates the fixed privacy contract",
			storagecontract.ErrInvalidRecord,
			rowID,
		)
	}
	return storagecontract.PolicyRecord{Policy: policy, ETag: entityTag(document)}, nil
}
