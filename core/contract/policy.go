package contract

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"regexp"
	"unicode/utf8"
)

type PolicyID string

type PolicyAction string

const (
	PolicyActionAllow  PolicyAction = "allow"
	PolicyActionWarn   PolicyAction = "warn"
	PolicyActionBlock  PolicyAction = "block"
	PolicyActionRedact PolicyAction = "redact"
)

func (action PolicyAction) Valid() bool {
	return action == PolicyActionAllow || action == PolicyActionWarn ||
		action == PolicyActionBlock || action == PolicyActionRedact
}

type PolicyDetector string

const (
	PolicyDetectorRegex      PolicyDetector = "regex"
	PolicyDetectorLocalModel PolicyDetector = "local_model"

	// PolicyDetectorOpenAIPrivacyFilter is retained as a source-compatibility
	// alias. The only accepted wire value is "local_model".
	PolicyDetectorOpenAIPrivacyFilter = PolicyDetectorLocalModel
)

func (detector PolicyDetector) Valid() bool {
	return detector == PolicyDetectorRegex || detector == PolicyDetectorLocalModel
}

const (
	DefaultPrivacyPolicyID       PolicyID = "policy_privacy_default"
	DefaultPrivacyPolicyName              = "隐私保护"
	DefaultPrivacyPolicyPriority          = 0
	DefaultPrivacyMinConfidence           = 0.80

	MaxPolicyCustomRegexRules  = 64
	MaxPolicyRegexPatternRunes = 512
	MaxPolicyAllowlistRules    = 128
	MaxPolicyAllowlistRunes    = 256
)

// PlaceholderStyle selects the wire shape of a redaction placeholder.
//
// "token" emits an opaque marker such as <PRIVATE_EMAIL_b299548153dfc50a>.
// It fails loudly: a model will not mistake it for a usable value and an
// unrestored leak is obvious to a human reader, at the cost of being
// out-of-distribution text that violates typed tool-argument schemas.
//
// "natural" emits a syntactically valid stand-in drawn from a permanently
// reserved namespace such as redacted-<hex>@private.invalid. A model copies it
// verbatim without instruction and it satisfies schema validation, but an
// unrestored leak looks plausible, so it is only safe together with
// tool-argument restoration.
type PlaceholderStyle string

const (
	PlaceholderStyleNatural PlaceholderStyle = "natural"
	PlaceholderStyleToken   PlaceholderStyle = "token"
)

func (style PlaceholderStyle) Valid() bool {
	return style == PlaceholderStyleNatural || style == PlaceholderStyleToken
}

func (style PlaceholderStyle) Effective() PlaceholderStyle {
	if style == "" {
		return PlaceholderStyleToken
	}
	return style
}

// PolicyKindRule is the per-kind detection switch and placeholder shape.
type PolicyKindRule struct {
	Kind    string           `json:"kind"`
	Enabled bool             `json:"enabled"`
	Style   PlaceholderStyle `json:"style"`
}

// PrivacyKinds is the canonical ordering of every kind a detector may emit.
// The Regex detector emits the first seven; the local model adds the rest.
func PrivacyKinds() []string {
	return []string{
		"common_secret",
		"payment_card",
		"account",
		"email",
		"phone",
		"url",
		"ip_address",
		"private_person",
		"private_address",
		"private_date",
	}
}

func ValidPrivacyKind(kind string) bool {
	for _, candidate := range PrivacyKinds() {
		if kind == candidate {
			return true
		}
	}
	return false
}

// PlaceholderStyleLocked reports kinds whose placeholder shape is a safety
// property rather than a preference, and therefore is not configurable.
//
// common_secret must stay opaque: dressing a credential up as a usable-looking
// key invites the model to actually call an API with it.
//
// private_person, private_address, and private_date have no reserved namespace
// to draw from, so any natural stand-in risks colliding with a real person or
// place, and a plausible fake date would be reasoned over as if it were true.
func PlaceholderStyleLocked(kind string) bool {
	switch kind {
	case "common_secret", "private_person", "private_address", "private_date":
		return true
	default:
		return false
	}
}

