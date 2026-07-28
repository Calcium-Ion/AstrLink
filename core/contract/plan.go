package contract

import "fmt"

type PlanType string

const (
	PlanTypeNative    PlanType = "native"
	PlanTypeDelegated PlanType = "delegated"
	PlanTypeRelayKit  PlanType = "relaykit"
)

func (planType PlanType) Valid() bool {
	switch planType {
	case PlanTypeNative, PlanTypeDelegated, PlanTypeRelayKit:
		return true
	default:
		return false
	}
}

func (planType PlanType) AvailableInAlpha() bool {
	return planType == PlanTypeNative || planType == PlanTypeDelegated
}

func (planType PlanType) UsesLocalConversion() bool {
	return planType == PlanTypeRelayKit
}

type ConversionQuality string

const (
	ConversionQualityGood        ConversionQuality = "good"
	ConversionQualityFair        ConversionQuality = "fair"
	ConversionQualityDiscouraged ConversionQuality = "discouraged"
)

func (quality ConversionQuality) Valid() bool {
	switch quality {
	case ConversionQualityGood, ConversionQualityFair, ConversionQualityDiscouraged:
		return true
	default:
		return false
	}
}

type ConversionEdge struct {
	From      ProtocolID        `json:"from"`
	To        ProtocolID        `json:"to"`
	Quality   ConversionQuality `json:"quality"`
	Streaming bool              `json:"streaming"`
}

// RuntimeProfile describes execution features supplied by the current Core
// composition. It deliberately does not change Alpha compatibility.
type RuntimeProfile struct {
	RelayKitAvailable bool
	Edges             []ConversionEdge
}

func (edge ConversionEdge) Validate() error {
	if err := edge.From.Validate(); err != nil {
		return fmt.Errorf("from: %w", err)
	}
	if err := edge.To.Validate(); err != nil {
		return fmt.Errorf("to: %w", err)
	}
	if edge.From == edge.To {
		return fmt.Errorf("conversion edge must change protocol")
	}
	if !edge.Quality.Valid() {
		return fmt.Errorf("unknown conversion quality %q", edge.Quality)
	}
	return nil
}

// ExecutionPlan records the externally meaningful route decision. Native and
// delegated plans preserve the ingress protocol at the AstrLink boundary.
// Only relaykit plans may contain a local conversion path.
type ExecutionPlan struct {
	Type              PlanType          `json:"plan_type"`
	ServiceID         ServiceID         `json:"service_id"`
	InputProtocol     ProtocolID        `json:"input_protocol"`
	UpstreamProtocol  ProtocolID        `json:"upstream_protocol"`
	ConversionPath    []ConversionEdge  `json:"conversion_path"`
	ConversionQuality ConversionQuality `json:"conversion_quality,omitempty"`
	Streaming         bool              `json:"streaming"`
}

// UnmarshalJSON accepts endpoint_id in historical request records while
// preserving service_id as the only current wire representation.
func (plan *ExecutionPlan) UnmarshalJSON(data []byte) error {
	var wire struct {
		Type              PlanType          `json:"plan_type"`
		ServiceID         *ServiceID        `json:"service_id"`
		EndpointID        *ServiceID        `json:"endpoint_id"`
		InputProtocol     ProtocolID        `json:"input_protocol"`
		UpstreamProtocol  ProtocolID        `json:"upstream_protocol"`
		ConversionPath    []ConversionEdge  `json:"conversion_path"`
		ConversionQuality ConversionQuality `json:"conversion_quality,omitempty"`
		Streaming         bool              `json:"streaming"`
	}
	if err := decodeStrictContractJSON(data, &wire); err != nil {
		return err
	}
	if wire.ServiceID != nil && wire.EndpointID != nil {
		return fmt.Errorf("execution plan cannot contain both service_id and endpoint_id")
	}
	serviceID := ServiceID("")
	if wire.ServiceID != nil {
		serviceID = *wire.ServiceID
	} else if wire.EndpointID != nil {
		serviceID = *wire.EndpointID
	}
	*plan = ExecutionPlan{
		Type:              wire.Type,
		ServiceID:         serviceID,
		InputProtocol:     wire.InputProtocol,
		UpstreamProtocol:  wire.UpstreamProtocol,
		ConversionPath:    wire.ConversionPath,
		ConversionQuality: wire.ConversionQuality,
		Streaming:         wire.Streaming,
	}
	return nil
}

