package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

// standInTimeLayout is fixed width so issued_at sorts as text; RFC3339Nano
// drops trailing zeros and would put .1Z after .12Z.
const standInTimeLayout = "2006-01-02T15:04:05.000000000Z"

func (store *Store) PrivacyStandIns(ctx context.Context, session contract.SessionID) ([]storagecontract.PrivacyStandIn, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT stand_in, owner FROM privacy_stand_ins
WHERE session_id = ? ORDER BY issued_at, rowid`, string(session))
	if err != nil {
		return nil, fmt.Errorf("read privacy stand-ins: %w", err)
	}
	defer rows.Close()
	var standIns []storagecontract.PrivacyStandIn
	for rows.Next() {
		var standIn storagecontract.PrivacyStandIn
		if err := rows.Scan(&standIn.StandIn, &standIn.Owner); err != nil {
			return nil, fmt.Errorf("read privacy stand-in: %w", err)
		}
		standIns = append(standIns, standIn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read privacy stand-ins: %w", err)
	}
	return standIns, nil
}

func (store *Store) RememberPrivacyStandIns(ctx context.Context, session contract.SessionID, standIns []storagecontract.PrivacyStandIn) (err error) {
	if session == "" {
		return fmt.Errorf("%w: session id is required", storagecontract.ErrInvalidArgument)
	}
	if len(standIns) == 0 {
		return nil
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin privacy stand-ins: %w", err)
	}
	defer rollbackOnError(transaction, &err)
	issued := store.now().UTC().Format(standInTimeLayout)
	for _, standIn := range standIns {
		if _, err = transaction.ExecContext(ctx, `INSERT INTO privacy_stand_ins (session_id, stand_in, owner, issued_at)
VALUES (?, ?, ?, ?) ON CONFLICT(session_id, stand_in) DO NOTHING`,
			string(session), standIn.StandIn, standIn.Owner, issued); err != nil {
			return fmt.Errorf("write privacy stand-in: %w", err)
		}
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit privacy stand-ins: %w", err)
	}
	return nil
}

// deleteOrphanedStandIns drops the stand-ins of conversations that have no
// request record left, since nothing can continue them.
func deleteOrphanedStandIns(ctx context.Context, transaction *sql.Tx) error {
	if _, err := transaction.ExecContext(ctx, `DELETE FROM privacy_stand_ins WHERE session_id NOT IN (
    SELECT COALESCE(session_id, id) FROM request_records WHERE parent_request_id IS NULL
)`); err != nil {
		return fmt.Errorf("delete orphaned privacy stand-ins: %w", err)
	}
	return nil
}
