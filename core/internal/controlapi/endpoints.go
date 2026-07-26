package controlapi

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const maxControlBodyBytes = 1 << 20

type credentialInput struct {
	Secret string `json:"secret"`
}

type endpointCreateRequest struct {
	Name         string                 `json:"name"`
	Kind         *contract.EndpointKind `json:"kind"`
	BaseURL      string                 `json:"base_url"`
	Enabled      json.RawMessage        `json:"enabled,omitempty"`
	Auth         json.RawMessage        `json:"auth"`
	Credential   json.RawMessage        `json:"credential,omitempty"`
	Capabilities json.RawMessage        `json:"capabilities"`
}

type endpointAuthInput struct {
	Scheme     *contract.AuthScheme `json:"scheme"`
	HeaderName json.RawMessage      `json:"header_name,omitempty"`
}

type endpointCapabilityInput struct {
	Protocol  *contract.ProtocolID     `json:"protocol"`
	Mode      *contract.CapabilityMode `json:"mode"`
	Streaming *bool                    `json:"streaming"`
	Models    json.RawMessage          `json:"models,omitempty"`
}

type endpointPageResponse struct {
	Items      []contract.Endpoint `json:"items"`
	NextCursor *string             `json:"next_cursor"`
}

func (handler *Handler) registerEndpointRoutes() {
	handler.mux.HandleFunc(EndpointsPath, handler.authenticated(handler.endpointCollection))
	handler.mux.HandleFunc(EndpointsPath+"/", handler.authenticated(handler.endpointItem))
}

