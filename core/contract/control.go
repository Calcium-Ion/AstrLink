package contract

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	ControlAPIVersion       = "v1"
	ProtocolContractVersion = "v1"
	DefaultCoreVersion      = "0.1.0-dev"
)

type HealthResponse struct {
	Status string `json:"status"`
}

// LocalDataStatus reports saved data this device cannot decrypt: the data
// directory came from another device, or the keychain entry or key file
// holding its local key is gone (plan §5.7).
type LocalDataStatus struct {
	// UnreadableCredentials counts service API keys, proxy and built-in tool
	// credentials, and subscription sign-ins that must be entered again.
	UnreadableCredentials int `json:"unreadable_credentials"`
	// UnreadableAccessTokens counts local access tokens whose value can no
	// longer be shown. They still authenticate.
	UnreadableAccessTokens int `json:"unreadable_access_tokens"`
	// AuditKeyMissing is true when bodies captured earlier cannot be opened.
	AuditKeyMissing bool `json:"audit_key_missing"`
}

// ClientIdentities reports the subscription client identities AstrLink
// supplies when it must provide one itself.
type ClientIdentities struct {
	Codex  ClientIdentityStatus `json:"codex"`
	Claude ClientIdentityStatus `json:"claude"`
	Grok   ClientIdentityStatus `json:"grok"`
}

// ClientIdentityStatus pairs the version learned from official client
// requests with the built-in version used until one is learned.
type ClientIdentityStatus struct {
	// LearnedVersion is empty until an official client request is learned. It
	// is reported even while learning is off.
	LearnedVersion string `json:"learned_version,omitempty"`
	BuiltinVersion string `json:"builtin_version"`
}

type VersionResponse struct {
	CoreVersion             string `json:"core_version"`
	ControlAPIVersion       string `json:"control_api_version"`
	ProtocolContractVersion string `json:"protocol_contract_version"`
	BuildCommit             string `json:"build_commit"`
}

var (
	contractVersionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]*$`)
	buildCommitPattern     = regexp.MustCompile(`^(?:[0-9a-f]{7,64}|unknown)$`)
)

func (version VersionResponse) Validate() error {
	if version.CoreVersion == "" || utf8.RuneCountInString(version.CoreVersion) > 64 {
		return fmt.Errorf("core_version must contain 1 to 64 characters")
	}
	for name, value := range map[string]string{
		"control_api_version":       version.ControlAPIVersion,
		"protocol_contract_version": version.ProtocolContractVersion,
	} {
		if len(value) < 1 || len(value) > 64 || !contractVersionPattern.MatchString(value) {
			return fmt.Errorf("%s is invalid", name)
		}
	}
	if !buildCommitPattern.MatchString(version.BuildCommit) {
		return fmt.Errorf("build_commit must be unknown or 7 to 64 lowercase hexadecimal characters")
	}
	return nil
}

type PlanTypeDescriptor struct {
	ID                  PlanType `json:"id"`
	AvailableInAlpha    bool     `json:"available_in_alpha"`
	UsesLocalConversion bool     `json:"uses_local_conversion"`
}

type ConversionEngineDescriptor struct {
	Name      string           `json:"name"`
	Version   *string          `json:"version"`
	Available bool             `json:"available"`
	Edges     []ConversionEdge `json:"edges"`
}

type CapabilitiesResponse struct {
	ProtocolContractVersion string                     `json:"protocol_contract_version"`
	Protocols               []ProtocolDescriptor       `json:"protocols"`
	PlanTypes               []PlanTypeDescriptor       `json:"plan_types"`
	ConversionEngine        ConversionEngineDescriptor `json:"conversion_engine"`
}

func DefaultVersionResponse(coreVersion, buildCommit string) VersionResponse {
	if coreVersion == "" {
		coreVersion = DefaultCoreVersion
	}
	if buildCommit == "" {
		buildCommit = "unknown"
	}
	return VersionResponse{
		CoreVersion:             coreVersion,
		ControlAPIVersion:       ControlAPIVersion,
		ProtocolContractVersion: ProtocolContractVersion,
		BuildCommit:             buildCommit,
	}
}

func DefaultCapabilitiesResponse() CapabilitiesResponse {
	planTypes := []PlanType{PlanTypeNative, PlanTypeDelegated, PlanTypeRelayKit}
	plans := make([]PlanTypeDescriptor, 0, len(planTypes))
	for _, planType := range planTypes {
		plans = append(plans, PlanTypeDescriptor{
			ID:                  planType,
			AvailableInAlpha:    planType.AvailableInAlpha(),
			UsesLocalConversion: planType.UsesLocalConversion(),
		})
	}
	return CapabilitiesResponse{
		ProtocolContractVersion: ProtocolContractVersion,
		Protocols:               AlphaProtocolDescriptors(),
		PlanTypes:               plans,
		ConversionEngine: ConversionEngineDescriptor{
			Name:      "relaykit",
			Version:   nil,
			Available: false,
			Edges:     []ConversionEdge{},
		},
	}
}

// ReadyEvent is the single machine-readable line emitted to stdout after both
// listeners are bound. Operational logs belong on stderr.
type ReadyEvent struct {
	Event                   string `json:"event"`
	CoreVersion             string `json:"core_version"`
	ControlAPIVersion       string `json:"control_api_version"`
	ProtocolContractVersion string `json:"protocol_contract_version"`
	InferenceURL            string `json:"inference_url"`
	ControlURL              string `json:"control_url"`
}

func (event ReadyEvent) Validate() error {
	if event.Event != "ready" {
		return fmt.Errorf("event must be ready")
	}
	if err := (VersionResponse{
		CoreVersion:             event.CoreVersion,
		ControlAPIVersion:       event.ControlAPIVersion,
		ProtocolContractVersion: event.ProtocolContractVersion,
		BuildCommit:             "unknown",
	}).Validate(); err != nil {
		return err
	}
	if err := validateReadyURL("inference_url", event.InferenceURL); err != nil {
		return err
	}
	if err := validateReadyURL("control_url", event.ControlURL); err != nil {
		return err
	}
	return nil
}

func validateReadyURL(name, value string) error {
	const prefix = "http://127.0.0.1:"
	if !strings.HasPrefix(value, prefix) {
		return fmt.Errorf("%s must be a canonical IPv4 loopback URL", name)
	}
	portText := strings.TrimPrefix(value, prefix)
	if portText == "" || (len(portText) > 1 && portText[0] == '0') {
		return fmt.Errorf("%s must contain a canonical port from 1 to 65535", name)
	}
	for _, character := range portText {
		if character < '0' || character > '9' {
			return fmt.Errorf("%s must contain a canonical port from 1 to 65535", name)
		}
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != portText {
		return fmt.Errorf("%s must contain a canonical port from 1 to 65535", name)
	}
	return nil
}
