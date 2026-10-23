package privacy

import (
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type Engine struct {
	provider      PolicyProvider
	regexDetector Detector
	modelDetector Detector
	// derivationKey keys the placeholder HMAC. Empty means the process-wide key.
	derivationKey []byte
	standIns      standInLedgers
}

func New(provider PolicyProvider, modelDetector Detector) (*Engine, error) {
	if provider == nil {
		return nil, fmt.Errorf("privacy policy provider is required")
	}
	return &Engine{
		provider:      provider,
		regexDetector: NewRegexDetector(),
		modelDetector: modelDetector,
	}, nil
}

// placeholderKeyInfo is the HKDF info string that separates the placeholder
// key from every other use of the audit data key. Changing it renames every
// value once.
const placeholderKeyInfo = "astrlink/privacy-placeholder/v1"

// PersistPlaceholders derives the placeholder key from the device's audit data
// key and keeps each conversation's IPv4 stand-ins in store, so a restart
// gives no value a new name: the history a client sends back then matches
// the upstream prompt cache, and a placeholder quoted in unrestored reasoning
// is still the one the response path restores. Call it before the engine
// serves requests.
func (engine *Engine) PersistPlaceholders(auditKey []byte, store storage.PrivacyStandInStore) error {
	if len(auditKey) == 0 || store == nil {
		return fmt.Errorf("privacy: an audit key and a stand-in store are required")
	}
	key, err := hkdf.Key(sha256.New, auditKey, nil, placeholderKeyInfo, sha256.Size)
	if err != nil {
		return fmt.Errorf("privacy: derive the placeholder key: %w", err)
	}
	engine.derivationKey = key
	engine.standIns.store = store
	return nil
}

func (engine *Engine) ResolvePolicy(ctx context.Context, scope Scope) (Policy, error) {
	policy, err := engine.provider.RequestPolicy(ctx, scope)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return Policy{}, err
		}
		return Policy{}, ErrPolicyUnavailable
	}
	if !policy.Enabled {
		return Policy{}, nil
	}
	if !policy.Mode.Valid() || !policy.Action.Valid() ||
		!validMinConfidence(policy.MinConfidence) {
		return Policy{}, ErrPolicyUnavailable
	}
	if policy.Mode == ModeLocalModel && policy.LocalModelID.Validate() != nil {
		return Policy{}, ErrPolicyUnavailable
	}
	if policy.Action == ActionAllow {
		return Policy{}, nil
	}
	return policy, nil
}

