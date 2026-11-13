package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestUsageSummaryCompleteWindowAndOutcomeSemantics(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage.db"))
	defer store.Close()
	ctx := context.Background()
	zone, _ := time.LoadLocation("Asia/Tokyo")
	from := time.Date(2026, 9, 19, 0, 0, 0, 0, zone)
	to := from.AddDate(0, 0, 1)
	options := storage.UsageSummaryOptions{From: from, To: to, TimeZone: "Asia/Tokyo", Bucket: "hour"}
	service := contract.ServiceID("service_one")
	read, write := 1, 2
	base := contract.RequestRecord{ID: "request_base", StartedAt: from, Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
		ServiceID: &service, RequestedModel: ptrString("model_one"),
		Usage: &contract.Usage{InputTokens: 5, OutputTokens: 2, TotalTokens: 7, CacheReadTokens: &read, CacheWriteTokens: &write}}
	for i := 0; i < 4005; i++ {
		record := base
		record.ID = contract.RequestID(fmt.Sprintf("request_%05d", i))
		record.StartedAt = from.Add(time.Duration(i) * time.Nanosecond)
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		id                        string
		status                    contract.RequestStatus
		at                        time.Time
		child, httpError, noUsage bool
	}{
		{"before", contract.RequestStatusSucceeded, from.Add(-time.Nanosecond), false, false, false},
		{"after", contract.RequestStatusSucceeded, to, false, false, false},
		{"after_fraction", contract.RequestStatusSucceeded, to.Add(time.Nanosecond), false, false, false},
		{"child", contract.RequestStatusSucceeded, from, true, false, false},
		{"failed", contract.RequestStatusFailed, from.Add(time.Hour), false, false, false},
		{"legacy", contract.RequestStatusSucceeded, from.Add(time.Hour), false, true, false},
		{"pending", contract.RequestStatusPending, from, false, false, false},
		{"blocked", contract.RequestStatusBlocked, from, false, false, false},
		{"cancelled", contract.RequestStatusCancelled, from, false, false, false},
		{"empty", contract.RequestStatusSucceeded, from.Add(2 * time.Hour), false, false, true},
	} {
		record := base
		record.ID, record.Status, record.StartedAt = contract.RequestID("request_"+test.id), test.status, test.at
		if test.child {
			parent := contract.RequestID("request_00000")
			record.ParentRequestID, record.AttemptIndex = &parent, 1
		}
		if test.httpError {
			code := 500
			record.HTTPStatus = &code
		}
		if test.noUsage {
			record.Usage, record.ServiceID, record.RequestedModel = nil, nil, nil
		}
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := store.GetUsageSummary(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	want := storage.UsageTotals{Requests: 4006, FailedRequests: 2, InputTokens: 4005 * 5, OutputTokens: 4005 * 2, TotalTokens: 4005 * 7, CacheReadTokens: 4005, CacheWriteTokens: 4005 * 2}
	if summary.Totals != want || summary.ScannedRecords != 4008 {
		t.Fatalf("totals=%+v scanned=%d", summary.Totals, summary.ScannedRecords)
	}
	// Outcomes that are not requests are counted beside them, never in them.
	wantStatuses := storage.UsageStatusCounts{Succeeded: 4006, Failed: 2, Blocked: 1, Cancelled: 1, Pending: 1}
	if summary.StatusCounts != wantStatuses {
		t.Fatalf("status counts=%+v", summary.StatusCounts)
	}
	if len(summary.ByDay) != 1 || summary.ByDay[0].Date != "2026-09-19" || summary.ByDay[0].UsageTotals != want {
		t.Fatalf("days=%+v", summary.ByDay)
	}
	if len(summary.ByHour) != 3 || *summary.ByHour[1].Hour != 1 || summary.ByHour[1].FailedRequests != 2 {
		t.Fatalf("hours=%+v", summary.ByHour)
	}
	if len(summary.ByService) != 2 || summary.ByService[0].Requests != 4005 || summary.ByService[1].ID != nil || summary.ByService[1].Requests != 1 {
		t.Fatalf("services=%+v", summary.ByService)
	}
	if err := store.DeleteRequestRecord(ctx, "request_00000"); err != nil {
		t.Fatal(err)
	}
	summary, err = store.GetUsageSummary(ctx, options)
	if err != nil || summary.Totals.TotalTokens != want.TotalTokens-7 {
		t.Fatalf("delete summary=%+v err=%v", summary, err)
	}
	if _, err := store.PurgeRequestRecords(ctx, contract.PurgeRequest{Scope: contract.PurgeScopeAll, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	summary, err = store.GetUsageSummary(ctx, options)
	if err != nil || summary.ScannedRecords != 0 || summary.ByDay == nil || summary.ByService == nil || summary.StatusCounts != (storage.UsageStatusCounts{}) {
		t.Fatalf("empty=%+v err=%v", summary, err)
	}
}

// The tray and the overview open a status count as the session list filtered
// by call status over the same window. Every counted call must sit in a listed
// conversation, including one that recovered and now reads as succeeded.
func TestUsageSummaryFailuresListAsFailedSessions(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "failed-sessions.db"))
	defer store.Close()
	ctx := context.Background()
	zone, _ := time.LoadLocation("Asia/Tokyo")
	from := time.Date(2026, 9, 19, 0, 0, 0, 0, zone)
	to := from.AddDate(0, 0, 1)
	insert := func(id, session string, status contract.RequestStatus, at time.Time, parent string) {
		t.Helper()
		sessionID := contract.SessionID(session)
		record := contract.RequestRecord{ID: contract.RequestID(id), SessionID: &sessionID, StartedAt: at, Status: status,
			InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary()}
		if parent != "" {
			parentID := contract.RequestID(parent)
			record.ParentRequestID, record.AttemptIndex = &parentID, 1
		}
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	at := func(hours int) time.Time { return from.Add(time.Duration(hours) * time.Hour) }
	insert("request_recovered_1", "session_recovered", contract.RequestStatusFailed, at(1), "")
	insert("request_recovered_2", "session_recovered", contract.RequestStatusSucceeded, at(2), "")
	insert("request_failing_1", "session_failing", contract.RequestStatusFailed, at(3), "")
	insert("request_failing_2", "session_failing", contract.RequestStatusFailed, at(4), "")
	// A policy block is its own outcome, and a retried attempt is not a call.
	insert("request_blocked", "session_blocked", contract.RequestStatusBlocked, at(5), "")
	insert("request_retried", "session_retried", contract.RequestStatusSucceeded, at(6), "")
	insert("request_retried_attempt", "session_retried", contract.RequestStatusFailed, at(6).Add(time.Minute), "request_retried")
	insert("request_yesterday", "session_yesterday", contract.RequestStatusFailed, from.Add(-time.Hour), "")
	// Model discovery is not a model call: neither counted nor listed.
	discovery := contract.RequestRecord{ID: "request_discovery", StartedAt: at(7), Status: contract.RequestStatusFailed,
		InputProtocol: contract.ProtocolOpenAIModels, Audit: contract.NotCapturedAuditSummary()}
	if err := store.InsertRequestRecord(ctx, discovery); err != nil {
		t.Fatal(err)
	}

	summary, err := store.GetUsageSummary(ctx, storage.UsageSummaryOptions{From: from, To: to, TimeZone: "Asia/Tokyo", Bucket: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Totals.FailedRequests != 3 || summary.Totals.Requests != 2 {
		t.Fatalf("totals=%+v", summary.Totals)
	}
	if want := (storage.UsageStatusCounts{Succeeded: 2, Failed: 3, Blocked: 1}); summary.StatusCounts != want {
		t.Fatalf("status counts=%+v", summary.StatusCounts)
	}
	for status, want := range map[contract.RequestStatus]map[contract.SessionID]contract.SessionStatus{
		contract.RequestStatusFailed: {
			"session_failing":   contract.SessionStatusFailed,
			"session_recovered": contract.SessionStatusSucceeded,
		},
		contract.RequestStatusSucceeded: {
			"session_recovered": contract.SessionStatusSucceeded,
			"session_retried":   contract.SessionStatusSucceeded,
		},
		contract.RequestStatusBlocked: {
			"session_blocked": contract.SessionStatusBlocked,
		},
	} {
		page, err := store.ListRequestSessions(ctx, storage.RequestSessionListOptions{Kind: "inference", From: &from, To: &to, Status: &status})
		if err != nil {
			t.Fatal(err)
		}
		listed := map[contract.SessionID]contract.SessionStatus{}
		for _, session := range page.Items {
			listed[session.ID] = session.Status
		}
		if !reflect.DeepEqual(listed, want) {
			t.Fatalf("%s listed=%v want=%v", status, listed, want)
		}
	}
}

func TestUsageSummaryDSTRepeatingHour(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "dst.db"))
	defer store.Close()
	zone, _ := time.LoadLocation("America/New_York")
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, zone)
	for i, hours := range []int{1, 2, 24} {
		if err := store.InsertRequestRecord(context.Background(), contract.RequestRecord{
			ID: contract.RequestID(fmt.Sprintf("request_dst_%d", i)), StartedAt: from.Add(time.Duration(hours) * time.Hour),
			Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses,
			Audit: contract.NotCapturedAuditSummary(), Usage: &contract.Usage{TotalTokens: 3},
		}); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := store.GetUsageSummary(context.Background(), storage.UsageSummaryOptions{From: from, To: from.AddDate(0, 0, 1), TimeZone: zone.String(), Bucket: "hour"})
	if err != nil || len(summary.ByHour) != 2 || *summary.ByHour[0].Hour != 1 || summary.ByHour[0].Requests != 2 || *summary.ByHour[1].Hour != 23 {
		t.Fatalf("dst=%+v err=%v", summary, err)
	}
}

func TestUsageSummaryExcludesModelDiscovery(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "discovery.db"))
	defer store.Close()
	ctx := context.Background()
	from := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	options := storage.UsageSummaryOptions{From: from, To: from.AddDate(0, 0, 2), TimeZone: "UTC", Bucket: "hour"}
	empty, err := store.GetUsageSummary(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	service := contract.ServiceID("service_one")
	for protocolIndex, protocol := range []contract.ProtocolID{contract.ProtocolOpenAIModels, contract.ProtocolGoogleModels} {
		for i, outcome := range []struct {
			status     contract.RequestStatus
			httpStatus int
			attributed bool
		}{
			{contract.RequestStatusSucceeded, 200, false},
			{contract.RequestStatusFailed, 502, false},
			{contract.RequestStatusSucceeded, 500, false},
			{contract.RequestStatusSucceeded, 200, true},
		} {
			record := contract.RequestRecord{
				ID: contract.RequestID(fmt.Sprintf("request_discovery_%d_%d", protocolIndex, i)), StartedAt: from.Add(time.Duration(i) * time.Hour),
				Status: outcome.status, HTTPStatus: &outcome.httpStatus, InputProtocol: protocol, Audit: contract.NotCapturedAuditSummary(),
			}
			// Exclude discovery by protocol even if it has attribution or usage.
			if outcome.attributed {
				record.ServiceID, record.RequestedModel = &service, ptrString("model_one")
				record.Usage = &contract.Usage{InputTokens: 5, OutputTokens: 2, TotalTokens: 7}
			}
			if err := store.InsertRequestRecord(ctx, record); err != nil {
				t.Fatal(err)
			}
		}
	}
	summary, err := store.GetUsageSummary(ctx, options)
	if err != nil || !reflect.DeepEqual(summary, empty) {
		t.Fatalf("discovery-only summary=%+v err=%v", summary, err)
	}
	for i, status := range []contract.RequestStatus{contract.RequestStatusSucceeded, contract.RequestStatusSucceeded, contract.RequestStatusFailed} {
		record := contract.RequestRecord{
			ID: contract.RequestID(fmt.Sprintf("request_inference_%d", i)), StartedAt: from.Add(24 * time.Hour),
			Status: status, InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
			ServiceID: &service, RequestedModel: ptrString("model_one"),
		}
		if i == 0 {
			record.Usage = &contract.Usage{InputTokens: 5, OutputTokens: 2, TotalTokens: 7}
		}
		if status == contract.RequestStatusFailed {
			record.ServiceID = nil // Pre-routing failures still count.
		}
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	summary, err = store.GetUsageSummary(ctx, options)
	want := storage.UsageTotals{Requests: 2, FailedRequests: 1, InputTokens: 5, OutputTokens: 2, TotalTokens: 7}
	if err != nil || summary.Totals != want || summary.ScannedRecords != 3 {
		t.Fatalf("mixed summary=%+v err=%v", summary, err)
	}
	if len(summary.ByDay) != 1 || summary.ByDay[0].Date != "2026-09-20" || summary.ByDay[0].UsageTotals != want ||
		len(summary.ByHour) != 1 || summary.ByHour[0].Date != "2026-09-20" || *summary.ByHour[0].Hour != 0 || summary.ByHour[0].UsageTotals != want {
		t.Fatalf("days=%+v hours=%+v", summary.ByDay, summary.ByHour)
	}
	// The pre-routing failure clears only the service, so it opens a null service
	// bucket. The requested model is still set and stays on that model group.
	if len(summary.ByService) != 2 || summary.ByService[0].ID == nil || summary.ByService[0].Requests != 2 ||
		summary.ByService[0].FailedRequests != 0 || summary.ByService[0].TotalTokens != 7 ||
		summary.ByService[1].ID != nil || summary.ByService[1].Requests != 0 || summary.ByService[1].FailedRequests != 1 {
		t.Fatalf("services=%+v", summary.ByService)
	}
	if len(summary.ByModel) != 1 || summary.ByModel[0].ID == nil || *summary.ByModel[0].ID != "model_one" ||
		summary.ByModel[0].Requests != 2 || summary.ByModel[0].FailedRequests != 1 || summary.ByModel[0].TotalTokens != 7 {
		t.Fatalf("models=%+v", summary.ByModel)
	}
}

func TestSessionSummaryMatchesDetailAcrossFiltersAndRetries(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "summaries.db"))
	defer store.Close()
	ctx := context.Background()
	start := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	for s := 0; s < 3; s++ {
		session := contract.SessionID(fmt.Sprintf("session_%d", s))
		for i, turn := range []int{1, 1, 2, 1} {
			record := contract.RequestRecord{ID: contract.RequestID(fmt.Sprintf("request_%d_%d", s, i)), StartedAt: start.Add(time.Duration(i) * time.Minute),
				SessionID: &session, TurnIndex: &turn, Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses,
				Audit: contract.NotCapturedAuditSummary(), RequestedModel: ptrString("model"), ReasoningEffort: ptrString("high")}
			if i == 0 {
				record.InputPreview = ptrString("first title")
			}
			if i == 3 {
				record.Status = contract.RequestStatusCancelled
			}
			if err := store.InsertRequestRecord(ctx, record); err != nil {
				t.Fatal(err)
			}
			parent := record.ID
			record.ID, record.ParentRequestID, record.AttemptIndex = contract.RequestID(fmt.Sprintf("child_%d_%d", s, i)), &parent, 1
			if err := store.InsertRequestRecord(ctx, record); err != nil {
				t.Fatal(err)
			}
		}
	}
	from := start.Add(time.Minute)
	page, err := store.ListRequestSessions(ctx, storage.RequestSessionListOptions{Limit: 2, From: &from})
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	for _, session := range page.Items {
		detail, err := store.GetRequestSession(ctx, string(session.ID))
		if err != nil || !reflect.DeepEqual(session, detail.RequestSession) {
			t.Fatalf("summary=%+v detail=%+v err=%v", session, detail.RequestSession, err)
		}
		if session.TurnCount != 3 || session.CallCount != 8 || session.Title != "first title" || session.Status != contract.SessionStatusInterrupted {
			t.Fatalf("summary=%+v", session)
		}
	}
	next, err := store.ListRequestSessions(ctx, storage.RequestSessionListOptions{Limit: 2, From: &from, Cursor: page.NextCursor})
	if err != nil || len(next.Items) != 1 || next.NextCursor != "" {
		t.Fatalf("next=%+v err=%v", next, err)
	}
}

func TestUsageSummaryGroupsFailuresAndCurrentAccessTokens(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage-token-groups.db"))
	defer store.Close()
	ctx := t.Context()
	metadata, err := store.ListAccessTokens(ctx)
	if err != nil || len(metadata) == 0 {
		t.Fatalf("ListAccessTokens: %v", err)
	}
	currentToken := metadata[0].ID
	deletedToken := contract.AccessTokenID("token_deleted")
	service := contract.ServiceID("service_usage")
	model := "model_usage"
	start := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	insert := func(record contract.RequestRecord) {
		t.Helper()
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	insert(contract.RequestRecord{ID: "usage_token_success", StartedAt: start, Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
		ServiceID: &service, RequestedModel: &model, LocalAccessTokenID: &currentToken,
		Usage: &contract.Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10}})
	insert(contract.RequestRecord{ID: "usage_token_failed", StartedAt: start.Add(time.Minute), Status: contract.RequestStatusFailed,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
		ServiceID: &service, RequestedModel: &model, LocalAccessTokenID: &currentToken,
		Error: &contract.ErrorSummary{Category: "upstream", Code: "failed", Message: "failed"}})
	insert(contract.RequestRecord{ID: "usage_deleted_token", StartedAt: start.Add(2 * time.Minute), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
		ServiceID: &service, RequestedModel: &model, LocalAccessTokenID: &deletedToken,
		Usage: &contract.Usage{TotalTokens: 20}})
	insert(contract.RequestRecord{ID: "usage_unassigned_failed", StartedAt: start.Add(3 * time.Minute), Status: contract.RequestStatusFailed,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
		Error: &contract.ErrorSummary{Category: "upstream", Code: "failed", Message: "failed"}})

	summary, err := store.GetUsageSummary(ctx, storage.UsageSummaryOptions{
		From: start.Add(-time.Second), To: start.Add(time.Hour), TimeZone: "UTC", Bucket: "hour",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.ByToken) != 1 || summary.ByToken[0].ID == nil || *summary.ByToken[0].ID != string(currentToken) {
		t.Fatalf("by_token=%+v", summary.ByToken)
	}
	if summary.ByToken[0].Requests != 1 || summary.ByToken[0].FailedRequests != 1 || summary.ByToken[0].TotalTokens != 10 {
		t.Fatalf("token totals=%+v", summary.ByToken[0])
	}
	// Deleted-token requests still contribute to service/model usage. Only the
	// token breakdown is intersected with the current-token directory.
	for _, test := range []struct {
		name   string
		id     string
		groups []storage.UsageGroup
	}{
		{name: "service", id: string(service), groups: summary.ByService},
		{name: "model", id: model, groups: summary.ByModel},
	} {
		t.Run(test.name, func(t *testing.T) {
			if len(test.groups) != 2 {
				t.Fatalf("groups=%+v", test.groups)
			}
			assigned, unassigned := test.groups[0], test.groups[1]
			if assigned.ID == nil || *assigned.ID != test.id || assigned.Requests != 2 || assigned.FailedRequests != 1 || assigned.TotalTokens != 30 {
				t.Fatalf("assigned group=%+v", assigned)
			}
			if unassigned.ID != nil || unassigned.Requests != 0 || unassigned.FailedRequests != 1 || unassigned.TotalTokens != 0 {
				t.Fatalf("unassigned group=%+v", unassigned)
			}
		})
	}
}

func TestUsageSummaryGroupsModelsByUpstreamModel(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage-upstream-model.db"))
	defer store.Close()
	ctx := t.Context()
	service := contract.ServiceID("service_usage")
	start := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	insert := func(id string, requested string, redirect *contract.RequestModelRedirect, recovery *contract.RequestRecovery, tokens int) {
		t.Helper()
		if err := store.InsertRequestRecord(ctx, contract.RequestRecord{ID: contract.RequestID(id), StartedAt: start, Status: contract.RequestStatusSucceeded,
			InputProtocol: contract.ProtocolAnthropicMessages, Audit: contract.NotCapturedAuditSummary(),
			ServiceID: &service, RequestedModel: &requested, ModelRedirect: redirect, Recovery: recovery,
			Usage: &contract.Usage{TotalTokens: tokens}}); err != nil {
			t.Fatal(err)
		}
	}
	redirect := &contract.RequestModelRedirect{From: "claude-sonnet-4-5", To: "gpt-6-astra"}
	insert("usage_served", "claude-sonnet-4-5", redirect, &contract.RequestRecovery{UpstreamModel: "gpt-6-astra"}, 10)
	// A redirect whose request never reached a provider still reports its target.
	insert("usage_redirect_only", "claude-sonnet-4-5", redirect, nil, 20)
	insert("usage_direct", "claude-haiku-4-5", nil, nil, 5)

	summary, err := store.GetUsageSummary(ctx, storage.UsageSummaryOptions{
		From: start.Add(-time.Second), To: start.Add(time.Hour), TimeZone: "UTC", Bucket: "hour",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, group := range summary.ByModel {
		if group.ID == nil {
			t.Fatalf("unexpected unassigned model group=%+v", group)
		}
		got[*group.ID] = group.TotalTokens
	}
	if want := map[string]int64{"gpt-6-astra": 30, "claude-haiku-4-5": 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("by_model=%v want %v", got, want)
	}
}

func TestUsageSummaryHourModelBucketsAndClients(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage-hour-models.db"))
	defer store.Close()
	ctx := t.Context()
	zone, _ := time.LoadLocation("Asia/Shanghai")
	from := time.Date(2026, 10, 30, 0, 0, 0, 0, zone)
	local := func(day, hour, minute, second int) time.Time {
		return time.Date(2026, 10, day, hour, minute, second, 0, zone)
	}
	insert := func(id string, at time.Time, status contract.RequestStatus, model string, client contract.ClientType, tokens int) contract.RequestRecord {
		t.Helper()
		record := contract.RequestRecord{ID: contract.RequestID(id), StartedAt: at, Status: status,
			InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(), ClientType: client}
		if model != "" {
			record.RequestedModel = &model
		}
		if status == contract.RequestStatusSucceeded {
			record.Usage = &contract.Usage{TotalTokens: tokens}
		}
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
		return record
	}
	insert("request_late_gpt", local(30, 23, 30, 0), contract.RequestStatusSucceeded, "gpt-5.5", contract.ClientCodex, 100)
	insert("request_late_claude", local(30, 23, 59, 59), contract.RequestStatusSucceeded, "claude-opus-5-5", contract.ClientClaudeCode, 300)
	// Local midnight is 16:00 UTC the day before; it opens the next local day.
	insert("request_midnight", local(31, 0, 0, 0), contract.RequestStatusSucceeded, "gpt-5.5", contract.ClientUnknown, 50)
	insert("request_historical", local(31, 0, 10, 0), contract.RequestStatusSucceeded, "gpt-5.5", "", 25)
	insert("request_future_client", local(31, 0, 20, 0), contract.RequestStatusSucceeded, "gpt-5.5", contract.ClientCodex, 5)
	if _, err := store.db.ExecContext(ctx, `UPDATE request_records SET client_type = 'future_client' WHERE id = 'request_future_client'`); err != nil {
		t.Fatal(err)
	}
	insert("request_failed", local(31, 5, 0, 0), contract.RequestStatusFailed, "gpt-5.5", contract.ClientCodex, 0)
	insert("request_no_model", local(31, 5, 30, 0), contract.RequestStatusSucceeded, "", contract.ClientCursor, 10)
	insert("request_blocked", local(31, 6, 0, 0), contract.RequestStatusBlocked, "gpt-5.5", contract.ClientCursor, 0)
	insert("request_before", from.Add(-time.Second), contract.RequestStatusSucceeded, "gpt-5.5", contract.ClientCodex, 1000)
	parent := insert("request_parent", local(31, 7, 0, 0), contract.RequestStatusSucceeded, "gpt-5.5", contract.ClientPi, 1)
	child := parent
	child.ID, child.ParentRequestID, child.AttemptIndex = "request_retry", &parent.ID, 1
	if err := store.InsertRequestRecord(ctx, child); err != nil {
		t.Fatal(err)
	}

	// Day-bucketed windows carry the buckets too, and the cached read matches.
	options := storage.UsageSummaryOptions{From: from, To: from.AddDate(0, 0, 2), TimeZone: "Asia/Shanghai", Bucket: "day"}
	summary, err := store.GetUsageSummary(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := store.GetUsageSummary(ctx, options)
	if err != nil || !reflect.DeepEqual(cached, summary) {
		t.Fatalf("cached=%+v err=%v", cached, err)
	}
	type bucket struct {
		date            string
		hour            int
		model           string
		requests, total int64
	}
	var buckets []bucket
	for _, item := range summary.ByHourModel {
		model := "<nil>"
		if item.Model != nil {
			model = *item.Model
		}
		buckets = append(buckets, bucket{item.Date, item.Hour, model, item.Requests, item.TotalTokens})
	}
	wantBuckets := []bucket{
		{"2026-10-30", 23, "claude-opus-5-5", 1, 300},
		{"2026-10-30", 23, "gpt-5.5", 1, 100},
		{"2026-10-31", 0, "gpt-5.5", 3, 80},
		{"2026-10-31", 5, "<nil>", 1, 10},
		{"2026-10-31", 7, "gpt-5.5", 1, 1},
	}
	if !reflect.DeepEqual(buckets, wantBuckets) {
		t.Fatalf("by_hour_model=%+v\nwant %+v", buckets, wantBuckets)
	}
	type client struct {
		id                      string
		requests, failed, total int64
	}
	var clients []client
	for _, group := range summary.ByClient {
		id := "<nil>"
		if group.ID != nil {
			id = *group.ID
		}
		clients = append(clients, client{id, group.Requests, group.FailedRequests, group.TotalTokens})
	}
	// Unknown, historical and unrecognized clients share the null group.
	wantClients := []client{
		{"claude_code", 1, 0, 300},
		{"codex", 1, 1, 100},
		{"<nil>", 3, 0, 80},
		{"cursor", 1, 0, 10},
		{"pi", 1, 0, 1},
	}
	if !reflect.DeepEqual(clients, wantClients) {
		t.Fatalf("by_client=%+v\nwant %+v", clients, wantClients)
	}
	var clientTokens int64
	for _, group := range summary.ByClient {
		clientTokens += group.TotalTokens
	}
	if clientTokens != summary.Totals.TotalTokens {
		t.Fatalf("client tokens=%d total=%d", clientTokens, summary.Totals.TotalTokens)
	}
}

func TestUsageSummaryHourModelBucketsSpan(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage-hour-model-span.db"))
	defer store.Close()
	ctx := t.Context()
	zone, _ := time.LoadLocation("America/New_York")
	// 01:30 EDT and 01:30 EST on the fall-back day share the local hour 1.
	fallBack := time.Date(2026, 11, 1, 1, 30, 0, 0, zone)
	for i, at := range []time.Time{fallBack, fallBack.Add(time.Hour)} {
		if err := store.InsertRequestRecord(ctx, contract.RequestRecord{
			ID: contract.RequestID(fmt.Sprintf("request_span_%d", i)), StartedAt: at, Status: contract.RequestStatusSucceeded,
			InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
			RequestedModel: ptrString("model_one"), ClientType: contract.ClientCodex, Usage: &contract.Usage{TotalTokens: 4},
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Thirty local days across the DST change run 721 hours and keep the buckets.
	month := storage.UsageSummaryOptions{From: time.Date(2026, 10, 15, 0, 0, 0, 0, zone), To: time.Date(2026, 11, 14, 0, 0, 0, 0, zone), TimeZone: zone.String(), Bucket: "day"}
	summary, err := store.GetUsageSummary(ctx, month)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.ByHourModel) != 1 || summary.ByHourModel[0].Date != "2026-11-01" || summary.ByHourModel[0].Hour != 1 ||
		summary.ByHourModel[0].Requests != 2 || summary.ByHourModel[0].TotalTokens != 8 {
		t.Fatalf("month by_hour_model=%+v", summary.ByHourModel)
	}
	// Past the cap the buckets stay empty while every other group still fills.
	quarter := month
	quarter.From = time.Date(2026, 8, 16, 0, 0, 0, 0, zone)
	if quarter.To.Sub(quarter.From) <= storage.UsageDetailMaxSpan {
		t.Fatalf("quarter span %s is not past the cap", quarter.To.Sub(quarter.From))
	}
	summary, err = store.GetUsageSummary(ctx, quarter)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ByHourModel == nil || len(summary.ByHourModel) != 0 || summary.ByRoute == nil || len(summary.ByRoute) != 0 ||
		summary.Totals.Requests != 2 || len(summary.ByClient) != 1 || summary.ByClient[0].ID == nil || *summary.ByClient[0].ID != "codex" {
		t.Fatalf("quarter hour models=%+v routes=%+v clients=%+v totals=%+v", summary.ByHourModel, summary.ByRoute, summary.ByClient, summary.Totals)
	}
}

func TestUsageSummaryRoutesAndGatewayRewrites(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage-routes.db"))
	defer store.Close()
	ctx := t.Context()
	zone, _ := time.LoadLocation("Asia/Shanghai")
	from := time.Date(2026, 11, 2, 0, 0, 0, 0, zone)
	minute := 0
	insert := func(id string, client contract.ClientType, service string, status contract.RequestStatus, edit func(*contract.RequestRecord)) contract.RequestRecord {
		t.Helper()
		minute++
		started := from.Add(time.Duration(minute) * time.Minute)
		completed := started.Add(10 * time.Second)
		record := contract.RequestRecord{ID: contract.RequestID(id), StartedAt: started, CompletedAt: &completed, Status: status,
			InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(), ClientType: client,
			RequestedModel: ptrString("gpt-5.5"), AttemptIndex: 1}
		if service != "" {
			id := contract.ServiceID(service)
			record.ServiceID = &id
		}
		if status == contract.RequestStatusSucceeded {
			record.Usage = &contract.Usage{TotalTokens: 10}
		}
		if edit != nil {
			edit(&record)
		}
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
		return record
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := store.db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	note := func(rewrite contract.GatewayRewrite) contract.ConversionDiagnostic {
		return contract.ConversionDiagnostic{Phase: contract.ConversionDiagnosticPhaseRequest, Severity: contract.ConversionDiagnosticWarning,
			Code: contract.GatewayRewriteDiagnostic, Path: string(rewrite), Message: "noted"}
	}
	images := func(summary string) func(*contract.RequestRecord) {
		return func(record *contract.RequestRecord) {
			ended := record.StartedAt.Add(time.Second)
			record.Events = []contract.RequestEvent{{Kind: contract.RequestEventImageCompression, StartedAt: record.StartedAt,
				EndedAt: &ended, Status: contract.RequestStatusSucceeded, Summary: summary, AttemptIndex: 1}}
		}
	}

	insert("request_plain", contract.ClientCodex, "service_a", contract.RequestStatusSucceeded, nil)
	insert("request_plain_too", contract.ClientCodex, "service_a", contract.RequestStatusSucceeded, nil)
	insert("request_global_redirect", contract.ClientCodex, "service_a", contract.RequestStatusSucceeded, func(record *contract.RequestRecord) {
		record.ModelRedirect = &contract.RequestModelRedirect{From: "gpt-5.5", To: "glm-5.3"}
		record.Recovery = &contract.RequestRecovery{UpstreamModel: "glm-5.3"}
	})
	// A provider's own redirect shows only in the model it sent upstream.
	insert("request_provider_redirect", contract.ClientCodex, "service_a", contract.RequestStatusSucceeded, func(record *contract.RequestRecord) {
		record.Recovery = &contract.RequestRecovery{UpstreamModel: "gpt-5.5-mini"}
	})
	insert("request_same_model", contract.ClientCodex, "service_a", contract.RequestStatusSucceeded, func(record *contract.RequestRecord) {
		record.Recovery = &contract.RequestRecovery{UpstreamModel: "gpt-5.5"}
	})
	insert("request_privacy", contract.ClientClaudeCode, "service_a", contract.RequestStatusSucceeded, nil)
	exec(`UPDATE request_records SET privacy_decision = 'redact' WHERE id = 'request_privacy'`)
	insert("request_images", contract.ClientClaudeCode, "service_a", contract.RequestStatusSucceeded, images("lossless · 1/2 images · 100 → 50 bytes"))
	// Images found but none replaced are no compression.
	insert("request_images_kept", contract.ClientClaudeCode, "service_a", contract.RequestStatusSucceeded, images("lossy q80 · 0/2 images · 100 → 100 bytes"))
	insert("request_noted", contract.ClientClaudeCode, "service_b", contract.RequestStatusSucceeded, func(record *contract.RequestRecord) {
		record.ConversionDiagnostics = []contract.ConversionDiagnostic{
			note(contract.GatewayRewriteClientIdentity), note(contract.GatewayRewriteSystemPrompt), note(contract.GatewayRewriteReasoning),
			{Phase: contract.ConversionDiagnosticPhaseRequest, Severity: contract.ConversionDiagnosticWarning, Code: "gateway_reasoning",
				Path: "reasoning", Message: "fixed: sent high, client asked medium"},
		}
	})
	exec(`UPDATE request_records SET plan_json = json_object('plan_type', 'relaykit', 'service_id', 'service_b',
		'input_protocol', 'openai.responses', 'upstream_protocol', 'anthropic.messages', 'conversion_path', json_array(), 'streaming', 0)
		WHERE id = 'request_noted'`)
	// The newest failed attempt at another provider is where the call failed over from.
	failover := insert("request_failover", contract.ClientCursor, "service_b", contract.RequestStatusSucceeded, func(record *contract.RequestRecord) {
		record.AttemptIndex = 3
	})
	for index, service := range []string{"service_c", "service_a"} {
		child := failover
		child.ID = contract.RequestID(fmt.Sprintf("request_failover_child_%d", index))
		child.ParentRequestID, child.AttemptIndex, child.Status, child.Usage = &failover.ID, index+1, contract.RequestStatusFailed, nil
		id := contract.ServiceID(service)
		child.ServiceID = &id
		if err := store.InsertRequestRecord(ctx, child); err != nil {
			t.Fatal(err)
		}
	}
	// A retry at the same provider is no failover.
	retried := insert("request_retried", contract.ClientCursor, "service_a", contract.RequestStatusFailed, func(record *contract.RequestRecord) {
		record.AttemptIndex = 2
	})
	child := retried
	child.ID, child.ParentRequestID, child.AttemptIndex = "request_retried_child", &retried.ID, 1
	if err := store.InsertRequestRecord(ctx, child); err != nil {
		t.Fatal(err)
	}
	insert("request_blocked", contract.ClientCursor, "", contract.RequestStatusBlocked, nil)
	insert("request_unknown", contract.ClientUnknown, "", contract.RequestStatusFailed, nil)

	summary, err := store.GetUsageSummary(ctx, storage.UsageSummaryOptions{From: from, To: from.AddDate(0, 0, 1), TimeZone: "Asia/Shanghai", Bucket: "hour"})
	if err != nil {
		t.Fatal(err)
	}
	text := func(value *string) string {
		if value == nil {
			return "-"
		}
		return *value
	}
	var routes []string
	for _, route := range summary.ByRoute {
		routes = append(routes, fmt.Sprintf("%s>%s<%s failed=%t %v x%d", text(route.ClientType), text(route.ServiceID),
			text(route.FailedServiceID), route.Failed, route.Rewrites, route.Requests))
	}
	want := []string{
		"codex>service_a<- failed=false [] x3",
		"codex>service_a<- failed=false [model_redirect] x2",
		"->-<- failed=true [] x1",
		"claude_code>service_a<- failed=false [] x1",
		"claude_code>service_a<- failed=false [image_compression] x1",
		"claude_code>service_a<- failed=false [privacy] x1",
		"claude_code>service_b<- failed=false [global_prompt reasoning_effort conversion client_identity] x1",
		"cursor>service_a<- failed=true [] x1",
		"cursor>service_b<service_a failed=false [] x1",
	}
	if !reflect.DeepEqual(routes, want) {
		t.Fatalf("by_route=\n%s\nwant\n%s", strings.Join(routes, "\n"), strings.Join(want, "\n"))
	}

	// The live feed carries the same route once a call has ended.
	calls, err := store.ListRequestActivity(ctx, from)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]contract.RequestActivityCall{}
	for _, call := range calls {
		found[call.StartedAt.In(zone).Format("15:04")] = call
	}
	noted, moved := found["00:09"], found["00:10"]
	if noted.ServiceID == nil || *noted.ServiceID != "service_b" || noted.FailedServiceID != nil ||
		!reflect.DeepEqual(noted.Rewrites, []contract.GatewayRewrite{contract.GatewayRewriteSystemPrompt, contract.GatewayRewriteReasoning,
			contract.GatewayRewriteConversion, contract.GatewayRewriteClientIdentity}) {
		t.Fatalf("noted call=%+v", noted)
	}
	if moved.ServiceID == nil || *moved.ServiceID != "service_b" || moved.FailedServiceID == nil || *moved.FailedServiceID != "service_a" ||
		moved.Rewrites != nil {
		t.Fatalf("failover call=%+v", moved)
	}
}