// DefaultPrivacyKindRules seeds one rule per kind.
//
// url and ip_address default to disabled: they are the dominant false-positive
// source for coding agents, whose prompts are dense with documentation links,
// loopback addresses, and repository URLs that carry no user PII.
func DefaultPrivacyKindRules() []PolicyKindRule {
	rules := make([]PolicyKindRule, 0, len(PrivacyKinds()))
	for _, kind := range PrivacyKinds() {
		style := PlaceholderStyleNatural
		if PlaceholderStyleLocked(kind) {
			style = PlaceholderStyleToken
		}
		enabled := kind != "url" && kind != "ip_address"
		rules = append(rules, PolicyKindRule{Kind: kind, Enabled: enabled, Style: style})
	}
	return rules
}

func (rule PolicyKindRule) Validate() error {
	if !ValidPrivacyKind(rule.Kind) {
		return fmt.Errorf("unknown kind %q", rule.Kind)
	}
	if !rule.Style.Valid() {
		return fmt.Errorf("unknown placeholder style %q", rule.Style)
	}
	if PlaceholderStyleLocked(rule.Kind) && rule.Style != PlaceholderStyleToken {
		return fmt.Errorf(
			"kind %q must keep placeholder style %q",
			rule.Kind, PlaceholderStyleToken,
		)
	}
	return nil
}

type PolicyAllowlistType string

const (
	PolicyAllowlistTypeLiteral      PolicyAllowlistType = "literal"
	PolicyAllowlistTypeDomainSuffix PolicyAllowlistType = "domain_suffix"
	PolicyAllowlistTypeCIDR         PolicyAllowlistType = "cidr"
)

func (allowlistType PolicyAllowlistType) Valid() bool {
	return allowlistType == PolicyAllowlistTypeLiteral ||
		allowlistType == PolicyAllowlistTypeDomainSuffix ||
		allowlistType == PolicyAllowlistTypeCIDR
}

// PolicyAllowlistRule exempts a matched value from redaction so the model and
// the local agent keep the real string.
type PolicyAllowlistRule struct {
	Type  PolicyAllowlistType `json:"type"`
	Value string              `json:"value"`
}

func (rule PolicyAllowlistRule) Validate() error {
	if !rule.Type.Valid() {
		return fmt.Errorf("unknown allowlist type %q", rule.Type)
	}
	runes := utf8.RuneCountInString(rule.Value)
	if runes < 1 || runes > MaxPolicyAllowlistRunes {
		return fmt.Errorf("allowlist value must contain 1 to %d characters", MaxPolicyAllowlistRunes)
	}
	if rule.Type == PolicyAllowlistTypeCIDR {
		if _, _, err := net.ParseCIDR(rule.Value); err != nil {
			return fmt.Errorf("invalid allowlist cidr %q", rule.Value)
		}
	}
	return nil
}

// DefaultPrivacyAllowlistRules seeds the hosts and ranges that are effectively
// never user PII but are matched aggressively by the url and ip_address rules.
func DefaultPrivacyAllowlistRules() []PolicyAllowlistRule {
	return []PolicyAllowlistRule{
		{Type: PolicyAllowlistTypeDomainSuffix, Value: "localhost"},
		{Type: PolicyAllowlistTypeDomainSuffix, Value: "github.com"},
		{Type: PolicyAllowlistTypeDomainSuffix, Value: "githubusercontent.com"},
		{Type: PolicyAllowlistTypeCIDR, Value: "127.0.0.0/8"},
		{Type: PolicyAllowlistTypeCIDR, Value: "::1/128"},
		{Type: PolicyAllowlistTypeCIDR, Value: "10.0.0.0/8"},
		{Type: PolicyAllowlistTypeCIDR, Value: "172.16.0.0/12"},
		{Type: PolicyAllowlistTypeCIDR, Value: "192.168.0.0/16"},
		{Type: PolicyAllowlistTypeCIDR, Value: "169.254.0.0/16"},
	}
}

type PolicyRegexSource string

const (
	PolicyRegexSourceBuiltin PolicyRegexSource = "builtin"
	PolicyRegexSourceCustom  PolicyRegexSource = "custom"
)

