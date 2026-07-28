package contract

import (
	"fmt"
	"unicode/utf8"
)

type RouteID string

const AstrLinkAutoModelID = "astrlink/auto"
const AstrLinkTextClassificationV1 = "astrlink-text-v1"

const maxRouteClassificationIDBytes = 64

type RouteSelectionMode string

const (
	RouteSelectionModePriority RouteSelectionMode = "priority"
	RouteSelectionModeAuto     RouteSelectionMode = "auto"
)

func (mode RouteSelectionMode) Valid() bool {
	return mode == RouteSelectionModePriority || mode == RouteSelectionModeAuto
}

// RouteSelection controls how an eligible target becomes the first attempt.
// A missing selection preserves the historical priority behavior.
type RouteSelection struct {
	Mode       RouteSelectionMode `json:"mode"`
	TaxonomyID string             `json:"taxonomy_id,omitempty"`
}

func (selection RouteSelection) Validate() error {
	if !selection.Mode.Valid() {
		return fmt.Errorf("unknown route selection mode %q", selection.Mode)
	}
	if selection.Mode == RouteSelectionModeAuto {
		if !validRouteClassificationID(selection.TaxonomyID) {
			return fmt.Errorf("auto selection requires a valid taxonomy_id")
		}
		return nil
	}
	if selection.TaxonomyID != "" {
		return fmt.Errorf("taxonomy_id is only valid for auto selection")
	}
	return nil
}

type RouteMatch struct {
	Protocol ProtocolID `json:"protocol"`
	// Model is an exact public model or alias. An empty value matches any model.
	Model string `json:"model,omitempty"`
}

type RouteTarget struct {
	ServiceID        ServiceID  `json:"service_id"`
	PlanType         PlanType   `json:"plan_type"`
	UpstreamProtocol ProtocolID `json:"upstream_protocol"`
	Priority         int        `json:"priority"`
	// UpstreamModel rewrites the public model to this value for this target.
	// Empty preserves the public model bytes exactly (ADR 0006).
	UpstreamModel string `json:"upstream_model,omitempty"`
}

// UnmarshalJSON keeps persisted pre-v12 routes readable while ensuring all
// newly marshaled documents use the canonical service_id field.
func (target *RouteTarget) UnmarshalJSON(data []byte) error {
	var wire struct {
		ServiceID        *ServiceID `json:"service_id"`
		EndpointID       *ServiceID `json:"endpoint_id"`
		PlanType         PlanType   `json:"plan_type"`
		UpstreamProtocol ProtocolID `json:"upstream_protocol"`
		Priority         int        `json:"priority"`
		UpstreamModel    string     `json:"upstream_model,omitempty"`
	}
	if err := decodeStrictContractJSON(data, &wire); err != nil {
		return err
	}
	if wire.ServiceID != nil && wire.EndpointID != nil {
		return fmt.Errorf("route target cannot contain both service_id and endpoint_id")
	}
	serviceID := ServiceID("")
	if wire.ServiceID != nil {
		serviceID = *wire.ServiceID
	} else if wire.EndpointID != nil {
		serviceID = *wire.EndpointID
	}
	*target = RouteTarget{
		ServiceID:        serviceID,
		PlanType:         wire.PlanType,
		UpstreamProtocol: wire.UpstreamProtocol,
		Priority:         wire.Priority,
		UpstreamModel:    wire.UpstreamModel,
	}
	return nil
}

// RouteCategory owns the concrete model candidates configured for one
// classifier output. It is Route configuration, not model capability metadata.
type RouteCategory struct {
	CategoryID string        `json:"category_id"`
	Targets    []RouteTarget `json:"targets"`
}

type Route struct {
	ID         RouteID         `json:"id"`
	Name       string          `json:"name"`
	Enabled    bool            `json:"enabled"`
	Priority   int             `json:"priority"`
	Match      RouteMatch      `json:"match"`
	Selection  *RouteSelection `json:"selection,omitempty"`
	Targets    []RouteTarget   `json:"targets,omitempty"`
	Categories []RouteCategory `json:"categories,omitempty"`
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
	if route.Selection != nil {
		if err := route.Selection.Validate(); err != nil {
			return fmt.Errorf("selection: %w", err)
		}
	}
	if route.selectionMode() == RouteSelectionModeAuto {
		return route.validateAutoSelection()
	}
	return route.validatePrioritySelection()
}

