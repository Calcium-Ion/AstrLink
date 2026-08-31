package planner

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
)

var ErrConversionUnavailable = errors.New("relaykit conversion edge is unavailable")

type RelayKitInput struct {
	Service          contract.Service
	Endpoint         contract.Endpoint // compatibility view for callers migrating to Service
	InputProtocol    contract.ProtocolID
	UpstreamProtocol contract.ProtocolID
	Streaming        bool
	Edges            []contract.ConversionEdge
}

// BuildRelayKit binds exactly one engine-advertised conversion edge to a
// natively-capable upstream endpoint. Multi-edge planning is intentionally not
// selected by the ingress MVP.
func BuildRelayKit(input RelayKitInput) (contract.ExecutionPlan, error) {
	service := input.Service
	if service.ID == "" && input.Endpoint.ID != "" {
		service = contract.ServiceFromEndpoint(input.Endpoint)
	}
	if err := input.InputProtocol.Validate(); err != nil {
		return contract.ExecutionPlan{}, fmt.Errorf("input protocol: %w", err)
	}
	if err := input.UpstreamProtocol.Validate(); err != nil {
		return contract.ExecutionPlan{}, fmt.Errorf("upstream protocol: %w", err)
	}
	if err := service.Validate(); err != nil {
		return contract.ExecutionPlan{}, fmt.Errorf("endpoint: %w", err)
	}
	if !service.Enabled {
		return contract.ExecutionPlan{}, ErrEndpointDisabled
	}
	if !supportsNative(service, input.UpstreamProtocol, input.Streaming) &&
		!hasDeclaredConversion(service, input.InputProtocol, input.UpstreamProtocol, input.Streaming) {
		return contract.ExecutionPlan{}, &CapabilityUnavailableError{
			Protocol: input.UpstreamProtocol, Mode: contract.CapabilityModeNative, Streaming: input.Streaming,
		}
	}
	var selected *contract.ConversionEdge
	for index := range input.Edges {
		edge := &input.Edges[index]
		if edge.From == input.InputProtocol && edge.To == input.UpstreamProtocol &&
			(!input.Streaming || edge.Streaming) {
			selected = edge
			break
		}
	}
	if selected == nil {
		return contract.ExecutionPlan{}, fmt.Errorf("%w: %s to %s streaming=%t",
			ErrConversionUnavailable, input.InputProtocol, input.UpstreamProtocol, input.Streaming)
	}
	plan := contract.ExecutionPlan{
		Type: contract.PlanTypeRelayKit, ServiceID: service.ID,
		InputProtocol: input.InputProtocol, UpstreamProtocol: input.UpstreamProtocol,
		ConversionPath: []contract.ConversionEdge{*selected}, ConversionQuality: selected.Quality,
		Streaming: input.Streaming,
	}
	if err := plan.ValidateForRuntime(contract.RuntimeProfile{RelayKitAvailable: true, Edges: input.Edges}); err != nil {
		return contract.ExecutionPlan{}, fmt.Errorf("build RelayKit plan: %w", err)
	}
	return plan, nil
}

func supportsNative(endpoint contract.Service, protocol contract.ProtocolID, streaming bool) bool {
	for _, capability := range endpoint.Capabilities {
		if capability.Protocol == protocol && capability.ConvertTo == "" &&
			(!streaming || capability.Streaming) {
			return true
		}
	}
	return false
}

func hasDeclaredConversion(
	endpoint contract.Service,
	from contract.ProtocolID,
	to contract.ProtocolID,
	streaming bool,
) bool {
	for _, capability := range endpoint.Capabilities {
		if capability.Protocol == from && capability.ConvertTo == to &&
			(!streaming || capability.Streaming) {
			return true
		}
	}
	return false
}
