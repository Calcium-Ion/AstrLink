package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func writerTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "writer.db")
	store, err := Open(t.Context(), path, WithLocalKey(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func TestWriterAdmissionHonorsRequestDeadline(t *testing.T) {
	for _, operation := range []string{"request", "exec", "prepared"} {
		t.Run(operation, func(t *testing.T) {
			store, _ := writerTestStore(t)
			statement, err := store.db.Prepare(`UPDATE audit_settings SET id = id`)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			tx, err := store.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec(`UPDATE audit_settings SET id = id`); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "request":
					err = store.UpsertRequestRecord(ctx, contract.RequestRecord{
						ID: "request_waiting", StartedAt: time.Now().UTC(), Status: contract.RequestStatusPending,
						InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
					})
				case "exec":
					_, err = store.db.ExecContext(ctx, `UPDATE audit_settings SET id = id`)
				case "prepared":
					_, err = statement.ExecContext(ctx)
				}
				done <- err
			}()
			// A reader must still proceed while the transaction owns the writer
			// gate and the other operation is waiting to enter it.
			readCtx, stopRead := context.WithTimeout(t.Context(), time.Second)
			defer stopRead()
			if _, err := store.GetAuditSettings(readCtx); err != nil {
				t.Fatalf("read while writer held: %v", err)
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("blocked writer error = %v", err)
				}
			case <-time.After(time.Second):
				_ = tx.Rollback()
				t.Fatalf("writer ignored its 100ms deadline: %v", <-done)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if _, err := statement.ExecContext(readCtx); err != nil {
				t.Fatalf("cancelled waiter leaked writer gate: %v", err)
			}
		})
	}
}

func TestWriterCancellationReleasesActiveTransaction(t *testing.T) {
	store, _ := writerTestStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE audit_settings SET id = id`); err != nil {
		cancel()
		_ = tx.Rollback()
		t.Fatal(err)
	}
	cancel()
	// database/sql rolls back cancelled transactions asynchronously. A new
	// write must be admitted after that rollback, without a timer-based settle.
	next, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if _, err := store.db.ExecContext(next, `UPDATE audit_settings SET id = id`); err != nil {
		t.Fatalf("cancelled transaction leaked writer gate: %v", err)
	}
}

func TestReadOnlyTransactionDoesNotHoldWriterGate(t *testing.T) {
	store, _ := writerTestStore(t)
	read, err := store.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer read.Rollback()
	var count int
	if err := read.QueryRow(`SELECT COUNT(*) FROM audit_settings`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := store.db.ExecContext(ctx, `UPDATE audit_settings SET id = id`); err != nil {
		t.Fatalf("read-only transaction blocked writer: %v", err)
	}
	if err := read.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE audit_settings SET id = id`); err != nil {
		t.Fatalf("read-only commit damaged writer gate: %v", err)
	}
}

func TestWriterExternalBusyHonorsDeadlineAndRetries(t *testing.T) {
	store, path := writerTestStore(t)
	other, err := sql.Open(driverName, sqliteFileDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	tx, err := other.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE audit_settings SET id = id`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := store.db.ExecContext(ctx, `UPDATE audit_settings SET id = id`)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("external busy error = %v", err)
		}
	case <-time.After(time.Second):
		_ = tx.Rollback()
		t.Fatalf("external lock ignored 100ms deadline: %v", <-done)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(t.Context(), `UPDATE audit_settings SET id = id`); err != nil {
		t.Fatalf("write after external lock released: %v", err)
	}
}

func TestWriterDoesNotReplayPartiallyCommittedSQLBatch(t *testing.T) {
	store, _ := writerTestStore(t)
	otherPath := filepath.Join(t.TempDir(), "attached.db")
	other, err := sql.Open(driverName, sqliteFileDSN(otherPath))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Exec(`CREATE TABLE probe (value INTEGER)`); err != nil {
		t.Fatal(err)
	}
	conn, err := store.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(t.Context(), `ATTACH DATABASE ? AS busy`, otherPath); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), `CREATE TABLE replay_probe (value INTEGER)`); err != nil {
		t.Fatal(err)
	}
	tx, err := other.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err = conn.ExecContext(ctx, `INSERT INTO replay_probe(value) VALUES (1); INSERT INTO busy.probe(value) VALUES (1)`)
	if err == nil {
		t.Fatal("batch unexpectedly wrote through the other database's lock")
	}
	var count int
	if err := conn.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM replay_probe`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("committed prefix executed %d times, want once", count)
	}
}

func TestSQLiteStatementReplayBoundary(t *testing.T) {
	for _, test := range []struct {
		query string
		want  bool
	}{
		{`UPDATE x SET value = ?`, true},
		{`UPDATE x SET value = 'a;''b'; -- trailing ; comment`, true},
		{"/* leading ; comment */ INSERT INTO \"quoted;table\" VALUES (?);\n/* done */", true},
		{`INSERT INTO x VALUES (1); INSERT INTO x VALUES (2)`, false},
		{`INSERT INTO x VALUES (1); /* comment */ DELETE FROM x`, false},
		{`INSERT INTO x VALUES ('unterminated; )`, false},
	} {
		if got := singleSQLiteStatement(test.query); got != test.want {
			t.Errorf("single statement %q = %v, want %v", test.query, got, test.want)
		}
	}
}
