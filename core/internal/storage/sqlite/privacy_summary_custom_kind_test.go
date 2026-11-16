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

func TestPrivacySummaryCountsCustomKindsApart(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "privacy-summary-custom.db"))
	defer store.Close()
	ctx := context.Background()
	from := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	decision := func(value contract.PrivacyDecision) *contract.PrivacyDecision { return &value }
	records := []contract.RequestRecord{
		{ID: "request_custom_redacted", StartedAt: from.Add(time.Hour), Status: contract.RequestStatusSucceeded,
			PrivacyDecision: decision(contract.PrivacyDecisionRedact),
			PrivacyRestore: &contract.PrivacyRestoreSummary{Enabled: true, MappingCount: 4, Hits: []contract.PrivacyHitCount{
				{Kind: contract.CanonicalKindAccount, Count: 1}, {Kind: "custom_org", Count: 2}, {Kind: "custom_agency", Count: 1},
			}}},
		{ID: "request_custom_warned", StartedAt: from.Add(2 * time.Hour), Status: contract.RequestStatusSucceeded,
			PrivacyDecision: decision(contract.PrivacyDecisionWarn),
			PrivacyFindings: []contract.PrivacyFinding{{Kind: "custom_agency", JSONPath: "/input", Count: 1}},
			PrivacyIgnored:  []contract.PrivacyIgnored{{Kind: "custom_org", Reason: contract.PrivacyIgnoreKindDisabled, Count: 3}}},
	}
	for _, record := range records {
		record.InputProtocol = contract.ProtocolOpenAIResponses
		record.Audit = contract.NotCapturedAuditSummary()
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatalf("%s: %v", record.ID, err)
		}
	}
	summary, err := store.GetPrivacySummary(ctx, storage.PrivacySummaryOptions{
		From: from, To: from.AddDate(0, 0, 1), TimeZone: "UTC", Bucket: "day",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Each custom kind keeps its own row instead of folding into account;
	// on a tie a built-in kind comes first, then custom kinds by id.
	want := []storage.PrivacyKindCount{
		{Kind: "custom_agency", Count: 2},
		{Kind: "custom_org", Count: 2},
		{Kind: "account", Count: 1},
	}
	if !reflect.DeepEqual(summary.ByKind, want) {
		t.Fatalf("kinds = %+v, want %+v", summary.ByKind, want)
	}
	if summary.Totals.Redacted != 1 || summary.Totals.Warned != 1 {
		t.Fatalf("totals = %+v", summary.Totals)
	}
}
