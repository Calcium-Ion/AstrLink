package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
)

func (store *Store) builtinCredential(ctx context.Context, ref secretstore.Ref, operation string, secret []byte) ([]byte, error) {
	kind := strings.TrimPrefix(string(ref), "local://builtin-tool/")
	if !contract.BuiltinToolKind(kind) {
		return nil, fmt.Errorf("invalid tool credential reference")
	}
	switch operation {
	case "put":
		if err := validateCredential(secret); err != nil {
			return nil, err
		}
		_, err := store.db.ExecContext(ctx, `INSERT INTO builtin_tool_credentials(kind, credential_value) VALUES (?, ?) ON CONFLICT(kind) DO UPDATE SET credential_value=excluded.credential_value`, kind, secret)
		return nil, err
	case "delete":
		_, err := store.db.ExecContext(ctx, `DELETE FROM builtin_tool_credentials WHERE kind=?`, kind)
		return nil, err
	default:
		var value []byte
		err := store.db.QueryRowContext(ctx, `SELECT credential_value FROM builtin_tool_credentials WHERE kind=?`, kind).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, secretstore.ErrNotFound
		}
		return value, err
	}
}
