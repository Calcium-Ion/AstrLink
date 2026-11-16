package ingress

import (
	"reflect"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

func TestPrivacyRecordsKeepCustomKinds(t *testing.T) {
	hits := privacyHitCounts([]privacy.Redaction{
		{Placeholder: "<PRIVATE_CUSTOM_ORG_aaaaaaaaaaaaaaaa>", Kind: "custom_org"},
		{Placeholder: "<PRIVATE_CUSTOM_ORG_bbbbbbbbbbbbbbbb>", Kind: "custom_org"},
		{Placeholder: "<PRIVATE_CUSTOM_AGENCY_cccccccccccccccc>", Kind: "custom_agency"},
		{Placeholder: "<PRIVATE_ACCOUNT_NUMBER_dddddddddddddddd>", Kind: privacy.KindAccount},
		{Placeholder: "<PRIVATE_eeeeeeeeeeeeeeee>", Kind: "organization"},
	})
	want := []contract.PrivacyHitCount{
		{Kind: contract.CanonicalKindAccount, Count: 1},
		{Kind: "custom_agency", Count: 1},
		{Kind: "custom_org", Count: 2},
	}
	if !reflect.DeepEqual(hits, want) {
		t.Fatalf("hits = %#v, want %#v", hits, want)
	}
	if err := (contract.PrivacyRestoreSummary{Hits: hits}).Validate(); err != nil {
		t.Fatalf("recorded hits fail validation: %v", err)
	}

	findings := mergePrivacyFindings(nil, []privacy.Finding{
		{Kind: "custom_org", Path: "/input"}, {Kind: "custom_org", Path: "/input"},
	}, nil)
	if len(findings) != 1 || findings[0].Kind != "custom_org" || findings[0].Count != 2 {
		t.Fatalf("findings = %#v", findings)
	}
	ignored := mergePrivacyIgnored(nil, []privacy.Finding{
		{Kind: "custom_org", Suppression: privacy.SuppressionKindDisabled},
	}, nil)
	if len(ignored) != 1 || ignored[0].Kind != "custom_org" {
		t.Fatalf("ignored = %#v", ignored)
	}
}
