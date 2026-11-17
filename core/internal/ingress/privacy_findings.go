package ingress

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

// privacyFindingKeyPattern admits schema-like object keys. Any other key is
// caller-chosen (a tool argument named after an address, say) and is shown
// as "*" so a finding path never repeats request content.
var privacyFindingKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// privacyThreshold is the confidence threshold the policy judged matches
// against, or nil when the regex detector decided: its matches carry no
// confidence.
func privacyThreshold(policy privacy.Policy) *float64 {
	if policy.Mode != privacy.ModeLocalModel {
		return nil
	}
	threshold := policy.MinConfidence
	return &threshold
}

// widenPrivacyConfidence returns a new range that also covers confidence.
// The entry's range is replaced rather than changed in place, because record
// snapshots share it.
func widenPrivacyConfidence(current *contract.PrivacyConfidence, confidence float64, threshold *float64) *contract.PrivacyConfidence {
	if threshold == nil {
		return current
	}
	next := contract.PrivacyConfidence{Min: confidence, Max: confidence, Threshold: *threshold}
	if current != nil {
		next.Min = min(current.Min, confidence)
		next.Max = max(current.Max, confidence)
	}
	return &next
}

// mergePrivacyFindings counts accepted findings by kind and structural path.
// Paths are JSON pointers from the inspection segments; values never enter.
func mergePrivacyFindings(existing []contract.PrivacyFinding, findings []privacy.Finding, threshold *float64) []contract.PrivacyFinding {
	for _, finding := range findings {
		kind := contract.CanonicalKind(finding.Kind)
		if !kind.Recorded() {
			continue
		}
		path := privacyFindingPath(finding.Path)
		merged := false
		for index := range existing {
			if existing[index].Kind == kind && existing[index].JSONPath == path {
				existing[index].Count++
				existing[index].Confidence = widenPrivacyConfidence(existing[index].Confidence, finding.Confidence, threshold)
				merged = true
				break
			}
		}
		if merged {
			continue
		}
		if len(existing) >= contract.MaxPrivacyFindings {
			continue
		}
		existing = append(existing, contract.PrivacyFinding{
			Kind: kind, JSONPath: path, Count: 1,
			Confidence: widenPrivacyConfidence(nil, finding.Confidence, threshold),
		})
	}
	return existing
}

// mergePrivacyIgnored counts suppressed findings by kind and reason. A match
// suppressed because it is already a placeholder is skipped: it was never
// private content.
func mergePrivacyIgnored(existing []contract.PrivacyIgnored, suppressed []privacy.Finding, threshold *float64) []contract.PrivacyIgnored {
	for _, finding := range suppressed {
		kind := contract.CanonicalKind(finding.Kind)
		reason := contract.PrivacyIgnoreReason(finding.Suppression)
		if !kind.Recorded() || !reason.Valid() {
			continue
		}
		merged := false
		for index := range existing {
			if existing[index].Kind == kind && existing[index].Reason == reason {
				existing[index].Count++
				existing[index].Confidence = widenPrivacyConfidence(existing[index].Confidence, finding.Confidence, threshold)
				merged = true
				break
			}
		}
		if !merged && len(existing) < contract.MaxPrivacyFindings {
			existing = append(existing, contract.PrivacyIgnored{
				Kind: kind, Reason: reason, Count: 1,
				Confidence: widenPrivacyConfidence(nil, finding.Confidence, threshold),
			})
		}
	}
	return existing
}

func privacyFindingPath(pointer string) string {
	tokens := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	var builder strings.Builder
	for _, token := range tokens {
		if token == "" {
			continue
		}
		builder.WriteByte('/')
		if isPrivacyFindingIndex(token) || privacyFindingKeyPattern.MatchString(token) {
			builder.WriteString(token)
		} else {
			builder.WriteByte('*')
		}
	}
	path := builder.String()
	if path == "" {
		return "/"
	}
	path = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, path)
	if runes := []rune(path); len(runes) > contract.MaxPrivacyFindingPathRunes {
		path = string(runes[:contract.MaxPrivacyFindingPathRunes])
	}
	return path
}

func isPrivacyFindingIndex(token string) bool {
	if token == "" || len(token) > 9 {
		return false
	}
	for _, r := range token {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
