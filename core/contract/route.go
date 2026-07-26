package contract

import (
	"fmt"
	"unicode/utf8"
)

type RouteID string

type RouteMatch struct {
	Protocol ProtocolID `json:"protocol"`
	// Model is an exact public model or alias. An empty value matches any model.
	Model string `json:"model,omitempty"`
}

type RouteTarget struct {
	EndpointID       EndpointID `json:"endpoint_id"`
	PlanType         PlanType   `json:"plan_type"`
	UpstreamProtocol ProtocolID `json:"upstream_protocol"`
	Priority         int        `json:"priority"`
	// UpstreamModel rewrites the public model to this value for this target.
	// Empty preserves the public model bytes exactly (ADR 0006).
	UpstreamModel string `json:"upstream_model,omitempty"`
}

type Route struct {
	ID       RouteID       `json:"id"`
	Name     string        `json:"name"`
	Enabled  bool          `json:"enabled"`
	Priority int           `json:"priority"`
	Match    RouteMatch    `json:"match"`
	Targets  []RouteTarget `json:"targets"`
}

func (route Route) Validate() error {
	if err := route.ID.Validate(); err != nil {
		return err
	}
	if route.Name == "" || utf8.RuneCountInString(route.Name) > 128 {
		return fmt.Errorf("route name must contain 1 to 128 characters")
	}
	if route.Priority < 0 || route.Priority > 1_000_000 {
		return fmt.Errorf("route priority must be between 0 and 1000000")
	}
	if err := route.Match.Protocol.Validate(); err != nil {
		return fmt.Errorf("match protocol: %w", err)
	}
	if utf8.RuneCountInString(route.Match.Model) > 256 {
		return fmt.Errorf("match model exceeds 256 characters")
	}
	if len(route.Targets) == 0 {
		return fmt.Errorf("route requires at least one target")
	}
	for index, target := range route.Targets {
		if err := target.EndpointID.Validate(); err != nil {
			return fmt.Errorf("targets[%d]: %w", index, err)
		}
		if !target.PlanType.Valid() {
			return fmt.Errorf("targets[%d]: unknown plan type %q", index, target.PlanType)
		}
		if err := target.UpstreamProtocol.Validate(); err != nil {
			return fmt.Errorf("targets[%d]: upstream protocol: %w", index, err)
		}
		if target.Priority < 0 || target.Priority > 1_000_000 {
			return fmt.Errorf("targets[%d]: priority must be between 0 and 1000000", index)
		}
		if target.PlanType != PlanTypeRelayKit && target.UpstreamProtocol != route.Match.Protocol {
			return fmt.Errorf("targets[%d]: %s target must preserve the ingress protocol", index, target.PlanType)
		}
		if target.UpstreamModel != "" {
			if utf8.RuneCountInString(target.UpstreamModel) > 256 {
				return fmt.Errorf("targets[%d]: upstream model exceeds 256 characters", index)
			}
			if target.PlanType == PlanTypeRelayKit {
				return fmt.Errorf("targets[%d]: upstream model is not supported on relaykit targets", index)
			}
			if route.Match.Model == "" {
				return fmt.Errorf("targets[%d]: upstream model requires an exact match model", index)
			}
		}
	}
	return nil
}

func (route Route) ValidateForAlpha() error {
	if err := route.Validate(); err != nil {
		return err
	}
	if !route.Match.Protocol.AvailableInAlpha() {
		return fmt.Errorf("match protocol %q is not available in Alpha", route.Match.Protocol)
	}
	for index, target := range route.Targets {
		if !target.PlanType.AvailableInAlpha() {
			return fmt.Errorf("targets[%d]: plan type %q is not available in Alpha", index, target.PlanType)
		}
		if !target.UpstreamProtocol.AvailableInAlpha() {
			return fmt.Errorf("targets[%d]: upstream protocol %q is not available in Alpha", index, target.UpstreamProtocol)
		}
	}
	return nil
}