func (engine *Engine) Inspect(ctx context.Context, policy Policy, protocol contract.ProtocolID, body []byte) (Result, error) {
	if !policy.Enabled || policy.Action == ActionAllow {
		return Result{Decision: DecisionAllow}, nil
	}
	if !policy.Mode.Valid() || !policy.Action.Valid() ||
		!validMinConfidence(policy.MinConfidence) {
		return Result{}, ErrPolicyUnavailable
	}
	if policy.Mode == ModeLocalModel && policy.LocalModelID.Validate() != nil {
		return Result{}, ErrPolicyUnavailable
	}
	document, extracted, err := extractDocument(protocol, body, policy.InspectionOptions())
	if err != nil {
		return Result{}, err
	}
	if len(extracted) == 0 {
		return Result{Decision: DecisionAllow}, nil
	}

	segments := make([]Segment, len(extracted))
	for index := range extracted {
		segments[index] = extracted[index].Segment
	}
	detector := engine.regexDetector
	if policy.Mode == ModeLocalModel {
		detector = engine.modelDetector
		if detector == nil {
			return Result{}, ErrDetectorUnavailable
		}
	} else if policy.RegexSource == contract.PolicyRegexSourceCustom {
		custom, err := NewCustomRegexDetector(policy.CustomRegexRules)
		if err != nil {
			return Result{}, ErrPolicyUnavailable
		}
		detector = custom
	}
	findings, err := detector.Detect(ctx, DetectInput{
		Protocol:             protocol,
		ExpectedLocalModelID: policy.LocalModelID,
		Segments:             segments,
	})
	if err != nil {
		return Result{}, normalizeDetectorError(ctx, err)
	}
	findings, err = normalizeFindings(findings, segments)
	if err != nil {
		return Result{}, ErrDetectorUnavailable
	}
	findings = alignStructuredFindings(extracted, findings)
	accepted, suppressed := engine.partitionFindings(policy, findings, segments)
	if len(accepted) == 0 {
		return Result{
			Decision:           DecisionAllow,
			SuppressedFindings: suppressed,
		}, nil
	}

	switch policy.Action {
	case ActionWarn:
		notified := notifiedFindings(policy, accepted)
		return Result{
			Decision:           DecisionWarn,
			Findings:           accepted,
			SuppressedFindings: suppressed,
			Notified:           notified,
			Fingerprints:       warningFingerprints(notified, segments),
		}, nil
	case ActionBlock:
		return Result{
			Decision:           DecisionBlock,
			Findings:           accepted,
			SuppressedFindings: suppressed,
			Protected:          protectedValues(accepted, segments),
		}, nil
	case ActionRedact:
		if !utf8.Valid(body) || document.duplicateKeys {
			return Result{
				Findings:           accepted,
				SuppressedFindings: suppressed,
			}, ErrUnsafeRewrite
		}
		allocator := newPlaceholderAllocator(engine.derivationKey, policy.KindRule, body)
		if slices.ContainsFunc(accepted, func(finding Finding) bool { return finding.Kind == KindIPAddress }) {
			allocator.ledger = engine.standIns.conversation(ctx, conversationFrom(ctx))
		}
		skillListed := placeholderSkillListed(protocol, body)
		notice := ""
		if policy.PlaceholderNotice && !skillListed {
			notice = placeholderNoticeText(policy.ResponseRestore && policy.RestoreToolArguments)
		}
		outcome, err := rewriteDocument(
			document,
			extracted,
			accepted,
			allocator,
			protocol,
			notice,
		)
		if err != nil {
			return Result{
				Findings:           accepted,
				SuppressedFindings: suppressed,
			}, ErrUnsafeRewrite
		}
		allocator.ledger.save(ctx)
		return Result{
			Decision:           DecisionRedact,
			Body:               outcome.Body,
			Findings:           markExhaustedKinds(accepted, allocator.exhaustedKinds()),
			SuppressedFindings: suppressed,
			Redactions:         outcome.Redactions,
			Protected:          protectedValues(accepted, segments),
			NoticeInjected:     outcome.NoticeInjected,
			SkillListed:        skillListed,
		}, nil
	default:
		return Result{}, ErrPolicyUnavailable
	}
}

// partitionFindings applies every suppression rule through one path so that a
// dry-run and the desktop inspector can always report why a match was left
// alone rather than showing an unexplained gap.
func (engine *Engine) partitionFindings(
	policy Policy,
	findings []Finding,
	segments []Segment,
) ([]Finding, []Finding) {
	list := newAllowlist(policy.Allowlist)
	accepted := make([]Finding, 0, len(findings))
	suppressed := make([]Finding, 0)
	for _, finding := range findings {
		if finding.Segment >= 0 && finding.Segment < len(segments) {
			finding.Path = segments[finding.Segment].Path
		}
		if reason := suppressionFor(policy, list, finding, segments); reason != "" {
			finding.Suppression = reason
			suppressed = append(suppressed, finding)
			continue
		}
		accepted = append(accepted, finding)
	}
	return accepted, suppressed
}

func suppressionFor(
	policy Policy,
	list *allowlist,
	finding Finding,
	segments []Segment,
) SuppressionReason {
	if !policy.KindRule(finding.Kind).Enabled {
		return SuppressionKindDisabled
	}
	if policy.Mode == ModeLocalModel && finding.Confidence < policy.MinConfidence {
		return SuppressionLowConfidence
	}
	if finding.Segment < 0 || finding.Segment >= len(segments) {
		return ""
	}
	value := segments[finding.Segment].Value[finding.Start:finding.End]
	if isNaturalPlaceholder(finding.Kind, value) || isTokenPlaceholder(value) {
		return SuppressionPlaceholder
	}
	if list.allows(finding.Kind, value) {
		return SuppressionAllowlisted
	}
	// Whatever kind the detector assigned, a span that covers one of
	// AstrLink's own identifiers, or part of one, and nothing else holds no
	// private value.
	if isAstrLinkIdentifierSpan(segments[finding.Segment].Value, finding.Start, finding.End) {
		return SuppressionAstrLinkIdentifier
	}
	return ""
}

