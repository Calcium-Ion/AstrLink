// Package routinggraph evaluates the same bounded graph for inference and preview.
// It never performs network I/O, obtains credentials or changes health state.
package routinggraph

import (
	"encoding/json"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

type Facts struct {
	Model      string                                            `json:"-"`
	Protocol   string                                            `json:"protocol"`
	Streaming  bool                                              `json:"streaming"`
	HasTools   *bool                                             `json:"has_tools,omitempty"`
	HasImages  *bool                                             `json:"has_images,omitempty"`
	LastStatus *int                                              `json:"-"`
	LastError  *string                                           `json:"-"`
	Attempts   int                                               `json:"-"`
	Quota      map[contract.ServiceID]contract.SubscriptionUsage `json:"quota,omitempty"`
	Now        time.Time                                         `json:"-"`
}

type Run struct {
	Facts      Facts
	Steps      []contract.RoutingGraphStep
	StopReason string
	nodes      map[string]contract.RoutingGraphNode
	edges      map[string]map[string]string
	cursor     string
	visited    int
	Observe    func(contract.RoutingGraphStep)
}

func New(graph contract.RoutingGraph, entry string, facts Facts) *Run {
	run := &Run{Facts: facts, Steps: []contract.RoutingGraphStep{}, nodes: map[string]contract.RoutingGraphNode{}, edges: map[string]map[string]string{}, cursor: entry}
	if run.Facts.Now.IsZero() {
		run.Facts.Now = time.Now()
	}
	for _, node := range graph.Nodes {
		run.nodes[node.ID] = node
	}
	for _, edge := range graph.Edges {
		if run.edges[edge.Source] == nil {
			run.edges[edge.Source] = map[string]string{}
		}
		run.edges[edge.Source][edge.Port] = edge.Target
	}
	return run
}

func (run *Run) Note(nodeID, status, reason, port string) {
	if len(run.Steps) >= 2048 {
		return
	}
	node := run.nodes[nodeID]
	step := contract.RoutingGraphStep{NodeID: nodeID, Kind: node.Kind, Status: status, Reason: reason, Port: port, ServiceID: node.ServiceID, Model: node.UpstreamModel}
	run.Steps = append(run.Steps, step)
	if run.Observe != nil {
		run.Observe(step)
	}
}

func (run *Run) Outcome(nodeID string, status int, reason string) {
	run.Facts.LastStatus = nil
	if status > 0 {
		run.Facts.LastStatus = &status
	}
	run.Facts.LastError = &reason
	run.Note(nodeID, "failed", reason, "")
}

// Next follows one branch. Disabled calls keep their failure edge, emit a skip,
// and never become an executable attempt or consume a network budget.
func (run *Run) Next() (contract.RoutingGraphNode, bool) {
	for run.cursor != "" && run.visited < 1024 {
		id := run.cursor
		node, ok := run.nodes[id]
		if !ok {
			run.StopReason = "missing_node"
			break
		}
		run.visited++
		switch node.Kind {
		case "entry":
			if !node.Enabled {
				run.Note(id, "stopped", "entry_paused", "")
				run.StopReason = "entry_paused"
				run.cursor = ""
				break
			}
			run.Note(id, "selected", "entry_match", "next")
			run.cursor = run.edges[id]["next"]
		case "condition":
			port, reason := "otherwise", "no_rule_matched"
			for _, rule := range node.Rules {
				value, known := Evaluate(rule.Predicate, run.Facts)
				if !known {
					port = node.UnknownPort
					if port == "" {
						port = "otherwise"
					}
					reason = "unknown:" + rule.ID
					break
				}
				if value {
					port = rule.ID
					reason = "matched:" + rule.ID
					break
				}
			}
			run.Note(id, "selected", reason, port)
			run.cursor = run.edges[id][port]
		case "call":
			run.cursor = run.edges[id]["failure"]
			if !node.Enabled {
				run.Note(id, "skipped", "node_disabled", "failure")
				continue
			}
			return node, true
		case "stop":
			run.Note(id, "stopped", "explicit_stop", "")
			run.StopReason = "explicit_stop"
			run.cursor = ""
		default:
			run.StopReason = "invalid_node"
			run.cursor = ""
		}
	}
	if run.visited >= 1024 {
		run.StopReason = "step_limit"
	}
	if run.StopReason == "" {
		run.StopReason = "targets_exhausted"
	}
	return contract.RoutingGraphNode{}, false
}

func (run *Run) Stop(reason string) { run.StopReason = reason; run.cursor = "" }

func Evaluate(p contract.RoutingPredicate, facts Facts) (bool, bool) {
	if p.All != nil || p.Any != nil {
		children, all := p.All, true
		if p.Any != nil {
			children, all = p.Any, false
		}
		known := true
		for _, child := range children {
			value, ok := Evaluate(child, facts)
			if ok && value != all {
				return value, true
			}
			known = known && ok
		}
		return all, known
	}
	actual, known := fact(p, facts)
	if !known {
		return false, false
	}
	var expected any
	if json.Unmarshal(p.Value, &expected) != nil {
		return false, false
	}
	if p.Operator == "eq" || p.Operator == "ne" {
		equal := actual == expected
		if p.Operator == "ne" {
			equal = !equal
		}
		return equal, true
	}
	a, ok := actual.(float64)
	b, okB := expected.(float64)
	if !ok || !okB {
		return false, false
	}
	switch p.Operator {
	case "gt":
		return a > b, true
	case "gte":
		return a >= b, true
	case "lt":
		return a < b, true
	case "lte":
		return a <= b, true
	}
	return false, false
}

func fact(p contract.RoutingPredicate, f Facts) (any, bool) {
	switch p.Field {
	case "request.model":
		return f.Model, true
	case "request.protocol":
		return f.Protocol, true
	case "request.streaming":
		return f.Streaming, true
	case "request.has_tools":
		if f.HasTools != nil {
			return *f.HasTools, true
		}
	case "request.has_images":
		if f.HasImages != nil {
			return *f.HasImages, true
		}
	case "last.status":
		if f.LastStatus != nil {
			return float64(*f.LastStatus), true
		}
	case "last.error":
		if f.LastError != nil {
			return *f.LastError, true
		}
	case "attempts":
		return float64(f.Attempts), true
	case "quota.exhausted", "quota.used_percent":
		usage, ok := f.Quota[p.ServiceID]
		if !ok || usage.FetchedAt.IsZero() || f.Now.Sub(usage.FetchedAt) > 10*time.Minute || usage.FetchedAt.After(f.Now.Add(time.Minute)) {
			return nil, false
		}
		window := usage.Primary
		if p.Window == "secondary" {
			window = usage.Secondary
		}
		if window == nil {
			return nil, false
		}
		reset := window.ResetAt
		if reset == nil && window.ResetAfterSeconds != nil {
			at := usage.FetchedAt.Add(time.Duration(*window.ResetAfterSeconds) * time.Second)
			reset = &at
		}
		if reset != nil && !f.Now.Before(*reset) {
			return nil, false
		}
		if p.Field == "quota.exhausted" {
			return window.UsedPercent >= 100, true
		}
		return window.UsedPercent, true
	}
	return nil, false
}

type PreviewInput struct {
	Graph    contract.RoutingGraph `json:"graph"`
	EntryID  string                `json:"entry_id"`
	Facts    Facts                 `json:"facts"`
	Outcomes map[string]string     `json:"outcomes"`
}
type Preview struct {
	Steps      []contract.RoutingGraphStep `json:"steps"`
	StopReason string                      `json:"stop_reason"`
	Attempts   int                         `json:"attempts"`
}
