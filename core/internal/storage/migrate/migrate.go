// Package migrate provides a forward-only, transactional SQLite migration
// runner without choosing a SQLite driver. A later storage package can inject
// any database/sql-compatible driver through SQLDatabase.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrDatabaseNewer = errors.New("database schema is newer than this core")

const createMigrationsTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at TEXT NOT NULL
)`

type Migration struct {
	Version    int64
	Name       string
	Statements []string
}

type Row interface {
	Scan(...any) error
}

type Transaction interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) Row
	Commit() error
	Rollback() error
}

type Database interface {
	Begin(context.Context) (Transaction, error)
}

// SQLDatabase adapts *sql.DB to Database while leaving driver selection to the
// executable that will own persistent storage in a later milestone.
type SQLDatabase struct {
	DB *sql.DB
}

func (database SQLDatabase) Begin(ctx context.Context) (Transaction, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database is nil")
	}
	transaction, err := database.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return sqlTransaction{Tx: transaction}, nil
}

type sqlTransaction struct {
	*sql.Tx
}

func (transaction sqlTransaction) QueryRowContext(ctx context.Context, query string, args ...any) Row {
	return transaction.Tx.QueryRowContext(ctx, query, args...)
}

type Runner struct {
	database   Database
	migrations []Migration
	now        func() time.Time
}

func New(database Database, migrations []Migration) (*Runner, error) {
	if database == nil {
		return nil, fmt.Errorf("database is required")
	}
	copyOfMigrations := append([]Migration(nil), migrations...)
	if err := validate(copyOfMigrations); err != nil {
		return nil, err
	}
	return &Runner{
		database:   database,
		migrations: copyOfMigrations,
		now:        time.Now,
	}, nil
}

func validate(migrations []Migration) error {
	var previous int64
	for index, migration := range migrations {
		if migration.Version <= 0 {
			return fmt.Errorf("migrations[%d]: version must be positive", index)
		}
		if migration.Version <= previous {
			return fmt.Errorf("migrations[%d]: versions must be strictly increasing", index)
		}
		if strings.TrimSpace(migration.Name) == "" {
			return fmt.Errorf("migrations[%d]: name is required", index)
		}
		if len(migration.Statements) == 0 {
			return fmt.Errorf("migrations[%d]: at least one statement is required", index)
		}
		for statementIndex, statement := range migration.Statements {
			if strings.TrimSpace(statement) == "" {
				return fmt.Errorf("migrations[%d].statements[%d]: statement is empty", index, statementIndex)
			}
		}
		previous = migration.Version
	}
	return nil
}

func (runner *Runner) Up(ctx context.Context) (err error) {
	transaction, err := runner.database.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = transaction.Rollback()
		}
	}()

	if _, err = transaction.ExecContext(ctx, createMigrationsTable); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	var currentVersion int64
	row := transaction.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`)
	if err = row.Scan(&currentVersion); err != nil {
		return fmt.Errorf("read current schema version: %w", err)
	}
	var latestVersion int64
	if len(runner.migrations) > 0 {
		latestVersion = runner.migrations[len(runner.migrations)-1].Version
	}
	if currentVersion > latestVersion {
		return fmt.Errorf("%w: database=%d core=%d", ErrDatabaseNewer, currentVersion, latestVersion)
	}

	for _, migration := range runner.migrations {
		if migration.Version <= currentVersion {
			continue
		}
		for _, statement := range migration.Statements {
			if _, err = transaction.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply migration %d (%s): %w", migration.Version, migration.Name, err)
			}
		}
		appliedAt := runner.now().UTC().Format(time.RFC3339Nano)
		if _, err = transaction.ExecContext(
			ctx,
			`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			migration.Version,
			migration.Name,
			appliedAt,
		); err != nil {
			return fmt.Errorf("record migration %d (%s): %w", migration.Version, migration.Name, err)
		}
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}