func (source PolicyRegexSource) Valid() bool {
	return source == PolicyRegexSourceBuiltin || source == PolicyRegexSourceCustom
}

func (source PolicyRegexSource) Effective() PolicyRegexSource {
	if source == "" {
		return PolicyRegexSourceBuiltin
	}
	return source
}

// PolicyRegexRule is one user-authored Regex detector rule.
type PolicyRegexRule struct {
	Kind    string `json:"kind"`
	Pattern string `json:"pattern"`
}

// PolicyRegexBuiltinRulesResponse is the read-only catalog of built-in Regex
// patterns used to seed custom rules.
type PolicyRegexBuiltinRulesResponse struct {
	Rules []PolicyRegexRule `json:"rules"`
}

// RegexDetectorKinds are the canonical kinds the Regex detector may emit.
func RegexDetectorKinds() []string {
	return []string{
		"email",
		"phone",
		"account",
		"payment_card",
		"ip_address",
		"url",
		"common_secret",
	}
}

func ValidRegexDetectorKind(kind string) bool {
	for _, candidate := range RegexDetectorKinds() {
		if kind == candidate {
			return true
		}
	}
	return false
}

func (rule PolicyRegexRule) Validate() error {
	if !ValidRegexDetectorKind(rule.Kind) {
		return fmt.Errorf("unknown regex rule kind %q", rule.Kind)
	}
	runes := utf8.RuneCountInString(rule.Pattern)
	if runes < 1 || runes > MaxPolicyRegexPatternRunes {
		return fmt.Errorf("regex pattern must contain 1 to %d characters", MaxPolicyRegexPatternRunes)
	}
	if _, err := regexp.Compile(rule.Pattern); err != nil {
		return fmt.Errorf("invalid regex pattern: %w", err)
	}
	return nil
}

// NormalizePrivacyPolicyDefaults fills legacy omissions for the singleton policy.
//
// KindRules is normalized to an explicit, canonically ordered entry per kind so
// that a partial patch never leaves the effective shape of a kind ambiguous.
func NormalizePrivacyPolicyDefaults(policy *Policy) {
	if policy == nil {
		return
	}
	if policy.RegexSource == "" {
		policy.RegexSource = PolicyRegexSourceBuiltin
	}
	if policy.CustomRegexRules == nil {
		policy.CustomRegexRules = []PolicyRegexRule{}
	}
	if policy.AllowlistRules == nil {
		policy.AllowlistRules = []PolicyAllowlistRule{}
	}
	policy.KindRules = normalizeKindRules(policy.KindRules)
}

// normalizeKindRules keeps every supplied style verbatim so that Validate can
// still reject a locked kind whose style was tampered with. Only omissions are
// filled: an absent kind takes its default rule, an absent style takes the
// default style for that kind.
func normalizeKindRules(rules []PolicyKindRule) []PolicyKindRule {
	supplied := make(map[string]PolicyKindRule, len(rules))
	unknown := make([]PolicyKindRule, 0)
	for _, rule := range rules {
		if !ValidPrivacyKind(rule.Kind) {
			unknown = append(unknown, rule)
			continue
		}
		if _, exists := supplied[rule.Kind]; exists {
			continue
		}
		supplied[rule.Kind] = rule
	}
	normalized := make([]PolicyKindRule, 0, len(PrivacyKinds())+len(unknown))
	for _, defaultRule := range DefaultPrivacyKindRules() {
		rule, exists := supplied[defaultRule.Kind]
		if !exists {
			normalized = append(normalized, defaultRule)
			continue
		}
		if rule.Style == "" {
			rule.Style = defaultRule.Style
		}
		normalized = append(normalized, rule)
	}
	return append(normalized, unknown...)
}

type PolicyMatch struct {
	Protocols  []ProtocolID `json:"protocols,omitempty"`
	Models     []string     `json:"models,omitempty"`
	ServiceIDs []ServiceID  `json:"service_ids,omitempty"`
}

