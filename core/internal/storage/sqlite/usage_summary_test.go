package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
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
	if err != nil || summary.ScannedRecords != 0 || summary.ByDay == nil || summary.ByService == nil {
		t.Fatalf("empty=%+v err=%v", summary, err)
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
	for _, groups := range [][]storage.UsageGroup{summary.ByService, summary.ByModel} {
		if len(groups) != 1 || groups[0].ID == nil || groups[0].Requests != 2 || groups[0].TotalTokens != 7 {
			t.Fatalf("groups=%+v", groups)
		}
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
