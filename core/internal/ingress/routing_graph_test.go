package ingress

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/routinggraph"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

type graphTestResolver struct {
	candidateResolver
	plan *endpoint.RoutingGraphPlan
}

func (resolver graphTestResolver) ResolveRoutingGraph(_ context.Context, request endpoint.ResolveRequest) (*endpoint.RoutingGraphPlan, error) {
	if request.Model != resolver.plan.Entry.Model {
		return nil, nil
	}
	copy := *resolver.plan
	copy.Candidates = append([]endpoint.Resolved(nil), resolver.plan.Candidates...)
	return &copy, nil
}
func (resolver graphTestResolver) ListRoutingGraphModels(context.Context) ([]string, error) {
	return []string{resolver.plan.Entry.Model}, nil
}

func graphTestPlan() *endpoint.RoutingGraphPlan {
	policy := contract.DefaultFailurePolicy()
	policy.MaxRetries = 0
	policy.InitialDelayMS = 0
	candidates := recoveryCandidates(3, policy, contract.FailoverPolicy{Enabled: true, Strategy: contract.RetryFirst, MaxAttempts: 6})
	graph := contract.RoutingGraph{Nodes: []contract.RoutingGraphNode{{ID: "entry", Kind: "entry", Model: "public", Enabled: true}}, Edges: []contract.RoutingGraphEdge{}}
	previous, port := "entry", "next"
	for i, id := range []string{"node_a", "node_b", "node_c"} {
		candidates[i].GraphNodeID = id
		candidates[i].UpstreamModel = "actual_" + strings.TrimPrefix(id, "node_")
		graph.Nodes = append(graph.Nodes, contract.RoutingGraphNode{ID: id, Kind: "call", Enabled: id != "node_b", ServiceID: candidates[i].CanonicalService().ID, UpstreamModel: "actual_" + strings.TrimPrefix(id, "node_")})
		graph.Edges = append(graph.Edges, contract.RoutingGraphEdge{ID: "edge_" + id, Source: previous, Port: port, Target: id})
		previous, port = id, "failure"
	}
	return &endpoint.RoutingGraphPlan{Graph: graph, Entry: graph.Nodes[0], Revision: 7, Candidates: candidates, MaxAttempts: 6, Facts: routinggraph.Facts{Protocol: string(contract.ProtocolOpenAIChat)}}
}

