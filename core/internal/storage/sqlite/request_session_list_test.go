package sqlite

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

// The list walks roots newest first and stops once a page is full. Every page
// must still be exactly what grouping all matching roots would give: sessions
// by their newest matching root, ties by descending id, as text the way SQLite
// compares the stored timestamps.
func TestListRequestSessionsPagesLikeAFullAggregation(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "session-pages.db"))
	defer store.Close()
	ctx := context.Background()
	random := rand.New(rand.NewPCG(7, 11))
	start := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	tokenA, tokenB := contract.AccessTokenID("token_a"), contract.AccessTokenID("token_b")
	tokens := []*contract.AccessTokenID{nil, &tokenA, &tokenB}
	protocols := []contract.ProtocolID{
		contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponses,
		contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIModels, contract.ProtocolGoogleModels,
	}
	statuses := []contract.RequestStatus{
		contract.RequestStatusSucceeded, contract.RequestStatusSucceeded, contract.RequestStatusFailed,
	}
	for index := range 240 {
		// Whole seconds tie often. A half second sorts before them as text.
		startedAt := start.Add(time.Duration(random.IntN(40)) * time.Second)
		if random.IntN(4) == 0 {
			startedAt = startedAt.Add(500 * time.Millisecond)
		}
		record := contract.RequestRecord{
			ID:                 contract.RequestID(fmt.Sprintf("request_page_%03d", index)),
			StartedAt:          startedAt,
			Status:             statuses[random.IntN(len(statuses))],
			InputProtocol:      protocols[random.IntN(len(protocols))],
			LocalAccessTokenID: tokens[random.IntN(len(tokens))],
			Audit:              contract.NotCapturedAuditSummary(),
		}
		if random.IntN(4) > 0 {
			session := contract.SessionID(fmt.Sprintf("session_page_%02d", random.IntN(50)))
			record.SessionID = &session
		}
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
		if index%9 == 0 {
			// Retries are not roots and never place a session.
			child := record
			child.ID += "_retry"
			child.ParentRequestID = &record.ID
			child.AttemptIndex = 1
			child.StartedAt = start.Add(time.Hour)
			if err := store.InsertRequestRecord(ctx, child); err != nil {
				t.Fatal(err)
			}
		}
	}

	type root struct {
		session, startedAt, protocol, status string
		token                                sql.NullString
	}
	var roots []root
	rows, err := store.db.QueryContext(ctx, `SELECT COALESCE(session_id, id), started_at, input_protocol, status, local_access_token_id
FROM request_records WHERE parent_request_id IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var item root
		if err := rows.Scan(&item.session, &item.startedAt, &item.protocol, &item.status, &item.token); err != nil {
			t.Fatal(err)
		}
		roots = append(roots, item)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	expected := func(options storagecontract.RequestSessionListOptions) []contract.SessionID {
		newest := map[string]string{}
		for _, item := range roots {
			discovery := item.protocol == string(contract.ProtocolOpenAIModels) ||
				item.protocol == string(contract.ProtocolGoogleModels)
			switch {
			case options.Kind == "inference" && discovery, options.Kind == "discovery" && !discovery:
				continue
			case options.From != nil && item.startedAt < options.From.UTC().Format(time.RFC3339Nano):
				continue
			case options.To != nil && item.startedAt >= options.To.UTC().Format(time.RFC3339Nano):
				continue
			case options.Status != nil && item.status != string(*options.Status):
				continue
			case len(options.LocalAccessTokenIDs) > 0 && (!item.token.Valid ||
				!slices.Contains(options.LocalAccessTokenIDs, contract.AccessTokenID(item.token.String))):
				continue
			}
			newest[item.session] = max(newest[item.session], item.startedAt)
		}
		ids := make([]string, 0, len(newest))
		for id := range newest {
			ids = append(ids, id)
		}
		slices.SortFunc(ids, func(left, right string) int {
			return cmp.Or(strings.Compare(newest[right], newest[left]), strings.Compare(right, left))
		})
		sessions := make([]contract.SessionID, len(ids))
		for index, id := range ids {
			sessions[index] = contract.SessionID(id)
		}
		return sessions
	}

	failed := contract.RequestStatusFailed
	from, to := start.Add(10*time.Second), start.Add(30*time.Second)
	for _, options := range []storagecontract.RequestSessionListOptions{
		{},
		{Kind: "inference"},
		{Kind: "discovery"},
		{LocalAccessTokenIDs: []contract.AccessTokenID{tokenA}},
		{Kind: "inference", LocalAccessTokenIDs: []contract.AccessTokenID{tokenA, tokenB}},
		{Status: &failed},
		{From: &from, To: &to},
	} {
		want := expected(options)
		if len(want) < 5 {
			t.Fatalf("options %+v match only %d sessions", options, len(want))
		}
		for _, limit := range []int{1, 4, 50} {
			options := options
			options.Limit = limit
			var got []contract.SessionID
			for pages := 0; ; pages++ {
				if pages > len(want) {
					t.Fatalf("options %+v: paging does not end", options)
				}
				page, err := store.ListRequestSessions(ctx, options)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Items) > limit {
					t.Fatalf("options %+v: page of %d", options, len(page.Items))
				}
				for _, item := range page.Items {
					got = append(got, item.ID)
				}
				if page.NextCursor == "" {
					break
				}
				options.Cursor = page.NextCursor
			}
			if !slices.Equal(got, want) {
				t.Fatalf("options %+v:\n got %v\nwant %v", options, got, want)
			}
		}
	}
}

// The early stop depends on index order. A plan that sorts would read every
// matching root on each poll again. Core runs without ANALYZE statistics, so
// plans are checked that way.
func TestRequestSessionQueriesReadInIndexOrder(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "session-plans.db"))
	defer store.Close()
	ctx := context.Background()
	explain := func(query string, args ...any) string {
		t.Helper()
		rows, err := store.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(detail + "; ")
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return plan.String()
	}

	from, to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, options := range []storagecontract.RequestSessionListOptions{
		{},
		{Kind: "inference"},
		{Kind: "discovery"},
		{Kind: "inference", LocalAccessTokenIDs: []contract.AccessTokenID{"token_a"}},
		{Kind: "inference", LocalAccessTokenIDs: []contract.AccessTokenID{"token_a", "token_b"}},
		{From: &from, To: &to},
	} {
		query, args := requestSessionScan(options)
		plan := explain(query, args...)
		if strings.Contains(plan, "TEMP B-TREE") || !strings.Contains(plan, "USING INDEX request_records_root_") {
			t.Fatalf("options %+v plan=%s", options, plan)
		}
	}
	sessions := []any{"session_a", "session_b", "session_c"}
	for name, query := range map[string]string{
		"summary": sessionSummaryQuery(len(sessions)),
		"runtime": sessionRuntimeQuery(len(sessions)),
	} {
		plan := explain(query, sessions...)
		if strings.Contains(plan, "TEMP B-TREE") || !strings.Contains(plan, "request_records_session_turns_idx") {
			t.Fatalf("%s plan=%s", name, plan)
		}
	}
}