// UnmarshalJSON accepts the legacy endpoint_ids key from pre-v12 persisted
// policies. Marshaling always emits service_ids.
func (match *PolicyMatch) UnmarshalJSON(data []byte) error {
	var wire struct {
		Protocols   []ProtocolID `json:"protocols,omitempty"`
		Models      []string     `json:"models,omitempty"`
		ServiceIDs  *[]ServiceID `json:"service_ids,omitempty"`
		EndpointIDs *[]ServiceID `json:"endpoint_ids,omitempty"`
	}
	if err := decodeStrictContractJSON(data, &wire); err != nil {
		return err
	}
	if wire.ServiceIDs != nil && wire.EndpointIDs != nil {
		return fmt.Errorf("policy match cannot contain both service_ids and endpoint_ids")
	}
	serviceIDs := []ServiceID(nil)
	if wire.ServiceIDs != nil {
		serviceIDs = *wire.ServiceIDs
	} else if wire.EndpointIDs != nil {
		serviceIDs = *wire.EndpointIDs
	}
	*match = PolicyMatch{
		Protocols:  wire.Protocols,
		Models:     wire.Models,
		ServiceIDs: serviceIDs,
	}
	return nil
}

// Policy keeps request and response decisions explicit. Body-audit switches
// are intentionally not part of this type; they are privileged control-plane
// settings and default to off.
type Policy struct {
	ID               PolicyID          `json:"id"`
	Name             string            `json:"name"`
	Enabled          bool              `json:"enabled"`
	Priority         int               `json:"priority"`
	Detector         PolicyDetector    `json:"detector"`
	LocalModelID     *PrivacyModelID   `json:"local_model_id"`
	MinConfidence    float64           `json:"min_confidence"`
	RegexSource      PolicyRegexSource `json:"regex_source"`
	CustomRegexRules []PolicyRegexRule `json:"custom_regex_rules"`
	// KindRules carries the per-kind detection switch and placeholder shape.
	KindRules       []PolicyKindRule      `json:"kind_rules"`
	AllowlistRules  []PolicyAllowlistRule `json:"allowlist_rules"`
	Match           PolicyMatch           `json:"match"`
	RequestAction   PolicyAction          `json:"request_action"`
	ResponseAction  PolicyAction          `json:"response_action"`
	ResponseRestore bool                  `json:"response_restore"`
	// RestoreToolArguments extends restoration into tool-call arguments. The
	// tool call is executed by the operator's own agent harness, so the real
	// value belongs there; without this a natural placeholder is written into
	// local files and shell commands as if it were genuine.
	RestoreToolArguments bool `json:"restore_tool_arguments"`
	// PlaceholderNotice prepends a short convention note to the upstream system
	// prompt when the request emitted at least one token-shaped placeholder.
	PlaceholderNotice bool `json:"placeholder_notice"`
	// SkipToolDeclarations keeps the request's top-level tool declarations out
	// of inspection: the function names, descriptions and schemas the client
	// offers the model. They are inspected by default; function call arguments
	// and results are inspected either way.
	SkipToolDeclarations bool `json:"skip_tool_declarations"`
	// InspectAdditionalTools extends inspection into the tool declarations
	// Codex carries in additional_tools input items. They are skipped by
	// default. Those items sit in the conversation input, not the top-level
	// tools, so this is independent of SkipToolDeclarations.
	InspectAdditionalTools bool `json:"inspect_additional_tools"`
}

