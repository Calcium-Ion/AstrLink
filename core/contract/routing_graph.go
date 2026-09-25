package contract

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// RoutingGraph is the executable document. Coordinates never influence routing.
type RoutingGraph struct {
	Nodes []RoutingGraphNode `json:"nodes"`
	Edges []RoutingGraphEdge `json:"edges"`
}

func (graph *RoutingGraph) UnmarshalJSON(data []byte) error {
	type document RoutingGraph
	var value document
	if err := decodeStrictContractJSON(data, &value); err != nil {
		return err
	}
	if value.Nodes == nil || value.Edges == nil {
		return fmt.Errorf("graph nodes and edges must be arrays")
	}
	*graph = RoutingGraph(value)
	return graph.ValidateDraft(nil)
}

type RoutingGraphNode struct {
	ID            string             `json:"id"`
	Kind          string             `json:"kind"`
	Name          string             `json:"name,omitempty"`
	Enabled       bool               `json:"enabled"`
	Model         string             `json:"model,omitempty"`
	ServiceID     ServiceID          `json:"service_id,omitempty"`
	UpstreamModel string             `json:"upstream_model,omitempty"`
	MaxAttempts   int                `json:"max_attempts,omitempty"`
	FailurePolicy *FailurePolicy     `json:"failure_policy,omitempty"`
	Rules         []RoutingGraphRule `json:"rules,omitempty"`
	UnknownPort   string             `json:"unknown_port,omitempty"`
}

type RoutingGraphRule struct {
	ID        string           `json:"id"`
	Label     string           `json:"label,omitempty"`
	Predicate RoutingPredicate `json:"predicate"`
}

type RoutingPredicate struct {
	All       []RoutingPredicate `json:"all,omitempty"`
	Any       []RoutingPredicate `json:"any,omitempty"`
	Field     string             `json:"field,omitempty"`
	Operator  string             `json:"operator,omitempty"`
	Value     json.RawMessage    `json:"value,omitempty"`
	ServiceID ServiceID          `json:"service_id,omitempty"`
	Window    string             `json:"window,omitempty"`
}

type RoutingGraphEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Port   string `json:"port"`
	Target string `json:"target"`
}

type RoutingPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type RoutingLayout map[string]RoutingPosition

type RoutingGraphRevision struct {
	Revision  int64  `json:"revision"`
	CreatedAt string `json:"created_at"`
}

type RoutingGraphDocument struct {
	Draft    RoutingGraph           `json:"draft"`
	Layout   RoutingLayout          `json:"layout"`
	Active   RoutingGraph           `json:"active"`
	Revision int64                  `json:"revision"`
	ETag     string                 `json:"etag"`
	History  []RoutingGraphRevision `json:"history"`
}

