package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"time"

	sqlitedriver "modernc.org/sqlite"
)

// SQLite's busy handler sleeps inside the driver and does not promptly notice
// context cancellation. Queue this pool's writers before entering SQLite, then
// retry short BUSY waits for other pools/processes within the original budget.
// Reads keep using the WAL concurrently, including while a writer is queued.
func openWriterDatabase(dsn string) *sql.DB {
	return sql.OpenDB(&writerConnector{dsn: dsn, gate: make(chan struct{}, 1)})
}

type writerConnector struct {
	dsn  string
	gate chan struct{}
	drv  sqlitedriver.Driver
}

func (connector *writerConnector) Driver() driver.Driver { return &connector.drv }

func (connector *writerConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := retrySQLiteBusy(ctx, func() (driver.Conn, error) {
		return connector.drv.Open(connector.dsn)
	})
	if err != nil {
		return nil, err
	}
	return &writerConn{Conn: conn, gate: connector.gate}, nil
}

type writerConn struct {
	driver.Conn
	gate          chan struct{}
	inTransaction bool
}

func (conn *writerConn) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case conn.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (conn *writerConn) Begin() (driver.Tx, error) {
	return conn.BeginTx(context.Background(), driver.TxOptions{})
}

func (conn *writerConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	if options.ReadOnly {
		tx, err := conn.Conn.(driver.ConnBeginTx).BeginTx(ctx, options)
		if err != nil {
			return nil, err
		}
		conn.inTransaction = true
		return &writerTx{Tx: tx, conn: conn, ctx: ctx}, nil
	}
	if err := conn.acquire(ctx); err != nil {
		return nil, err
	}
	tx, err := retrySQLiteBusy(ctx, func() (driver.Tx, error) {
		return conn.Conn.(driver.ConnBeginTx).BeginTx(ctx, options)
	})
	if err != nil {
		<-conn.gate
		return nil, err
	}
	conn.inTransaction = true
	return &writerTx{Tx: tx, conn: conn, ctx: ctx, ownsWriter: true}, nil
}

func (conn *writerConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return conn.exec(ctx, singleSQLiteStatement(query), func() (driver.Result, error) {
		return conn.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
	})
}

func (conn *writerConn) exec(ctx context.Context, canRetry bool, run func() (driver.Result, error)) (driver.Result, error) {
	if conn.inTransaction {
		// BEGIN IMMEDIATE already owns the writer lock. Do not replay any
		// transaction statement, which could otherwise repeat partial work.
		return run()
	}
	if err := conn.acquire(ctx); err != nil {
		return nil, err
	}
	defer func() { <-conn.gate }()
	if !canRetry {
		// A SQL batch can commit an earlier statement before a later one
		// encounters BUSY. Replaying it would duplicate that committed work.
		return run()
	}
	return retrySQLiteBusy(ctx, run)
}

func (conn *writerConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	// Store queries are reads (including PRAGMA). Writes use Exec or a
	// transaction; do not turn an unclassified query/RETURNING into a retry.
	return conn.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (conn *writerConn) Prepare(query string) (driver.Stmt, error) {
	return conn.PrepareContext(context.Background(), query)
}

func (conn *writerConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	statement, err := conn.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &writerStmt{Stmt: statement, conn: conn, canRetry: singleSQLiteStatement(query)}, nil
}

func (conn *writerConn) Ping(ctx context.Context) error {
	return conn.Conn.(driver.Pinger).Ping(ctx)
}

func (conn *writerConn) ResetSession(ctx context.Context) error {
	return conn.Conn.(driver.SessionResetter).ResetSession(ctx)
}

func (conn *writerConn) IsValid() bool {
	return conn.Conn.(driver.Validator).IsValid()
}

type writerTx struct {
	driver.Tx
	conn       *writerConn
	ctx        context.Context
	ownsWriter bool
}

func (tx *writerTx) release() {
	tx.conn.inTransaction = false
	if tx.ownsWriter {
		<-tx.conn.gate
	}
}

func (tx *writerTx) Commit() error {
	defer tx.release()
	_, err := retrySQLiteBusy(tx.ctx, func() (struct{}, error) {
		return struct{}{}, tx.Tx.Commit()
	})
	if err != nil {
		// database/sql considers a failed Commit terminal. SQLite may still
		// own the transaction (for example SQLITE_BUSY), so release it before
		// returning this connection to the pool and admitting another writer.
		_ = tx.Tx.Rollback()
	}
	return err
}

func (tx *writerTx) Rollback() error {
	defer tx.release()
	return tx.Tx.Rollback()
}

type writerStmt struct {
	driver.Stmt
	conn     *writerConn
	canRetry bool
}

func (statement *writerStmt) Exec(args []driver.Value) (driver.Result, error) {
	return statement.conn.exec(context.Background(), statement.canRetry, func() (driver.Result, error) {
		return statement.Stmt.Exec(args)
	})
}

func (statement *writerStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return statement.conn.exec(ctx, statement.canRetry, func() (driver.Result, error) {
		return statement.Stmt.(driver.StmtExecContext).ExecContext(ctx, args)
	})
}

// This only decides whether replay is safe, not whether SQL is valid. Quoted
// semicolons and trailing comments are harmless; trigger bodies are deliberately
// treated as batches (migrations already execute them inside a transaction).
func singleSQLiteStatement(query string) bool {
	if !strings.Contains(query, ";") {
		return true
	}
	terminated := false
	for i := 0; i < len(query); {
		switch {
		case strings.ContainsRune(" \t\r\n\f", rune(query[i])):
			i++
		case strings.HasPrefix(query[i:], "--"):
			end := strings.IndexByte(query[i:], '\n')
			if end < 0 {
				return true
			}
			i += end + 1
		case strings.HasPrefix(query[i:], "/*"):
			end := strings.Index(query[i+2:], "*/")
			if end < 0 {
				return false
			}
			i += end + 4
		case terminated:
			return false
		case query[i] == ';':
			terminated = true
			i++
		case strings.ContainsRune("'\"`[", rune(query[i])):
			quote := query[i]
			if quote == '[' {
				quote = ']'
			}
			i++
			closed := false
			for i < len(query) {
				if query[i] != quote {
					i++
					continue
				}
				i++
				if quote != ']' && i < len(query) && query[i] == quote {
					i++
					continue
				}
				closed = true
				break
			}
			if !closed {
				return false
			}
		default:
			i++
		}
	}
	return true
}

func (statement *writerStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return statement.Stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
}

func retrySQLiteBusy[T any](ctx context.Context, run func() (T, error)) (T, error) {
	var zero T
	deadline := time.Now().Add(time.Duration(busyTimeoutMS) * time.Millisecond)
	for {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		value, err := run()
		var sqliteErr *sqlitedriver.Error
		// A snapshot conflict requires restarting the whole transaction;
		// retrying the same statement cannot resolve it.
		if !errors.As(err, &sqliteErr) || sqliteErr.Code()&255 != 5 || sqliteErr.Code() == 517 {
			return value, err
		}
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return value, err
		}
		timer := time.NewTimer(min(remaining, 10*time.Millisecond))
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
}
