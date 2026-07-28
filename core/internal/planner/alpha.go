// Package planner builds explicit execution plans. Alpha planning never calls
// a conversion engine: native and delegated requests preserve their ingress
// protocol byte-for-byte at the AstrLink boundary.
package planner

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
)

var (
	ErrEndpointDisabled      = errors.New("endpoint is disabled")
	ErrCapabilityUnavailable = errors.New("endpoint capability is unavailable")
)

// CapabilityUnavailableError identifies the exact Alpha capability required by
// a request. It unwraps to ErrCapabilityUnavailable for callers that only need
// to distinguish capability misses from invalid Endpoint configuration.
type CapabilityUnavailableError struct {
	Protocol  contract.ProtocolID
	Mode      contract.CapabilityMode
	Streaming bool
}

func (err *CapabilityUnavailableError) Error() string {
	return fmt.Sprintf(
		"%s: protocol=%q mode=%q streaming=%t",
		ErrCapabilityUnavailable,
		err.Protocol,
		err.Mode,
		err.Streaming,
	)
}

func (err *CapabilityUnavailableError) Unwrap() error {
	return ErrCapabilityUnavailable
}

type AlphaInput struct {
	Service   contract.Service
	Endpoint  contract.Endpoint // compatibility view for callers migrating to Service
	Protocol  contract.ProtocolID
	Mode      contract.CapabilityMode
	Streaming bool
}

// BuildAlpha selects an explicitly declared Endpoint capability and returns a
// protocol-preserving plan. RelayKit cannot be selected by this function.
func BuildAlpha(input AlphaInput) (contract.ExecutionPlan, error) {
	service := input.Service
	if service.ID == "" && input.Endpoint.ID != "" {
		service = contract.ServiceFromEndpoint(input.Endpoint)
	}
	if err := input.Protocol.Validate(); err != nil {
		return contract.ExecutionPlan{}, fmt.Errorf("protocol: %w", err)
	}
	if !input.Mode.Valid() {
		return contract.ExecutionPlan{}, fmt.Errorf("capability mode %q is not available in Alpha", input.Mode)
	}
	if err := service.Validate(); err != nil {
		return contract.ExecutionPlan{}, fmt.Errorf("endpoint: %w", err)
	}
	if !service.Enabled {
		return contract.ExecutionPlan{}, ErrEndpointDisabled
	}

	var selected *contract.Capability
	for index := range service.Capabilities {
		capability := &service.Capabilities[index]
		if capability.Protocol == input.Protocol && capability.Mode == input.Mode {
			selected = capability
			break
		}
	}
	if selected == nil || (input.Streaming && !selected.Streaming) {
		return contract.ExecutionPlan{}, &CapabilityUnavailableError{
			Protocol:  input.Protocol,
			Mode:      input.Mode,
			Streaming: input.Streaming,
		}
	}

	planType := contract.PlanTypeNative
	if selected.Mode == contract.CapabilityModeDelegated {
		planType = contract.PlanTypeDelegated
	}
	plan := contract.ExecutionPlan{
		Type:             planType,
		ServiceID:        service.ID,
		InputProtocol:    input.Protocol,
		UpstreamProtocol: input.Protocol,
		ConversionPath:   []contract.ConversionEdge{},
		Streaming:        input.Streaming,
	}
	if err := plan.ValidateForAlpha(); err != nil {
		return contract.ExecutionPlan{}, fmt.Errorf("build Alpha plan: %w", err)
	}
	return plan, nil
}
