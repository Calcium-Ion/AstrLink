package ingress

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func discoveryEndpoint(
	id contract.EndpointID,
	protocol contract.ProtocolID,
	mode contract.CapabilityMode,
) contract.Endpoint {
	return contract.Endpoint{
		ID: id, Name: string(id), Kind: contract.EndpointKindOpenAICompatible,
		BaseURL: "https://" + strings.ReplaceAll(string(id), "_", "-") + ".example",
		Auth:    contract.EndpointAuth{Scheme: contract.AuthSchemeNone},
		Enabled: true,
		Capabilities: []contract.Capability{{
			Protocol: protocol, Mode: mode, Streaming: false,
		}},
	}
}

func discoveryHostEndpointID(host string) string {
	return strings.ReplaceAll(strings.TrimSuffix(host, ".example"), "-", "_")
}

func TestModelDiscoveryAggregatesDeterministicallyWithRoutingOrderConflictWins(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		protocol  contract.ProtocolID
		upstreams map[string]string
		wantPath  string
		wantBody  string
	}{
		{
			name:     "openai path merges openai and anthropic shaped listings",
			path:     "/v1/models",
			protocol: contract.ProtocolOpenAIModels,
			upstreams: map[string]string{
				"endpoint-b.example": `{"object":"list","data":[{"id":"shared-model","owned_by":"native-b"},{"id":"zeta-model"}]}`,
				"endpoint-c.example": `{"data":[{"id":"shared-model","owned_by":"native-c"},{"id":"delta-model"}],"first_id":"shared-model","has_more":false,"last_id":"delta-model"}`,
				"endpoint-a.example": `{"object":"list","data":[{"id":"alpha-model"},{"id":"shared-model","owned_by":"delegated-a"}]}`,
			},
			wantPath: "/v1/models",
			wantBody: `{"object":"list","data":[{"id":"alpha-model"},{"id":"delta-model"},{"id":"shared-model","owned_by":"native-b"},{"id":"zeta-model"}],"first_id":"alpha-model","has_more":false,"last_id":"zeta-model"}`,
		},
		{
			name:     "gemini path merges models arrays and drops page tokens",
			path:     "/v1beta/models?pageSize=3",
			protocol: contract.ProtocolGoogleModels,
			upstreams: map[string]string{
				"endpoint-b.example": `{"models":[{"name":"models/shared","version":"native-b"},{"name":"models/zeta"}]}`,
				"endpoint-c.example": `{"models":[{"name":"models/shared","version":"native-c"}],"nextPageToken":"next"}`,
				"endpoint-a.example": `{"models":[{"name":"models/alpha"},{"name":"models/shared","version":"delegated-a"}]}`,
			},
			wantPath: "/v1beta/models?pageSize=3",
			wantBody: `{"models":[{"name":"models/alpha"},{"name":"models/shared","version":"native-b"},{"name":"models/zeta"}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// endpoint_a is delegated while endpoint_b and endpoint_c are
			// native, so the defaultCandidates order is b, c, a and the shared
			// public ID must keep endpoint_b's entry.
			resolver, err := endpoint.NewStoreResolver(endpointPageReader{items: []storage.EndpointRecord{
				{Endpoint: discoveryEndpoint("endpoint_c", test.protocol, contract.CapabilityModeNative)},
				{Endpoint: discoveryEndpoint("endpoint_a", test.protocol, contract.CapabilityModeDelegated)},
				{Endpoint: discoveryEndpoint("endpoint_b", test.protocol, contract.CapabilityModeNative)},
			}})
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			fetches := map[string]int{}
			handler := NewWithDependencies(Dependencies{
				Resolver: resolver,
				Authorizer: authorizerFunc(func(_ context.Context, candidate contract.Endpoint) (http.Header, error) {
					return http.Header{
						"Authorization": {"Bearer secret-for-" + string(candidate.ID)},
					}, nil
				}),
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					host := request.URL.Host
					mu.Lock()
					fetches[host]++
					mu.Unlock()
					body, known := test.upstreams[host]
					if !known {
						t.Errorf("unexpected upstream host %q", host)
						return nil, errors.New("unknown upstream")
					}
					wantURL := "https://" + host + test.wantPath
					if request.URL.String() != wantURL {
						t.Errorf("upstream URL = %q, want %q", request.URL.String(), wantURL)
					}
					wantAuthorization := "Bearer secret-for-" + discoveryHostEndpointID(host)
					if got := request.Header.Get("Authorization"); got != wantAuthorization {
						t.Errorf("upstream Authorization = %q, want %q", got, wantAuthorization)
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(body)),
					}, nil
				})),
			})

			for repeat := 0; repeat < 2; repeat++ {
				request := httptest.NewRequest(http.MethodGet, test.path, nil)
				request.Header.Set("Authorization", "Bearer local-client-token")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)

				if response.Code != http.StatusOK || response.Body.String() != test.wantBody {
					t.Fatalf("repeat %d response = %d %q, want exact %q", repeat+1, response.Code, response.Body.String(), test.wantBody)
				}
				if response.Header().Get("Content-Type") != "application/json" ||
					response.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("aggregate headers = %#v", response.Header())
				}
			}
			mu.Lock()
			defer mu.Unlock()
			for host := range test.upstreams {
				if fetches[host] != 2 {
					t.Fatalf("fetches = %v, want every capable endpoint fetched once per request", fetches)
				}
			}
		})
	}
}

func TestModelDiscoveryIncludesAliasPublicNames(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		protocol contract.ProtocolID
		routes   []contract.Route
		upstream string
		wantBody string
		forbid   []string
	}{
		{
			name:     "openai alias appears sorted and wins id collision",
			path:     "/v1/models",
			protocol: contract.ProtocolOpenAIModels,
			routes: []contract.Route{
				func() contract.Route {
					route := contract.Route{
						ID: "route_alias", Name: "alias", Enabled: true,
						Match: contract.RouteMatch{
							Protocol: contract.ProtocolOpenAIResponses,
							Model:    "shared-model",
						},
						Targets: []contract.RouteTarget{{
							EndpointID: "endpoint_b", PlanType: contract.PlanTypeNative,
							UpstreamProtocol: contract.ProtocolOpenAIResponses,
							UpstreamModel:    "provider/secret-upstream",
						}},
					}
					return route
				}(),
				func() contract.Route {
					return contract.Route{
						ID: "route_zeta", Name: "zeta", Enabled: true,
						Match: contract.RouteMatch{
							Protocol: contract.ProtocolOpenAIChat,
							Model:    "zeta-alias",
						},
						Targets: []contract.RouteTarget{{
							EndpointID: "endpoint_b", PlanType: contract.PlanTypeNative,
							UpstreamProtocol: contract.ProtocolOpenAIChat,
							UpstreamModel:    "provider/zeta-real",
						}},
					}
				}(),
			},
			upstream: `{"object":"list","data":[{"id":"shared-model","owned_by":"native-b"},{"id":"alpha-model"}]}`,
			wantBody: `{"object":"list","data":[{"id":"alpha-model"},{"id":"shared-model","object":"model","created":0,"owned_by":"system"},{"id":"zeta-alias","object":"model","created":0,"owned_by":"system"}],"first_id":"alpha-model","has_more":false,"last_id":"zeta-alias"}`,
			forbid:   []string{"provider/secret-upstream", "provider/zeta-real", "endpoint_b", "native-b"},
		},
		{
			name:     "gemini alias appears as models/alias",
			path:     "/v1beta/models",
			protocol: contract.ProtocolGoogleModels,
			routes: []contract.Route{
				{
					ID: "route_gemini_alias", Name: "gemini alias", Enabled: true,
					Match: contract.RouteMatch{
						Protocol: contract.ProtocolGoogleGenerateContent,
						Model:    "public-gemini",
					},
					Targets: []contract.RouteTarget{{
						EndpointID: "endpoint_b", PlanType: contract.PlanTypeNative,
						UpstreamProtocol: contract.ProtocolGoogleGenerateContent,
						UpstreamModel:    "gemini-secret",
					}},
				},
			},
			upstream: `{"models":[{"name":"models/alpha"}]}`,
			wantBody: `{"models":[{"name":"models/alpha"},{"name":"models/public-gemini","displayName":"public-gemini"}]}`,
			forbid:   []string{"gemini-secret", "endpoint_b"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := discoveryAliasStore{
				endpoints: []contract.Endpoint{
					discoveryEndpoint("endpoint_b", test.protocol, contract.CapabilityModeNative),
				},
				routes: test.routes,
			}
			// Endpoints used by OpenAI-family alias routes also need chat/responses
			// capabilities only for ValidateForAlpha on routes; discovery fans out
			// on the listing protocol alone.
			if test.protocol == contract.ProtocolOpenAIModels {
				candidate := store.endpoints[0]
				candidate.Capabilities = append(candidate.Capabilities,
					contract.Capability{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true},
					contract.Capability{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true},
				)
				store.endpoints[0] = candidate
			}
			if test.protocol == contract.ProtocolGoogleModels {
				candidate := store.endpoints[0]
				candidate.Capabilities = append(candidate.Capabilities,
					contract.Capability{Protocol: contract.ProtocolGoogleGenerateContent, Mode: contract.CapabilityModeNative, Streaming: true},
				)
				store.endpoints[0] = candidate
			}
			resolver, err := endpoint.NewStoreResolver(store)
			if err != nil {
				t.Fatal(err)
			}
			handler := NewWithDependencies(Dependencies{
				Resolver: resolver,
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(test.upstream)),
					}, nil
				})),
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
			if response.Body.String() != test.wantBody {
				t.Fatalf("body = %s\nwant %s", response.Body.String(), test.wantBody)
			}
			for _, leaked := range test.forbid {
				if strings.Contains(response.Body.String(), leaked) {
					t.Fatalf("response leaked %q: %s", leaked, response.Body.String())
				}
			}
		})
	}
}

type discoveryAliasStore struct {
	endpoints []contract.Endpoint
	routes    []contract.Route
}

func (store discoveryAliasStore) ListEndpoints(
	context.Context,
	storage.EndpointListOptions,
) (storage.EndpointPage, error) {
	items := make([]storage.EndpointRecord, 0, len(store.endpoints))
	for _, candidate := range store.endpoints {
		items = append(items, storage.EndpointRecord{Endpoint: candidate})
	}
	return storage.EndpointPage{Items: items}, nil
}

func (store discoveryAliasStore) ListRoutes(context.Context) ([]storage.RouteRecord, error) {
	items := make([]storage.RouteRecord, 0, len(store.routes))
	for _, route := range store.routes {
		items = append(items, storage.RouteRecord{Route: route})
	}
	return items, nil
}

func TestModelDiscoveryServesPartialAggregateAndRecordsCircuitOutcomes(t *testing.T) {
	protocol := contract.ProtocolOpenAIModels
	resolver := &healthTrackingCandidateResolver{candidateResolver: candidateResolver{candidates: []endpoint.Resolved{
		{Endpoint: discoveryEndpoint("endpoint_ok", protocol, contract.CapabilityModeNative)},
		{Endpoint: discoveryEndpoint("endpoint_dial", protocol, contract.CapabilityModeNative)},
		{Endpoint: discoveryEndpoint("endpoint_status", protocol, contract.CapabilityModeNative)},
		{Endpoint: discoveryEndpoint("endpoint_garbled", protocol, contract.CapabilityModeNative)},
	}}}
	handler := NewWithDependencies(Dependencies{
		Resolver: resolver,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			switch request.URL.Host {
			case "endpoint-ok.example":
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"object":"list","data":[{"id":"model-a"}]}`)),
				}, nil
			case "endpoint-dial.example":
				return nil, errors.New("dial detail must stay private")
			case "endpoint-status.example":
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{}`)),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`not json`)),
				}, nil
			}
		})),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	const wantBody = `{"object":"list","data":[{"id":"model-a"}],"first_id":"model-a","has_more":false,"last_id":"model-a"}`
	if response.Code != http.StatusOK || response.Body.String() != wantBody {
		t.Fatalf("partial aggregate = %d %q", response.Code, response.Body.String())
	}
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	failures := append([]contract.EndpointID(nil), resolver.failures...)
	sort.Slice(failures, func(left, right int) bool { return failures[left] < failures[right] })
	wantFailures := []contract.EndpointID{"endpoint_dial", "endpoint_garbled", "endpoint_status"}
	if len(resolver.successes) != 1 || resolver.successes[0] != "endpoint_ok" {
		t.Fatalf("successes = %v, want [endpoint_ok]", resolver.successes)
	}
	if len(failures) != len(wantFailures) {
		t.Fatalf("failures = %v, want %v", failures, wantFailures)
	}
	for index, want := range wantFailures {
		if failures[index] != want {
			t.Fatalf("failures = %v, want %v", failures, wantFailures)
		}
	}
	if len(resolver.abandons) != 0 {
		t.Fatalf("abandons = %v, want none", resolver.abandons)
	}
}

func TestModelDiscoveryReturnsStructuredErrorWhenEveryCapableEndpointFails(t *testing.T) {
	type upstreamBehavior struct {
		err    error
		status int
		body   string
	}
	tests := []struct {
		name       string
		behaviors  map[string]upstreamBehavior
		wantStatus int
		wantCode   string
	}{
		{
			name: "all connections fail",
			behaviors: map[string]upstreamBehavior{
				"endpoint-a.example": {err: errors.New("dial detail must stay private")},
				"endpoint-b.example": {err: errors.New("dial detail must stay private")},
			},
			wantStatus: http.StatusBadGateway,
			wantCode:   "upstream_unavailable",
		},
		{
			name: "all fetches time out",
			behaviors: map[string]upstreamBehavior{
				"endpoint-a.example": {err: context.DeadlineExceeded},
				"endpoint-b.example": {err: context.DeadlineExceeded},
			},
			wantStatus: http.StatusGatewayTimeout,
			wantCode:   "upstream_timeout",
		},
		{
			name: "mixed timeout and connection failure",
			behaviors: map[string]upstreamBehavior{
				"endpoint-a.example": {err: context.DeadlineExceeded},
				"endpoint-b.example": {err: errors.New("dial detail must stay private")},
			},
			wantStatus: http.StatusBadGateway,
			wantCode:   "upstream_unavailable",
		},
		{
			name: "malformed listings fail the fetch",
			behaviors: map[string]upstreamBehavior{
				"endpoint-a.example": {status: http.StatusOK, body: `not json`},
				"endpoint-b.example": {status: http.StatusOK, body: `{"data":[{"id":""}]}`},
			},
			wantStatus: http.StatusBadGateway,
			wantCode:   "upstream_unavailable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewWithDependencies(Dependencies{
				Resolver: candidateResolver{candidates: []endpoint.Resolved{
					{Endpoint: discoveryEndpoint("endpoint_a", contract.ProtocolOpenAIModels, contract.CapabilityModeNative)},
					{Endpoint: discoveryEndpoint("endpoint_b", contract.ProtocolOpenAIModels, contract.CapabilityModeNative)},
				}},
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					behavior := test.behaviors[request.URL.Host]
					if behavior.err != nil {
						return nil, behavior.err
					}
					return &http.Response{
						StatusCode: behavior.status,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(behavior.body)),
					}, nil
				})),
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

			envelope := assertInferenceError(t, response, test.wantStatus, test.wantCode)
			if !envelope.Error.Retryable {
				t.Fatalf("aggregate discovery failure must stay retryable: %#v", envelope)
			}
			if len(envelope.Error.Details) != 1 ||
				envelope.Error.Details[0].Protocol != string(contract.ProtocolOpenAIModels) {
				t.Fatalf("details = %#v", envelope.Error.Details)
			}
			if strings.Contains(response.Body.String(), "dial detail") {
				t.Fatalf("upstream detail leaked: %s", response.Body.String())
			}
		})
	}
}

func TestModelDiscoveryKeepsResolveErrorContracts(t *testing.T) {
	tests := []struct {
		name       string
		resolver   candidateResolver
		wantStatus int
		wantCode   string
	}{
		{
			name:       "no endpoint declares the capability",
			resolver:   candidateResolver{err: endpoint.ErrNoEndpoint},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "missing_protocol_capability",
		},
		{
			name: "structured capability error",
			resolver: candidateResolver{err: &endpoint.CapabilityUnavailableError{
				Protocol: contract.ProtocolOpenAIModels,
				Modes: []contract.CapabilityMode{
					contract.CapabilityModeNative,
					contract.CapabilityModeDelegated,
				},
			}},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "missing_protocol_capability",
		},
		{
			name:       "empty candidate sequence",
			resolver:   candidateResolver{},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "missing_protocol_capability",
		},
		{
			name:       "every capable endpoint is unhealthy",
			resolver:   candidateResolver{err: endpoint.ErrNoHealthyEndpoint},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "upstream_unavailable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fetched := false
			handler := NewWithDependencies(Dependencies{
				Resolver: test.resolver,
				Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
					fetched = true
					return nil
				}),
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

			assertInferenceError(t, response, test.wantStatus, test.wantCode)
			if fetched {
				t.Fatal("no upstream fetch may happen when resolution fails")
			}
		})
	}
}

func TestModelDiscoveryEnforcesUpstreamResponseByteBound(t *testing.T) {
	oversized := bytes.Repeat([]byte("x"), maxMetadataBytes+1)
	protocol := contract.ProtocolOpenAIModels
	tests := []struct {
		name         string
		candidates   []contract.EndpointID
		wantStatus   int
		wantBody     string
		wantCode     string
		wantFailures []contract.EndpointID
	}{
		{
			name:         "oversized endpoint fails cleanly while the aggregate survives",
			candidates:   []contract.EndpointID{"endpoint_big", "endpoint_small"},
			wantStatus:   http.StatusOK,
			wantBody:     `{"object":"list","data":[{"id":"tiny-model"}],"first_id":"tiny-model","has_more":false,"last_id":"tiny-model"}`,
			wantFailures: []contract.EndpointID{"endpoint_big"},
		},
		{
			name:         "only an oversized endpoint fails the aggregate",
			candidates:   []contract.EndpointID{"endpoint_big"},
			wantStatus:   http.StatusBadGateway,
			wantCode:     "upstream_unavailable",
			wantFailures: []contract.EndpointID{"endpoint_big"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidates := make([]endpoint.Resolved, 0, len(test.candidates))
			for _, id := range test.candidates {
				candidates = append(candidates, endpoint.Resolved{
					Endpoint: discoveryEndpoint(id, protocol, contract.CapabilityModeNative),
				})
			}
			resolver := &healthTrackingCandidateResolver{candidateResolver: candidateResolver{candidates: candidates}}
			handler := NewWithDependencies(Dependencies{
				Resolver: resolver,
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					if request.URL.Host == "endpoint-big.example" {
						return &http.Response{
							StatusCode: http.StatusOK,
							Header:     http.Header{"Content-Type": {"application/json"}},
							Body:       io.NopCloser(bytes.NewReader(oversized)),
						}, nil
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"tiny-model"}]}`)),
					}, nil
				})),
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

			if test.wantCode != "" {
				assertInferenceError(t, response, test.wantStatus, test.wantCode)
			} else if response.Code != test.wantStatus || response.Body.String() != test.wantBody {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
			resolver.mu.Lock()
			defer resolver.mu.Unlock()
			if len(resolver.failures) != len(test.wantFailures) || resolver.failures[0] != test.wantFailures[0] {
				t.Fatalf("failures = %v, want %v", resolver.failures, test.wantFailures)
			}
		})
	}
}

