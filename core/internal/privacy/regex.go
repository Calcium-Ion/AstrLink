package privacy

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/QuantumNous/astrlink/core/contract"
)

const maxDetectorFindings = 4_096

type regexSpec struct {
	kind     Kind
	pattern  *regexp.Regexp
	validate func(string) bool
	boundary func(string, int, int) bool
}

type builtinRegexDefinition struct {
	kind     Kind
	pattern  string
	validate func(string) bool
	boundary func(string, int, int) bool
}

type RegexDetector struct {
	specs []regexSpec
}

func builtinRegexDefinitions() []builtinRegexDefinition {
	return []builtinRegexDefinition{
		{
			kind:    KindCommonSecret,
			pattern: `\b(?:sk-(?:proj-|ant-)?[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{35}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`,
		},
		{
			kind:    KindCommonSecret,
			pattern: `(?i)\b(?:api[_-]?key|access[_-]?token|secret|password)\s*[:=]\s*["']?[A-Za-z0-9_./+=-]{12,}`,
		},
		{
			kind:     KindPaymentCard,
			pattern:  `\b[0-9](?:[ -]?[0-9]){12,18}\b`,
			validate: validPaymentCard,
		},
		{
			kind:    KindEmail,
			pattern: `(?i)\b[A-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Z0-9](?:[A-Z0-9-]{0,61}[A-Z0-9])?(?:\.[A-Z0-9](?:[A-Z0-9-]{0,61}[A-Z0-9])?)+\b`,
		},
		{
			kind:     KindAccount,
			pattern:  `\b[A-Z]{2}[0-9]{2}[A-Z0-9]{11,30}\b`,
			validate: validIBAN,
		},
		{
			kind:     KindAccount,
			pattern:  `(?i)\b(?:account|acct)\s*(?:number|no\.?|#)?\s*[:=-]\s*[0-9][0-9 -]{6,22}[0-9]\b`,
			validate: validContextAccount,
		},
		{
			kind:     KindPhone,
			pattern:  `\+[1-9][0-9 ()-]{8,20}[0-9]`,
			validate: validPhone,
		},
		{
			kind:     KindPhone,
			pattern:  `(?:\([0-9]{2,4}\)[- .]?|\b[0-9]{2,4}[-.])[0-9]{3,4}[-.][0-9]{3,4}\b`,
			validate: validPhone,
		},
		{
			kind:     KindURL,
			pattern:  `https?://[^\s<>"']+`,
			validate: validHTTPURL,
		},
		{
			kind:     KindIPAddress,
			pattern:  `\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`,
			validate: validIPAddress,
		},
		{
			kind:     KindIPAddress,
			pattern:  `(?:[0-9A-Fa-f]{0,4}:){2,7}[0-9A-Fa-f]{0,4}`,
			validate: validIPv6Address,
			boundary: validIPv6Boundary,
		},
	}
}

// BuiltinRegexRules returns the fixed Regex catalog as kind/pattern pairs for
// control-plane seeding. Built-in validators are not included.
func BuiltinRegexRules() []contract.PolicyRegexRule {
	definitions := builtinRegexDefinitions()
	rules := make([]contract.PolicyRegexRule, 0, len(definitions))
	for _, definition := range definitions {
		rules = append(rules, contract.PolicyRegexRule{
			Kind:    string(definition.kind),
			Pattern: definition.pattern,
		})
	}
	return rules
}

func NewRegexDetector() *RegexDetector {
	definitions := builtinRegexDefinitions()
	specs := make([]regexSpec, 0, len(definitions))
	for _, definition := range definitions {
		specs = append(specs, regexSpec{
			kind:     definition.kind,
			pattern:  regexp.MustCompile(definition.pattern),
			validate: definition.validate,
			boundary: definition.boundary,
		})
	}
	return &RegexDetector{specs: specs}
}

// NewCustomRegexDetector compiles user-authored rules without built-in
// validators or boundary helpers.
func NewCustomRegexDetector(rules []contract.PolicyRegexRule) (*RegexDetector, error) {
	if len(rules) == 0 {
		return nil, fmt.Errorf("custom regex rules are required")
	}
	specs := make([]regexSpec, 0, len(rules))
	for index, rule := range rules {
		if err := rule.Validate(); err != nil {
			return nil, fmt.Errorf("custom regex rule %d: %w", index, err)
		}
		compiled, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, fmt.Errorf("custom regex rule %d: %w", index, err)
		}
		specs = append(specs, regexSpec{
			kind:    Kind(rule.Kind),
			pattern: compiled,
		})
	}
	return &RegexDetector{specs: specs}, nil
}

