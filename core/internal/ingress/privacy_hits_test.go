package ingress

import (
	"reflect"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

func TestPrivacyHitCountsDedupesPlaceholdersByKind(t *testing.T) {
	hits := privacyHitCounts([]privacy.Redaction{
		{Placeholder: "<PRIVATE_EMAIL_aaaaaaaaaaaaaaaa>", Kind: privacy.KindEmail, Value: "alice@example.com"},
		{Placeholder: "<PRIVATE_EMAIL_aaaaaaaaaaaaaaaa>", Kind: privacy.KindEmail, Value: "alice@example.com"},
		{Placeholder: "<PRIVATE_EMAIL_bbbbbbbbbbbbbbbb>", Kind: privacy.KindEmail, Value: "bob@example.com"},
		{Placeholder: "<PRIVATE_URL_cccccccccccccccc>", Kind: privacy.KindURL, Value: "https://example.com"},
		{Placeholder: "", Kind: privacy.KindPhone, Value: "+1-415-555-0001"},
	})
	want := []contract.PrivacyHitCount{
		{Kind: contract.CanonicalKindEmail, Count: 2},
		{Kind: contract.CanonicalKindURL, Count: 1},
	}
	if !reflect.DeepEqual(hits, want) {
		t.Fatalf("hits=%#v want=%#v", hits, want)
	}
	if uniqueRedactionMappingCount([]privacy.Redaction{
		{Placeholder: "<PRIVATE_EMAIL_aaaaaaaaaaaaaaaa>", Kind: privacy.KindEmail},
		{Placeholder: "<PRIVATE_EMAIL_aaaaaaaaaaaaaaaa>", Kind: privacy.KindEmail},
		{Placeholder: "<PRIVATE_EMAIL_bbbbbbbbbbbbbbbb>", Kind: privacy.KindEmail},
		{Placeholder: "<PRIVATE_URL_cccccccccccccccc>", Kind: privacy.KindURL},
	}) != 3 {
		t.Fatal("mapping count should match unique placeholders")
	}
}
