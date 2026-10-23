package sqlite

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestPrivacySummaryCountsDecisionsValuesAndKinds(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "privacy-summary.db"))
	defer store.Close()
	ctx := context.Background()
	zone, _ := time.LoadLocation("Asia/Tokyo")
	from := time.Date(2026, 9, 19, 0, 0, 0, 0, zone)
	to := from.AddDate(0, 0, 1)
	decision := func(value contract.PrivacyDecision) *contract.PrivacyDecision { return &value }
	redacted := &contract.PrivacyRestoreSummary{
		Enabled: true, MappingCount: 3, RestoredCount: 4, VisibleRestoredCount: 3, ToolArgumentRestoredCount: 1, FallbackCount: 1,
		Hits: []contract.PrivacyHitCount{{Kind: contract.CanonicalKindEmail, Count: 2}, {Kind: contract.CanonicalKindPhone, Count: 1}},
	}
	secret := []contract.PrivacyFinding{{Kind: contract.CanonicalKindCommonSecret, JSONPath: "/input/0/content", Count: 2}}
	allowlisted := []contract.PrivacyIgnored{
		{Kind: contract.CanonicalKindEmail, Reason: contract.PrivacyIgnoreAllowlisted, Count: 2},
		{Kind: contract.CanonicalKindURL, Reason: contract.PrivacyIgnoreKindDisabled, Count: 5},
	}
	for _, test := range []struct {
		id       string
		status   contract.RequestStatus
		at       time.Time
		decision *contract.PrivacyDecision
		restore  *contract.PrivacyRestoreSummary
		findings []contract.PrivacyFinding
		ignored  []contract.PrivacyIgnored
		child    bool
		protocol contract.ProtocolID
	}{
		{id: "redacted", status: contract.RequestStatusSucceeded, at: from.Add(time.Hour), decision: decision(contract.PrivacyDecisionRedact), restore: redacted, findings: secret,
			ignored: []contract.PrivacyIgnored{{Kind: contract.CanonicalKindPerson, Reason: contract.PrivacyIgnoreLowConfidence, Count: 3}}},
		{id: "redacted_failed", status: contract.RequestStatusFailed, at: from.Add(time.Hour), decision: decision(contract.PrivacyDecisionRedact), restore: redacted},
		{id: "blocked", status: contract.RequestStatusBlocked, at: from.Add(2 * time.Hour), decision: decision(contract.PrivacyDecisionBlock), findings: secret},
		{id: "warned", status: contract.RequestStatusSucceeded, at: from.Add(2 * time.Hour), decision: decision(contract.PrivacyDecisionWarn), findings: secret},
		{id: "allowed", status: contract.RequestStatusCancelled, at: from, decision: decision(contract.PrivacyDecisionAllow)},
		// A disabled kind is the user's choice, not a match left behind.
		{id: "allowed_disabled_kind", status: contract.RequestStatusSucceeded, at: from, decision: decision(contract.PrivacyDecisionAllow),
			ignored: []contract.PrivacyIgnored{{Kind: contract.CanonicalKindURL, Reason: contract.PrivacyIgnoreKindDisabled, Count: 4}}},
		// AstrLink's own identifiers hold nothing to protect either.
		{id: "allowed_own_identifier", status: contract.RequestStatusSucceeded, at: from, decision: decision(contract.PrivacyDecisionAllow),
			ignored: []contract.PrivacyIgnored{{Kind: contract.CanonicalKindCommonSecret, Reason: contract.PrivacyIgnoreAstrLinkIdentifier, Count: 2}}},
		{id: "ignored", status: contract.RequestStatusSucceeded, at: from, decision: decision(contract.PrivacyDecisionAllow), ignored: allowlisted},
		{id: "fail_open", status: contract.RequestStatusSucceeded, at: from, decision: decision(contract.PrivacyDecisionFailOpen)},
		{id: "off", status: contract.RequestStatusSucceeded, at: from, decision: decision(contract.PrivacyDecisionNone)},
		{id: "trusted", status: contract.RequestStatusSucceeded, at: from, decision: decision(contract.PrivacyDecisionTrusted)},
		// Rows from before decisions were recorded keep their own evidence.
		{id: "legacy_blocked", status: contract.RequestStatusBlocked, at: from},
		{id: "legacy_redacted", status: contract.RequestStatusSucceeded, at: from, restore: &contract.PrivacyRestoreSummary{Enabled: true, MappingCount: 1, RestoredCount: 1}},
		{id: "legacy_unknown", status: contract.RequestStatusSucceeded, at: from},
		// None of these is a finished root call in the window.
		{id: "pending", status: contract.RequestStatusPending, at: from, decision: decision(contract.PrivacyDecisionRedact), restore: redacted},
		{id: "child", status: contract.RequestStatusSucceeded, at: from, decision: decision(contract.PrivacyDecisionRedact), restore: redacted, child: true},
		{id: "discovery", status: contract.RequestStatusSucceeded, at: from, decision: decision(contract.PrivacyDecisionAllow), protocol: contract.ProtocolOpenAIModels},
		{id: "before", status: contract.RequestStatusSucceeded, at: from.Add(-time.Nanosecond), decision: decision(contract.PrivacyDecisionRedact), restore: redacted},
		{id: "after", status: contract.RequestStatusSucceeded, at: to, decision: decision(contract.PrivacyDecisionRedact), restore: redacted},
	} {
		record := contract.RequestRecord{ID: contract.RequestID("request_" + test.id), StartedAt: test.at, Status: test.status,
			InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
			PrivacyDecision: test.decision, PrivacyRestore: test.restore, PrivacyFindings: test.findings, PrivacyIgnored: test.ignored}
		if test.protocol != "" {
			record.InputProtocol = test.protocol
		}
		if test.child {
			parent := contract.RequestID("request_redacted")
			record.ParentRequestID, record.AttemptIndex = &parent, 1
		}
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatalf("%s: %v", test.id, err)
		}
	}

	summary, err := store.GetPrivacySummary(ctx, storage.PrivacySummaryOptions{From: from, To: to, TimeZone: "Asia/Tokyo", Bucket: "hour"})
	if err != nil {
		t.Fatal(err)
	}
	want := storage.PrivacyTotals{Requests: 14, Allowed: 3, Ignored: 1, Warned: 1, Redacted: 3, Blocked: 2, FailOpen: 1, Trusted: 1,
		Replaced: 7, Restored: 9, RestoredVisible: 6, RestoredToolArguments: 2, Unrestored: 2,
		IgnoredAllowlisted: 2, IgnoredLowConfidence: 3}
	if summary.Totals != want {
		t.Fatalf("totals = %+v, want %+v", summary.Totals, want)
	}
	// Hits name a redacted call's kinds even where findings exist too; the
	// warned and blocked calls add their findings. Ties keep canonical order.
	wantKinds := []storage.PrivacyKindCount{
		{Kind: "common_secret", Count: 4},
		{Kind: "email", Count: 4},
		{Kind: "phone", Count: 2},
	}
	if !reflect.DeepEqual(summary.ByKind, wantKinds) {
		t.Fatalf("kinds = %+v", summary.ByKind)
	}
	if len(summary.ByDay) != 1 || summary.ByDay[0].Date != "2026-09-19" || summary.ByDay[0].PrivacyTotals != want {
		t.Fatalf("days = %+v", summary.ByDay)
	}
	if len(summary.ByHour) != 3 || *summary.ByHour[1].Hour != 1 || summary.ByHour[1].Redacted != 2 || summary.ByHour[1].Replaced != 6 ||
		*summary.ByHour[2].Hour != 2 || summary.ByHour[2].Blocked != 1 || summary.ByHour[2].Warned != 1 {
		t.Fatalf("hours = %+v", summary.ByHour)
	}

	days, err := store.GetPrivacySummary(ctx, storage.PrivacySummaryOptions{From: from, To: to, TimeZone: "Asia/Tokyo", Bucket: "day"})
	if err != nil || len(days.ByHour) != 0 || days.Totals != want {
		t.Fatalf("day buckets = %+v, %v", days, err)
	}
	if _, err := store.PurgeRequestRecords(ctx, contract.PurgeRequest{Scope: contract.PurgeScopeAll, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	empty, err := store.GetPrivacySummary(ctx, storage.PrivacySummaryOptions{From: from, To: to, TimeZone: "Asia/Tokyo", Bucket: "day"})
	if err != nil || empty.Totals != (storage.PrivacyTotals{}) || empty.ByDay == nil || empty.ByHour == nil || empty.ByKind == nil {
		t.Fatalf("empty = %+v, %v", empty, err)
	}
}

func TestPrivacySummaryRejectsInvalidWindows(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "privacy-window.db"))
	defer store.Close()
	from := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	for _, options := range []storage.PrivacySummaryOptions{
		{From: from, To: from, TimeZone: "UTC", Bucket: "day"},
		{From: from, To: from.AddDate(0, 0, 1), TimeZone: "UTC", Bucket: "week"},
		{From: from, To: from.AddDate(0, 0, 1), TimeZone: "Local", Bucket: "day"},
	} {
		if _, err := store.GetPrivacySummary(context.Background(), options); err == nil {
			t.Fatalf("options %+v were accepted", options)
		}
	}
}
