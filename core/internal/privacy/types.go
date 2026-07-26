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

// Policy is the minimal execution contract. Storage/control-plane adapters may
// resolve richer policy documents into one effective request policy.
type Policy struct {
	Enabled         bool
	Mode            Mode
	LocalModelID    contract.PrivacyModelID
	Action          Action
	ResponseRestore bool
}

type Scope struct {
	Protocol      contract.ProtocolID
	Model         string
	EndpointID    contract.EndpointID
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

// Finding identifies a byte range without retaining or returning the matched
// plaintext. Detector implementations must index the supplied Segment.Value.
// Plaintext for local restoration is carried only on Result.Redactions after a
// successful DecisionRedact and must never be logged, persisted, or sent upstream.
type Finding struct {
	Segment int
	Start   int
	End     int
	Kind    Kind
}

// Redaction maps a request-scoped placeholder to the original plaintext for
// in-process response restoration and dry-run preview only.
type Redaction struct {
	Placeholder string
	Kind        Kind
	Value       string
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
	Decision  Decision
	Body      []byte
	Findings  []Finding
	Redactions []Redaction
}

type Filter interface {
	ResolvePolicy(context.Context, Scope) (Policy, error)
	Inspect(context.Context, Policy, contract.ProtocolID, []byte) (Result, error)
}