func (policy Policy) Validate() error {
	if err := policy.ID.Validate(); err != nil {
		return err
	}
	if policy.Name == "" || utf8.RuneCountInString(policy.Name) > 128 {
		return fmt.Errorf("policy name must contain 1 to 128 characters")
	}
	if policy.Priority < 0 || policy.Priority > 1_000_000 {
		return fmt.Errorf("policy priority must be between 0 and 1000000")
	}
	if !policy.Detector.Valid() {
		return fmt.Errorf("unknown policy detector %q", policy.Detector)
	}
	if math.IsNaN(policy.MinConfidence) || math.IsInf(policy.MinConfidence, 0) ||
		policy.MinConfidence < 0 || policy.MinConfidence > 1 {
		return fmt.Errorf("min_confidence must be between 0 and 1")
	}
	regexSource := policy.RegexSource.Effective()
	if policy.RegexSource != "" && !policy.RegexSource.Valid() {
		return fmt.Errorf("unknown regex_source %q", policy.RegexSource)
	}
	if len(policy.CustomRegexRules) > MaxPolicyCustomRegexRules {
		return fmt.Errorf("custom_regex_rules must contain at most %d rules", MaxPolicyCustomRegexRules)
	}
	for index, rule := range policy.CustomRegexRules {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("custom_regex_rules[%d]: %w", index, err)
		}
	}
	seenKinds := make(map[string]struct{}, len(policy.KindRules))
	for index, rule := range policy.KindRules {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("kind_rules[%d]: %w", index, err)
		}
		if _, exists := seenKinds[rule.Kind]; exists {
			return fmt.Errorf("kind_rules[%d]: duplicate kind %q", index, rule.Kind)
		}
		seenKinds[rule.Kind] = struct{}{}
	}
	if len(policy.AllowlistRules) > MaxPolicyAllowlistRules {
		return fmt.Errorf("allowlist_rules must contain at most %d rules", MaxPolicyAllowlistRules)
	}
	seenAllowlist := make(map[PolicyAllowlistRule]struct{}, len(policy.AllowlistRules))
	for index, rule := range policy.AllowlistRules {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("allowlist_rules[%d]: %w", index, err)
		}
		if _, exists := seenAllowlist[rule]; exists {
			return fmt.Errorf("allowlist_rules[%d]: duplicate allowlist rule", index)
		}
		seenAllowlist[rule] = struct{}{}
	}
	switch policy.Detector {
	case PolicyDetectorRegex:
		if policy.LocalModelID != nil {
			return fmt.Errorf("local_model_id must be null for the regex detector")
		}
		if regexSource == PolicyRegexSourceCustom && len(policy.CustomRegexRules) == 0 {
			return fmt.Errorf("custom_regex_rules must contain at least one rule when regex_source is custom")
		}
	case PolicyDetectorLocalModel:
		if policy.LocalModelID == nil {
			return fmt.Errorf("local_model_id is required for the local_model detector")
		}
		if err := policy.LocalModelID.Validate(); err != nil {
			return fmt.Errorf("local_model_id: %w", err)
		}
	}
	if !policy.RequestAction.Valid() {
		return fmt.Errorf("unknown request action %q", policy.RequestAction)
	}
	if !policy.ResponseAction.Valid() {
		return fmt.Errorf("unknown response action %q", policy.ResponseAction)
	}
	seen := make(map[ProtocolID]struct{}, len(policy.Match.Protocols))
	for index, protocol := range policy.Match.Protocols {
		if err := protocol.Validate(); err != nil {
			return fmt.Errorf("match.protocols[%d]: %w", index, err)
		}
		if _, ok := seen[protocol]; ok {
			return fmt.Errorf("match.protocols[%d]: duplicate protocol %q", index, protocol)
		}
		seen[protocol] = struct{}{}
	}
	seenModels := make(map[string]struct{}, len(policy.Match.Models))
	for index, model := range policy.Match.Models {
		if model == "" || utf8.RuneCountInString(model) > 256 {
			return fmt.Errorf("match.models[%d] must contain 1 to 256 characters", index)
		}
		if _, ok := seenModels[model]; ok {
			return fmt.Errorf("match.models[%d]: duplicate model %q", index, model)
		}
		seenModels[model] = struct{}{}
	}
	seenEndpoints := make(map[ServiceID]struct{}, len(policy.Match.ServiceIDs))
	for index, endpointID := range policy.Match.ServiceIDs {
		if err := endpointID.Validate(); err != nil {
			return fmt.Errorf("match.service_ids[%d]: %w", index, err)
		}
		if _, ok := seenEndpoints[endpointID]; ok {
			return fmt.Errorf("match.service_ids[%d]: duplicate endpoint id %q", index, endpointID)
		}
		seenEndpoints[endpointID] = struct{}{}
	}
	return nil
}

