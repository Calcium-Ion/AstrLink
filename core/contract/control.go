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
