package privacy

import "testing"

func TestWarningSummaryContainsOnlyDeterministicCategoriesAndCounts(t *testing.T) {
	summary := WarningSummary([]Finding{
		{Kind: KindPhone},
		{Kind: KindEmail},
		{Kind: KindEmail},
		{Kind: Kind("unknown")},
	})
	if summary != "email=2,phone=1" {
		t.Fatalf("WarningSummary = %q", summary)
	}
}