func (route Route) validateTargets(targets []RouteTarget, path string) error {
	for index, target := range targets {
		if err := target.ServiceID.Validate(); err != nil {
			return fmt.Errorf("%s[%d]: %w", path, index, err)
		}
		if !target.PlanType.Valid() {
			return fmt.Errorf("%s[%d]: unknown plan type %q", path, index, target.PlanType)
		}
		if err := target.UpstreamProtocol.Validate(); err != nil {
			return fmt.Errorf("%s[%d]: upstream protocol: %w", path, index, err)
		}
		if target.Priority < 0 || target.Priority > 1_000_000 {
			return fmt.Errorf("%s[%d]: priority must be between 0 and 1000000", path, index)
		}
		if target.PlanType != PlanTypeRelayKit && target.UpstreamProtocol != route.Match.Protocol {
			return fmt.Errorf(
				"%s[%d]: %s target must preserve the ingress protocol",
				path,
				index,
				target.PlanType,
			)
		}
		if target.UpstreamModel != "" {
			if utf8.RuneCountInString(target.UpstreamModel) > 256 {
				return fmt.Errorf("%s[%d]: upstream model exceeds 256 characters", path, index)
			}
			if route.Match.Model == "" {
				return fmt.Errorf(
					"%s[%d]: upstream model requires an exact match model",
					path,
					index,
				)
			}
		}
	}
	return nil
}

func (route Route) ValidateForAlpha() error {
	if err := route.Validate(); err != nil {
		return err
	}
	if route.selectionMode() == RouteSelectionModeAuto {
		return fmt.Errorf("route selection mode %q is not implemented in this build", RouteSelectionModeAuto)
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

func (route Route) ValidateForRuntime(profile RuntimeProfile) error {
	if err := route.Validate(); err != nil {
		return err
	}
	if route.selectionMode() == RouteSelectionModeAuto {
		return fmt.Errorf("route selection mode %q is not implemented in this build", RouteSelectionModeAuto)
	}
	if !route.Match.Protocol.AvailableInAlpha() {
		return fmt.Errorf("match protocol %q is not available in Alpha", route.Match.Protocol)
	}
	for index, target := range route.Targets {
		if target.PlanType == PlanTypeRelayKit && !profile.RelayKitAvailable {
			return fmt.Errorf("targets[%d]: plan type %q is not available in this runtime", index, target.PlanType)
		}
		if !target.UpstreamProtocol.AvailableInAlpha() {
			return fmt.Errorf("targets[%d]: upstream protocol %q is not available in Alpha", index, target.UpstreamProtocol)
		}
	}
	return nil
}

func (route Route) selectionMode() RouteSelectionMode {
	if route.Selection == nil {
		return RouteSelectionModePriority
	}
	return route.Selection.Mode
}

func (route Route) validatePrioritySelection() error {
	if route.Match.Model == AstrLinkAutoModelID {
		return fmt.Errorf("match model %q is reserved for auto selection", AstrLinkAutoModelID)
	}
	if route.Categories != nil {
		return fmt.Errorf("categories require auto selection")
	}
	if len(route.Targets) == 0 {
		return fmt.Errorf("route requires at least one target")
	}
	return route.validateTargets(route.Targets, "targets")
}

func (route Route) validateAutoSelection() error {
	if route.Match.Model != AstrLinkAutoModelID {
		return fmt.Errorf("auto selection requires match model %q", AstrLinkAutoModelID)
	}
	if route.Targets != nil {
		return fmt.Errorf("top-level targets are not valid for auto selection")
	}
	if len(route.Categories) < 2 {
		return fmt.Errorf("auto selection requires at least two categories")
	}

	models := make(map[string]struct{})
	categoryIDs := make(map[string]struct{}, len(route.Categories))
	for categoryIndex, category := range route.Categories {
		if !validRouteClassificationID(category.CategoryID) {
			return fmt.Errorf("categories[%d].category_id is invalid", categoryIndex)
		}
		if _, duplicate := categoryIDs[category.CategoryID]; duplicate {
			return fmt.Errorf(
				"categories[%d].category_id duplicates category %q",
				categoryIndex,
				category.CategoryID,
			)
		}
		categoryIDs[category.CategoryID] = struct{}{}
		if len(category.Targets) == 0 {
			return fmt.Errorf("categories[%d] requires at least one target", categoryIndex)
		}
		path := fmt.Sprintf("categories[%d].targets", categoryIndex)
		if err := route.validateTargets(category.Targets, path); err != nil {
			return err
		}
		for targetIndex, target := range category.Targets {
			if target.UpstreamModel == "" {
				return fmt.Errorf(
					"categories[%d].targets[%d]: auto selection requires an upstream model",
					categoryIndex,
					targetIndex,
				)
			}
			models[target.UpstreamModel] = struct{}{}
		}
	}
	if len(models) < 2 {
		return fmt.Errorf("auto selection requires at least two distinct upstream models")
	}
	return nil
}

func validRouteClassificationID(value string) bool {
	if len(value) == 0 || len(value) > maxRouteClassificationIDBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' {
			continue
		}
		if index > 0 && character >= '0' && character <= '9' {
			continue
		}
		if index > 0 && (character == '.' || character == '_' || character == '-') {
			continue
		}
		return false
	}
	return true
}
