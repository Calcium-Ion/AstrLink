package contract

import (
	"strings"
	"testing"
)

func TestRouteUpstreamModelValidation(t *testing.T) {
	validAlias := Route{
		ID: "route_01", Name: "alias", Enabled: true,
		Match: RouteMatch{Protocol: ProtocolOpenAIResponses, Model: "public-alias"},
		Targets: []RouteTarget{{
			EndpointID: "endpoint_01", PlanType: PlanTypeNative,
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
					EndpointID: "endpoint_01", PlanType: PlanTypeNative,
					UpstreamProtocol: ProtocolOpenAIResponses,
				}},
			},
		},
		{
			name: "rejects upstream model on relaykit target",
			route: Route{
				ID: "route_01", Name: "relay", Enabled: true,
				Match: RouteMatch{Protocol: ProtocolOpenAIResponses, Model: "public-alias"},
				Targets: []RouteTarget{{
					EndpointID: "endpoint_01", PlanType: PlanTypeRelayKit,
					UpstreamProtocol: ProtocolAnthropicMessages,
					UpstreamModel:    "provider/real-model",
				}},
			},
			wantErr: "upstream model is not supported on relaykit targets",
		},
		{
			name: "rejects upstream model when match model is empty",
			route: Route{
				ID: "route_01", Name: "wildcard", Enabled: true,
				Match: RouteMatch{Protocol: ProtocolOpenAIResponses},
				Targets: []RouteTarget{{
					EndpointID: "endpoint_01", PlanType: PlanTypeNative,
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
					EndpointID: "endpoint_01", PlanType: PlanTypeNative,
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
