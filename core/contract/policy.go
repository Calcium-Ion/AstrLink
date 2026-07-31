package contract

import (
	"encoding/json"
	"fmt"
	"math"
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
	DefaultPrivacyMinConfidence           = 0.60
)

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
	ID              PolicyID        `json:"id"`
	Name            string          `json:"name"`
	Enabled         bool            `json:"enabled"`
	Priority        int             `json:"priority"`
	Detector        PolicyDetector  `json:"detector"`
	LocalModelID    *PrivacyModelID `json:"local_model_id"`
	MinConfidence   float64         `json:"min_confidence"`
	Match           PolicyMatch     `json:"match"`
	RequestAction   PolicyAction    `json:"request_action"`
	ResponseAction  PolicyAction    `json:"response_action"`
	ResponseRestore bool            `json:"response_restore"`
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
	switch policy.Detector {
	case PolicyDetectorRegex:
		if policy.LocalModelID != nil {
			return fmt.Errorf("local_model_id must be null for the regex detector")
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
		ID:              DefaultPrivacyPolicyID,
		Name:            DefaultPrivacyPolicyName,
		Enabled:         false,
		Priority:        DefaultPrivacyPolicyPriority,
		Detector:        PolicyDetectorRegex,
		LocalModelID:    nil,
		MinConfidence:   DefaultPrivacyMinConfidence,
		Match:           PolicyMatch{},
		RequestAction:   PolicyActionRedact,
		ResponseAction:  PolicyActionAllow,
		ResponseRestore: true,
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
	// min_confidence, request_action, response_restore.
	Policy map[string]json.RawMessage `json:"policy,omitempty"`
}

// PolicyDryRunFinding identifies a matched span without returning plaintext.
type PolicyDryRunFinding struct {
	Kind       string  `json:"kind"`
	Path       string  `json:"path"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Confidence float64 `json:"confidence"`
}

// PolicyDryRunRedaction is returned only for dry-run of user-supplied samples.
// Live inference never exposes these values on the wire.
type PolicyDryRunRedaction struct {
	Placeholder string `json:"placeholder"`
	Kind        string `json:"kind"`
	Value       string `json:"value"`
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
