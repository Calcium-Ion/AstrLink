package sqlite

import (
	"bytes"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/testutil/sqliteschema"
)

func TestHistoryCacheObservesLocalExternalAndRolledBackWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history-cache.sqlite")
	store := openTestStore(t, path)
	defer store.Close()
	// Reusing one connection exercises total_changes, which data_version
	// deliberately does not increment for the connection's own writes.
	store.db.SetMaxOpenConns(1)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	options := storage.UsageSummaryOptions{From: start, To: start.AddDate(0, 0, 1), TimeZone: "UTC", Bucket: "day"}
	sid := contract.SessionID("session_cache")
	record := contract.RequestRecord{ID: "request_cache", SessionID: &sid, StartedAt: start,
		Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses,
		Usage: &contract.Usage{TotalTokens: 10}, Audit: contract.NotCapturedAuditSummary()}
	if err := store.InsertRequestRecord(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	check := func(tokens int64, calls int) {
		t.Helper()
		for range 2 {
			summary, err := store.GetUsageSummary(t.Context(), options)
			if err != nil || summary.Totals.TotalTokens != tokens {
				t.Fatalf("usage=%+v err=%v", summary, err)
			}
			page, err := store.ListRequestSessions(t.Context(), storage.RequestSessionListOptions{})
			if err != nil || len(page.Items) != calls {
				t.Fatalf("sessions=%+v err=%v", page, err)
			}
			if calls > 0 {
				// A caller may freely mutate its returned pointers and slices.
				page.Items[0].Title = "caller mutation"
				if again, err := store.ListRequestSessions(t.Context(), storage.RequestSessionListOptions{}); err != nil || again.Items[0].Title == "caller mutation" {
					t.Fatalf("cached value aliased: %+v, %v", again, err)
				}
			}
			if len(summary.ByService) > 0 {
				summary.ByService[0].TotalTokens = -1
			}
		}
	}
	check(10, 1)
	record.Usage.TotalTokens = 20
	if err := store.UpsertRequestRecord(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	check(20, 1)
	transaction, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(`UPDATE request_records SET usage_json = '{"total_tokens":99}'`); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	check(20, 1)
	// A second process/Store has no access to the cache's Go state.
	external, err := sql.Open(driverName, sqliteFileDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	if _, err := external.Exec(`UPDATE request_records SET usage_json = '{"total_tokens":30}', input_preview = 'external title'`); err != nil {
		t.Fatal(err)
	}
	check(30, 1)
	page, err := store.ListRequestSessions(t.Context(), storage.RequestSessionListOptions{})
	if err != nil || page.Items[0].Title != "external title" {
		t.Fatalf("external session update lost: %+v, %v", page, err)
	}
	if err := store.DeleteRequestRecord(t.Context(), record.ID); err != nil {
		t.Fatal(err)
	}
	check(0, 0)
}

func TestHistoryCacheSkipsSnapshotsChangedDuringRead(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "changing-history.sqlite"))
	defer store.Close()
	var cache historyCache
	if _, err := store.db.Exec(`CREATE TABLE cache_probe (value INTEGER)`); err != nil {
		t.Fatal(err)
	}
	loads := 0
	load := func(conn *sql.Conn) (int, error) {
		loads++
		// Any write on another connection invalidates the pinned snapshot.
		_, err := store.db.ExecContext(t.Context(), `INSERT INTO cache_probe VALUES (1)`)
		return loads, err
	}
	for want := 1; want <= 2; want++ {
		got, err := cachedHistoryRead(t.Context(), store.db, &cache, "same options", load)
		if err != nil || got != want {
			t.Fatalf("load=%d want=%d err=%v", got, want, err)
		}
	}
}

