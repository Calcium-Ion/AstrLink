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

// regexValueGroup names the capture group that narrows a finding to part of
// a match. A rule such as token=(?P<value>...) then replaces the credential
// and leaves the key name readable. Other groups, named or not, do not change
// the span.
const regexValueGroup = "value"

type regexSpec struct {
	kind    Kind
	pattern *regexp.Regexp
	// values lists the groups named regexValueGroup, leftmost first. Empty
	// when the whole match is the finding.
	values []int
	// validate and boundary see the reported span: the value group when the
	// pattern has one, otherwise the whole match.
	validate func(string) bool
	boundary func(string, int, int) bool
}

func newRegexSpec(
	kind Kind,
	pattern *regexp.Regexp,
	validate func(string) bool,
	boundary func(string, int, int) bool,
) regexSpec {
	spec := regexSpec{kind: kind, pattern: pattern, validate: validate, boundary: boundary}
	for index, name := range pattern.SubexpNames() {
		if name == regexValueGroup {
			spec.values = append(spec.values, index)
		}
	}
	return spec
}

// spans returns the reported byte range of every match in text. A pattern
// with a value group reports the first such group that took part in the
// match, so alternatives can each name their own value. A match whose value
// group is empty or did not take part holds nothing to replace and reports
// nothing.
func (spec regexSpec) spans(text string) [][]int {
	if len(spec.values) == 0 {
		return spec.pattern.FindAllStringIndex(text, -1)
	}
	matches := spec.pattern.FindAllStringSubmatchIndex(text, -1)
	spans := make([][]int, 0, len(matches))
	for _, match := range matches {
		for _, group := range spec.values {
			start, end := match[2*group], match[2*group+1]
			if start < 0 {
				continue
			}
			if end > start {
				spans = append(spans, match[2*group:2*group+2])
			}
			break
		}
	}
	return spans
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
			kind: KindCommonSecret,
			// accesstoken.Manager mints a local access token as astr_ and 43
			// base64url characters. A token can end in - or _, where \b does
			// not hold, so the boundary check rejects a match that is part of
			// a longer base64url run instead, on both sides. Leaving \b out
			// also keeps the literal prefix that lets this rule skip text
			// quickly.
			pattern:  `astr_[A-Za-z0-9_-]{43}`,
			boundary: validBase64URLRunBoundary,
		},
		{
			kind: KindCommonSecret,
			// An HTTP bearer credential (RFC 6750 b64token, which covers
			// JWTs). Only the token is replaced; "Bearer" stays.
			pattern: `(?i)\bbearer[ \t]+(?P<value>[A-Za-z0-9._~+/-]{16,}=*)`,
		},
		{
			kind: KindCommonSecret,
			// A credential assigned to a key in prose, code, env files, YAML,
			// JSON (the key's closing quote comes before the colon), query
			// strings, and headers such as X-API-Key. Only the value is
			// replaced, so the key name stays readable. \b keeps compound
			// keys such as max_tokens= and prompt_tokens out.
			pattern: `(?i)\b(?:api[_-]?key|(?:(?:access|auth|refresh)[_-]?)?token|secret|password)["']?\s*[:=]\s*["']?(?P<value>[A-Za-z0-9_./+=-]{12,})`,
		},
		{
			kind:     KindPaymentCard,
			pattern:  `\b[0-9](?:[ -]?[0-9]){12,18}\b`,
			validate: validPaymentCard,
			boundary: validPaymentCardBoundary,
		},
		{
			kind: KindEmail,
			// A numeric final domain label is typically a package version, such
			// as package@1.2.3. Keep alphabetic and punycode domain suffixes.
			pattern:  `(?i)\b[A-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@(?:[A-Z0-9](?:[A-Z0-9-]{0,61}[A-Z0-9])?\.)+(?:XN--[A-Z0-9](?:[A-Z0-9-]{0,57}[A-Z0-9])?|[A-Z]{2,63})\b`,
			boundary: validEmailBoundary,
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
// control-plane seeding. Built-in validators and boundary checks are not
// included; value groups are part of the pattern and carry over.
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
		specs = append(specs, newRegexSpec(
			definition.kind,
			regexp.MustCompile(definition.pattern),
			definition.validate,
			definition.boundary,
		))
	}
	return &RegexDetector{specs: specs}
}

// NewCustomRegexDetector compiles user-authored rules without built-in
// validators or boundary helpers. A rule with a group named value reports
// only that group; any other rule reports its whole match.
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
		specs = append(specs, newRegexSpec(Kind(rule.Kind), compiled, nil, nil))
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
			for _, location := range spec.spans(segment.Value) {
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
	for begin := 0; begin < len(deduplicated); {
		end := begin + 1
		for end < len(deduplicated) && deduplicated[end].Segment == deduplicated[begin].Segment {
			end++
		}
		segmentCandidates := deduplicated[begin:end]
		selector := newSpanSelector(segmentCandidates)
		for _, candidate := range segmentCandidates {
			if selector.accept(candidate) {
				selected = append(selected, candidate)
			}
		}
		begin = end
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
	if !validPaymentCardGrouping(value) {
		return false
	}
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

func validPaymentCardGrouping(value string) bool {
	separator := " "
	if strings.Contains(value, "-") {
		if strings.Contains(value, " ") {
			return false
		}
		separator = "-"
	}
	groups := strings.Split(value, separator)
	if len(groups) == 1 {
		return true
	}
	// Luhn alone accepts roughly one in ten arbitrary digit sequences. SVG
	// coordinates must not become cards just because their digits concatenate
	// to a valid checksum. Accept common PAN layouts: groups of four, or 4-6-5
	// / 4-6-4. The digit count and checksum are still checked separately.
	if len(groups) == 3 && len(groups[0]) == 4 && len(groups[1]) == 6 {
		return len(groups[2]) == 4 || len(groups[2]) == 5
	}
	if len(groups) < 4 || len(groups) > 5 {
		return false
	}
	for _, group := range groups[:len(groups)-1] {
		if len(group) != 4 {
			return false
		}
	}
	last := groups[len(groups)-1]
	return len(last) >= 1 && len(last) <= 4
}

func validPaymentCardBoundary(value string, start, end int) bool {
	// Do not turn the fractional or integral part of a decimal into a card.
	if start >= 2 && value[start-1] == '.' && isDecimalByte(value[start-2]) {
		return false
	}
	return end+1 >= len(value) || value[end] != '.' || !isDecimalByte(value[end+1])
}

func isDecimalByte(value byte) bool {
	return value >= '0' && value <= '9'
}

func validEmailBoundary(value string, _, end int) bool {
	if end >= len(value) {
		return true
	}
	// Avoid accepting only the alphabetic prefix of a longer domain label or
	// version suffix. A sentence-ending dot is still outside the email span.
	if value[end] == '-' {
		return false
	}
	return value[end] != '.' || end+1 == len(value) ||
		!isIdentifierOrColon(value[end+1]) && value[end+1] != '-'
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

// validBase64URLRunBoundary accepts a match only when it is the whole run of
// base64url characters around it.
func validBase64URLRunBoundary(value string, start, end int) bool {
	return (start == 0 || !isBase64URLByte(value[start-1])) &&
		(end == len(value) || !isBase64URLByte(value[end]))
}

func isBase64URLByte(value byte) bool {
	return value == '-' || value == '_' ||
		value >= '0' && value <= '9' ||
		value >= 'A' && value <= 'Z' ||
		value >= 'a' && value <= 'z'
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
