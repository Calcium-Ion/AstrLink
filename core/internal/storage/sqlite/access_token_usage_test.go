package sqlite

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestAccessTokenUsageCountsAllHistoryAndLocalDayWithoutRetryDuplicates(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	ctx := context.Background()
	today := time.Date(2026, 9, 19, 0, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	tokenA, tokenB := contract.AccessTokenID("token_a"), contract.AccessTokenID("token_b")
	base := contract.RequestRecord{
		StartedAt: today, CompletedAt: ptrTime(today.Add(time.Second)),
		Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses,
		LocalAccessTokenID: &tokenA, Audit: contract.NotCapturedAuditSummary(),
		Streaming: true, LatencyMs: ptrInt(1000), FirstTokenMs: ptrInt(200),
		Usage: &contract.Usage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3, CacheReadTokens: ptrInt(1)},
	}
	// This must exceed the former frontend cap of five pages of 200 records.
	for i := 0; i < 1005; i++ {
		record := base
		record.ID = contract.RequestID(fmt.Sprintf("request_%04d", i))
		record.StartedAt = today.Add(time.Duration(i) * time.Nanosecond)
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	before := base
	before.ID, before.StartedAt = "request_before", today.Add(-time.Nanosecond)
	other := base
	other.ID, other.LocalAccessTokenID = "request_other", &tokenB
	child := base
	parent := contract.RequestID("request_0000")
	child.ID, child.ParentRequestID, child.AttemptIndex = "request_child", &parent, 1
	failed := base
	failed.ID, failed.Status = "request_failed", contract.RequestStatusFailed
	failed.Error = &contract.ErrorSummary{Category: "upstream", Code: "failed", Message: "failed"}
	pending := base
	pending.ID, pending.Status, pending.CompletedAt = "request_pending", contract.RequestStatusPending, nil
	noToken := base
	noToken.ID, noToken.LocalAccessTokenID = "request_anonymous", nil
	noUsage := other
	noUsage.ID, noUsage.Usage = "request_no_usage", nil
	badHTTP := base
	httpStatus := 500
	badHTTP.ID, badHTTP.HTTPStatus = "request_bad_http", &httpStatus
	for _, record := range []contract.RequestRecord{before, other, child, failed, pending, noToken, noUsage, badHTTP} {
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	check := func(todayTokens, totalTokens int64) {
		t.Helper()
		items, err := store.ListAccessTokenUsage(ctx, today)
		if err != nil || len(items) != 2 {
			t.Fatalf("usage=%#v err=%v", items, err)
		}
		if items[0].TokenID != tokenA || items[0].TodayTokens != todayTokens || items[0].TotalTokens != totalTokens ||
			items[1].TokenID != tokenB || items[1].TodayTokens != 3 || items[1].TotalTokens != 3 {
			t.Fatalf("usage=%#v", items)
		}
		if items[0].TodayPerformance.CacheSamples != todayTokens/3 || items[0].TotalPerformance.SpeedSamples != totalTokens/3 ||
			items[0].TodayPerformance.CacheHitRate == nil || *items[0].TodayPerformance.CacheHitRate != 0.5 ||
			items[0].TotalPerformance.OutputTokensPerSecond == nil || *items[0].TotalPerformance.OutputTokensPerSecond != 1.25 {
			t.Fatalf("performance was capped or incorrectly counted: %+v", items[0])
		}
	}
	check(3015, 3018)
	// Updates and deletion are reflected immediately; no stale rollup remains.
	pending.Status, pending.CompletedAt = contract.RequestStatusSucceeded, base.CompletedAt
	if err := store.UpsertRequestRecord(ctx, pending); err != nil {
		t.Fatal(err)
	}
	check(3018, 3021)
	if err := store.DeleteRequestRecord(ctx, parent); err != nil {
		t.Fatal(err)
	}
	check(3015, 3018)
	if _, err := store.PurgeRequestRecords(ctx, contract.PurgeRequest{Scope: contract.PurgeScopeAll, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListAccessTokenUsage(ctx, today)
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("empty usage=%#v err=%v", items, err)
	}
}

func TestAccessTokenPerformanceAcrossProvidersAndPeriods(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "token-performance.db"))
	defer store.Close()
	today := time.Date(2026, 9, 25, 0, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	tokenA, tokenB := contract.AccessTokenID("token_a"), contract.AccessTokenID("token_b")
	serviceA, serviceB := contract.ServiceID("service_a"), contract.ServiceID("service_b")
	base := contract.RequestRecord{StartedAt: today, Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, LocalAccessTokenID: &tokenA, ServiceID: &serviceA,
		Streaming: true, LatencyMs: ptrInt(2000), FirstTokenMs: ptrInt(500),
		Usage: &contract.Usage{InputTokens: 100, CacheReadTokens: ptrInt(80), OutputTokens: 150, TotalTokens: 250}}
	insert := func(id string, mutate func(*contract.RequestRecord)) {
		t.Helper()
		r := base
		r.ID = contract.RequestID(id)
		u := *base.Usage
		r.Usage = &u
		if mutate != nil {
			mutate(&r)
		}
		if err := store.InsertRequestRecord(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	insert("request_first", nil)
	insert("request_second", func(r *contract.RequestRecord) {
		r.ServiceID = &serviceB
		r.Usage = &contract.Usage{InputTokens: 900, CacheReadTokens: ptrInt(180), OutputTokens: 90, TotalTokens: 990}
		r.LatencyMs, r.FirstTokenMs = ptrInt(4000), ptrInt(1000)
	})
	insert("request_old", func(r *contract.RequestRecord) {
		r.StartedAt = today.Add(-time.Nanosecond)
		r.Usage = &contract.Usage{InputTokens: 1000, CacheReadTokens: ptrInt(1000), OutputTokens: 260, TotalTokens: 1260}
		r.LatencyMs, r.FirstTokenMs = ptrInt(3000), ptrInt(1000)
	})
	insert("request_other", func(r *contract.RequestRecord) {
		r.LocalAccessTokenID = &tokenB
		r.Usage.CacheReadTokens = ptrInt(0)
		r.FirstTokenMs = nil
	})
	insert("request_failed", func(r *contract.RequestRecord) { r.Status = contract.RequestStatusFailed })
	insert("request_child", func(r *contract.RequestRecord) {
		id := contract.RequestID("request_first")
		r.ParentRequestID = &id
		r.AttemptIndex = 1
	})
	insert("request_discovery", func(r *contract.RequestRecord) { r.InputProtocol = contract.ProtocolOpenAIModels })
	insert("request_incomplete", func(r *contract.RequestRecord) { r.Usage.BillingIncomplete = true })
	insert("request_missing", func(r *contract.RequestRecord) { r.Usage.CacheReadTokens = nil; r.FirstTokenMs = nil })
	items, err := store.ListAccessTokenUsage(t.Context(), today)
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	for _, test := range []struct {
		stats        storagecontract.ServicePerformance
		cache, speed float64
		samples      int64
	}{
		{items[0].TodayPerformance, .26, 240.0 / 4.5, 2},
		{items[0].TotalPerformance, .63, 500.0 / 6.5, 3},
	} {
		if test.stats.CacheHitRate == nil || math.Abs(*test.stats.CacheHitRate-test.cache) > 1e-9 || test.stats.OutputTokensPerSecond == nil || math.Abs(*test.stats.OutputTokensPerSecond-test.speed) > 1e-9 || test.stats.CacheSamples != test.samples || test.stats.SpeedSamples != test.samples {
			t.Fatalf("incorrect token performance: %+v", test.stats)
		}
	}
	if got := items[1].TodayPerformance; got.CacheHitRate == nil || *got.CacheHitRate != 0 || got.OutputTokensPerSecond != nil || got.CacheSamples != 1 || got.SpeedSamples != 0 {
		t.Fatalf("zero/unknown lost: %+v", got)
	}
}

func TestAccessTokenUsageRejectsInvalidCounts(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	for _, usage := range []string{`{"total_tokens":-1}`, `{"total_tokens":"3"}`, `{"total_tokens":1.5}`, `{}`} {
		if _, err := store.db.Exec(`INSERT OR REPLACE INTO request_records
            (id, started_at, status, input_protocol, streaming, local_access_token_id, usage_json, audit_json, created_at)
            VALUES ('request_invalid', '2026-09-19T00:00:00Z', 'succeeded', 'openai.responses', 0, 'token_a', ?, '{}', '2026-09-19T00:00:00Z')`, usage); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ListAccessTokenUsage(context.Background(), time.Now().Truncate(time.Second)); !errors.Is(err, storagecontract.ErrInvalidRecord) {
			t.Fatalf("usage=%s err=%v", usage, err)
		}
	}
}

func TestAccessTokenBillingKeepsLifetimeDecimalsAndLedgerCoverage(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "billing.db"))
	defer store.Close()
	today := time.Date(2026, 9, 25, 0, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	for _, row := range []struct {
		root           string
		attempt        int
		at             time.Time
		amount, reason string
		revalued       bool
	}{
		{"request_old", 1, today.AddDate(-2, 0, 0), "100.000000001", "priced", false},
		{"request_boundary", 1, today.Add(-time.Nanosecond), "0.1", "priced", false},
		{"request_today", 1, today, "0.2", "priced", false},
		{"request_today", 2, today.Add(time.Nanosecond), "0.000000001", "priced", true},
		{"request_unknown", 1, today, "0", "unpriced", false},
		{"request_pending", 1, today, "0", "pending", false},
	} {
		_, err := store.db.Exec(`INSERT INTO billing_ledger
(root_id,attempt,service_id,account_key,model,started_at,terminal,amount_usd,reason,revalued,local_access_token_id)
VALUES (?,?,'service_billing','','model',?,1,?,?,?,'token_billing')`, row.root, row.attempt, billingTime(row.at), row.amount, row.reason, row.revalued)
		if err != nil {
			t.Fatal(err)
		}
	}
	// No request rows are needed: cost history survives their retention period.
	items, err := store.ListAccessTokenUsage(t.Context(), today)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	got := items[0]
	if got.TodayTokens != 0 || got.TotalTokens != 0 {
		t.Fatalf("tokens=%+v", got)
	}
	if got.TodayBilling.AmountUSD != "0.200000001" || got.TotalBilling.AmountUSD != "100.300000002" {
		t.Fatalf("amounts=%+v", got)
	}
	if got.TodayBilling.Priced != 2 || got.TodayBilling.Requests != 3 || got.TotalBilling.Requests != 5 || got.TotalBilling.Unpriced != 1 || got.TotalBilling.Pending != 1 || got.TotalBilling.Revalued != 1 {
		t.Fatalf("coverage=%+v", got)
	}
	if _, err := store.db.Exec(`UPDATE billing_ledger SET amount_usd='invalid' WHERE root_id='request_today'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListAccessTokenUsage(t.Context(), today); !errors.Is(err, storagecontract.ErrInvalidRecord) {
		t.Fatalf("invalid amount err=%v", err)
	}
}