func TestHistoryCacheConcurrentReadersKeepWindowsAndValuesIndependent(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "concurrent-history.sqlite"))
	defer store.Close()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for day := range 2 {
		sid := contract.SessionID(fmt.Sprintf("session_day_%d", day))
		record := contract.RequestRecord{ID: contract.RequestID(fmt.Sprintf("request_day_%d", day)), SessionID: &sid,
			StartedAt: start.AddDate(0, 0, day), Status: contract.RequestStatusSucceeded,
			InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
			Usage: &contract.Usage{TotalTokens: 10 + day}}
		if err := store.InsertRequestRecord(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 8)
	for i := range 8 {
		go func() {
			day := i % 2
			from, to := start.AddDate(0, 0, day), start.AddDate(0, 0, day+1)
			for range 5 {
				usage, err := store.GetUsageSummary(t.Context(), storage.UsageSummaryOptions{From: from, To: to, TimeZone: "UTC", Bucket: "day"})
				if err != nil {
					done <- err
					return
				}
				if usage.Totals.TotalTokens != int64(10+day) || len(usage.ByDay) != 1 {
					done <- fmt.Errorf("wrong usage window: %+v", usage)
					return
				}
				usage.ByDay[0].TotalTokens = -1
				page, err := store.ListRequestSessions(t.Context(), storage.RequestSessionListOptions{From: &from, To: &to})
				if err != nil {
					done <- err
					return
				}
				if len(page.Items) != 1 || string(page.Items[0].ID) != fmt.Sprintf("session_day_%d", day) {
					done <- fmt.Errorf("wrong session window: %+v", page)
					return
				}
				page.Items[0].Title = "private caller copy"
			}
			done <- nil
		}()
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

func BenchmarkHistoryReadSnapshots(b *testing.B) {
	for _, kind := range []string{"usage", "sessions"} {
		for _, cached := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/cached_%t", kind, cached), func(b *testing.B) {
				count := 50000
				if kind == "sessions" {
					count = 20000
				}
				path := filepath.Join(b.TempDir(), "benchmark-history.sqlite")
				sqliteschema.Seed(b, path)
				store, err := Open(b.Context(), path, WithLocalKey(bytes.Repeat([]byte{0x41}, 32)))
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() { _ = store.Close() })
				start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
				sid := contract.SessionID("session_benchmark")
				completed, latency, firstToken, turn := start.Add(time.Second), 1000, 100, 1
				record := contract.RequestRecord{ID: "request_benchmark", SessionID: &sid, StartedAt: start,
					CompletedAt: &completed, Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses,
					Streaming: true, LatencyMs: &latency, FirstTokenMs: &firstToken, TurnIndex: &turn,
					Usage: &contract.Usage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120}, Audit: contract.NotCapturedAuditSummary()}
				row, err := encodeRequestRecordRow(record, start)
				if err != nil {
					b.Fatal(err)
				}
				tx, err := store.db.Begin()
				if err != nil {
					b.Fatal(err)
				}
				defer tx.Rollback()
				stmt, err := tx.Prepare(`INSERT INTO request_records (` + requestRecordInsertColumns + `) VALUES ` + requestRecordInsertValues)
				if err != nil {
					b.Fatal(err)
				}
				defer stmt.Close()
				for i := range count {
					args := row.insertArgs()
					args[0] = fmt.Sprintf("request_%07d", i)
					at := start.Add(time.Duration(i) * time.Second)
					args[3], args[4] = at.Format(time.RFC3339Nano), at.Add(time.Second).Format(time.RFC3339Nano)
					if _, err := stmt.Exec(args...); err != nil {
						b.Fatal(err)
					}
				}
				if err := tx.Commit(); err != nil {
					b.Fatal(err)
				}
				options := storage.UsageSummaryOptions{From: start, To: start.AddDate(0, 0, 1), TimeZone: "UTC", Bucket: "day"}
				read := func() error {
					if kind == "usage" {
						if cached {
							_, err := store.GetUsageSummary(b.Context(), options)
							return err
						}
						_, err := getUsageSummary(b.Context(), store.db, options)
						return err
					}
					if cached {
						_, err := store.ListRequestSessions(b.Context(), storage.RequestSessionListOptions{Limit: 20})
						return err
					}
					_, err := listRequestSessions(b.Context(), store.db, storage.RequestSessionListOptions{Limit: 20})
					return err
				}
				if err := read(); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if err := read(); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