func TestRoutingGraphDisabledNodeBypassAndPreview(t *testing.T) {
	for _, tt := range []struct {
		name          string
		status, limit int
		want          []string
		response      int
	}{{"429 skips disabled middle", 429, 6, []string{"actual_a", "actual_c"}, 200}, {"400 stops", 400, 6, []string{"actual_a"}, 400}, {"budget counts network only", 429, 1, []string{"actual_a"}, 429}} {
		t.Run(tt.name, func(t *testing.T) {
			plan := graphTestPlan()
			plan.MaxAttempts = tt.limit
			var models []string
			records := newRedirectSettingsStore(enabledRedirect("public", "unrelated"))
			handler := NewWithDependencies(Dependencies{Resolver: graphTestResolver{plan: plan}, RequestRecords: records, Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				assertNoGatewayIdentity(t, request, "fixture-client")
				body, _ := io.ReadAll(request.Body)
				var value map[string]any
				if err := json.Unmarshal(body, &value); err != nil {
					t.Fatal(err)
				}
				model, _ := value["model"].(string)
				models = append(models, model)
				if model == "actual_a" {
					return jsonResponse(tt.status, `{"error":{"message":"failed"}}`), nil
				}
				return jsonResponse(200, `{"id":"answer","model":"actual_c","choices":[]}`), nil
			}))})
			request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"public","messages":[{"role":"user","content":"hello"}]}`))
			request.Header.Set("User-Agent", "fixture-client")
			request.Header.Set("X-AstrLink-Test", "local-only")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.response || !reflect.DeepEqual(models, tt.want) {
				t.Fatalf("status=%d models=%v body=%s", response.Code, models, response.Body.String())
			}
			if tt.response == 200 && !strings.Contains(response.Body.String(), `"model":"public"`) {
				t.Fatalf("public model not restored: %s", response.Body.String())
			}
			root := records.root(t)
			if root.Recovery == nil || root.Recovery.GraphRevision != 7 {
				t.Fatalf("missing graph recovery: %#v", root.Recovery)
			}
			if tt.response == 200 {
				found := false
				for _, step := range root.Recovery.GraphTrace {
					if step.NodeID == "node_b" && step.Reason == "node_disabled" {
						found = true
					}
				}
				if !found {
					t.Fatal("disabled middle was not recorded")
				}
			}
			preview, err := PreviewRoutingGraph(context.Background(), plan, routinggraph.PreviewInput{Graph: plan.Graph, EntryID: "entry", Outcomes: map[string]string{"node_a": stringStatus(tt.status)}})
			if err != nil {
				t.Fatal(err)
			}
			var attempted []string
			for _, step := range preview.Steps {
				if step.Status == "attempted" {
					attempted = append(attempted, step.Model)
				}
			}
			if !reflect.DeepEqual(attempted, models) {
				t.Fatalf("preview=%v actual=%v", attempted, models)
			}
		})
	}
}

func stringStatus(status int) string {
	if status == 429 {
		return "429"
	}
	return "400"
}

func TestRoutingGraphRequestFactsDoNotCountWhitespaceOrAudioAsFeatures(t *testing.T) {
	for _, input := range []struct {
		body          string
		tools, images bool
	}{
		{`{"tools": [ ], "messages":[]}`, false, false},
		{`{"tools":[{"type":"function"}],"messages":[]}`, true, false},
		{`{"contents":[{"parts":[{"inlineData":{"mimeType":"audio/wav","data":"fixture"}}]}]}`, false, false},
		{`{"contents":[{"parts":[{"inlineData":{"mimeType":"image/png","data":"fixture"}}]}]}`, false, true},
	} {
		plan := graphTestPlan()
		schedule := newRecoverySchedule(plan.Candidates, true)
		schedule.attachGraph(plan, []byte(input.body), nil)
		facts := schedule.graph.Facts
		if facts.HasTools == nil || *facts.HasTools != input.tools || facts.HasImages == nil || *facts.HasImages != input.images {
			t.Fatalf("incorrect feature facts for %s: %+v", input.body, facts)
		}
	}
}

func TestRoutingGraphDisabledPrefixAndAllDisabled(t *testing.T) {
	for _, all := range []bool{false, true} {
		plan := graphTestPlan()
		plan.Graph.Nodes[1].Enabled = false
		plan.Graph.Nodes[3].Enabled = !all
		calls := 0
		handler := NewWithDependencies(Dependencies{Resolver: graphTestResolver{plan: plan}, Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return jsonResponse(200, `{"model":"actual_c"}`), nil
		}))})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"public","messages":[]}`)))
		if all && calls != 0 || !all && calls != 1 {
			t.Fatalf("all=%v calls=%d", all, calls)
		}
		if all && response.Code != 503 {
			t.Fatalf("all disabled status=%d", response.Code)
		}
	}
}

