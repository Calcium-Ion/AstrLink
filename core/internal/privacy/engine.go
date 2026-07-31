package privacy

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
)

type Engine struct {
	provider           PolicyProvider
	regexDetector      Detector
	modelDetector      Detector
	placeholderEntropy io.Reader
}

func New(provider PolicyProvider, modelDetector Detector) (*Engine, error) {
	if provider == nil {
		return nil, fmt.Errorf("privacy policy provider is required")
	}
	return &Engine{
		provider:           provider,
		regexDetector:      NewRegexDetector(),
		modelDetector:      modelDetector,
		placeholderEntropy: rand.Reader,
	}, nil
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
	document, extracted, err := extractDocument(protocol, body)
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
		if errors.Is(err, ErrDetectorLimit) {
			return Result{}, ErrDetectorLimit
		}
		return Result{}, ErrDetectorUnavailable
	}
	accepted := findings
	var suppressed []Finding
	if policy.Mode == ModeLocalModel {
		accepted = make([]Finding, 0, len(findings))
		suppressed = make([]Finding, 0, len(findings))
		for _, finding := range findings {
			if finding.Confidence >= policy.MinConfidence {
				accepted = append(accepted, finding)
			} else {
				suppressed = append(suppressed, finding)
			}
		}
	}
	if len(accepted) == 0 {
		return Result{
			Decision:           DecisionAllow,
			SuppressedFindings: suppressed,
		}, nil
	}

	switch policy.Action {
	case ActionWarn:
		return Result{
			Decision:           DecisionWarn,
			Findings:           accepted,
			SuppressedFindings: suppressed,
		}, nil
	case ActionBlock:
		return Result{
			Decision:           DecisionBlock,
			Findings:           accepted,
			SuppressedFindings: suppressed,
		}, nil
	case ActionRedact:
		if !utf8.Valid(body) || document.duplicateKeys {
			return Result{
				Decision:           DecisionBlock,
				Findings:           accepted,
				SuppressedFindings: suppressed,
			}, ErrUnsafeRewrite
		}
		redacted, redactions, err := rewriteDocument(
			document,
			extracted,
			accepted,
			newPlaceholderAllocator(engine.placeholderEntropy),
		)
		if err != nil {
			return Result{
				Decision:           DecisionBlock,
				Findings:           accepted,
				SuppressedFindings: suppressed,
			}, ErrUnsafeRewrite
		}
		return Result{
			Decision:           DecisionRedact,
			Body:               redacted,
			Findings:           accepted,
			SuppressedFindings: suppressed,
			Redactions:         redactions,
		}, nil
	default:
		return Result{}, ErrPolicyUnavailable
	}
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
	if len(findings) > maxDetectorFindings {
		return nil, ErrDetectorLimit
	}
	type findingIdentity struct {
		Segment int
		Start   int
		End     int
		Kind    Kind
	}
	unique := make(map[findingIdentity]Finding, len(findings))
	for _, finding := range findings {
		if finding.Segment < 0 || finding.Segment >= len(segments) ||
			finding.Start < 0 || finding.End <= finding.Start ||
			finding.End > len(segments[finding.Segment].Value) ||
			!validKind(finding.Kind) ||
			!validMinConfidence(finding.Confidence) ||
			!utf8.ValidString(segments[finding.Segment].Value[:finding.Start]) ||
			!utf8.ValidString(segments[finding.Segment].Value[:finding.End]) {
			return nil, ErrDetectorUnavailable
		}
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
	return normalized, nil
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