func (plan ExecutionPlan) Validate() error {
	if !plan.Type.Valid() {
		return fmt.Errorf("unknown plan type %q", plan.Type)
	}
	if err := plan.ServiceID.Validate(); err != nil {
		return err
	}
	if err := plan.InputProtocol.Validate(); err != nil {
		return fmt.Errorf("input protocol: %w", err)
	}
	if err := plan.UpstreamProtocol.Validate(); err != nil {
		return fmt.Errorf("upstream protocol: %w", err)
	}
	if plan.ConversionPath == nil {
		return fmt.Errorf("conversion_path must be a non-null array")
	}

	switch plan.Type {
	case PlanTypeNative, PlanTypeDelegated:
		if plan.InputProtocol != plan.UpstreamProtocol {
			return fmt.Errorf("%s plan must preserve the ingress protocol", plan.Type)
		}
		if len(plan.ConversionPath) != 0 || plan.ConversionQuality != "" {
			return fmt.Errorf("%s plan must not contain a local conversion", plan.Type)
		}
	case PlanTypeRelayKit:
		if len(plan.ConversionPath) == 0 {
			return fmt.Errorf("relaykit plan requires a conversion path")
		}
		if !plan.ConversionQuality.Valid() {
			return fmt.Errorf("relaykit plan requires a valid conversion quality")
		}
		current := plan.InputProtocol
		for index, edge := range plan.ConversionPath {
			if err := edge.Validate(); err != nil {
				return fmt.Errorf("conversion_path[%d]: %w", index, err)
			}
			if edge.From != current {
				return fmt.Errorf("conversion_path[%d] does not continue from %q", index, current)
			}
			if plan.Streaming && !edge.Streaming {
				return fmt.Errorf("conversion_path[%d] does not support streaming", index)
			}
			current = edge.To
		}
		if current != plan.UpstreamProtocol {
			return fmt.Errorf("conversion path ends at %q, want %q", current, plan.UpstreamProtocol)
		}
	}
	return nil
}

func (plan ExecutionPlan) ValidateForAlpha() error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if !plan.Type.AvailableInAlpha() {
		return fmt.Errorf("plan type %q is not available in Alpha", plan.Type)
	}
	if !plan.InputProtocol.AvailableInAlpha() {
		return fmt.Errorf("input protocol %q is not available in Alpha", plan.InputProtocol)
	}
	if !plan.UpstreamProtocol.AvailableInAlpha() {
		return fmt.Errorf("upstream protocol %q is not available in Alpha", plan.UpstreamProtocol)
	}
	return nil
}

func (plan ExecutionPlan) ValidateForRuntime(profile RuntimeProfile) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if plan.Type == PlanTypeRelayKit && !profile.RelayKitAvailable {
		return fmt.Errorf("plan type %q is not available in this runtime", plan.Type)
	}
	if !plan.InputProtocol.AvailableInAlpha() {
		return fmt.Errorf("input protocol %q is not available in Alpha", plan.InputProtocol)
	}
	if !plan.UpstreamProtocol.AvailableInAlpha() {
		return fmt.Errorf("upstream protocol %q is not available in Alpha", plan.UpstreamProtocol)
	}
	if len(profile.Edges) == 0 || plan.Type != PlanTypeRelayKit {
		return nil
	}
	for index, edge := range plan.ConversionPath {
		found := false
		for _, available := range profile.Edges {
			if available.From == edge.From && available.To == edge.To {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("conversion_path[%d] is unavailable in this runtime", index)
		}
	}
	return nil
}
