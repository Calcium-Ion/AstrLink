package endpoint

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type resolverStore struct {
	endpoints []contract.Endpoint
	routes    []contract.Route
	routeErr  error
}

func (store resolverStore) ListEndpoints(context.Context, storage.EndpointListOptions) (storage.EndpointPage, error) {
	items := make([]storage.EndpointRecord, 0, len(store.endpoints))
	for _, candidate := range store.endpoints {
		items = append(items, storage.EndpointRecord{Endpoint: candidate})
	}
	return storage.EndpointPage{Items: items}, nil
}

func (store resolverStore) ListRoutes(context.Context) ([]storage.RouteRecord, error) {
	if store.routeErr != nil {
		return nil, store.routeErr
	}
	items := make([]storage.RouteRecord, 0, len(store.routes))
	for _, route := range store.routes {
		items = append(items, storage.RouteRecord{Route: route})
	}
	return items, nil
}

func TestStoreResolverCandidateOrdering(t *testing.T) {
	request := ResolveRequest{Protocol: contract.ProtocolOpenAIResponses, Model: "gpt-5", Streaming: true}
	tests := []struct {
		name      string
		endpoints []contract.Endpoint
		routes    []contract.Route
		want      []string
		wantRoute contract.RouteID
		wantPin   bool
	}{
		{
			name: "no matching route preserves native ID then delegated ID",
			endpoints: []contract.Endpoint{
				resolverEndpoint("endpoint_z", contract.CapabilityModeDelegated, true, nil),
				resolverEndpoint("endpoint_c", contract.CapabilityModeNative, true, nil),
				resolverEndpoint("endpoint_a", contract.CapabilityModeDelegated, true, nil),
				resolverEndpoint("endpoint_b", contract.CapabilityModeNative, true, nil),
			},
			routes: []contract.Route{
				resolverRoute("route_other", 0, "other", resolverTarget("endpoint_z", contract.PlanTypeDelegated, 0)),
			},
			want: []string{"endpoint_b/native", "endpoint_c/native", "endpoint_a/delegated", "endpoint_z/delegated"},
		},
		{
			name: "lower route priority selects one route only",
			endpoints: []contract.Endpoint{
				resolverEndpoint("endpoint_a", contract.CapabilityModeNative, true, nil),
				resolverEndpoint("endpoint_b", contract.CapabilityModeNative, true, nil),
			},
			routes: []contract.Route{
				resolverRoute("route_later", 20, "", resolverTarget("endpoint_a", contract.PlanTypeNative, 0)),
				resolverRoute("route_first", 10, "", resolverTarget("endpoint_b", contract.PlanTypeNative, 0)),
			},
			want:      []string{"endpoint_b/native"},
			wantRoute: "route_first",
			wantPin:   true,
		},
		{
			name: "exact model wins equal priority before lexical route ID",
			endpoints: []contract.Endpoint{
				resolverEndpoint("endpoint_exact", contract.CapabilityModeNative, true, nil),
				resolverEndpoint("endpoint_wild", contract.CapabilityModeNative, true, nil),
			},
			routes: []contract.Route{
				resolverRoute("route_a_wild", 5, "", resolverTarget("endpoint_wild", contract.PlanTypeNative, 0)),
				resolverRoute("route_z_exact", 5, "gpt-5", resolverTarget("endpoint_exact", contract.PlanTypeNative, 0)),
			},
			want:      []string{"endpoint_exact/native"},
			wantRoute: "route_z_exact",
			wantPin:   true,
		},
		{
			name: "lexical route ID is final route tie break",
			endpoints: []contract.Endpoint{
				resolverEndpoint("endpoint_a", contract.CapabilityModeNative, true, nil),
				resolverEndpoint("endpoint_z", contract.CapabilityModeNative, true, nil),
			},
			routes: []contract.Route{
				resolverRoute("route_z", 5, "", resolverTarget("endpoint_z", contract.PlanTypeNative, 0)),
				resolverRoute("route_a", 5, "", resolverTarget("endpoint_a", contract.PlanTypeNative, 0)),
			},
			want:      []string{"endpoint_a/native"},
			wantRoute: "route_a",
			wantPin:   true,
		},
		{
			name: "target priority then mode then endpoint ID",
			endpoints: []contract.Endpoint{
				resolverEndpoint("endpoint_d2", contract.CapabilityModeDelegated, true, nil),
				resolverEndpoint("endpoint_n2", contract.CapabilityModeNative, true, nil),
				resolverEndpoint("endpoint_d1", contract.CapabilityModeDelegated, true, nil),
				resolverEndpoint("endpoint_n1", contract.CapabilityModeNative, true, nil),
			},
			routes: []contract.Route{
				resolverRoute("route_ordered", 0, "",
					resolverTarget("endpoint_d2", contract.PlanTypeDelegated, 0),
					resolverTarget("endpoint_n2", contract.PlanTypeNative, 1),
					resolverTarget("endpoint_d1", contract.PlanTypeDelegated, 0),
					resolverTarget("endpoint_n1", contract.PlanTypeNative, 0),
				),
			},
			want: []string{
				"endpoint_n1/native",
				"endpoint_d1/delegated",
				"endpoint_d2/delegated",
				"endpoint_n2/native",
			},
			wantRoute: "route_ordered",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver, err := NewStoreResolver(resolverStore{endpoints: test.endpoints, routes: test.routes})
			if err != nil {
				t.Fatal(err)
			}
			candidates, err := resolver.ResolveCandidates(context.Background(), request)
			if err != nil {
				t.Fatalf("ResolveCandidates: %v", err)
			}
			got := make([]string, 0, len(candidates))
			for _, candidate := range candidates {
				got = append(got, string(candidate.Endpoint.ID)+"/"+string(candidate.Mode))
				if candidate.RouteID != test.wantRoute {
					t.Errorf("RouteID = %q, want %q", candidate.RouteID, test.wantRoute)
				}
				if candidate.Pinned != test.wantPin {
					t.Errorf("Pinned = %t, want %t", candidate.Pinned, test.wantPin)
				}
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("candidate order = %v, want %v", got, test.want)
			}
		})
	}
}

