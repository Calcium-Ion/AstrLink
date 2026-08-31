package ingress

import (
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

var privacyHitKindOrder = []contract.CanonicalKind{
	contract.CanonicalKindEmail,
	contract.CanonicalKindPhone,
	contract.CanonicalKindAccount,
	contract.CanonicalKindPaymentCard,
	contract.CanonicalKindIPAddress,
	contract.CanonicalKindURL,
	contract.CanonicalKindCommonSecret,
	contract.CanonicalKindAddress,
	contract.CanonicalKindDate,
	contract.CanonicalKindPerson,
}

// privacyRestoreSummaryText renders the restore event. Tool-argument restores
// are called out separately because they mean the local agent was handed a real
// value to act on, which is a different event from the model quoting one back.
func privacyRestoreSummaryText(summary contract.PrivacyRestoreSummary) string {
	text := fmt.Sprintf("restore · %d/%d", summary.RestoredCount, summary.MappingCount)
	if summary.ToolArgumentRestoredCount > 0 {
		text += fmt.Sprintf(" · tool %d", summary.ToolArgumentRestoredCount)
	}
	return text
}

func uniqueRedactionMappingCount(redactions []privacy.Redaction) int {
	seen := make(map[string]struct{}, len(redactions))
	for _, redaction := range redactions {
		if redaction.Placeholder == "" {
			continue
		}
		seen[redaction.Placeholder] = struct{}{}
	}
	return len(seen)
}

func privacyHitCounts(redactions []privacy.Redaction) []contract.PrivacyHitCount {
	seen := make(map[string]privacy.Kind, len(redactions))
	for _, redaction := range redactions {
		if redaction.Placeholder == "" {
			continue
		}
		if _, exists := seen[redaction.Placeholder]; exists {
			continue
		}
		if !contract.CanonicalKind(redaction.Kind).Valid() {
			continue
		}
		seen[redaction.Placeholder] = redaction.Kind
	}
	counts := make(map[contract.CanonicalKind]int, len(privacyHitKindOrder))
	for _, kind := range seen {
		counts[contract.CanonicalKind(kind)]++
	}
	hits := make([]contract.PrivacyHitCount, 0, len(counts))
	for _, kind := range privacyHitKindOrder {
		if count := counts[kind]; count > 0 {
			hits = append(hits, contract.PrivacyHitCount{Kind: kind, Count: count})
		}
	}
	if len(hits) == 0 {
		return nil
	}
	return hits
}
