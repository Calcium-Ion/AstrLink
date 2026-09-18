package controlapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
)

const RecoveryPathsPath = "/control/v1/recovery-paths"

func (handler *Handler) registerRecoveryPaths() {
	handler.mux.HandleFunc(RecoveryPathsPath, handler.authenticated(handler.recoveryPathsCollection))
	handler.mux.HandleFunc(RecoveryPathsPath+"/preview", handler.authenticated(handler.previewRecoveryPath))
	handler.mux.HandleFunc(RecoveryPathsPath+"/", handler.authenticated(handler.recoveryPathItem))
}
func (handler *Handler) recoveryPathsCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		records, err := handler.recoveryPaths.ListRecoveryPaths(r.Context())
		if err != nil {
			handler.writeStoreError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": records})
	case http.MethodPost:
		if !requireMediaType(w, r, "application/json") {
			return
		}
		var path contract.RecoveryPath
		if !decodeControlJSON(w, r, &path) {
			return
		}
		if path.ID != "" {
			writeError(w, 422, "invalid_recovery_path", "id is assigned by Core")
			return
		}
		id, err := randomRouteID()
		if err != nil {
			handler.writeStoreError(w, err)
			return
		}
		path.ID = contract.RecoveryPathID(id)
		record, err := handler.recoveryPaths.CreateRecoveryPath(r.Context(), path)
		if err != nil {
			handler.writeStoreError(w, err)
			return
		}
		w.Header().Set("ETag", record.ETag)
		w.Header().Set("Location", RecoveryPathsPath+"/"+string(path.ID))
		writeJSON(w, 201, record)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, 405, "method_not_allowed", "only GET and POST are allowed")
	}
}
func (handler *Handler) recoveryPathItem(w http.ResponseWriter, r *http.Request) {
	id := contract.RecoveryPathID(strings.TrimPrefix(r.URL.Path, RecoveryPathsPath+"/"))
	if id.Validate() != nil {
		writeError(w, 400, "invalid_recovery_path_id", "invalid path id")
		return
	}
	switch r.Method {
	case http.MethodGet:
		record, err := handler.recoveryPaths.GetRecoveryPath(r.Context(), id)
		if err != nil {
			handler.writeStoreError(w, err)
			return
		}
		w.Header().Set("ETag", record.ETag)
		writeJSON(w, 200, record)
	case http.MethodPatch:
		if !requireMediaType(w, r, "application/merge-patch+json") {
			return
		}
		tag := r.Header.Get("If-Match")
		if tag == "" {
			writeError(w, 428, "precondition_required", "If-Match is required")
			return
		}
		var patch map[string]json.RawMessage
		if !decodeControlJSON(w, r, &patch) {
			return
		}
		record, err := handler.recoveryPaths.GetRecoveryPath(r.Context(), id)
		if err != nil {
			handler.writeStoreError(w, err)
			return
		}
		path, err := patchRecoveryPath(record.Path, patch)
		if err != nil {
			writeError(w, 422, "invalid_recovery_path", err.Error())
			return
		}
		record, err = handler.recoveryPaths.UpdateRecoveryPath(r.Context(), path, tag)
		if err != nil {
			handler.writeStoreError(w, err)
			return
		}
		w.Header().Set("ETag", record.ETag)
		writeJSON(w, 200, record)
	case http.MethodDelete:
		tag := r.Header.Get("If-Match")
		if tag == "" {
			writeError(w, 428, "precondition_required", "If-Match is required")
			return
		}
		if err := handler.recoveryPaths.DeleteRecoveryPath(r.Context(), id, tag); err != nil {
			handler.writeStoreError(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		w.Header().Set("Allow", "GET, PATCH, DELETE")
		writeError(w, 405, "method_not_allowed", "only GET, PATCH and DELETE are allowed")
	}
}
func patchRecoveryPath(path contract.RecoveryPath, patch map[string]json.RawMessage) (contract.RecoveryPath, error) {
	if len(patch) == 0 {
		return path, fmt.Errorf("empty patch")
	}
	raw, _ := json.Marshal(path)
	var object map[string]json.RawMessage
	_ = json.Unmarshal(raw, &object)
	for key, value := range patch {
		switch key {
		case "name", "protocol", "mode", "targets", "steps", "strategy", "max_attempts", "failure_policy":
		default:
			return path, fmt.Errorf("unknown field %s", key)
		}
		if isJSONNull(value) {
			delete(object, key)
		} else {
			object[key] = value
		}
	}
	raw, _ = json.Marshal(object)
	var updated contract.RecoveryPath
	if err := strictUnmarshal(raw, &updated); err != nil {
		return path, err
	}
	return updated, updated.Validate()
}
func (handler *Handler) previewRecoveryPath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, 405, "method_not_allowed", "only POST is allowed")
		return
	}
	if !requireMediaType(w, r, "application/json") {
		return
	}
	var input contract.RecoveryPreviewInput
	if !decodeControlJSON(w, r, &input) {
		return
	}
	if input.Path.ID == "" {
		input.Path.ID = "path_preview"
	}
	if err := input.Path.Validate(); err != nil {
		writeError(w, 422, "invalid_recovery_path", err.Error())
		return
	}
	resolver := handler.recoveryResolver
	if resolver == nil {
		writeError(w, 503, "preview_unavailable", "recovery preview is unavailable")
		return
	}
	settings, err := handler.routingSettings.GetRoutingSettings(r.Context())
	if err != nil {
		handler.writeStoreError(w, err)
		return
	}
	// A standalone editor previews a routed path. Passing a route applies its
	// existing exceptions without persisting or editing that route.
	route := &contract.Route{ID: "preview_route", Name: input.Path.Name, Match: contract.RouteMatch{Protocol: input.Path.Protocol, Model: input.Model}}
	if input.RouteID != "" {
		record, e := handler.routeStore.GetRoute(r.Context(), input.RouteID)
		if e != nil {
			handler.writeStoreError(w, e)
			return
		}
		route = &record.Route
		if route.Match.Protocol != input.Path.Protocol {
			writeError(w, 422, "invalid_preview", "protocol mismatch")
			return
		}
	}
	candidates, err := resolver.ResolvePath(r.Context(), contract.RecoveryPathRecord{Path: input.Path}, endpoint.ResolveRequest{Protocol: input.Path.Protocol, Model: input.Model, Streaming: input.Streaming, Category: input.CategoryID}, route, settings)
	if err != nil {
		handler.writeStoreError(w, err)
		return
	}
	preview, err := ingress.PreviewRecovery(r.Context(), candidates, input)
	if err != nil {
		writeError(w, 422, "invalid_preview", err.Error())
		return
	}
	writeJSON(w, 200, preview)
}