// protectedValues lists the distinct plaintext the findings cover.
func protectedValues(findings []Finding, segments []Segment) []string {
	seen := make(map[string]struct{}, len(findings))
	values := make([]string, 0, len(findings))
	for _, finding := range findings {
		value := segments[finding.Segment].Value[finding.Start:finding.End]
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

// markExhaustedKinds annotates accepted findings whose kind ran out of reserved
// stand-ins, so the operator can see that those spans silently reverted to token
// placeholders instead of the configured natural shape.
func markExhaustedKinds(findings []Finding, exhausted map[Kind]bool) []Finding {
	if len(exhausted) == 0 {
		return findings
	}
	for index := range findings {
		if exhausted[findings[index].Kind] {
			findings[index].Suppression = SuppressionUnrepresentable
		}
	}
	return findings
}

func normalizeDetectorError(ctx context.Context, err error) error {
	switch {
	case errors.Is(ctx.Err(), context.Canceled), errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(ctx.Err(), context.DeadlineExceeded),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, ErrDetectorTimeout):
		return ErrDetectorTimeout
	case errors.Is(err, ErrDetectorLimit):
		return ErrDetectorLimit
	case errors.Is(err, ErrDetectorUnavailable):
		return ErrDetectorUnavailable
	default:
		return ErrDetectorUnavailable
	}
}

func normalizeFindings(findings []Finding, segments []Segment) ([]Finding, error) {
	// Each value is validated once; a span then only has to fall on rune
	// boundaries. Validating the prefix of every span instead costs the length
	// of the text for each one.
	validated := make(map[int]bool)
	for _, finding := range findings {
		if finding.Segment < 0 || finding.Segment >= len(segments) ||
			finding.Start < 0 || finding.End <= finding.Start ||
			finding.End > len(segments[finding.Segment].Value) ||
			!validKind(finding.Kind) ||
			!validMinConfidence(finding.Confidence) {
			return nil, ErrDetectorUnavailable
		}
		value := segments[finding.Segment].Value
		if !validated[finding.Segment] {
			if !utf8.ValidString(value) {
				return nil, ErrDetectorUnavailable
			}
			validated[finding.Segment] = true
		}
		if !utf8.RuneStart(value[finding.Start]) ||
			(finding.End < len(value) && !utf8.RuneStart(value[finding.End])) {
			return nil, ErrDetectorUnavailable
		}
	}
	return uniqueFindings(findings), nil
}

// uniqueFindings keeps the most confident report of each span and kind, in
// segment and offset order.
func uniqueFindings(findings []Finding) []Finding {
	type findingIdentity struct {
		Segment int
		Start   int
		End     int
		Kind    Kind
	}
	unique := make(map[findingIdentity]Finding, len(findings))
	for _, finding := range findings {
		identity := findingIdentity{
			Segment: finding.Segment,
			Start:   finding.Start,
			End:     finding.End,
			Kind:    finding.Kind,
		}
		if previous, exists := unique[identity]; !exists ||
			finding.Confidence > previous.Confidence {
			unique[identity] = finding
		}
	}
	normalized := make([]Finding, 0, len(unique))
	for _, finding := range unique {
		normalized = append(normalized, finding)
	}
	sort.Slice(normalized, func(left, right int) bool {
		a, b := normalized[left], normalized[right]
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
	return normalized
}

func validMinConfidence(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

func validKind(kind Kind) bool {
	switch kind {
	case KindEmail, KindPhone, KindAccount, KindPaymentCard,
		KindIPAddress, KindURL, KindCommonSecret, KindAddress, KindDate, KindPerson:
		return true
	default:
		return false
	}
}

var _ Filter = (*Engine)(nil)