type admissionRefusingResolver struct {
	*healthTrackingCandidateResolver
	refuse map[contract.EndpointID]bool
}

func (resolver admissionRefusingResolver) BeginAttempt(candidate endpoint.Resolved) bool {
	return !resolver.refuse[candidate.Endpoint.ID]
}

func TestModelDiscoverySkipsCandidatesRefusedByCircuitAdmission(t *testing.T) {
	protocol := contract.ProtocolOpenAIModels
	tests := []struct {
		name       string
		refuse     map[contract.EndpointID]bool
		wantStatus int
		wantBody   string
		wantCode   string
		wantHosts  []string
	}{
		{
			name:       "refused candidate is skipped without upstream io",
			refuse:     map[contract.EndpointID]bool{"endpoint_a": true},
			wantStatus: http.StatusOK,
			wantBody:   `{"object":"list","data":[{"id":"endpoint_b-model"}],"first_id":"endpoint_b-model","has_more":false,"last_id":"endpoint_b-model"}`,
			wantHosts:  []string{"endpoint-b.example"},
		},
		{
			name:       "all candidates refused",
			refuse:     map[contract.EndpointID]bool{"endpoint_a": true, "endpoint_b": true},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "upstream_unavailable",
			wantHosts:  nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := admissionRefusingResolver{
				healthTrackingCandidateResolver: &healthTrackingCandidateResolver{candidateResolver: candidateResolver{candidates: []endpoint.Resolved{
					{Endpoint: discoveryEndpoint("endpoint_a", protocol, contract.CapabilityModeNative)},
					{Endpoint: discoveryEndpoint("endpoint_b", protocol, contract.CapabilityModeNative)},
				}}},
				refuse: test.refuse,
			}
			var mu sync.Mutex
			var hosts []string
			handler := NewWithDependencies(Dependencies{
				Resolver: resolver,
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					mu.Lock()
					hosts = append(hosts, request.URL.Host)
					mu.Unlock()
					model := discoveryHostEndpointID(request.URL.Host) + "-model"
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"` + model + `"}]}`)),
					}, nil
				})),
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

			if test.wantCode != "" {
				assertInferenceError(t, response, test.wantStatus, test.wantCode)
			} else if response.Code != test.wantStatus || response.Body.String() != test.wantBody {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
			mu.Lock()
			defer mu.Unlock()
			if strings.Join(hosts, ",") != strings.Join(test.wantHosts, ",") {
				t.Fatalf("fetched hosts = %v, want %v", hosts, test.wantHosts)
			}
		})
	}
}

func TestModelDiscoveryBoundsConcurrentFanOut(t *testing.T) {
	const capableEndpoints = maxConcurrentDiscoveryFetches + 2
	candidates := make([]endpoint.Resolved, 0, capableEndpoints)
	for index := range capableEndpoints {
		id := contract.EndpointID("endpoint_" + string(rune('a'+index)))
		candidates = append(candidates, endpoint.Resolved{
			Endpoint: discoveryEndpoint(id, contract.ProtocolOpenAIModels, contract.CapabilityModeNative),
		})
	}
	entered := make(chan struct{}, capableEndpoints)
	release := make(chan struct{})
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: candidates},
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, _ *http.Request, _ transport.Target) error {
			entered <- struct{}{}
			<-release
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, err := writer.Write([]byte(`{"data":[]}`))
			return err
		}),
	})
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	}()

	for index := range maxConcurrentDiscoveryFetches {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatalf("fetch %d did not start", index+1)
		}
	}
	select {
	case <-entered:
		t.Fatal("fan-out exceeded the fixed concurrency bound")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("aggregation did not finish")
	}
	if response.Code != http.StatusOK ||
		response.Body.String() != `{"object":"list","data":[],"first_id":null,"has_more":false,"last_id":null}` {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}

func TestModelDiscoveryEmptyAggregateKeepsProtocolEnvelopes(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		protocol     contract.ProtocolID
		baseURL      string
		upstreamBody string
		wantURL      string
		wantBody     string
	}{
		{
			name:         "openai list envelope",
			path:         "/v1/models",
			protocol:     contract.ProtocolOpenAIModels,
			baseURL:      "https://upstream.example/sdk/v1",
			upstreamBody: `{"object":"list","data":[]}`,
			wantURL:      "https://upstream.example/sdk/v1/models",
			wantBody:     `{"object":"list","data":[],"first_id":null,"has_more":false,"last_id":null}`,
		},
		{
			name:         "gemini models envelope",
			path:         "/v1beta/models?pageSize=20",
			protocol:     contract.ProtocolGoogleModels,
			baseURL:      "https://upstream.example/sdk/v1beta",
			upstreamBody: `{"models":[]}`,
			wantURL:      "https://upstream.example/sdk/v1beta/models?pageSize=20",
			wantBody:     `{"models":[]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstreamEndpoint := fixtureEndpoint(test.protocol, false, test.baseURL)
			handler := NewWithDependencies(Dependencies{
				Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
					if request.Protocol != test.protocol || request.Model != "" || request.Streaming {
						t.Errorf("resolve request = %#v", request)
					}
					return endpoint.Resolved{Endpoint: upstreamEndpoint}, nil
				}),
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					if request.URL.String() != test.wantURL {
						t.Errorf("upstream URL = %q, want %q", request.URL.String(), test.wantURL)
					}
					if request.Header.Get("X-Client-Marker") != "preserved" {
						t.Errorf("end-to-end request header was not preserved")
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(test.upstreamBody)),
					}, nil
				})),
			})
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("X-Client-Marker", "preserved")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK || response.Body.String() != test.wantBody {
				t.Fatalf("response = %d %q, want exact %q", response.Code, response.Body.String(), test.wantBody)
			}
		})
	}
}