func DefaultPrivacyPolicy() Policy {
	return Policy{
		ID:                   DefaultPrivacyPolicyID,
		Name:                 DefaultPrivacyPolicyName,
		Enabled:              false,
		Priority:             DefaultPrivacyPolicyPriority,
		Detector:             PolicyDetectorRegex,
		LocalModelID:         nil,
		MinConfidence:        DefaultPrivacyMinConfidence,
		RegexSource:          PolicyRegexSourceBuiltin,
		CustomRegexRules:     []PolicyRegexRule{},
		KindRules:            DefaultPrivacyKindRules(),
		AllowlistRules:       DefaultPrivacyAllowlistRules(),
		Match:                PolicyMatch{},
		RequestAction:        PolicyActionRedact,
		ResponseAction:       PolicyActionAllow,
		ResponseRestore:      true,
		RestoreToolArguments: true,
		PlaceholderNotice:    true,
		// Tool declarations are inspected; additional_tools are skipped.
		SkipToolDeclarations:   false,
		InspectAdditionalTools: false,
	}
}

// MaxPolicyDryRunSampleBytes caps sample text accepted by policy dry-run.
const MaxPolicyDryRunSampleBytes = 256 * 1024

// PolicyDryRunRequest evaluates a sample against the privacy policy without
// forwarding upstream or mutating stored policy state.
type PolicyDryRunRequest struct {
	Protocol   ProtocolID `json:"protocol"`
	SampleText string     `json:"sample_text"`
	// Policy optionally overrides patchable fields for this evaluation only.
	// Keys match PolicyPatch: enabled, detector, local_model_id,
	// min_confidence, regex_source, custom_regex_rules, kind_rules,
	// allowlist_rules, request_action, response_restore,
	// restore_tool_arguments, placeholder_notice, skip_tool_declarations,
	// inspect_additional_tools.
	Policy map[string]json.RawMessage `json:"policy,omitempty"`
}

// PolicyDryRunFinding identifies a matched span without returning plaintext.
// Reason is empty for accepted findings and names the suppression rule
// otherwise, so an operator can tell why a match was left untouched.
type PolicyDryRunFinding struct {
	Kind       string  `json:"kind"`
	Path       string  `json:"path"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason,omitempty"`
}

// PolicyDryRunRedaction is returned only for dry-run of user-supplied samples.
// Live inference never exposes these values on the wire.
type PolicyDryRunRedaction struct {
	Placeholder string           `json:"placeholder"`
	Kind        string           `json:"kind"`
	Value       string           `json:"value"`
	Style       PlaceholderStyle `json:"style"`
}

// PolicyDryRunResponse is the local preview of a privacy-policy evaluation.
type PolicyDryRunResponse struct {
	Decision           string                  `json:"decision"`
	FindingsSummary    string                  `json:"findings_summary"`
	Findings           []PolicyDryRunFinding   `json:"findings"`
	SuppressedFindings []PolicyDryRunFinding   `json:"suppressed_findings"`
	Redactions         []PolicyDryRunRedaction `json:"redactions,omitempty"`
	RedactedBody       *string                 `json:"redacted_body,omitempty"`
	InspectedBody      string                  `json:"inspected_body"`
}

// ValidatePrivacyDefault keeps the MVP policy identity and global match scope
// immutable while allowing its detector and request/response decisions to be
// configured through the authenticated control API.
func ValidatePrivacyDefault(policy Policy) error {
	NormalizePrivacyPolicyDefaults(&policy)
	if err := policy.Validate(); err != nil {
		return err
	}
	if policy.ID != DefaultPrivacyPolicyID {
		return fmt.Errorf("privacy policy id must be %q", DefaultPrivacyPolicyID)
	}
	if policy.Name != DefaultPrivacyPolicyName {
		return fmt.Errorf("privacy policy name is immutable")
	}
	if policy.Priority != DefaultPrivacyPolicyPriority {
		return fmt.Errorf("privacy policy priority is immutable")
	}
	if policy.ResponseAction != PolicyActionAllow {
		return fmt.Errorf("privacy policy response action must remain %q", PolicyActionAllow)
	}
	if len(policy.Match.Protocols) != 0 || len(policy.Match.Models) != 0 || len(policy.Match.ServiceIDs) != 0 {
		return fmt.Errorf("privacy policy match must remain global")
	}
	return nil
}
