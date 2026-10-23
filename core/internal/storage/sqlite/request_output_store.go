package sqlite

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

// UpdateRequestOutput refreshes what a running call's response is producing
// without rewriting the rest of its row, so the stream can report progress
// from wherever it is read. A call that has ended keeps the output its final
// snapshot wrote.
func (store *Store) UpdateRequestOutput(
	ctx context.Context,
	id contract.RequestID,
	output contract.OutputActivity,
) error {
	if err := output.Validate(); err != nil {
		return fmt.Errorf("%w: %v", storagecontract.ErrInvalidArgument, err)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("encode output: %w", err)
	}
	if _, err := store.db.ExecContext(ctx,
		`UPDATE request_records SET output_json = ? WHERE id = ? AND status = 'pending'`,
		string(encoded), string(id),
	); err != nil {
		return fmt.Errorf("update request output: %w", err)
	}
	return nil
}