func TestRoutingGraphConditionsObserveFailureAndUnknown(t *testing.T) {
	plan := graphTestPlan()
	plan.Graph.Nodes = append(plan.Graph.Nodes, contract.RoutingGraphNode{ID: "condition", Kind: "condition", Enabled: true, Rules: []contract.RoutingGraphRule{{ID: "limited", Predicate: contract.RoutingPredicate{Field: "last.status", Operator: "eq", Value: json.RawMessage(`429`)}}}})
	plan.Graph.Edges[1].Target = "condition"
	plan.Graph.Edges = append(plan.Graph.Edges, contract.RoutingGraphEdge{ID: "limited_edge", Source: "condition", Port: "limited", Target: "node_c"}, contract.RoutingGraphEdge{ID: "otherwise_edge", Source: "condition", Port: "otherwise", Target: "node_b"})
	preview, err := PreviewRoutingGraph(context.Background(), plan, routinggraph.PreviewInput{Graph: plan.Graph, EntryID: "entry", Outcomes: map[string]string{"node_a": "429"}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, step := range preview.Steps {
		if step.NodeID == "condition" && step.Port == "limited" {
			found = true
		}
	}
	if !found || preview.Attempts != 2 {
		t.Fatalf("preview=%+v", preview)
	}
	plan.Graph.Nodes[len(plan.Graph.Nodes)-1].Rules[0].Predicate = contract.RoutingPredicate{Field: "quota.exhausted", Operator: "eq", Value: json.RawMessage(`true`), ServiceID: "service_unknown", Window: "primary"}
	preview, err = PreviewRoutingGraph(context.Background(), plan, routinggraph.PreviewInput{Graph: plan.Graph, EntryID: "entry", Outcomes: map[string]string{"node_a": "429"}})
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, step := range preview.Steps {
		if step.NodeID == "condition" && step.Port == "otherwise" && strings.HasPrefix(step.Reason, "unknown:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("unknown was not explained: %+v", preview)
	}
}

func TestRoutingGraphContinuationSkipsUnboundPrefixWithoutInjectingTarget(t *testing.T) {
	plan := graphTestPlan()
	plan.Graph.Nodes[2].Enabled = true
	for i := range plan.Candidates {
		plan.Candidates[i].Endpoint.Capabilities = []contract.Capability{{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative}}
		plan.Candidates[i].UpstreamProtocol = contract.ProtocolOpenAIResponses
	}
	var models []string
	handler := NewWithDependencies(Dependencies{Resolver: graphTestResolver{plan: plan}, Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var value map[string]any
		_ = json.NewDecoder(request.Body).Decode(&value)
		model, _ := value["model"].(string)
		models = append(models, model)
		if model == "actual_a" {
			return jsonResponse(503, `{"error":{"message":"unavailable"}}`), nil
		}
		return jsonResponse(200, `{"id":"resp_`+model+`","model":"`+model+`","output":[]}`), nil
	}))})
	send := func(body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(response, request)
		return response
	}
	if response := send(`{"model":"public","input":"hello"}`); response.Code != 200 || strings.Join(models, ",") != "actual_a,actual_b" {
		t.Fatalf("first turn %d %v %s", response.Code, models, response.Body.String())
	}
	models = nil
	if response := send(`{"model":"public","input":"next","previous_response_id":"resp_actual_b"}`); response.Code != 200 || strings.Join(models, ",") != "actual_b" {
		t.Fatalf("continuation must skip the unbound prefix: %d %v %s", response.Code, models, response.Body.String())
	}
	// A condition that selects another branch never reaches the bound target;
	// the request fails instead of sending state elsewhere or injecting B.
	plan.Graph.Nodes = append(plan.Graph.Nodes, contract.RoutingGraphNode{ID: "condition", Kind: "condition", Enabled: true, Rules: []contract.RoutingGraphRule{{ID: "plain", Predicate: contract.RoutingPredicate{Field: "request.streaming", Operator: "eq", Value: json.RawMessage(`false`)}}}})
	plan.Graph.Edges[0].Target = "condition"
	plan.Graph.Edges = append(plan.Graph.Edges, contract.RoutingGraphEdge{ID: "plain_edge", Source: "condition", Port: "plain", Target: "node_c"}, contract.RoutingGraphEdge{ID: "otherwise_edge", Source: "condition", Port: "otherwise", Target: "node_a"})
	if err := plan.Graph.Validate(); err != nil {
		t.Fatal(err)
	}
	models = nil
	if response := send(`{"model":"public","input":"next","previous_response_id":"resp_actual_b"}`); response.Code != http.StatusConflict || len(models) != 0 || !strings.Contains(response.Body.String(), "protocol_binding") {
		t.Fatalf("branch without bound target: %d %v %s", response.Code, models, response.Body.String())
	}
}

func TestRoutingGraphDiscoveryListsEntriesWithoutUpstreamListing(t *testing.T) {
	handler := NewWithDependencies(Dependencies{Resolver: graphTestResolver{plan: graphTestPlan()}, Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("discovery without candidates reached an upstream")
		return nil, nil
	}))})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/v1/models", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"id":"public"`) {
		t.Fatalf("graph entry missing from discovery: %d %s", response.Code, response.Body.String())
	}
}
