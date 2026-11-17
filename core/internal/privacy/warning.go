package privacy

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/astrlink/core/contract"
)

// WarningSummary contains only stable detector categories and occurrence
// counts. It is safe for a local response header and never includes plaintext.
func WarningSummary(findings []Finding) string {
	counts := warningKindCounts(findings)
	parts := make([]string, 0, len(counts))
	for _, count := range counts {
		parts = append(parts, string(count.Kind)+"="+strconv.Itoa(count.Count))
	}
	return strings.Join(parts, ",")
}

// WarningKindCount is how often one detector category matched.
type WarningKindCount struct {
	Kind  Kind `json:"kind"`
	Count int  `json:"count"`
}

// warningKindCounts counts findings by kind, in a fixed order, skipping kinds
// this build does not know.
func warningKindCounts(findings []Finding) []WarningKindCount {
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
	result := make([]WarningKindCount, 0, len(counts))
	for _, kind := range order {
		if count := counts[kind]; count > 0 {
			result = append(result, WarningKindCount{Kind: kind, Count: count})
		}
	}
	// Custom kinds follow the built-in ones, by name.
	custom := make([]Kind, 0)
	for kind := range counts {
		if contract.IsCustomPrivacyKind(string(kind)) {
			custom = append(custom, kind)
		}
	}
	slices.Sort(custom)
	for _, kind := range custom {
		result = append(result, WarningKindCount{Kind: kind, Count: counts[kind]})
	}
	return result
}

// Fingerprint is a keyed digest of one value a warn decision let through. It
// tells the same value apart from a different one within this process, and is
// never logged, stored, or sent anywhere.
type Fingerprint [sha256.Size]byte

// fingerprintKey keys every Fingerprint, because a bare digest of an email
// address or a short secret is reversed by enumeration. It lives in memory for
// the life of the process; a restart rotates it.
var fingerprintKey = sync.OnceValue(func() []byte {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		panic("privacy: warning fingerprint key is unavailable: " + err.Error())
	}
	return key
})

// notifiedFindings keeps the findings whose kind rule notifies.
func notifiedFindings(policy Policy, findings []Finding) []Finding {
	notified := make([]Finding, 0, len(findings))
	for _, finding := range findings {
		if policy.KindRule(finding.Kind).Notify {
			notified = append(notified, finding)
		}
	}
	return notified
}

// warningFingerprints digests the distinct values the findings cover.
func warningFingerprints(findings []Finding, segments []Segment) []Fingerprint {
	values := protectedValues(findings, segments)
	fingerprints := make([]Fingerprint, 0, len(values))
	for _, value := range values {
		mac := hmac.New(sha256.New, fingerprintKey())
		mac.Write([]byte(value))
		var fingerprint Fingerprint
		mac.Sum(fingerprint[:0])
		fingerprints = append(fingerprints, fingerprint)
	}
	return fingerprints
}