func TestStoreResolverMatchingRouteDoesNotFallThrough(t *testing.T) {
	store := resolverStore{
		endpoints: []contract.Endpoint{
			resolverEndpoint("endpoint_fallback", contract.CapabilityModeNative, true, nil),
			resolverEndpoint("endpoint_nonstream", contract.CapabilityModeNative, false, nil),
		},
		routes: []contract.Route{
			resolverRoute("route_first", 0, "", resolverTarget("endpoint_nonstream", contract.PlanTypeNative, 0)),
			resolverRoute("route_later", 1, "", resolverTarget("endpoint_fallback", contract.PlanTypeNative, 0)),
		},
	}
	resolver, err := NewStoreResolver(store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.ResolveCandidates(context.Background(), ResolveRequest{
		Protocol:  contract.ProtocolOpenAIResponses,
		Model:     "gpt-5",
		Streaming: true,
	})
	if !errors.Is(err, ErrNoEndpoint) {
		t.Fatalf("ResolveCandidates error = %v, want ErrNoEndpoint", err)
	}
	var capabilityErr *CapabilityUnavailableError
	if !errors.As(err, &capabilityErr) {
		t.Fatalf("ResolveCandidates error type = %T, want *CapabilityUnavailableError", err)
	}
	if capabilityErr.Protocol != contract.ProtocolOpenAIResponses ||
		!reflect.DeepEqual(capabilityErr.Modes, []contract.CapabilityMode{contract.CapabilityModeNative}) ||
		!capabilityErr.Streaming {
		t.Fatalf("capability error = %#v", capabilityErr)
	}
}

func TestStoreResolverDeduplicatesOnePhysicalPinnedEndpoint(t *testing.T) {
	candidate := resolverEndpoint("endpoint_both", contract.CapabilityModeNative, true, nil)
	candidate.Capabilities = append(candidate.Capabilities, contract.Capability{
		Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeDelegated, Streaming: true,
	})
	resolver, err := NewStoreResolver(resolverStore{
		endpoints: []contract.Endpoint{candidate},
		routes: []contract.Route{
			resolverRoute(
				"route_pin",
				0,
				"",
				resolverTarget("endpoint_both", contract.PlanTypeDelegated, 0),
				resolverTarget("endpoint_both", contract.PlanTypeNative, 0),
			),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := resolver.ResolveCandidates(context.Background(), ResolveRequest{
		Protocol:  contract.ProtocolOpenAIResponses,
		Model:     "gpt-5",
		Streaming: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Mode != contract.CapabilityModeNative || !candidates[0].Pinned {
		t.Fatalf("candidates = %#v", candidates)
	}
}

func TestStoreResolverListAliasModels(t *testing.T) {
	tests := []struct {
		name      string
		discovery contract.ProtocolID
		routes    []contract.Route
		want      []string
	}{
		{
			name:      "filters disabled no-rewrite and wrong family",
			discovery: contract.ProtocolOpenAIModels,
			routes: []contract.Route{
				func() contract.Route {
					route := resolverRoute("route_ok", 0, "alias-b",
						resolverTarget("endpoint_a", contract.PlanTypeNative, 0))
					route.Targets[0].UpstreamModel = "real-b"
					return route
				}(),
				func() contract.Route {
					route := resolverRoute("route_a", 0, "alias-a",
						resolverTarget("endpoint_a", contract.PlanTypeNative, 0))
					route.Targets[0].UpstreamModel = "real-a"
					return route
				}(),
				func() contract.Route {
					route := resolverRoute("route_disabled", 0, "alias-disabled",
						resolverTarget("endpoint_a", contract.PlanTypeNative, 0))
					route.Enabled = false
					route.Targets[0].UpstreamModel = "real"
					return route
				}(),
				resolverRoute("route_norewrite", 0, "alias-plain",
					resolverTarget("endpoint_a", contract.PlanTypeNative, 0)),
				func() contract.Route {
					route := resolverRoute("route_gemini", 0, "alias-gemini",
						resolverTarget("endpoint_a", contract.PlanTypeNative, 0))
					route.Match.Protocol = contract.ProtocolGoogleGenerateContent
					route.Targets[0].UpstreamProtocol = contract.ProtocolGoogleGenerateContent
					route.Targets[0].UpstreamModel = "gemini-real"
					return route
				}(),
				func() contract.Route {
					route := resolverRoute("route_dup", 0, "alias-a",
						resolverTarget("endpoint_a", contract.PlanTypeNative, 0))
					route.Targets[0].UpstreamModel = "real-a-dup"
					return route
				}(),
			},
			want: []string{"alias-a", "alias-b"},
		},
		{
			name:      "gemini discovery maps generate_content routes",
			discovery: contract.ProtocolGoogleModels,
			routes: []contract.Route{
				func() contract.Route {
					route := resolverRoute("route_openai", 0, "alias-openai",
						resolverTarget("endpoint_a", contract.PlanTypeNative, 0))
					route.Targets[0].UpstreamModel = "real"
					return route
				}(),
				func() contract.Route {
					route := resolverRoute("route_gemini", 0, "alias-gemini",
						resolverTarget("endpoint_a", contract.PlanTypeNative, 0))
					route.Match.Protocol = contract.ProtocolGoogleGenerateContent
					route.Targets[0].UpstreamProtocol = contract.ProtocolGoogleGenerateContent
					route.Targets[0].UpstreamModel = "gemini-real"
					return route
				}(),
			},
			want: []string{"alias-gemini"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver, err := NewStoreResolver(resolverStore{
				endpoints: []contract.Endpoint{
					resolverEndpoint("endpoint_a", contract.CapabilityModeNative, true, nil),
				},
				routes: test.routes,
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := resolver.ListAliasModels(context.Background(), test.discovery)
			if err != nil {
				t.Fatalf("ListAliasModels: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("aliases = %v, want %v", got, test.want)
			}
		})
	}
}

func TestStoreResolverCarriesTargetUpstreamModel(t *testing.T) {
	tests := []struct {
		name      string
		endpoints []contract.Endpoint
		routes    []contract.Route
		want      []string
	}{
		{
			name: "resolved candidate carries target upstream model",
			endpoints: []contract.Endpoint{
				resolverEndpoint("endpoint_a", contract.CapabilityModeNative, true, nil),
				resolverEndpoint("endpoint_b", contract.CapabilityModeNative, true, nil),
			},
			routes: []contract.Route{
				func() contract.Route {
					route := resolverRoute(
						"route_alias",
						0,
						"public-alias",
						resolverTarget("endpoint_a", contract.PlanTypeNative, 0),
						resolverTarget("endpoint_b", contract.PlanTypeNative, 1),
					)
					route.Targets[0].UpstreamModel = "real-a"
					route.Targets[1].UpstreamModel = "real-b"
					return route
				}(),
			},
			want: []string{"endpoint_a/real-a", "endpoint_b/real-b"},
		},
		{
			name: "duplicate endpoint keeps first sorted target rewrite",
			endpoints: []contract.Endpoint{
				func() contract.Endpoint {
					candidate := resolverEndpoint("endpoint_both", contract.CapabilityModeNative, true, nil)
					candidate.Capabilities = append(candidate.Capabilities, contract.Capability{
						Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeDelegated, Streaming: true,
					})
					return candidate
				}(),
			},
			routes: []contract.Route{
				func() contract.Route {
					// Native ranks before delegated at equal priority, so the
					// native target's rewrite must survive dedupe.
					route := resolverRoute(
						"route_pin",
						0,
						"public-alias",
						resolverTarget("endpoint_both", contract.PlanTypeDelegated, 0),
						resolverTarget("endpoint_both", contract.PlanTypeNative, 0),
					)
					route.Targets[0].UpstreamModel = "delegated-rewrite"
					route.Targets[1].UpstreamModel = "native-rewrite"
					return route
				}(),
			},
			want: []string{"endpoint_both/native-rewrite"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver, err := NewStoreResolver(resolverStore{endpoints: test.endpoints, routes: test.routes})
			if err != nil {
				t.Fatal(err)
			}
			candidates, err := resolver.ResolveCandidates(context.Background(), ResolveRequest{
				Protocol:  contract.ProtocolOpenAIResponses,
				Model:     "public-alias",
				Streaming: true,
			})
			if err != nil {
				t.Fatalf("ResolveCandidates: %v", err)
			}
			got := make([]string, 0, len(candidates))
			for _, candidate := range candidates {
				got = append(got, string(candidate.Endpoint.ID)+"/"+candidate.UpstreamModel)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("candidates = %v, want %v", got, test.want)
			}
		})
	}
}

func TestStoreResolverAppliesServiceAllowlistToEffectiveRouteModel(t *testing.T) {
	t.Run("filters targets by rewritten upstream model", func(t *testing.T) {
		route := resolverRoute(
			"route_alias",
			0,
			"public-alias",
			resolverTarget("endpoint_blocked", contract.PlanTypeNative, 0),
			resolverTarget("endpoint_allowed", contract.PlanTypeNative, 1),
		)
		route.Targets[0].UpstreamModel = "real-blocked"
		route.Targets[1].UpstreamModel = "real-allowed"
		resolver, err := NewStoreResolver(resolverStore{
			endpoints: []contract.Endpoint{
				resolverEndpoint("endpoint_blocked", contract.CapabilityModeNative, true, []string{"public-alias"}),
				resolverEndpoint("endpoint_allowed", contract.CapabilityModeNative, true, []string{"real-allowed"}),
			},
			routes: []contract.Route{route},
		})
		if err != nil {
			t.Fatal(err)
		}
		candidates, err := resolver.ResolveCandidates(context.Background(), ResolveRequest{
			Protocol: contract.ProtocolOpenAIResponses,
			Model:    "public-alias",
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(candidates) != 1 || candidates[0].Service.ID != "endpoint_allowed" {
			t.Fatalf("candidates = %#v, want only endpoint_allowed", candidates)
		}
	})

	t.Run("uses public model when target has no rewrite", func(t *testing.T) {
		resolver, err := NewStoreResolver(resolverStore{
			endpoints: []contract.Endpoint{
				resolverEndpoint("endpoint_no_public", contract.CapabilityModeNative, true, []string{"other"}),
			},
			routes: []contract.Route{
				resolverRoute(
					"route_public",
					0,
					"public-alias",
					resolverTarget("endpoint_no_public", contract.PlanTypeNative, 0),
				),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = resolver.ResolveCandidates(context.Background(), ResolveRequest{
			Protocol: contract.ProtocolOpenAIResponses,
			Model:    "public-alias",
		})
		if !errors.Is(err, ErrNoEndpoint) {
			t.Fatalf("ResolveCandidates() error = %v, want ErrNoEndpoint", err)
		}
	})
}

func TestStoreResolverRejectsAlphaIneligibleRouteAndRouteReadFailure(t *testing.T) {
	relayRoute := resolverRoute(
		"route_relay",
		0,
		"",
		resolverTarget("endpoint_01", contract.PlanTypeRelayKit, 0),
	)
	relayRoute.Targets[0].UpstreamProtocol = contract.ProtocolOpenAIChat
	resolver, err := NewStoreResolver(resolverStore{
		endpoints: []contract.Endpoint{
			resolverEndpoint("endpoint_01", contract.CapabilityModeNative, true, nil),
		},
		routes: []contract.Route{relayRoute},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveCandidates(context.Background(), ResolveRequest{
		Protocol: contract.ProtocolOpenAIResponses, Model: "gpt-5",
	}); !errors.Is(err, ErrNoEndpoint) {
		t.Fatalf("unavailable RelayKit route error = %v, want no endpoint", err)
	}

	privateErr := errors.New("route database unavailable")
	resolver, _ = NewStoreResolver(resolverStore{routeErr: privateErr})
	if _, err := resolver.ResolveCandidates(context.Background(), ResolveRequest{
		Protocol: contract.ProtocolOpenAIResponses,
	}); !errors.Is(err, privateErr) {
		t.Fatalf("route read error = %v", err)
	}
}

func resolverRoute(
	id contract.RouteID,
	priority int,
	model string,
	targets ...contract.RouteTarget,
) contract.Route {
	return contract.Route{
		ID:       id,
		Name:     string(id),
		Enabled:  true,
		Priority: priority,
		Match: contract.RouteMatch{
			Protocol: contract.ProtocolOpenAIResponses,
			Model:    model,
		},
		Targets: targets,
	}
}

func resolverTarget(
	id contract.ServiceID,
	planType contract.PlanType,
	priority int,
) contract.RouteTarget {
	return contract.RouteTarget{
		ServiceID:        id,
		PlanType:         planType,
		UpstreamProtocol: contract.ProtocolOpenAIResponses,
		Priority:         priority,
	}
}
