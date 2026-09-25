package controlapi

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
	"github.com/QuantumNous/astrlink/core/internal/routinggraph"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const RoutingGraphPath = "/control/v1/routing-graph"

func (handler *Handler) routingGraphResource(w http.ResponseWriter, r *http.Request) {
	store, ok := handler.serviceStore.(storage.RoutingGraphStore)
	if !ok {
		writeError(w, 503, "routing_graph_unavailable", "routing graph storage is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if raw := r.URL.Query().Get("revision"); raw != "" {
			revision, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || revision < 1 {
				writeError(w, 400, "invalid_revision", "invalid revision")
				return
			}
			graph, err := store.GetRoutingGraphRevision(r.Context(), revision)
			if err != nil {
				handler.writeStoreError(w, err)
				return
			}
			writeJSON(w, 200, graph)
			return
		}
		doc, err := store.GetRoutingGraph(r.Context())
		if err != nil {
			handler.writeStoreError(w, err)
			return
		}
		w.Header().Set("ETag", doc.ETag)
		writeJSON(w, 200, doc)
	case http.MethodPut:
		if !requireMediaType(w, r, "application/json") {
			return
		}
		tag := r.Header.Get("If-Match")
		if tag == "" {
			writeError(w, 428, "precondition_required", "If-Match is required")
			return
		}
		var input struct {
			Graph  *contract.RoutingGraph  `json:"graph"`
			Layout *contract.RoutingLayout `json:"layout"`
			Apply  *bool                   `json:"apply"`
		}
		if !decodeControlJSON(w, r, &input) {
			return
		}
		if input.Graph == nil || input.Layout == nil || input.Apply == nil {
			writeError(w, 422, "invalid_routing_graph", "graph, layout and apply are required")
			return
		}
		doc, err := store.SaveRoutingGraph(r.Context(), *input.Graph, *input.Layout, tag, *input.Apply)
		if err != nil {
			handler.writeStoreError(w, err)
			return
		}
		w.Header().Set("ETag", doc.ETag)
		writeJSON(w, 200, doc)
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeError(w, 405, "method_not_allowed", "only GET and PUT are allowed")
	}
}

func (handler *Handler) previewRoutingGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, 405, "method_not_allowed", "only POST is allowed")
		return
	}
	if !requireMediaType(w, r, "application/json") {
		return
	}
	var input routinggraph.PreviewInput
	if !decodeControlJSON(w, r, &input) {
		return
	}
	if err := input.Graph.Validate(); err != nil {
		writeError(w, 422, "invalid_routing_graph", err.Error())
		return
	}
	protocol := contract.ProtocolID(input.Facts.Protocol)
	if protocol.Validate() != nil || protocol.IsModelDiscovery() {
		writeError(w, 422, "invalid_protocol", "preview requires an inference protocol")
		return
	}
	var entry *contract.RoutingGraphNode
	for _, node := range input.Graph.Nodes {
		if node.Kind == "entry" && node.ID == input.EntryID {
			copy := node
			entry = &copy
			break
		}
	}
	if entry == nil {
		writeError(w, 422, "invalid_entry", "select a model entry")
		return
	}
	resolver := handler.recoveryResolver
	if resolver == nil {
		var err error
		resolver, err = endpoint.NewStoreResolver(handler.serviceStore)
		if err != nil {
			writeError(w, 503, "preview_unavailable", err.Error())
			return
		}
	}
	plan, err := resolver.PrepareRoutingGraph(r.Context(), input.Graph, *entry, 0, endpoint.ResolveRequest{Protocol: protocol, Model: entry.Model, Streaming: input.Facts.Streaming})
	if err != nil {
		handler.writeStoreError(w, err)
		return
	}
	preview, err := ingress.PreviewRoutingGraph(r.Context(), plan, input)
	if err != nil {
		writeError(w, 422, "invalid_preview", err.Error())
		return
	}
	writeJSON(w, 200, preview)
}