func (handler *Handler) authenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		const prefix = "Bearer "
		authorization := request.Header.Get("Authorization")
		provided := []byte("")
		if strings.HasPrefix(authorization, prefix) {
			provided = []byte(strings.TrimPrefix(authorization, prefix))
		}
		if len(provided) != len(handler.controlToken) || subtle.ConstantTimeCompare(provided, handler.controlToken) != 1 {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="astrlink-control"`)
			writeError(writer, http.StatusUnauthorized, "unauthorized", "missing or invalid local control token")
			return
		}
		next(writer, request)
	}
}

func (handler *Handler) endpointCollection(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		handler.listEndpoints(writer, request)
	case http.MethodPost:
		handler.createEndpoint(writer, request)
	default:
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and POST are allowed")
	}
}

func (handler *Handler) endpointItem(writer http.ResponseWriter, request *http.Request) {
	rawID := strings.TrimPrefix(request.URL.Path, EndpointsPath+"/")
	if rawID == "" || strings.Contains(rawID, "/") {
		writeError(writer, http.StatusNotFound, "not_found", "control endpoint not found")
		return
	}
	decodedID, err := url.PathUnescape(rawID)
	if err != nil || decodedID != rawID {
		writeError(writer, http.StatusBadRequest, "invalid_endpoint_id", "endpoint_id must use its canonical form")
		return
	}
	id := contract.EndpointID(decodedID)
	if err := id.Validate(); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_endpoint_id", "endpoint_id is invalid")
		return
	}
	switch request.Method {
	case http.MethodGet:
		handler.getEndpoint(writer, request, id)
	case http.MethodPatch:
		handler.patchEndpoint(writer, request, id)
	case http.MethodDelete:
		handler.deleteEndpoint(writer, request, id)
	default:
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodPatch+", "+http.MethodDelete)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET, PATCH, and DELETE are allowed")
	}
}

func (handler *Handler) listEndpoints(writer http.ResponseWriter, request *http.Request) {
	options, err := parseEndpointListOptions(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", "endpoint list query is invalid")
		return
	}
	page, err := handler.endpointStore.ListEndpoints(request.Context(), options)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	response := endpointPageResponse{Items: make([]contract.Endpoint, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, item.Endpoint)
	}
	if page.NextCursor != "" {
		response.NextCursor = &page.NextCursor
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) createEndpoint(writer http.ResponseWriter, request *http.Request) {
	if !requireMediaType(writer, request, "application/json") {
		return
	}
	var input endpointCreateRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if input.Kind == nil || input.Auth == nil || input.Capabilities == nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_endpoint", "kind, auth, and capabilities are required")
		return
	}
	auth, err := decodeEndpointAuth(input.Auth)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_endpoint", "auth violates the contract")
		return
	}
	capabilities, err := decodeEndpointCapabilities(input.Capabilities)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_endpoint", "capabilities violate the contract")
		return
	}
	id, err := handler.newEndpointID()
	if err != nil || id.Validate() != nil {
		writeError(writer, http.StatusInternalServerError, "endpoint_id_unavailable", "could not allocate an endpoint id")
		return
	}
	enabled := true
	if input.Enabled != nil {
		if isJSONNull(input.Enabled) || strictUnmarshal(input.Enabled, &enabled) != nil {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_endpoint", "enabled must be a boolean")
			return
		}
	}
	endpoint := contract.Endpoint{
		ID: id, Name: input.Name, Kind: *input.Kind, BaseURL: input.BaseURL,
		Auth: auth, Enabled: enabled, Capabilities: capabilities,
	}
	credential := storage.CredentialMutation{}
	if input.Credential != nil {
		if isJSONNull(input.Credential) {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_endpoint", "credential must be an object when provided")
			return
		}
		var credentialValue credentialInput
		if err := strictUnmarshal(input.Credential, &credentialValue); err != nil || credentialValue.Secret == "" {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_endpoint", "credential.secret must not be empty")
			return
		}
		credential.Present = true
		credential.Secret = []byte(credentialValue.Secret)
		defer clear(credential.Secret)
	}
	record, err := handler.endpointStore.CreateEndpoint(request.Context(), endpoint, credential)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	writer.Header().Set("Location", EndpointsPath+"/"+string(record.Endpoint.ID))
	writer.Header().Set("ETag", record.ETag)
	writeJSON(writer, http.StatusCreated, record.Endpoint)
}

func (handler *Handler) getEndpoint(writer http.ResponseWriter, request *http.Request, id contract.EndpointID) {
	record, err := handler.endpointStore.GetEndpoint(request.Context(), id)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	writer.Header().Set("ETag", record.ETag)
	writeJSON(writer, http.StatusOK, record.Endpoint)
}

func (handler *Handler) patchEndpoint(writer http.ResponseWriter, request *http.Request, id contract.EndpointID) {
	if !requireMediaType(writer, request, "application/merge-patch+json") {
		return
	}
	expectedETag := request.Header.Get("If-Match")
	if expectedETag == "" {
		writeError(writer, http.StatusBadRequest, "if_match_required", "If-Match is required")
		return
	}
	current, err := handler.endpointStore.GetEndpoint(request.Context(), id)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	if current.ETag != expectedETag {
		handler.writeStoreError(writer, storage.ErrPrecondition)
		return
	}
	var patch map[string]json.RawMessage
	if !decodeControlJSON(writer, request, &patch) {
		return
	}
	if len(patch) == 0 {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_endpoint_patch", "endpoint patch must contain at least one property")
		return
	}
	endpoint, credential, err := applyEndpointPatch(current.Endpoint, patch)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_endpoint_patch", "endpoint patch violates the contract")
		return
	}
	defer clear(credential.Secret)
	record, err := handler.endpointStore.UpdateEndpoint(request.Context(), endpoint, credential, expectedETag)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	writer.Header().Set("ETag", record.ETag)
	writeJSON(writer, http.StatusOK, record.Endpoint)
}

func (handler *Handler) deleteEndpoint(writer http.ResponseWriter, request *http.Request, id contract.EndpointID) {
	expectedETag := request.Header.Get("If-Match")
	if expectedETag == "" {
		writeError(writer, http.StatusBadRequest, "if_match_required", "If-Match is required")
		return
	}
	if err := handler.endpointStore.DeleteEndpoint(request.Context(), id, expectedETag); err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func applyEndpointPatch(endpoint contract.Endpoint, patch map[string]json.RawMessage) (contract.Endpoint, storage.CredentialMutation, error) {
	credential := storage.CredentialMutation{}
	allowed := map[string]bool{
		"name": true, "kind": true, "base_url": true, "enabled": true,
		"auth": true, "credential": true, "capabilities": true,
	}
	for name, raw := range patch {
		if !allowed[name] {
			return endpoint, credential, fmt.Errorf("unknown field %q", name)
		}
		if isJSONNull(raw) && name != "credential" {
			return endpoint, credential, fmt.Errorf("required endpoint field %q cannot be deleted", name)
		}
	}
	if raw, ok := patch["name"]; ok {
		if err := json.Unmarshal(raw, &endpoint.Name); err != nil {
			return endpoint, credential, err
		}
	}
	if raw, ok := patch["kind"]; ok {
		if err := json.Unmarshal(raw, &endpoint.Kind); err != nil {
			return endpoint, credential, err
		}
	}
	if raw, ok := patch["base_url"]; ok {
		if err := json.Unmarshal(raw, &endpoint.BaseURL); err != nil {
			return endpoint, credential, err
		}
	}
	if raw, ok := patch["enabled"]; ok {
		if err := json.Unmarshal(raw, &endpoint.Enabled); err != nil {
			return endpoint, credential, err
		}
	}
	if raw, ok := patch["auth"]; ok {
		auth, err := decodeEndpointAuth(raw)
		if err != nil {
			return endpoint, credential, err
		}
		endpoint.Auth = auth
	}
	if raw, ok := patch["capabilities"]; ok {
		capabilities, err := decodeEndpointCapabilities(raw)
		if err != nil {
			return endpoint, credential, err
		}
		endpoint.Capabilities = capabilities
	}
	// Parse credential last so an invalid endpoint field never materializes
	// replacement secret bytes in memory.
	if raw, ok := patch["credential"]; ok {
		credential.Present = true
		if isJSONNull(raw) {
			return endpoint, credential, nil
		}
		var input credentialInput
		if err := strictUnmarshal(raw, &input); err != nil {
			return endpoint, credential, err
		}
		if input.Secret == "" {
			return endpoint, credential, fmt.Errorf("credential.secret must not be empty")
		}
		credential.Secret = []byte(input.Secret)
	}
	return endpoint, credential, nil
}

func decodeEndpointCapabilities(raw json.RawMessage) ([]contract.Capability, error) {
	if isJSONNull(raw) {
		return nil, fmt.Errorf("capabilities must be an array")
	}
	var inputs []endpointCapabilityInput
	if err := strictUnmarshal(raw, &inputs); err != nil {
		return nil, fmt.Errorf("decode capabilities: %w", err)
	}
	capabilities := make([]contract.Capability, 0, len(inputs))
	for index, input := range inputs {
		if input.Protocol == nil || input.Mode == nil || input.Streaming == nil {
			return nil, fmt.Errorf("capabilities[%d] must include protocol, mode, and streaming", index)
		}
		var models []string
		if input.Models != nil {
			if isJSONNull(input.Models) {
				return nil, fmt.Errorf("capabilities[%d].models must be an array", index)
			}
			if err := strictUnmarshal(input.Models, &models); err != nil {
				return nil, fmt.Errorf("decode capabilities[%d].models: %w", index, err)
			}
		}
		capabilities = append(capabilities, contract.Capability{
			Protocol:  *input.Protocol,
			Mode:      *input.Mode,
			Streaming: *input.Streaming,
			Models:    models,
		})
	}
	return capabilities, nil
}

func decodeEndpointAuth(raw json.RawMessage) (contract.EndpointAuth, error) {
	if isJSONNull(raw) {
		return contract.EndpointAuth{}, fmt.Errorf("auth must be an object")
	}
	var input endpointAuthInput
	if err := strictUnmarshal(raw, &input); err != nil {
		return contract.EndpointAuth{}, fmt.Errorf("decode auth: %w", err)
	}
	if input.Scheme == nil {
		return contract.EndpointAuth{}, fmt.Errorf("auth.scheme is required")
	}
	auth := contract.EndpointAuth{Scheme: *input.Scheme}
	if input.HeaderName != nil {
		if isJSONNull(input.HeaderName) {
			return contract.EndpointAuth{}, fmt.Errorf("auth.header_name must be a string")
		}
		if err := strictUnmarshal(input.HeaderName, &auth.HeaderName); err != nil {
			return contract.EndpointAuth{}, fmt.Errorf("decode auth.header_name: %w", err)
		}
	}
	if auth.Scheme == contract.AuthSchemeCustomHeader {
		if input.HeaderName == nil {
			return contract.EndpointAuth{}, fmt.Errorf("custom_header auth requires header_name")
		}
	} else if input.HeaderName != nil {
		return contract.EndpointAuth{}, fmt.Errorf("header_name is only valid for custom_header auth")
	}
	if err := auth.Validate(); err != nil {
		return contract.EndpointAuth{}, err
	}
	return auth, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func parseEndpointListOptions(request *http.Request) (storage.EndpointListOptions, error) {
	query := request.URL.Query()
	for name := range query {
		if name != "limit" && name != "cursor" && name != "enabled" && name != "kind" {
			return storage.EndpointListOptions{}, fmt.Errorf("unknown query parameter")
		}
		if len(query[name]) != 1 {
			return storage.EndpointListOptions{}, fmt.Errorf("query parameter must occur once")
		}
	}
	options := storage.EndpointListOptions{Cursor: query.Get("cursor")}
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
	if value := query.Get("kind"); value != "" {
		kind := contract.EndpointKind(value)
		if !kind.Valid() {
			return options, fmt.Errorf("invalid kind filter")
		}
		options.Kind = &kind
	}
	return options, nil
}

func requireMediaType(writer http.ResponseWriter, request *http.Request, expected string) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != expected {
		writeError(writer, http.StatusUnsupportedMediaType, "unsupported_media_type", "request Content-Type is not supported")
		return false
	}
	return true
}

func decodeControlJSON(writer http.ResponseWriter, request *http.Request, target any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, maxControlBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return false
	}
	if err := ensureControlJSONEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain exactly one JSON value")
		return false
	}
	return true
}

func strictUnmarshal(value []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureControlJSONEOF(decoder)
}

func ensureControlJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func (handler *Handler) writeStoreError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "endpoint not found")
	case errors.Is(err, storage.ErrConflict):
		writeError(writer, http.StatusConflict, "conflict", "endpoint conflicts with current state")
	case errors.Is(err, storage.ErrPrecondition):
		writeError(writer, http.StatusPreconditionFailed, "precondition_failed", "If-Match does not match the current endpoint")
	case errors.Is(err, storage.ErrInvalidCursor):
		writeError(writer, http.StatusBadRequest, "invalid_cursor", "endpoint cursor is invalid")
	case errors.Is(err, storage.ErrInvalidArgument):
		writeError(writer, http.StatusUnprocessableEntity, "invalid_endpoint", "endpoint violates the storage contract")
	case errors.Is(err, storage.ErrInvalidRecord):
		writeError(writer, http.StatusInternalServerError, "persisted_state_invalid", "persisted endpoint state failed validation")
	default:
		writeError(writer, http.StatusInternalServerError, "storage_unavailable", "persistent endpoint storage is unavailable")
	}
}

func randomEndpointID() (contract.EndpointID, error) {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return contract.EndpointID("endpoint_" + hex.EncodeToString(random[:])), nil
}
