package sqlite

import (
	"context"
	"errors"
	"fmt"
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
		Usage: &contract.Usage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3},
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
		if items[0] != (storagecontract.AccessTokenUsage{TokenID: tokenA, TodayTokens: todayTokens, TotalTokens: totalTokens}) ||
			items[1] != (storagecontract.AccessTokenUsage{TokenID: tokenB, TodayTokens: 3, TotalTokens: 3}) {
			t.Fatalf("usage=%#v", items)
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
