package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestRoutingGraphDraftApplyConflictAndHistory(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "graph.db"))
	defer store.Close()
	ctx := context.Background()
	initial, err := store.GetRoutingGraph(ctx)
	if err != nil {
		t.Fatal(err)
	}
	graph := contract.RoutingGraph{Nodes: []contract.RoutingGraphNode{{ID: "entry", Kind: "entry", Model: "public", Enabled: true}}, Edges: []contract.RoutingGraphEdge{}}
	draft, err := store.SaveRoutingGraph(ctx, graph, nil, initial.ETag, false)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Revision != 0 || len(draft.Active.Nodes) != 0 {
		t.Fatal("draft became active")
	}
	if _, err = store.SaveRoutingGraph(ctx, graph, nil, draft.ETag, true); !errors.Is(err, storage.ErrInvalidArgument) {
		t.Fatalf("incomplete graph applied: %v", err)
	}
	graph.Nodes = append(graph.Nodes, contract.RoutingGraphNode{ID: "stop", Kind: "stop", Enabled: true})
	graph.Edges = append(graph.Edges, contract.RoutingGraphEdge{ID: "edge", Source: "entry", Port: "next", Target: "stop"})
	if _, err = store.SaveRoutingGraph(ctx, graph, nil, initial.ETag, true); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale graph accepted: %v", err)
	}
	active, err := store.SaveRoutingGraph(ctx, graph, nil, draft.ETag, true)
	if err != nil {
		t.Fatal(err)
	}
	if active.Revision != 1 || len(active.Active.Nodes) != 2 {
		t.Fatal("apply did not atomically publish")
	}
	graph.Nodes[0].Enabled = false
	if _, err = store.SaveRoutingGraph(ctx, graph, nil, active.ETag, true); err != nil {
		t.Fatal(err)
	}
	old, err := store.GetRoutingGraphRevision(ctx, 1)
	if err != nil || !old.Nodes[0].Enabled {
		t.Fatal("historical graph changed")
	}
	graph.Edges = append(graph.Edges, contract.RoutingGraphEdge{ID: "cycle", Source: "stop", Port: "next", Target: "entry"})
	graph.Nodes[0].Enabled = true
	if graph.Validate() == nil {
		t.Fatal("invalid executable edge was accepted")
	}
}