type RoutingGraphStep struct {
	NodeID    string    `json:"node_id"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	Reason    string    `json:"reason,omitempty"`
	Port      string    `json:"port,omitempty"`
	ServiceID ServiceID `json:"service_id,omitempty"`
	Model     string    `json:"model,omitempty"`
}

func EmptyRoutingGraph() RoutingGraph {
	return RoutingGraph{Nodes: []RoutingGraphNode{}, Edges: []RoutingGraphEdge{}}
}

func (graph RoutingGraph) Entry(model string) (RoutingGraphNode, bool) {
	for _, node := range graph.Nodes {
		if node.Kind == "entry" && node.Model == model {
			return node, true
		}
	}
	return RoutingGraphNode{}, false
}

func (graph RoutingGraph) Reachable(entry string) map[string]bool {
	seen := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		for _, edge := range graph.Edges {
			if edge.Source == id {
				visit(edge.Target)
			}
		}
	}
	visit(entry)
	return seen
}

func (graph RoutingGraph) ValidateDraft(layout RoutingLayout) error {
	if len(graph.Nodes) > 512 || len(graph.Edges) > 1024 {
		return fmt.Errorf("graph exceeds 512 nodes or 1024 edges")
	}
	ids := map[string]bool{}
	for _, node := range graph.Nodes {
		if err := validateResourceID("node", node.ID); err != nil {
			return err
		}
		if ids[node.ID] {
			return fmt.Errorf("duplicate node %s", node.ID)
		}
		ids[node.ID] = true
		switch node.Kind {
		case "entry", "call", "condition", "stop":
		default:
			return fmt.Errorf("unknown node kind %q", node.Kind)
		}
		if len(node.Name) > 512 || len(node.Model) > 1024 || len(node.UpstreamModel) > 1024 || len(node.Rules) > 32 || len(node.ServiceID) > 96 || len(node.UnknownPort) > 96 {
			return fmt.Errorf("node %s exceeds field limits", node.ID)
		}
		if node.FailurePolicy != nil {
			if err := node.FailurePolicy.Validate(); err != nil {
				return fmt.Errorf("node %s: %w", node.ID, err)
			}
		}
		for _, rule := range node.Rules {
			if len(rule.ID) > 96 || len(rule.Label) > 256 {
				return fmt.Errorf("condition rule exceeds field limits")
			}
			if err := rule.Predicate.validate(0); err != nil {
				return fmt.Errorf("node %s: %w", node.ID, err)
			}
		}
	}
	edgeIDs := map[string]bool{}
	for _, edge := range graph.Edges {
		if err := validateResourceID("edge", edge.ID); err != nil {
			return err
		}
		if edgeIDs[edge.ID] {
			return fmt.Errorf("duplicate edge %s", edge.ID)
		}
		edgeIDs[edge.ID] = true
		if len(edge.Source) > 128 || len(edge.Target) > 128 || len(edge.Port) > 128 {
			return fmt.Errorf("edge fields too long")
		}
	}
	if len(layout) > 512 {
		return fmt.Errorf("layout exceeds node limit")
	}
	for _, p := range layout {
		if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) || math.Abs(p.X) > 1e6 || math.Abs(p.Y) > 1e6 {
			return fmt.Errorf("invalid node position")
		}
	}
	return nil
}

// Validate checks every enabled entry and its reachable nodes. Unconnected work
// can remain in the draft without changing executable paths.
func (graph RoutingGraph) Validate() error {
	if err := graph.ValidateDraft(nil); err != nil {
		return err
	}
	nodes := map[string]RoutingGraphNode{}
	models := map[string]bool{}
	reachable := map[string]bool{}
	for _, node := range graph.Nodes {
		nodes[node.ID] = node
		if node.Kind != "entry" {
			continue
		}
		if strings.TrimSpace(node.Model) == "" || strings.TrimSpace(node.Model) != node.Model || len(node.Model) > 256 {
			return fmt.Errorf("entry %s requires an exact model name", node.ID)
		}
		if models[node.Model] {
			return fmt.Errorf("duplicate entry model %s", node.Model)
		}
		models[node.Model] = true
		if node.MaxAttempts < 0 || node.MaxAttempts > 20 {
			return fmt.Errorf("entry %s max_attempts must be 1–20 or inherited", node.ID)
		}
		if node.Enabled {
			for id := range graph.Reachable(node.ID) {
				reachable[id] = true
			}
		}
	}
	ports := map[string]map[string]string{}
	for _, edge := range graph.Edges {
		if !reachable[edge.Source] {
			continue
		}
		source, ok := nodes[edge.Source]
		if !ok {
			return fmt.Errorf("missing source %s", edge.Source)
		}
		target, ok := nodes[edge.Target]
		if !ok {
			return fmt.Errorf("missing target %s", edge.Target)
		}
		if target.Kind == "entry" {
			return fmt.Errorf("edges cannot enter model entries")
		}
		valid := source.Kind == "entry" && edge.Port == "next" || source.Kind == "call" && edge.Port == "failure"
		if source.Kind == "condition" {
			valid = edge.Port == "otherwise" || edge.Port == "unknown"
			for _, rule := range source.Rules {
				valid = valid || edge.Port == rule.ID
			}
		}
		if !valid {
			return fmt.Errorf("invalid port %s on node %s", edge.Port, source.ID)
		}
		if ports[source.ID] == nil {
			ports[source.ID] = map[string]string{}
		}
		if ports[source.ID][edge.Port] != "" {
			return fmt.Errorf("port %s on %s has multiple destinations", edge.Port, source.ID)
		}
		ports[source.ID][edge.Port] = edge.Target
	}
	for id := range reachable {
		node, ok := nodes[id]
		if !ok {
			return fmt.Errorf("missing node %s", id)
		}
		switch node.Kind {
		case "entry":
			if ports[id]["next"] == "" {
				return fmt.Errorf("entry %s has no destination", node.Model)
			}
		case "call":
			if !node.Enabled {
				continue
			}
			if err := node.ServiceID.Validate(); err != nil {
				return fmt.Errorf("call %s requires a service", id)
			}
			if strings.TrimSpace(node.UpstreamModel) == "" || strings.TrimSpace(node.UpstreamModel) != node.UpstreamModel || len(node.UpstreamModel) > 256 {
				return fmt.Errorf("call %s requires an upstream model", id)
			}
		case "condition":
			if ports[id]["otherwise"] == "" {
				return fmt.Errorf("condition %s needs an otherwise destination", id)
			}
			unknown := node.UnknownPort
			if unknown == "" {
				unknown = "otherwise"
			}
			if ports[id][unknown] == "" {
				return fmt.Errorf("condition %s needs an unknown destination", id)
			}
			rules := map[string]bool{}
			for _, rule := range node.Rules {
				if err := validateResourceID("rule", rule.ID); err != nil {
					return err
				}
				if rule.ID == "otherwise" || rule.ID == "unknown" || rules[rule.ID] {
					return fmt.Errorf("invalid or duplicate condition rule")
				}
				rules[rule.ID] = true
				if ports[id][rule.ID] == "" {
					return fmt.Errorf("rule %s has no destination", rule.ID)
				}
			}
		}
	}
	colors := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		if colors[id] == 1 {
			return fmt.Errorf("routing graph contains a cycle at %s", id)
		}
		if colors[id] == 2 {
			return nil
		}
		colors[id] = 1
		for _, to := range ports[id] {
			if err := visit(to); err != nil {
				return err
			}
		}
		colors[id] = 2
		return nil
	}
	for id := range reachable {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func (p RoutingPredicate) validate(depth int) error {
	if depth > 4 || len(p.All)+len(p.Any) > 16 {
		return fmt.Errorf("condition nesting exceeds limit")
	}
	groups := 0
	if p.All != nil {
		groups++
	}
	if p.Any != nil {
		groups++
	}
	if p.Field != "" {
		groups++
	}
	if groups != 1 {
		return fmt.Errorf("condition must contain exactly one field, all or any")
	}
	if p.All != nil || p.Any != nil {
		children := p.All
		if p.Any != nil {
			children = p.Any
		}
		if len(children) == 0 {
			return fmt.Errorf("empty condition group")
		}
		for _, child := range children {
			if err := child.validate(depth + 1); err != nil {
				return err
			}
		}
		return nil
	}
	switch p.Field {
	case "request.model", "request.protocol", "request.streaming", "request.has_tools", "request.has_images", "last.status", "last.error", "attempts", "quota.exhausted", "quota.used_percent":
	default:
		return fmt.Errorf("unknown condition field %s", p.Field)
	}
	switch p.Operator {
	case "eq", "ne", "gt", "gte", "lt", "lte":
	default:
		return fmt.Errorf("unknown condition operator")
	}
	var value any
	if err := json.Unmarshal(p.Value, &value); err != nil {
		return fmt.Errorf("invalid condition value")
	}
	switch p.Field {
	case "request.model", "request.protocol", "last.error":
		if _, ok := value.(string); !ok || len(p.Value) > 256 || p.Operator != "eq" && p.Operator != "ne" {
			return fmt.Errorf("string condition requires eq/ne and a string")
		}
	case "request.streaming", "request.has_tools", "request.has_images", "quota.exhausted":
		if _, ok := value.(bool); !ok || p.Operator != "eq" && p.Operator != "ne" {
			return fmt.Errorf("boolean condition requires eq/ne and a boolean")
		}
	default:
		if n, ok := value.(float64); !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return fmt.Errorf("numeric condition requires a number")
		}
	}
	if strings.HasPrefix(p.Field, "quota.") {
		if err := p.ServiceID.Validate(); err != nil {
			return fmt.Errorf("quota condition requires a service")
		}
		if p.Window != "primary" && p.Window != "secondary" {
			return fmt.Errorf("quota condition requires primary or secondary window")
		}
	}
	return nil
}