func (detector *RegexDetector) Detect(ctx context.Context, input DetectInput) ([]Finding, error) {
	if detector == nil {
		return nil, ErrDetectorUnavailable
	}
	candidates := make([]Finding, 0)
	for segmentIndex, segment := range input.Segments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, spec := range detector.specs {
			remaining := maxDetectorFindings - len(candidates)
			if remaining <= 0 {
				return nil, ErrDetectorLimit
			}
			locations := spec.pattern.FindAllStringIndex(segment.Value, remaining+1)
			if len(locations) > remaining {
				return nil, ErrDetectorLimit
			}
			for _, location := range locations {
				if spec.boundary != nil &&
					!spec.boundary(segment.Value, location[0], location[1]) {
					continue
				}
				matched := segment.Value[location[0]:location[1]]
				if spec.validate != nil && !spec.validate(matched) {
					continue
				}
				candidates = append(candidates, Finding{
					Segment:    segmentIndex,
					Start:      location[0],
					End:        location[1],
					Kind:       spec.kind,
					Confidence: 1,
				})
			}
		}
	}
	return selectDeterministicFindings(candidates), nil
}

func selectDeterministicFindings(candidates []Finding) []Finding {
	unique := make(map[Finding]struct{}, len(candidates))
	deduplicated := make([]Finding, 0, len(candidates))
	for _, candidate := range candidates {
		if _, exists := unique[candidate]; exists {
			continue
		}
		unique[candidate] = struct{}{}
		deduplicated = append(deduplicated, candidate)
	}
	sort.Slice(deduplicated, func(left, right int) bool {
		a, b := deduplicated[left], deduplicated[right]
		if a.Segment != b.Segment {
			return a.Segment < b.Segment
		}
		if priorityA, priorityB := kindPriority(a.Kind), kindPriority(b.Kind); priorityA != priorityB {
			return priorityA > priorityB
		}
		if lengthA, lengthB := a.End-a.Start, b.End-b.Start; lengthA != lengthB {
			return lengthA > lengthB
		}
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		return a.Kind < b.Kind
	})

	selected := make([]Finding, 0, len(deduplicated))
	for _, candidate := range deduplicated {
		overlap := false
		for _, existing := range selected {
			if candidate.Segment == existing.Segment &&
				candidate.Start < existing.End && existing.Start < candidate.End {
				overlap = true
				break
			}
		}
		if !overlap {
			selected = append(selected, candidate)
		}
	}
	sort.Slice(selected, func(left, right int) bool {
		a, b := selected[left], selected[right]
		if a.Segment != b.Segment {
			return a.Segment < b.Segment
		}
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.End != b.End {
			return a.End > b.End
		}
		return a.Kind < b.Kind
	})
	return selected
}

func validPaymentCard(value string) bool {
	digits := decimalDigits(value)
	if len(digits) < 13 || len(digits) > 19 || allSameByte(digits) {
		return false
	}
	sum := 0
	parity := len(digits) % 2
	for index, digit := range digits {
		number := int(digit - '0')
		if index%2 == parity {
			number *= 2
			if number > 9 {
				number -= 9
			}
		}
		sum += number
	}
	return sum%10 == 0
}

func validPhone(value string) bool {
	digits := decimalDigits(value)
	return len(digits) >= 10 && len(digits) <= 15 && !allSameByte(digits)
}

func validContextAccount(value string) bool {
	digits := decimalDigits(value)
	return len(digits) >= 8 && len(digits) <= 20 && !allSameByte(digits)
}

func validIBAN(value string) bool {
	compact := strings.ToUpper(strings.ReplaceAll(value, " ", ""))
	if len(compact) < 15 || len(compact) > 34 {
		return false
	}
	rearranged := compact[4:] + compact[:4]
	remainder := 0
	for _, character := range rearranged {
		switch {
		case character >= '0' && character <= '9':
			remainder = (remainder*10 + int(character-'0')) % 97
		case character >= 'A' && character <= 'Z':
			number := int(character-'A') + 10
			remainder = (remainder*100 + number) % 97
		default:
			return false
		}
	}
	return remainder == 1
}

func validIPAddress(value string) bool {
	return net.ParseIP(value) != nil
}

func validIPv6Address(value string) bool {
	if net.ParseIP(value) == nil || !strings.Contains(value, ":") {
		return false
	}
	// A two-colon, letters-only fragment is common in source code
	// (for example std::vector or foo::bar). Prefer a conservative miss over
	// corrupting code under the default redact action.
	return strings.ContainsAny(value, "0123456789") ||
		strings.Count(value, ":") >= 4
}

func validIPv6Boundary(value string, start, end int) bool {
	if start > 0 && isIdentifierOrColon(value[start-1]) {
		return false
	}
	return end >= len(value) || !isIdentifierOrColon(value[end])
}

func isIdentifierOrColon(value byte) bool {
	return value == ':' || value == '_' ||
		value >= '0' && value <= '9' ||
		value >= 'A' && value <= 'Z' ||
		value >= 'a' && value <= 'z'
}

func validHTTPURL(value string) bool {
	value = strings.TrimRightFunc(value, func(character rune) bool {
		return strings.ContainsRune(".,;:!?)]}", character)
	})
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != ""
}

func decimalDigits(value string) []byte {
	result := make([]byte, 0, len(value))
	for _, character := range value {
		if unicode.IsDigit(character) && character >= '0' && character <= '9' {
			result = append(result, byte(character))
		}
	}
	return result
}

func allSameByte(value []byte) bool {
	if len(value) == 0 {
		return true
	}
	for _, current := range value[1:] {
		if current != value[0] {
			return false
		}
	}
	return true
}

var _ Detector = (*RegexDetector)(nil)
