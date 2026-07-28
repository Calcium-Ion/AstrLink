package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRouteUpstreamModelValidation(t *testing.T) {
	validAlias := Route{
		ID: "route_01", Name: "alias", Enabled: true,
		Match: RouteMatch{Protocol: ProtocolOpenAIResponses, Model: "public-alias"},
		Targets: []RouteTarget{{
			ServiceID: "endpoint_01", PlanType: PlanTypeNative,
			UpstreamProtocol: ProtocolOpenAIResponses,
			UpstreamModel:    "provider/real-model",
		}},
	}
	tests := []struct {
		name    string
		route   Route
		wantErr string
	}{
		{
			name:  "accepts valid alias route",
			route: validAlias,
		},
		{
			name: "absent upstream model remains valid",
			route: Route{
				ID: "route_01", Name: "plain", Enabled: true,
				Match: RouteMatch{Protocol: ProtocolOpenAIResponses, Model: "gpt-5"},
				Targets: []RouteTarget{{
					ServiceID: "endpoint_01", PlanType: PlanTypeNative,
					UpstreamProtocol: ProtocolOpenAIResponses,
				}},
			},
		},
		{
			name: "accepts upstream model on exact-model relaykit target",
			route: Route{
				ID: "route_01", Name: "relay", Enabled: true,
				Match: RouteMatch{Protocol: ProtocolOpenAIResponses, Model: "public-alias"},
				Targets: []RouteTarget{{
					ServiceID: "endpoint_01", PlanType: PlanTypeRelayKit,
					UpstreamProtocol: ProtocolAnthropicMessages,
					UpstreamModel:    "provider/real-model",
				}},
			},
			wantErr: "",
		},
		{
			name: "rejects upstream model when match model is empty",
			route: Route{
				ID: "route_01", Name: "wildcard", Enabled: true,
				Match: RouteMatch{Protocol: ProtocolOpenAIResponses},
				Targets: []RouteTarget{{
					ServiceID: "endpoint_01", PlanType: PlanTypeNative,
					UpstreamProtocol: ProtocolOpenAIResponses,
					UpstreamModel:    "provider/real-model",
				}},
			},
			wantErr: "upstream model requires an exact match model",
		},
		{
			name: "rejects 257-rune upstream model",
			route: Route{
				ID: "route_01", Name: "long", Enabled: true,
				Match: RouteMatch{Protocol: ProtocolOpenAIResponses, Model: "public-alias"},
				Targets: []RouteTarget{{
					ServiceID: "endpoint_01", PlanType: PlanTypeNative,
					UpstreamProtocol: ProtocolOpenAIResponses,
					UpstreamModel:    strings.Repeat("m", 257),
				}},
			},
			wantErr: "upstream model exceeds 256 characters",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.route.Validate()
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestRouteAutoSelectionValidation(t *testing.T) {
	validAutoRoute := func() Route {
		return Route{
			ID: "route_auto", Name: "automatic model", Enabled: true,
			Match: RouteMatch{Protocol: ProtocolOpenAIResponses, Model: AstrLinkAutoModelID},
			Selection: &RouteSelection{
				Mode:       RouteSelectionModeAuto,
				TaxonomyID: AstrLinkTextClassificationV1,
			},
			Categories: []RouteCategory{
				{
					CategoryID: "general",
					Targets: []RouteTarget{
						{
							ServiceID: "endpoint_01", PlanType: PlanTypeNative,
							UpstreamProtocol: ProtocolOpenAIResponses,
							UpstreamModel:    "provider/economy-model",
						},
						{
							ServiceID: "endpoint_01", PlanType: PlanTypeNative,
							UpstreamProtocol: ProtocolOpenAIResponses,
							UpstreamModel:    "provider/quality-model",
							Priority:         1,
						},
					},
				},
				{
					CategoryID: "code",
					Targets: []RouteTarget{{
						ServiceID: "endpoint_01", PlanType: PlanTypeNative,
						UpstreamProtocol: ProtocolOpenAIResponses,
						UpstreamModel:    "provider/code-model",
					}},
				},
			},
		}
	}

	tests := []struct {
		name          string
		mutate        func(*Route)
		validateAlpha bool
		wantErr       string
	}{
		{
			name:   "accepts multiple models in one category on one endpoint",
			mutate: func(*Route) {},
		},
		{
			name: "rejects reserved model without auto selection",
			mutate: func(route *Route) {
				route.Selection = nil
			},
			wantErr: "reserved for auto selection",
		},
		{
			name: "rejects auto selection for another public model",
			mutate: func(route *Route) {
				route.Match.Model = "public-alias"
			},
			wantErr: `auto selection requires match model "astrlink/auto"`,
		},
		{
			name: "rejects auto selection without a taxonomy",
			mutate: func(route *Route) {
				route.Selection.TaxonomyID = ""
			},
			wantErr: "auto selection requires a valid taxonomy_id",
		},
		{
			name: "rejects an invalid taxonomy id",
			mutate: func(route *Route) {
				route.Selection.TaxonomyID = "AstrLink text v1"
			},
			wantErr: "auto selection requires a valid taxonomy_id",
		},
		{
			name: "rejects a taxonomy on priority selection",
			mutate: func(route *Route) {
				route.Match.Model = "public-alias"
				route.Selection.Mode = RouteSelectionModePriority
			},
			wantErr: "taxonomy_id is only valid for auto selection",
		},
		{
			name: "rejects fewer than two categories",
			mutate: func(route *Route) {
				route.Categories = route.Categories[:1]
			},
			wantErr: "auto selection requires at least two categories",
		},
		{
			name: "rejects an empty category",
			mutate: func(route *Route) {
				route.Categories[1].Targets = nil
			},
			wantErr: "categories[1] requires at least one target",
		},
		{
			name: "rejects a target without a concrete upstream model",
			mutate: func(route *Route) {
				route.Categories[1].Targets[0].UpstreamModel = ""
			},
			wantErr: "auto selection requires an upstream model",
		},
		{
			name: "rejects an invalid category",
			mutate: func(route *Route) {
				route.Categories[1].CategoryID = "Code Generation"
			},
			wantErr: "category_id is invalid",
		},
		{
			name: "rejects a duplicate category",
			mutate: func(route *Route) {
				route.Categories[1].CategoryID = route.Categories[0].CategoryID
			},
			wantErr: "duplicates category",
		},
		{
			name: "rejects one distinct upstream model",
			mutate: func(route *Route) {
				model := route.Categories[0].Targets[0].UpstreamModel
				for categoryIndex := range route.Categories {
					for targetIndex := range route.Categories[categoryIndex].Targets {
						route.Categories[categoryIndex].Targets[targetIndex].UpstreamModel = model
					}
				}
			},
			wantErr: "auto selection requires at least two distinct upstream models",
		},
		{
			name: "rejects top-level targets on auto selection",
			mutate: func(route *Route) {
				route.Targets = []RouteTarget{route.Categories[0].Targets[0]}
			},
			wantErr: "top-level targets are not valid for auto selection",
		},
		{
			name: "rejects categories without auto selection",
			mutate: func(route *Route) {
				route.Match.Model = "public-alias"
				route.Selection = nil
			},
			wantErr: "categories require auto selection",
		},
		{
			name: "rejects an unknown selection mode",
			mutate: func(route *Route) {
				route.Selection.Mode = "semantic"
			},
			wantErr: `unknown route selection mode "semantic"`,
		},
		{
			name:          "rejects auto mode from the current runtime guard",
			mutate:        func(*Route) {},
			validateAlpha: true,
			wantErr:       "not implemented in this build",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			route := validAutoRoute()
			test.mutate(&route)

			var err error
			if test.validateAlpha {
				err = route.ValidateForAlpha()
			} else {
				err = route.Validate()
			}
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestRoutePrioritySelectionRemainsBackwardCompatible(t *testing.T) {
	route := Route{
		ID: "route_01", Name: "priority", Enabled: true,
		Match:     RouteMatch{Protocol: ProtocolOpenAIResponses, Model: "public-alias"},
		Selection: &RouteSelection{Mode: RouteSelectionModePriority},
		Targets: []RouteTarget{{
			ServiceID: "endpoint_01", PlanType: PlanTypeNative,
			UpstreamProtocol: ProtocolOpenAIResponses,
			UpstreamModel:    "provider/real-model",
		}},
	}
	if err := route.ValidateForAlpha(); err != nil {
		t.Fatalf("ValidateForAlpha() error = %v", err)
	}

	route.Selection = nil
	if err := route.ValidateForAlpha(); err != nil {
		t.Fatalf("ValidateForAlpha() without selection error = %v", err)
	}
	encoded, err := json.Marshal(route)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), `"selection"`) {
		t.Fatalf("legacy route unexpectedly materialized selection: %s", encoded)
	}
	if strings.Contains(string(encoded), `"categories"`) {
		t.Fatalf("legacy route unexpectedly materialized categories: %s", encoded)
	}
}
