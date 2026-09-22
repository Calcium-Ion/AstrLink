// Package privacy applies local, protocol-aware privacy policy to inference
// request bodies before an upstream credential is loaded or a request is sent.
package privacy

import (
	"context"
	"errors"

	"github.com/QuantumNous/astrlink/core/contract"
)

type Mode string

const (
	ModeRegex      Mode = "regex"
	ModeLocalModel Mode = "local_model"

	ModeOpenAIPrivacyFilter = ModeLocalModel
	ModeModel               = ModeLocalModel
)

func (mode Mode) Valid() bool {
	return mode == ModeRegex || mode == ModeLocalModel
}

type Action string

const (
	ActionAllow  Action = "allow"
	ActionRedact Action = "redact"
	ActionBlock  Action = "block"
	ActionWarn   Action = "warn"
)

func (action Action) Valid() bool {
	return action == ActionAllow || action == ActionRedact ||
		action == ActionBlock || action == ActionWarn
}

// KindRule is the effective per-kind detection switch and placeholder shape.
type KindRule struct {
	Enabled bool
	Style   contract.PlaceholderStyle
}

// Policy is the minimal execution contract. Storage/control-plane adapters may
// resolve richer policy documents into one effective request policy.
type Policy struct {
	Enabled          bool
	Mode             Mode
	LocalModelID     contract.PrivacyModelID
	MinConfidence    float64
	RegexSource      contract.PolicyRegexSource
	CustomRegexRules []contract.PolicyRegexRule
	// KindRules holds one entry per canonical kind. A nil map means every kind
	// is enabled with token placeholders: for a privacy filter the safe default
	// is to redact, so an unresolved or hand-built policy must never quietly
	// forward plaintext. Once the map is present it is authoritative, and a kind
	// omitted from it is disabled.
	KindRules            map[Kind]KindRule
	Allowlist            []contract.PolicyAllowlistRule
	Action               Action
	ResponseRestore      bool
	RestoreToolArguments bool
	PlaceholderNotice    bool
}

// KindRule resolves the effective rule for a kind. See Policy.KindRules for why
// a nil map redacts everything rather than nothing.
func (policy Policy) KindRule(kind Kind) KindRule {
	if policy.KindRules == nil {
		return KindRule{Enabled: true, Style: contract.PlaceholderStyleToken}
	}
	rule, exists := policy.KindRules[kind]
	if !exists {
		return KindRule{}
	}
	if rule.Style == "" {
		rule.Style = contract.PlaceholderStyleToken
	}
	return rule
}

type Scope struct {
	Protocol      contract.ProtocolID
	Model         string
	ServiceID     contract.ServiceID
	AccessTokenID contract.AccessTokenID
}

type PolicyProvider interface {
	RequestPolicy(context.Context, Scope) (Policy, error)
}

type PolicyProviderFunc func(context.Context, Scope) (Policy, error)

func (function PolicyProviderFunc) RequestPolicy(ctx context.Context, scope Scope) (Policy, error) {
	return function(ctx, scope)
}

type Kind string

const (
	KindEmail        Kind = "email"
	KindPhone        Kind = "phone"
	KindAccount      Kind = "account"
	KindPaymentCard  Kind = "payment_card"
	KindIPAddress    Kind = "ip_address"
	KindURL          Kind = "url"
	KindCommonSecret Kind = "common_secret"
	KindAddress      Kind = "private_address"
	KindDate         Kind = "private_date"
	KindPerson       Kind = "private_person"
)

type Segment struct {
	Path  string
	Value string
}

type DetectInput struct {
	Protocol             contract.ProtocolID
	ExpectedLocalModelID contract.PrivacyModelID
	Segments             []Segment
}

// SuppressionReason names the rule that kept a detected span in the request.
type SuppressionReason string

const (
	SuppressionLowConfidence SuppressionReason = "low_confidence"
	SuppressionKindDisabled  SuppressionReason = "kind_disabled"
	SuppressionAllowlisted   SuppressionReason = "allowlisted"
	// SuppressionPlaceholder marks a value that already belongs to a
	// placeholder namespace. Redacting it again would build a chain of
	// placeholders standing in for placeholders, and the innermost original
	// could never be restored.
	SuppressionPlaceholder SuppressionReason = "placeholder"
	// SuppressionUnrepresentable marks a span whose kind ran out of reserved
	// natural stand-ins, so the request fell back to a token placeholder.
	SuppressionUnrepresentable SuppressionReason = "unrepresentable"
)

// Finding identifies a byte range without retaining or returning the matched
// plaintext. Detector implementations must index the supplied Segment.Value.
// Plaintext for local restoration is carried only on Result.Redactions after a
// successful DecisionRedact and must never be logged, persisted, or sent upstream.
//
// Suppression is empty on accepted findings and set on suppressed ones.
// Detectors never populate it; only the engine does.
type Finding struct {
	Segment     int
	Start       int
	End         int
	Kind        Kind
	Confidence  float64
	Suppression SuppressionReason
}

// Redaction maps a request-scoped placeholder to the original plaintext for
// in-process response restoration and dry-run preview only.
type Redaction struct {
	Placeholder string
	Kind        Kind
	Value       string
	Style       contract.PlaceholderStyle
}

type Detector interface {
	Detect(context.Context, DetectInput) ([]Finding, error)
}

type DetectorFunc func(context.Context, DetectInput) ([]Finding, error)

func (function DetectorFunc) Detect(ctx context.Context, input DetectInput) ([]Finding, error) {
	return function(ctx, input)
}

var (
	ErrPolicyUnavailable       = errors.New("privacy policy is unavailable")
	ErrSafetyEngineUnavailable = errors.New("SafetyEngineUnavailable")
	ErrDetectorUnavailable     = ErrSafetyEngineUnavailable
	ErrDetectorLimit           = errors.New("privacy detector limit exceeded")
	ErrDetectorTimeout         = errors.New("privacy detector timed out")
	ErrUnsafeInput             = errors.New("privacy input cannot be inspected safely")
	ErrUnsafeRewrite           = errors.New("privacy input cannot be rewritten safely")
)

type Decision string

const (
	DecisionAllow  Decision = "allow"
	DecisionRedact Decision = "redact"
	DecisionBlock  Decision = "block"
	DecisionWarn   Decision = "warn"
)

type Result struct {
	// Decision is only populated when inspection succeeds. A processing error
	// must not be reported as a policy decision to block the request.
	Decision           Decision
	Body               []byte
	Findings           []Finding
	SuppressedFindings []Finding
	Redactions         []Redaction
	// NoticeInjected reports that a placeholder convention note was prepended
	// to the upstream system prompt.
	NoticeInjected bool
}

type Filter interface {
	ResolvePolicy(context.Context, Scope) (Policy, error)
	Inspect(context.Context, Policy, contract.ProtocolID, []byte) (Result, error)
}
