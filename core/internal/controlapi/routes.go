package controlapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type routeCreateRequest struct {
	Name       *string         `json:"name"`
	Enabled    json.RawMessage `json:"enabled,omitempty"`
	Priority   *int            `json:"priority"`
	Match      json.RawMessage `json:"match"`
	Selection  json.RawMessage `json:"selection,omitempty"`
	Targets    json.RawMessage `json:"targets,omitempty"`
	Categories json.RawMessage `json:"categories,omitempty"`
}

type routePageResponse struct {
	Items      []contract.Route `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

func (handler *Handler) registerRouteRoutes() {
	handler.mux.HandleFunc(RoutesPath, handler.authenticated(handler.routeCollection))
	handler.mux.HandleFunc(RoutesPath+"/", handler.authenticated(handler.routeItem))
}

func (handler *Handler) routeCollection(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		handler.listRoutes(writer, request)
	case http.MethodPost:
		handler.createRoute(writer, request)
	default:
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and POST are allowed")
	}
}

func (handler *Handler) routeItem(writer http.ResponseWriter, request *http.Request) {
	rawID := strings.TrimPrefix(request.URL.Path, RoutesPath+"/")
	if rawID == "" || strings.Contains(rawID, "/") {
		writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
		return
	}
	decodedID, err := url.PathUnescape(rawID)
	if err != nil || decodedID != rawID {
		writeError(writer, http.StatusBadRequest, "invalid_route_id", "route_id must use its canonical form")
		return
	}
	id := contract.RouteID(decodedID)
	if err := id.Validate(); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_route_id", "route_id is invalid")
		return
	}
	switch request.Method {
	case http.MethodGet:
		handler.getRoute(writer, request, id)
	case http.MethodPatch:
		handler.patchRoute(writer, request, id)
	case http.MethodDelete:
		handler.deleteRoute(writer, request, id)
	default:
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodPatch+", "+http.MethodDelete)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET, PATCH, and DELETE are allowed")
	}
}

func (handler *Handler) listRoutes(writer http.ResponseWriter, request *http.Request) {
	options, err := parseRouteListOptions(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", "route list query is invalid")
		return
	}
	page, err := handler.routeStore.ListRoutePage(request.Context(), options)
	if err != nil {
		handler.writeRouteStoreError(writer, err)
		return
	}
	response := routePageResponse{Items: make([]contract.Route, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, item.Route)
	}
	if page.NextCursor != "" {
		response.NextCursor = &page.NextCursor
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) createRoute(writer http.ResponseWriter, request *http.Request) {
	if !requireMediaType(writer, request, "application/json") {
		return
	}
	var input routeCreateRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if input.Name == nil || input.Priority == nil || input.Match == nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_route", "name, priority, and match are required")
		return
	}
	id, err := handler.newRouteID()
	if err != nil || id.Validate() != nil {
		writeError(writer, http.StatusInternalServerError, "route_id_unavailable", "could not allocate a route id")
		return
	}
	route, err := routeFromCreate(id, input)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_route", "route violates the current runtime contract")
		return
	}
	record, err := handler.routeStore.CreateRoute(request.Context(), route)
	if err != nil {
		handler.writeRouteStoreError(writer, err)
		return
	}
	writer.Header().Set("Location", RoutesPath+"/"+string(record.Route.ID))
	writer.Header().Set("ETag", record.ETag)
	writeJSON(writer, http.StatusCreated, record.Route)
}

func routeFromCreate(id contract.RouteID, input routeCreateRequest) (contract.Route, error) {
	route := contract.Route{
		ID:       id,
		Name:     *input.Name,
		Enabled:  true,
		Priority: *input.Priority,
	}
	if input.Enabled != nil {
		if isJSONNull(input.Enabled) || strictUnmarshal(input.Enabled, &route.Enabled) != nil {
			return route, fmt.Errorf("enabled must be a boolean")
		}
	}
	if isJSONNull(input.Match) || strictUnmarshal(input.Match, &route.Match) != nil {
		return route, fmt.Errorf("match must be an object")
	}
	if input.Selection != nil {
		if isJSONNull(input.Selection) || strictUnmarshal(input.Selection, &route.Selection) != nil {
			return route, fmt.Errorf("selection must be an object")
		}
	}
	if input.Targets != nil {
		if isJSONNull(input.Targets) || strictUnmarshal(input.Targets, &route.Targets) != nil {
			return route, fmt.Errorf("targets must be an array")
		}
	}
	if input.Categories != nil {
		if isJSONNull(input.Categories) || strictUnmarshal(input.Categories, &route.Categories) != nil {
			return route, fmt.Errorf("categories must be an array")
		}
	}
	if err := route.ValidateForAlpha(); err != nil {
		return route, err
	}
	return route, nil
}

func (handler *Handler) getRoute(
	writer http.ResponseWriter,
	request *http.Request,
	id contract.RouteID,
) {
	record, err := handler.routeStore.GetRoute(request.Context(), id)
	if err != nil {
		handler.writeRouteStoreError(writer, err)
		return
	}
	writer.Header().Set("ETag", record.ETag)
	writeJSON(writer, http.StatusOK, record.Route)
}

func (handler *Handler) patchRoute(
	writer http.ResponseWriter,
	request *http.Request,
	id contract.RouteID,
) {
	if !requireMediaType(writer, request, "application/merge-patch+json") {
		return
	}
	expectedETag := request.Header.Get("If-Match")
	if expectedETag == "" {
		writeError(writer, http.StatusBadRequest, "if_match_required", "If-Match is required")
		return
	}
	current, err := handler.routeStore.GetRoute(request.Context(), id)
	if err != nil {
		handler.writeRouteStoreError(writer, err)
		return
	}
	if current.ETag != expectedETag {
		handler.writeRouteStoreError(writer, storage.ErrPrecondition)
		return
	}
	var patch map[string]json.RawMessage
	if !decodeControlJSON(writer, request, &patch) {
		return
	}
	if len(patch) == 0 {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_route_patch", "route patch must contain at least one property")
		return
	}
	updated, err := applyRoutePatch(current.Route, patch)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_route_patch", "route patch violates the current runtime contract")
		return
	}
	record, err := handler.routeStore.UpdateRoute(request.Context(), updated, expectedETag)
	if err != nil {
		handler.writeRouteStoreError(writer, err)
		return
	}
	writer.Header().Set("ETag", record.ETag)
	writeJSON(writer, http.StatusOK, record.Route)
}

func applyRoutePatch(route contract.Route, patch map[string]json.RawMessage) (contract.Route, error) {
	for name, raw := range patch {
		switch name {
		case "name":
			if isJSONNull(raw) || strictUnmarshal(raw, &route.Name) != nil {
				return route, fmt.Errorf("name must be a string")
			}
		case "enabled":
			if isJSONNull(raw) || strictUnmarshal(raw, &route.Enabled) != nil {
				return route, fmt.Errorf("enabled must be a boolean")
			}
		case "priority":
			if isJSONNull(raw) || strictUnmarshal(raw, &route.Priority) != nil {
				return route, fmt.Errorf("priority must be an integer")
			}
		case "match":
			if isJSONNull(raw) || strictUnmarshal(raw, &route.Match) != nil {
				return route, fmt.Errorf("match must be an object")
			}
		case "selection":
			if isJSONNull(raw) {
				route.Selection = nil
			} else if strictUnmarshal(raw, &route.Selection) != nil {
				return route, fmt.Errorf("selection must be an object or null")
			}
		case "targets":
			if isJSONNull(raw) {
				route.Targets = nil
			} else if strictUnmarshal(raw, &route.Targets) != nil {
				return route, fmt.Errorf("targets must be an array or null")
			}
		case "categories":
			if isJSONNull(raw) {
				route.Categories = nil
			} else if strictUnmarshal(raw, &route.Categories) != nil {
				return route, fmt.Errorf("categories must be an array or null")
			}
		default:
			return route, fmt.Errorf("unknown or immutable route field %q", name)
		}
	}
	if err := route.ValidateForAlpha(); err != nil {
		return route, err
	}
	return route, nil
}

func (handler *Handler) deleteRoute(
	writer http.ResponseWriter,
	request *http.Request,
	id contract.RouteID,
) {
	expectedETag := request.Header.Get("If-Match")
	if expectedETag == "" {
		writeError(writer, http.StatusBadRequest, "if_match_required", "If-Match is required")
		return
	}
	if err := handler.routeStore.DeleteRoute(request.Context(), id, expectedETag); err != nil {
		handler.writeRouteStoreError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func parseRouteListOptions(request *http.Request) (storage.RouteListOptions, error) {
	query := request.URL.Query()
	for name := range query {
		if name != "limit" && name != "cursor" && name != "enabled" {
			return storage.RouteListOptions{}, fmt.Errorf("unknown query parameter")
		}
		if len(query[name]) != 1 {
			return storage.RouteListOptions{}, fmt.Errorf("query parameter must occur once")
		}
	}
	options := storage.RouteListOptions{Cursor: query.Get("cursor")}
	if len(options.Cursor) > 512 {
		return options, fmt.Errorf("cursor too long")
	}
	if value := query.Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 200 {
			return options, fmt.Errorf("invalid limit")
		}
		options.Limit = limit
	}
	if value := query.Get("enabled"); value != "" {
		if value != "true" && value != "false" {
			return options, fmt.Errorf("invalid enabled filter")
		}
		enabled := value == "true"
		options.Enabled = &enabled
	}
	return options, nil
}

func (handler *Handler) writeRouteStoreError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "route not found")
	case errors.Is(err, storage.ErrConflict):
		writeError(writer, http.StatusConflict, "conflict", "route conflicts with current state")
	case errors.Is(err, storage.ErrPrecondition):
		writeError(writer, http.StatusPreconditionFailed, "precondition_failed", "If-Match does not match the current route")
	case errors.Is(err, storage.ErrInvalidCursor):
		writeError(writer, http.StatusBadRequest, "invalid_cursor", "route cursor is invalid")
	case errors.Is(err, storage.ErrInvalidArgument):
		writeError(writer, http.StatusUnprocessableEntity, "invalid_route", "route violates the current runtime or service contract")
	case errors.Is(err, storage.ErrInvalidRecord):
		writeError(writer, http.StatusInternalServerError, "persisted_state_invalid", "persisted route state failed validation")
	default:
		writeError(writer, http.StatusInternalServerError, "storage_unavailable", "persistent route storage is unavailable")
	}
}

func randomRouteID() (contract.RouteID, error) {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return contract.RouteID("route_" + hex.EncodeToString(random[:])), nil
}
