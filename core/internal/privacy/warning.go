package privacy

import (
	"strconv"
	"strings"
)

// WarningSummary contains only stable detector categories and occurrence
// counts. It is safe for a local response header and never includes plaintext.
func WarningSummary(findings []Finding) string {
	counts := make(map[Kind]int)
	for _, finding := range findings {
		if validKind(finding.Kind) {
			counts[finding.Kind]++
		}
	}
	order := []Kind{
		KindAccount,
		KindCommonSecret,
		KindEmail,
		KindIPAddress,
		KindPaymentCard,
		KindPhone,
		KindURL,
		KindAddress,
		KindDate,
		KindPerson,
	}
	parts := make([]string, 0, len(counts))
	for _, kind := range order {
		if count := counts[kind]; count > 0 {
			parts = append(parts, string(kind)+"="+strconv.Itoa(count))
		}
	}
	return strings.Join(parts, ",")
}
